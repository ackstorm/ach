# ACH CLI Subcommands — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task.

**Goal:** Implement the `ach login`, `ach whoami`, `ach hydrate`, `ach env {list,create,revoke}` CLI subcommands so that `examples/hydrate-demo.sh` collapses to a one-liner — `ach login --sso && ach hydrate --environment demo > hydrate.json` — and the shell script is deleted in favor of a Go-driven e2e test fixture.

**Architecture:** All subcommands wire into the existing cobra root (`cmd/ach/cmd/root.go`) alongside `operator`/`platform-api`/`forwarder`/`content-service`/`migrate`. Each subcommand follows the established `<mode>.go` per-file pattern (one cobra `*Command` registered from `init()` via `rootCmd.AddCommand(...)`). The CLI shares an HTTP client + on-disk config loader under a new internal package `internal/cli/`. The config file lives at `$XDG_CONFIG_HOME/ach/config.json` (default `~/.config/ach/config.json`) and carries `{endpoint, pk, last_environment}` — written by `ach login`, read by every other subcommand. All CLI HTTP calls send `x-ach-key: <pk_>` exactly as `examples/hydrate-demo.sh` does today.

**Tech stack:**
- Go 1.26 + `github.com/spf13/cobra` (already wired via root.go)
- `net/http` (stdlib) for the shared client; `net/http/httptest` for unit tests
- `encoding/json` (stdlib) for config + wire bodies
- `pkg/browser` (third party, ~50 LOC dep) for opening the SSO URL in the OS default browser — OR `os/exec` shelling out to `xdg-open`/`open`/`rundll32` if we want zero new deps (decided in Task 2)
- No new server-side dependencies (the `/platform/whoami` endpoint added in Task 5 reuses existing `middleware.KeyContextFromCtx`)

**Source paths (read-only):**
- `/home/jcm/Projects/ach/cmd/ach/cmd/root.go` — cobra root, `rootCmd` var, `Version` ldflag injection point
- `/home/jcm/Projects/ach/cmd/ach/cmd/migrate.go` — smallest existing subcommand; canonical style template
- `/home/jcm/Projects/ach/cmd/ach/cmd/platform_api.go` — larger existing subcommand showing flag binding + config validation idioms
- `/home/jcm/Projects/ach/examples/hydrate-demo.sh` — the shell script this CLI subsumes; line-by-line authority on the wire format
- `/home/jcm/Projects/ach/examples/hydrate.json` — **golden** the new CLI must reproduce byte-for-byte
- `/home/jcm/Projects/ach/internal/platformapi/auth/sso.go` — SSO flow (LoginHandler / CallbackHandler) the CLI drives
- `/home/jcm/Projects/ach/internal/platformapi/auth/cookies.go` — `__Host-ach_sso` cookie semantics the CLI's cookie jar must respect
- `/home/jcm/Projects/ach/internal/platformapi/hydrate/handler.go` — `HydrateResponse` shape + `x-ach-key` auth path
- `/home/jcm/Projects/ach/internal/platformapi/envkeys/handler.go` — `CreateRequest`, `CreateResponse`, `EkRowView`, `ListResponse` wire types
- `/home/jcm/Projects/ach/internal/platformapi/envkeys/mount.go` — confirms `POST /platform/env-keys`, `GET /platform/env-keys`, `GET /{key_id}`, `DELETE /{key_id}` route surface
- `/home/jcm/Projects/ach/internal/platformapi/server.go` — middleware chain order so the new `/platform/whoami` slots into the same `Authn`-gated chi.Group
- `/home/jcm/Projects/ach/internal/platformapi/middleware/` — `KeyContextFromCtx`, `ActorFromCtx`, `RequestIDFromCtx`
- `/home/jcm/Projects/ach/CLAUDE.md` — toolchain rules (Go in devtools container only; `./scripts/dev.sh` prefix every `go`/`make` invocation)

**Working directory:** `/home/jcm/Projects/ach/`

**Branch policy:** single feature branch `feat/cli-subcommands`, atomic commits per task, single PR titled `feat(cli): §10 login + whoami + hydrate + env CRUD`.

**Cross-plan refs:**
- **DEPENDS ON §2 (domain port)** — platform-api REST surface (login/sso-callback/hydrate/env endpoints) must already be wired by the bootstrap plan. **Status today:** all four are mounted in `server.go` ✅.
- **DEPENDS ON §7 (AccessGroupSynced reconciler)** for `ach env create` to succeed (today returns `503 not_ready` until §7 lands).
- **DEPENDS ON §8 (content service routes)** for `ach hydrate`'s `downloadUrls` to resolve correctly when the CLI follows them (Task 11 e2e).
- **INDEPENDENT** of §3 (Helm), §5 (marketplace), §6 (BIP).
- **CONTRACT:** `examples/hydrate.json` is the **golden** — the new CLI MUST produce byte-equivalent JSON when run against the same cluster state. Task 11 enforces this with a golden-file diff.

---

## Pre-flight (do once before Task 1)

### Pre-flight Finding F1: `ach-old` has no reference CLI

The task brief mentions porting from `/home/jcm/Projects/ach-old/cmd/ach/cmd/*.go`. Inspection shows **that directory does not exist** (`ls: cannot access`). The new CLI is **designed from scratch** using the platform-api REST routes as the contract. The shell script `examples/hydrate-demo.sh` is the only end-to-end working reference and dictates the wire-format expectations.

### Pre-flight Finding F2: no `/platform/whoami` endpoint exists today

`grep -rn "whoami" internal/platformapi/` returns zero matches. `ach whoami` cannot land as a pure-CLI change — it requires a new server-side route. Task 5 adds it: a trivial handler that reads `middleware.KeyContextFromCtx` and returns `{owner_email, key_id, key_type, environment, is_admin, expires_at}`. The route is `GET /platform/whoami`, mounted inside the existing `Authn`-gated chi.Group in `server.go` so the same `x-ach-key` header gates it.

### Pre-flight Finding F3: SSO returns plaintext in a JSON body, not a redirect

`internal/platformapi/auth/sso.go:182-186` (`callbackResponse`) shows the SSO callback returns `{key_id, plaintext, owner_email}` as JSON on success. The shell demo (lines 110-112) already extracts these with `jq`. The CLI's `--sso` path must therefore:
1. Start a local HTTP server on an ephemeral port (e.g. `127.0.0.1:0` → real port) to be the OAuth2 redirect URI.
2. Open the browser to `<endpoint>/platform/auth/login` (which 302-redirects to Dex).
3. When Dex completes the round-trip and the callback fires `/platform/auth/sso/callback`, the platform-api returns the JSON to **whoever made the request** — which is the **browser**, not the local HTTP server.

**Branch decision needed (Task 2):** the `--sso` flow either (A) requires the user to **paste the JSON output** back into the CLI, or (B) requires extending the platform-api to support a CLI-driven callback URL that POSTs to the local server, or (C) the simpler `--token <pk_>` paste-mode is the v1 implementation. The plan defaults to **(C) for v1** (simplest, mirrors `gcloud auth login --no-launch-browser`); `--sso` (A) is documented as a follow-up. **Justification:** the existing SSO flow uses the `__Host-ach_sso` cookie which is Secure+HttpOnly+SameSite=Strict — a browser-driven flow is the only practical consumer. The `--token` paste-mode is a one-line ergonomic win over today's curl pipeline and is sufficient for the §10 acceptance gate (the e2e test in Task 11 drives `--token` directly).

### Pre-flight Finding F4: `x-ach-key` is the auth header (not Authorization: Bearer)

`examples/hydrate-demo.sh:138` and `internal/platformapi/middleware/` confirm the bearer header is `x-ach-key: <pk_>`. The shared HTTP client (Task 3) MUST use `x-ach-key` — using `Authorization: Bearer` would silently fail Authn.

### Pre-flight Finding F5: XDG path is the right config location

The CLI is a user-facing tool. Config goes to `$XDG_CONFIG_HOME/ach/config.json` (default `$HOME/.config/ach/config.json`) per the XDG Base Directory Spec. File mode is 0600 because it holds a `pk_` plaintext. Task 3 enforces both.

### Pre-flight steps

```bash
cd /home/jcm/Projects/ach
git checkout -b feat/cli-subcommands
./scripts/dev.sh make unit                                 # baseline: green
./scripts/dev.sh make lint                                 # baseline: green
./scripts/dev.sh go build -o /tmp/ach-baseline ./cmd/ach   # baseline: existing modes compile
/tmp/ach-baseline --help                                   # baseline: shows root help with 5 subcommands
```

Confirm baseline green before touching any file.

---

## Task 1: Catalog the wire surface the CLI calls (no code yet)

**Files:** none — this is a paper exercise that drives Tasks 2-10.

