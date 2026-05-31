# Phase 7 / W6-01 — Follow-up handoff

**Branch:** `phase7/w6-01-cluster-handoff`
**As of:** 2026-05-30
**Picked up by:** runtime-gate agent (you handed this off to run the W6-01 live e2e).

This file is the entry point for resuming. The detailed design + ordered
build steps live in
`docs/superpowers/plans/2026-05-30-adapter-mcp-surgical-merge.md` — read it
together with this.

---

## TL;DR

The W6-01 runtime gate (first-ever live run of the Phase 7 hydrate engine)
found that the gate's premise ("14/14 green, just run it") was false. The
engine had **real bugs that only surface at runtime**. The biggest finding,
**confirmed and fixed with you (the human) in the loop**, is that the adapter
write path **clobbered the user's pre-existing MCP servers** — so we
redesigned it to **surgically merge** into each tool's native config.

That redesign is **landed and validated**. Two pre-existing engine problems
+ the e2e rewrite remain. Nothing below is blocked on a decision — it's all
"do the work."

---

## ✅ DONE (committed on this branch, gated green by pre-commit)

| Commit | Summary |
|--------|---------|
| `643fc1f` | `fix(make)`: `build-e2e` had stale prereqs (`manifests`/`generate` → `gen-manifests`/`gen-code`) and didn't auto-route into devtools; now mirrors `_build-server`. Also pinned `KUBECONFIG` to `.gocache/kube/config` for host-only `wait-*`/`logs-*` so `wait-ach` stops hitting a stale ambient `~/.kube/config` port. |
| `f0e1df7` | `feat(07-W6-01)`: the adapter surgical-merge redesign + two engine fixes (below). |

What `f0e1df7` contains (all unit-tested + lint-clean + **validated live**):

1. **Surgical merge** — `internal/cli/hydrate/wiring.go`
   `publishRuntimeFile` + `mergeForward{,JSON,TOML}` + `deepMergeInto`.
   Adapter config now resolves under a **tool root** (workspace root in
   project scope, `$HOME` under `--global`) decoupled from `achDir`, and
   we **read the user's existing JSON/TOML and upsert only our keys**. The
   user's other servers/settings are preserved. Threaded `toolRoot`
   through `AdapterDispatcher.Render` (`result.go`) + `commit.go`.
2. **Per-key drift** — the §8.4 truth table now applies to *our* keys only
   (canonical subtree hash via `subtreeHash`/`extractByKeys`). A user edit
   to *our* key → `exit.Drift` unless `--force`; a true no-op skips the
   rewrite (byte-stable). Removed the dead `adapterContentResolver` +
   the old whole-file collision/cascade in `Render`.
3. **claude-code path** — `.claude/.mcp.json` → `.claude/settings.json`
   (your directive; see "Open question" below). gemini/opencode/codex
   project paths were already correct.
4. **Bug A (CLI-03)** — `cmd/ach-cli/cmd/hydrate.go`: the content-fetch
   client now sends `x-ach-environment` for `pk_` (was `400
   missing_environment` on every artifact GET).
5. **Credential (ADAPT-03)** — `commit.go` now wraps the adapter ctx with
   `adapter.WithCredential(bearer)`; the rendered config carries a real
   `x-ach-key` (was empty — would never authenticate).

**Live validation** (claude-code, against the kind cluster, before it was
reset): pre-seeded `.claude/settings.json` with a user `my-personal-server`
+ `permissions`; after `hydrate` the file contained **our** `demo-mcp-jwt`/
`demo-mcp-nojwt`/`demo-agent` **and** the user's `my-personal-server` +
`permissions`, with a populated `x-ach-key`. The core requirement works.

Unit tests rewritten: `internal/cli/hydrate/wiring_test.go`
(`SurgicalMerge_PreservesUserKeys`, `PerKeyDrift_RefusesUserEditOfOurKey`)
+ `internal/cli/adapter/claudecode/claudecode_test.go` paths.

---

## ⬜ MISSING (do these, in order)

