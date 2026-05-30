# Phase 6: CLI Foundation - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-05-28
**Phase:** 06-cli-foundation
**Areas discussed:** scope shape, ach login SSO mechanism, multi-deployment registry, demo + e2e, scope edges

---

## Phase scope shape

Initial framing question: ROADMAP+REQ Phase 6 covers 13 CLI- REQs + 5 SCs (`ach login` Dex SSO, `whoami --verify`, `env list/describe`, `env-keys` CRUD + `--save-as`, `hydrate` with pk_ warning + mutex creds, admin commands, synthetic mode, multi-deployment registry, `ach config` CRUD). User has a 1645-line draft at `docs/plans/2026-05-26-cli-commands.md` that ships a smaller v1 (4 subcommands, JSON config, paste-token only).

Three options framed; user pushed back asking for "intermediate" with Dex SSO mandatory and an honest cost estimate.

| Option | Description | Selected |
|--------|-------------|----------|
| Intermediate A — Full spec, 1 phase, 3 waves | Wave 1 internals + Dex login + whoami; Wave 2 env/env-keys/hydrate/config/logout; Wave 3 synthetic + admin + e2e umbrella + demo deletion. ~9 plans. | ✓ |
| Intermediate B — Demo-fast Phase 6 + REQ-debt Phase 6b | Phase 6 = W1+W2 (~6 plans, CLI-01..09,11,13 closed). Phase 6b inserts synthetic + admin + ach config CRUD. Smaller phases, roadmap edit. | |
| Intermediate C — Draft Wave 1 verbatim + spec retrofit Wave 2 | Wave 1 = draft plan exactly (JSON config, --token paste). Wave 2 retrofits yaml + Dex + everything else. Cheapest first PR; biggest total rework (schema swap). | |

**User's choice:** Intermediate A.
**Notes:** User confirmed Dex SSO is mandatory; user asked whether full spec is "sencillo" enough to ship — confirmed by Claude's estimate (~9 plans at recent ~6.6 min/plan velocity = ~90 min raw exec; Dex device-code is known-pattern, multi-deployment yaml day-1 eliminates schema migration).

---

## ach login SSO mechanism

After locking Intermediate A (full Dex SSO required), drilled into the handoff mechanism.

### Sub-question 1: how pk_ travels browser → CLI on Dex callback

| Option | Description | Selected |
|--------|-------------|----------|
| Query-string redirect | /platform/auth/sso/callback honors ?cli_callback=http://127.0.0.1:<port>/cb + state nonce. Server 302s browser to localhost listener with token=pk_xxx in query. ~30 LOC server + ~80 LOC client. Pattern: gh, gcloud. Trade: pk_ in browser URL history. | |
| One-time exchange code | Callback redirects to localhost with random code; pk_ stashed server-side keyed by code (Redis 60s TTL). CLI POSTs /platform/auth/cli/exchange {code, state}. ~150 LOC server + ~100 LOC client. pk_ never in URL. | |
| Polling (device-code-style) | CLI POSTs /platform/auth/cli/init → {session_id, verification_url}. Browser opens, server stores pk_ keyed by session_id. CLI polls /token endpoint. ~180 LOC server + ~90 LOC client. Works behind restrictive networks. | ✓ |

**User's choice:** Polling (device-code-style).
**Notes:** Most secure (pk_ never in URL); no localhost listener requirement; aligns with OAuth 2.0 Device Authorization Grant. Locked in CONTEXT.md as D-02.

### Sub-question 2: ach login UX shape

| Option | Description | Selected |
|--------|-------------|----------|
| Spec verbatim | Interactive prompts for deployment name + URL; opens browser; on success prints SSO email + masked pk_ tail. --no-browser prints URL and waits. Callback timeout → exit 1 with partial entry persisted. Matches CLI-01 + AC1. | ✓ |
| Non-interactive only | --deployment + --base-url required; no prompts; CI-friendly. Spec allows but drops interactive path. | |
| Hybrid — flags win, prompt fallback | Flags override prompts; missing flags → prompts. Mirrors gh auth login. | |

