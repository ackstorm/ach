---
phase: 06-cli-foundation
plan: 05
type: execute
wave: 2
depends_on:
  - 06-01-cli-shared-internals
  - 06-03-ach-login-whoami-logout
files_modified:
  - cmd/ach/cmd/env_keys.go
  - cmd/ach/cmd/env_keys_test.go
  - .planning/REQUIREMENTS.md
  - spec/ach_cli_spec_v20260515_FINALv4.md
autonomous: true
requirements:
  - CLI-04
  - CLI-09
  - CLI-10
  - CLI-13

must_haves:
  truths:
    - "`ach env-keys create` ALWAYS persists ek_ plaintext to deployments.<active>.ek.<server-name> per D-07 (deviation from spec §5.6 --save-as opt-in)"
    - "`ach env-keys create --no-save` opts out of persist; ek_ plaintext goes to stdout only (CI / vault workflows)"
    - "`ach env-keys create` in synthetic mode + WITHOUT --no-save exits 1 (D-08)"
    - "`ach env-keys create --no-save` in synthetic mode prints ek_ to stdout and exits 0"
    - "`ach env-keys list` lists rows for the resolved key's identity (server-side filtering already applied by Phase 3 §8.5)"
    - "`ach env-keys revoke <ekid_…>` rejects raw plaintext input (must be ekid_ form) with stderr 400 invalid_argument message (CLI-13)"
    - "`ach env-keys revoke <ekid_…>` prompts interactive confirmation; --yes bypasses (CLI-13)"
    - "REQUIREMENTS.md CLI-09 + AC4 flagged as DEVIATED with reference to D-07 (in the SAME commit per CLAUDE.md docs hygiene)"
    - "spec/ach_cli_spec_v20260515_FINALv4.md carries a changelog note documenting the `--save-as` → always-persist + `--no-save` swap (in the SAME commit)"
    - "ek_ plaintext printed exactly once at env-keys create (CLI-04); never echoed on list or revoke"
  artifacts:
    - path: "cmd/ach/cmd/env_keys.go"
      provides: "3 sub-subcommands (create/list/revoke) under `ach env-keys`"
      contains: "var envKeysCmd"
    - path: ".planning/REQUIREMENTS.md"
      provides: "CLI-09 + AC4 deviation marker pointing at D-07"
      contains: "DEVIATED"
    - path: "spec/ach_cli_spec_v20260515_FINALv4.md"
      provides: "changelog note: --save-as removed; always-persist + --no-save"
      contains: "DEVIATION 2026-05"
  key_links:
    - from: "cmd/ach/cmd/env_keys.go (create)"
      to: "internal/cli/config/config.go"
      via: "config.Save with deployments.<active>.ek[<name>] = ek_..."
      pattern: "config.Save"
    - from: "cmd/ach/cmd/env_keys.go (revoke)"
      to: "internal/keys"
      via: "client-side plaintext rejection mirrors keys.ClassifyBearer's ekid_ prefix check"
      pattern: "EkidKeyIDPrefix\\|ekid_"
---

<objective>
Ship `ach env-keys` (3 sub-subcommands: create / list / revoke)
including the **D-07 always-persist deviation** from spec §5.6. This
is the ONLY intentional spec divergence in Phase 6 — it MUST be
documented in the SAME commit per CLAUDE.md "Documentation hygiene":
- `.planning/REQUIREMENTS.md` CLI-09 + AC4 row marked DEVIATED with
  reference to D-07.
- `spec/ach_cli_spec_v20260515_FINALv4.md` carries a changelog note
  in its top frontmatter / changelog section.

Server-side wire shapes consumed are already shipped by Phase 3
(`internal/platformapi/envkeys/handler.go` lines 93-100 / 720): the
client only decodes them.

Purpose: env-keys is the ek_ lifecycle on the client side — every
hydrate against a per-environment key needs this command landing
first. The deviation must be flagged to keep REQUIREMENTS.md +
spec.md honest about the wire-format change so future planners don't
re-derive the `--save-as` plan.

