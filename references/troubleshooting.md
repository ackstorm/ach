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
>
> **⚠ Plugin / PluginMarketplace failure modes are INACTIVE** in the current
> build: `featuregate.PluginsEnabled = false` (`internal/featuregate`) gates off
> the plugin CRDs, reconcilers, content-service `/content/plugin/{name}` serve,
> and admin inventory. So the plugin-specific entries below — content-service
> 404 for plugins, `SourceReachable=False` on a `plugin/...`, the
> `UpstreamInvalid` marketplace stage-2 summary, and the `NameConflict` /
> marketplace-scoped-ref notes — **cannot fire** as shipped; they document the
> re-enabled (`PluginsEnabled=true` + `make helm-sync`) build. **`Skill` /
> `SkillMarketplace` troubleshooting is fully live and unaffected.**

### ❌ `downloadUrl` from /platform/hydrate returns 404
```bash
curl https://ach.local.test/content/prompt/foo
# HTTP 404 (or no handler registered at all → chi 404)
```
✅ Confirm the content-service sidecar in the operator Pod is on a
build that includes `internal/contentservice` routes (not the Phase 1
stub). **By default** (`contentService.standalone=false`) the
content-service runs as the second container of the `ach-operator`
Deployment (RWO PVC forces co-location); there is NO
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

> **HA / standalone split (G16).** The default single-replica content
> path is co-located with the operator, so content availability is tied
> to the operator Pod and cannot scale independently. To split it into
> its own N-replica `ach-content-service` Deployment, set
> `contentService.standalone=true` **and** give the cache an RWX class:
> `operator.cache.accessMode=ReadWriteMany` +
> `operator.cache.storageClassName=<efs-sc|nfs-client|cephfs>`. In that
> mode the sidecar drops out, the `ach-content-service` Service selector
> points at the new Deployment, and the operator Pod remains the sole
> *writer* of the cache (content-service mounts it readOnly). Exec into
> `deploy/ach-content-service` (not the operator Pod) when standalone.
> RWX is required because both Pods mount the same PVC concurrently; the
> kind/e2e cluster ships only RWO, so standalone is validated by
> `helm template` + a real RWX cluster, not the local e2e suite.

### ❌ `/content/{prompt,artifact}/…` 404s right after deploying the uniform-`.tar.gz` build
```bash
curl .../content/prompt/foo   # HTTP 404 content_not_found
ls $ACH_CACHE_ROOT/prompt/    # foo  (bare, no .tar.gz)
```
✅ Force a FULL re-fetch of each Prompt/Artifact (`scope=object`) so the
operator rewrites the cache file as `<name>.tar.gz`. As of the
uniform-context-format change, Prompt and Artifact `scope=object` are
published as a 1-entry gzip tar (`prompt/<name>.tar.gz`,
`artifact/<name>.tar.gz`) — the same shape as skill/plugin/directory-
artifact — and `ResolvePath` / `contentTypeFor` serve them as
`application/gzip`. The harness and `ach-cli` already untar every kind
with `mode="r:gz"`, so they extract into a directory
`<kind>/<name>/<source-basename>`. (skills + directory-artifacts already
ship `.tar.gz`, so they need NO migration.)

⚠ **A force-refresh annotation / Admin-API refresh / plain resync does
NOT migrate it.** Those re-run the fetch but still send the stored
upstream SHA as `PriorRev`, so the fetcher returns `NotModified`
(`external_ref_refresh.go` Step 4) and the helper returns BEFORE staging
— the bare file is never rewritten. `PriorRev` is cleared (forcing a real
body) ONLY on a **`metadata.generation` bump**
(`external_ref_driver.go` — the F1 spec-change clause) or a
delete/recreate. So the migration needs a real spec change:
```bash
# generation bump → clears PriorRev → full re-fetch → writes <name>.tar.gz
kubectl -n ach patch prompt   <name> --type=merge -p '{"spec":{"refresh":{"interval":"2h"}}}'
kubectl -n ach patch artifact <name> --type=merge -p '{"spec":{"refresh":{"interval":"2h"}}}'  # scope=object only
# (or kubectl delete + re-apply the CR). Then verify on the PVC:
kubectl -n ach-system exec deploy/ach-operator -c content-service -- \
  ls $ACH_CACHE_ROOT/prompt/ $ACH_CACHE_ROOT/artifact/   # expect *.tar.gz
```
WHY IT FAILS: after the rollout, `ResolvePath` looks for `<name>.tar.gz`
while the old **bare** file still sits there → 404 `content_not_found`.
It does NOT self-heal on resync — the conditional-fetch `NotModified`
shortcut skips the rewrite until a generation bump forces a full
re-fetch. **Orphan bare files**: a live CR's old bare `prompt/<name>` /
`artifact/<name>` is NOT auto-removed (the name changed) — the
finalizer's legacy-bare cleanup only fires on CR *delete*. One-time
housekeeping after all CRs re-fetch (optional; NOT scripted into the
operator — YAGNI):
```bash
find $ACH_CACHE_ROOT/{prompt,artifact} -maxdepth 1 -type f ! -name '*.tar.gz' -delete
```