**Why first:** every subcommand is a thin wrapper over one or two HTTP calls. Naming the calls + their request/response shapes here means we never have to re-derive them mid-task.

Produce the following inline table inside this plan (as a comment in the first new file, `internal/cli/doc.go`, in Task 3) — it is the single source of truth for the CLI ↔ platform-api binding:

| CLI command                                     | HTTP method + path                              | Auth header              | Request body                                            | Success body                                                                 |
|-------------------------------------------------|-------------------------------------------------|--------------------------|---------------------------------------------------------|------------------------------------------------------------------------------|
| `ach login --token <pk_>`                       | `GET /platform/whoami`                          | `x-ach-key: <pk_>`       | (none)                                                  | `{owner_email, key_id, key_type, environment?, is_admin, expires_at}`        |
| `ach login --sso` (v2 follow-up)                | `GET /platform/auth/login` → Dex → callback     | (cookie)                 | (none)                                                  | `{key_id, plaintext, owner_email}` (one-time)                                |
| `ach whoami`                                    | `GET /platform/whoami`                          | `x-ach-key: <pk_>`       | (none)                                                  | same as above                                                                |
| `ach hydrate --environment <name>`              | `POST /platform/hydrate`                        | `x-ach-key: <pk_/ek_>`   | `{"environment": "<name>"}`                             | `HydrateResponse` (schemaVersion, environment, runtime{models,mcpServers,a2aAgents}, context{prompts,plugins,artifacts}) |
| `ach env list [--owner-email <email>]`          | `GET /platform/env-keys[?owner_email=...]`      | `x-ach-key: <pk_>`       | (none)                                                  | `{items: [<EkRowView>], next_cursor: <string\|null>}`                        |
| `ach env create --environment <env> --name <n>` | `POST /platform/env-keys`                       | `x-ach-key: <pk_>`       | `{"environment":"<env>","name":"<n>"}`                  | `{key_id, plaintext, environment, name, owner_email, created_at}` (one-time) |
| `ach env revoke <ek_id>`                        | `DELETE /platform/env-keys/<ek_id>`             | `x-ach-key: <pk_>`       | (none)                                                  | `204 No Content`                                                             |

**Acceptance:** the table compiles into Task 3's `internal/cli/doc.go` and is referenced from every subcommand's package comment.

**Commit:** none (paper-only). The table lives in `doc.go` after Task 3.

---

## Task 2: Decide `--sso` strategy (v1 = `--token`, v2 = browser-callback)

**Files:** none — design decision captured here.

**Decision (v1 ship):** `ach login --sso` is **NOT implemented in v1**. Only `ach login --token <pk_>` ships. The shell `hydrate-demo.sh` is amended to keep doing the cookie-jar SSO dance, extract the `pk_`, and call `ach login --token "$PK"` (Task 9). This collapses the demo from 145 lines to ~50 lines without requiring server-side changes to the SSO callback contract.

**Rationale:**
- The existing `/platform/auth/sso/callback` returns the `pk_` to the **browser** as a JSON body. There is no machine-readable handoff to a local CLI listener today.
- Adding one (option B from Pre-flight F3) means either:
  - extending the callback to accept a `?redirect_to=http://127.0.0.1:<port>/cb` parameter and POSTing the credential body to it (security-sensitive change, deserves its own plan + threat model), OR
  - introducing a polling endpoint `/platform/auth/cli-token?session=<uuid>` (more complex; requires a new DB table or Redis key).
- Both are out of scope for §10 and add risk to a plan whose acceptance gate is "the shell script collapses".

**Follow-up note** (NOT in this plan): file a §10.1 ticket for `ach login --sso` browser-callback flow after §7 + §8 land.

**Acceptance:** the decision is documented in `cmd/ach/cmd/login.go`'s package comment (Task 4), and a `// TODO(§10.1): --sso browser-callback flow` line marks the cobra flag stub as a deliberate not-yet.

**Commit:** none.

---

## Task 3: Shared CLI internals — config + HTTP client (TDD)

**Files:**
- Create: `internal/cli/doc.go` — package overview + Task 1 wire table embedded as a comment
- Create: `internal/cli/config.go` — `Config` struct, `Load()`, `Save()` (XDG path, 0600)
- Create: `internal/cli/config_test.go` — unit tests for Load/Save (round-trip, mode bits, missing-file behavior)
- Create: `internal/cli/client.go` — `Client` struct wrapping `*http.Client` with `x-ach-key` injection, JSON encode/decode, error envelope decode
- Create: `internal/cli/client_test.go` — httptest-driven unit tests (200, 4xx envelope, 5xx, network error)
- Create: `internal/cli/errors.go` — typed errors mapped from §15.5 envelope codes (`invalid_argument`, `unauthorized_team`, `not_ready`, `environment_not_found`, `litellm_unreachable`)

**Why first:** every subcommand depends on these. Building them TDD-first means we can develop each subcommand against a stable in-process fake (`httptest.Server`) without ever touching a real cluster until Task 11.

### Step 3.1: `internal/cli/doc.go`

```go
// SPDX-License-Identifier: Apache-2.0

// Package cli is the shared helper layer for every `ach <cmd>` subcommand
// outside the long-running services (operator, platform-api, forwarder,
// content-service, migrate). The package owns:
//
//   - Config: XDG-located JSON file at $XDG_CONFIG_HOME/ach/config.json
//     (default ~/.config/ach/config.json), mode 0600. Carries
//     {endpoint, pk, last_environment}.
//   - Client: a thin *http.Client wrapper that injects `x-ach-key: <pk>`
//     on every request and decodes the §15.5 error envelope on non-2xx.
//   - Typed errors that map 1:1 to the §15.5 wire codes so each
//     subcommand can branch on them without string-matching.
//
// Subcommand → platform-api wire map (the contract this package implements):
//
//   ach login --token <pk_>         → GET    /platform/whoami
//   ach whoami                       → GET    /platform/whoami
//   ach hydrate --environment <n>    → POST   /platform/hydrate
//   ach env list                     → GET    /platform/env-keys
//   ach env create --environment ... → POST   /platform/env-keys
//   ach env revoke <ek_id>           → DELETE /platform/env-keys/<ek_id>
//
// Auth header is x-ach-key (NOT Authorization: Bearer) — see
// examples/hydrate-demo.sh line 138 + internal/platformapi/middleware.
package cli
```

### Step 3.2: `internal/cli/config.go`

```go
// SPDX-License-Identifier: Apache-2.0

package cli

import (
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
)

// Config is the on-disk CLI state. Persisted as JSON at
// $XDG_CONFIG_HOME/ach/config.json (default ~/.config/ach/config.json),
// file mode 0600 (holds a pk_ plaintext per §16.1).
type Config struct {
    Endpoint        string `json:"endpoint"`              // e.g. https://ach.example.com (no trailing slash)
    PK              string `json:"pk"`                    // pk_<26> bearer; persisted between sessions
    LastEnvironment string `json:"last_environment,omitempty"` // remembered from last `hydrate` for ergonomic default
}

// ErrNotLoggedIn is returned by Load when no config file exists (caller
// must run `ach login` first).
var ErrNotLoggedIn = errors.New("ach: not logged in (run `ach login --token <pk_>`)")

// ConfigPath returns the resolved path. Honors XDG_CONFIG_HOME; falls back
// to $HOME/.config/ach/config.json.
func ConfigPath() (string, error) {
    if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
        return filepath.Join(xdg, "ach", "config.json"), nil
    }
    home, err := os.UserHomeDir()
    if err != nil {
        return "", fmt.Errorf("resolve home dir: %w", err)
    }
    return filepath.Join(home, ".config", "ach", "config.json"), nil
}

// Load reads + parses the config file. Returns ErrNotLoggedIn when the file
// does not exist.
func Load() (*Config, error) {
    p, err := ConfigPath()
    if err != nil { return nil, err }
    b, err := os.ReadFile(p)
    if errors.Is(err, os.ErrNotExist) { return nil, ErrNotLoggedIn }
    if err != nil { return nil, fmt.Errorf("read %s: %w", p, err) }
    var c Config
    if err := json.Unmarshal(b, &c); err != nil {
        return nil, fmt.Errorf("parse %s: %w", p, err)
    }
    return &c, nil
}

// Save writes the config atomically with mode 0600. Creates parent dirs as
// needed (mode 0700).
func Save(c *Config) error {
    p, err := ConfigPath()
    if err != nil { return err }
    if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
        return fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
    }
    b, err := json.MarshalIndent(c, "", "  ")
    if err != nil { return fmt.Errorf("marshal config: %w", err) }
    tmp := p + ".tmp"
    if err := os.WriteFile(tmp, b, 0o600); err != nil {
        return fmt.Errorf("write %s: %w", tmp, err)
    }
    if err := os.Rename(tmp, p); err != nil {
        return fmt.Errorf("rename %s: %w", p, err)
    }
    return nil
}
```

