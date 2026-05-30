---
phase: 06-cli-foundation
plan: 01
subsystem: cli-foundation
tags: [cli, config, httpclient, exit, redaction, foundation]
requirements: [CLI-02, CLI-04, CLI-08]
dependency_graph:
  requires: []
  provides:
    - "internal/cli/config (Path, Load/LoadWith, Save, Mask, ResolveActive)"
    - "internal/cli/httpclient (Client.Do, Client.DoRaw, ExtraHeaders, ServerError, Redact, HeaderDump, ErrEnvelopeDecode)"
    - "internal/cli/exit (Code constants OK/General/AuthN/Network/ConfigFile, MapServerError, CodedError)"
    - "cmd/ach/main.go typed-error dispatch"
  affects:
    - "every Phase 6 cobra subcommand (W1-P2 device-code endpoints, W1-P3 login/whoami/logout, W2 env/env-keys/hydrate, W3 admin) consumes these packages"
tech_stack:
  added:
    - "gopkg.in/yaml.v3 (promoted from indirect → direct)"
  patterns:
    - "stdlib-only file-system discipline (mirror internal/cachefs, no log/slog/fmt to stderr)"
    - "Pattern P5 — net/http wrapper with auth-header carrier + envelope decode"
    - "Pattern P6 — typed Code constants + closed-switch MapServerError"
    - "Pattern P10 — yaml file I/O with 0600/0700 mode discipline + atomic tmp+rename"
    - "Pattern P12 — main.go errors.As → exit-code dispatch"
key_files:
  created:
    - "internal/cli/doc.go"
    - "internal/cli/config/config.go"
    - "internal/cli/config/config_test.go"
    - "internal/cli/httpclient/client.go"
    - "internal/cli/httpclient/client_test.go"
    - "internal/cli/httpclient/redact.go"
    - "internal/cli/httpclient/redact_test.go"
    - "internal/cli/exit/exit.go"
    - "internal/cli/exit/exit_test.go"
  modified:
    - "cmd/ach/main.go (typed-error exit dispatch via errors.As)"
    - "go.mod (promote gopkg.in/yaml.v3 indirect → direct)"
decisions:
  - "ExtraHeaders is a foundation contract field (not a per-call argument). W1-P3 whoami ek_ and W2-P3 hydrate consume it unconditionally — no downstream conditional extension."
  - "DoRaw returns the live *http.Response with Body unread on 2xx so hydrate can io.Copy bytes verbatim to stdout (byte-for-byte golden parity). On non-2xx DoRaw still returns *ServerError, body is consumed by the envelope decode."
  - "Redact and config.Mask are deliberately distinct: Redact returns '<prefix>_***' for stderr header dumps (CLI-04); Mask returns '<prefix>_****<last-4>' for ach config show (D-05)."
  - "exit codes 2/4/5/7 are intentionally absent — hydrate-engine territory (Phase 7). Phase 6 ships the 0/1/3/6/8 subset only."
  - "MapServerError is a closed switch with a catch-all General arm — defends against exit-code spoofing via crafted server errors (T-06-01-07)."
  - "LoadWith(path, warn func(format,args)) added as an injectable warning-sink seam alongside Load. Load wraps LoadWith with a noop sink. This is the testable form of the file-mode warning path (T-06-01-04)."
metrics:
  duration_minutes: 18
  completed_date: 2026-05-28
  tasks: 3
  files_created: 9
  files_modified: 2
---

# Phase 6 Plan 01: CLI Shared Internals Summary

JWT-free Phase 6 foundation: `internal/cli/` ships the yaml registry, the HTTP client with §15.5 envelope decode + redaction, the typed exit-code matrix, and the `cmd/ach/main.go` rewrite that dispatches typed errors to §9.3 exit codes — everything every subsequent Phase 6 subcommand imports.

## What landed

### internal/cli/config (Task 1 — `f0da7dc`)
- `Path()` resolves `$XDG_CONFIG_HOME/ach/config.yaml` → fallback `$HOME/.config/ach/config.yaml` per D-04 / CLI spec §3.2.
- `Load(path)` returns `(nil, nil)` on `os.IsNotExist` (fresh install / synthetic mode); returns `ErrConfigParse`-wrapped error on yaml decode failure; returns `ErrNonHTTPSURL` when any deployment carries a non-https `url:`.
- `LoadWith(path, warn func(format, args ...any))` is the injectable warning-sink seam. The default `Load` wraps it with a noop sink. The seam exists so cobra RunE can route warnings through a `--no-warnings`-aware printer and so tests can capture them without redirecting stderr.
- `Save(path, *File)` writes mode 0600 and parent dir 0700 (D-04 + T-06-01-03). Atomic via tmp+rename in the same dir.
- `Mask(s string) string` returns `"<prefix>_****<last-4>"` for inputs of 8+ chars with an underscore; returns `"<masked>"` otherwise.
- `ResolveActive(f, flag, env)` implements CLI-08 precedence: `--deployment flag` → `ACH_DEPLOYMENT env` → `file.Default` → sole entry → `ErrNoDeployment`. Unknown name on flag/env returns `ErrNoDeployment` wrapping the offending name.
- Sentinel errors: `ErrNonHTTPSURL`, `ErrConfigParse`, `ErrNoDeployment`, `ErrFileMode`.
- Stdlib + `gopkg.in/yaml.v3` only. NO `log` / `log/slog` / `fmt.Print*` imports — mirrors the `internal/credhash/doc.go` no-logger discipline (Pattern S5 → T-06-01-06).