### ❌ UI edit to an Environment returns 403 `immutable_via_ui` (or my draft "vanished" after `kubectl apply`)
```bash
curl -X PATCH .../platform/objects/environments/demo -d '{"spec":{...}}'
# HTTP 403 {"code":"immutable_via_ui", ...}
```
✅ Working as designed — **GitOps-wins (G2)**. The UI Objects API
(`/platform/objects`, Environment only in v1) writes `origin='ui'` DRAFT rows.
The operator is always authoritative: when a CR with the same `(namespace,name)`
is applied it TAKES OVER the row (`origin` flips `ui`→`cr`, `locked=TRUE`) and
the UI can no longer modify it — PATCH/DELETE return `403 immutable_via_ui`,
POST returns `409 conflict_with_kubernetes_object`. Manage operator-owned
(`origin='cr'`) objects via Git/`kubectl`, not the UI. A UI draft you exported
(`GET /platform/objects/environments/<name>/yaml`) and applied is now
operator-owned — that is the intended round-trip, not data loss. To re-open an
object for UI editing, `kubectl delete` the CR (the operator removes the row);
a fresh UI POST then creates a new `origin='ui'` draft.
WHY: a UI draft has NULL status conditions (not `Available`), so hydrate will
not serve it until the operator reconciles the applied CR. Set
`ACH_DISABLE_UI_WRITES=true` to disable the write path entirely
(every write → `403 ui_writes_disabled`).

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

### ❌ platform-api / forwarder CrashLoopBackOff: `ACH_KEY_ENCRYPTION_KEY` missing/invalid (G3)
```bash
kubectl -n ach-system logs deploy/ach-platform-api
# validateConfig: ACH_KEY_ENCRYPTION_KEY is not set
# (or) ACH_KEY_ENCRYPTION_KEY still holds the placeholder value
# (or) ACH_KEY_ENCRYPTION_KEY decoded to 16 bytes: must be 32 bytes
```
✅ Both platform-api and forwarder require the AES-256 DEK that seals
LiteLLM virtual-key material at rest (G3 — same hard-requirement posture
as the credential-hash pepper, `ACH_CREDENTIAL_HASH_PEPPER`). The Helm chart
injects BOTH env vars into platform-api + forwarder from the top-level
`security` block (`security.keyEncryptionKey.secretRef` +
`security.credentialHashPepper.secretRef`, via the `ach.securityEnv` helper) —
but it never CREATES the Secrets, because they must stay stable across
upgrades. Provision them out-of-band before install/upgrade (defaults:
`ach-key-encryption-key`/`dek`, `ach-credential-hash-pepper`/`pepper`):
`kubectl create secret generic ach-key-encryption-key --from-literal=dek="$(openssl rand -base64 32)"`
(base64 of **exactly 32 bytes**) and likewise the pepper Secret. The kind/e2e
harness seeds fixed dev values in `test/e2e/cluster/02-ach/secrets/`. WHY:
`dekenv.Load` rejects an unset value, the `REPLACE-ME-WITH-RANDOM-`
placeholder, and any value that does not decode to 32 bytes — encryption is
always on, there is no plaintext fallback. (Do NOT also set these in
`extraEnv` — the chart now sources them; a duplicate env entry results.)