### Step 3.3: `internal/cli/config_test.go`

```go
// SPDX-License-Identifier: Apache-2.0

package cli_test

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/ackstorm/ach/internal/cli"
)

func TestConfigRoundTrip(t *testing.T) {
    dir := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", dir)

    if _, err := cli.Load(); err != cli.ErrNotLoggedIn {
        t.Fatalf("Load before save = %v; want ErrNotLoggedIn", err)
    }

    want := &cli.Config{Endpoint: "https://ach.example.test", PK: "pk_abc", LastEnvironment: "demo"}
    if err := cli.Save(want); err != nil { t.Fatalf("Save: %v", err) }

    got, err := cli.Load()
    if err != nil { t.Fatalf("Load: %v", err) }
    if *got != *want { t.Errorf("Load = %+v; want %+v", *got, *want) }

    // Mode 0600 (read/write by owner only — pk_ plaintext invariant).
    p := filepath.Join(dir, "ach", "config.json")
    info, err := os.Stat(p)
    if err != nil { t.Fatalf("Stat: %v", err) }
    if mode := info.Mode().Perm(); mode != 0o600 {
        t.Errorf("config file mode = %o; want 0600", mode)
    }
}

func TestConfigPathFallsBackToHome(t *testing.T) {
    t.Setenv("XDG_CONFIG_HOME", "")
    home := t.TempDir()
    t.Setenv("HOME", home)

    p, err := cli.ConfigPath()
    if err != nil { t.Fatalf("ConfigPath: %v", err) }
    want := filepath.Join(home, ".config", "ach", "config.json")
    if p != want { t.Errorf("ConfigPath = %q; want %q", p, want) }
}
```

### Step 3.4: `internal/cli/errors.go`

```go
// SPDX-License-Identifier: Apache-2.0

package cli

import (
    "errors"
    "fmt"
)

// APIError carries the §15.5 error envelope decoded from a non-2xx
// platform-api response. The Code field is one of the constants below
// (or a literal string if the server returned a code not yet promoted
// to a constant here).
type APIError struct {
    Status  int    // HTTP status code
    Code    string // envelope `error.code` value
    Message string // envelope `error.message` value
    ReqID   string // x-request-id header echoed back for ops correlation
}

func (e *APIError) Error() string {
    return fmt.Sprintf("ach api: %d %s: %s (request_id=%s)", e.Status, e.Code, e.Message, e.ReqID)
}

// Code constants — keep in sync with internal/audit/outcomes.go and the
// §15.5 wire vocabulary. The CLI branches on these to print actionable
// hints (e.g. NotReady → "run `kubectl describe environment/<n>`").
const (
    CodeInvalidArgument    = "invalid_argument"
    CodeInvalidKeyType     = "invalid_key_type"
    CodeUnauthorizedTeam   = "unauthorized_team"
    CodeEnvironmentNotFound = "environment_not_found"
    CodeNotReady           = "not_ready"
    CodeLitellmUnreachable = "litellm_unreachable"
    CodeNotKeyOwner        = "not_key_owner"
    CodeWrongEnvironment   = "wrong_environment"
    CodeInternalError      = "internal_error"
    CodeDBInsertFailed     = "db_insert_failed"
    CodeMissingEnvironment = "missing_environment"
)

// IsCode reports whether err is an *APIError with the given code.
func IsCode(err error, code string) bool {
    var ae *APIError
    if !errors.As(err, &ae) { return false }
    return ae.Code == code
}
```

### Step 3.5: `internal/cli/client.go`

```go
// SPDX-License-Identifier: Apache-2.0

package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"
)

const (
    authHeader        = "x-ach-key"
    requestIDHeader   = "x-request-id"
    defaultUserAgent  = "ach-cli"
    defaultTimeout    = 30 * time.Second
)

// Client is the shared HTTP client every subcommand uses. Each request
// carries the configured pk_ (or ek_) as `x-ach-key` and a User-Agent
// of "ach-cli/<version>". Network/transport errors surface as the
// underlying *url.Error; non-2xx responses surface as *APIError.
type Client struct {
    endpoint string       // e.g. https://ach.example.com (no trailing slash)
    pk       string       // bearer to inject as x-ach-key
    http     *http.Client // configurable for tests (httptest server, custom transport)
    ua       string       // User-Agent suffix (ldflags-injected version at build time)
}

// NewClient builds a Client. endpoint trailing slash is trimmed.
func NewClient(endpoint, pk, version string) *Client {
    return &Client{
        endpoint: strings.TrimRight(endpoint, "/"),
        pk:       pk,
        http:     &http.Client{Timeout: defaultTimeout},
        ua:       defaultUserAgent + "/" + version,
    }
}

// FromConfig is the typical construction: read the config + the ldflag
// Version, wrap them.
func FromConfig(c *Config, version string) *Client {
    return NewClient(c.Endpoint, c.PK, version)
}

// Do is the low-level send. body may be nil. Decodes JSON success into
// out when out != nil; decodes the §15.5 error envelope on non-2xx into
// *APIError.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
    var buf io.Reader
    if body != nil {
        b, err := json.Marshal(body)
        if err != nil { return fmt.Errorf("marshal body: %w", err) }
        buf = bytes.NewReader(b)
    }
    req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, buf)
    if err != nil { return fmt.Errorf("build request: %w", err) }
    if c.pk != "" { req.Header.Set(authHeader, c.pk) }
    if body != nil { req.Header.Set("Content-Type", "application/json") }
    req.Header.Set("Accept", "application/json")
    req.Header.Set("User-Agent", c.ua)

    resp, err := c.http.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()

    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        if out == nil || resp.StatusCode == http.StatusNoContent { return nil }
        if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
            return fmt.Errorf("decode %d response: %w", resp.StatusCode, err)
        }
        return nil
    }

    // Non-2xx → decode §15.5 envelope.
    var env struct {
        Error struct {
            Code    string `json:"code"`
            Message string `json:"message"`
        } `json:"error"`
    }
    _ = json.NewDecoder(resp.Body).Decode(&env) // best-effort
    return &APIError{
        Status:  resp.StatusCode,
        Code:    env.Error.Code,
        Message: env.Error.Message,
        ReqID:   resp.Header.Get(requestIDHeader),
    }
}
```

### Step 3.6: `internal/cli/client_test.go`

```go
// SPDX-License-Identifier: Apache-2.0

package cli_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/ackstorm/ach/internal/cli"
)

func TestClient_DoSuccess(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if got := r.Header.Get("x-ach-key"); got != "pk_test" {
            t.Errorf("x-ach-key = %q; want pk_test", got)
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
    }))
    defer srv.Close()

    c := cli.NewClient(srv.URL, "pk_test", "test")
    var out map[string]string
    if err := c.Do(context.Background(), "GET", "/x", nil, &out); err != nil {
        t.Fatalf("Do: %v", err)
    }
    if out["ok"] != "yes" { t.Errorf("out = %v", out) }
}

func TestClient_DoErrorEnvelope(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("x-request-id", "req-123")
        w.WriteHeader(http.StatusServiceUnavailable)
        _ = json.NewEncoder(w).Encode(map[string]any{
            "error": map[string]string{"code": "not_ready", "message": "environment access group not yet synced"},
        })
    }))
    defer srv.Close()

    c := cli.NewClient(srv.URL, "pk_test", "test")
    err := c.Do(context.Background(), "POST", "/x", map[string]string{"a": "b"}, nil)
    if !cli.IsCode(err, cli.CodeNotReady) {
        t.Fatalf("expected CodeNotReady; got %v", err)
    }
}

func TestClient_Do204(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusNoContent)
    }))
    defer srv.Close()

    c := cli.NewClient(srv.URL, "pk_test", "test")
    if err := c.Do(context.Background(), "DELETE", "/x", nil, nil); err != nil {
        t.Errorf("Do 204: %v", err)
    }
}
```

### Step 3.7: Run + commit

```bash
./scripts/dev.sh go test ./internal/cli/...
./scripts/dev.sh make lint-changed
git add internal/cli/
git commit -m "feat(cli): §10 shared CLI helper — config (XDG, 0600) + HTTP client"
```

**Acceptance:** all three test files pass; `go vet` clean; `make lint-changed` clean.

---

## Task 4: `ach login --token <pk_>` (TDD)

**Files:**
- Create: `cmd/ach/cmd/login.go` — cobra command + RunE
- Create: `cmd/ach/cmd/login_test.go` — unit test driving `RunE` against an httptest fake

