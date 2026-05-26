# ACH — Post-bootstrap follow-ups

Tracker for non-blocking work after bootstrap release `v0.1.0`. Each item is self-contained, agent-actionable.

---

## 1. Hardening batch (small)

**Scope**: fix the 3 known smoke-verification failures from Phase 16.

**Files**:
- `scripts/cluster.sh` — postgres image tag pin
- `docs/Makefile` — nested-docker mount path translation
- `.golangci.yml` and/or scaffold code — outstanding deprecations

**Acceptance criteria**:
- `./scripts/dev.sh make cluster-up && make cluster-status && make cluster-down` end-to-end PASS with hydrated postgres/valkey/dex/litellm/toolhive
- `./scripts/dev.sh make docs-build` PASS (or document host-only path explicitly + remove from `./scripts/dev.sh` smoke list)
- `./scripts/dev.sh make lint` exit 0 with zero exclusions added beyond the current SA1019 one
- `make pre-push` GREEN with zero warnings (or 1, max — re-evaluate gate 11 self-match exclusions)

**Specific fixes**:

### 1a. postgres bitnami tag pruned upstream
- **Symptom**: kind cluster-up fails because `docker.io/bitnami/postgresql:16.4.0-debian-12-r14` is `NotFound` (Bitnami pruned)
- **Options**: (a) bump to latest published Bitnami tag, (b) mirror to `ghcr.io/ackstorm/mirror/postgresql`, (c) switch to `cloudnative-pg` or upstream postgres image
- **Recommend**: option (b) — Bitnami's image retention policy will keep pruning; mirror once + control rotation
- **Audit also**: valkey, redis, any other Bitnami pins in `scripts/cluster.sh` + `deploy/helm/ach/values.yaml`

### 1b. docs-build nested-docker mount
- **Symptom**: `./scripts/dev.sh make docs-build` invokes a docker container that mounts `$(abspath docs/..)` which resolves to `/workspace` inside devtools, then the mount uses host docker socket → host has no `/workspace`
- **Options**: (a) translate mount path back to host via env var `HOST_PWD`, (b) refuse to run `docs-build` inside devtools and require host invocation, (c) use `docs-build-local` (python3) as devtools fallback
- **Recommend**: option (a) — pass `HOST_PWD=$(pwd)` from `dev.sh` into devtools env; docs-build uses `$(HOST_PWD)` for nested mount

### 1c. lint deprecations
- **Already added**: `SA1019` exclusion for `scheme.Builder` in `api/*`
- **Audit other warnings**: run `./scripts/dev.sh make lint` (full output) and triage anything else introduced by go 1.26 / k8s 0.36 / controller-runtime 0.24.1 churn

---

## 2. Domain port from ach-old

**Scope**: lift business logic from `/home/jcm/Projects/ach-old/` into bootstrap shell.

**Files (read-only sources)**:
- `/home/jcm/Projects/ach-old/api/ach/v1alpha1/*_types.go` — real Spec/Status fields for 6 CRDs
- `/home/jcm/Projects/ach-old/internal/controller/*_controller.go` — reconciler logic
- `/home/jcm/Projects/ach-old/internal/{audit,cachefs,config,connection,credhash,db,keys,keystore,litellm,orphan,platformapi,snapshot,sources}/` — domain packages
- `/home/jcm/Projects/ach-old/cmd/{operator,platform-api,forwarder,content-service,ach,migrate}/main.go` — entrypoint wiring
- `/home/jcm/Projects/ach-old/db/migrations/` — golang-migrate SQL files

