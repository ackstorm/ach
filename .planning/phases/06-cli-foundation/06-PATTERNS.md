# Phase 6: CLI Foundation - Pattern Map

**Mapped:** 2026-05-28
**Phase:** 06-cli-foundation
**Files analyzed:** 27 new files + 5 modified files
**Analogs found:** 27 / 27 (100% — every new file has a close codebase analog)

## Scope Recap

Phase 6 surface (from `06-CONTEXT.md` `<domain>` block):

- **8 new cobra subcommands** at `cmd/ach/cmd/<verb>.go` (`login`, `logout`, `whoami`, `config`, `env`, `env_keys`, `hydrate`, `admin`).
- **~12 new files** under a new `internal/cli/` package (config registry, HTTP client, exit codes, redaction, synthetic-mode gate, render, device-code).
- **2 new Platform API endpoints** under `internal/platformapi/auth/cli/` (`init` + `token`) + minor extension to `internal/platformapi/auth/sso.go` (D-20 session-id threading).
- **1 new audit constant** (`ActionCliLogin`) in `internal/audit/events.go`.
- **1 new e2e test** `test/e2e/cli_login_hydrate_test.go`.
- **3 modified files** — `internal/platformapi/server.go` (mount the new CLI auth subtree), `examples/README.md` (drop hydrate-demo.sh), root `README.md` + `CLAUDE.md` "Common failure modes".
- **1 deleted file** — `examples/hydrate-demo.sh` (Wave 3 D-17).

---

## File Classification

### A. Cobra subcommands (cmd/ach/cmd/) — role=cobra-subcommand, data-flow=request-response

| New File | Wave | Role | Data Flow | Closest Analog | Match |
|---|---|---|---|---|---|
| `cmd/ach/cmd/login.go` | W1 | cobra-subcommand | request-response (device-code poll loop) | `cmd/ach/cmd/migrate.go` (style); `internal/platformapi/auth/sso.go` (server-side counterpart) | role-match |
| `cmd/ach/cmd/logout.go` | W2 | cobra-subcommand | local-config-mutate | `cmd/ach/cmd/migrate.go` | exact (smallest subcommand) |
| `cmd/ach/cmd/whoami.go` | W1 | cobra-subcommand | request-response (one HTTP call) | `cmd/ach/cmd/migrate.go` + future `internal/cli/httpclient.go` | role-match |
| `cmd/ach/cmd/config.go` | W2 | cobra-subcommand (5 sub-subcommands: list/show/use/remove/rename) | local-config-mutate | `cmd/ach/cmd/migrate.go` (single-cmd) + cobra parent-with-children pattern (see below) | role-match |
| `cmd/ach/cmd/env.go` | W2 | cobra-subcommand (2 sub-subcommands: list/describe) | request-response (paginated GET + 2-call describe) | `cmd/ach/cmd/migrate.go` (style); `internal/platformapi/environments/handler.go` (server contract) | role-match |
| `cmd/ach/cmd/env_keys.go` | W2 | cobra-subcommand (3 sub-subcommands: create/list/revoke) | request-response | `internal/platformapi/envkeys/handler.go` (wire contract); `cmd/ach/cmd/migrate.go` (style) | role-match |
| `cmd/ach/cmd/hydrate.go` | W2 | cobra-subcommand | request-response (single POST + stdout JSON) | `cmd/ach/cmd/migrate.go` + `internal/platformapi/hydrate/handler.go` (server contract) | role-match |
| `cmd/ach/cmd/admin.go` | W3 | cobra-subcommand (3 sub-subcommands: keys revoke / users revoke-keys / refresh) | request-response | `internal/platformapi/admin/handler.go` + `internal/platformapi/admin/mount.go` (server contract); `cmd/ach/cmd/migrate.go` (style) | role-match |

### B. Shared CLI internals (internal/cli/) — net-new package

| New File | Wave | Role | Data Flow | Closest Analog | Match |
|---|---|---|---|---|---|
| `internal/cli/config/config.go` | W1 | utility (yaml file I/O + multi-deployment registry) | file-I/O | `internal/cachefs/` (file-system layout helper; stdlib-only) | role-match |
| `internal/cli/config/config_test.go` | W1 | test | file-I/O | `internal/cachefs/` test patterns | role-match |
| `internal/cli/httpclient/client.go` | W1 | utility (HTTP client wrapping `net/http`) | request-response | `internal/litellm/restclient.go` (REST client w/ master-key header) | role-match |
| `internal/cli/httpclient/client_test.go` | W1 | test | request-response | `internal/platformapi/auth/sso_test.go` (httptest mock + table-driven) | role-match |
| `internal/cli/httpclient/redact.go` | W1 | utility (header redaction) | transform | inline redaction pattern in `internal/platformapi/middleware/keyctx.go` (KeyContext discipline) | partial |
| `internal/cli/exit/exit.go` | W1 | utility (typed exit-code constants + mapper) | transform | `internal/audit/events.go` (closed enum of strings; package-level constants) | role-match |
| `internal/cli/exit/exit_test.go` | W1 | test | transform | `internal/audit/events_test.go` style | exact |
| `internal/cli/synthetic/synthetic.go` | W3 | utility (env-var resolution + reject list) | transform | `internal/config/` env helpers (`config.EnvOr`, `config.MustEnvNonEmpty`) | role-match |
| `internal/cli/devicecode/client.go` | W1 | utility (POST /init + poll /token loop) | request-response | `internal/litellm/restclient.go` `makeRequest` shape; new pattern (no exact analog — Dex device-code is novel) | role-match |
| `internal/cli/devicecode/client_test.go` | W1 | test | request-response | `internal/platformapi/auth/sso_test.go` (httptest mock) | role-match |
| `internal/cli/render/render.go` | W2 | utility (text formatters for env list/describe/whoami/config show) | transform | `internal/platformapi/render/json.go` (single-package render owner) | partial (text vs JSON) |
| `internal/cli/render/render_test.go` | W2 | test | transform | `internal/platformapi/render/json_test.go` | exact |
| `internal/cli/doc.go` | W1 | doc (package-level overview) | n/a | `internal/keys/doc.go` (single-file no-import doc); `internal/audit/doc.go` | exact |