**Behavior:**
- Flags: `--endpoint string` (default empty; required if config has no endpoint), `--token string` (required for v1), `--sso bool` (stub; prints "not yet implemented; use --token for now" and exits 1).
- On success: calls `GET /platform/whoami` to validate the token, then writes config to XDG path with the supplied endpoint + pk. Prints `Logged in as <owner_email> (key_id=<pkid_...>) → <endpoint>`.
- On invalid token: prints the API error (e.g. `401 unauthorized: invalid key`) and exits non-zero. Does NOT persist the config.

### Step 4.1: `cmd/ach/cmd/login.go`

Skeleton — mirror the style of `cmd/ach/cmd/migrate.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// `ach login` persists a pk_ bearer + platform-api endpoint into the XDG
// config so subsequent subcommands (whoami, hydrate, env) authenticate
// without re-passing flags. v1 supports --token only; --sso is reserved
// for §10.1 follow-up once the platform-api gains a CLI-callback flow.

package cmd

import (
    "context"
    "errors"
    "fmt"

    "github.com/spf13/cobra"

    "github.com/ackstorm/ach/internal/cli"
    "github.com/ackstorm/ach/internal/platformapi/whoami" // wire type (Task 5)
)

var (
    loginEndpoint string
    loginToken    string
    loginSSO      bool
)

var loginCmd = &cobra.Command{
    Use:   "login",
    Short: "Persist a pk_ bearer + platform-api endpoint to ~/.config/ach/config.json",
    Long: `Validates the supplied pk_ against GET /platform/whoami and, on
success, persists {endpoint, pk} to the XDG config so subsequent
subcommands authenticate transparently.

v1 supports --token only. --sso (browser-driven OAuth2 round-trip) is
reserved for the §10.1 follow-up; today the SSO flow is exercised via
examples/hydrate-demo.sh which extracts the pk_ from the cookie-jar
round-trip and pipes it into 'ach login --token "$PK"'.`,
    RunE: runLogin,
}

func init() {
    loginCmd.Flags().StringVar(&loginEndpoint, "endpoint", "", "platform-api base URL (e.g. https://ach.example.com); required on first login")
    loginCmd.Flags().StringVar(&loginToken, "token", "", "pk_ bearer (required; obtain via examples/hydrate-demo.sh in v1)")
    loginCmd.Flags().BoolVar(&loginSSO, "sso", false, "TODO(§10.1): browser-driven SSO; not implemented in v1")
    rootCmd.AddCommand(loginCmd)
}

func runLogin(cmd *cobra.Command, _ []string) error {
    if loginSSO {
        return errors.New("ach login --sso: not implemented in v1 (see plan §10.1); use --token <pk_>")
    }
    if loginToken == "" {
        return errors.New("ach login: --token is required (v1)")
    }
    endpoint := loginEndpoint
    if endpoint == "" {
        existing, err := cli.Load()
        if err == nil && existing.Endpoint != "" {
            endpoint = existing.Endpoint
        }
    }
    if endpoint == "" {
        return errors.New("ach login: --endpoint required on first login (no prior config to inherit)")
    }

    client := cli.NewClient(endpoint, loginToken, Version)
    var info whoami.Response
    if err := client.Do(context.Background(), "GET", "/platform/whoami", nil, &info); err != nil {
        return fmt.Errorf("validate token: %w", err)
    }

    if err := cli.Save(&cli.Config{Endpoint: endpoint, PK: loginToken}); err != nil {
        return fmt.Errorf("persist config: %w", err)
    }
    fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s (key_id=%s) → %s\n",
        info.OwnerEmail, info.KeyID, endpoint)
    return nil
}
```

### Step 4.2: `cmd/ach/cmd/login_test.go`

```go
// SPDX-License-Identifier: Apache-2.0

package cmd_test

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "testing"

    "github.com/ackstorm/ach/cmd/ach/cmd"
)

func TestLogin_Success(t *testing.T) {
    cfgDir := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", cfgDir)

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/platform/whoami" { t.Errorf("path=%q", r.URL.Path) }
        if r.Header.Get("x-ach-key") != "pk_demo" { t.Errorf("missing x-ach-key") }
        _ = json.NewEncoder(w).Encode(map[string]any{
            "owner_email": "alice@example.com",
            "key_id":      "pkid_zzzz",
            "key_type":    "pk",
            "is_admin":    false,
        })
    }))
    defer srv.Close()

    // Drive cobra exec via os.Args + ExecuteContext on the root.
    var stdout bytes.Buffer
    cmd.SetOutput(&stdout) // small helper added in cmd/ach/cmd/root_test_helpers.go
    cmd.SetArgs([]string{"login", "--endpoint", srv.URL, "--token", "pk_demo"})
    if err := cmd.Execute(); err != nil {
        t.Fatalf("Execute: %v", err)
    }

    // Verify config written.
    b, err := os.ReadFile(filepath.Join(cfgDir, "ach", "config.json"))
    if err != nil { t.Fatalf("read config: %v", err) }
    var c map[string]string
    _ = json.Unmarshal(b, &c)
    if c["pk"] != "pk_demo" || c["endpoint"] != srv.URL {
        t.Errorf("config = %v", c)
    }
}

func TestLogin_InvalidToken(t *testing.T) {
    cfgDir := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", cfgDir)

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusUnauthorized)
        _ = json.NewEncoder(w).Encode(map[string]any{
            "error": map[string]string{"code": "invalid_key_type", "message": "bad key"},
        })
    }))
    defer srv.Close()

    cmd.SetArgs([]string{"login", "--endpoint", srv.URL, "--token", "pk_bad"})
    if err := cmd.Execute(); err == nil {
        t.Fatal("expected error")
    }
    if _, err := os.Stat(filepath.Join(cfgDir, "ach", "config.json")); !os.IsNotExist(err) {
        t.Error("config was persisted despite invalid token")
    }
}
```

