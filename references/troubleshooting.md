# Troubleshooting — domain-deep failure modes

> Relocated from `CLAUDE.md` to keep the hub lean. These are the
> **service-specific** debugging entries (content-service, forwarder JWT,
> external-ref refresh, Environment access-groups, mcp-echo, Postgres-SoT
> conflicts, image-roll). Generic workflow failure modes (make routing,
> polling loops, missing subcommand, push gates, kubectl context, relative
> paths) stay inline in `CLAUDE.md`.
>
> Add a new `### ❌ ... ✅ ...` entry here when you hit a new
> **service/domain** failure mode; add generic workflow ones to `CLAUDE.md`
> (Documentation-hygiene rule).

### ❌ `downloadUrl` from /platform/hydrate returns 404
```bash
curl https://ach.local.test/content/prompt/foo
# HTTP 404 (or no handler registered at all → chi 404)
```
✅ Confirm the content-service sidecar in the operator Pod is on a
build that includes `internal/contentservice` routes (not the Phase 1
stub). The content-service runs as the second container of the
`ach-operator` Deployment (RWO PVC forces co-location); there is NO
`ach-content-service` Deployment. Use the operator Deployment + the
`content-service` container name when exec'ing:
```bash
kubectl -n ach-system exec deploy/ach-operator \
  -c content-service -- ach content-service --help \
  | grep -q "/content/{prompt,plugin,artifact}"
```
WHY IT FAILS: Pre-`feat/content-service-routes` builds shipped a
`/healthz`-only stub. The Service is healthy, the Pod is Ready, the
hydrate URLs look right — and every GET 404s because the route doesn't
exist. Fix is a rolling image update; no data migration.

### ❌ Forwarder Pod CrashLoopBackOff: `ach-jwt-signing-keys` Secret missing
```bash
kubectl -n ach-system logs deploy/ach-forwarder -c forwarder
# fatal: load JWT signing keys: secret "ach-jwt-signing-keys" not found in namespace "ach-system"
```
✅ The forwarder refuses to start without `ach-jwt-signing-keys`
(FWD-09 — no in-cluster fallback, no implicit zero-key). The Secret
must carry two keys: `current.kid` (short ASCII id) and `current.seed`
(32 random bytes). `scripts/cluster.sh hydrate_fixtures` seeds a fresh
(kid=`dev-<timestamp>`, seed=`openssl rand 32`) pair on every
`cluster.sh up` if the Secret is absent; production deploys must
provision it explicitly (e.g. ExternalSecrets / SealedSecrets — never
the dev seed). Manual seed if you need one:
```bash
jwttmp=$(mktemp -d)
openssl rand 32 > "${jwttmp}/current.seed"
printf 'dev-%s' "$(date +%s)" > "${jwttmp}/current.kid"
kubectl -n ach-system create secret generic ach-jwt-signing-keys \
  --from-file=current.kid="${jwttmp}/current.kid" \
  --from-file=current.seed="${jwttmp}/current.seed"
rm -rf "${jwttmp}"
```
WHY IT FAILS: The forwarder mints the per-request JWT off this seed at
startup; a missing Secret turns the whole JWT trust path unreachable,
which would silently degrade upstream auth. Refusing to start is the
correct posture — fix the seed, not the check.

### ❌ `helm install` aborts: `no matches for kind "LiteLLMConnection"`
```bash
# Error: failed post-install: ... resource mapping not found for name: "default"
#   ... no matches for kind "LiteLLMConnection" in version "ach.ackstorm.ai/v1alpha1"
```
✅ This is fixed — the chart no longer ships the CR. The operator
bootstraps `LiteLLMConnection/default` on boot
(`internal/operator/litellmconnboot.EnsureConnection`, run before
`mgr.Start` like the JWT-key mint). WHY THE CHART CAN'T: ACH CRDs render
as ordinary `templates/` (managed-upgrade strategy, NOT Helm's `crds/`
dir). Helm builds its REST mapper once per action and only refreshes it
after a `crds/` install — never after a `templates/`-rendered CRD. So
within the install release NO manifest (normal template OR post-install
hook) can be mapped to the just-created kind; both fail with the error
above. The operator's live-discovery client has no such limitation.
Config flows in via `litellmConnection.{endpoint,masterKeySecretRef}` →
operator env `ACH_LITELLM_CONNECTION_{ENDPOINT,SECRET_NAME,SECRET_KEY}`
(Secret *reference* only — the master-key value is never put in env). The
bootstrap is idempotent + drift-correcting; `litellmConnection.enabled:
false` (empty endpoint) skips it. If `default` is missing after install,
check `kubectl -n ach-system logs deploy/ach-operator -c operator | grep
litellmconnboot`.