**Targets in /home/jcm/Projects/ach/**:
- `api/ach/v1alpha1/*_types.go` — replace placeholder `Foo string` fields with real Spec/Status
- `internal/controller/ach/*_controller.go` — real Reconcile logic
- `internal/{audit,cachefs,config,connection,credhash,db,keys,keystore,litellm,orphan,platformapi,snapshot,sources}/` — port packages
- `cmd/ach/cmd/{operator,platform_api,forwarder,content_service,migrate}.go` — wire real subcommands (replace stub Println with manager.Start / chi.Serve / etc.)
- `db/migrations/` — SQL files

**Order**:
1. CRD types (smallest unit, unblocks DeepCopy regen)
2. `internal/db/` (Postgres models + queries) — needed by everything else
3. `internal/keystore/` + `internal/keys/` + `internal/credhash/` — auth foundations
4. `internal/connection/` + `internal/litellm/` — LiteLLM client
5. `internal/controller/ach/` reconcilers
6. `cmd/ach/cmd/operator.go` — wire manager.Start
7. `internal/platformapi/` + `cmd/ach/cmd/platform_api.go` — REST surface
8. `internal/sources/` + `internal/cachefs/` + `cmd/ach/cmd/content_service.go` — artifact streaming
9. `cmd/ach/cmd/forwarder.go` — MCP/A2A forwarding
10. `cmd/ach/cmd/migrate.go` + `db/migrations/` — DB schema

**Acceptance**: `./scripts/dev.sh make envtest-fast` + `./scripts/dev.sh make e2e-full` green with real reconcilers. UAT-Phase3-style scenario from `ach-old/scripts/uat-phase3.sh` passes.

**Caveats**:
- ach-old uses Ginkgo for some tests; we have Ginkgo on envtest+e2e — should slot in cleanly
- ach-old has `internal/toolhive/` references that conflicted with envtest-fast (removed in T9.1) — restore if porting toolhive integration
- Single-binary cobra layout: ach-old's `cmd/operator/main.go` content goes into `cmd/ach/cmd/operator.go` `RunE` (not a separate main)

---

## 3. Multi-component Helm templates

**Scope**: current `deploy/helm/ach/templates/install.yaml` renders a single Deployment (operator-only baseline inherited from alitellm). `values.yaml` already exposes 5 per-mode toggles (operator, platformApi, forwarder, contentService, migrate). Templates need to consume them.

**Files**:
- `deploy/helm/ach/templates/operator-deployment.yaml` (new)
- `deploy/helm/ach/templates/platform-api-deployment.yaml` (new)
- `deploy/helm/ach/templates/forwarder-deployment.yaml` (new)
- `deploy/helm/ach/templates/content-service-deployment.yaml` (new)
- `deploy/helm/ach/templates/migrate-job.yaml` (new — Job, not Deployment)
- `deploy/helm/ach/templates/_helpers.tpl` (extend with per-mode labels/serviceAccount selectors)
- `deploy/helm/ach/templates/{service,rbac,sa}-*.yaml` per-mode resources
- Remove or refactor: `deploy/helm/ach/templates/install.yaml` (monolith from alitellm)

**Pattern**: each Deployment template references `{{ .Values.image.repo }}:{{ .Values.image.tag }}` (single image) with `args: {{ .Values.<mode>.args | toYaml }}` (cobra subcommand). Gated by `{{- if .Values.<mode>.enabled }}`.

**Sync source-of-truth**: `make helm-sync` regenerates from kustomize. Either (a) author Helm templates directly and skip kustomize-to-helm, or (b) extend `config/deployments/` kustomize bases for all 5 modes and let `kustomize-to-helm.sh` regenerate.

**Recommend**: (a) — single-binary makes per-mode templates trivial; keep kustomize for k8s-native users; Helm chart authored independently.

**Acceptance**:
- `helm template deploy/helm/ach --set operator.enabled=true --set platformApi.enabled=true` renders 2 Deployments
- `helm template deploy/helm/ach --set migrate.enabled=true` renders 1 Job + the default Deployments
- `helm lint deploy/helm/ach` PASS
- Install into kind cluster end-to-end via `helm install ach deploy/helm/ach --namespace ach-system --create-namespace`

---

## 4. Sync-back PR to alitellm-operator ✅ STARTED

**Scope**: open a PR against `/home/jcm/Projects/alitellm-operator/` containing the real-bug fixes + hardening improvements ACH developed during bootstrap.

**Briefing files staged at**: `/home/jcm/Projects/alitellm-operator/SYNC-FROM-ACH/`

See `/home/jcm/Projects/alitellm-operator/SYNC-FROM-ACH/README.md` for the agent prompt + per-fix diffs.

**Acceptance**:
- Branch on alitellm-operator: `sync-from-ach-2026-05-25`
- Single commit per fix (or one bundled commit per category)
- PR description references ACH commit SHAs
- All fixes pass alitellm's own `make pre-push` + CI

---

## 5. PluginMarketplace schema mismatch — re-model to upstream

**Status**: discovered during end-to-end demo (2026-05-26). Partial work in tree (see "Current state" below). Do NOT patch with a tolerance shim — re-model to the real upstream schema in one pass.

### The problem

Our `PluginMarketplace` CRD reconciler parses a `marketplace.json` document that we modeled with our own internal schema. The real upstream schema (used by `anthropics/claude-plugins-official`, the canonical Claude Code marketplace) is materially different. We discovered three layered bugs while running `examples/hydrate-demo.sh` against the live cluster:

1. **F.1 — outer-fetch shape mismatch**
   GitHub/GitLab/Bitbucket fetchers return the WHOLE repo tarball (Path-subset extraction is deferred to v1beta1 per `internal/sources/github/fetcher.go:18-20`). The Plugin reconciler ships that tarball as `plugin/<name>.tar.gz`. The PluginMarketplace reconciler used to feed the tarball bytes directly to `json.Unmarshal` → `invalid character '\x1f'` (gzip magic). Examples/05 worked around this by using `type: http` against `raw.githubusercontent.com`, which dodges GitHub auth + the tarball wrapper.

2. **F.2a — string-vs-object union**
   The real schema allows `"source": "./plugins/agent-sdk-dev"` (bare string for local-subdir) AND `"source": {...}` (object form). Our parser's `ClaudeCodeMarketplacePlugin.Source` field is a struct, so the bare-string case fails outright with `cannot unmarshal string into Go struct field`.

3. **F.2b — wrong discriminator + wrong enum**
   Real object form uses `source.source` as the discriminator (not `source.type`), with values `git-subdir` and `url`. Each entry also carries `url`, `path`, `ref`, `sha` fields pointing at an arbitrary external git repo (NOT the marketplace's own repo). Our parser models a closed enum `{github, gitlab, bitbucket, s3, gcs, http, npm}` with nested subobjects matching our internal `achv1alpha1.*Source` types. Real upstream entries trigger our parser's `default:` case at `internal/controller/ach/marketplace_parse.go:159` → aborts the whole marketplace.

### Real upstream schema (verbatim sample)

Fetch sample: `https://raw.githubusercontent.com/anthropics/claude-plugins-official/main/.claude-plugin/marketplace.json`

```json
{
  "$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
  "name": "claude-plugins-official",
  "owner": {"name": "Anthropic", "email": "support@anthropic.com"},
  "plugins": [
    {
      "name": "42crunch-api-security-testing",
      "description": "...",
      "author": {"name": "42Crunch"},
      "category": "security",
      "source": {
        "source": "git-subdir",
        "url": "https://github.com/42Crunch-AI/claude-plugins.git",
        "path": "plugins/api-security-testing",
        "ref": "v1.5.5",
        "sha": "a175b24f7b34852b70c78c21545cce8037eb3112"
      },
      "homepage": "https://42crunch.com"
    },
    {
      "name": "agent-sdk-dev",
      "description": "...",
      "source": "./plugins/agent-sdk-dev",
      "category": "development"
    },
    {
      "name": "aikido",
      "source": {
        "source": "url",
        "url": "https://github.com/AikidoSec/aikido-claude-plugin.git",
        "sha": "79ac524f87c9faa9a356ff3d495b8a5b77e01bbd"
      }
    }
  ]
}
```

Key facts:
- `source` is a union: object OR string
- Object form: `source.source` ∈ `{git-subdir, url}` (NOT our `type` enum)
- `git-subdir`: clone the repo, materialize ONLY the subtree at `path`
- `url`: clone the repo, materialize the WHOLE tree
- `sha` is authoritative for conditional-fetch (PriorRev comparison)
- `ref` is the human-readable branch/tag, used for the initial clone
- Plugin entries point at ARBITRARY external git repos (any host with a git remote — not just GitHub)

### Mental model (confirmed with user)

A `Plugin` CR is a unitary fetch — one source, one tarball output. A `PluginMarketplace` CR is a CATALOG of plugin pointers — the outer fetch reads the catalog file, then EACH entry triggers a SEPARATE per-entry fetch against potentially a different upstream repo. Marketplace plugins are usually external; the marketplace's own repo typically does NOT host the plugin bodies.

### Why patch (A) was rejected

A pure tolerance shim (custom `UnmarshalJSON` on `ClaudeCodeMarketplaceSource` to accept both forms, route unknown discriminators to a sentinel) would let the parser stop crashing on the real schema, but every entry would resolve to `ReasonUnsupportedPluginSource`. The demo would project zero plugins forever until B lands anyway. Two passes of churn for one functional result. User directive (2026-05-26): "do things right, not patch."

### Plan B — full re-model

Single-pass refactor. Files touched (~8):

**Parser + types** — `internal/controller/ach/marketplace_parse.go`
- Replace `ClaudeCodeMarketplaceSource` struct with a normalized form:
  ```go
  type ClaudeCodeMarketplaceSource struct {
      // Wire-form discriminator. After UnmarshalJSON:
      //   "git-subdir" → URL + Path + Ref + SHA populated
      //   "url"        → URL + Ref + SHA populated, Path == ""
      //   "local-path" → Path populated (bare-string form, relative to marketplace repo)
      Kind string

      URL  string // git remote, e.g. https://github.com/foo/bar.git
      Path string // subdirectory inside the repo (or local-path body)
      Ref  string // branch/tag — initial clone target
      SHA  string // pinned commit — authoritative for conditional-fetch
  }
  ```
- Add `UnmarshalJSON` that handles three cases: bare string → `Kind=local-path`, object with `source.source=="git-subdir"`, object with `source.source=="url"`. Anything else → `Kind=""` and parser flips per-entry to `ReasonUnsupportedPluginSource`.
- DROP our six-source-discriminator inner enum + `npm` carve-out (legacy fiction).
- DNS-1123 name validation stays.
- KEEP outer-tarball extraction (`marketplace_extract.go`, already in tree).

**Stage-2 dispatch** — `internal/controller/ach/pluginmarketplace_controller.go`
- `materializeMarketplacePlugin` calls a new `gitClone(url, ref, sha)` helper instead of dispatching via our `registry.For`.
- Per-entry SHA used as `PriorRev` — `git fetch origin <sha>` short-circuits when local already at `<sha>`.
- Subtree extraction: for `git-subdir`, post-clone, tar only `path/...`; for `url`, tar the whole worktree minus `.git/`.
- For `local-path` (bare-string form): clone the MARKETPLACE's own repo (we already have its `spec.<type>` source), tar the subtree at `path` relative to repo root.

**New fetcher package** — `internal/sources/git/`
- Generic git-remote clone. Does NOT use GitHub API (works for self-hosted gitea, gitlab, any git remote with anonymous-or-token auth).
- `git clone --depth=1 --branch=<ref> <url> <dst>` then `git -C <dst> fetch --depth=1 origin <sha>` + `git -C <dst> checkout <sha>`.
- Auth: `https://<token>:x-oauth-basic@host/...` URL rewrite when token supplied; `~/.netrc` ALSO works.
- Output: streaming tar.gz of the worktree (or subtree).
- Security: clone into ephemeral dir under `${CacheRoot}/.tmp/git-<random>`; bounded clone size cap; never follow `--recurse-submodules` (submodule URLs are upstream-controlled, T-02-06-x parity).
- Tests: fixture remote via `git init --bare` in a tmpdir.

**Spec changes** — none. `PluginMarketplaceSpec` still references the catalog's OWN location via our 6-source-type discriminator (`github`/`gitlab`/`bitbucket`/`s3`/`gcs`/`http`). The new git fetcher is INNER-ONLY (per-entry materialization).

**Registry** — `internal/sources/registry/registry.go`: register the new git fetcher under `Type: "git"` so the inner dispatch can use the same factory pattern. The OUTER catalog fetch keeps using the existing github/gitlab/bitbucket/etc. dispatch.

**Envtests** — `internal/controller/ach/pluginmarketplace_envtest_test.go`
- Rewrite `mkGithubPlugin` / `mkNpmPlugin` test helpers to produce real-schema entries (object form with `source.source=="url"` / `source.source=="git-subdir"`).
- Drop the npm-specific test; replace with `local-path` and `unsupported-discriminator` cases.
- Marketplace conflict tests stay mostly untouched (operate on plugin NAMES, not source shape).

**Marketplace parse tests** — `internal/controller/ach/marketplace_parse_test.go`
- Re-author fixtures using real-upstream JSON shape.
- Add fixtures from `anthropics/claude-plugins-official` (a trimmed snapshot, NOT the full 200+ entries — keep ~5 representative).

**Documentation**
- `api/ach/v1alpha1/pluginmarketplace_types.go` — update doc comment to describe the inner schema.
- `CLAUDE.md` "Repository-specific patterns" — add a note clarifying inner-vs-outer fetch.
- Delete F.1 + F.2 from `FIX01.md` (resolved by this work).

**Examples**
- `examples/05-pluginmarketplace-anthropic.yaml`: rewrite from `type: http` to `type: github` with `repo: anthropics/claude-plugins-official`, `ref: main`. Drop the raw.githubusercontent.com URL hack. `filters.include: ["^code-.*"]` stays.
- `examples/hydrate-demo.sh`: should "just work" after re-model — the apply list already references 05.
- `examples/README.md`: refresh the "What's here" table description for 05.

### Codex parser audit findings (fold into this refactor)

Codex CLI ran `gsd-reviewer`-style audit on `marketplace_parse.go` (2026-05-26). Findings to address while re-modeling:

- HIGH: `:128` plugin name echoed in errors → truncate at 64 chars
- HIGH: `:159` `source.type` echoed unbounded → truncate
- HIGH: `conditions.go:128` `status.message` has no 253-char cap → truncate at write
- MED: `:122` `plugins[]` count unbounded → enforce max (suggested: 5000)
- MED: `:117` duplicate JSON keys accepted → not fixable with stdlib (note in code comment, don't block)
- MED: `:117` no streaming → stdlib `json.Decoder` could stream but the body is already capped at 5 MiB by `marketplaceJSONMaxBytes`; keep as-is
- MED: `:90` regex misses 63-char per-label cap → split labels and check len
- MED: `pluginmarketplace_controller.go:198` `LimitReader` truncates silently at 5 MiB → detect overflow by attempting one extra read after LimitReader EOF, return ErrUpstreamInvalid if body exceeded
- LOW: `:117` unknown JSON fields accepted → KEEP (Anthropic schema has `$schema`, `author`, `category`, `homepage` we don't model; strict mode would reject forward-compat fields)
- LOW: missing test fixtures for `duplicate-key`, `64-char-label`, `oversized plugins[]` → add

### Current state (partial work in tree)

The following files were touched on branch `fix/post-bootstrap-hardening` BEFORE deciding to do B:

- `internal/controller/ach/marketplace_extract.go` (NEW) — gzip+tar walker that extracts `*/.claude-plugin/marketplace.json` from a repo tarball. Correct + needed regardless of A/B. Keep.
- `internal/controller/ach/pluginmarketplace_controller.go` — body-reshape branch added: git-tarball source types call `extractMarketplaceJSON` before parse, s3/gcs/http stay as direct body read. Correct + needed. Keep.
- `api/ach/v1alpha1/pluginmarketplace_types.go` — CRD doc comment updated to describe tarball-extraction behavior. Correct. Keep.

These three changes are NOT yet committed. They are necessary precursors to B (the outer fetch path must extract the JSON from the tarball regardless of the inner-entry schema). Either commit them as a small "outer-fetch extraction" prep commit, or fold into the B branch.

### Acceptance criteria

1. `./scripts/dev.sh make unit` PASS
2. `./scripts/dev.sh make envtest-fast` PASS with re-authored fixtures
3. `examples/hydrate-demo.sh` end-to-end against live cluster:
   - PluginMarketplace `anthropic-official` reaches `Synced=True`
   - At least one inner plugin (e.g. one matching `^code-.*`) is materialized as a `marketplace_plugins` row with a real `storage_location` tarball on the operator cache PVC
   - hydrate.json contains the marketplace-sourced plugin in `context.plugins[]` alongside the standalone `caveman` Plugin
4. Adversarial cases handled gracefully:
   - Inner entry pointing at a non-existent repo → per-entry `ReasonNotFound`, marketplace stays `Synced=True` with `status.message` listing the failure
   - Inner entry with malformed `source.url` → per-entry `ReasonUnsupportedPluginSource`
   - Inner entry with bare-string `source` ("local-path") that escapes the marketplace repo root (`../../etc/passwd`) → REJECTED at parse time
5. `examples/05-pluginmarketplace-anthropic.yaml` uses `type: github` (the natural mirror of `examples/06-plugin-caveman.yaml`)

### Effort estimate

Half-day to one day, depending on whether the git-remote fetcher needs its own integration tests (likely yes — adversarial submodule URLs, large clone sizes, auth modes).

### Order of work

1. Commit current in-tree extractor + doc updates as `feat(marketplace): extract marketplace.json from repo tarball` (the outer-fetch fix).
2. Branch `feat/marketplace-real-schema`.
3. Re-model `ClaudeCodeMarketplaceSource` + `UnmarshalJSON`. Parser audit hardening (the HIGH/MED findings above).
4. New `internal/sources/git/` fetcher + tests.
5. Re-wire `materializeMarketplacePlugin` to dispatch to the git fetcher with per-entry URL/SHA.
6. Rewrite envtest fixtures.
7. Update example 05 + README.
8. Run `make envtest-fast` + `examples/hydrate-demo.sh` end-to-end.
9. Squash + open PR.

---

## 6. BackendIdentityPolicy — Forwarder read-path resolves duplicates

**Design decision (2026-05-26)**: the operator stays dumb on BIP duplicates. There is NO DuplicateTarget reconciler, NO Synced status churn, NO shadow flip. Multiple CRs targeting the same `(target.kind, target.name)` are allowed by design.

The Forwarder (when ported from ach-old / wired into `cmd/ach/cmd/forwarder.go`) MUST resolve duplicates at READ time:

1. List all `BackendIdentityPolicy` CRs in its watched namespace via the informer cache.
2. Filter to entries where `spec.target.kind` and `spec.target.name` both match the incoming `/mcp/<name>` or `/a2a/<name>` route segment.
3. Sort matching CRs by `metadata.name` ASCENDING.
4. Take `Items[len-1]` (alphabetically LAST). Honor its `spec.forwardIdentityJWT`. Mint + attach §9.1 JWT iff `true`.
5. If zero matches: no JWT attached (Forwarder strips client `Authorization`).

Rationale: operators wanting different precedence rename their CRs (`zz-` suffix flips the winner). No babysitting on the operator side. Memory: `feedback_bip_no_shadow_logic.md`.

**Cleanup on domain port (TODO §2)**:
- SKIP ach-old's BIP DuplicateTarget reconciler when porting.
- Scrub stale "DuplicateTarget" / "shadow" language from any spec doc, planning corpus, or referenced Hub §9.3 text.
- `examples/09-backendidentitypolicy-context7.yaml` + `examples/10-backendidentitypolicy-duplicate.yaml` already in tree as the exemplar pair.

**Acceptance**:
- Forwarder route dispatcher uses the alphabetically-LAST CR
- `kubectl get bip` on a duplicate pair shows both CRs with empty Synced column (no `DuplicateTarget` reason emitted)
- Integration test: two BIPs on same target, opposite `forwardIdentityJWT`; Forwarder behavior tracks the LAST CR's value when one is renamed

---

## 7. Environment.AccessGroupSynced never reaches True — BLOCKS EnvKey lifecycle

**Severity**: HIGH (every `POST /platform/env-keys` returns 503 `not_ready`).

**Surface**: `POST /platform/env-keys`, `POST /platform/admin/keys/revoke`, `GET /platform/env-keys` against a freshly-created Environment CR.

**Symptom**:

```bash
curl -X POST http://localhost:8080/platform/env-keys \
  -H "x-ach-key: $PK" -H "Content-Type: application/json" \
  -d '{"environment":"demo","name":"my-worker"}'