### C. Server-side Phase 6 deltas (internal/platformapi/) — modified + new

| File | Wave | Role | Data Flow | Closest Analog | Match |
|---|---|---|---|---|---|
| `internal/platformapi/auth/cli/init.go` (NEW) | W1 | server-endpoint (POST /platform/auth/cli/init) | request-response (anonymous mint of session_id) | `internal/platformapi/auth/sso.go` `LoginHandler` (step 1: gen state, set cookie, redirect) | role-match |
| `internal/platformapi/auth/cli/init_test.go` (NEW) | W1 | test | request-response | `internal/platformapi/auth/sso_test.go` | exact |
| `internal/platformapi/auth/cli/token.go` (NEW) | W1 | server-endpoint (POST /platform/auth/cli/token; 200/202/404/410) | request-response (Redis-backed one-shot retrieval) | `internal/platformapi/envkeys/handler.go` (DisallowUnknownFields + decode + service call + render); `internal/platformapi/auth/sso.go` `CallbackHandler` step 7-8 (DB INSERT compensation pattern) | role-match |
| `internal/platformapi/auth/cli/token_test.go` (NEW) | W1 | test | request-response | `internal/platformapi/envkeys/handler_test.go` | role-match |
| `internal/platformapi/auth/cli/mount.go` (NEW) | W1 | chi-mount | request-response | `internal/platformapi/envkeys/mount.go` | exact |
| `internal/platformapi/auth/cli/session.go` (NEW) | W1 | utility (Redis key shape + TTL helpers for `ach:cli-session:<id>`) | pub-sub-ish | `internal/contentservice/envcache/` (Redis read-through with TTL); `internal/keystore/cached_resolver.go` (Redis key prefix + TTL discipline) | role-match |
| `internal/platformapi/auth/sso.go` (MODIFIED, D-20) | W1 | server-endpoint (existing callback; add optional session_id branch) | request-response | itself (extend with a thin `if sessionID != "" { writeRedisSession } else { renderJSON }` branch at step 8) | self-extend |
| `internal/platformapi/server.go` (MODIFIED) | W1 | chi-route-mount | request-response | itself (lines 122-136 mount the unauth SSO routes; add `r.Route("/platform/auth/cli", auth.MountCLI(authDeps))`) | self-extend |
| `internal/audit/events.go` (MODIFIED) | W1 | enum constant | n/a | itself (lines 49-69 Action* block — add `ActionCliLogin = "platform.cli.login"`) | self-extend |

### D. E2E + demo collapse (Wave 3)

