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
./bin/ach-cli hydrate --environment demo > /tmp/hydrate-test.json
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
   ./bin/ach-cli hydrate --environment demo > examples/hydrate.json
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