### ❌ Upstream 401 + forwarder logs `key material decrypt failed` (G3)
```bash
kubectl -n ach-system logs deploy/ach-forwarder -c forwarder | grep 'key material decrypt failed'
```
✅ The forwarder decrypts the sealed LiteLLM key material per request; on a
decrypt failure it forwards **no** key, so LiteLLM returns 401. Cause is
either a **DEK mismatch** (platform-api sealed under a different
`ACH_KEY_ENCRYPTION_KEY` than the forwarder holds) or a **legacy/plaintext
row** (a `pk_`/`ek_` minted before migration 000014, whose material was
nulled). Fix: ensure platform-api and forwarder mount the SAME DEK Secret,
then **re-mint** the affected key (`ach-cli login` / `ach-cli keys
create`, or the back-compat alias `ach-cli env-keys create`). The log never prints the material or the DEK.

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
# {"reason":"Unauthorized","message":"github: unreachable: ..."}
```
✅ `github`/`gitlab`/`bitbucket` sources fetch exclusively over git (git
ls-remote + git clone; no REST call, no per-IP REST rate-limit — the legacy
`spec.<provider>.transport: rest` escape hatch was removed). A failure here
means the upstream is genuinely unreachable, the ref doesn't exist, or git's
HTTPS auth-prompt fired (anonymous + a nonexistent or private repo cannot be
told apart by git/HTTPS — both surface as "please authenticate"). Either:
  - set `authSecretRef` on a CR whose repo legitimately needs auth, OR
  - verify `spec.ref` actually exists on the upstream, OR
  - investigate why the operator is reconciling more often than expected
    (`kubectl -n ach-system logs deploy/ach-operator | grep -c "fetch:"`)

The transport that served each fetch is surfaced on the `SourceReachable=True`
(Plugin/Prompt/Artifact) and `Synced=True` (PluginMarketplace) condition
messages as `transport=<git|n/a>`.

**Self-hosted GitLab (as of 2026-06-04)**: ACH authenticates GitLab
git-smart-http with HTTP Basic `oauth2:<token>` (GitLab's documented
PAT/Group/Project-token method), NOT `Authorization: Bearer`. Self-hosted
instances configured without Bearer support (e.g. `git.example.com`) reject
Bearer with `401 / sources: unauthorized` even when the token, scope, and
project path are all valid. Basic is selected automatically for `gitlab`
source types and for marketplace clones whose host matches the marketplace's
`spec.gitlab.host`. `spec.gitlab.host` accepts `git.example.com` or
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
`ListMCPServers` / `ListA2AAgents` / `ListAllTeams`; any unresolved
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

### Team has zero models/MCP tools although the access group lists it

LiteLLM stores the team↔access-group relation twice: `access_group.assigned_team_ids`
(`GET /v1/access_group`) and `team.access_group_ids` (`GET /v2/team/list`). Grants are
enforced off the **team** side. A manual team edit or a partial write can leave the two
disagreeing — the group lists the team, the team lists no group, and the team's keys
resolve zero tools.

Check both sides:

```bash
curl -sH "Authorization: Bearer $LITELLM_MASTER_KEY" "$LITELLM/v1/access_group" \
  | jq '.[] | {access_group_name, assigned_team_ids}'
curl -sH "Authorization: Bearer $LITELLM_MASTER_KEY" "$LITELLM/v2/team/list?page_size=100" \
  | jq '.teams[] | {team_alias, access_group_ids}'