Add a small `cmd/ach/cmd/root_test_helpers.go` (build-tagged `//go:build test_helpers` is unnecessary — Go's `_test.go` package-level vars are file-scoped; the helper just exposes `SetArgs` + `SetOutput` for tests):

```go
// SPDX-License-Identifier: Apache-2.0

package cmd

import "io"

// SetArgs is a test helper exposing rootCmd.SetArgs.
func SetArgs(args []string) { rootCmd.SetArgs(args) }

// SetOutput is a test helper exposing rootCmd.SetOut (and SetErr to the same writer).
func SetOutput(w io.Writer) { rootCmd.SetOut(w); rootCmd.SetErr(w) }
```

### Step 4.3: Run + commit

```bash
./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestLogin
./scripts/dev.sh make lint-changed
git add cmd/ach/cmd/login.go cmd/ach/cmd/login_test.go cmd/ach/cmd/root_test_helpers.go
git commit -m "feat(cli): §10 ach login --token (validates via /platform/whoami, persists XDG config)"
```

**Acceptance:** `TestLogin_Success` + `TestLogin_InvalidToken` pass. Note this commit will **not compile** until Task 5 lands the `whoami` package; mark this commit as `-n` (no hooks) — see Task 5 acceptance note — OR collapse Tasks 4+5 into a single commit if the pre-push gate fires on intermediate commits. **Recommended: collapse 4+5 into one commit titled `feat(cli+api): §10 ach login + /platform/whoami contract`.**

---

## Task 5: New `/platform/whoami` endpoint (TDD)

**Files:**
- Create: `internal/platformapi/whoami/doc.go` — package overview
- Create: `internal/platformapi/whoami/handler.go` — `Response` type + `Handler(...)` func
- Create: `internal/platformapi/whoami/handler_test.go` — handler-level tests using `httptest.NewRecorder` + a hand-rolled `KeyContext`
- Modify: `internal/platformapi/server.go` — mount `r.Get("/platform/whoami", whoami.Handler(...))` inside the existing Authn-gated chi.Group

### Step 5.1: `internal/platformapi/whoami/handler.go`

```go
// SPDX-License-Identifier: Apache-2.0

// Package whoami implements GET /platform/whoami — a read-only echo of
// the authenticated KeyContext used by `ach login` to validate a pk_/ek_
// without side effects and by `ach whoami` for human-facing introspection.

package whoami

import (
    "net/http"

    "github.com/ackstorm/ach/internal/platformapi/middleware"
    "github.com/ackstorm/ach/internal/platformapi/render"
)

// Response is the JSON body returned to authenticated callers.
// ExpiresAt is omitted (zero string) for ek_ callers since they don't
// expire via the sliding-window mechanism. Environment is omitted for
// pk_ callers since they aren't scoped to one.
type Response struct {
    OwnerEmail  string `json:"owner_email"`
    KeyID       string `json:"key_id"`
    KeyType     string `json:"key_type"`            // "pk" or "ek"
    Environment string `json:"environment,omitempty"` // ek_ only
    IsAdmin     bool   `json:"is_admin"`
    ExpiresAt   string `json:"expires_at,omitempty"` // pk_ only; RFC3339
}

// Handler returns GET /platform/whoami. No deps beyond the middleware-
// established KeyContext.
func Handler() http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        kc, ok := middleware.KeyContextFromCtx(r.Context())
        if !ok {
            // Authn must have run; this is a wiring bug.
            render.Error(w, http.StatusInternalServerError, "internal_error",
                "auth context missing", middleware.RequestIDFromCtx(r.Context()))
            return
        }
        resp := Response{
            OwnerEmail:  kc.OwnerEmail,
            KeyID:       kc.KeyID,
            KeyType:     kc.KeyType,
            Environment: kc.Environment,
            IsAdmin:     kc.IsAdmin,
        }
        if !kc.ExpiresAt.IsZero() {
            resp.ExpiresAt = kc.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00")
        }
        render.JSON(w, http.StatusOK, resp)
    }
}
```

> Note: confirm `middleware.KeyContext` carries an `ExpiresAt time.Time` field before relying on it; if not, simply drop the `ExpiresAt` line and the field — it's an ergonomic add, not a correctness requirement.

### Step 5.2: `internal/platformapi/whoami/handler_test.go`

```go
// SPDX-License-Identifier: Apache-2.0

package whoami_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/ackstorm/ach/internal/keys"
    "github.com/ackstorm/ach/internal/platformapi/middleware"
    "github.com/ackstorm/ach/internal/platformapi/whoami"
)

func TestWhoami_PK(t *testing.T) {
    h := whoami.Handler()
    req := httptest.NewRequest("GET", "/platform/whoami", nil)
    ctx := middleware.WithKeyContext(req.Context(), middleware.KeyContext{
        KeyID:      "pkid_abc",
        KeyType:    keys.PrefixPk,
        OwnerEmail: "alice@example.com",
        IsAdmin:    true,
    })
    req = req.WithContext(ctx)
    w := httptest.NewRecorder()
    h(w, req)
    if w.Code != http.StatusOK { t.Fatalf("status=%d body=%s", w.Code, w.Body.String()) }
    var got whoami.Response
    _ = json.NewDecoder(w.Body).Decode(&got)
    if got.OwnerEmail != "alice@example.com" || got.KeyType != "pk" || !got.IsAdmin {
        t.Errorf("got = %+v", got)
    }
    if got.Environment != "" { t.Errorf("Environment should be empty for pk_; got %q", got.Environment) }
}

func TestWhoami_EK(t *testing.T) {
    h := whoami.Handler()
    req := httptest.NewRequest("GET", "/platform/whoami", nil)
    ctx := middleware.WithKeyContext(req.Context(), middleware.KeyContext{
        KeyID:       "ekid_xyz",
        KeyType:     keys.PrefixEk,
        OwnerEmail:  "alice@example.com",
        Environment: "demo",
    })
    req = req.WithContext(ctx)
    w := httptest.NewRecorder()
    h(w, req)
    var got whoami.Response
    _ = json.NewDecoder(w.Body).Decode(&got)
    if got.KeyType != "ek" || got.Environment != "demo" {
        t.Errorf("got = %+v", got)
    }
}

func TestWhoami_NoContext_500(t *testing.T) {
    h := whoami.Handler()
    req := httptest.NewRequest("GET", "/platform/whoami", nil)
    w := httptest.NewRecorder()
    h(w, req)
    if w.Code != http.StatusInternalServerError {
        t.Errorf("status=%d; want 500", w.Code)
    }
    _ = context.Background()
}
```

### Step 5.3: Mount in `server.go`

In `internal/platformapi/server.go`, inside the `r.Group(func(r chi.Router) { ... })` block that already wires hydrate/env-keys/environments/admin, add **as the first authenticated route**:

```go
r.Get("/platform/whoami", whoami.Handler())
```

…plus the import: `"github.com/ackstorm/ach/internal/platformapi/whoami"`.

### Step 5.4: Run + commit

```bash
./scripts/dev.sh go test ./internal/platformapi/whoami/...
./scripts/dev.sh go test ./internal/platformapi/...  # full platform-api regression
./scripts/dev.sh make lint-changed
git add internal/platformapi/whoami/ internal/platformapi/server.go
git commit -m "feat(platform-api): §10 add GET /platform/whoami (auth context echo for ach login + whoami)"
```

**Acceptance:** new tests pass; `server_test.go` still green (the new route is additive — no existing wiring changed).

**Note (Task 4+5 commit ordering):** if collapsing Tasks 4+5 to keep `main` always-compilable, the single commit message is `feat(cli+api): §10 ach login + /platform/whoami contract`. The decision is local to the executor.

---

## Task 6: `ach whoami` (TDD)

**Files:**
- Create: `cmd/ach/cmd/whoami.go`
- Create: `cmd/ach/cmd/whoami_test.go`

### Step 6.1: `cmd/ach/cmd/whoami.go`

```go
// SPDX-License-Identifier: Apache-2.0

// `ach whoami` echoes the authenticated KeyContext as resolved by the
// platform-api's GET /platform/whoami (Task 5). Useful for "what pk_ am
// I using?" and for verifying token rotation. Pretty-prints the JSON
// when stdout is a TTY; emits raw JSON on a pipe (so `ach whoami |
// jq -r .owner_email` works).

package cmd

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/spf13/cobra"

    "github.com/ackstorm/ach/internal/cli"
    "github.com/ackstorm/ach/internal/platformapi/whoami"
)

var whoamiCmd = &cobra.Command{
    Use:   "whoami",
    Short: "Print the authenticated user/key for the active config",
    RunE:  runWhoami,
}

func init() { rootCmd.AddCommand(whoamiCmd) }

func runWhoami(cmd *cobra.Command, _ []string) error {
    cfg, err := cli.Load()
    if err != nil { return err }
    client := cli.FromConfig(cfg, Version)

    var info whoami.Response
    if err := client.Do(context.Background(), "GET", "/platform/whoami", nil, &info); err != nil {
        return err
    }
    b, _ := json.MarshalIndent(info, "", "  ")
    fmt.Fprintln(cmd.OutOrStdout(), string(b))
    return nil
}
```

### Step 6.2: `cmd/ach/cmd/whoami_test.go`

```go
// SPDX-License-Identifier: Apache-2.0

package cmd_test

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/ackstorm/ach/cmd/ach/cmd"
    "github.com/ackstorm/ach/internal/cli"
)

func TestWhoami(t *testing.T) {
    cfgDir := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", cfgDir)

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/platform/whoami" { t.Errorf("path=%q", r.URL.Path) }
        if r.Header.Get("x-ach-key") != "pk_test" { t.Errorf("missing x-ach-key") }
        _ = json.NewEncoder(w).Encode(map[string]any{
            "owner_email": "carol@example.com",
            "key_id":      "pkid_xyz",
            "key_type":    "pk",
            "is_admin":    false,
        })
    }))
    defer srv.Close()

    if err := cli.Save(&cli.Config{Endpoint: srv.URL, PK: "pk_test"}); err != nil {
        t.Fatalf("seed config: %v", err)
    }

    var stdout bytes.Buffer
    cmd.SetOutput(&stdout)
    cmd.SetArgs([]string{"whoami"})
    if err := cmd.Execute(); err != nil { t.Fatalf("Execute: %v", err) }

    if !strings.Contains(stdout.String(), `"owner_email": "carol@example.com"`) {
        t.Errorf("stdout = %q", stdout.String())
    }
}

func TestWhoami_NotLoggedIn(t *testing.T) {
    cfgDir := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", cfgDir)
    cmd.SetArgs([]string{"whoami"})
    if err := cmd.Execute(); err == nil {
        t.Fatal("expected ErrNotLoggedIn")
    }
}
```

### Step 6.3: Run + commit

```bash
./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestWhoami
git add cmd/ach/cmd/whoami.go cmd/ach/cmd/whoami_test.go
git commit -m "feat(cli): §10 ach whoami (echo /platform/whoami JSON)"
```

---

## Task 7: `ach hydrate --environment <name>` (TDD with golden-file diff)

**Files:**
- Create: `cmd/ach/cmd/hydrate.go`
- Create: `cmd/ach/cmd/hydrate_test.go`
- Create: `cmd/ach/cmd/testdata/hydrate-demo-golden.json` (copy of `examples/hydrate.json` — bytes-identical at this moment so the golden test pins drift)

### Step 7.1: `cmd/ach/cmd/hydrate.go`

```go
// SPDX-License-Identifier: Apache-2.0

// `ach hydrate --environment <name>` is the load-bearing subcommand: it
// reproduces the JSON returned by examples/hydrate-demo.sh step 6 so the
// shell driver can be deleted in favor of the test/e2e Go fixture in
// Task 11. Output goes to stdout (or --output <file>) as canonicalized
// JSON (2-space indent + trailing newline) so byte-diffing against the
// recorded golden (examples/hydrate.json) is stable.