| File | Wave | Role | Data Flow | Closest Analog | Match |
|---|---|---|---|---|---|
| `test/e2e/cli_login_hydrate_test.go` (NEW) | W3 | e2e-test | request-response | `test/e2e/phase3_invariants_test.go` (Phase 3 e2e suite shape — //go:build e2e, t.Run subtests, phase3SuiteGuard) | exact |
| `test/e2e/phase6_helpers_test.go` (NEW, optional) | W3 | test-utility | n/a | `test/e2e/phase3_helpers_test.go` (sibling file pattern in Phase 3) | exact |
| `examples/hydrate-demo.sh` (DELETED) | W3 | shell-script | — | n/a (deletion) | n/a |
| `examples/README.md` (MODIFIED) | W3 | doc | — | itself | self-extend |
| `README.md` + `CLAUDE.md` (MODIFIED) | W3 | doc | — | itself ("Common failure modes" entries) | self-extend |

---

## Pattern Assignments

The excerpts below are what each new file should copy from. Every excerpt cites file + line range so the planner can drop a "Copy from `path.go` lines L1-L2" reference into the plan action.

---

### Pattern P1 — SPDX header + package doc (every `*.go` outside vendor/zz_generated*/mock_*)

**Source:** `cmd/ach/cmd/migrate.go` lines 1-12 (canonical smallest example)

```go
// SPDX-License-Identifier: Apache-2.0

// `ach migrate` applies Postgres schema migrations against ACH_DB_URL
// using the file:// migrations bundled under ACH_MIGRATIONS_PATH
// (default /db/migrations baked into the operator image). Designed to
// run as the Plan 08 init container in the Operator + Content Service
// Pod (D-07). Refuses to start with empty/invalid ACH_DB_URL (D-08
// invariant) — non-zero exit leaves the Pod in Init:Error rather than
// silently skipping. Body lifted from ach-old/cmd/migrate/main.go;
// adapted to a cobra RunE for the single-binary layout.

package cmd
```

**Apply to:** EVERY new `*.go` file in this phase. Hard gate via pre-push (CLAUDE.md §"Publication"). The pre-commit hook will reject the push otherwise.

---

### Pattern P2 — Smallest cobra subcommand template

**Source:** `cmd/ach/cmd/migrate.go` lines 14-61 (entire file)

This is the canonical pattern for every new `cmd/ach/cmd/<verb>.go`:

```go
package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/db"  // <-- swap for internal/cli/* in Phase 6
)

const defaultMigrationsPath = "/db/migrations"

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply Postgres schema migrations from db/migrations against ACH_DB_URL",
	Long: `Apply Postgres schema migrations bundled under ACH_MIGRATIONS_PATH against
the database at ACH_DB_URL. Refuses to start with an empty ACH_DB_URL.

Environment:
  ACH_DB_URL              Postgres DSN (required)
  ACH_MIGRATIONS_PATH     Path to db/migrations directory (default: /db/migrations)
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		url := os.Getenv("ACH_DB_URL")
		if url == "" {
			return fmt.Errorf("ACH_DB_URL is required for migrate")
		}
		// ... single-purpose work ...
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
```

**Apply to:** `login.go`, `logout.go`, `whoami.go`, `hydrate.go` (leaf commands). For parent-with-children commands (`config.go`, `env.go`, `env_keys.go`, `admin.go`) see Pattern P3.

**Key things to copy:**
- File-level comment block above `package cmd` describing what the command does and which CONTEXT.md decisions back it (e.g., for `login.go`: "implements D-02 device-code polling, D-03 §5.1 UX verbatim").
- `var <name>Cmd = &cobra.Command{ ... }` package-level var.
- `Use:`, `Short:`, `Long:` populated. `Long:` is a raw string literal listing flags and env vars.
- `RunE:` returns `error`; cobra prints it and `cmd/ach/main.go` sets exit 1 — but Phase 6 needs a richer exit-code matrix (see Pattern P12).
- `init() { rootCmd.AddCommand(<name>Cmd) }` is the registration point.

---

### Pattern P3 — Parent-with-children cobra subcommand (for `config`, `env`, `env_keys`, `admin`)

**Source:** No exact analog yet in ach; the established cobra idiom is:

```go
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage local CLI configuration (deployments, default, env-keys)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()  // parent prints help when no sub-subcommand given
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured deployments",
	RunE:  runConfigList,
}

var configShowCmd = &cobra.Command{
	Use:   "show [deployment]",
	Short: "Show one deployment's config (pk_/ek_ masked unless --reveal)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runConfigShow,
}

// ... use, remove, rename ...

func init() {
	configCmd.AddCommand(configListCmd, configShowCmd, configUseCmd, configRemoveCmd, configRenameCmd)
	rootCmd.AddCommand(configCmd)
}
```

**Cross-reference:** `rootCmd.RunE` in `cmd/ach/cmd/root.go:29-31` already uses the `return cmd.Help()` idiom for the no-subcommand case — mirror it for each parent verb.

**Apply to:**
- `config.go` — 5 children (list, show, use, remove, rename) per D-05 / spec §5.4.
- `env.go` — 2 children (list, describe) per spec §5.5.
- `env_keys.go` — 3 children (create, list, revoke) per D-07 / spec §5.6.
- `admin.go` — 3 children (`keys revoke`, `users revoke-keys`, `refresh`) per spec §5.10. Note that `admin keys revoke` is a 2-level parent-with-children (admin → keys → revoke); use a nested `adminKeysCmd` intermediary.

---

### Pattern P4 — Config validation + env-var bag (for `login.go`, `whoami.go`, `hydrate.go`, `env*.go`, `admin.go`)

**Source:** `cmd/ach/cmd/platform_api.go` lines 75-144 (`platformAPIConfig` + `validatePlatformAPIConfig`)

```go
// platformAPIConfig holds the validated env-var surface; never mutated
// after validatePlatformAPIConfig returns.
type platformAPIConfig struct {
	BaseURL          string
	DBURL            string
	// ... fields ...
}

func validatePlatformAPIConfig() (*platformAPIConfig, error) {
	cfg := &platformAPIConfig{}
	baseURL, err := config.MustEnvNonEmpty("ACH_BASE_URL")
	if err != nil {
		return nil, fmt.Errorf("ACH_BASE_URL required: %w", err)
	}
	if !strings.HasPrefix(baseURL, "https://") {
		return nil, errors.New("ACH_BASE_URL must be https:// (Hub §9.1 / T-API-04)")
	}
	cfg.BaseURL = baseURL
	// ... more MustEnvNonEmpty / EnvOr / EnvBool / EnvIntNonNeg calls ...
	return cfg, nil
}
```

**Apply to:** Every new CLI subcommand that needs flag + env resolution. The CLI variant resolves from **(--flag → env-var → on-disk config)** in that precedence (D-04, spec §3.1); `internal/cli/config/Resolve()` (new) wraps the precedence rules. The `config.MustEnvNonEmpty` / `config.EnvOr` helpers from `internal/config/` are reusable for env-var defaults.

**Mutex credential check** (D-11) goes here too — return exit 1 (`cli.ExitError`) when >1 of `--api-key`, `--env-key`, `ACH_API_KEY`, `ACH_ENV_KEY` is set.

---

### Pattern P5 — HTTP client with auth header carrier + redaction

**Source:** `internal/litellm/restclient.go` is the closest analog for an outbound HTTP client. Read `internal/litellm/restclient.go` `makeRequest` (Phase 2 plan 02-01 shipped it) for the structure: `http.Client`, `req.Header.Set("Authorization", "Bearer " + masterKey)`, JSON decode, error envelope decode.

**Adapted shape for `internal/cli/httpclient/client.go`:**

```go
package httpclient