### ❌ "SourceReachable=False reason=Unauthorized" on a public GitHub repo
```bash
kubectl get plugin/caveman -o jsonpath='{.status.conditions[0]}'
# {"reason":"Unauthorized","message":"github: GetCommit 403: sources: unauthorized"}
```
✅ This is GitHub's 60 req/h/IP anonymous rate-limit — NOT a config bug. The
within-interval gate (`shouldSkipFetch` in `internal/controller/ach/external_ref_refresh.go`)
already prevents steady-state burn; a 403 here means a real burst (multiple
`cluster.sh up` cycles, operator restarts, or force-refresh fires within an
hour) has actually exhausted the quota. Either:
  - wait ~1h for the quota window to roll over, OR
  - set `authSecretRef` on a CR whose repo legitimately needs auth, OR
  - investigate why the operator is reconciling more often than expected
    (`kubectl -n ach-system logs deploy/ach-operator | grep -c GetCommit`)

WHY IT FAILS: GitHub rate-limits anonymous REST calls by source IP. The Hub's
default refresh interval is 1h per CR, so legitimate steady-state should
average <5 API calls/h across 3-5 CRs — well below the 60/h ceiling. Hitting
the limit means a tight loop or many cluster rebuilds in the same hour.

**Resolution as of 2026-05-27**: The default outer transport for all three
git source types (`github`, `gitlab`, `bitbucket`) is now `git`
(`FIX_GIT.txt`), which has no per-IP REST rate-limit. If you still see this
error on the default transport, the upstream is genuinely unreachable, the
ref doesn't exist, or git's HTTPS auth-prompt fired (anonymous + a
nonexistent or private repo cannot be told apart by git/HTTPS — both
surface as "please authenticate"). To temporarily revert one CR to the
legacy REST path, set `spec.<github|gitlab|bitbucket>.transport: rest` on
the CR; that path still hits the per-provider anonymous quotas (GitHub
60/h, GitLab 60/min, Bitbucket 60/h) and will be removed one release after
the git transport is observed clean. The transport that actually served
each fetch is now surfaced on the `SourceReachable=True` (Plugin/Prompt/
Artifact) and `Synced=True` (PluginMarketplace) condition messages as
`transport=<git|rest|n/a>`.

**Self-hosted GitLab + git transport (as of 2026-06-04)**: ACH authenticates
GitLab git-smart-http with HTTP Basic `oauth2:<token>` (GitLab's documented
PAT/Group/Project-token method), NOT `Authorization: Bearer`. Self-hosted
instances configured without Bearer support (e.g. `git.example.com`) reject
Bearer with `401 / sources: unauthorized` even when the token, scope, and
project path are all valid. Basic is selected automatically for `gitlab`
source types and for marketplace clones whose host matches the marketplace's
`spec.gitlab.host` — `transport: rest` is NO LONGER required as a workaround
for this 401. `spec.gitlab.host` accepts `git.example.com` or
`https://git.example.com` identically (the clone URL is always https).
(github.com / bitbucket.org still use Bearer.)

### ℹ️ `NameConflict` condition reason — removed in v0.2.5+
`PluginMarketplace` previously set `Synced=False reason=NameConflict` when two
marketplaces exposed a plugin with the same bare name. This reason is **deleted**.
Plugin references in `Environment.spec.context.plugins` are now marketplace-scoped:

- Bare `name` → internal Plugin CRD only.
- `name@marketplace` → that PluginMarketplace's plugin, resolved by exact
  `(marketplace_name, name)` PK. Two marketplaces exposing the same name both
  reach `Synced=True`; no conflict is possible.

