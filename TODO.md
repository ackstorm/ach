# ACH — Post-bootstrap follow-ups

Tracker for non-blocking work after bootstrap release `v0.1.0`. Each item is self-contained, agent-actionable.

---

## 2. Domain port from ach-old

> **Status:** 📋 PLANNED — see [docs/plans/2026-05-25-ach-domain-port.md](docs/plans/2026-05-25-ach-domain-port.md) (existing plan covers this slice)

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

## 5. PluginMarketplace schema mismatch — re-model to upstream

> **Status:** ✅ DONE — PR #5 (`d09d076`). 3 Kinds (git-subdir/url/local-path) shipped. KNOWN limitation: real upstream has 5 Kinds (adds github + npm + metadata.pluginRoot); github/npm entries resolve to Kind="" → ReasonUnsupportedPluginSource per-entry. Defer 5-Kind expansion to follow-up. KNOWN test gap: TestPMR_Stage2_* envtest race — pre-existing on clean branch, not introduced by §5.

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

> **Status:** ✅ DONE — Layer A shipped via §4-04 `internal/forwarder/bip/` (RegisterIndex + alpha-LAST ResolveWinner). Layer B shipped via §4-08 (proxy/handlers.go calls bip.ResolveWinner; cobra wires bip.RegisterIndex). Doc scrubs shipped via §4-05 (PR `9641a9d`, `ab90f34`).

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

> **Status:** ✅ DONE (acab41e + 597a0de + 6f60ef1) — Environment reconciler now emits AccessGroupSynced True/False per Hub §6.6 closed-set; 5 envtests green; plan at [docs/plans/2026-05-26-environment-accessgroup-reconciler.md](docs/plans/2026-05-26-environment-accessgroup-reconciler.md). E2E `POST /platform/env-keys` validation deferred to §16 UAT.

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

> **Status:** 📋 PLANNED — see [docs/plans/2026-05-26-content-service-routes.md](docs/plans/2026-05-26-content-service-routes.md) (18 tasks across 7 phases; uses http.ServeContent for free sendfile(2); §2 dependency forked with seed-content-cache.sh fallback)

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

> **Status:** ✅ DONE (9148202 + e6837c5 + f2c98d4 + 828a61c + 27276a3 + 88efeb4) — Environment reconciler now emits `Available` composite rollup per Hub §6.6 closed-set; `computeAvailable` helper drives precedence (False > Unknown/missing > True); envtest covers the True path end-to-end; plan at [docs/plans/2026-05-26-environment-ready-composite.md](docs/plans/2026-05-26-environment-ready-composite.md). E2E gated behind `ACH_E2E_PHASE9=1` pending §16 LiteLLM seed.

**Severity**: LOW (cosmetic — `kubectl wait` works on the sub-conditions).

**Where**: `internal/controller/ach/environment_controller.go`, `api/ach/v1alpha1/environment_types.go`.

**Symptom**: `kubectl wait --for=condition=Ready environment/demo` times out — no controller writes that condition.

**Root cause**: Hub §6.6 documents `Available`, `ContentReady`, `ExecutionResourcesResolved`, `AccessGroupSynced` as the closed set. There's no `Ready` rollup combining them.

**Fix sketch**: after each `SetStatusCondition` call, evaluate the sub-conditions and emit `Ready=True` iff all required sub-conditions are True. Verify the Hub-spec acceptance rule before deciding which sub-conditions are mandatory vs. optional for `Ready`.

**Depends on**: TODO §7 (AccessGroupSynced needs to be written first; otherwise Ready would never roll up True).

---

## 10. CLI commands missing — `ach login`, `ach hydrate`, `ach env`, `ach whoami`

> **Status:** 📋 PLANNED — see [docs/plans/2026-05-26-cli-commands.md](docs/plans/2026-05-26-cli-commands.md) (12 tasks; designs from scratch — ach-old/cmd/ach/cmd/ does NOT exist; adds new /platform/whoami route; --sso browser flow deferred to §10.1, v1 ships --token paste mode)

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

## 11. Promote shell-driven UAT checks into `test/e2e/` (Go/Ginkgo)

> **Status:** 📋 PLANNED — see [docs/plans/2026-05-26-e2e-promotion.md](docs/plans/2026-05-26-e2e-promotion.md) (21 tasks across six §11.x sub-test groups + harness/docs wrap-up; stdlib testing not Ginkgo per memory feedback_023; Makefile e2e-focus footgun fix included)

Today's end-to-end sweep (2026-05-26) drove every CR surface via `kubectl` + `curl` against a live kind cluster. None landed in the Go-based suite at `test/e2e/`. These checks are good regression-catch candidates and belong as `phase4_*_test.go` (or split per concern; the suite already has `phase1_invariants_test.go`, `phase2_invariants_test.go`, `phase2_sc5_orphan_test.go`, `phase3_invariants_test.go`).

Smallest-to-biggest:

### 11a. Force-refresh annotation cycle (3 CR kinds)

