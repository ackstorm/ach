---
phase: 06-cli-foundation
plan: 06
type: execute
wave: 2
depends_on:
  - 06-01-cli-shared-internals
  - 06-03-ach-login-whoami-logout
files_modified:
  - cmd/ach/cmd/hydrate.go
  - cmd/ach/cmd/hydrate_test.go
autonomous: true
requirements:
  - CLI-03
  - CLI-05
  - CLI-06
  - CLI-09

must_haves:
  truths:
    - "`ach hydrate --environment <name>` POSTs `/platform/hydrate {environment: <name>}` carrying `x-ach-key: <pk_ or ek_>` (CLI-03)"
    - "`ach hydrate` writes the HydrateResponse JSON to stdout byte-for-byte via `httpclient.Client.DoRaw` + `io.Copy(os.Stdout, resp.Body)` — no transformation; matches examples/hydrate.json golden"
    - "`ach hydrate` consumes `httpclient.Client.DoRaw` from 06-01 (foundation API, NOT extended here)"
    - "`ach hydrate` with pk_ emits the §6.6 stderr warning BEFORE the HTTP call; suppressed by --no-warnings (CLI-05)"
    - "`ach hydrate` with pk_ REQUIRES --environment (or ACH_ENVIRONMENT); missing → exit 1 with the spec-mandated message (CLI-06)"
    - "`ach hydrate` with ek_ accepts --environment OPTIONALLY (CLI-06)"
    - "Mutually-exclusive credential sources `--api-key`/`--env-key`/ACH_API_KEY/ACH_ENV_KEY → >1 present → exit 1 with conflict list (CLI-09); explicit closed list of accepted sources, NO flag-aliasing"
    - "Phase 6 does NOT implement on-disk write, diff, state.json, adapter dispatch — those are Phase 7 (D-09)"
  artifacts:
    - path: "cmd/ach/cmd/hydrate.go"
      provides: "ach hydrate cobra subcommand — POST + stdout JSON"
      contains: "var hydrateCmd"
  key_links:
    - from: "cmd/ach/cmd/hydrate.go"
      to: "internal/platformapi/hydrate/handler.go HydrateResponse"
      via: "POST /platform/hydrate → decode + re-encode to stdout"
      pattern: "HydrateResponse"
    - from: "cmd/ach/cmd/hydrate.go"
      to: "examples/hydrate.json"
      via: "byte-for-byte golden match (W3-P3 e2e diffs this)"
      pattern: "examples/hydrate.json"
---

<objective>
Ship `ach hydrate --environment <name>` — the **headline demo
target** of Phase 6. This command POSTs `/platform/hydrate` with the
resolved credential and writes the response JSON to stdout
byte-for-byte. Replacement for the 139-line `examples/hydrate-demo.sh`
shell driver (the demo collapse lands in W3-P3).

This plan ships the surface-only Phase 6 form per D-09:
- Mutex credential enforcement (CLI-09 / D-11).
- pk_ stderr warning per CLI-05 (D-10 / spec §6.6).
- --environment required-for-pk_ / optional-for-ek_ (CLI-06 / D-12).

The full hydrate engine (concurrency lock, atomic state.json v2,
dual-hash drift, adapter dispatch, safe extraction, --include-runtime
/ --only-runtime / --sync / --force / --dry-run) is Phase 7 (CLI-14
to CLI-21, STATE-*). DO NOT scope creep here.

Purpose: This is the W2 deliverable that makes the demo-collapse
promise reachable in W3. The byte-for-byte golden diff vs
`examples/hydrate.json` is the W3 e2e umbrella's anchor.