Intra-marketplace duplicates (same name listed twice in a single
`marketplace.json`) are soft-skipped: the first entry wins, the rest surface in
the Synced condition `message` with reason `DuplicateName`; `Synced` stays
`True`.

If you see `NameConflict` in `kubectl get pluginmarketplace` output, the cluster
is running a pre-v0.2.5 operator image. Roll to the current image.

### ❌ `plugin <name>: UpstreamInvalid` in a marketplace stage-2 summary
The fetched plugin subtree has **neither** `.claude-plugin/plugin.json` **nor**
any recognized convention component — i.e. the marketplace entry's `source`
points at the wrong path/dir. Note manifest-less plugins ARE accepted as of
`verifyPluginContents` (`internal/controller/ach/marketplace_manifest.go`):
`.claude-plugin/plugin.json` is optional per the Claude Code schema, so a
plugin with only `commands/`/`agents/`/`skills/`/`hooks/`/`output-styles/`/
`themes/`/`monitors/` (or root `SKILL.md`/`.mcp.json`/`.lsp.json`) passes.
`UpstreamInvalid` therefore means none of those were found at the tar root.

`verifyPluginContents` / `verifySkillContents` ALSO walk the WHOLE tar and
reject (same `UpstreamInvalid` reason) any entry the CLI hydrate extractor
refuses under every policy — path traversal (`../`, absolute, `.//../` , `\`,
`C:\`), hardlinks, device/fifo nodes, unknown typeflags, pax-injected paths, or
an out-of-tree symlink target — plus archives over the operator walk caps
(`maxVerifyEntries` / `maxVerifyDecompressedBytes`, a bomb guard). So
`UpstreamInvalid` here means EITHER "no recognized component" OR "the tar
carries an entry/shape hydrate would reject" (F3). In-tree symlinks are allowed.

✅ Verify the entry `source` resolves to the **plugin root** (where the
convention dirs live), not a parent dir or a docs-only directory; and that the
upstream tree contains no unsafe entries. The marketplace stays `Synced=True`;
the good plugins keep serving while the bad entry is reported in
`status.message`.

### ❌ Environment stuck in `AccessGroupSynced=False reason=UnresolvedReferences`
```bash
kubectl get environment demo -n ach-system -o jsonpath='{.status.conditions[?(@.type=="AccessGroupSynced")]}'
# {"type":"AccessGroupSynced","status":"False","reason":"UnresolvedReferences",
#  "message":"unresolved: mcpServers=[demo-mcp] a2aAgents=[] authorizedTeams=[]"}
```
✅ The named MCP server / A2A agent / authorized team does not exist in
LiteLLM. The reconciler resolves names on each reconcile via
`ListMCPServers` / `ListA2AAgents` / `ListTeamsByAlias`; any unresolved
entry flips the condition with the offending list in the message.

Register the missing resource(s):
```bash
# MCP server
kubectl -n litellm-system exec deploy/litellm -c litellm -- \
  curl -sf -X POST http://localhost:4000/v1/mcp/server \
    -H 'Authorization: Bearer sk-test-master-key' \
    -d '{"server_name":"<name>","transport":"http","url":"http://<addr>"}'

# A2A agent
kubectl -n litellm-system exec deploy/litellm -c litellm -- \
  curl -sf -X POST http://localhost:4000/v1/agents \
    -H 'Authorization: Bearer sk-test-master-key' \
    -d '{"agent_name":"<name>","agent_card_params":{"name":"<name>","url":"<addr>"}}'

# Team
kubectl -n litellm-system exec deploy/litellm -c litellm -- \
  curl -sf -X POST http://localhost:4000/team/new \
    -H 'Authorization: Bearer sk-test-master-key' \
    -d '{"team_alias":"<alias>"}'
```
The next reconcile (or any spec-change touch) re-runs the resolvers and
the condition flips to `True/Synced`.

WHY IT FAILS: the legacy `POST /access_group/new` rejected empty
`model_names` (issue #17). The `/v1/access_group` endpoint accepts
empty resource sets, but every ID in `access_mcp_server_ids` /
`access_agent_ids` / `assigned_team_ids` must exist upstream. The
reconciler converts names → IDs on-demand each reconcile (no
Snapshotter cache), so the condition reflects fresh upstream state.

### ❌ Hydrate output ≠ examples/hydrate.json ✅ Normalize golden against cluster base URL
```bash
./bin/ach-cli env hydrate demo > /tmp/hydrate-test.json
diff -u /tmp/hydrate-test.json examples/hydrate.json
# --- /tmp/hydrate-test.json
# +++ examples/hydrate.json
# -        "downloadUrl": "https://kind.cluster.local/content/prompt/..."
# +        "downloadUrl": "http://localhost:8080/content/prompt/..."
```
✅ The golden at `examples/hydrate.json` is stored against the literal base
URL the standard kind+Helm fixture emits — `http://localhost:8080`, the
`ACH_BASE_URL` the `ach-local-gateway` serves (set in
`test/e2e/cluster/02-ach/ach.values.yaml`). When the kept cluster exposes the
platform-api on a different externally-visible base (a custom Ingress, a
different DNS name, an https prod host, etc.), the raw `diff -u` flags every
`downloadUrl`/`endpoint` row even though the response shape is byte-identical.

Two remedies, in preference order:

1. **Normalize the golden in-test** (CLI e2e suite does this automatically):
   the canonical helper is `phase6NormalizeHydrate(golden, liveBaseURL)` in
   `test/e2e/phase6_helpers_test.go`, which rewrites the golden's stored base
   URL (`http://localhost:8080`, scheme + host) to the live cluster's base.
   It is a no-op on the standard fixture; it only does real work against an
   exotic-host cluster (set `ACH_E2E_PHASE6_BASE_URL`). The `TestPhase6CLI`
   umbrella's `hydrate_golden_diff` subtest uses it. Manual repro:
   ```bash
   sed 's#http://localhost:8080#<your-cluster-base-url>#g' examples/hydrate.json | \
     diff -u - /tmp/hydrate-test.json
   ```
2. **Re-capture the golden** against the standard fixture (only when the
   response shape legitimately changed — new field, schemaVersion bump, an
   Environment fixture edit):
   ```bash
   ./bin/ach-cli env hydrate demo > examples/hydrate.json
   git diff examples/hydrate.json   # audit — should be the intended shape change
   ```

WHY IT FAILS: the hydrate command emits the response body verbatim via
`io.Copy` (no re-encoding); the only intentional cross-cluster transform is
the base URL (scheme + host) on each `downloadUrl`/`endpoint`. A raw
byte-for-byte compare without normalization trips on any cluster topology
that isn't the standard `http://localhost:8080` fixture — and the failure
mode is identical to a real schema drift (`schemaVersion` bump, new field,
etc.), so documenting the gotcha protects future debuggers from chasing a
phantom regression.

### ❌ `ach-cli env hydrate` exits 1 "environment is required" ✅ pass the positional `<name>` (engine namespaces state by env)
The hydrate ENGINE namespaces its `<ach-dir>` by Environment in BOTH scopes
(`<cwd>/.ach/<environment>/` in project scope, `$HOME/.ach/<environment>/` under
`--global`) per CLI spec §8.1. So the positional `<name>` (or `ACH_ENVIRONMENT`) is
REQUIRED for any engine run — including the `ek_` credential path, which used to
treat it as optional. `--raw` is exempt (it short-circuits before the engine).

Two Environments hydrated into ONE project therefore get isolated caches +
state (`.ach/<envA>/`, `.ach/<envB>/`) and DON'T clobber each other. NOTE: the
adapter-native projection (`.claude/`, `.codex/`, …) is single-path by
construction — agents read fixed config locations — so two Environments UNION in
the same `.claude/` (each surgically tracked + independently uninstallable via
`ach-cli env uninstall <env>`).

A pre-namespacing flat `<cwd>/.ach/state.json` is auto-migrated into
`.ach/<env>/` on the next hydrate (a one-line stderr `notice:`). `ach-cli env status`
with no `--environment` (project scope) enumerates ALL `.ach/<env>/` so a
multi-env project lists every installed set.

`.ach/<env>/runtime/{mcp,a2a,model}.json` are credential-free snapshots of the
Environment's runtime block (the bearer is injected ONLY into the adapter
config, never the cache) — recorded in `state.runtimeFiles` so `--sync` /
uninstall and drift cover runtime entries (incl. models, which have no adapter
destination).