```

LiteLLM's mirror is **delta-driven**: it rewrites `team.access_group_ids` only for teams that
enter or leave `assigned_team_ids`. Re-PUTting the same list changes nothing, which is why the
pre-v0.6.16 operator could not have repaired this even if it had noticed.

Since v0.6.16 the Environment reconciler compares both sides every pass and repairs a divergent
mirror with two PUTs — first `assigned_team_ids` **without the drifted teams** (and with any
stale foreign team temporarily added), then the desired list — so the required deltas fire while
co-authorized healthy teams stay in both calls and never lose access for an instant. Log line:
`access group team mirror drifted; running delta repair`.

If the mirror still diverges after the sequence, the Environment goes
`AccessGroupSynced=False / MirrorUnconverged` and further repairs are suppressed until the spec
changes — deliberately loud, so a LiteLLM semantics change surfaces as a broken Environment
rather than an endless write loop. Investigate LiteLLM itself in that case; the operator has done
all it can.

### ❌ A deleted Environment's `ek_` still answers 200 for up to a minute

Only possible if the shell team (`ach-env-<env>`) was deleted BEFORE its keys —
by hand, or by a build predating v0.6.17. LiteLLM caches key rows for ~60s, and
inside that window the key cannot be revoked by any route: `POST /key/delete`
(by `keys` and by `tokens`), `POST /key/block` and `POST /key/update` all return
404 while the key still serves traffic. The key keeps the deleted team's
restrictions, so it is a revocation-LATENCY problem, not privilege escalation.

There is no fix from outside — wait out the cache. Never read those 404s as
"already revoked": the operator logs `ek_ absent in LiteLLM at revoke time` only
for keys it confirmed gone BEFORE touching the team.

Since v0.6.17 the finalizer order is: revoke every `ek_` → delete the access
group → delete the shell team, so the window does not occur on a normal delete.

### ℹ️ An `ek_` created before v0.6.17 can still reach models its Environment never granted

Keys minted before the shell-team change carry no `team_id`. A LiteLLM key with
no team is fail-open on models — the access group cannot narrow it (MCP and
agents DO scope correctly even then). They were deliberately NOT migrated.

Fix per key: `ach-cli keys revoke <ekid_…>` then `ach-cli keys create`. The new
key is minted into `ach-env-<env>` and capped correctly. To find them:

    psql "$ACH_DB_URL" -c \
      "SELECT key_id, environment, owner_email, created_at FROM environment_keys
        WHERE status='active' AND created_at < '2026-07-21' ORDER BY created_at"

### ℹ️ A fresh `pk_` reaches nothing for up to a few minutes after first login

Expected. `mintAndPersistPK` provisions the caller's `ach-user-<email>` shell
and mints the key into it immediately, but the shell carries NO grants of its
own — the operator is the sole writer of `assigned_team_ids` and only attaches
the shell to an entitled Environment's access group on that Environment's
NEXT reconcile pass. A brand-new pk_ is therefore briefly fail-closed (reaches
nothing), not fail-open. One-time per user; resolves itself on the next
Environment reconcile (well under the default `wait-*` timeouts).

### ℹ️ A `pk_` minted before the per-user shell change can reach everything

The mirror image of the `ek_` case above: pre-change PKs carry `team_id=NULL`
and `expires:None` — deliberately NOT migrated (spec decision iii). A teamless
key is fail-open on models. Fix per key: have the user re-login via
`ach-cli login` / the SSO flow to mint a fresh, properly-shelled `pk_`, then
revoke the old one (`ach-cli keys revoke <pkid_…>` or an admin force-revoke).

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
REQUIRED for any single-env engine run — including the `ek_` credential path, which
used to treat it as optional. Two exemptions: `--raw` (it short-circuits before the
engine), and a committed `ach.yaml` — a **bare** `ach-cli env hydrate` (no `<name>`,
no `ACH_ENVIRONMENT`) reads `ach.yaml` and hydrates each listed Environment
best-effort (exit ≠0 if any fails). Create it with `ach-cli env save`.

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

### ❌ `ach-cli env hydrate` (bare, via ach.yaml) → an env "not found" though it exists
`ach.yaml` is **hub-agnostic** — it lists Environment names only, never which
hub they live on. Bare `env hydrate` resolves the hub from your **active
profile**. If your active profile points at a different hub than the one where
those Environments live, the names won't resolve and you get a per-env
`FAIL: … not found` in the summary (best-effort: other envs still hydrate).
Fix: point your active profile at the right hub (`ach-cli config use <profile>`
or `ach-cli login` against the correct hub), then re-run `ach-cli env hydrate`.

### ❌ `ach-cli login` fails `synthetic mode is half-set` after `export ACH_BASE_URL=…` ✅ use ACH_PLATFORM_URL to pre-fill the URL prompt
`ACH_BASE_URL` is NOT a login-URL prefill — it is the **synthetic/headless mode
switch** (CLI spec §3.3). Set it WITH a credential (`ACH_API_KEY` or
`--api-key`) and the CLI runs server-mediated commands off env vars, no disk
config — but `login` is REFUSED in synthetic mode regardless. Set it ALONE (URL,
no credential) and you hit the half-set hard-error before any command runs:
```
synthetic mode is half-set: ACH_BASE_URL is set but no credential resolved …
```
To pre-fill the interactive `ach login` URL prompt, use **`ACH_PLATFORM_URL`** —
a login-only convenience (precedence: `--base-url` flag → `ACH_PLATFORM_URL` env
→ prompt). The two are deliberately distinct vars:

| Var | Job | Effect on `login` |
|-----|-----|-------------------|
| `ACH_PLATFORM_URL` | pre-fill the login URL prompt | prompt suggests the URL; profile saved to disk |
| `ACH_BASE_URL` (+ `ACH_API_KEY`) | synthetic/headless mode (no disk config) | REFUSED (`not available in synthetic mode`) |
| `ACH_BASE_URL` alone | — (misconfiguration) | half-set hard error, exit 1 |

Fix: `unset ACH_BASE_URL; export ACH_PLATFORM_URL=https://hub.example.com; ach-cli login`.

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