# → {"error":{"code":"not_ready","message":"environment access group not yet synced"}}
```

**Where it fails**:
- `internal/platformapi/envkeys/handler.go:237` — `deps.Store.EnvironmentAccessGroupSynced(ctx, env)` returns false
- `internal/platformapi/store/store.go:115-128` — reads `AccessGroupSynced` from `Environment.status.conditions`; missing → false
- `internal/controller/ach/environment_controller.go:154-235` — Snapshotter-wired path ONLY emits `ExecutionResourcesResolved`; never writes `AccessGroupSynced`

The unit-test back-compat branch at `environment_controller.go:162-170` emits `AccessGroupSynced=Unknown reason=Initializing` only when `Snapshotter == nil`. Production starts with the Snapshotter wired and never advances past that condition.

**Fix sketch**: the steady-state reconcile should:

1. `r.LiteLLM.CreateAccessGroup(ctx, env.Name)` — idempotent, LiteLLM returns 200 on existing.
2. For each `team_id` in `spec.authorizedTeams`, `r.LiteLLM.BindTeamToAccessGroup(ctx, env.Name, team_id)`.
3. On full success: `AccessGroupSynced=True reason=Synced`.
4. On partial failure: `AccessGroupSynced=False reason=PartialBind` with offending team listed.
5. Drift detection on snapshot ticks: list current bindings; reconcile against `authorizedTeams`.

**Domain-port reference**: ach-old has the full implementation at `/home/jcm/Projects/ach-old/internal/controller/environment_controller.go` (Hub §6.4 Snapshotter path). Port verbatim — same TODO §2 work, just this slice.

**Acceptance**:
- `kubectl get environment demo -o jsonpath='{.status.conditions[?(@.type=="AccessGroupSynced")].status}'` returns `True` within 30s of CR apply.
- `POST /platform/env-keys` with a valid pk_ returns 200 + `ek_<plaintext>` body.
- `GET /platform/env-keys` lists the minted ek_ with `environment=demo` field.

---

## 8. Content Service `/content/{kind}/{name}` routes are unimplemented — BLOCKS artifact download

**Severity**: HIGH (every URL in `hydrate.json::context.*[].downloadUrl` is a dangling pointer today).

**Surface**: `GET /content/prompt/...`, `/content/plugin/...`, `/content/artifact/...` on the `ach-content-service` Service (port 8082).

**Symptom**: every artifact-download path returns 404. `/healthz` returns 200, so the container is alive — but only the health endpoint is registered.

**Where**: `cmd/ach/cmd/content_service.go:30-45` — explicitly documented as the "Phase 1 stub" with the real sendfile(2)-backed surface deferred to "Phase 5". The mux registers `/healthz` and nothing else.

**Domain-port reference**: ach-old has `internal/contentservice/handler.go` (sendfile-backed file streaming under `/var/cache/ach/{prompt,plugin,artifact,marketplace}/<name>.{md,tar.gz}` with scope-aware auth). Lift into a new `internal/contentservice/` package + wire into `runContentService` in place of the stub mux. Co-locates naturally with the operator Pod (shared RWO PVC).

**Hub §15.2 contract** (re-read before porting):
- `Content-Type`: respects `Prompt.spec.contentType` for prompts (default `text/markdown`); `application/gzip` for plugin/artifact/marketplace tarballs.
- Auth: anonymous OK in v1alpha1 (the hydrate response embeds signed-URL semantics in v1beta1; today the URLs are unsigned and the operator assumes the consumer already authenticated via pk_ on /platform/hydrate).
- Range requests: SHOULD support; sendfile passes through.
- Cache-Control: `public, max-age=300`.

**Acceptance**:
- `curl http://localhost:8082/content/prompt/claude-code-system-prompt` returns the raw markdown body with `Content-Type: text/markdown`
- `curl http://localhost:8082/content/plugin/caveman` returns the `.tar.gz` with `Content-Type: application/gzip`
- 404 only when the file is absent from the cache PVC
- Existing `examples/hydrate-demo.sh` shows non-zero `size_download` against each URL