For each of `Plugin/caveman`, `Prompt/claude-code-system-prompt`, `Artifact/openclaw-templates`:
- snapshot `status.lastSuccessfulRefresh`
- `kubectl annotate <kind>/<name> ach.ackstorm.ai/force-refresh=now --overwrite`
- assert annotation gets cleared within 10s
- assert `lastSuccessfulRefresh` advances strictly
- assert `status.upstreamRev` stays stable (no upstream change → no re-publish)

Cheap. Single helper iterated over the 3 kinds.

### 11b. BackendIdentityPolicy admission + finalizer + duplicate-target

- Apply `examples/09-backendidentitypolicy-context7.yaml` + `examples/10-backendidentitypolicy-duplicate.yaml`
- Assert both stored, both finalizer-tagged
- Assert `status.conditions` empty on both (no DuplicateTarget — by design per memory `feedback_bip_no_shadow_logic.md`)
- Delete both, assert finalizers cleanly removed

### 11c. PluginMarketplace internal-schema happy path

- Drive `examples/05b-pluginmarketplace-internal-http.yaml` end-to-end:
  - Pre-create ConfigMap `mkt-test-fixture` from `.gocache/uat/marketplace.json`
  - Deploy `mkt-test-server` (nginx serving the ConfigMap)
  - Apply the PluginMarketplace CR
  - Assert `Synced=True` within 30s
  - Assert `marketplace_plugins` table has the expected entry (name, upstream_rev, storage_location)
  - Delete CR, assert finalizer cleanup drops the DB row

This is the regression contract for the OUTER fetch + parser of OUR internal schema. Independent of TODO §5 (real Anthropic schema re-model).

### 11d. Operator restart + informer resync

- Snapshot operator pod uid
- `kubectl delete pod <operator-pod> --wait=false`
- Wait for fresh pod Ready (new uid)
- Annotate a Plugin CR with force-refresh
- Assert reconciliation fires within 30s (annotation cleared, lastRefresh advances)

Catches any "wires-only-on-startup" bugs in the controller-manager setup.

### 11e. `/platform/hydrate` golden JSON

Currently `examples/hydrate-demo.sh` drives the entire pk_ + hydrate path end-to-end and writes `examples/hydrate.json`. Promote to:
- `test/e2e/phase4_hydrate_test.go` that drives the same flow using the Go test harness (port-forward via the existing `phase3_helpers_test.go` pattern)
- Assert the response JSON shape against a checked-in golden file at `test/e2e/fixtures/hydrate-golden.json`
- Tolerate field-order differences but exact-match values + downloadUrl paths

This is the highest-value e2e add — it locks the contract surface the future `ach hydrate` CLI will depend on.

### 11f. Delete + finalizer cleanup matrix (5 CR kinds)

Partially covered by phase3 today. Extend to cover:
- Environment delete drives the §6.5 LiteLLM `DeleteAccessGroup` + `DeleteTag` calls (assert via LiteLLM mock or live)
- PluginMarketplace delete drives the §10.3 cache cleanup + `marketplace_plugins` DELETE
- BIP delete is finalizer-only (no PVC, no DB) — assert clean removal

**Acceptance**: all 6 sub-tests slot into the existing `make e2e-full` / `make e2e-keep` harness; `e2e-focus FOCUS="phase4"` runs only the new ones during dev-loop. Each adds < 30s to the full-suite runtime when run against an already-up kept cluster.

---

## 15. Configurable LiteLLM default-team alias (single source of truth)

> **Status:** 📋 PLANNED — see [docs/plans/2026-05-26-default-team-alias.md](docs/plans/2026-05-26-default-team-alias.md) (26 tasks across 10 phases; atomic interface widening keeps build green between commits; TDD via unit + envtest + e2e)

**Severity**: LOW (enhancement; default `"default"` works for the bootstrap deployment).

**Where**: `internal/litellm/team.go::defaultTeamAlias` constant; SSO handler at `internal/platformapi/auth/sso.go::provisionUser` (hardcoded `"default"` in `ListTeamsByAlias` + `TeamMemberAdd` calls); EnvKey handler at `internal/platformapi/envkeys/handler.go` (hardcoded `defaultTeam` variable).

**Symptom**: today the operator-bootstrapped team (J.5) is always alias=`default`. Deployers wanting a tenant-specific identity (e.g. an "engineering" team that every SSO user joins) cannot change it without a code patch.

**Fix sketch**:
- Add a top-level operator config field `--default-team-alias` (default `"default"`); read via the existing config layer (env var `ACH_DEFAULT_TEAM_ALIAS` mirror).
- Pass into `LiteLLMConnectionReconciler` as a field; the reconciler passes the value to `client.EnsureDefaultTeam(ctx, alias)` (interface signature widens to take the alias).
- Same value flows into `platformapi/auth/Deps` and `platformapi/envkeys/Deps` so the SSO + EnvKey paths use the same canonical name.
- Hub §8.1 spec note: "The default team alias is configured at operator startup. SSO callbacks enroll every newly-SSO'd user into the team with this alias."