### 1. Bug E — content re-hydrate GAP (pre-existing; NOT the redesign) — **blocks multi-hydrate e2e**

On the **2nd** hydrate, content extraction dies with:
```
extract content (plugin): extract: read existing for sha256: read <ws>/.ach/plugin/caveman: is a directory
```
Root cause: the D-15 disk-write short-circuit (`internal/cli/extract/stage.go`
`fileSha256IfExists`, called from `StageAndPublish` step 3, ~line 225) is
**single-file** logic — it `io.Copy`s the existing target for sha256. But a
plugin (gzip) extracts to a **directory** at `finalRelPath`, so the copy
errors. Even past that, `publishGzip` → `renameAtomic` can't rename the new
extraction over a pre-existing directory.

The deeper truth: the W5-01 **"replace step in the orchestrator"** (delete
prior content before re-extract) and the **orchestrator-level content
drift/skip** (compare freshly-fetched tarball `xxh3` vs state `SourceHash`
→ no-op) were **never wired**. `extractorImpl.ExtractContent`
(`wiring.go`) calls `StageAndPublish` unconditionally; nothing deletes prior
content or short-circuits a plugin no-op.

**Fix approach (must satisfy `w1_baseline` no-op idempotency):**
- The plugin idempotency check belongs at the **orchestrator** level
  (`commit.go` content loop / `extractorImpl`), not in `StageAndPublish`'s
  file-sha256 short-circuit (which can't compare a tarball to an extracted
  dir). On re-hydrate: if the fetched tarball's `SourceHash` == the prior
  state entry's `SourceHash` for that content target → **skip** (no-op,
  `FilesWritten==0`). Else → **delete the prior `finalRelPath`** then
  re-extract.