Output: 2 new files (env_keys.go + test); 2 modified docs files.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/REQUIREMENTS.md
@.planning/phases/06-cli-foundation/06-CONTEXT.md
@.planning/phases/06-cli-foundation/06-PATTERNS.md
@spec/ach_cli_spec_v20260515_FINALv4.md
@spec/ach_hub_spec_v20260515_FINALv4.md
@CLAUDE.md
@cmd/ach/cmd/migrate.go
@internal/platformapi/envkeys/handler.go
@internal/keys
@.planning/phases/06-cli-foundation/06-01-SUMMARY.md
@.planning/phases/06-cli-foundation/06-03-SUMMARY.md
@.planning/phases/06-cli-foundation/06-04-SUMMARY.md
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Author `ach env-keys` with D-07 always-persist behavior + flag-the-deviation docs</name>
  <files>
    cmd/ach/cmd/env_keys.go
    cmd/ach/cmd/env_keys_test.go
    .planning/REQUIREMENTS.md
    spec/ach_cli_spec_v20260515_FINALv4.md
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-07" (always-persist + --no-save) + §"D-08" (synthetic-mode rules) + §"Specifics" (deviation flagging guidance)
    - 06-PATTERNS.md §"Pattern P3" (parent-with-children) + §"Pattern P5" (httpclient) + §"Pattern S5" (no plaintext leak — ek_ printed once)
    - spec/ach_cli_spec_v20260515_FINALv4.md §5.6 (current ach env-keys spec — `--save-as` opt-in)
    - .planning/REQUIREMENTS.md lines 147-148 (CLI-09 + AC4 + AC16 current text — to be marked DEVIATED)
    - internal/platformapi/envkeys/handler.go lines 93-100 (CreateRequest{Environment,Name}), lines 100-110 (CreateResponse{KeyID, Plaintext, OwnerEmail, ...}), lines 708-720 (EkRowView), lines 720+ (ListResponse{Items, NextCursor}), DELETE /env-keys/{key_id} contract
    - internal/keys (EkidKeyIDPrefix, EkBearerPrefix constants for client-side classification)
    - CLAUDE.md §"Documentation hygiene" (code + docs in SAME commit)
  </read_first>
  <behavior>
    create tests:
    - Test 1: ach env-keys create --environment demo --name local-laptop against an httptest /platform/env-keys POST returning {key_id:"ekid_abc",plaintext:"ek_xyz",environment:"demo",name:"local-laptop"} → writes deployments.<active>.ek["local-laptop"] = "ek_xyz" to t.TempDir() config; prints `ek_xyz` to stdout exactly once. Exit 0.
    - Test 2: ach env-keys create --environment demo --name local-laptop --no-save → does NOT write to config; prints ek_xyz to stdout. Exit 0.
    - Test 3: ach env-keys create in synthetic mode (ACH_BASE_URL + ACH_API_KEY set) WITHOUT --no-save → exit 1 (D-08).
    - Test 4: ach env-keys create in synthetic mode --no-save → prints ek_ to stdout, exit 0 (D-08).
    - Test 5: Server returns 503 → main.go maps to exit 6; ek_ NOT printed (CLI-04 safety — partial response should never leak plaintext).
    - Test 6: --environment is required; missing → cobra-side exit 1 (cobra MarkFlagRequired).
    - Test 7: --name is required; missing → cobra-side exit 1.

    list tests:
    - Test 8: ach env-keys list against /platform/env-keys?cursor=... paginated returns the table via `render.FormatEkList` (per W7 — defined in 06-04 render package; consumed by both env-keys list and admin keys list). NOT inlined.
    - Test 9: ach env-keys list --environment demo filters by environment (passes ?environment= query param).

    revoke tests:
    - Test 10: ach env-keys revoke ekid_abc with --yes → DELETEs /platform/env-keys/ekid_abc → 204 → exit 0.
    - Test 11: ach env-keys revoke ekid_abc without --yes → prompts "Confirm revoke of ekid_abc [y/N]: "; on "y" → DELETE → exit 0; on "n" or empty → exit 1.
    - Test 12: ach env-keys revoke ek_rawplaintext (raw ek_, NOT ekid_) → exit 1 with stderr "key id must be in ekid_ form, got ek_... (raw plaintext rejected)" — CLI-13 client-side reject before any HTTP.
    - Test 13: ach env-keys revoke pkid_abc (pk key id form) → exit 1 with stderr "ach env-keys revoke accepts only ekid_… ids" (env-keys command rejects pkid_; `ach admin keys revoke` will accept both per W3-P2).
    - Test 14: ach env-keys revoke ekid_abc with server 404 → exit 1 (not in {3,6}: not auth, not network — it's a state error).

    Docs assertions:
    - Test 15: .planning/REQUIREMENTS.md CLI-09 row contains the string "DEVIATED" AND a reference to D-07.
    - Test 16: spec/ach_cli_spec_v20260515_FINALv4.md contains a changelog note (string "always-persist" AND "--no-save") in the changelog or §5.6 deviation block.
  </behavior>
  <action>
    Author `cmd/ach/cmd/env_keys.go` mirroring Pattern P3:
    - File-level docstring citing D-07 (deviation) + D-08 (synthetic-mode) + spec §5.6 (with deviation marker).
    - `var envKeysCmd` parent. NOTE: cobra uses hyphenated subcommand names → register as `Use: "env-keys"`. The package-level var is `envKeysCmd` (Go identifier).
    - 3 children:

      `envKeysCreateCmd`:
        - Flags:
          - `--environment <name>` (string, required via `MarkFlagRequired`).
          - `--name <local-label>` (string, required).
          - `--no-save` (bool) — D-07 escape hatch.
          - `--verbose` (bool) + standard credential mutex flags from Pattern P4 (--api-key/--env-key/etc.).
        - RunE:
          1. Synthetic-mode check: if synthetic && !no-save → return CodedError(General, "ach env-keys create requires --no-save in synthetic mode").
          2. Compose httpclient.Client; POST `/platform/env-keys` with body `{environment: <env>, name: <name>}`. Decode the CreateResponse.
          3. Print response.Plaintext to stdout (CLI-04 — exactly once).
          4. If !no-save:
             - config.Load. Resolve active deployment. Mutate `dep.EK[name] = response.Plaintext` (create map if nil). config.Save.
             - On config write failure → exit 8 (ConfigFile). Surface a stderr message but do NOT re-print the plaintext (already on stdout once).

      `envKeysListCmd`:
        - Flags: `--environment <name>` (optional filter), `--owner-email <addr>` (admin-only filter; server enforces — CLI just passes through), `--cursor` (opaque), `--limit`, standard credential flags.
        - RunE:
          1. Compose httpclient.Client.
          2. Paginate `GET /platform/env-keys?environment=&owner_email=&cursor=...` until next_cursor is null.
          3. Decode each row into `render.EkRowView` (the shared type from 06-04).
          4. Render via `render.FormatEkList(rows)` and write to stdout. Per W7, NO inline rendering — render is the single source of truth shared with 06-08 admin keys list.

      `envKeysRevokeCmd`:
        - `Args: cobra.ExactArgs(1)` — the ekid_ id.
        - Flags: `--yes` (bool) — bypass interactive confirmation. Standard credential flags.
        - RunE:
          1. Validate args[0]:
             - If `strings.HasPrefix(args[0], keys.EkidKeyIDPrefix)` → ok (use `internal/keys.EkidKeyIDPrefix` constant).
             - Else if `strings.HasPrefix(args[0], keys.EkBearerPrefix)` (raw plaintext) → exit 1 with stderr "raw plaintext rejected; use ekid_ form".
             - Else if `strings.HasPrefix(args[0], "pkid_")` → exit 1 with stderr "ach env-keys revoke accepts only ekid_; use `ach admin keys revoke` for pkid_".
             - Else → exit 1 with stderr "invalid key id".
          2. If !--yes: print `Confirm revoke of <ekid> [y/N]: ` to stderr, read one line from stdin via bufio.Scanner. Accept "y"/"Y"/"yes" → continue; anything else → exit 1 "cancelled".
          3. `httpclient.Client.Do(ctx, "DELETE", "/platform/env-keys/"+args[0], nil, nil)`. On 204 → exit 0. On *ServerError → main.go mapping (401→3, 503→6, 404→1).

    Register `init() { envKeysCmd.AddCommand(envKeysCreateCmd, envKeysListCmd, envKeysRevokeCmd); rootCmd.AddCommand(envKeysCmd) }`.

    Tests use httptest with route mux for POST /platform/env-keys, GET /platform/env-keys, DELETE /platform/env-keys/{id}. Interactive confirmation tested by stdin redirection via `os.Pipe()` or by injecting `bufio.NewScanner` source via a package-level var seam.

    **Documentation update (SAME commit per CLAUDE.md §"Documentation hygiene"):**

    Modify `.planning/REQUIREMENTS.md`:
    - Find the CLI-09 line (around line 147): append `(DEVIATED Phase 6 D-07: --save-as removed; ek_ always-persists; --no-save opts out)`.
    - At the bottom of the file (or in the appropriate section), add a footnote section:
      ```
      ## Phase 6 Deviations

      | REQ | Status | Decision | Notes |
      |-----|--------|----------|-------|
      | CLI-09 (AC4 wire shape) | DEVIATED | D-07 | spec §5.6 `--save-as` replaced with always-persist + `--no-save` opt-out. Wire-format binary-compat: `--save-as` flag removed; `--no-save` added; default behavior changes. See spec changelog 2026-05. |
      ```

    Modify `spec/ach_cli_spec_v20260515_FINALv4.md`:
    - Find the changelog / revision history section at the top of the file. Append a changelog entry:
      ```
      ## Changelog

      ### DEVIATION 2026-05 — ach env-keys create: always-persist
      Per ACH project Phase 6 decision D-07, `ach env-keys create` ALWAYS persists the
      returned ek_ plaintext to `deployments.<active>.ek.<server-name>` in the active
      deployment of `~/.config/ach/config.yaml`. The `--save-as` flag specified in §5.6
      is REMOVED; the `--no-save` flag is added as the opt-out escape hatch (for CI /
      secret-manager workflows that pipe ek_ to a vault).
      ```
    - Update §5.6 inline: strike-through or annotate the `--save-as` reference with `[DEVIATED — see changelog]`.

    Both doc edits land in the SAME commit as `cmd/ach/cmd/env_keys.go`. Per CLAUDE.md: "If a doc claim is found stale during work, fix it in the same change that revealed the staleness. Drift is a bug, not tech debt."

    SPDX header on every new `*.go` file (no SPDX on .md files).
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestEnvKeys" &amp;&amp; grep -c "DEVIATED" .planning/REQUIREMENTS.md &amp;&amp; grep -c "DEVIATION 2026" spec/ach_cli_spec_v20260515_FINALv4.md</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestEnvKeys"` exits 0.
    - Source assertion: `grep -c '"create"\|"list"\|"revoke"' cmd/ach/cmd/env_keys.go` returns 3.
    - Source assertion: `grep -c 'envKeysCmd.AddCommand' cmd/ach/cmd/env_keys.go` returns ≥ 1.
    - Source assertion: `grep -c '\-\-no-save\|noSave\|NoSave' cmd/ach/cmd/env_keys.go` returns ≥ 2.
    - Source assertion: `grep -c 'EkidKeyIDPrefix\|"ekid_"' cmd/ach/cmd/env_keys.go` returns ≥ 1.
    - Source assertion: `grep -c '"DELETE"\|http.MethodDelete' cmd/ach/cmd/env_keys.go` returns ≥ 1.
    - Source assertion: `grep -c '\\-\\-yes\|Yes\\s*bool' cmd/ach/cmd/env_keys.go` returns ≥ 1.
    - Docs assertion: `grep -c 'CLI-09.*DEVIATED\|DEVIATED.*D-07' .planning/REQUIREMENTS.md` returns ≥ 1.
    - Docs assertion: `grep -c 'DEVIATION 2026.*always-persist\|always-persist.*D-07\|--no-save' spec/ach_cli_spec_v20260515_FINALv4.md` returns ≥ 1.
    - Behavior: ach env-keys create with --no-save does NOT touch the config.yaml file (assert via os.Stat mtime unchanged before/after).
    - Behavior: ach env-keys revoke ek_rawplaintext exits 1 BEFORE any HTTP call (assert httptest counter on /env-keys is 0).
    - Behavior: ach env-keys revoke without --yes and stdin "n\n" exits 1 with "cancelled" message.
  </acceptance_criteria>
  <done>
    env-keys 3 children green; D-07 deviation flagged in REQUIREMENTS.md AND spec.md changelog (same commit); ek_ plaintext printed once at create, never on list/revoke; client-side plaintext rejection on revoke.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| CLI process ↔ ~/.config/ach/config.yaml | `env-keys create` (default) WRITES ek_ plaintext into `deployments.<active>.ek.<server-name>` per D-07. The config file is the local trust artifact authorized to hold plaintext (Hub §15.4). |
| CLI ↔ network (env-keys endpoints) | POST/GET/DELETE `/platform/env-keys[/<id>]`. ek_ plaintext flows on the response of create only; revoke + list never carry plaintext. |
| `--no-save` flag ↔ stdout | `--no-save` opts out of disk persist; ek_ plaintext goes to stdout only — assumed to be piped to a vault/CI script. |
| Argument `<ekid_…>` ↔ revoke endpoint | The revoke command rejects raw plaintext (`ek_…`/`pk_…`) CLIENT-side via prefix check before any HTTP. |
| Interactive stdin ↔ confirmation | Revoke prompts y/N unless `--yes`; `--yes` bypass is the documented automation path. |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-06-05-01 | Information Disclosure | ek_ plaintext at rest in ~/.config/ach/config.yaml | accept | Hub §15.4 explicitly authorizes the config file as the local trust artifact (plaintext-on-disk posture); spec §13 defers OS keyring to v1beta1. File mode 0600 + parent dir 0700 (06-01 T-06-01-03 + T-06-01-05). The D-07 deviation surfaces this acceptance in REQUIREMENTS.md + spec changelog — same commit. |
| T-06-05-02 | Information Disclosure | `--no-save` ek_ leaks via shell history / scrollback | accept | `--no-save` is the EXPLICIT CI / vault-piping path; users opt in to handling the plaintext themselves. The CLI prints the plaintext exactly once to stdout (Pattern S5 — never echoed on list or revoke). |
| T-06-05-03 | Spoofing | `ach env-keys revoke ek_rawplaintext` via cut/paste | mitigate | CLIENT-side prefix check refuses anything that doesn't start with `ekid_` (CLI-13). Source-assertion gate verifies the prefix branch fires BEFORE any HTTP call. Even an attacker who tricks the user into pasting a plaintext key cannot trigger a server roundtrip — and the raw key is also explicitly rejected with a message that surfaces the mistake in stderr. |
| T-06-05-04 | Tampering | Interactive confirm bypass via `--yes` in scripts | accept | `--yes` is the documented automation bypass. The destructive blast-radius is bounded: revoke targets exactly one ekid_ (server-side enforcement); no batch operation. |
| T-06-05-05 | Elevation of Privilege | `ach env-keys revoke pkid_…` reaches the env-keys revoke endpoint | mitigate | Client-side prefix check rejects `pkid_…` with stderr "ach env-keys revoke accepts only ekid_; use `ach admin keys revoke` for pkid_". This prevents a non-admin from accidentally hitting the env-keys endpoint with an admin key id (no privilege escalation; the server would reject too, but client-side rejection is a clearer error). |
| T-06-05-06 | Repudiation | env-keys create not audited | mitigate | The server-side `internal/platformapi/envkeys/handler.go` emits `ActionEkCreate` / `ActionEkRevoke` audit events on every successful operation (Phase 3). CLI does not need its own audit; pk_ used as `x-ach-key` flows into the server `actor` field per Pattern S5. |
| T-06-05-07 | Information Disclosure | ek_ leaks via `--verbose` header dump | mitigate | `--verbose` runs `x-ach-key` through `httpclient.Redact` (06-01 T-06-01-01). The response body containing the ek_ plaintext is NOT dumped by `--verbose` (only headers are). Source-assertion gate verifies no plaintext flows through deps.Logger. |
| T-06-05-08 | Tampering | REQUIREMENTS.md / spec changelog drift if doc edit forgotten | mitigate | Same-commit policy per CLAUDE.md §"Documentation hygiene"; the task's acceptance_criteria include both grep gates (`grep -c "DEVIATED"` ≥ 1 and `grep -c "DEVIATION 2026"` ≥ 1). The commit message MUST cite the deviation per the verification block. |
| T-06-05-SC | Tampering | npm/pip/cargo installs | mitigate | No new third-party deps; consumes the foundation render + httpclient + config packages from 06-01 / 06-04 only. Existing govulncheck ack-list applies. |
</threat_model>

<verification>
After the task completes:

```bash
./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestEnvKeys"
./scripts/dev.sh go build ./cmd/ach/...
./scripts/dev.sh make lint
grep -c "DEVIATED" .planning/REQUIREMENTS.md
grep -c "DEVIATION 2026" spec/ach_cli_spec_v20260515_FINALv4.md
```

Smoke:
```bash
./bin/ach env-keys create --environment demo --name local-laptop
./bin/ach env-keys list
./bin/ach env-keys revoke ekid_xxxxx --yes
```

The commit message MUST cite the deviation: `feat(cli): ach env-keys + D-07 always-persist deviation (REQUIREMENTS.md CLI-09 + spec §5.6 marked DEVIATED)`.
</verification>

<success_criteria>
- env-keys create defaults to always-persist (D-07); --no-save opts out.
- env-keys revoke rejects raw plaintext client-side.
- ach env-keys revoke accepts only ekid_; pkid_ goes to admin command (W3-P2).
- REQUIREMENTS.md + spec changelog updated in the same commit.
- ek_ plaintext printed exactly once at create.
</success_criteria>

<output>
Create `.planning/phases/06-cli-foundation/06-05-SUMMARY.md` when done. Record:
- The exact REQUIREMENTS.md row format used for the DEVIATED marker (so cross-AI reviewers and the Phase 6 verifier find it).
- The spec/ach_cli_spec changelog entry text verbatim (for traceability).
- Confirm `render.FormatEkList` is imported from `internal/cli/render` (landed in 06-04 per W7) — env_keys.go must NOT contain an inline formatter; 06-08 admin keys list will also consume the same render func.
</output>