// Client is the CLI's outbound HTTP client. It wraps net/http with:
//   - base URL composition (deployments.<active>.url + path)
//   - x-ach-key header carrier (pk_/ek_ depending on resolved cred)
//   - error-envelope decode (§15.5 {error:{code,message},request_id})
//   - --verbose redaction (x-ach-key → "<prefix>_***")
type Client struct {
	BaseURL    string
	APIKey     string  // pk_<...> or ek_<...>
	HTTPClient *http.Client
	Verbose    bool
}

// Do issues req (with x-ach-key header injected) and returns the decoded
// body or a typed *ServerError matching the spec §15.5 envelope.
func (c *Client) Do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	// ... copy makeRequest scaffold from internal/litellm/restclient.go ...
}

// ServerError is the §15.5 error envelope decoded.
type ServerError struct {
	Code      string
	Message   string
	RequestID string
	Status    int  // HTTP status
}
```

**Error-envelope shape to decode** (verbatim from `internal/platformapi/render/json.go:52-62`):

```go
// {
//   "error": { "code": "<code>", "message": "<message>" },
//   "request_id": "<requestID>"
// }
```

**Redaction pattern (header dump for `--verbose`):** `x-ach-key: pk_***` (D-15, spec §6.6). The value is the prefix (`pk_` or `ek_`) followed by literal `***` — never the last-4 (that's only for `ach config show`, see Pattern P10).

---

### Pattern P6 — Exit code matrix as typed constants

**Source:** `internal/audit/events.go` lines 77-115 (canonical example of a closed-enum constant block with file-level doc explaining additivity rules)

```go
package events

// 9 constants — Hub §18.2 action enum verbatim.
const (
	ActionSSOLogin   = "platform.sso.login"
	ActionEkCreate   = "platform.ek.create"
	// ... etc ...
)
```

**Adapted shape for `internal/cli/exit/exit.go`:**

```go
// SPDX-License-Identifier: Apache-2.0

// Package exit defines the closed exit-code set the CLI emits per CLI
// spec §9.3. Phase 6 ships codes 0/1/3/6/8; codes 2/4/5/7 are
// hydrate-engine territory (Phase 7).
package exit

// Code is the typed exit-status wrapper. Each constant maps to one
// §9.3 row.
type Code int

const (
	OK              Code = 0  // success
	General         Code = 1  // general / synth-incompatible / mutex creds
	AuthN           Code = 3  // 401 invalid_key / 403 not_admin / 403 unauthorized_team
	Network         Code = 6  // transport error / 503
	ConfigFile      Code = 8  // ~/.config/ach/config.yaml parse / write error
)

// MapServerError converts a *httpclient.ServerError to a Code per §18.2
// outcome → §9.3 exit-code mapping.
func MapServerError(e *ServerError) Code {
	if e == nil { return OK }
	switch e.Status {
	case 401: return AuthN
	case 403:
		if e.Code == "not_admin" || e.Code == "unauthorized_team" {
			return AuthN
		}
		return General
	case 503: return Network
	}
	return General
}
```

**Apply to:** Every subcommand's `RunE` returns `error` but the CLI binary needs to map the `*ServerError` → exit code. The driver shape is:

```go
// in cmd/ach/main.go (modify the existing main):
if err := cmd.Execute(); err != nil {
    var sErr *httpclient.ServerError
    if errors.As(err, &sErr) {
        os.Exit(int(exit.MapServerError(sErr)))
    }
    os.Exit(int(exit.General))
}
os.Exit(int(exit.OK))
```

---

### Pattern P7 — Server endpoint mount (chi.Router subtree)

**Source:** `internal/platformapi/envkeys/mount.go` lines 1-32 (entire file — the simplest mount example)

```go
// SPDX-License-Identifier: Apache-2.0

package envkeys

import "github.com/go-chi/chi/v5"

// Mount returns the chi.Router subtree constructor for the
// /platform/env-keys endpoint family.
func Mount(deps Deps) func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/", CreateHandler(deps))
		r.Get("/", ListHandler(deps))
		r.Get("/{key_id}", GetHandler(deps))
		r.Delete("/{key_id}", RevokeHandler(deps))
	}
}
```

**Adapted shape for `internal/platformapi/auth/cli/mount.go`:**

```go
package cli