Output: 1 new command file + 1 test file under `cmd/ach/cmd/`.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/phases/06-cli-foundation/06-CONTEXT.md
@.planning/phases/06-cli-foundation/06-PATTERNS.md
@spec/ach_cli_spec_v20260515_FINALv4.md
@spec/ach_hub_spec_v20260515_FINALv4.md
@CLAUDE.md
@cmd/ach/cmd/migrate.go
@internal/platformapi/hydrate/handler.go
@examples/hydrate.json
@examples/hydrate-demo.sh
@.planning/phases/06-cli-foundation/06-01-SUMMARY.md
@.planning/phases/06-cli-foundation/06-03-SUMMARY.md
@.planning/phases/06-cli-foundation/06-04-SUMMARY.md
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Author `ach hydrate` cobra subcommand</name>
  <files>
    cmd/ach/cmd/hydrate.go
    cmd/ach/cmd/hydrate_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-09" (Phase 6 surface only — no engine), §"D-10" (pk_ warning), §"D-11" (mutex creds), §"D-12" (--environment required for pk_), §"D-15" (--verbose discipline)
    - 06-PATTERNS.md §"Pattern P2" (leaf cobra subcommand) + §"Pattern P4" (mutex creds + env-var bag) + §"Pattern P5" (httpclient consumer)
    - spec/ach_cli_spec_v20260515_FINALv4.md §5.7 (ach hydrate Phase 6 form), §6.1 (mutex creds), §6.6 (pk_ stderr warning verbatim text)
    - spec/ach_hub_spec_v20260515_FINALv4.md §15.1 (POST /platform/hydrate contract)
    - internal/platformapi/hydrate/handler.go lines 56-108 (HydrateRequest{Environment string}, HydrateResponse{SchemaVersion, Environment, Runtime, Context})
    - examples/hydrate.json (the golden artifact — confirm format: pretty-printed 2-space indent or compact? Match server's render.JSON output discipline)
    - examples/hydrate-demo.sh (existing wire-format authority; understand what the shell driver emits so the CLI matches byte-for-byte)
    - 06-01-SUMMARY.md (httpclient.Client API)
    - 06-04-SUMMARY.md (render.HydrateView shape if reused; or independent decode)
  </read_first>
  <behavior>
    - Test 1: ach hydrate --environment demo with --api-key pk_xyz against an httptest server returning a sample HydrateResponse → stdout byte-equals the server's response body. The httptest mock should return the exact bytes of examples/hydrate.json (assuming the golden was produced by the server's render.JSON — verify by reading the file in the test setup). NOTE: byte-for-byte means the CLI MUST NOT re-marshal — it should `io.Copy(os.Stdout, resp.Body)` after a status-check, or buffer and tee. Pick the simpler route: HEAD-check status, then stream body to stdout.
    - Test 2: ach hydrate --environment demo --api-key pk_xyz emits the §6.6 stderr warning BEFORE the HTTP call (assert via stderr capture). Warning text per spec §6.6 — extract verbatim into a package-level const.
    - Test 3: ach hydrate --environment demo --api-key pk_xyz --no-warnings does NOT emit the warning (assert stderr is empty of the warning text).
    - Test 4: ach hydrate --api-key pk_xyz (NO --environment, NO ACH_ENVIRONMENT) → exit 1 with stderr "--environment is required when using a pk_ key". Counter on /platform/hydrate is 0 (client-side check; D-12).
    - Test 5: ach hydrate --env-key local-laptop (where deployments.prod.ek.local-laptop = "ek_xyz") → resolves to "ek_xyz" and POSTs WITHOUT requiring --environment. No pk_ warning. Exit 0.
    - Test 6: ach hydrate --env-key local-laptop --environment demo (ek_ + --environment) → both sent in request body; server-side mismatch yields 403 wrong_environment → exit 3 (per CLI exit-code map for 403).
    - Test 7: ach hydrate --api-key pk_xyz --env-key local-laptop → exit 1 with stderr listing both as conflicting credentials (D-11). Same for any 2+ of {--api-key, --env-key, ACH_API_KEY, ACH_ENV_KEY}.
    - Test 8: ach hydrate --environment demo when neither pk_ nor ek_ is resolvable (no flags, no env, no config) → exit 1 with stderr "no credential resolved; run `ach login` or set ACH_API_KEY".
    - Test 9: ach hydrate --environment demo in synthetic mode with ACH_BASE_URL + ACH_API_KEY set → POSTs to ACH_BASE_URL with the env-resolved pk_; exit 0. NOTE: hydrate WORKS in synthetic mode (unlike login/config/logout/env-keys create); D-11 calls --env-key/ACH_ENV_KEY unavailable in synthetic — assert that synthetic + --env-key → exit 1.
    - Test 10: 503 from server → exit 6 (network); 401 → exit 3 (authN); 400 missing_environment → exit 1 (general).
    - Test 11: Output byte-comparison: capture stdout, compare against the canned HydrateResponse the server returned. Equal-on-byte assertion.
  </behavior>
  <action>
    Author `cmd/ach/cmd/hydrate.go` mirroring Pattern P2 + P4 + P5:
    - File-level docstring citing D-09 (surface only), D-10 (pk_ warning), D-11 (mutex), D-12 (--environment required for pk_).
    - Package-level constant for the spec §6.6 warning text. Extract verbatim from the spec — text starts approximately:
      ```
      WARNING: ach hydrate is running with a personal key (pk_).
      Hydrate against a personal key reflects YOUR full set of authorized
      environments. For agent-runtime workflows in production, prefer an
      environment key (ek_) — see `ach env-keys create`.
      ```
      (Pull the exact spec §6.6 text in this task by reading the spec file fully and copying verbatim. Wrap as a `const pkWarning = "..."`.)
    - Flags:
      - `--environment <name>` (string) — required for pk_, optional for ek_.
      - `--no-warnings` (bool) — suppress §6.6 stderr warning.
      - `--verbose` (bool) — header dump to stderr.
      - `--api-key <pk_…>` (string) — credential override.
      - `--env-key <local-label>` (string) — credential override (resolves against deployments.<active>.ek.<label>; rejected in synthetic mode).
      - `--deployment <name>` (string) — override resolution.
    - RunE flow:
      1. Mutex credential check (D-11): collect non-empty values of --api-key, --env-key, ACH_API_KEY, ACH_ENV_KEY. If len ≥ 2 → CodedError(General, "conflicting credential sources: <list>"). Do this BEFORE config.Load to avoid wasted I/O.
      2. Synthetic-mode detection (inline same as login): if ACH_BASE_URL && (ACH_API_KEY OR --api-key) → synthetic=true. In synthetic: --deployment + ACH_DEPLOYMENT + --env-key + ACH_ENV_KEY are forbidden (exit 1 if set).
      3. Resolve credential. Branch by classification:
         - pk_: bear key; deployment URL comes from synthetic ACH_BASE_URL OR config.ResolveActive.
         - ek_: same; key sourced from --api-key/ACH_API_KEY (raw ek_ allowed via --api-key per spec) OR --env-key/ACH_ENV_KEY (label resolved from config).
         - Neither / empty → CodedError(General, "no credential resolved").
      4. --environment required-for-pk_ enforcement (D-12): if pk_ AND --environment == "" AND ACH_ENVIRONMENT == "" → CodedError(General, "--environment is required when using a pk_ key").
      5. If pk_ AND !--no-warnings: write pkWarning to stderr.
      6. Compose httpclient.Client{BaseURL: ..., APIKey: ..., Verbose: verbose}.
      7. Build request body: `map[string]string{"environment": <name>}` IF --environment OR ACH_ENVIRONMENT non-empty; else `struct{}{}` (ek_ + no env).
      8. Issue POST /platform/hydrate via `c.DoRaw(ctx, "POST", "/platform/hydrate", body)`. Per 06-01 (W2 fix), DoRaw is part of the foundation API — NO conditional extension here. DoRaw returns the live *http.Response with Body unread on 2xx and *httpclient.ServerError on non-2xx (Body consumed for envelope decode).
      9. On 200: `defer resp.Body.Close(); io.Copy(os.Stdout, resp.Body)`. Exit 0. The CLI MUST NOT re-marshal — byte-for-byte equivalence with the server's response is the W3-P3 golden-diff anchor.
      10. On non-2xx (*ServerError from DoRaw): return to main.go for exit-code mapping.

    NOTE on byte-for-byte: the server-side render.JSON pretty-prints with 2-space indent (verify from `internal/platformapi/render/json.go` — the existing render.JSON helper sets `enc.SetIndent("", "  ")` based on the Phase 3 contract). The CLI's `io.Copy` preserves the server's exact bytes including trailing newline if present. The W3-P3 e2e golden-diff test depends on this.

    Tests use httptest with a handler that reads `examples/hydrate.json` and writes it as the response body (or a fixture variant). Capture stdout via os.Pipe swap or cobra's SetOut. Assert `bytes.Equal(captured, expected)`.

    Register with `init() { rootCmd.AddCommand(hydrateCmd) }`.

    SPDX header on every new file.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestHydrate"</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestHydrate"` exits 0.
    - Source assertion: `grep -c '"/platform/hydrate"' cmd/ach/cmd/hydrate.go` returns ≥ 1.
    - Source assertion: `grep -c 'pkWarning\|"WARNING:.*pk_\|§6.6' cmd/ach/cmd/hydrate.go` returns ≥ 1.
    - Source assertion: `grep -c '\-\-no-warnings\|NoWarnings\|noWarnings' cmd/ach/cmd/hydrate.go` returns ≥ 1.
    - Source assertion: `grep -c '\-\-environment\|Environment\s*string' cmd/ach/cmd/hydrate.go` returns ≥ 2.
    - Source assertion: `grep -cE '"\-\-api-key"|"ACH_API_KEY"|"\-\-env-key"|"ACH_ENV_KEY"' cmd/ach/cmd/hydrate.go` returns ≥ 4 (mutex list).
    - Source assertion: `grep -c 'io.Copy\(os.Stdout\|io.Copy(os.Stdout' cmd/ach/cmd/hydrate.go` returns ≥ 1 (raw stream-to-stdout; NOT json re-marshaling).
    - Behavior: Test 11 byte-comparison passes (captured stdout byte-equals canned response).
    - Behavior: pk_ + missing --environment → exit 1 BEFORE any HTTP (httptest counter == 0).
    - Behavior: 2+ mutex credentials → exit 1 BEFORE any HTTP.
    - Behavior: --no-warnings test asserts pkWarning ABSENT from stderr; default (no flag) → present.
  </acceptance_criteria>
  <done>
    hydrate command green; byte-for-byte stdout JSON preserved; mutex credential check before any I/O; pk_ warning gated on flag; synthetic-mode + --env-key rejected.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| CLI process ↔ ~/.config/ach/config.yaml | Reads pk_/ek_ via `config.ResolveActive` (06-01) when neither `--api-key`/`--env-key` nor `ACH_API_KEY`/`ACH_ENV_KEY` is set. |
| Flag/env ↔ credential resolver (closed list) | The mutex-credential list is the closed enum `{--api-key, --env-key, ACH_API_KEY, ACH_ENV_KEY}`. No flag-aliasing; >1 set → exit 1. |
| CLI ↔ network (POST /platform/hydrate) | The single load-bearing request; the response body becomes the byte-for-byte stdout artifact compared against `examples/hydrate.json`. |
| CLI ↔ stdout | DoRaw + io.Copy streams the response body verbatim; no re-marshal, no JSON re-format. |
| CLI ↔ stderr | §6.6 pk_ warning + optional --verbose header dump (redacted). |
| `--env-key` ↔ synthetic mode | `--env-key` / `ACH_ENV_KEY` REJECTED in synthetic mode (D-11). |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-06-06-01 | Spoofing | mutex-credential bypass via flag-aliasing | mitigate | The mutex check uses an EXPLICIT closed list of 4 sources: `--api-key`, `--env-key`, `ACH_API_KEY`, `ACH_ENV_KEY`. No wildcard / no env-prefix scan. Adding a new credential source requires editing the list — drift is visible in code review. Source-assertion gate (`grep -cE` over the four literal names) returns ≥ 4. |
| T-06-06-02 | Tampering | io.Copy buffer-bleed between requests | accept | `io.Copy(os.Stdout, resp.Body)` uses Go's stdlib io implementation; the buffer is local-scope per call. No cross-request bleed possible. Stdlib invariant; not the CLI's responsibility to re-verify. |
| T-06-06-03 | Information Disclosure | `--no-warnings` suppresses a security advisory | accept | `--no-warnings` is the EXPLICIT user opt-out — spec §6.6 mandates the flag exists. The warning is informational (pk_ posture vs ek_ for production); suppression is a documented choice. The flag does NOT suppress any other security gate (mutex check, --environment requirement, synthetic enforcement). |
| T-06-06-04 | Information Disclosure | pk_/ek_ in --verbose stderr dump | mitigate | `httpclient.Redact` (06-01) handles the header redaction. The hydrate response body — which may carry the user's prompts/plugins/artifacts metadata — flows to stdout (intended), NOT stderr. |
| T-06-06-05 | Elevation of Privilege | pk_ used without `--environment` returns cross-environment data | mitigate | CLIENT-side check enforces `--environment` REQUIRED for pk_ (D-12 / CLI-06). The server-side `400 missing_environment` is the backstop, but the CLI refuses to issue the request — exits 1 before any HTTP. Source-assertion gate verifies httptest counter == 0 on the missing-flag path. |
| T-06-06-06 | Tampering | Server bytes mutated mid-stream by io.Copy | accept | TCP-level integrity is provided by TLS to the Platform API. io.Copy is byte-faithful (stdlib). The W3-P3 golden-diff test is the live verification gate. |
| T-06-06-07 | Information Disclosure | ek_ leak via `--api-key` accepting `ek_…` raw form | accept | spec §6.1 permits `--api-key` to carry either pk_ or ek_ (the flag is the credential-type-agnostic carrier). Classification via `keys.ClassifyBearer` routes to the right server path. No information disclosed beyond what the user already had. |
| T-06-06-08 | Repudiation | hydrate not surfaced in CLI audit | mitigate | Server-side `internal/platformapi/hydrate/handler.go` emits the hydrate audit event with actor=key.id + request_id (Phase 3). CLI does not maintain its own audit log; the server is the source of truth. |
| T-06-06-SC | Tampering | npm/pip/cargo installs | mitigate | No new third-party deps; stdlib `net/http`, `io`, `encoding/json` + the foundation packages from 06-01 only. Existing govulncheck ack-list applies. |
</threat_model>

<verification>
After Task 1:

```bash
./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestHydrate"
./scripts/dev.sh go build ./cmd/ach/...
./scripts/dev.sh make lint
```

Smoke (engineer-pending; W3 e2e covers the demo collapse end-to-end):
```bash
./bin/ach login --deployment demo --base-url https://hub.test
./bin/ach hydrate --environment demo > /tmp/hydrate-cli.json
diff -q /tmp/hydrate-cli.json examples/hydrate.json
```

The diff MUST exit 0 (byte-for-byte equality) — this is the W3-P3 e2e anchor.
</verification>

<success_criteria>
- ach hydrate POST returns 200 → byte-for-byte stdout; non-2xx → main.go exit-code mapping.
- pk_ stderr warning present unless --no-warnings.
- pk_ + missing --environment → exit 1 client-side.
- ek_ + --environment optional.
- Mutex credentials enforced; --env-key + synthetic mode rejected.
- examples/hydrate.json byte-equals `ach hydrate --environment demo > out.json` against a Wave-1 Hub.
</success_criteria>

<output>
Create `.planning/phases/06-cli-foundation/06-06-SUMMARY.md` when done. Record:
- The exact spec §6.6 warning text used in the package-level const (W3-P3 e2e may assert this verbatim).
- Confirm `httpclient.Client.DoRaw` is consumed unchanged from 06-01 (no inline extension); io.Copy preserves server bytes verbatim.
- Confirmation that examples/hydrate.json was NOT regenerated in this plan (the golden stays untouched until Phase 7 evolves the wire format).
</output>
