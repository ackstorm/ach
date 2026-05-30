# Phase 6: CLI Foundation - Context

**Gathered:** 2026-05-28
**Status:** Ready for planning
**Mode:** `/gsd-discuss-phase 6` (interactive — 4 selected areas + 2 scope-edge follow-ups)

<domain>
## Phase Boundary

Phase 6 lights up the `ach` CLI surface — every cobra subcommand at `cmd/ach/cmd/<verb>.go` plus shared internals under a new `internal/cli/` package. It implements the **full ROADMAP+REQ Phase 6 scope** (13 REQs CLI-01..CLI-13, 5 success criteria, the full §3 + §5 surface from `spec/ach_cli_spec_v20260515_FINALv4.md`) staged across **3 waves / ~9 plans**:

- **Wave 1 — Foundation + Dex (3 plans)**
  - `internal/cli/` shared internals: yaml multi-deployment config (D-03), HTTP client (D-04 — `x-ach-key` carrier, mutex-credential resolution), exit-code matrix (D-13), redaction helper (`x-ach-key → <prefix>_***`).
  - `ach login` Dex SSO via **device-code polling** (D-06). Server-side: two new endpoints under Platform API — `POST /platform/auth/cli/init` mints a session_id + verification_url; `POST /platform/auth/cli/token` returns the pk_ once Dex round-trip completes (Redis-backed session store, short TTL). CLI side: spec §5.1 interactive UX verbatim (prompts deployment name + URL, opens browser, polls token endpoint, prints SSO email + masked tail).
  - `ach whoami` no-net default (reads on-disk config, prints identity block) + `--verify` asymmetric per spec §5.3: `pk_` → `GET /platform/environments?limit=1`; `ek_` → `POST /platform/hydrate {}` with `Accept-Encoding: gzip` discarded body. Exit codes 0/3/6.

- **Wave 2 — Core surface (3 plans)**
  - `ach env {list, describe}` — list paginates `next_cursor` automatically, describe two-call (list + hydrate) with admin-403 graceful fallback per spec §5.5, `--metadata-only` flag.
  - `ach env-keys {create, list, revoke}` with the **ek_ persist deviation locked in D-09**: always-persist, `--no-save` flag for ephemeral, label = server `--name` (alias doubles as config key `deployments.<active>.ek.<name>`). `revoke` rejects raw plaintext (`ekid_…` only); interactive confirmation + `--yes` bypass.
  - `ach hydrate --environment <name>` — `POST /platform/hydrate` only (no hydrate engine — Phase 7 owns adapters/state.json/safe-extract/distribution). pk_ stderr warning per §6.6 (suppressed by `--no-warnings`); mutex credential sources (`--api-key` / `--env-key` / `ACH_API_KEY` / `ACH_ENV_KEY`) exit 1 when >1 set; `--environment` required for `pk_`, optional for `ek_`. Output: byte-for-byte equivalent to `examples/hydrate.json` (golden).
  - `ach config {list, show, use, remove, rename}` per spec §5.4 — 5 commands; `show` masks pk_/ek_ to `<prefix>_****<last-4>`; `--reveal` flag opt-in unmask for named deployment only (never whole file); `use` sets `default:`; `rename` preserves `pk` + `ek` map.
  - `ach logout` per spec §5.2 — wipes `pk:` from active deployment, leaves `url:` so subsequent `ach login` resumes.