### internal/cli/httpclient + internal/cli/exit (Task 2 — `ee7a12f`)
- `httpclient.Client{BaseURL, APIKey, HTTPClient, Verbose, Stderr, ExtraHeaders}`. `HTTPClient` defaults to `&http.Client{Timeout: 60*time.Second}` (D-19 / Claude's Discretion).
- `Client.Do(ctx, method, path, body, out) error` — composes BaseURL+path, encodes body as JSON when non-nil, sets `x-ach-key`/`Accept: application/json`/`Content-Type: application/json`, applies every `ExtraHeaders` entry via `Header.Add` so multi-value headers survive, then fires. On 2xx and non-nil `out`, JSON-decodes with `DisallowUnknownFields`. On non-2xx, decodes the §15.5 envelope into a `*ServerError` (wraps decode failures with `ErrEnvelopeDecode` via `Underlying`).
- `Client.DoRaw(ctx, method, path, body) (*http.Response, error)` — identical request composition. On 2xx returns the live `*http.Response` with Body unread; caller owns `Close()`. On non-2xx returns `nil + *ServerError` (body consumed for the envelope decode then closed). Used by `ach hydrate` to `io.Copy(os.Stdout, resp.Body)` without re-marshaling.
- Verbose mode writes a redacted request-line + header dump to `c.Stderr` before fire, using `HeaderDump`.
- `httpclient.Redact(s)` reduces `pk_…`/`ek_…` to `"<prefix>_***"`; falls through to literal `"redacted"` for unknown values.
- `httpclient.HeaderDump(h)` renders a deterministic sorted multi-line `Key: value` dump with `x-ach-key` values (case-insensitive) run through `Redact`. Multi-value headers join with `", "` (matches `net/http` wire serialization).
- `exit.Code` typed integer with constants `OK=0`, `General=1`, `AuthN=3`, `Network=6`, `ConfigFile=8` per CLI spec §9.3.
- `exit.CodedError{Code, Msg, Wrapped}` implements `error` + `Unwrap` so `errors.Is` composes through.
- `exit.MapServerError(e *httpclient.ServerError) Code` — closed switch: `nil → OK`; `401 → AuthN`; `403 with Code in {not_admin, unauthorized_team} → AuthN, else General`; `503/504 → Network`; default `→ General`.

### cmd/ach/main.go rewrite (Task 3 — `6f2d3c9`)
- `errors.As (*httpclient.ServerError)` → `exit.MapServerError` → `os.Exit(int(code))`.
- `errors.As (*exit.CodedError)` → `os.Exit(int(cErr.Code))`.
- Fallback branch prints `err` to stderr and exits `General (1)` — covers cobra's argument-parse errors verbatim.
- Help-on-no-subcommand behavior preserved (cobra root's `RunE = cmd.Help()` is untouched). Verified: built binary, ran with no args, exit 0 + help printed.

## Import paths (downstream wiring contract)

```go
import (
    "github.com/ackstorm/ach/internal/cli/config"
    "github.com/ackstorm/ach/internal/cli/httpclient"
    "github.com/ackstorm/ach/internal/cli/exit"
)
```

- `config.Path`, `config.Load`, `config.LoadWith`, `config.Save`, `config.Mask`, `config.ResolveActive`, `config.File`, `config.Deployment`.
- `httpclient.Client{BaseURL, APIKey, HTTPClient, Verbose, Stderr, ExtraHeaders}`, `httpclient.Client.Do`, `httpclient.Client.DoRaw`, `httpclient.ServerError{Status, Code, Message, RequestID, Underlying}`, `httpclient.ErrEnvelopeDecode`, `httpclient.Redact`, `httpclient.HeaderDump`.
- `exit.Code`, `exit.OK`, `exit.General`, `exit.AuthN`, `exit.Network`, `exit.ConfigFile`, `exit.CodedError`, `exit.MapServerError`.

## Foundation-contract confirmation (anti-rework gate)

The plan called out two W1/W2 conditional-extension risks. Both are eliminated at the foundation here:

1. **`httpclient.Client.ExtraHeaders` is a top-level field**, not a per-call argument. W1-P3 `ach whoami --verify` against an `ek_` (which needs `Accept-Encoding: gzip`) sets it once on the Client and reuses Do/DoRaw without per-call hook plumbing.
2. **`httpclient.Client.DoRaw` ships now**, not as a conditional W2-P3 hydrate extension. W2-P3 `ach hydrate` can `io.Copy(os.Stdout, resp.Body)` without re-marshaling, preserving the byte-for-byte golden parity vs `examples/hydrate.json` that D-17 / D-18 anchor on.

Downstream W1-P3 + W2-P3 plans need not carry "extend Client with X" branches.

## go mod tidy diff

```
 require (
 	...
 	golang.org/x/sync v0.20.0
 	google.golang.org/api v0.274.0
+	gopkg.in/yaml.v3 v3.0.1
 	k8s.io/api v0.36.1
 	...

 require (
 	...
 	gopkg.in/inf.v0 v0.9.1 // indirect
-	gopkg.in/yaml.v3 v3.0.1 // indirect
 	k8s.io/apiextensions-apiserver v0.36.0 // indirect
```

Single dep promoted from indirect → direct. No new packages, no version bumps, no new transitive surface. govulncheck ack-list unchanged.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] errcheck lint failures on `resp.Body.Close()` + `fmt.Fprintf`**
- **Found during:** Task 2 commit (pre-commit `make lint-changed`).
- **Issue:** golangci-lint errcheck flagged two unchecked error returns: `defer resp.Body.Close()` in `Client.Do` and `fmt.Fprintf(c.Stderr, ...)` in `dumpVerbose`.
- **Fix:** wrapped both in `_ =` discard form — `defer func() { _ = resp.Body.Close() }()` and `_, _ = fmt.Fprintf(...)`. Mirrors the established pattern in `internal/platformapi/render/json.go:32` (`_ = json.NewEncoder(...).Encode(body)`).
- **Files modified:** `internal/cli/httpclient/client.go`.
- **Commit:** `ee7a12f` (absorbed before commit landed).

### Documented divergences from plan acceptance text

**2. `LoadWith` makes the `func` grep count 6, not 5**
- **Found during:** Task 1 acceptance check.
- **Issue:** plan acceptance text says `grep -E 'func (Path|Load|Save|Mask|ResolveActive)' internal/cli/config/config.go` matches exactly 5 lines. The plan body explicitly requires `LoadWith` (the warning-sink seam, see `<action>` lines 142). The regex `Load` is a prefix of `LoadWith`, so my file matches 6 lines.
- **Resolution:** kept `LoadWith` (required by plan body for test seam + T-06-01-04 mitigation). The "exactly 5" assertion in the acceptance text is internally inconsistent with the plan body that requires `LoadWith`.
- **Files modified:** none — this is a doc-vs-body inconsistency in the plan, not a code change.

## Threat surface scan

| Threat ID    | Coverage status                                                                                                                                                                         |
| ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| T-06-01-01   | `httpclient.HeaderDump` runs every `x-ach-key` value (case-insensitive) through `Redact` before stderr emission. Tests `TestHeaderDump_RedactsAchKey` + `TestHeaderDump_CaseInsensitive` assert no plaintext leaks. |
| T-06-01-02   | `json.NewDecoder(...).DisallowUnknownFields()` on both the envelope decode (`decodeServerError`) and the response-body decode (`Client.Do`). Decode failures wrap `ErrEnvelopeDecode` for callers. |
| T-06-01-03   | `config.Save` uses `os.CreateTemp(dir, ".config-*.yaml.tmp")` + `os.Rename` in the same dir for atomic publish. Tests confirm round-trip + mode-0600 file + mode-0700 parent dir. |
| T-06-01-04   | `LoadWith` invokes the injectable warning seam when file mode > 0600. Default `Load` wraps with a noop; cobra RunE wires a stderr printer. `Save` normalizes back to 0600 on next write. Test `TestLoad_WarnOnPermissiveMode` asserts the seam fires. |
| T-06-01-05   | `ErrNonHTTPSURL` fires on **both** Load and Save. Tests `TestSave_RefuseNonHTTPS` + `TestLoad_RefuseNonHTTPS` cover both directions. |
| T-06-01-06   | Source-assertion gate: `grep -cE '"log"\|"log/slog"' internal/cli/config/config.go` returns 0. Error strings carry deployment NAME, not URL+credential. |
| T-06-01-07   | `MapServerError` is a closed switch. Tests cover the five Phase-6 outcomes; the default arm forces General even when a malicious server returns `{error:{code:"ok"}}` on a 500. |
| T-06-01-08   | Accepted — CLI is single-operator; server-side `actor` audit field carries `pk_id`/`ek_id`. No CLI-side audit log in v1alpha1. |
| T-06-01-SC   | Single dep promoted (`gopkg.in/yaml.v3`) — already indirect, Apache-2.0, in govulncheck ack-list scope. No new untyped packages. |

No new threat-flagged surface introduced beyond the plan's `<threat_model>` register.

## Self-Check: PASSED

Verified:
- `internal/cli/doc.go` exists.
- `internal/cli/config/{config.go, config_test.go}` exist.
- `internal/cli/httpclient/{client.go, client_test.go, redact.go, redact_test.go}` exist.
- `internal/cli/exit/{exit.go, exit_test.go}` exist.
- `cmd/ach/main.go` modified.
- Commits `f0da7dc`, `ee7a12f`, `6f2d3c9` in `git log`.
- `./scripts/dev.sh go test ./internal/cli/...` exits 0.
- `./scripts/dev.sh go build ./cmd/ach/...` exits 0.
- `./scripts/dev.sh make lint` exits 0.
- SPDX header on all 9 new files.
- Build + run with no args exits 0 (help-on-no-subcommand preserved).