package cmd

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"

    "github.com/spf13/cobra"

    "github.com/ackstorm/ach/internal/cli"
    "github.com/ackstorm/ach/internal/platformapi/hydrate"
)

var (
    hydrateEnvironment string
    hydrateOutput      string
)

var hydrateCmd = &cobra.Command{
    Use:   "hydrate",
    Short: "Fetch the §15.2 manifest for an Environment (replaces examples/hydrate-demo.sh step 6)",
    RunE:  runHydrate,
}

func init() {
    hydrateCmd.Flags().StringVar(&hydrateEnvironment, "environment", "", "Environment name (required for pk_ callers; ek_ uses its bound env)")
    hydrateCmd.Flags().StringVar(&hydrateOutput, "output", "", "Write JSON to this path (default: stdout)")
    rootCmd.AddCommand(hydrateCmd)
}

func runHydrate(cmd *cobra.Command, _ []string) error {
    cfg, err := cli.Load()
    if err != nil { return err }
    if hydrateEnvironment == "" && cfg.LastEnvironment != "" {
        hydrateEnvironment = cfg.LastEnvironment
    }
    if hydrateEnvironment == "" {
        return errors.New("ach hydrate: --environment is required (no last_environment in config)")
    }

    client := cli.FromConfig(cfg, Version)
    var resp hydrate.HydrateResponse
    body := map[string]string{"environment": hydrateEnvironment}
    if err := client.Do(context.Background(), "POST", "/platform/hydrate", body, &resp); err != nil {
        // Friendly hints for common §15.5 codes.
        switch {
        case cli.IsCode(err, cli.CodeNotReady):
            return fmt.Errorf("%w\nhint: run `kubectl describe environment/%s` to see why AccessGroupSynced != True", err, hydrateEnvironment)
        case cli.IsCode(err, cli.CodeEnvironmentNotFound):
            return fmt.Errorf("%w\nhint: kubectl get environment | grep -i %s", err, hydrateEnvironment)
        }
        return err
    }

    out := cmd.OutOrStdout()
    if hydrateOutput != "" {
        f, err := os.Create(hydrateOutput)
        if err != nil { return fmt.Errorf("open output: %w", err) }
        defer f.Close()
        out = f
    }
    return writeCanonicalJSON(out, &resp, cfg, hydrateEnvironment)
}

// writeCanonicalJSON emits the response with 2-space indent + trailing
// newline so byte-diffing against examples/hydrate.json is stable across
// machines. Also persists last_environment to config for ergonomic
// defaulting on the next call.
func writeCanonicalJSON(w io.Writer, resp *hydrate.HydrateResponse, cfg *cli.Config, env string) error {
    b, err := json.MarshalIndent(resp, "", "  ")
    if err != nil { return fmt.Errorf("marshal response: %w", err) }
    if _, err := w.Write(b); err != nil { return fmt.Errorf("write output: %w", err) }
    if _, err := w.Write([]byte("\n")); err != nil { return fmt.Errorf("write trailing newline: %w", err) }

    // Best-effort update last_environment (do not fail if config write fails).
    cfg.LastEnvironment = env
    _ = cli.Save(cfg)
    return nil
}
```

### Step 7.2: `cmd/ach/cmd/hydrate_test.go`

```go
// SPDX-License-Identifier: Apache-2.0

package cmd_test

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "testing"

    "github.com/ackstorm/ach/cmd/ach/cmd"
    "github.com/ackstorm/ach/internal/cli"
)

func TestHydrate_GoldenByteDiff(t *testing.T) {
    cfgDir := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", cfgDir)

    golden, err := os.ReadFile(filepath.Join("testdata", "hydrate-demo-golden.json"))
    if err != nil { t.Fatalf("read golden: %v", err) }

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/platform/hydrate" { t.Errorf("path=%q", r.URL.Path) }
        if r.Header.Get("x-ach-key") != "pk_test" { t.Errorf("missing x-ach-key") }
        var req map[string]string
        _ = json.NewDecoder(r.Body).Decode(&req)
        if req["environment"] != "demo" { t.Errorf("body env=%q", req["environment"]) }
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write(golden)
    }))
    defer srv.Close()

    if err := cli.Save(&cli.Config{Endpoint: srv.URL, PK: "pk_test"}); err != nil { t.Fatalf("seed: %v", err) }

    var stdout bytes.Buffer
    cmd.SetOutput(&stdout)
    cmd.SetArgs([]string{"hydrate", "--environment", "demo"})
    if err := cmd.Execute(); err != nil { t.Fatalf("Execute: %v", err) }

    if !bytes.Equal(bytes.TrimSpace(stdout.Bytes()), bytes.TrimSpace(golden)) {
        t.Errorf("hydrate output drift vs golden\n--- got ---\n%s\n--- want ---\n%s", stdout.String(), string(golden))
    }
}

func TestHydrate_NotReady_HintsAccessGroup(t *testing.T) {
    cfgDir := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", cfgDir)

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("x-request-id", "r-1")
        w.WriteHeader(http.StatusServiceUnavailable)
        _ = json.NewEncoder(w).Encode(map[string]any{
            "error": map[string]string{"code": "not_ready", "message": "access group not synced"},
        })
    }))
    defer srv.Close()

    _ = cli.Save(&cli.Config{Endpoint: srv.URL, PK: "pk_test"})
    cmd.SetArgs([]string{"hydrate", "--environment", "demo"})
    err := cmd.Execute()
    if err == nil { t.Fatal("expected error") }
    if !bytes.Contains([]byte(err.Error()), []byte("kubectl describe environment/demo")) {
        t.Errorf("error missing actionable hint: %v", err)
    }
}
```

### Step 7.3: Seed the golden file

```bash
cp examples/hydrate.json cmd/ach/cmd/testdata/hydrate-demo-golden.json
```

### Step 7.4: Run + commit

```bash
./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestHydrate
git add cmd/ach/cmd/hydrate.go cmd/ach/cmd/hydrate_test.go cmd/ach/cmd/testdata/
git commit -m "feat(cli): §10 ach hydrate --environment (golden-file diffed vs examples/hydrate.json)"
```

**Acceptance:** `TestHydrate_GoldenByteDiff` passes, proving the CLI output is byte-equivalent to the recorded shell-driven snapshot. Any future drift in `hydrate.HydrateResponse` field tags will trip this test.

---

## Task 8: `ach env list / create / revoke` (TDD)

**Files:**
- Create: `cmd/ach/cmd/env.go` — parent `env` command + three subcommands (`list`, `create`, `revoke`)
- Create: `cmd/ach/cmd/env_test.go` — httptest-driven unit tests for all three

### Step 8.1: `cmd/ach/cmd/env.go`

```go
// SPDX-License-Identifier: Apache-2.0

// `ach env <list|create|revoke>` wraps the /platform/env-keys CRUD
// surface (POST/GET/DELETE). Mirrors the wire types in
// internal/platformapi/envkeys/handler.go so a struct-tag drift in the
// server breaks compilation here.

package cmd

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net/url"

    "github.com/spf13/cobra"

    "github.com/ackstorm/ach/internal/cli"
    "github.com/ackstorm/ach/internal/platformapi/envkeys"
)

var envCmd = &cobra.Command{
    Use:   "env",
    Short: "Manage Environment Keys (ek_) — list, create, revoke",
}

// --- list ---

var envListOwnerEmail string

var envListCmd = &cobra.Command{
    Use:   "list",
    Short: "List Environment Keys owned by the caller (admin: optionally --owner-email <email>)",
    RunE:  runEnvList,
}

func runEnvList(cmd *cobra.Command, _ []string) error {
    cfg, err := cli.Load()
    if err != nil { return err }
    client := cli.FromConfig(cfg, Version)

    path := "/platform/env-keys"
    if envListOwnerEmail != "" {
        path += "?owner_email=" + url.QueryEscape(envListOwnerEmail)
    }
    var resp envkeys.ListResponse
    if err := client.Do(context.Background(), "GET", path, nil, &resp); err != nil { return err }

    b, _ := json.MarshalIndent(resp, "", "  ")
    fmt.Fprintln(cmd.OutOrStdout(), string(b))
    return nil
}

// --- create ---

var (
    envCreateEnvironment string
    envCreateName        string
)

var envCreateCmd = &cobra.Command{
    Use:   "create",
    Short: "Create an ek_ scoped to an Environment (plaintext is shown exactly once)",
    RunE:  runEnvCreate,
}