func Mount(deps Deps) func(r chi.Router) {
	return func(r chi.Router) {
		// Both endpoints are unauthenticated (no Authn middleware) —
		// init mints a session_id anonymously; token is gated by the
		// session_id itself (one-shot Redis lookup).
		r.Post("/init", InitHandler(deps))
		r.Post("/token", TokenHandler(deps))
	}
}
```

**Mount point in `internal/platformapi/server.go`:** add OUTSIDE the `Authn`-gated `r.Group(...)` at line 142, alongside the existing `/platform/auth/login` route (line 135). Lines 122-136 are the canonical "unauthenticated carve-out" region. Insert the new mount as:

```go
// Existing (line 135-136):
r.Get("/platform/auth/login", auth.LoginHandler(authDeps))
r.Get("/platform/auth/sso/callback", auth.CallbackHandler(authDeps))

// New (insert after line 136):
r.Route("/platform/auth/cli", authcli.Mount(authcli.Deps{
    Redis:       deps.Redis,
    Audit:       deps.Audit,
    Logger:      deps.Logger,
    Namespace:   deps.Namespace,
    OAuth2Cfg:   deps.OAuth2Cfg,
    SessionTTL:  5 * time.Minute,
    PollInterval: 2 * time.Second,
}))
```

---

### Pattern P8 — Strict JSON decode + render success/error

**Source:** `internal/platformapi/envkeys/handler.go` lines 148-179 (CreateHandler step 2 — strict JSON decode with DisallowUnknownFields)

```go
// Step 2: strict JSON decode with DisallowUnknownFields (D-16).
var req CreateRequest
dec := json.NewDecoder(r.Body)
dec.DisallowUnknownFields()
if err := dec.Decode(&req); err != nil {
    render.Error(w, http.StatusBadRequest, codeInvalidArgument, "invalid request body", reqID)
    return
}
if req.Environment == "" || req.Name == "" {
    render.Error(w, http.StatusBadRequest, codeInvalidArgument, "environment and name required", reqID)
    return
}
```

**Render helpers:** `internal/platformapi/render/json.go:29-62` — copy the `render.JSON(w, status, body)` + `render.Error(w, status, code, message, requestID)` pair. The error envelope shape is hard-coded — never echo upstream errors.

**Apply to:** `internal/platformapi/auth/cli/init.go` (body shape: `{}` accepted, returns `{session_id, verification_url, poll_interval, expires_in}`) and `internal/platformapi/auth/cli/token.go` (body shape: `{session_id}`, returns 200/202/404/410 per D-02).

---

### Pattern P9 — Redis session storage with TTL (init/token state machine)

**Source:** Closest analog is `internal/contentservice/envcache/` (Redis read-through with TTL) — but the simpler example of a Redis namespace + TTL discipline is `internal/keystore/cached_resolver.go` (Phase 3 D-08). Read the cache key prefix pattern there:

```go
// Cache key shape:
cacheKey := "ach:key:" + credentialHash    // from envkeys/handler.go:680
// Equivalent CLI session shape:
cacheKey := "ach:cli-session:" + sessionID  // Phase 6 D-19
```

**Shape for `internal/platformapi/auth/cli/session.go`:**

```go
package cli

import (
	"context"
	"encoding/json"
	"time"
	"github.com/redis/go-redis/v9"
)

const (
	sessionKeyPrefix = "ach:cli-session:"
	defaultSessionTTL = 5 * time.Minute  // D-02 (Claude's Discretion: planner picks)
)

// Session is the JSON-serialized payload stored at sessionKeyPrefix+id.
// Populated by sso.CallbackHandler when called with a session_id query
// param; consumed (one-shot) by TokenHandler.
type Session struct {
	KeyID      string `json:"key_id"`       // pkid_...
	Plaintext  string `json:"plaintext"`    // pk_... — single-use; deleted on first read
	OwnerEmail string `json:"owner_email"`
	CreatedAt  string `json:"created_at"`   // RFC3339
}

// Put stores a Session at sessionKeyPrefix+id with TTL.
func Put(ctx context.Context, rdb *redis.Client, id string, s Session, ttl time.Duration) error {
	b, err := json.Marshal(s)
	if err != nil { return err }
	return rdb.Set(ctx, sessionKeyPrefix+id, b, ttl).Err()
}

// GetAndDelete atomically reads + deletes the session at sessionKeyPrefix+id.
// Returns (nil, redis.Nil) when absent (TTL bust or first-read-after-fetch).
// Uses GETDEL for atomicity (Redis 6.2+).
func GetAndDelete(ctx context.Context, rdb *redis.Client, id string) (*Session, error) {
	val, err := rdb.GetDel(ctx, sessionKeyPrefix+id).Result()
	if err != nil { return nil, err }
	var s Session
	if err := json.Unmarshal([]byte(val), &s); err != nil { return nil, err }
	return &s, nil
}
```

**Audit emission for the new `cli_login` action:** add `ActionCliLogin = "platform.cli.login"` to `internal/audit/events.go` after line 56 (`ActionSSOLogin` block). Use the existing `audit.EmitAudit` shape (`internal/audit/emit.go:96-119`) verbatim — the only delta is the Action constant.

---

### Pattern P10 — On-disk yaml config with file-mode discipline

**Source:** `internal/cachefs/` (Phase 1 — stdlib-only file-system helper). Read `internal/cachefs/cachefs.go` for: stdlib-only `os` + `path/filepath`, no `log`, no `slog`, file-mode invariants (`0700`/`0600`), idempotent `os.MkdirAll`.

**Adapted shape for `internal/cli/config/config.go`:**

```go
// SPDX-License-Identifier: Apache-2.0

