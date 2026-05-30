---
phase: 06-cli-foundation
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - internal/cli/doc.go
  - internal/cli/config/config.go
  - internal/cli/config/config_test.go
  - internal/cli/httpclient/client.go
  - internal/cli/httpclient/client_test.go
  - internal/cli/httpclient/redact.go
  - internal/cli/httpclient/redact_test.go
  - internal/cli/exit/exit.go
  - internal/cli/exit/exit_test.go
  - cmd/ach/main.go
autonomous: true
requirements:
  - CLI-02
  - CLI-04
  - CLI-08

must_haves:
  truths:
    - "internal/cli/config can load + save ~/.config/ach/config.yaml with 0600/0700 mode discipline"
    - "internal/cli/config refuses non-HTTPS url on both load and save (CLI-02)"
    - "internal/cli/config.Mask returns '<prefix>_****<last-4>' for pk_/ek_ plaintext (CLI-04)"
    - "internal/cli/httpclient injects x-ach-key header and decodes the §15.5 error envelope into *ServerError"
    - "internal/cli/httpclient.Client.ExtraHeaders is forwarded on every Do/DoRaw call (whoami ek_ Accept-Encoding: gzip, future consumers)"
    - "internal/cli/httpclient.Client.DoRaw returns the *http.Response on 2xx so callers can io.Copy the body verbatim (hydrate byte-for-byte stdout)"
    - "internal/cli/httpclient.Redact rewrites x-ach-key to '<prefix>_***' in --verbose dumps (CLI-04)"
    - "internal/cli/exit defines typed Code constants 0/1/3/6/8 matching CLI spec §9.3"
    - "internal/cli/exit.MapServerError maps 401→3, 403 not_admin/unauthorized_team→3, 503→6, other→1"
    - "cmd/ach/main.go maps *httpclient.ServerError and *exit.CodedError to exit codes via errors.As"
    - "Deployment resolution precedence: --deployment → ACH_DEPLOYMENT → default: → sole entry (CLI-08)"
  artifacts:
    - path: "internal/cli/config/config.go"
      provides: "yaml file I/O, Path resolver, Load/Save, Mask helper, Deployment + File types"
      contains: "func Load(path string)"
    - path: "internal/cli/httpclient/client.go"
      provides: "Client wrapping net/http; Do + DoRaw methods; ExtraHeaders field; ServerError type"
      contains: "type ServerError struct"
    - path: "internal/cli/httpclient/redact.go"
      provides: "Redact helper for header dump"
      contains: "func Redact(key string) string"
    - path: "internal/cli/exit/exit.go"
      provides: "typed Code constants + MapServerError + CodedError type"
      contains: "type Code int"
    - path: "cmd/ach/main.go"
      provides: "exit-code dispatch via errors.As on Execute() result"
      contains: "exit.MapServerError"
  key_links:
    - from: "internal/cli/httpclient/client.go"
      to: "internal/platformapi/render/json.go:52-62"
      via: "wire-contract decode of {error:{code,message},request_id}"
      pattern: "ServerError"
    - from: "cmd/ach/main.go"
      to: "internal/cli/exit/exit.go"
      via: "errors.As + MapServerError + os.Exit(int(code))"
      pattern: "errors.As.*ServerError"
---

<objective>
Ship the shared `internal/cli/` package foundation that every Phase 6
cobra subcommand will consume: yaml multi-deployment config registry
(D-03/D-04), HTTP client with x-ach-key carrier + §15.5 error envelope
decode (D-04, Pattern P5), per-call ExtraHeaders + raw stream-back
DoRaw (downstream consumers in W1-P3 whoami + W2-P3 hydrate require
both — landed here in foundation, NOT conditionally extended later),
header redaction helper (CLI-04, D-15), typed exit-code constants
matching spec §9.3 (D-16, Pattern P6), and the main.go entrypoint
rewire that maps typed errors to exit codes (Pattern P12).