---

## 9. Environment `Ready` composite condition missing

**Severity**: LOW (cosmetic — `kubectl wait` works on the sub-conditions).

**Where**: `internal/controller/ach/environment_controller.go`, `api/ach/v1alpha1/environment_types.go`.

**Symptom**: `kubectl wait --for=condition=Ready environment/demo` times out — no controller writes that condition.

**Root cause**: Hub §6.6 documents `Available`, `ContentReady`, `ExecutionResourcesResolved`, `AccessGroupSynced` as the closed set. There's no `Ready` rollup combining them.

**Fix sketch**: after each `SetStatusCondition` call, evaluate the sub-conditions and emit `Ready=True` iff all required sub-conditions are True. Verify the Hub-spec acceptance rule before deciding which sub-conditions are mandatory vs. optional for `Ready`.

**Depends on**: TODO §7 (AccessGroupSynced needs to be written first; otherwise Ready would never roll up True).

---

## 10. CLI commands missing — `ach login`, `ach hydrate`, `ach env`, `ach whoami`

**Severity**: MEDIUM (every demo runs through `examples/hydrate-demo.sh` which is the stand-in for the missing CLI).

**Where**:
- `cmd/ach/cmd/login.go` — not present
- `cmd/ach/cmd/hydrate.go` — not present
- `cmd/ach/cmd/env.go` — env-keys CRUD subcommands not present
- `cmd/ach/cmd/whoami.go` — not present