**User's choice:** Spec verbatim.
**Notes:** Locked as D-03. Hybrid flag-or-prompt is technically the same shape (flags supplied → prompts skip silently per spec); spec verbatim covers both.

---

## Multi-deployment registry (auto-locked by scope choice A)

Intermediate A choice implied full multi-deployment yaml + ach config CRUD day 1. Confirmed sub-shape with two follow-ups.

### Sub-question 1: ek_ storage in config.yaml

| Option | Description | Selected |
|--------|-------------|----------|
| --save-as opt-in only (spec) | ach env-keys create --save-as <local-label> writes plaintext. Without --save-as, plaintext to stdout once. Spec §3.2 production posture: dev-convenience only. Matches CLI-09 + AC4. | |
| Always persist + --no-save flag | ach env-keys create always writes to deployments.<active>.ek.<auto-label> unless --no-save. Inverted default. Deviates from spec. | ✓ |

**User's choice:** Always persist + --no-save flag.
**Notes:** **INTENTIONAL SPEC DEVIATION** from CLI-09 + AC4. User decided always-persist is more ergonomic for v1alpha1 single-developer audience. Locked as D-07. Must be flagged in REQUIREMENTS.md + CLI spec changelog when shipped.

### Sub-question 2: ach config use — default-selector surface

| Option | Description | Selected |
|--------|-------------|----------|
| Spec verbatim 5 commands | ach config list/show/use/remove/rename. show masks pk_/ek_; --reveal opt-in for named deployment only. None contact server. None available in synthetic mode. | ✓ |
| Minimal 3 commands | ach config list/use/remove only. Drops show + rename. Saves ~1 plan, breaks spec parity. | |

**User's choice:** Spec verbatim 5 commands.
**Notes:** Locked as D-05.

### Sub-question 3: always-persist ek_ — label source

| Option | Description | Selected |
|--------|-------------|----------|
| Server name (--name flag) | Server --name doubles as local config key: deployments.<active>.ek.<name>. Natural; collisions overwrite. | ✓ |
| Auto-label | If --name omitted, generate ek-<timestamp>. | |
| Separate --label flag | Two identifiers (server alias + local key). Closest to spec semantics. | |

**User's choice:** Server name (--name flag).
**Notes:** Locked as D-07 (label source clarification).

### Sub-question 4: synthetic mode + ach env-keys create

| Option | Description | Selected |
|--------|-------------|----------|
| Always exits 1 in synthetic | Synthetic has no config file; any write-config command rejected. --no-save bypass for CI piping. | ✓ |
| Auto --no-save in synthetic | Synthetic auto-applies --no-save silently; command succeeds. More ergonomic but silent behavior change. | |

**User's choice:** Always exits 1 in synthetic (with --no-save bypass).
**Notes:** Locked as D-08.

---

## Demo collapse + Go e2e fixture

### Sub-question 1: examples/hydrate-demo.sh fate

| Option | Description | Selected |
|--------|-------------|----------|
| Delete in Wave 3 | Once `ach login` + `ach hydrate` produce examples/hydrate.json byte-for-byte, shell is deleted. README + CLAUDE.md updated. | ✓ |
| Keep as backstop | Shell stays as server-side wire-format canary (no CLI dep). CLI demo alongside: examples/hydrate-cli-demo.sh. Two demos. | |
| Replace with go-driven runnable | Delete shell AND golden. Replace with examples/cli-demo/main.go driving CLI via os/exec. Compiled, type-safe, heavier. | |

**User's choice:** Delete in Wave 3.
**Notes:** Locked as D-17.

### Sub-question 2: CLI e2e shape