Purpose: Every subsequent Phase 6 plan (W1-P2 device-code endpoints,
W1-P3 login/whoami/logout, W2-P1 env+config, W2-P2 env-keys, W2-P3
hydrate, W3-P1 synthetic, W3-P2 admin) imports from this package. By
landing it first as a stdlib + go.yaml.in/yaml/v3 + go-redis-free
package, downstream plans run in parallel without contract churn.

Output: 7 new Go files under `internal/cli/{config,httpclient,exit}`,
1 doc.go, modified `cmd/ach/main.go`, no go.mod additions (gopkg.in/yaml.v3 already indirect; promote to direct via tidy).
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/STATE.md
@.planning/phases/06-cli-foundation/06-CONTEXT.md
@.planning/phases/06-cli-foundation/06-PATTERNS.md
@spec/ach_cli_spec_v20260515_FINALv4.md
@spec/ach_hub_spec_v20260515_FINALv4.md
@CLAUDE.md
@cmd/ach/cmd/migrate.go
@cmd/ach/main.go
@internal/audit/events.go
@internal/cachefs/cachefs.go
@internal/platformapi/render/json.go
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Author internal/cli/config package (yaml file I/O + multi-deployment registry)</name>
  <files>
    internal/cli/doc.go
    internal/cli/config/config.go
    internal/cli/config/config_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-04" (config schema verbatim) + §"D-05" (mask shape) + §"D-13" / §"D-14" (synthetic mode constraints)
    - 06-PATTERNS.md §"Pattern P10" lines 511-583 (full adapted shape for config.go) + §"Pattern S5" (audit-safety)
    - spec/ach_cli_spec_v20260515_FINALv4.md §3.2 (config schema), §3.3 (synthetic mode)
    - internal/cachefs/cachefs.go (stdlib-only file-system discipline — `os` + `path/filepath`, no `slog`/`log`/`fmt`)
    - internal/audit/events.go lines 1-50 (closed-enum + doc.go style)
    - go.mod (confirm gopkg.in/yaml.v3 is available as indirect; this plan promotes to direct via `go mod tidy`)
  </read_first>
  <behavior>
    - Test 1: Path() returns $XDG_CONFIG_HOME/ach/config.yaml when XDG_CONFIG_HOME is set; falls back to $HOME/.config/ach/config.yaml.
    - Test 2: Save writes mode 0600 and parent dir 0700, atomic via tmp+rename in same dir.
    - Test 3: Save refuses to write a Deployment whose URL does not begin with "https://" — returns ErrNonHTTPSURL sentinel.
    - Test 4: Load refuses to parse a file with mode > 0600 (emits a warning to stderr via the injected logger seam) BUT proceeds to load — and normalises on next Save. (D-04: "warn on read; normalize on write".)
    - Test 5: Load returns (nil, nil) when file is absent (fresh install / synthetic mode); returns ErrConfigParse on yaml decode failure.
    - Test 6: Load refuses to load any deployment whose URL is non-HTTPS — emits ErrNonHTTPSURL with the deployment name in the message.
    - Test 7: Mask("pk_abcdefghijklmnopWXYZ") returns "pk_****WXYZ"; Mask("ek_xyz") returns "<masked>" (input < 8 chars); Mask("garbage") returns "<masked>" (no underscore).
    - Test 8: ResolveActive(file, flagDeployment, envDeployment) implements precedence flag → env → default: → sole entry → ErrNoDeployment (CLI-08).
  </behavior>
  <action>
    Author `internal/cli/doc.go` with SPDX header and a 5-line package-level overview citing CLI spec §3.2 + Hub §15.4 ("local trust artifact authorized to hold plaintext on disk").

    Author `internal/cli/config/config.go` mirroring Pattern P10 shape:
    - Package `config` under `internal/cli/config/`.
    - Types: `File{Default string yaml:"default,omitempty"; Deployments map[string]*Deployment yaml:"deployments,omitempty"}`, `Deployment{URL string yaml:"url"; PK string yaml:"pk,omitempty"; EK map[string]string yaml:"ek,omitempty"}`.
    - Sentinel errors: `ErrNonHTTPSURL`, `ErrConfigParse`, `ErrNoDeployment`, `ErrFileMode`.
    - Funcs:
      - `Path() (string, error)` — $XDG_CONFIG_HOME/ach/config.yaml fallback to $HOME/.config/ach/config.yaml. Per D-04.
      - `Load(path string) (*File, error)` — returns (nil, nil) when os.IsNotExist; stats the file and writes a single stderr warning line via an injected `*log.Logger`-style seam (signature: `LoadWith(path string, warn func(format string, args ...any)) (*File, error)`; `Load` calls `LoadWith` with a `log.New(os.Stderr,...)` wrapper). When mode > 0600, warn but proceed. Refuses non-HTTPS URLs with `ErrNonHTTPSURL`.
      - `Save(path string, f *File) error` — atomic tmp+rename in the same parent dir; chmod 0600; ensures parent dir exists with 0700. Refuses non-HTTPS URLs.
      - `Mask(s string) string` — returns `s[:idx+1] + "****" + s[len(s)-4:]` where idx is first '_'; returns `"<masked>"` when len < 8 or no '_' found.
      - `ResolveActive(f *File, flagDeployment, envDeployment string) (name string, dep *Deployment, err error)` — implements CLI-08 precedence: --deployment flag > ACH_DEPLOYMENT env > f.Default > sole-entry-by-map-iteration; returns `ErrNoDeployment` when none resolves and the file is empty/nil.

    Use only stdlib `errors`, `fmt`, `io/fs`, `os`, `path/filepath`, `strings` + `gopkg.in/yaml.v3` (promote to direct dep via `go mod tidy` in this plan). NO `log`, NO `slog`, NO direct `os.Stderr` writes — mirror the no-logger discipline of `internal/credhash/doc.go`.

    SPDX header `// SPDX-License-Identifier: Apache-2.0` on EVERY new `*.go` file per Pattern S1 (gate 9 of 17-gate pre-push).

    Test file mirrors `internal/credhash/credhash_test.go` style (table-driven stdlib testing, no testify/gomega). Use `t.TempDir()` for filesystem fixtures. Skip the mode-bit assertion on UID 0 + Windows mirroring `internal/cachefs` test discipline.

    Verify SPDX header on every new file; pre-push gate 9 will reject otherwise.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./internal/cli/config/...</automated>
  </verify>
  <acceptance_criteria>
    - File `internal/cli/config/config.go` exists; first line is `// SPDX-License-Identifier: Apache-2.0`.
    - File `internal/cli/config/config_test.go` exists; first line is `// SPDX-License-Identifier: Apache-2.0`.
    - File `internal/cli/doc.go` exists; first line is `// SPDX-License-Identifier: Apache-2.0`.
    - `./scripts/dev.sh go test ./internal/cli/config/...` exits 0.
    - Source assertion: `grep -E 'ErrNonHTTPSURL|ErrConfigParse|ErrNoDeployment|ErrFileMode' internal/cli/config/config.go` matches ≥ 4 lines.
    - Source assertion: `grep -E 'func (Path|Load|Save|Mask|ResolveActive)' internal/cli/config/config.go` matches exactly 5 lines.
    - Source assertion: `grep -E '"log"|"log/slog"' internal/cli/config/config.go` matches 0 lines (no-logger discipline per Pattern S5).
    - Source assertion: `grep -E '0600|0700' internal/cli/config/config.go` matches ≥ 2 lines (mode discipline per CLI-02).
    - `./scripts/dev.sh go mod tidy && git diff --name-only -- go.mod go.sum | wc -l` — non-zero only when `gopkg.in/yaml.v3` was promoted from indirect → direct.
    - Behavior: Load on a fresh empty `t.TempDir()` returns (nil, nil) without error.
    - Behavior: Save followed by Load round-trips a File{Default:"prod",Deployments:{"prod":{URL:"https://x"}}} byte-identically.
    - Behavior: Save({Deployments:{"x":{URL:"http://insecure"}}}) returns ErrNonHTTPSURL.
  </acceptance_criteria>
  <done>
    Tests green; SPDX headers in place; no `log`/`slog` imports in `internal/cli/config/config.go`; `go mod tidy` clean.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Author internal/cli/httpclient + internal/cli/exit packages</name>
  <files>
    internal/cli/httpclient/client.go
    internal/cli/httpclient/client_test.go
    internal/cli/httpclient/redact.go
    internal/cli/httpclient/redact_test.go
    internal/cli/exit/exit.go
    internal/cli/exit/exit_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-04" (HTTP client x-ach-key carrier) + §"D-15" (redaction to '<prefix>_***') + §"D-16" (exit-code matrix)
    - 06-PATTERNS.md §"Pattern P5" lines 246-294 (HTTP client adapted shape) + §"Pattern P6" lines 295-361 (exit code shape) + §"Pattern S6" (error envelope wire contract)
    - spec/ach_cli_spec_v20260515_FINALv4.md §9.3 (9-code exit matrix; Phase 6 ships 0/1/3/6/8 only)
    - spec/ach_hub_spec_v20260515_FINALv4.md §15.5 (error envelope `{error:{code,message},request_id}`) and §18.2 (outcome enum that anchors `not_admin`, `unauthorized_team`)
    - internal/litellm/restclient.go (analog for outbound HTTP client; structure of makeRequest)
    - internal/platformapi/render/json.go lines 29-62 (canonical error envelope shape Phase 6 decodes)
    - internal/audit/events.go lines 77-115 (closed-enum constant block pattern)
  </read_first>
  <behavior>
    httpclient tests:
    - Test 1: Client.Do issues GET against an httptest server, returns 200 + decoded body; header dump includes `x-ach-key: pk_***` when Verbose=true.
    - Test 2: Client.Do issues POST with JSON body; decoded into provided out struct.
    - Test 3: Server returns 401 with `{"error":{"code":"invalid_key","message":"x"},"request_id":"req_test"}` → Client.Do returns *ServerError{Status:401,Code:"invalid_key",Message:"x",RequestID:"req_test"}; the error implements the `error` interface with message `"401 invalid_key: x (request_id=req_test)"`.
    - Test 4: Server returns 403 with code "not_admin" → *ServerError correctly populated.
    - Test 5: Server returns malformed JSON body on 4xx → *ServerError with Status set but Code/Message zero values + ErrEnvelopeDecode wrapped.
    - Test 6: Transport error (server closed) → returns a non-ServerError error; caller can detect via errors.As.
    - Test 7 (ExtraHeaders): Client.ExtraHeaders = http.Header{"Accept-Encoding": []string{"gzip"}} → request issued by Do/DoRaw carries `Accept-Encoding: gzip` on the wire. Multiple values supported.
    - Test 8 (DoRaw): Client.DoRaw(ctx, method, path, body) returns the live *http.Response with Body unread on 2xx; caller can `io.Copy` it. On non-2xx, DoRaw still returns *ServerError just like Do; the response Body is consumed for the envelope decode.

    redact tests:
    - Test 9: Redact("pk_abc") returns "pk_***"; Redact("ek_xyz") returns "ek_***"; Redact("garbage") returns "garbage_redacted" (no prefix detected falls through to literal redaction).
    - Test 10: HeaderDump(http.Header{"X-Ach-Key":["pk_abc"],"Authorization":["Bearer y"]}) returns a multi-line string with "X-Ach-Key: pk_***" and Authorization preserved (case-insensitive header name match).

    exit tests:
    - Test 11: MapServerError(nil) returns OK (0).
    - Test 12: MapServerError({Status:401}) returns AuthN (3).
    - Test 13: MapServerError({Status:403,Code:"not_admin"}) returns AuthN (3).
    - Test 14: MapServerError({Status:403,Code:"unauthorized_team"}) returns AuthN (3).
    - Test 15: MapServerError({Status:403,Code:"missing_environment"}) returns General (1).
    - Test 16: MapServerError({Status:503}) returns Network (6).
    - Test 17: MapServerError({Status:500}) returns General (1).
    - Test 18: CodedError implements error; (&CodedError{Code:Network,Msg:"x"}).Error() == "x".
  </behavior>
  <action>
    Author `internal/cli/httpclient/client.go` mirroring Pattern P5:
    - Package `httpclient` under `internal/cli/httpclient/`.
    - Types:
      - `Client{BaseURL string; APIKey string; HTTPClient *http.Client; Verbose bool; Stderr io.Writer; ExtraHeaders http.Header}`. APIKey is `pk_…` or `ek_…`. HTTPClient defaults to `&http.Client{Timeout: 60 * time.Second}` (D-claude-discretion). ExtraHeaders defaults to nil; when non-nil, every key/value is set on the outbound request before fire — used by whoami ek_ path (`Accept-Encoding: gzip`) and by any future caller that needs per-request headers without modifying Client.
      - `ServerError{Status int; Code string; Message string; RequestID string; Underlying error}`. Implements `error` interface with format `"%d %s: %s (request_id=%s)"`.
      - Sentinel `ErrEnvelopeDecode`.
    - Methods:
      - `(c *Client) Do(ctx context.Context, method, path string, body any, out any) error` — composes BaseURL+path, encodes body as JSON if non-nil, sets `x-ach-key: <APIKey>`, sets `Content-Type: application/json` + `Accept: application/json`, applies every entry from `c.ExtraHeaders` (req.Header.Set), fires via `HTTPClient.Do`. On 2xx and non-nil out, JSON-decodes the response body into out. On non-2xx, decodes `{error:{code,message},request_id}` into a *ServerError and returns it (wraps decode failures with ErrEnvelopeDecode).
      - `(c *Client) DoRaw(ctx context.Context, method, path string, body any) (*http.Response, error)` — identical request composition to Do (x-ach-key, JSON encode of body, Content-Type/Accept, ExtraHeaders applied). On 2xx returns the live *http.Response with Body unread — caller owns Close(). On non-2xx returns nil + *ServerError (the implementation consumes the body for envelope decode then closes). Used by `ach hydrate` to `io.Copy(os.Stdout, resp.Body)` without re-marshaling.
      - When Verbose==true, write a redacted request-line + headers dump to `c.Stderr` (default os.Stderr) before issuing the request, using `httpclient.HeaderDump(req.Header)`. Applies to both Do and DoRaw.

    Implementation note: factor the shared composition (URL, body encode, headers, ExtraHeaders fold, verbose dump) into an internal helper that returns the *http.Request + cancel fn; Do and DoRaw both call it and only diverge on response handling.

    Author `internal/cli/httpclient/redact.go`:
    - `func Redact(value string) string` — when value matches `^(pk|ek)_`, returns `<prefix>_***` (literal `***`, NOT `****<last-4>` — that's config-show's mask in Task 1). When neither prefix matches, returns the literal string "redacted".
    - `func HeaderDump(h http.Header) string` — returns a multi-line `key: value` string with `x-ach-key` (case-insensitive) values run through Redact. Other headers passed through verbatim. Sorted by canonical header name for determinism.

    Author `internal/cli/exit/exit.go` mirroring Pattern P6:
    - Package `exit` under `internal/cli/exit/`.
    - `type Code int` with package-level constants:
      - `OK Code = 0` (success)
      - `General Code = 1` (general error / synth-incompatible / mutex creds / missing-env-flag on pk_)
      - `AuthN Code = 3` (401 / 403 not_admin / 403 unauthorized_team)
      - `Network Code = 6` (transport error / 503 / 504)
      - `ConfigFile Code = 8` (~/.config/ach/config.yaml parse / write error)
    - `type CodedError struct { Code Code; Msg string; Wrapped error }` implements `error` interface with `Msg` text; `Unwrap()` returns `Wrapped`.
    - `func MapServerError(e *httpclient.ServerError) Code`:
      - nil → OK
      - Status 401 → AuthN
      - Status 403 with Code in {"not_admin","unauthorized_team"} → AuthN; else General
      - Status 503, 504 → Network
      - Status 5xx other → General
      - default → General

    Note: avoid an import cycle — `internal/cli/exit` imports `internal/cli/httpclient`. Both packages are leaves under `internal/cli/`; the cobra subcommands import both.

    SPDX header on every new file.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./internal/cli/httpclient/... ./internal/cli/exit/...</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./internal/cli/httpclient/... ./internal/cli/exit/...` exits 0.
    - Source assertion: `grep -E 'OK|General|AuthN|Network|ConfigFile' internal/cli/exit/exit.go | grep -cE 'Code\s*=\s*[01368]'` returns 5.
    - Source assertion: `grep -c 'x-ach-key' internal/cli/httpclient/client.go` returns ≥ 1.
    - Source assertion: `grep -c 'DisallowUnknownFields\|json.NewDecoder' internal/cli/httpclient/client.go` returns ≥ 1.
    - Source assertion: `grep -c 'ExtraHeaders\s\+http.Header\|ExtraHeaders http.Header' internal/cli/httpclient/client.go` returns ≥ 1 (Client field present).
    - Source assertion: `grep -cE 'func \(c \*Client\) DoRaw' internal/cli/httpclient/client.go` returns 1 (DoRaw method present).
    - Source assertion: SPDX header on every new file: `head -1 internal/cli/{httpclient/client.go,httpclient/client_test.go,httpclient/redact.go,httpclient/redact_test.go,exit/exit.go,exit/exit_test.go} | grep -c "Apache-2.0"` returns 6.
    - Behavior: httptest server returning 503 + valid envelope → `exit.MapServerError(err.(*httpclient.ServerError))` returns `exit.Network (6)`.
    - Behavior: Verbose=true + APIKey="pk_supersecretlong" → header dump contains `x-ach-key: pk_***` exactly; does NOT contain `supersecretlong`.
    - Behavior: ExtraHeaders={"Accept-Encoding":["gzip"]} → outbound request carries the header (assert via httptest server capturing the request).
    - Behavior: DoRaw returns *http.Response with Body unread on 2xx; consumer io.Copy preserves server bytes verbatim.
  </acceptance_criteria>
  <done>
    httpclient + exit packages green; redaction never prints plaintext; MapServerError table-driven test covers all five Phase-6 exit codes; ExtraHeaders + DoRaw shipped as foundation contracts (no conditional extension in W1-P3 or W2-P3).
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Rewrite cmd/ach/main.go to map typed errors to exit codes</name>
  <files>
    cmd/ach/main.go
  </files>
  <read_first>
    - cmd/ach/main.go (current 15-line shape)
    - 06-PATTERNS.md §"Pattern P12" lines 648-680 (modified main.go shape)
    - 06-CONTEXT.md §"D-16" (exit code matrix)
    - cmd/ach/cmd/root.go (Execute()/rootCmd surface)
  </read_first>
  <behavior>
    - Test: not added (main.go is the process entrypoint; behavior is exercised by every subsequent W1-P3+ subcommand test). Task 2's exit_test.go covers the mapping logic itself.
  </behavior>
  <action>
    Replace `cmd/ach/main.go` body. The new main:
    1. Calls `cmd.Execute()`.
    2. On nil → `os.Exit(int(exit.OK))`.
    3. On non-nil:
       - `var sErr *httpclient.ServerError; if errors.As(err, &sErr) { fmt.Fprintln(os.Stderr, sErr.Error()); os.Exit(int(exit.MapServerError(sErr))) }`
       - `var cErr *exit.CodedError; if errors.As(err, &cErr) { fmt.Fprintln(os.Stderr, cErr.Error()); os.Exit(int(cErr.Code)) }`
       - Fallback: `fmt.Fprintln(os.Stderr, err); os.Exit(int(exit.General))`.

    Preserve the SPDX header on line 1. Import `errors`, `fmt`, `os`, plus `github.com/ackstorm/ach/internal/cli/exit` and `github.com/ackstorm/ach/internal/cli/httpclient`.

    Do NOT silence cobra's own error rendering — cobra prints argument-parse errors itself (e.g. "unknown command 'foo'"); the new main just forwards them to exit 1 via the fallback branch.
  </action>
  <verify>
    <automated>./scripts/dev.sh go build ./cmd/ach/... &amp;&amp; ./scripts/dev.sh go test ./internal/cli/exit/...</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go build ./cmd/ach/...` exits 0 (binary compiles).
    - Source assertion: `grep -c 'errors.As' cmd/ach/main.go` returns ≥ 2 (one for *ServerError, one for *CodedError).
    - Source assertion: `grep -c 'exit.MapServerError\|exit.OK\|exit.General' cmd/ach/main.go` returns ≥ 3.
    - Source assertion: SPDX header line 1: `head -1 cmd/ach/main.go` equals `// SPDX-License-Identifier: Apache-2.0`.
    - Behavior: running compiled `./bin/ach` with no subcommand prints help and exits 0 (preserves rootCmd.RunE=cmd.Help() contract from `cmd/ach/cmd/root.go:29-31`).
  </acceptance_criteria>
  <done>
    Compiled binary; typed-error dispatch wired; existing rootCmd.Help() behavior preserved.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| CLI process ↔ on-disk config | `~/.config/ach/config.yaml` is the local trust artifact (Hub §15.4) carrying pk_/ek_ plaintext at mode 0600; the package owns the read/write discipline that keeps that posture honest. |