// Package config owns ~/.config/ach/config.yaml — the CLI's local trust
// artifact (Hub §15.4) authorized to hold pk_/ek_ plaintext on disk.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"gopkg.in/yaml.v3"
)

// File is the §3.2 schema verbatim:
//   default: <name>
//   deployments:
//     <name>:
//       url: https://...
//       pk:  pk_...                 # optional
//       ek:
//         <local-label>: ek_...     # optional convenience map
type File struct {
	Default     string                  `yaml:"default,omitempty"`
	Deployments map[string]*Deployment  `yaml:"deployments,omitempty"`
}

type Deployment struct {
	URL string            `yaml:"url"`
	PK  string            `yaml:"pk,omitempty"`
	EK  map[string]string `yaml:"ek,omitempty"`
}

// Path resolves $XDG_CONFIG_HOME/ach/config.yaml or ~/.config/ach/config.yaml.
func Path() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ach", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ach", "config.yaml"), nil
}

// Load reads + parses the config file. Returns (nil, nil) when the file
// is absent (fresh install / synthetic mode). Refuses non-HTTPS URLs on
// load (D-04). Warns to stderr when the file mode is > 0600.
func Load(path string) (*File, error) { /* ... */ }

// Save writes the file with mode 0600, parent dir 0700. Refuses non-
// HTTPS URLs on write (D-04). Atomic via tmp+rename in the same dir.
func Save(path string, f *File) error { /* ... */ }

// Mask returns the masked form of a pk_/ek_ — "<prefix>_****<last-4>"
// — used by `ach config show` (D-05). `--reveal` skips this transform
// for a named deployment only.
func Mask(s string) string {
	if len(s) < 8 { return "<masked>" }
	idx := strings.Index(s, "_")
	if idx < 0 { return "<masked>" }
	return s[:idx+1] + "****" + s[len(s)-4:]
}
```

**Stdlib-only discipline applies:** the only third-party dep is `gopkg.in/yaml.v3` (which is already in `go.mod` if Phase 6 lands it; alternatively `sigs.k8s.io/yaml` is already an indirect dep). Planner picks. NO `log`, NO `slog`, NO `fmt.Print*` from this package (mirror `internal/keys/doc.go` discipline). All caller-side stderr output happens in the cobra `RunE`.

---

### Pattern P11 — E2E test shape (//go:build e2e + stdlib testing.T)

**Source:** `test/e2e/phase3_invariants_test.go` lines 1-60 (file header + TestMain pattern; lines 53-60 show the top-level test composing subtests)

```go
//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 6 CLI e2e suite. Drives `ach login` + `ach hydrate --environment demo`
// against the kept kind cluster (per CLAUDE.md dev loop — `make cluster-keep`),
// then byte-for-byte diffs vs examples/hydrate.json (D-17, D-18).
//
// Activation: ./scripts/dev.sh make e2e-focus FOCUS=TestPhase6CLI

package e2e

import (
	"os/exec"
	"testing"
)

func TestPhase6CLI(t *testing.T) {
	t.Run("login_device_code", testPhase6Login)
	t.Run("hydrate_golden_diff", testPhase6HydrateGoldenDiff)
	t.Run("env_list", testPhase6EnvList)
	t.Run("env_keys_create", testPhase6EnvKeysCreate)
	t.Run("whoami_verify", testPhase6WhoamiVerify)
}

func testPhase6Login(t *testing.T) {
	t.Helper()
	phase6SuiteGuard(t)  // skip on missing-deps mirror of phase3SuiteGuard
	// ... shell out to `ach login` with test-only --token bypass OR env-var
	// injected pk_ (D-18 Claude's Discretion — planner picks) ...
}

func testPhase6HydrateGoldenDiff(t *testing.T) {
	t.Helper()
	phase6SuiteGuard(t)
	out, err := exec.CommandContext(t.Context(), "./bin/ach", "hydrate",
		"--environment", "demo").Output()
	if err != nil { t.Fatalf("ach hydrate: %v", err) }
	golden, err := os.ReadFile("../../examples/hydrate.json")
	if err != nil { t.Fatalf("read golden: %v", err) }
	if !bytes.Equal(out, golden) {
		t.Errorf("hydrate output != golden:\nwant=%s\ngot=%s", golden, out)
	}
}
```

**Key things to copy from Phase 3 e2e:**
- `//go:build e2e` first line, then SPDX header, then file-level docstring.
- `phase3SuiteGuard(t)` skip-when-deps-missing pattern (see `test/e2e/phase3_helpers_test.go` for the implementation — mirror it as `phase6SuiteGuard`).
- Single `TestPhase6CLI(t)` umbrella with `t.Run` subtests per CONTEXT.md D-18 (login, hydrate, env list, env-keys create, whoami --verify).
- Use `exec.CommandContext` (stdlib) to shell out to the compiled `./bin/ach` binary — the binary is built by `make build` in the devtools container.

---