| Option | Description | Selected |
|--------|-------------|----------|
| test/e2e/cli_login_hydrate_test.go against kept cluster | Single stdlib test against `make cluster-keep`. Device-code login → hydrate → byte-for-byte golden diff + env/env-keys/whoami smoke. Slots into TestPhase6CLI umbrella. Matches draft Task 11. | ✓ |
| Hermetic httptest server | internal/cli/*_test.go uses httptest mock. Fast, no cluster, no Dex. Catches contract drift but not real-world wire format. | |
| Both — hermetic unit + cluster e2e | Hermetic for every subcommand's HTTP behavior PLUS cluster e2e for golden-diff. ~2x test code. | |

**User's choice:** test/e2e/cli_login_hydrate_test.go against kept cluster.
**Notes:** Locked as D-18. Hermetic httptest unit tests aren't excluded — they're the default `*_test.go` neighbor of every subcommand in the natural TDD flow; the explicit "kept cluster" pick is for the umbrella e2e that diffs against the golden.

---

## Scope edges

### Sub-question 1: ach platforms list + ach content fetch — Phase 6 or 7?

| Option | Description | Selected |
|--------|-------------|----------|
| Phase 7 (recommended) | Both deferred to Phase 7. ach platforms list naturally lands with adapters; ach content fetch is a debugging primitive. | ✓ |
| Pull into Phase 6 | ach platforms list as static stub + ach content fetch as thin GET wrapper. +1-2 plans. | |
| Split — content in 6, platforms in 7 | content fetch independent of adapters, useful debugging primitive. platforms stays Phase 7. +0.5 plan. | |

**User's choice:** Phase 7 (recommended).
**Notes:** Locked in CONTEXT.md `<deferred>`.

### Sub-question 2: verbose logging + --output-format json

| Option | Description | Selected |
|--------|-------------|----------|
| Full spec output discipline | --verbose / -v / ACH_VERBOSE with debug logs to ~/.cache/ach/logs/. --output-format json on list/describe. ~1 plan. | |
| Minimal output, defer json mode | Plain text only. --verbose to stderr. No --output-format json. Defer to Phase 6b/7. | ✓ |
| JSON mode now, verbose later | Ship --output-format json (cheap). Defer log-to-file. ~0.5 plan. | |

**User's choice:** Minimal output, defer json mode.
**Notes:** Locked as D-15. Spec §9.1–9.4 full discipline is Phase 7 polish.

---

## Claude's Discretion

Planner-level details delegated (per D-02 + D-19 etc.):

- Session TTL exact value (recommend 5 min) and poll interval (recommend 2s) — D-02.
- Redis key prefix shape under `ach:cli-session:` — D-19.
- Whether interactive prompts use readline-style editing or simple `bufio.Scanner` — D-03.
- Exit-code constants location (`internal/cli/exit.go` recommended).
- HTTP client timeout defaults (60s default, 5 min for login poll) — D-04.
- Render package shape (single `internal/cli/render` vs per-subcommand format funcs).
- Test harness for device-code in unit tests (httptest mock pattern).
- Whether `cli_login` audit action reuses existing `key.login` or gets its own action name — D-19.

## Deferred Ideas

Captured in CONTEXT.md `<deferred>` section:

- OS keyring backend (v1beta1).
- `ach platforms list` (Phase 7).
- `ach content fetch` (Phase 7 or 6b debug primitive).
- `--output-format json` (Phase 7 or 6b polish).
- `--verbose` log-to-file at `~/.cache/ach/logs/` (Phase 7 or 6b).
- `ach version` dedicated subcommand (cobra `--version` suffices for v1alpha1).
- `ach hook emit` (out-of-scope per Hub v1alpha1).
- Offline `ach status` (out-of-scope per CLI spec §13).
- Server-side `GET /platform/whoami` introspection endpoint (v1beta1).
- Browser-native `--sso` flow via localhost listener (deferred indefinitely; device-code is the single SSO mechanism).
- Dual-key acceptance window for Forwarder↔LiteLLM shared key (Hub §20 v1beta1, carried from Phase 4).