| CLI ↔ network (Platform API) | Outbound HTTP via httpclient.Client carries `x-ach-key: <pk_ or ek_>`. The server's §15.5 error envelope is the trust boundary on the response side. |
| CLI ↔ stdin/stderr (--verbose dump) | Header dump prints to stderr; redaction is the only barrier between an operator copy-pasting `--verbose` output and a leaked plaintext key. |
| Flag/env ↔ credential resolver | `--api-key` / `ACH_API_KEY` / `--deployment` / `ACH_DEPLOYMENT` flow through `config.ResolveActive`; precedence drives which credential lands in `x-ach-key`. Drift here = wrong-deployment auth. |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-06-01-01 | Information Disclosure | pk_/ek_ in `--verbose` header dump | mitigate | `httpclient.Redact` rewrites every value with `^(pk|ek)_` prefix to `<prefix>_***` before HeaderDump emits to stderr (CLI-04). Source-assertion gate confirms Redact is the only path into stderr for header values. |
| T-06-01-02 | Tampering | Server error envelope decoded into wrong type | mitigate | `json.NewDecoder(...).DisallowUnknownFields()` rejects envelopes with extra fields (defense vs payload smuggling). Decode failures wrap `ErrEnvelopeDecode` so callers can distinguish from clean ServerError. |
| T-06-01-03 | Tampering | Concurrent config write race (TOCTOU) | mitigate | `config.Save` writes to a sibling tmp file in the same dir, chmod 0600, then `os.Rename` atomically. A reader during the window either sees the old file or the new — never a half-written partial. |
| T-06-01-04 | Information Disclosure | Config file mode regressed > 0600 by external touch | mitigate | `Load` warns on read; `Save` normalizes back to 0600. The warning seam is invoked via `LoadWith(path, warn)` so the seam is testable; the normalize-on-write closes the loop deterministically. |
| T-06-01-05 | Spoofing | Non-HTTPS deployment URL accepted | mitigate | `ErrNonHTTPSURL` fires on BOTH Load and Save (CLI-02). A malicious `http://` URL in the config refuses to load — preventing a downgrade-attack where an attacker writes the file to point pk_ traffic at an MITM proxy. |
| T-06-01-06 | Information Disclosure | pk_/ek_ printed by config-loading error message | mitigate | Pattern S5 enforced: `config.go` imports neither `log` nor `slog`; only `errors.New`/`fmt.Errorf` with deployment NAME (not URL+credential) in the error string. Source-assertion gate verifies no logger imports. |
| T-06-01-07 | Elevation of Privilege | Exit-code spoofing via crafted server error | mitigate | `MapServerError` is a closed switch on `(Status, Code)` pairs; unknown 4xx → exit 1 (General), never exit 0. A malicious server returning `{error:{code:"ok"}}` on a 500 still produces exit 1 via the 5xx→General arm. |
| T-06-01-08 | Repudiation | No record of which credential signed an outbound request | accept | The CLI is single-operator; the server-side `actor` audit field carries `pk_id`/`ek_id` on every call, which is the upstream source of truth. The CLI does not maintain its own audit log in v1alpha1. |
| T-06-01-SC | Tampering | npm/pip/cargo installs | mitigate | One new direct dep promoted via `go mod tidy`: `gopkg.in/yaml.v3` (already indirect in go.mod, Apache-2.0). No untyped/unaudited new packages. Existing govulncheck ack-list applies; the gate re-runs on pre-push. |
</threat_model>