- **Wave 3 — Edges (3 plans)**
  - **Synthetic mode** (CLI-07) — cross-cutting, gated at the config-resolution layer: active when `ACH_BASE_URL` set AND credential resolves from `--api-key`/`ACH_API_KEY`. Rejects `--deployment`/`ACH_DEPLOYMENT` with exit 1, rejects `ach login`/`ach config *`/`ach logout`/`ach env-keys create` (unless `--no-save`) with exit 1 per spec §3.3. Half-set (`ACH_BASE_URL` without credential) → exit 1. State files record `"deployment": "(env)"`.
  - `ach admin {keys revoke, users revoke-keys, refresh}` per spec §5.10 — exit 3 on `403 not_admin`. `keys revoke` accepts both `pkid_…` and `ekid_…`; raw plaintext rejected. `users revoke-keys` prints `{pk_count, ek_count}`. `refresh <kind> <name>` patches `ach.ackstorm.ai/force-refresh: <RFC3339>` annotation on target CR via `POST /platform/admin/refresh` (already mounted Phase 3); `<kind> ∈ {plugin, prompt, artifact, marketplace}`.
  - **E2E umbrella + demo collapse** — `test/e2e/cli_login_hydrate_test.go` against `make cluster-keep` kind cluster (per CLAUDE.md dev loop): drives device-code login → `ach hydrate --environment demo` → byte-for-byte golden diff vs `examples/hydrate.json` + env list / env-keys create / whoami --verify smoke. `examples/hydrate-demo.sh` deleted; README + CLAUDE.md `Common failure modes` cross-refs updated to reference `ach login` + `ach hydrate` instead.

Phase 6 explicitly **excludes**:

- **Hydrate engine** — concurrency lock, atomic `state.json` v2 write, dual-hash drift detection, `--include-runtime`/`--only-runtime`/`--sync`/`--force`/`--dry-run` flags (CLI spec §6, §8). Phase 7 owns CLI-14..21 + STATE-01..03.
- **Platform adapters** — `claude-code`, `codex`, `gemini-cli`, `opencode` adapters with merge strategies + autodetection + plugin transformation (CLI spec §7, ADAPT-01..07). Phase 7.
- **Safe tar extraction** — path sanitization, decompression-bomb caps, mode masking, atomic temp+rename per file (CLI spec §6.4, SAFE-*). Phase 7.
- **Distribution** — OCI container `ghcr.io/ackstorm/ach`, standalone binaries (linux/darwin/windows × amd64/arm64), Homebrew tap, Helm chart consumers (CLI spec §2, DIST-*). Phase 7.
- **`ach platforms list`** (CLI spec §5.8) — Phase 7 territory; the 4 adapter IDs only exist meaningfully once adapter machinery lands.
- **`ach content fetch`** (CLI spec §5.9) — direct content-service GET wrapper. Phase 7.
- **`--verbose` log-to-file infrastructure** (CLI spec §9.4) — `~/.cache/ach/logs/ach-<rfc3339>.log` with header redaction. Phase 6 ships plain text to stderr only; full log-to-file deferred.
- **`--output-format json`** on list/describe subcommands (CLI spec §9.2). Phase 6 ships human-readable text only; structured output deferred.
- **`ach version`** beyond cobra's implicit `--version` ldflag injection (already wired in `cmd/ach/cmd/root.go`). No dedicated `version` subcommand.
- **OS keyring integration** — pk_/ek_ live in `~/.config/ach/config.yaml` plaintext per spec §13 + §3.2 ("local trust artifact the CLI is authorized to hold"); keyring backend deferred to v1beta1.
- **`ach hook emit`** — not in Hub v1alpha1 (CLI spec §13).
- **Offline `ach status`** — every server-bearing subcommand requires connectivity (CLI spec §13).

</domain>

<decisions>
## Implementation Decisions

### Phase scope shape

- **D-01:** **Intermediate A — full spec, 1 phase, 3 waves, ~9 plans.** All 13 CLI- REQs + 5 SCs land in Phase 6 (no roadmap split into 6/6b). Wave breakdown is the planner's working unit; final plan count is the planner's call. Justification: Dex device-code is known-pattern (gh, gcloud, aws sso) not exploratory; multi-deployment yaml day-1 eliminates schema migration; recent velocity (STATE.md: ~6.6 min/plan avg) makes 9 plans a single sitting; Phase 7 (hydrate engine + adapters + safe extract + distribution) is heavier and benefits from a self-contained Phase 6.

### Dex SSO mechanism