### ❌ Cost stuck at 0 under `litellm_usage`

Check `/v2/model/info` reachability through the gateway. A control plane
predating `/v2` forwarding returns 404 at the edge, so the cost source cannot
read the model information it needs.

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

### ❌ LiteLLM 500 `MCP request failed` / `Only proxy admin … Route=/mcp/<n>/`
The forwarder now sends the caller's **non-admin** virtual key (post-`000011`),
and LiteLLM's MCP gateway guards it two ways (full analysis: jwt-forwarder.md
§2.6.1):
1. **Admin-only route.** `mcp_inference_routes` only whitelists the single-segment
   `/mcp/{subpath}` for non-admin keys. A trailing slash (`/mcp/<n>/`) or deeper
   subpath (`/mcp/<n>/mcp`) misses it → `_raise_admin_only_route_exception` →
   `500`. ✅ The Director collapses the upstream path to the bare `/mcp/<server>`
   (`mcpServerPath`, `proxy.go`); hydrate already writes the bare URL. If a `500`
   reappears, confirm the Director normalization is intact
   (`TestDirector_McpPathNormalizedToBareServer`).
2. **Object-permission** (`200` body `"User not allowed to call this tool"`). The
   key isn't granted the server. ✅ Register the MCP server with
   `allow_all_keys: true` (the e2e seed in `scripts/cluster.sh` does this). WHY:
   `proxy_admin` (the old master path) bypassed both guards; a per-user key needs
   the bare route AND an explicit grant. The backend identity still rides the JWT.

### ❌ Duplicate MCP servers / ambiguous `/mcp/<name>` routing
`POST /v1/mcp/server` is **not** idempotent — each call mints a new `server_id`,
so re-running `cluster-up`/`cluster-sync` piled up duplicate `demo-mcp-*` rows
with the same `server_name` (LiteLLM then routes to an arbitrary one). ✅ The
seed in `scripts/cluster.sh` now `DELETE`s every existing row for the name
(`jq` over the `GET /v1/mcp/server` array) before re-creating it. If you see
stale duplicates from an older run, delete them by `server_id` and re-sync.

### ❌ Transient MCP `tools/call` 200 `isError: Tool '<tool>' not found`
A proxied `tools/call` returns **HTTP 200** with an `isError` result body
`{"content":[{"type":"text","text":"Error: Tool 'echo' not found"}],"isError":true}`
for a tool that demonstrably exists (mcp-echo registers `echo` statically at
boot). Not the `tools=[]` auth-drop misconfig above — the SAME call succeeds a
few seconds later. ✅ It is LiteLLM's **MCP tool-discovery warmup window**: right
after `cluster-up` rolls the litellm chart, LiteLLM has not yet run `tools/list`
against a freshly-(re)registered MCP server, so its tool registry can't resolve
the (server-prefix-stripped) tool name. The error string is LiteLLM's, not
mcp-go's. Drive the echo through a bounded retry that re-issues the whole
`tools/call` until it round-trips — `callEchoViaForwarderEventually`
(`test/e2e/phase4_bip_loop_test.go`) is the canonical helper; a single-shot
forwarder→LiteLLM echo (e.g. `TestPhase4JWTValidate/ViaForwarder_PkRoundTrip`)
is exposed to the same race. WHY: discovery is async/periodic, so the very first
`tools/call` after a roll races it; it is NOT a JWT/BIP-precedence signal.