### Pattern P12 — Cobra RunE returning typed error → main.go maps to exit code

**Source:** `cmd/ach/main.go` lines 1-15 (the existing entrypoint) + Pattern P6 above.

**Current shape:**
```go
func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

**Modified shape (Phase 6 W1):**
```go
func main() {
	if err := cmd.Execute(); err != nil {
		var sErr *httpclient.ServerError
		if errors.As(err, &sErr) {
			fmt.Fprintln(os.Stderr, sErr.Error())
			os.Exit(int(exit.MapServerError(sErr)))
		}
		var cErr *exit.CodedError  // CLI-side typed error (mutex creds, etc.)
		if errors.As(err, &cErr) {
			fmt.Fprintln(os.Stderr, cErr.Error())
			os.Exit(int(cErr.Code))
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(int(exit.General))
	}
}
```

**Apply to:** `cmd/ach/main.go` (modified, W1). All subcommand `RunE` functions return `error`; the mapping happens once at main entry.

---

## Shared Patterns (cross-cutting)

### Pattern S1 — SPDX header on every Go file

Every `*.go` outside `vendor/`, `zz_generated*.go`, `mock_*.go` MUST start with:

```go
// SPDX-License-Identifier: Apache-2.0
```

Verified by `scripts/pre-push-check.sh` — gate 9 of the 17-gate pre-push (CLAUDE.md §"Publication"). The hook rejects any push with a missing header.

### Pattern S2 — Single-binary cobra layout

Per CLAUDE.md §"Repository-specific patterns" and Phase 1 D-03: every new subcommand goes under `cmd/ach/cmd/<verb>.go`. NEVER create a second `cmd/<x>/main.go` tree. The phase-6 8 new subcommands slot in 1:1 alongside the existing 5 (`operator`, `platform-api`, `forwarder`, `content-service`, `migrate`).

### Pattern S3 — TDD discipline (test file first)

Per CLAUDE.md §"Repository-specific patterns" (Phase 3/4/5 precedent): every `<file>.go` lands with `<file>_test.go` first. The unit-test pattern across the codebase is **stdlib `testing` only** (no Ginkgo/Gomega in unit tests; Ginkgo is reserved for e2e in `test/e2e/` and even there the Phase 3+ files use stdlib per Plan 01-11). Use `net/http/httptest` for hermetic HTTP tests — `internal/platformapi/auth/sso_test.go` is the closest large-test analog showing httptest + table-driven structure.

### Pattern S4 — Devtools container for all builds

Every `go build`/`go test`/`make` invocation is prefixed `./scripts/dev.sh` (CLAUDE.md §"Toolchain — host has NO Go"). The host has no Go binary. Examples:

```bash
./scripts/dev.sh go build ./...
./scripts/dev.sh go test ./internal/cli/...
./scripts/dev.sh make e2e-focus FOCUS=TestPhase6CLI
./scripts/dev.sh make lint-changed
```

`make pre-commit` and `make pre-push` are host-only (they spawn gitleaks/trufflehog containers). The installed git hooks (`make hooks`) fire on every commit/push — do NOT call `make pre-push` manually.

### Pattern S5 — Audit-safety contract (NO plaintext in logs/audit)

Per `internal/keys/doc.go:19-32` + Hub §16.1: pk_/ek_ plaintext MUST NOT appear anywhere except the one-time response body of the originating endpoint. For Phase 6 this means:
- `ach login` prints the pk_ exactly once (already-resolved on the CLI side; no re-emission).
- `ach env-keys create` prints the ek_ exactly once.
- `ach config show` masks pk_/ek_ to `<prefix>_****<last-4>` unless `--reveal` (D-05).
- `--verbose` HTTP header dump redacts `x-ach-key` to `<prefix>_***` (D-15).
- New `platform.cli.login` audit event MUST NOT carry the pk_ plaintext — only the resolved `key_id` (pkid_…) and `owner_email`. Mirror `audit.Event` shape from `internal/audit/emit.go:44-52`.

### Pattern S6 — Error envelope (§15.5) is the wire contract

Every Phase 6 CLI HTTP call decodes the response body as:

```json
{ "error": { "code": "<code>", "message": "<message>" }, "request_id": "req_..." }
```

Source: `internal/platformapi/render/json.go:52-62`. The CLI httpclient surfaces `error.code` as the exit-code anchor per Pattern P6. The `request_id` is echoed in any `--verbose` stderr line for cross-correlation with server-side audit lines (Pattern S5 keeps it grep-able).

---

## No Analog Found

| File | Reason | Fallback |
|------|--------|----------|
| (none) | Every Phase 6 file has a close codebase analog. The Dex device-code dance is novel as a *protocol* but the underlying primitives (HTTP client + Redis session + cobra RunE + chi mount) are all directly modeled by existing files. | n/a |

---

## Modification Hotspots

The following existing files need surgical edits — the planner should reference these line ranges when assigning Phase 6 work:

| File | Lines | Edit | Plan owner |
|------|-------|------|------------|
| `internal/platformapi/server.go` | 136 (after) | Insert `r.Route("/platform/auth/cli", authcli.Mount(...))` in the unauth carve-out region | W1 plan that ships the device-code endpoints |
| `internal/platformapi/auth/sso.go` | 213-475 (CallbackHandler) | Branch at step 8: if `r.URL.Query().Get("session_id") != ""`, write `Session{KeyID, Plaintext, OwnerEmail, CreatedAt}` to Redis via `cli.Put(ctx, deps.Redis, sessionID, sess, ttl)` AND render a friendly browser-side HTML "you may close this window" page (D-20); else preserve the existing `render.JSON(w, http.StatusOK, callbackResponse{...})` at line 470-474 | W1 plan that ships device-code (D-20) |
| `internal/audit/events.go` | 49-69 (Action* block) | Add `ActionCliLogin = "platform.cli.login"` per §18.5 additive extension policy (file-level comment at lines 31-38 documents the rule) | W1 plan that ships device-code |
| `internal/platformapi/auth/sso.go` Deps struct | 38-90 | Add `Redis *redis.Client` field threading; existing struct does NOT have Redis because Phase 3 SSO callback didn't need it. Phase 6 D-20 introduces the dependency | W1 plan that ships device-code |
| `internal/platformapi/server.go` Deps struct | 33-86 | No new fields needed — `Redis *redis.Client` and `Audit *slog.Logger` already present. Pass through to `authDeps` (line 124-134) by adding `Redis: deps.Redis` | W1 plan that ships device-code |
| `cmd/ach/main.go` | 10-14 (whole main func) | Replace `if err := cmd.Execute(); err != nil { os.Exit(1) }` with the typed-error mapping shown in Pattern P12 | W1 plan that ships internal/cli/exit |
| `examples/hydrate-demo.sh` | (whole file) | DELETE in W3 (D-17) | W3 plan |
| `examples/README.md` | references to hydrate-demo.sh | Replace with `ach login` + `ach hydrate --environment demo > hydrate.json` workflow | W3 plan |
| `README.md` (root) | references to hydrate-demo.sh / "Common failure modes" | Same as examples/README.md | W3 plan |
| `CLAUDE.md` (root) | "Common failure modes" sections referencing hydrate-demo.sh | Update to reference `ach login` + `ach hydrate` (CLAUDE.md §"Documentation hygiene" — same commit as the code change) | W3 plan |

---

## Wave-by-wave plan-shape suggestions

The planner has D-01 latitude on plan count; the breakdown below maps naturally to the 3-wave / ~9-plan target.

**Wave 1 — Foundation + Dex (3 plans recommended):**
- **W1-P1: `internal/cli/` foundation** — `config/`, `httpclient/`, `exit/`, `synthetic/`, `doc.go` + tests. Stdlib-heavy. Patterns P1, P4, P5, P6, P10, S1, S3.
- **W1-P2: Server-side device-code endpoints** — `internal/platformapi/auth/cli/{init,token,session,mount}.go` + tests, modify `sso.go` (D-20 branch), modify `server.go` (mount), modify `events.go` (`ActionCliLogin`). Patterns P7, P8, P9, S5.
- **W1-P3: `ach login` + `ach whoami` + `ach logout` + `internal/cli/devicecode/`** — three subcommands tied together by device-code flow. Patterns P2, P3 (login is leaf; logout is leaf; whoami is leaf), P12.

**Wave 2 — Core surface (3 plans recommended):**
- **W2-P1: `ach config` (5 sub-subcommands) + `ach env` (list, describe)** — config-mutate + read-only surface. Patterns P3, P10.
- **W2-P2: `ach env-keys` (create, list, revoke) + D-07 deviation** — locks the spec deviation in REQUIREMENTS.md changelog. Patterns P3, P5.
- **W2-P3: `ach hydrate`** — single POST + stdout JSON. Patterns P2, P4, P5.

**Wave 3 — Edges (3 plans recommended):**
- **W3-P1: `internal/cli/synthetic/` enforcement** — cross-cutting gate active across all subcommands. Pattern P4 (synthetic-mode rejection logic).
- **W3-P2: `ach admin` (3 sub-subcommands)** — admin surface. Patterns P3, P6 (exit-3 on 403 not_admin).
- **W3-P3: E2E + demo collapse** — `test/e2e/cli_login_hydrate_test.go` + delete `examples/hydrate-demo.sh` + README/CLAUDE.md updates. Pattern P11.

Total: **9 plans** (matches D-01 target).

---

## Metadata

- **Analog search scope:** `cmd/ach/cmd/`, `internal/platformapi/`, `internal/keys/`, `internal/audit/`, `internal/cachefs/`, `internal/litellm/`, `internal/keystore/`, `internal/contentservice/`, `test/e2e/`, `examples/`.
- **Files scanned:** 22 source files read in full; 8 spot-checked via grep.
- **Pattern extraction date:** 2026-05-28.
- **Upstream context:**
  - `.planning/phases/06-cli-foundation/06-CONTEXT.md` (253 lines — 20 decisions D-01..D-20).
  - `.planning/STATE.md` (8/10 phases complete, executing Phase 6 plan-phase step).
  - `.planning/ROADMAP.md` §"Phase 6: CLI Foundation" lines 270-284 (13 REQs, 5 SCs).
  - `CLAUDE.md` (project root) §"Repository-specific patterns", §"Toolchain", §"Publication", §"Documentation hygiene".