- **D-02:** **Device-code polling pattern** for `ach login` — two new Platform API endpoints under `/platform/auth/cli/`:
  - `POST /platform/auth/cli/init` → returns `{session_id, verification_url, poll_interval, expires_in}`. Server stores session in Redis with short TTL (planner picks: spec §13 v1beta1 mentions /platform/whoami, no precedent on session TTL — recommend 5 min, 2s poll). `verification_url` is the existing `/platform/auth/login` 302-to-Dex flow, parametrized with `session_id` so the existing `/platform/auth/sso/callback` knows which session bucket to write the pk_ into.
  - `POST /platform/auth/cli/token` body `{session_id}` → `200 {key_id, plaintext, owner_email}` once Dex round-trip completes; `202 {status: "pending"}` while waiting; `404 session_not_found` after TTL or after first successful retrieval (one-shot); `410 session_expired` on TTL bust.
  - CLI opens browser at `verification_url`, polls token endpoint until 200 / 4xx / 5xx / context cancel.
  - **Justification:** pk_ never traverses a URL (vs query-string redirect); no localhost listener requirement (works behind restrictive networks, in containerized dev environments); aligns with the OAuth 2.0 Device Authorization Grant pattern. ~180 LOC server + ~90 LOC client.
- **D-03:** **`ach login` UX = spec §5.1 verbatim.** Interactive prompts for deployment name (default existing or fresh) + URL (https:// required); `--deployment <name>` + `--base-url <url>` skip prompts; `--no-browser` prints `verification_url` and waits. On success: print SSO email + masked pk_ tail (`pk_****WXYZ`). Callback timeout → exit 1; partial entry (URL only, no `pk:`) persisted so `ach login --deployment <name>` resumes against the same URL. New `ach login` on existing deployment overwrites prior `pk:`; prior server-side key expires naturally per 7d sliding window (Hub §7.1). **Synthetic mode rejection** per §3.3 — exit 1 with the spec-mandated message.

### Config + multi-deployment registry

- **D-04:** **`~/.config/ach/config.yaml` with full multi-deployment schema day 1.** Path resolution: `$XDG_CONFIG_HOME/ach/config.yaml` if set, else `$HOME/.config/ach/config.yaml`. Schema per spec §3.2 verbatim:
  ```yaml
  default: <name>
  deployments:
    <name>:
      url:  https://...
      pk:   pk_...                # optional
      ek:                          # optional convenience map
        <local-label>: ek_...
  ```
  File mode `0600`, parent dir `0700`. Read-time check: file mode no more permissive than 0600 → stderr warning; write-time: normalize back to 0600. **Non-HTTPS `url:` refused on read AND write** (refuse to load, refuse to save).
- **D-05:** **`ach config` ships all 5 spec subcommands** (`list`, `show`, `use`, `remove`, `rename`). `show` masks `pk_`/`ek_` to `<prefix>_****<last-4>`; `--reveal` opt-in unmask for a **named deployment only** (never whole file). `remove` rejects deletion of the active default unless `--force`. `rename` preserves `pk` + `ek` map. None contact the server. All exit 1 in synthetic mode.
- **D-06:** **`ach logout`** per spec §5.2 — wipes `pk:` from active deployment, leaves `url:` so subsequent `ach login` resumes. Does NOT remove the deployment entry. Does NOT clear `default:`. Exit 1 in synthetic mode.

### ek_ persist (SPEC DEVIATION from CLI-09 / AC4)

- **D-07:** **`ach env-keys create` always persists `ek_` plaintext to `deployments.<active>.ek.<server-name>`** in the active deployment. The server-side `--name` flag doubles as the local config key — no separate `--save-as` flag. `--no-save` opts out of persist (ek_ goes to stdout only, useful for CI scripts piping ek_ into a vault).
- **D-08:** **Synthetic mode + `ach env-keys create`** — exit 1 unless `--no-save` is set. With `--no-save` in synthetic, ek_ prints to stdout and CLI exits 0 (no config write).
- **Justification for the deviation:** the spec's opt-in `--save-as` was designed around a "production posture" where ek_ in config is dev-only. User decided always-persist is the more ergonomic default for the v1alpha1 audience (single-user developers running `ach hydrate` against their own ek_), with `--no-save` as the explicit escape hatch for CI / secret-manager workflows. **This decision intentionally diverges from CLI-09 + AC4 — must be flagged in REQUIREMENTS.md and CLI spec changelog when the planner ships W2 plan that introduces it.**

### Hydrate (Phase 6 surface only)

- **D-09:** **`ach hydrate` is `POST /platform/hydrate` + stdout JSON dump.** No on-disk write, no diff, no state file, no adapter dispatch — those are Phase 7 (CLI-14..21). The spec §5.7 full surface is NOT implemented here.
- **D-10:** **pk_ runtime warning** per CLI-05 / spec §6.6 — emit to stderr BEFORE any HTTP call; suppressed by `--no-warnings`. Text per spec §6.6.
- **D-11:** **Mutex credential sources** per CLI-09 / spec §6.1 — `--api-key`, `--env-key`, `ACH_API_KEY`, `ACH_ENV_KEY` all four mutually exclusive; >1 present → exit 1 with the conflict list. `--env-key`/`ACH_ENV_KEY` resolve against `deployments.<active>.ek.<label>` and are unavailable in synthetic mode (exit 1).
- **D-12:** **`--environment` required for `pk_`, optional for `ek_`** per CLI-06 / spec §5.7. pk_ invocation without `--environment` → exit 1 (CLI-side check; server's `400 missing_environment` is the backstop).

### whoami (asymmetric verify, no server endpoint)

- **D-13:** **`ach whoami --verify` is purely client-side asymmetric** per spec §5.3 + CLI-11. No new `/platform/whoami` server endpoint in Phase 6 (spec §13 punts that to v1beta1). The CLI inspects the resolved credential's prefix and calls the right endpoint:
  - `pk_` → `GET /platform/environments?limit=1`
  - `ek_` → `POST /platform/hydrate {}` with `Accept-Encoding: gzip` and discarded body
- **D-14:** **Exit codes 0/3/6** per spec §5.3 — 200 → exit 0 (print identity block + `Verified: yes`); 401 → exit 3; network → exit 6.

### Output discipline (Phase 6 minimal)

- **D-15:** **Plain text human-readable output only** for Phase 6. `--output-format json` deferred to Phase 6b/7. `--verbose` writes to stderr only (no log-to-file, no `~/.cache/ach/logs/` infrastructure). `x-ach-key` redacted to `<prefix>_***` in any stderr log line that includes a request header dump. Spec §9.1–9.4 full discipline is NOT implemented here.
- **D-16:** **Exit code matrix per spec §9.3** — 0 (success), 1 (general error / synth-incompatible), 3 (auth/authz: 401, 403 not_admin/unauthorized_team), 6 (network/503), 8 (config file error). Codes 2 (drift), 4 (state mismatch), 5 (schema mismatch), 7 (local I/O) are hydrate-engine territory — Phase 7.

### Demo collapse + e2e

- **D-17:** **`examples/hydrate-demo.sh` deleted in Wave 3.** Replacement is the documented `ach login` + `ach hydrate --environment demo > hydrate.json` workflow in README. `examples/hydrate.json` stays as the golden artifact e2e diffs against.
- **D-18:** **`test/e2e/cli_login_hydrate_test.go` against kept kind cluster** (per CLAUDE.md dev loop — `make cluster-keep`). Single stdlib test slots into the existing `test/e2e/` umbrella, drives device-code login (using a test-only fast-path that bypasses interactive browser open — planner picks: env-var-injected pk_ for the test, OR a `--token` debug flag gated by build tag), then `ach hydrate --environment demo`, then byte-for-byte diff vs `examples/hydrate.json`. Adds smoke for `ach env list`, `ach env-keys create`, `ach whoami --verify`.

### Server-side Phase 6 deltas

- **D-19:** **Two new endpoints under `/platform/auth/cli/`** — `init` + `token`, both POST. Reuses Phase 3 D-08 `keystore` + Phase 3 `internal/audit` for emission. Redis session schema: `ach:cli-session:<session_id>` → `{pk_id, pk_plaintext, owner_email, created_at}` with TTL ~5 min. Audit event on successful exchange (same shape as `ach login` audit emission today; planner reuses existing `key.login` action or adds `key.cli_login`).
- **D-20:** **NO existing `/platform/auth/sso/callback` contract change beyond opaque session_id threading** — the callback still returns JSON to the browser when called without a session_id (preserves the `hydrate-demo.sh` style flow during the transition); when called with a session_id (CLI-driven), it writes the pk_ to Redis under that key and returns a friendly browser-side "you may close this window" HTML page. This avoids breaking the existing test/e2e/phase3_invariants browser-driven assertions.

### Claude's Discretion

- Session TTL exact value, poll interval exact value (D-02) — planner picks; recommend 5 min TTL, 2s poll.
- Redis key prefix shape (D-19) — planner aligns with Phase 3 audit conventions.
- Whether `ach login --deployment <name>` interactive resume uses readline-style editing or simple `bufio.Scanner` — planner picks (mirror existing internal/platformapi flow if any).
- Exit-code constants location — `internal/cli/exit.go` recommended; planner finalizes.
- HTTP client timeout defaults (login poll vs hydrate vs admin) — planner picks; recommend 60s default, 5 min for login poll session.
- Render package shape — single `internal/cli/render` vs per-subcommand format funcs. Planner picks.
- Test harness shape for device-code in unit tests (httptest mock for `/platform/auth/cli/{init,token}`) — planner picks.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### CLI spec (authoritative for §3, §4, §5, §6.6, §9.3)
- `spec/ach_cli_spec_v20260515_FINALv4.md` §3.1–§3.5 — config sources, registry shape, synthetic mode, env vars, flags
- `spec/ach_cli_spec_v20260515_FINALv4.md` §4 — `pk_`/`ek_` choice + plaintext lifecycle
- `spec/ach_cli_spec_v20260515_FINALv4.md` §5.1 — `ach login` (Dex SSO flow + UX + synthetic-mode rejection)
- `spec/ach_cli_spec_v20260515_FINALv4.md` §5.2 — `ach logout`
- `spec/ach_cli_spec_v20260515_FINALv4.md` §5.3 — `ach whoami --verify` asymmetric per key type
- `spec/ach_cli_spec_v20260515_FINALv4.md` §5.4 — `ach config` 5 commands + mask/--reveal
- `spec/ach_cli_spec_v20260515_FINALv4.md` §5.5 — `ach env list/describe` (two-call admin-403 graceful)
- `spec/ach_cli_spec_v20260515_FINALv4.md` §5.6 — `ach env-keys` (note: D-07 deviation from `--save-as`)
- `spec/ach_cli_spec_v20260515_FINALv4.md` §5.7 — `ach hydrate` (Phase 6 ships POST + warning + mutex creds only)
- `spec/ach_cli_spec_v20260515_FINALv4.md` §5.10 — `ach admin` (exit-3 on 403 not_admin)
- `spec/ach_cli_spec_v20260515_FINALv4.md` §6.6 — pk_ stderr warning text
- `spec/ach_cli_spec_v20260515_FINALv4.md` §9.3 — 9-code exit matrix (Phase 6 implements 0/1/3/6/8; 2/4/5/7 are Phase 7)

### Hub spec (referenced for endpoint contracts CLI consumes)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §7 + §8 — pk_/ek_ lifecycle (CLI mirrors the §7.1 prefix + §8.1 binding rules)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §15.1 — `POST /platform/hydrate` contract (Phase 6 `ach hydrate` consumer)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §15.4 — config file is local trust artifact (authorizes pk_ plaintext on disk)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §15.5 — error envelope `{error:{code,message}, request_id}` (CLI surfaces these on every 4xx/5xx)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §18 — admin allowlist contract (CLI consumes `403 not_admin`)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §18.2 — outcome enum (CLI exit-code mapping anchors here)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §16 — `pkid_`/`ekid_` key ID prefixes (`ach admin keys revoke` accepts both; raw plaintext rejected)

### Project planning
- `.planning/ROADMAP.md` §"Phase 6: CLI Foundation" — goal + 5 success criteria + 13 REQ refs
- `.planning/REQUIREMENTS.md` CLI-01..CLI-13 — testable acceptance criteria for every subcommand
- `.planning/PROJECT.md` "Core Value" + Constraints — CLI spec change (§13 OS keyring deferred, plaintext-on-disk authorized)
- `.planning/phases/03-hub-identity-platform-api/03-CONTEXT.md` — Platform API endpoint surface CLI consumes (keystore, audit handler, error envelope, admin allowlist mechanics)
- `.planning/phases/04-hub-forwarder-jwt-trust-path/04-CONTEXT.md` — `keystore.KeyResolver` + `TeamsResolver` (CLI doesn't call these directly, but the asymmetric whoami contract depends on the runtime resolution semantics they established)
- `.planning/phases/05-content-service-cross-component-observability/05-CONTEXT.md` — `/content/{kind}/{name}` surface (`ach content fetch` is Phase 7 but the contract matters for `ach env describe` artifact rendering)

### User draft plans in `docs/plans/` (NOT in `.planning/`, dated 2026-05-26)
- `docs/plans/2026-05-26-cli-commands.md` — 1645-line v1 CLI plan (paste-token + JSON config + 4 subcommands). **Superseded by D-01 (Intermediate A) — kept as wire-format reference: Tasks 1, 3, 7, 11 (golden-diff e2e shape) are reusable; Tasks 2, 4, 5, 6, 8, 9, 12 are obsolete because the full Dex flow + multi-deployment yaml replaces them.**
- `docs/plans/2026-05-25-ach-bootstrap.md` — single-binary cobra layout decision (Phase 1 origin)
- `docs/plans/2026-05-25-ach-domain-port.md` — single-binary refactor patterns (Phase 1 origin)

### Existing code surfaces Phase 6 wires into (read before any new file)
- `cmd/ach/main.go` — entrypoint shape
- `cmd/ach/cmd/root.go` — cobra root, Version ldflag injection point
- `cmd/ach/cmd/migrate.go` — smallest existing subcommand; canonical style template per draft Task 3
- `cmd/ach/cmd/platform_api.go` — larger subcommand showing flag-binding + config-validation idioms
- `internal/platformapi/server.go` — chi.Mux route surface (CLI calls every Authn-gated route here)
- `internal/platformapi/auth/sso.go` — `LoginHandler` + `CallbackHandler` (D-20 extends this with session_id awareness)
- `internal/platformapi/auth/cookies.go` — `__Host-ach_sso` cookie semantics (preserved by D-20)
- `internal/platformapi/middleware/` — `KeyContextFromCtx`, `ActorFromCtx`, `RequestIDFromCtx` (CLI's verbose log redaction mirrors what the server already does)
- `internal/platformapi/envkeys/handler.go` — `CreateRequest`, `CreateResponse`, `EkRowView`, `ListResponse` wire shapes
- `internal/platformapi/envkeys/mount.go` — confirms route surface CLI hits
- `internal/platformapi/environments/` — `ach env list`/`describe` consumer
- `internal/platformapi/hydrate/handler.go` — `HydrateResponse` shape `ach hydrate` consumes
- `internal/platformapi/admin/` — `ach admin {keys revoke, users revoke-keys, refresh}` consumer
- `internal/keys/` — `pk_`/`ek_` prefix validation (CLI mirrors for client-side rejection of raw plaintext on revoke)
- `internal/audit/handler.go` — audit emission convention for the new `cli_login` action (D-19)
- `examples/hydrate-demo.sh` — wire-format authority (deleted Wave 3)
- `examples/hydrate.json` — golden artifact for D-18 e2e diff

### Toolchain + dev loop (CLAUDE.md, MANDATORY)
- `CLAUDE.md` §"Toolchain — host has NO Go" — every `go`/`make` prefixed `./scripts/dev.sh`
- `CLAUDE.md` §"Test phases" — `unit`, `envtest-run`, `e2e-focus`, `e2e-full` taxonomy
- `CLAUDE.md` §"E2E debug loop" — kept-cluster iteration pattern for D-18
- `CLAUDE.md` §"Publication — pre-commit and pre-push gates" — 17-gate; SPDX header per `*.go`; govulncheck ack-list discipline
- `CLAUDE.md` §"Repository-specific patterns" — single-binary cobra layout (D-01 honors this verbatim)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- **cobra root + 5 existing subcommands** at `cmd/ach/cmd/{operator,platform_api,forwarder,content_service,migrate}.go` — proven `init() { rootCmd.AddCommand(...) }` pattern; the 6 new CLI subcommands slot in 1:1.
- **Platform API endpoint surface (Phase 3)** is complete for everything Phase 6 needs except the device-code endpoints — `internal/platformapi/server.go:135-191` shows every route already mounted: `/platform/auth/login`, `/sso/callback`, `/hydrate`, `/env-keys`, `/environments`, `/admin/*`. D-19 adds two endpoints under a new chi sub-router `/platform/auth/cli/{init,token}`.
- **`keystore.KeyResolver`** (Phase 3 D-08) handles `pk_`/`ek_` resolution including the §7.1 sliding-window CTE; the CLI doesn't touch this directly but every endpoint it calls already does.
- **`internal/audit/handler.go` slog handler** (Phase 2 D-17) for the new `cli_login` audit event (D-19).
- **`internal/keys/` package** — `pk_`/`ek_` prefix validation reusable for client-side `ach env-keys revoke` plaintext rejection (D-07-adjacent).
- **`net/http/httptest`** for hermetic unit tests of every subcommand's HTTP behavior (per draft Task 3+ pattern, still applicable).

### Established Patterns
- **Single-binary cobra layout** — Phase 1 + CLAUDE.md §"Repository-specific patterns" lock this. All Phase 6 subcommands go under `cmd/ach/cmd/<verb>.go`, NEVER as a second `cmd/<x>/main.go` tree.
- **SPDX-only license headers** — every `*.go` outside `vendor/`/`zz_generated*`/`mock_*` starts with `// SPDX-License-Identifier: Apache-2.0`. Pre-push gate enforces.
- **TDD discipline (red → green → refactor)** — Phase 3/4/5 set the precedent; every subcommand lands with `*_test.go` first.
- **Error envelope decoding** — `internal/platformapi/middleware/` writes `{error:{code,message},request_id:"req_..."}`; the shared CLI HTTP client decodes this once and surfaces `.error.code` as the exit-code anchor (matching spec §18.2 outcome enum). Phase 4 D-21 pinned this shape.
- **Exit code mapping pattern** — recent CLI tooling around the repo uses early-return + sentinel error pattern; planner should establish a single `internal/cli/exit.go` defining the 5 Phase-6 codes (0/1/3/6/8) as typed constants + a `mapServerError(*Response) ExitCode` helper.
- **Devtools container toolchain** — `./scripts/dev.sh go test ./internal/cli/...`, `./scripts/dev.sh make e2e-focus FOCUS=TestCLILoginHydrate`. Host has no Go (CLAUDE.md §"Toolchain").

### Integration Points
- **`/platform/auth/cli/{init,token}` mounting** in `internal/platformapi/server.go` — slots into the existing chi.Mux **outside** the `Authn`-gated `chi.Group` because the init endpoint is anonymous (it's the start of the auth flow) and the token endpoint authenticates via the session_id alone.
- **`/platform/auth/sso/callback` extension** (D-20) — the existing handler in `internal/platformapi/auth/sso.go` needs to look up a `session_id` (from the Dex callback state param or a fresh query/cookie value) and, if found, write the pk_ to Redis instead of returning JSON to the browser. The non-CLI path (`hydrate-demo.sh` and existing test/e2e/phase3 assertions) is preserved by absence-of-session-id branch.
- **Redis schema namespace** — Phase 6 introduces `ach:cli-session:<session_id>` under the existing Redis instance (Phase 1 D-?). No new Redis deployment.
- **Helm chart values** — no new toggles; the `platform-api` Deployment that already exists picks up the new endpoints automatically once code lands. No new ServiceMonitor / RBAC delta.
- **`test/e2e/`** — D-18 adds `cli_login_hydrate_test.go` alongside existing `phase{2,3,4,5}_invariants_test.go`. Slots into the `make e2e` umbrella with no Makefile changes.
- **README.md + CLAUDE.md** — Wave 3 plan updates the "Common failure modes" entries that today reference `hydrate-demo.sh` to instead show `ach login` + `ach hydrate --environment demo`.

</code_context>

<specifics>
## Specific Ideas

- **Demo collapse target:** `ach login` + `ach hydrate --environment demo > hydrate.json` reproduces `examples/hydrate.json` byte-for-byte. That single-line replacement of the 139-line `hydrate-demo.sh` is the headline-demo-able outcome of Phase 6.
- **Spec divergence flagging:** D-07 (always-persist ek_) is the ONLY intentional spec deviation in Phase 6. When the Wave 2 plan that ships it commits, it must (a) update CLI-09 + AC4 status in `.planning/REQUIREMENTS.md` with a "deviation" marker, (b) add a changelog note in `spec/ach_cli_spec_v20260515_FINALv4.md` (or its successor) documenting the `--save-as` → always-persist+`--no-save` swap. Per CLAUDE.md "Documentation hygiene": this happens in the same commit as the code change, not a follow-up.
- **Multi-deployment yaml from day 1** is the key anti-rework decision. JSON-then-yaml swap mid-phase (Intermediate C) was the single most-expensive rework risk; locking yaml from W1 eliminates it.
- **Device-code over query-string redirect** was chosen specifically because the existing `__Host-ach_sso` cookie is `Secure+HttpOnly+SameSite=Strict` — a localhost callback listener competes with the cookie's strict policy. Polling sidesteps the cookie story entirely; the CLI doesn't need a cookie jar at all.

</specifics>

<deferred>
## Deferred Ideas

- **OS keyring backend** for pk_/ek_ storage — spec §13 deferral acknowledged; v1beta1 candidate.
- **`ach platforms list`** (CLI spec §5.8) — Phase 7 with the 4 adapters.
- **`ach content fetch`** (CLI spec §5.9) — Phase 7 (or Phase 6b if a debug primitive proves needed during execution).
- **`--output-format json`** on list/describe — Phase 7 or a Phase 6b polish window.
- **`--verbose` log-to-file** at `~/.cache/ach/logs/ach-<rfc3339>.log` — Phase 7 or 6b.
- **`ach version` dedicated subcommand** — cobra's implicit `--version` suffices for v1alpha1; richer output (build info, go version, OS/arch) is Phase 7.
- **`ach hook emit`** — out-of-scope per Hub v1alpha1 (PROJECT.md "Out of Scope").
- **Offline `ach status`** — out-of-scope per CLI spec §13.
- **Server-side `GET /platform/whoami` introspection endpoint** — punted to v1beta1 by spec §13; Phase 6 stays with asymmetric verify (D-13).
- **Browser-native `--sso` flow (query-string redirect via localhost listener)** — alternative to D-02; deferred indefinitely. Device-code is the single Phase 6 SSO mechanism.
- **Dual-key acceptance window for the Forwarder↔LiteLLM shared key** — Hub §20 v1beta1 backlog (carried forward from Phase 4).

</deferred>

---

*Phase: 06-cli-foundation*
*Context gathered: 2026-05-28*