- Make `fileSha256IfExists` / step-3 short-circuit tolerate a directory
  target (don't error) — but the real skip decision should come from the
  orchestrator drift check above.
- `publishGzip`/`renameAtomic`: ensure the destination is removed before
  the atomic rename when re-extracting (the "replace step").
- `internal/cli/extract/stage.go:399-420` is the immediate error site;
  `StageAndPublish` is `stage.go:184`; orchestrator content loop is in
  `internal/cli/hydrate/commit.go` (the `c.extractor.ExtractContent` call,
  ~step 7/9) + `internal/cli/hydrate/wiring.go` `extractorImpl`.

### 2. e2e rewrite — `test/e2e/cli_hydrate_engine_test.go` (+ `phase7_helpers_test.go`)

The committed test still asserts the OLD model and will fail. Rewrite:
- **sc1 (8 subtests — single-hydrate, do NOT need Bug E):** change the
  runtime-config path const to the new target (`.claude/settings.json`,
  `.gemini/settings.json`, `.opencode/opencode.json`, `.codex/config.toml`).
  Add a **surgical-preserve** assertion: pre-seed a user MCP server in the
  target before hydrate, assert it survives + ours is added. These can go
  green FIRST (after the path fix) as the redesign's e2e proof.
- **`phase7StatePath`:** workspace mode is `<out>/.ach/state.json` (NO env
  segment — see `internal/cli/state/path.go:30-45`). The helper currently
  expects `<out>/.ach/<env>/state.json`; reconcile to impl.
- **sc2 / sc3 / sc4 / w1 (multi-hydrate):** depend on **Bug E** being fixed
  first. sc3 must move to **per-key** drift semantics (user edits OUR key →
  exit 2 preserve; user edits ANOTHER key → preserved + ours upserted, exit
  0). sc4 auto-claim becomes per-key.
- Activation (env passthrough across the devtools docker boundary —
  `make`'s `container_target` only forwards command-line vars, and
  `scripts/dev.sh` only `-e`'s an allowlist): run the suite via an explicit
  exported shell, NOT `make e2e-focus FOCUS=...`:
  ```
  ./scripts/dev.sh bash -c 'export ACH_E2E_PHASE7=1 \
    ACH_E2E_PHASE7_PK=<real-pk> ACH_E2E_PHASE7_BASE_URL=http://localhost:8080 \
    E2E_SKIP_SETUP=1; go test -tags=e2e -v -count=1 -timeout 15m \
    -run TestPhase7CLIEngine ./test/e2e/...'
  ```
  (`--network=host` in `dev.sh` makes `localhost:8080` reach the kind
  gateway; the test is stdlib, so it's `-run`, not the legacy `-ginkgo.focus`.)

### 3. Global-scope opencode remap + Sync path consistency
- opencode's `--global` target is `~/.config/opencode/opencode.json` (NOT
  `~/.opencode/...`) — the orchestrator must remap that one path when
  `--global`. The other 3 tools' relative paths are correct under both
  roots.
- `commit.go` Sync target resolution uses `filepath.Join(c.achDir, "..", target)`
  (~line 512) which equals the workspace root in project mode but `~/.ach`
  (not `$HOME`) in `--global`. Thread `toolRoot` into the Sync path for
  `--global` consistency.

### 4. Final gates
`make build-e2e` → run `TestPhase7CLIEngine` to **20/20 green** →
`make qa-security` → `git push` (the installed pre-push hook runs the
17-gate publication check) → open the PR.

### Out of scope
- **Bug F** (`--only-runtime` → `extract content (model): artifact/ 404`):
  pre-existing, but the e2e does NOT use `--only-runtime`, so it does not
  block the gate. File an issue; don't fix it for W6-01.

---

## How to resume (environment)

The kind cluster is **ephemeral** — it was reset mid-session (all docker
containers vanished; `kind get clusters` empty). Each recreate gets a NEW
random API port, but `make`'s `KUBECONFIG` pin (commit `643fc1f`) now tracks
`.gocache/kube/config`, so `make wait-ach` works after recreate.

```
make cluster-up            # ~10 min; synchronous; recreate the cluster
make wait-ach              # confirm Ready (now kubeconfig-pinned)
make build-e2e             # -tags=e2e binaries (auto-routes; SIGKILL seam)
```

**A `pk_` is per-cluster** (new cluster = new keystore — re-mint after every
recreate). Mint via the SSO flow (no real token committed anywhere):
```
# host-side, against localhost:8080 (gateway via kind extraPortMapping).
# Recipe in references/local-testing-gateway.md §3: GET
# /platform/auth/login → follow the Dex-mock redirect chain (clear cookie
# Secure flag, rewrite dex DNS → localhost:8080) → read the minted pk_.
```
`ach-cli` is CGO/glibc-linked for the devtools container — run it **inside**
`./scripts/dev.sh`, not on the host (host glibc is older).

---

## Open question for you (human), not blocking

The official Claude Code docs say MCP server **definitions** live in
`<ws>/.mcp.json` (project) / `~/.claude.json` (user), and `.claude/settings.json`
is the **approval allowlist** (`enabledMcpjsonServers`). You directed us to
write definitions into `.claude/settings.json` anyway ("same method for all").
We complied and centralized the path so it's a one-line change. If Claude Code
doesn't actually load `mcpServers` from `settings.json`, flip
`settingsJSONPath` in `internal/cli/adapter/claudecode/claudecode.go` to
`.mcp.json` + add the approval-allowlist write. (Gemini's `settings.json` IS
the real def location, so "same method" holds there.)

---

## Gotchas discovered (so you don't re-learn them)

- The original W6-01 instructions were **stale vs the committed code**: 20
  subtests (not 14); `make e2e-focus` needs `RUN=` not `FOCUS=` for the
  stdlib test; `pk_demo` is not a real key (mint via SSO); `.planning/phases/07-…`
  didn't exist locally until you vendored it (`d75ec52`).
- The Phase 7 engine had **never been run live** before W6-01 — that's why
  so many runtime-only bugs (A, the empty credential, E, F) surfaced at once.
  The runtime gate did exactly its job.
- `qa-lint-changed` reports "No Go changes vs origin/main" on this branch
  (its diff base is `origin/main`); lint directly with
  `./scripts/dev.sh bash -c './bin/golangci-lint run <pkgs>'` instead.