**Scope**: ROADMAP Phase 6+7. Subcommands wire into the existing cobra root in `cmd/ach/cmd/root.go` alongside operator/platform-api/forwarder/content-service/migrate.

**Reference**: ach-old has `cmd/ach/cmd/*.go` with the full CLI. Port verbatim into the single-binary layout (this repo's pattern).

**Acceptance**: `examples/hydrate-demo.sh` collapses to `ach login --sso && ach hydrate --environment demo > hydrate.json`. The shell script is deleted; the new flow becomes the e2e test fixture.

---

## Cross-cutting tech debt (deferred)

- **Goreleaser `dockers_v2` migration** — current configs emit deprecation warnings; future maintenance task
- **kustomize `namePrefix: ach-` collision** with already-prefixed source manifests (would produce `ach-ach-platform-api`) — drop or rename sources; cross-cuts rbac + manager
- **`config/manager/manager.yaml`** still single-container kubebuilder scaffold; ach-old has 2-container Pod (operator + content-service + shared RWO PVC). Port when content-service domain is wired
- **e2e Patch 1 silent no-op** in `config/e2e/kustomization.yaml` (targets nonexistent `ach-operator` Deployment) — fix when manager.yaml is ported
- **Bitnami image-retention exposure** broader audit — any other pinned bitnami images in scripts/values
- **Bootstrap tag** never created — Phase 16 reported 3 smoke failures so `bootstrap-complete` was not tagged. Once item 1 closes, tag retroactively at the post-fix commit
- **Pre-push gate 11 self-match warnings** — references/upstream-sync.md descriptive text contains `DO-NOT-COMMIT`/`DO NOT COMMIT` literals; gate exclusion needs widening or doc-text rewording