### ❌ ach-mcp-echo returns 401 invalid_token from /mcp/demo-mcp-echo
```bash
curl -i -H 'Authorization: Bearer pk_demo' https://forwarder.local/mcp/demo-mcp-echo
# HTTP/1.1 401 Unauthorized
# WWW-Authenticate: Bearer error="invalid_token"
```
✅ The mcp-echo backend (issue #35) cryptographically verifies the JWT
against the forwarder's JWKS. A 401 here means one of:
- The BIP `bip-demo-mcp-echo` is missing or `Synced=False` (forwarder
  did not promote to JWT-mint; backend sees the raw `pk_` instead of
  a JWT). Check `kubectl -n ach-system get bip bip-demo-mcp-echo -o yaml`.
- The forwarder's `ACH_BASE_URL` does not match the backend's
  `ACH_EXPECTED_ISS` (Helm `testMocks.mcpEcho.expectedIss`). The
  `iss` claim must match exactly.
- The minted JWT's `aud=mcp:<name>` does not match
  `testMocks.mcpEcho.expectedAud`. The route name and the audience
  expectation must agree.
- The backend's JWKS cache hasn't refreshed since a forwarder rotation.
  Restart `deploy/ach-mcp-echo` to force a clean re-fetch.
- The LiteLLM MCP server registration is missing
  `extra_headers: ["authorization"]`. LiteLLM acts as an MCP gateway
  in front of the backend; by default it DROPS the caller's
  Authorization header before forwarding. Without the opt-in, the
  backend never sees the JWT and 401s every call (visible upstream
  as `tools=[]` on `tools/list`). `scripts/cluster.sh hydrate_fixtures`
  registers `demo-mcp-echo` with this opt-in already in place; users
  registering their own backends MUST do the same.

WHY IT FAILS: The verifier is intentionally strict — the trust path is
only meaningful if the backend refuses on the slightest mismatch. Fix
the configuration, not the verifier.

### ❌ `POST /mcp/<name>` 404s while LiteLLM serves it directly
```
{"msg":"platform-api: access","method":"POST","path":"/mcp/vmcp-zoho","status":404,"latency_ms":0}
```
Two red herrings make this one confusing. (1) The `platform-api: access`
label does NOT mean platform-api handled it — `AccessLog`
(`internal/platformapi/middleware`) is shared middleware the forwarder
reuses verbatim, so a forwarder 404 still logs that label. (2) The gateway
routes `/mcp/` to the forwarder correctly (`internal/gateway/routes.go`);
the request reaches the forwarder.
✅ The tell is `latency_ms:0` with a 404: chi rejected the path at the
ROUTER, before precheck/JWT/LiteLLM ran (a real LiteLLM 404 has non-zero
latency; a precheck miss is 403, not 404; a missing Environment is a 404
but carries a JSON `environment_not_found` envelope, not chi's plain
`404 page not found`). The forwarder registers BOTH `/mcp/{name}` and
`/mcp/{name}/*` (`internal/forwarder/server.go`) precisely because chi
(no `RedirectSlashes`) will not match a slash-less `/mcp/<name>` against a
`/{name}/*`-only route. If a bare `/mcp/<name>` 404s, that bare route
regressed — `TestRouteAcceptsBareAndSubpathNames` guards it.
WHY IT FAILS: hydrate writes the endpoint as the bare `…/mcp/<name>`
(`internal/platformapi/hydrate/handler.go`); MCP clients POST exactly that.
A `/{name}/*`-only table drops it at the router. Same applies to `/a2a/<name>`.

### ❌ LiteLLM 401 on `/v1` or `/mcp` for a key that used to work
After migration `000011` the forwarder authenticates to LiteLLM with the
**caller's own** virtual key (`litellm_key_material`), not the master key
(TESTING-PHASE reversal of FIX01 §A.6). A key minted **before** `000011` has a
NULL `litellm_key_material`, so the forwarder sends an empty `x-litellm-api-key`
(`Bearer ` on `/mcp`) and LiteLLM rejects it `401`.
✅ Re-mint the pk_/ek_ (`ach-cli login` again, or recreate the env-key) — the
fresh key persists its material. There is **no master fallback** by design.
WHY IT FAILS: only mint (`platformapi/auth/sso.go`, `envkeys/handler.go`) writes
the column; existing rows are not backfilled (the one-time `sk-…` is gone). Note
the master key is still used for the teams precheck (`/user/info`) and for
operator/platform-api admin — just never in the proxied data path.

### ❌ Operator condition: `Synced=False reason=ConflictWithUIRow`
A CR's projection collides with a row created by the UI (`origin='ui'`). The
operator refuses to clobber the UI-managed row.
✅ Rename the CR, or delete the UI row from Postgres before letting the
operator reconcile. UI and CR row names must be disjoint within a (namespace).
WHY IT FAILS: every projection table (environments, plugins, prompts, artifacts,
litellm_connections, backend_identity_policies, external_refs, marketplace_plugins)
has an `origin TEXT CHECK IN ('cr','ui')` column. The operator's UPSERTs are
guarded by `ON CONFLICT (...) DO UPDATE ... WHERE existing.origin = 'cr'`; the
filter miss returns `pgx.ErrNoRows` which the helper maps to `ErrOriginConflict`,
which the reconciler maps to `Synced=False reason=ConflictWithUIRow` and a 1-min
requeue.

### ❌ orphan-cleanup revoked a LiteLLM key it should not have (or revoked nothing)
The operator's orphan-cleanup loop (`internal/orphan`, OP-15 / Hub §18.4)
revokes **only** ACH-minted keys that ACH no longer tracks as active. A key is
ACH-minted iff its LiteLLM `/key/list` metadata carries `ach_key_id` (set at
mint in `sso.go` / `envkeys/handler.go`); that id joins against the active
`key_id` set (`db.ListActiveACHKeyIDs`). Foreign keys (manual dashboard, `tf-*`,
token-factory — no `ach_key_id`) are **never** touched.
✅ Diagnose with the audit/metrics, then tune via env (operator-only, set through
Helm `extraEnv`):
```bash
make logs-operator | grep -E "orphan-cleanup: (revoked|WOULD revoke|WARNING)|skipped_"
# /metrics: ach_orphan_cleanup_candidates_total / _revoked_total /
#           _skipped_total{reason=dry_run|empty_active_set|circuit_breaker|...}
```
- **Emergency stop (no rebuild):** `ACH_ORPHAN_CLEANUP_INTERVAL=8760h` — the
  loop has no initial tick, so a year-long interval never fires for that pod.
- **Reversible neutralize (B3):** `ACH_ORPHAN_CLEANUP_DRY_RUN=true` — logs
  `WOULD revoke` + `skipped{dry_run}`, never calls RevokeKey.
- **Circuit-breaker (B2):** `ACH_ORPHAN_CLEANUP_MAX_REVOKE` (default 10) — a tick
  with more true-orphan candidates than the cap aborts revocation entirely and
  emits `outcome=skipped_circuit_breaker`.
- **Empty-active-set fail-safe (B1):** if the active key set is empty while
  ACH-owned candidates exist (the mis-wire shape), the tick skips with
  `outcome=skipped_empty_active_set` rather than revoking the fleet.
WHY IT MATTERED: the original loop joined the opaque LiteLLM `token` against the
`key_id` set — two non-intersecting namespaces — so every key older than the
10-min floor under a managed user was mis-classified orphan and revoked. The
ownership gate + B1/B2 guards make a future mis-wire observable and bounded.

NOTE (`ACH_ORPHAN_CLEANUP_DRY_RUN`): a malformed value (`tru`, `yes`, `on`)
**fails operator startup** — it is parsed with `MustEnvBool`, not the
silent-fallback `EnvBool`, so a typo can never quietly re-enable real
revocation. Under dry-run the B1/B2 guards still emit per-key `WOULD revoke` +
`skipped{dry_run}` so you can inspect exactly the batches they would abort.

KNOWN LIMITATION (orphan backstop coverage): the loop only checks users with an
**active** ACH row (`ListACHManagedLitellmUsers` filters `status='active'`). A
LiteLLM key whose owner's last ACH row is already `revoked` — e.g. a DB-side
revoke whose LiteLLM-side `/key/delete` failed — is NOT re-checked by later
ticks. If you see a stale upstream key for a fully-revoked user, delete it
manually (it 401s anyway); a widened-enumeration backstop is a tracked
follow-up, not part of PR #119.

### ❌ Code change rebuilt but the old container keeps serving after `cluster-up`
```bash
# edit Go code → make cluster-up → behavior unchanged; pod AGE is hours old
kubectl -n ach-system get pod -l app.kubernetes.io/component=platform-api
# ach-platform-api-... 1/1 Running 0 48m   ← NOT restarted
```
✅ `scripts/cluster.sh hydrate_ach` rebuilds + `kind load`s the image under
the **fixed** tag `:e2e` with `pullPolicy=IfNotPresent`. A rebuilt image
carries the same tag, so a plain `helm upgrade` renders a byte-identical
Deployment spec and does NOT roll the pods — kubelet keeps the stale image
running. `hydrate_ach` defeats this by passing
`--set-string image.rebuildId=$(date +%s)`: the chart renders that value
into an `ach.ackstorm.ai/rebuild-id` pod annotation on every ach
Deployment, so the podTemplate changes each run and **Helm rolls all pods
in the same upgrade** onto the freshly node-resident `:e2e` layer. The
value defaults to `""` (annotation omitted) so **production never rolls
pods spuriously**. If you redeploy by some other path (raw `helm upgrade`,
manual image rebuild), either pass the same `--set-string` or force the
roll yourself:
```bash
for d in ach-operator ach-platform-api ach-forwarder; do
  ./scripts/dev.sh kubectl -n ach-system rollout restart deploy/"$d"
done
```
WHY IT FAILS: `IfNotPresent` + a non-unique tag means the only signal that
would trigger a rollout (a changed podTemplate) never changes across
rebuilds. The image content under `:e2e` is new, but nothing tells kubelet
to recreate the pod. `image.rebuildId` is the prod-safe knob that makes the
podTemplate differ per build; a unique per-build image **tag** would also
work but leaves orphan images on the node.

### ❌ E2E capture test fails with `/__capture/reset: ... EOF`; mock container crashed
```bash
# e.g. TestPhase4Invariants/SC2_EkTagInjection:
#   phase4_invariants_test.go:163: POST /__capture/reset: ... EOF
./scripts/dev.sh kubectl -n ach-system get pod -l app.kubernetes.io/component=mock-model \
  -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}{"\n"}'   # ≥ 1
./scripts/dev.sh kubectl -n ach-system logs <pod> --previous   # panic / fatal error
```
An `EOF` on a freshly-established port-forward almost always means the
**backend container died**, not a test-logic bug. `ach-mock` and `ach-mcp-echo`
are applied via kustomize (stage-03 backends, NOT the ach Helm chart), so the
`image.rebuildId` roll above does **not** touch them — and the deeper trap is
the image **build**, not the rollout: a stale `COPY` layer can rebuild the mock
to a **byte-identical** pre-fix image, so `kind load` reports *"already present
… skipping"* and the old (crashing) binary keeps serving even after a pod roll.
This once shipped a pre-fix `ach-mock` binary that crashed with
`fatal error: sync: unlock of unlocked mutex` on every `/__capture/reset`.
✅ Confirm the crash from `--previous` logs, then force a clean rebuild and roll:
```bash
docker build --no-cache -t ach-mock:e2e -f test/e2e/mock/Dockerfile .
kind load docker-image ach-mock:e2e --name ach-e2e
./scripts/dev.sh kubectl -n ach-system rollout restart deploy/ach-mock-model
```
The structural fix is already in: both mock Dockerfiles use **explicit
allow-list `COPY`** (never `COPY . .`), so a source edit busts the layer cache
deterministically. Re-validate with `make e2e-run` (NOT `e2e-full`, whose
`cluster-up` rebuild could re-cache a stale layer on an unchanged tree).