<verification>
After all 3 tasks complete:

```bash
./scripts/dev.sh go test ./internal/cli/... && \
./scripts/dev.sh go build ./cmd/ach/... && \
./scripts/dev.sh make lint
```

Confirm SPDX header on every new `*.go` file:
```bash
for f in internal/cli/doc.go internal/cli/config/config.go internal/cli/config/config_test.go \
         internal/cli/httpclient/client.go internal/cli/httpclient/client_test.go \
         internal/cli/httpclient/redact.go internal/cli/httpclient/redact_test.go \
         internal/cli/exit/exit.go internal/cli/exit/exit_test.go; do
  head -1 "$f" | grep -q "Apache-2.0" || { echo "MISSING SPDX: $f"; exit 1; }
done
```
</verification>

<success_criteria>
- `internal/cli/{config,httpclient,exit}` ship with stdlib-friendly discipline.
- `cmd/ach/main.go` maps `*httpclient.ServerError` and `*exit.CodedError` to the right exit code.
- `httpclient.Client.ExtraHeaders` + `httpclient.Client.DoRaw` shipped as foundation contracts (W1-P3 whoami and W2-P3 hydrate consume them unconditionally; no conditional extension downstream).
- All unit tests pass via the devtools container.
- `go mod tidy` is clean (promote `gopkg.in/yaml.v3` from indirect → direct exactly once).
- Every new `*.go` file has the SPDX header (pre-push gate 9 satisfied).
</success_criteria>

<output>
Create `.planning/phases/06-cli-foundation/06-01-SUMMARY.md` when done. The summary MUST record:
- Final import paths for the new packages (so W1-P3 + W2 plans wire correctly).
- Confirm `ExtraHeaders` + `DoRaw` shipped on `httpclient.Client` (so W1-P3 whoami + W2-P3 hydrate stop carrying the conditional-extension branches in their plans).
- Any deviations from Pattern P5/P6/P10/P12 with rationale.
- `go mod tidy` diff (new direct deps added).
</output>