### ❌ Operator condition: `Synced=False reason=ConflictWithUIRow`
**Dormant / unreachable in v1alpha1.** The v1alpha1 write path is **GitOps/CRD
only** — there is no UI write path, so `origin='ui'` rows are never produced and
this condition cannot fire in practice. The machinery is **reserved** for the
future UI Objects API (#34, whose promotion half is unbuilt).
✅ N/A in v1alpha1 — there is nothing to fix; you will not encounter this. If you
somehow see it, an `origin='ui'` row was written by hand into Postgres (the
control plane never does this) — remove that row.
WHY (reserved-machinery background): every projection table (environments,
plugins, prompts, artifacts, litellm_connections, backend_identity_policies,
external_refs, marketplace_plugins) carries an `origin TEXT CHECK IN ('cr','ui')`
column. The operator's UPSERTs are guarded by
`ON CONFLICT (...) DO UPDATE ... WHERE existing.origin = 'cr'`; a filter miss
returns `pgx.ErrNoRows`, which the helper maps to `ErrOriginConflict` and the
reconciler maps to `Synced=False reason=ConflictWithUIRow` with a 1-min requeue.
Until a UI write path ships, that guard simply never trips.

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

### ❌ `ach-cli keys revoke <ekid_>` → `502 litellm_rejected` (key deleted in LiteLLM out-of-band)
ek_ revoke is **LiteLLM-first** (KEY-08): the ACH row flips only after LiteLLM
acks the `/key/delete`. If you deleted the ek_'s LiteLLM virtual key directly
(LiteLLM UI, raw `POST /key/delete`, or `ek save`-style tooling) instead of
through `ach-cli keys revoke`, LiteLLM no longer has the token → future
`/key/delete` returns **404 `Key not found.`**. The ACH row is untouched, so
the `ek-…` **still authenticates to ACH** (hydrate/reads work) while every model
+ MCP call 401s at LiteLLM — a desync between ACH and LiteLLM.
✅ The revoke handler now treats that 404 as **idempotent success** (the
upstream credential is already gone → the barrier's goal is met) and proceeds to
the DB flip, so a plain `ach-cli keys revoke <ekid_>` finishes cleanly and
returns 204. Any OTHER LiteLLM error (503 unreachable, 401/403 rejected) still
fails closed — the row stays `active` so a retry is safe.
WHY IT MATTERED: before this, a 404 was classified as a hard rejection → the row
could never be revoked via the CLI, leaving a live-in-ACH but dead-in-LiteLLM
key with no self-service recovery (needed a manual DB `UPDATE`). Root fix:
`litellm.IsHTTPNotFound` + the 404 branch in `revokeEnvironmentKey`
(`envkeys/handler.go` step 5). **Always** revoke via `ach-cli keys revoke`, not
directly in LiteLLM, to keep the two sides in sync in the first place.

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

### ❌ First SSO login returns `500 default_team_missing`
A login that arrives BEFORE the operator's first successful `EnsureDefaultTeam`
(operator just started / `LiteLLMConnection` not yet `Ready` / LiteLLM briefly
unreachable) can 500 with audit `outcome=default_team_missing`.
✅ It SELF-HEALS — the next LiteLLMConnection reconcile creates the canonical
`default` team (idempotent list-first → `POST /team/new`), and subsequent logins
succeed. No manual action needed; the operator bootstraps the team proactively
(`litellmconnection_controller.go`), so in steady state it is present before any
login. WHY: ACH does NOT lazily create the default team in the SSO path — that is
a fail-loud signal (`auth/doc.go`) — but the operator's proactive bootstrap closes
the gap once the control plane converges. The 500 is transient, not a dead-end.