func runEnvCreate(cmd *cobra.Command, _ []string) error {
    if envCreateEnvironment == "" || envCreateName == "" {
        return errors.New("ach env create: --environment and --name are required")
    }
    cfg, err := cli.Load()
    if err != nil { return err }
    client := cli.FromConfig(cfg, Version)

    body := envkeys.CreateRequest{Environment: envCreateEnvironment, Name: envCreateName}
    var resp envkeys.CreateResponse
    if err := client.Do(context.Background(), "POST", "/platform/env-keys", body, &resp); err != nil {
        if cli.IsCode(err, cli.CodeNotReady) {
            return fmt.Errorf("%w\nhint: §7 AccessGroupSynced reconciler must reach True before ek_ create succeeds", err)
        }
        return err
    }
    b, _ := json.MarshalIndent(resp, "", "  ")
    fmt.Fprintln(cmd.OutOrStdout(), string(b))
    fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: the 'plaintext' field above is shown EXACTLY ONCE. Save it now.")
    return nil
}

// --- revoke ---

var envRevokeCmd = &cobra.Command{
    Use:   "revoke <ekid_...>",
    Short: "Revoke an ek_ by its key_id (LiteLLM-first per §8.5)",
    Args:  cobra.ExactArgs(1),
    RunE:  runEnvRevoke,
}

func runEnvRevoke(cmd *cobra.Command, args []string) error {
    keyID := args[0]
    cfg, err := cli.Load()
    if err != nil { return err }
    client := cli.FromConfig(cfg, Version)

    if err := client.Do(context.Background(), "DELETE", "/platform/env-keys/"+url.PathEscape(keyID), nil, nil); err != nil {
        return err
    }
    fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s\n", keyID)
    return nil
}

func init() {
    envListCmd.Flags().StringVar(&envListOwnerEmail, "owner-email", "", "admin-only: filter by owner_email")
    envCreateCmd.Flags().StringVar(&envCreateEnvironment, "environment", "", "Environment name (required)")
    envCreateCmd.Flags().StringVar(&envCreateName, "name", "", "human-friendly name for the ek_ (required)")
    envCmd.AddCommand(envListCmd, envCreateCmd, envRevokeCmd)
    rootCmd.AddCommand(envCmd)
}
```

### Step 8.2: `cmd/ach/cmd/env_test.go`

Cover all three branches with httptest fakes (matches the patterns from Task 4 + 7). Tests to add:
- `TestEnvList_OK`: 200 with two rows; verify JSON contains both `key_id` values.
- `TestEnvList_AdminFilter`: sends `?owner_email=bob@x` when `--owner-email bob@x` is passed.
- `TestEnvCreate_OK`: 200 with `{key_id, plaintext, ...}`; verifies stderr contains the "shown EXACTLY ONCE" warning.
- `TestEnvCreate_NotReady_HintsReconciler`: 503 not_ready; error mentions `§7 AccessGroupSynced`.
- `TestEnvRevoke_204`: DELETE to `/platform/env-keys/ekid_abc` → 204 → stdout `Revoked ekid_abc`.
- `TestEnvRevoke_NotKeyOwner`: 403 not_key_owner; error surfaced verbatim.

### Step 8.3: Run + commit

```bash
./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestEnv
git add cmd/ach/cmd/env.go cmd/ach/cmd/env_test.go
git commit -m "feat(cli): §10 ach env list/create/revoke (POST/GET/DELETE /platform/env-keys)"
```

**Acceptance:** all six tests pass.

---

## Task 9: Slim `examples/hydrate-demo.sh` to use the new CLI

**Files:**
- Modify: `examples/hydrate-demo.sh` — keep steps 1-4 (LiteLLM team seed, CR apply, wait for ExecutionResourcesResolved, port-forward); replace step 5 to extract the `pk_` from the SSO round-trip and feed it to `ach login --token`; replace step 6 with `ach hydrate --environment $ENV_NAME --output $OUT`.

### Step 9.1: Edit the script

Concrete diff outline (the executor refines the actual sed/edit):
- Keep lines 1-90 (preamble, LiteLLM team seed, kubectl apply, wait, port-forwards) verbatim.
- Replace step 5 (lines 91-132) with: extract `PK` via curl + cookie jar exactly as today (lines 92-112 remain), then `ach login --endpoint "http://localhost:${PLATFORM_API_PORT}" --token "${PK}"` instead of `echo "[hydrate-demo]   pk minted: ..."`.
- Replace step 6 (lines 134-141) with: `ach hydrate --environment "${ENV_NAME}" --output "${OUT}"`.
- Add at the top: `ACH_BIN="${ACH_BIN:-./bin/ach}"` and prefix the two `ach` calls accordingly so the script works against a freshly-built binary without requiring `$PATH` plumbing.

### Step 9.2: Validate manually (no commit yet)

```bash
./scripts/dev.sh make build           # produces ./bin/ach with the new subcommands
make cluster-up                       # if not already up
./examples/hydrate-demo.sh            # should produce examples/hydrate.json byte-equivalent to the recorded golden
diff <(jq -S . examples/hydrate.json) <(jq -S . cmd/ach/cmd/testdata/hydrate-demo-golden.json)  # should be empty
```

### Step 9.3: Commit

```bash
git add examples/hydrate-demo.sh
git commit -m "refactor(examples): hydrate-demo.sh — call \`ach login\`/\`ach hydrate\` (drops 70+ lines of curl)"
```

**Acceptance:** the script runs end-to-end (assuming §7+§8 land) and produces a `hydrate.json` byte-equivalent to the recorded golden.

---

## Task 10: Documentation — README + CLAUDE.md

**Files:**
- Modify: `README.md` — add a "CLI quickstart" section
- Modify: `CLAUDE.md` — add a row to the "Common failure modes" section for the `--token` paste pattern + a row to the architecture table noting the CLI surface

### Step 10.1: README "CLI quickstart" section

Add after the existing "Architecture" section:

```markdown
## CLI quickstart

Once a cluster is running (see "Toolchain" below), authenticate and pull an
Environment manifest:

```bash
# v1: paste a pk_ obtained via examples/hydrate-demo.sh (the script extracts
# the cookie-jar SSO round-trip output and runs `ach login --token "$PK"`
# for you).
./bin/ach login --endpoint https://ach.example.com --token "pk_..."

# Inspect the active identity:
./bin/ach whoami

# Pull the §15.2 manifest for an Environment:
./bin/ach hydrate --environment demo > hydrate.json

# Manage Environment Keys (ek_):
./bin/ach env list
./bin/ach env create --environment demo --name "ci-runner"  # plaintext shown exactly once
./bin/ach env revoke ekid_xxxxxxxxxxxx
```

The browser-driven `--sso` flow is scheduled for §10.1 follow-up — see
`docs/plans/2026-05-26-cli-commands.md` Task 2 rationale.
```

### Step 10.2: CLAUDE.md additions

Add to the architecture table (the table that already lists the five long-running modes), a new section underneath:

```markdown
**CLI subcommands** (no long-running process; consult `internal/cli/`):

| Subcommand                 | Wraps                                        | Notes                                          |
|----------------------------|----------------------------------------------|------------------------------------------------|
| `ach login --token <pk_>`  | `GET /platform/whoami`                       | Persists XDG config; v1 paste-mode             |
| `ach whoami`               | `GET /platform/whoami`                       | Reads XDG config                               |
| `ach hydrate --environment`| `POST /platform/hydrate`                     | Output is byte-equivalent to examples/hydrate.json |
| `ach env list`             | `GET /platform/env-keys`                     | Admin: `--owner-email` filter                  |
| `ach env create`           | `POST /platform/env-keys`                    | Plaintext shown exactly once                   |
| `ach env revoke <ek_id>`   | `DELETE /platform/env-keys/<ek_id>`          | LiteLLM-first per §8.5                         |
```

Add to "Common failure modes":

```markdown
### ❌ Using `Authorization: Bearer pk_...` instead of `x-ach-key: pk_...`
```bash
curl -H "Authorization: Bearer pk_demo" .../platform/whoami
# 401 Unauthorized
```
✅ The platform-api Authn middleware reads `x-ach-key`, not `Authorization`:
```bash
curl -H "x-ach-key: pk_demo" .../platform/whoami
# 200 OK
```
WHY IT FAILS: `internal/platformapi/middleware/auth.go` extracts the bearer from
`x-ach-key` exclusively. The CLI (`internal/cli/client.go`) handles this
automatically — only manual curl invocations are at risk.
```

### Step 10.3: Run + commit

```bash
git add README.md CLAUDE.md
git commit -m "docs(cli): §10 add quickstart + CLAUDE.md surface notes"
```

---

## Task 11: e2e test driving the full CLI against a kept cluster

**Files:**
- Create: `test/e2e/cli_hydrate_test.go` — Go test that drives `./bin/ach login` + `./bin/ach hydrate` against the kept kind cluster

**Why:** the acceptance gate for §10 is "examples/hydrate-demo.sh collapses to a one-liner; new flow becomes e2e test fixture". This task lands the fixture.

### Step 11.1: `test/e2e/cli_hydrate_test.go`

```go
// SPDX-License-Identifier: Apache-2.0