**Acceptance**: an operator started with `--default-team-alias=engineering` (a) creates a team with that alias on first startup (idempotent), (b) enrolls every SSO-provisioned user into it, (c) makes the value visible in `kubectl describe deploy ach-operator` env.

---

## 16. Validation gate after §7 + §9 — Environment Available=True end-to-end

> **Status:** 📋 PLANNED — see [docs/plans/2026-05-26-environment-available-uat.md](docs/plans/2026-05-26-environment-available-uat.md) (8 tasks across 3 phases; §7 + §9 marked as hard blocking gates in Task 0; Phase A manual script + Phase B stdlib testing automated, NOT Ginkgo)

**Severity**: MEDIUM (acceptance test for the domain-port work, not a code task itself).

**Trigger**: run AFTER both §7 (AccessGroupSynced reconciler) AND §9 (Available composite rollup) have landed.

**Why this lives here**: when §7 + §9 are in place, `Environment/demo` should converge to `Available=True` IFF (a) AccessGroupSynced=True and (b) ExecutionResourcesResolved=True. (b) requires LiteLLM to have real rows for the 5 names in `examples/04-environment-demo.yaml::spec.runtime` — `gemini.gemini-flash-latest`, `openai.gpt-5-mini`, `vmcp-dev`, `vmcp-aws`, `test-noop-agent`. Today (2026-05-26) LiteLLM has only 2 sample models (`gpt-3.5-turbo`, `fake-openai-endpoint`) and zero MCP / zero agents — so the example is a deliberate "unresolved" UAT fixture.

**Validation procedure** (once §7 + §9 land):

1. Bring up the cluster (`make cluster-up`) — operator auto-bootstraps the LiteLLM default team (J.5).
2. Apply `examples/04-environment-demo.yaml` — exercises §7's access-group binding path. Expect `AccessGroupSynced=True` within 30s.
3. Seed the 5 LiteLLM resources via the admin API:
   ```bash
   # 2 models
   curl -X POST http://localhost:4001/model/new -H "Authorization: Bearer sk-test-master-key" \
     -d '{"model_name":"gemini.gemini-flash-latest","litellm_params":{"model":"gemini/gemini-flash-latest"}}'
   curl -X POST http://localhost:4001/model/new -H "Authorization: Bearer sk-test-master-key" \
     -d '{"model_name":"openai.gpt-5-mini","litellm_params":{"model":"openai/gpt-5-mini"}}'
   # 2 mcp servers
   curl -X POST http://localhost:4001/v1/mcp/server -H "Authorization: Bearer sk-test-master-key" \
     -d '{"server_name":"vmcp-dev","url":"http://localhost:9100/mcp","transport":"sse"}'
   curl -X POST http://localhost:4001/v1/mcp/server -H "Authorization: Bearer sk-test-master-key" \
     -d '{"server_name":"vmcp-aws","url":"http://localhost:9101/mcp","transport":"sse"}'
   # 1 a2a agent
   curl -X POST http://localhost:4001/v1/agents -H "Authorization: Bearer sk-test-master-key" \
     -d '{"agent_name":"test-noop-agent","url":"http://localhost:9200/a2a"}'
   ```
4. Wait one Snapshotter tick (≤5 min) or force one via the touch annotation.
5. Expect:
   ```yaml
   status:
     conditions:
       - type: ExecutionResourcesResolved, status: True,  reason: Resolved
       - type: AccessGroupSynced,           status: True,  reason: Synced
       - type: Available,                   status: True,  reason: AllSubConditionsTrue
     unresolvedRuntime:
       models: [], mcpServers: [], a2aAgents: []
   ```
6. Promote into `test/e2e/phase4_environment_available_test.go` (cross-references TODO §11): drive the same flow under Ginkgo, assert the three True conditions, use a httptest fake LiteLLM to register the 5 names without a real LLM provider.

**Cleanup**: tear down via `kubectl delete environment demo` — §6.5 drain exercises `DeleteAccessGroup` + `DeleteTag` (already wired).

---

## Cross-cutting tech debt (deferred)

- **Goreleaser `dockers_v2` migration** — current configs emit deprecation warnings; future maintenance task
- **kustomize `namePrefix: ach-` collision** with already-prefixed source manifests (would produce `ach-ach-platform-api`) — drop or rename sources; cross-cuts rbac + manager
- **`config/manager/manager.yaml`** still single-container kubebuilder scaffold; ach-old has 2-container Pod (operator + content-service + shared RWO PVC). Port when content-service domain is wired
- **e2e Patch 1 silent no-op** in `config/e2e/kustomization.yaml` (targets nonexistent `ach-operator` Deployment) — fix when manager.yaml is ported
- **Bitnami image-retention exposure** broader audit — any other pinned bitnami images in scripts/values
- **Bootstrap tag** never created — Phase 16 reported 3 smoke failures so `bootstrap-complete` was not tagged. Once item 1 closes, tag retroactively at the post-fix commit
- **Pre-push gate 11 self-match warnings** — references/upstream-sync.md descriptive text contains `DO-NOT-COMMIT`/`DO NOT COMMIT` literals; gate exclusion needs widening or doc-text rewording