// Plan §10 e2e: drives `./bin/ach login` + `./bin/ach hydrate` end-to-end
// against the kept kind cluster, asserts the resulting JSON is byte-
// equivalent to examples/hydrate.json (the golden). Replaces examples/
// hydrate-demo.sh step 5 + 6 as the canonical CLI regression.

package e2e

import (
    "bytes"
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestCLI_HydrateGoldenDiff(t *testing.T) {
    if os.Getenv("ACH_E2E") == "" {
        t.Skip("set ACH_E2E=1 to run against a kept kind cluster")
    }
    // Pre-req: examples/hydrate-demo.sh steps 1-4 must have run (LiteLLM team
    // seeded, CRs applied, port-forwards live). The e2e Makefile target wraps
    // this; running the test bare assumes the operator already setup the
    // demo Environment.
    pk := os.Getenv("ACH_E2E_PK")
    endpoint := os.Getenv("ACH_E2E_ENDPOINT") // e.g. http://localhost:8080
    if pk == "" || endpoint == "" {
        t.Skip("ACH_E2E_PK and ACH_E2E_ENDPOINT required")
    }

    tmp := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", tmp)
    bin := os.Getenv("ACH_BIN")
    if bin == "" { bin = "./bin/ach" }

    // 1. login
    if out, err := exec.Command(bin, "login", "--endpoint", endpoint, "--token", pk).CombinedOutput(); err != nil {
        t.Fatalf("ach login: %v\n%s", err, out)
    }

    // 2. hydrate
    var stdout bytes.Buffer
    cmd := exec.Command(bin, "hydrate", "--environment", "demo")
    cmd.Stdout = &stdout
    if err := cmd.Run(); err != nil {
        t.Fatalf("ach hydrate: %v\n%s", err, stdout.String())
    }

    // 3. golden diff (canonicalize both via jq-equivalent: re-marshal sorted).
    golden, err := os.ReadFile(filepath.Join("..", "..", "examples", "hydrate.json"))
    if err != nil { t.Fatalf("read golden: %v", err) }

    if !jsonEqual(t, stdout.Bytes(), golden) {
        t.Errorf("hydrate output diverged from examples/hydrate.json\n--- got ---\n%s\n--- want ---\n%s",
            stdout.String(), string(golden))
    }
}

// jsonEqual round-trips both blobs through json.Unmarshal/Marshal so
// whitespace + key-order differences are normalized away.
func jsonEqual(t *testing.T, a, b []byte) bool {
    t.Helper()
    var av, bv any
    if err := json.Unmarshal(a, &av); err != nil { t.Fatalf("unmarshal a: %v", err) }
    if err := json.Unmarshal(b, &bv); err != nil { t.Fatalf("unmarshal b: %v", err) }
    am, _ := json.Marshal(av)
    bm, _ := json.Marshal(bv)
    return bytes.Equal(am, bm)
}
```

### Step 11.2: Wire the test into `make e2e`

Confirm `test/e2e/` is already covered by `make e2e-full` / `make e2e-focus` (it is — see CLAUDE.md "E2E debug loop" section). The new test runs automatically once the `ACH_E2E=1` + token env vars are set. Update `examples/hydrate-demo.sh` to export those vars before exiting so the human round-trip story is "run the script, then `ACH_E2E=1 ./scripts/dev.sh make e2e-focus FOCUS=TestCLI_Hydrate`".

### Step 11.3: Run + commit

```bash
# Local manual smoke (requires kept cluster):
make cluster-keep
./examples/hydrate-demo.sh          # produces hydrate.json + exports ACH_E2E_PK/ENDPOINT
ACH_E2E=1 ./scripts/dev.sh make e2e-focus FOCUS=TestCLI_HydrateGoldenDiff

git add test/e2e/cli_hydrate_test.go
git commit -m "test(e2e): §10 CLI end-to-end — ach login + hydrate vs examples/hydrate.json golden"
```

**Acceptance:** the test passes against a kept cluster with §7 + §8 landed; skips cleanly when env vars are absent (so `make e2e-full` on a CI runner without the §7 deps doesn't fail).

---

## Task 12: Delete `examples/hydrate-demo.sh` once §7 + §8 are green (DEFERRED)

**Files:**
- (Eventually) Delete: `examples/hydrate-demo.sh`
- (Eventually) Modify: `examples/README.md` — point readers at the CLI quickstart + the e2e test

**Why deferred:** the script today does work that the CLI cannot do end-to-end yet (Dex SSO round-trip from a headless CI runner). Even after §7 + §8 land, the script remains useful as the **bootstrap** step that produces the first `pk_`. Delete it only when `ach login --sso` (the §10.1 follow-up) ships.

**Acceptance for §10:** this task is documented but **not executed** as part of this plan. The plan's gate is met by the `ach login --token` paste-mode + the e2e fixture in Task 11.

---

## Final verification (run before opening the PR)

```bash
cd /home/jcm/Projects/ach

# Per-package green.
./scripts/dev.sh go test ./internal/cli/...
./scripts/dev.sh go test ./internal/platformapi/whoami/...
./scripts/dev.sh go test ./cmd/ach/cmd/... -run 'TestLogin|TestWhoami|TestHydrate|TestEnv'

# Full sweep.
./scripts/dev.sh make unit
./scripts/dev.sh make envtest-run

# Lint + security.
./scripts/dev.sh make lint
./scripts/dev.sh make security

# e2e (kept cluster; requires §7 + §8 landed for a fully green run).
make cluster-keep
./examples/hydrate-demo.sh
ACH_E2E=1 ./scripts/dev.sh make e2e-focus FOCUS=TestCLI_HydrateGoldenDiff

# Pre-push gate (the 17-gate publication check).
make pre-push

git push -u origin feat/cli-subcommands
gh pr create \
  --title "feat(cli): §10 login + whoami + hydrate + env CRUD" \
  --body "$(cat <<'EOF'
## Summary
- New subcommands: `ach login --token`, `ach whoami`, `ach hydrate --environment`, `ach env {list,create,revoke}`.
- New platform-api endpoint: `GET /platform/whoami` (read-only KeyContext echo).
- New shared package `internal/cli/` with XDG-located config (mode 0600) + `x-ach-key`-injecting HTTP client.
- `examples/hydrate-demo.sh` slimmed by ~70 lines; CLI byte-diffs vs `examples/hydrate.json` golden in a new e2e test.

## Test plan
- [ ] `./scripts/dev.sh go test ./internal/cli/...`
- [ ] `./scripts/dev.sh go test ./internal/platformapi/whoami/...`
- [ ] `./scripts/dev.sh go test ./cmd/ach/cmd/... -run 'TestLogin|TestWhoami|TestHydrate|TestEnv'`
- [ ] `./scripts/dev.sh make envtest-run` (controller-runtime regression)
- [ ] `./scripts/dev.sh make lint` (full sweep)
- [ ] `make pre-push` (17-gate publication check)
- [ ] Kept-cluster e2e: `ACH_E2E=1 ./scripts/dev.sh make e2e-focus FOCUS=TestCLI_HydrateGoldenDiff`

## Out of scope / follow-up
- `ach login --sso` browser-callback flow → §10.1 (deferred; rationale in plan Task 2).
- Delete `examples/hydrate-demo.sh` → §10.2 (deferred until `--sso` ships).
EOF
)"
```

---

## Cross-plan dependency summary

| Item                                    | Status                                                                                                  |
|-----------------------------------------|---------------------------------------------------------------------------------------------------------|
| Platform-api routes wired (§2)          | ✅ — confirmed in `internal/platformapi/server.go`                                                       |
| `ach env create` succeeds (§7)          | ⏳ — requires `AccessGroupSynced=True`; until then returns `503 not_ready`. CLI test in Task 8 covers the friendly-error path so the CLI works against a degraded cluster even before §7 lands. |
| Hydrate `downloadUrls` resolve (§8)     | ⏳ — Content Service must be reachable for the URLs the CLI prints to be useful; CLI itself does not follow them, so Task 11's golden test passes without §8. |
| Helm chart (§3)                         | Independent.                                                                                            |
| Marketplace real schema (§5)            | Independent.                                                                                            |
| BIP forwarder read path (§6)            | Independent.                                                                                            |

**Bottom line:** Tasks 1-7 + 10 land cleanly today. Task 8 (env create) lands with a friendly error message that becomes a happy path the moment §7 ships. Task 11 e2e becomes fully green once §7 + §8 are both in.
