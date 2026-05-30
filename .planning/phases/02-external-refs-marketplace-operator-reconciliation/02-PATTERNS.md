# Phase 2: External Refs + Marketplace + Operator Reconciliation — Pattern Map

**Mapped:** 2026-05-15
**Files analyzed:** 24 (new + modified)
**Analogs found:** 22 / 24 (2 net-new — orphan Runnable + slog audit handler are infrastructure rooted in stdlib + memory feedback patterns)
**Sister project (canonical lift source):** `/home/jcm/Projects/ach_litellm/`
**Repo state:** Phase 1 complete — `internal/litellm/{client.go,noop.go}` stubs exist; controller Reconcile() bodies are finalizer-only; `cachefs.EnsureLayout` and `db.Open` are wired in `cmd/operator/main.go`.

This pattern map drives Phase 2 planning. The dominant lift is `../ach_litellm/internal/litellm/*.go` (~2,446 lines including tests) into `ach/internal/litellm/` with `LITELLM_OPERATOR_*` → `ACH_LITELLM_*` env-var rename (D-01, D-02). Six new source-type fetchers under `internal/sources/<type>/` follow a uniform `Fetcher` interface shape (D-04, D-05). Two new `manager.Runnable`s (LiteLLM snapshot + orphan cleanup) mirror the sister project's `connection.Cache` Runnable. The five external-ref reconcilers' Reconcile() bodies expand from Phase 1's finalizer-only shape to the full §10 fetch/materialize/rename/UPDATE loop (D-04..D-12).

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `internal/litellm/client.go` (MODIFY: extend `Client` interface; preserve Phase 1 signature) | service (REST client iface) | request-response | `../ach_litellm/internal/litellm/client.go` | exact (sister is the lift target; Phase 2 widens iface with `ListModels`/`ListMCPServers`/`ListA2AAgents`/`ListUserKeys`/`RevokeKey`) |
| `internal/litellm/transport.go` (NEW) | service (HTTP transport) | request-response | `../ach_litellm/internal/litellm/transport.go` | exact (verbatim lift + env-var rename) |
| `internal/litellm/team.go` (NEW) | service (REST endpoint) | request-response | `../ach_litellm/internal/litellm/team.go` | exact |
| `internal/litellm/model.go` (NEW) | service (REST endpoint) | request-response | `../ach_litellm/internal/litellm/model.go` | exact (gains `ListModels` helper Phase 2 needs) |
| `internal/litellm/mcp.go` (NEW) | service (REST endpoint) | request-response | `../ach_litellm/internal/litellm/mcp.go` | exact (already has `ListMCPServers`) |
| `internal/litellm/agents.go` (NEW) | service (REST endpoint) | request-response | `../ach_litellm/internal/litellm/agents.go` | exact (already has `ListAgents`; rename callsite → `ListA2AAgents`) |
| `internal/litellm/keyinfo.go` (NEW) | service (REST endpoint) | request-response | `../ach_litellm/internal/litellm/keyinfo.go` | role-match (sister exposes `ProbeConnection`; Phase 2 adds `ListUserKeys`/`RevokeKey` here per the file's "key/auth concerns" charter) |
| `internal/litellm/errors.go` (NEW) | utility (error types) | transform | `../ach_litellm/internal/litellm/errors.go` | exact |
| `internal/litellm/types.go` (NEW) | model (wire types) | n/a | `../ach_litellm/internal/litellm/types.go` | exact (gains UserKey / ListUserKeysResponse types Phase 2 needs) |
| `internal/litellm/doc.go` (MODIFY: rename env-var docs `LITELLM_OPERATOR_*` → `ACH_LITELLM_*`) | utility (pkg docs) | n/a | `../ach_litellm/internal/litellm/doc.go` | exact (modify env-var names + delete spike-specific paragraph) |
| `internal/litellm/noop.go` (MODIFY: add no-op impls for the 5 new iface methods) | service (test double) | request-response | (current) `ach/internal/litellm/noop.go` | exact (extend existing) |
| `internal/sources/github/fetcher.go` (NEW) | service (upstream fetcher) | streaming | `../ach_litellm/internal/litellm/client.go` (`makeRequest` shape) + RESEARCH.md §10.1 | role-match (HTTP request → `io.ReadCloser` + metadata; auth from Secret) |
| `internal/sources/gitlab/fetcher.go` (NEW) | service (upstream fetcher) | streaming | same as github | role-match |
| `internal/sources/bitbucket/fetcher.go` (NEW) | service (upstream fetcher) | streaming | same as github | role-match |
| `internal/sources/s3/fetcher.go` (NEW) | service (upstream fetcher) | streaming | same as github (SDK-only; no shared transport) | role-match |
| `internal/sources/gcs/fetcher.go` (NEW) | service (upstream fetcher) | streaming | same as github (SDK-only) | role-match |
| `internal/sources/http/fetcher.go` (NEW) | service (upstream fetcher) | streaming | `../ach_litellm/internal/litellm/client.go` (`makeRequest` + `drainAndClose`) | role-match (closest sister analog because both are stdlib `net/http`) |
| `internal/snapshot/litellm_snapshot.go` (NEW) | runnable (cache refresh) | event-driven (timer) | `../ach_litellm/internal/connection/cache.go` + `snapshot.go` | role-match (atomic.Pointer snapshot, `manager.Runnable` lifecycle, lock-free read) |
| `internal/orphan/runnable.go` (NEW) | runnable (background loop) | batch | `../ach_litellm/internal/connection/cache.go` (Runnable `Start(ctx)` shape) | role-match (ctx-driven Start; ticker loop; LiteLLM-unreachable abort) |
| `internal/audit/handler.go` (NEW) | utility (slog handler) | transform | NONE (stdlib `log/slog`) | rationale: net-new; relies on stdlib `slog.HandlerOptions` + a top-level `audit=true` attribute per D-17. Memory pattern [[feedback_litellm_operator_no_redaction_filter]] confirms discipline-over-scrubbing — handler emits raw, callers compose audit-safe events. |
| `internal/controller/ach/plugin_controller.go` (MODIFY: expand steady-state Reconcile) | controller | event-driven | (current) `ach/internal/controller/ach/plugin_controller.go` + `../ach_litellm/internal/controller/litellmconnection_controller.go` | exact (extend existing structure; add fetch/materialize/rename/DB-UPDATE block) |
| `internal/controller/ach/prompt_controller.go` (MODIFY) | controller | event-driven | (current) `ach/internal/controller/ach/prompt_controller.go` | exact (same shape as plugin; "raw bytes, no archive extension") |
| `internal/controller/ach/artifact_controller.go` (MODIFY) | controller | event-driven | (current) `ach/internal/controller/ach/artifact_controller.go` | exact (object vs directory scope; both cache paths) |
| `internal/controller/ach/pluginmarketplace_controller.go` (MODIFY: three-stage refresh) | controller | event-driven | (current) `ach/internal/controller/ach/pluginmarketplace_controller.go` | role-match (Phase 1 is finalizer-only; Phase 2 adds Stage-1 fetch + Stage-2 serial per-plugin + Stage-3 DELETE sweep) |
| `internal/controller/ach/environment_controller.go` (MODIFY: ExecutionResourcesResolved + RequeueAfter=5m) | controller | event-driven | (current) `ach/internal/controller/ach/environment_controller.go` | exact (extend steady-state branch only) |
| `cmd/operator/main.go` (MODIFY) | controller-manager entrypoint | event-driven | (current) `ach/cmd/operator/main.go` | exact (swap `NoopClient` ctor → `NewClient`; `mgr.Add(snapshot)`; `mgr.Add(orphan)`; cache-reset-on-empty PVC) |
| `internal/db/db.go` (MODIFY: add helper funcs) | utility (pgxpool) | CRUD | `../ach_litellm/internal/litellm/client.go` (parameterized SQL via `pgxpool`) | role-match (sister has no DB; pattern is "package owns Open(), callers own SQL" — Phase 2 adds `UpsertExternalRef`, `DeleteVanishedMarketplacePlugins`, `ResetExternalRefRefreshOnEmptyCache`, `ListACHManagedLitellmUsers`) |
| `config/rbac/operator_role.yaml` (NO-OP / verified) | config (RBAC) | n/a | (current) — already grants `secrets get/list/watch` | exact (Phase 1 forward-compat rule at lines 44-46 already satisfies D-11; no edit needed) |
| `internal/cachefs/bootstrap.go` (MODIFY: add `SweepTmp(root, age)` helper) | utility (filesystem) | file-I/O | (current) `ach/internal/cachefs/bootstrap.go` | exact (extend existing; `os.ReadDir(root+"/.tmp")` + `os.Remove` of entries older than 1h per Hub §10.3) |

**Net-new (no analog):**
- `internal/audit/handler.go` — slog `Handler` with `audit=true` predicate; pure stdlib.

**No-op (verified):**
- `config/rbac/operator_role.yaml` — D-11 already satisfied by Phase 1's forward-compat secrets rule (lines 44-46 of the current file).

---

## Pattern Assignments

### `internal/litellm/transport.go` (service, request-response)

**Analog:** `../ach_litellm/internal/litellm/transport.go` (verbatim lift; rename env-var)

**Env-var rename** (line 26 of sister):
```go
// SISTER:
const EnvDangerouslyLogBodies = "LITELLM_OPERATOR_DANGEROUSLY_LOG_BODIES"
// ACH (Phase 2):
const EnvDangerouslyLogBodies = "ACH_LITELLM_DANGEROUSLY_LOG_BODIES"
```

**Core redacting RoundTripper pattern** (lines 36-98 of sister — copy verbatim):
```go
type redactingRoundTripper struct {
    base      http.RoundTripper
    log       logr.Logger
    logBodies bool
}

func (r *redactingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    start := time.Now()
    // ... body-capture logic only when logBodies==true ...
    resp, err := r.base.RoundTrip(req)
    latency := time.Since(start)
    if err != nil {
        r.log.Info("litellm request",
            "method", req.Method, "path", req.URL.Path,
            "status", "error", "latency_ms", latency.Milliseconds(),
        )
        return resp, err
    }
    fields := []any{"method", req.Method, "path", req.URL.Path,
        "status", resp.StatusCode, "latency_ms", latency.Milliseconds()}
    // ... only on logBodies: append request_body / response_body ...
    r.log.Info("litellm request", fields...)
    return resp, nil
}
```

**HTTP-client factory** (lines 104-113 of sister):
```go
func newHTTPClient(log logr.Logger) *http.Client {
    return &http.Client{
        Transport: &redactingRoundTripper{
            base:      http.DefaultTransport,
            log:       log,
            logBodies: os.Getenv(EnvDangerouslyLogBodies) == "true",
        },
        Timeout: 30 * time.Second,
    }
}
```

**REL-04 drain helper** (lines 124-130 of sister — copy verbatim):
```go
func drainAndClose(body io.ReadCloser) {
    if body == nil { return }
    _, _ = io.Copy(io.Discard, body)
    _ = body.Close()
}
```

**Redaction discipline** (D-03; aligns with memory `feedback_litellm_operator_no_redaction_filter`): default log payload is exactly `{method, path, status, latency_ms}` — never headers, never bodies, never credentials.

---

### `internal/litellm/client.go` (service, request-response) — MODIFY

**Analog for the lifted struct:** `../ach_litellm/internal/litellm/client.go` lines 53-179 (verbatim).

**Analog for the preserved interface:** current `ach/internal/litellm/client.go` lines 53-61 (Phase 1 declared the `Client` interface).

**Env-var rename** (sister line 46):
```go
// SISTER:
const EnvAuthHeader = "LITELLM_OPERATOR_AUTH_HEADER"
// ACH (Phase 2):
const EnvAuthHeader = "ACH_LITELLM_AUTH_HEADER"
```

**The Phase 2 file fuses two structures.** The current Phase 1 file declares `type Client interface` only. The Phase 2 file must:

1. **Keep the `Client` interface** (rename existing to `interface` — already typed) and **widen** it:
```go
type Client interface {
    // Phase 1 (preserved verbatim):
    DeleteAccessGroup(ctx context.Context, name string) error
    DeleteTag(ctx context.Context, name string) error
    // Phase 2 (new — D-01, D-13, D-16):
    ListModels(ctx context.Context) ([]ModelInfoResponse, error)
    ListMCPServers(ctx context.Context) ([]MCPServerEntry, error)
    ListA2AAgents(ctx context.Context) ([]AgentEntry, error)
    ListUserKeys(ctx context.Context, userID string) ([]UserKeyInfo, error)
    RevokeKey(ctx context.Context, keyID string) error
}
```

2. **Add the concrete `*Client` struct** (sister lines 56-62) but rename to avoid the interface name collision. Sister project also uses `Client` for the struct — Phase 2 must rename to `RESTClient` (struct) so the file holds `type Client interface` and `type RESTClient struct` side by side, with `var _ Client = (*RESTClient)(nil)` at the bottom.

3. **Lift `NewClient`** (sister lines 68-85) and rename to `NewRESTClient(endpoint, masterKey string, log logr.Logger) *RESTClient`. The auth-header switch reads `os.Getenv(EnvAuthHeader)`.

4. **Lift `setAuth`** (sister lines 89-96) and `makeRequest` (sister lines 110-168) verbatim onto `*RESTClient`.

**Naming note for the planner:** the sister project uses `Client` for both the iface (n/a — no iface in sister) and the struct. ACH already has `Client` as the iface (Phase 1); the struct must be `RESTClient` (and the constructor `NewRESTClient`). Reconcilers stay typed against the `Client` iface — only `cmd/operator/main.go` constructs `*RESTClient`.

---

### `internal/litellm/types.go` (model, n/a) — NEW

**Analog:** `../ach_litellm/internal/litellm/types.go` (verbatim).

**Phase 2 additions** (no sister equivalent — derive from LiteLLM 1.83.10 `/key/info` endpoint shape):
```go
// UserKeyInfo is one row of GET /key/info?user_id=<id>. Used by
// orphan-cleanup (D-16) to list all keys owned by an ACH-managed
// LiteLLM user, then cross-reference against active personal_keys /
// environment_keys rows.
type UserKeyInfo struct {
    KeyID      string    `json:"key_id"`
    UserID     string    `json:"user_id"`
    CreatedAt  time.Time `json:"created_at"`
    KeyAlias   string    `json:"key_alias,omitempty"`
}

type ListUserKeysResponse struct {
    Keys []UserKeyInfo `json:"keys"`
}
```

Everything else (`LiteLLMParams`, `ModelInfo`, `Deployment`, `MCPServerEntry`, `AgentEntry`, etc.) is verbatim from sister.

---

### `internal/litellm/errors.go` (utility, transform) — NEW

**Analog:** `../ach_litellm/internal/litellm/errors.go` (verbatim).

**Key excerpt — typed 401 + envelope parser** (sister lines 25-46, 82-108):
```go
var ErrNotFound = errors.New("litellm: not found")

type Auth401Error struct {
    Path string
    Body []byte
}
func (e *Auth401Error) Error() string {
    return fmt.Sprintf("litellm: 401 unauthorized on %s", e.Path)
}

type litellmErrorEnvelope struct {
    Error struct {
        Message string          `json:"message"`
        Type    string          `json:"type"`
        Param   json.RawMessage `json:"param"`
        Code    string          `json:"code"`
    } `json:"error"`
}
```

**Phase 2 fetcher reuse:** D-04 / D-05 note that the same `Auth401Error` is reused by source-type fetchers for upstream 401 classification (`reason=Unauthorized`).

---

### `internal/litellm/model.go` / `mcp.go` / `agents.go` (service, request-response) — NEW

**Analogs:**
- `../ach_litellm/internal/litellm/model.go` — verbatim; Phase 2 adds `ListModels(ctx)` mirroring `ListMCPServers`/`ListAgents` shape (sister Phase 1 had no list endpoint for models because the sister project does not need cross-environment model snapshots).
- `../ach_litellm/internal/litellm/mcp.go` — verbatim; already has `ListMCPServers` (sister lines 61-76).
- `../ach_litellm/internal/litellm/agents.go` — verbatim; rename `ListAgents` → `ListA2AAgents` in the comment/exported wrapper (D-13 talks about A2A agents, but the LiteLLM endpoint name is `/v1/agents` — keep the endpoint and just expose the wrapper under the ACH name).

**REL-05 length-check pattern to mirror in new `ListModels`** (sister `mcp.go` lines 61-76):
```go
func (c *Client) ListMCPServers(ctx context.Context) ([]MCPServerEntry, error) {
    raw, err := c.makeRequest(ctx, "GET", "/v1/mcp/server", nil)
    if err != nil {
        return nil, err
    }
    var arr []MCPServerEntry
    if err := json.Unmarshal(raw, &arr); err != nil {
        return nil, fmt.Errorf("litellm: decode GET /v1/mcp/server: %w", err)
    }
    list := MCPServerListResponse{Data: arr}
    if len(list.Data) == 0 { return nil, ErrNotFound } // REL-05
    return list.Data, nil
}
```

**Phase 2 distinction:** the LiteLLM-snapshot Runnable (D-13) must treat `ErrNotFound` as **empty-set, not error** (an Environment that lists a model in `spec.runtime.models` against a LiteLLM with zero models is the empty intersection — that is a real `unresolvedRuntime` result, not a transient). The snapshot Runnable wraps `errors.Is(err, ErrNotFound)` → empty slice.

---

### `internal/litellm/keyinfo.go` (service, request-response) — NEW

**Analog:** `../ach_litellm/internal/litellm/keyinfo.go` (verbatim) + Phase 2 additions.

**Sister `ProbeConnection` pattern** (sister lines 29-32) is lifted verbatim. Phase 2 adds two new methods on the file because `keyinfo.go` already owns "key/auth concerns":

```go
// ListUserKeys: GET /key/info?user_id=<id>. Used by orphan-cleanup
// (D-16) to enumerate all LiteLLM keys owned by an ACH-managed user.
func (c *Client) ListUserKeys(ctx context.Context, userID string) ([]UserKeyInfo, error) {
    path := "/key/info?user_id=" + url.QueryEscape(userID)
    raw, err := c.makeRequest(ctx, "GET", path, nil)
    if err != nil { return nil, err }
    var resp ListUserKeysResponse
    if err := json.Unmarshal(raw, &resp); err != nil {
        return nil, fmt.Errorf("litellm: decode GET /key/info: %w", err)
    }
    return resp.Keys, nil
}

// RevokeKey: POST /key/delete with body {keys: [keyID]}. Used by
// orphan-cleanup (D-16) per Hub §18.4.
func (c *Client) RevokeKey(ctx context.Context, keyID string) error {
    _, err := c.makeRequest(ctx, "POST", "/key/delete", map[string]any{"keys": []string{keyID}})
    return err
}
```

---

### `internal/litellm/noop.go` (service, test double) — MODIFY

**Analog:** current `ach/internal/litellm/noop.go` (extend existing).

**Phase 2 extension:** the existing file has `DeleteAccessGroup` and `DeleteTag`. Phase 2 adds five log-only no-ops:
```go
func (c *NoopClient) ListModels(_ context.Context) ([]ModelInfoResponse, error) {
    c.Log.Info("stub: would list LiteLLM models")
    return nil, nil
}
// (same shape for ListMCPServers, ListA2AAgents, ListUserKeys, RevokeKey)
```

The Phase 1 compile-time assertion `var _ Client = (*NoopClient)(nil)` (line 67 of current noop.go) ensures the build breaks if Phase 2 forgets a method.

---

### `internal/sources/<type>/fetcher.go` (service, streaming) — NEW × 6

**Uniform interface** (D-04 / D-05 — no sister analog; define from RESEARCH §10.1):
```go
// Fetcher is the source-type-agnostic upstream-fetch contract every
// source/<type>/fetcher.go implements. The reconciler is responsible
// for .tmp/<random> lifecycle, fsync, atomic rename(2), and DB
// UPDATE — fetchers stay storage-agnostic (D-05).
type Fetcher interface {
    Fetch(ctx context.Context, source SourceConfig, secret *corev1.Secret) (*FetchResult, error)
}

type FetchResult struct {
    Body         io.ReadCloser           // caller closes
    UpstreamRev  string                  // commit SHA / ETag / generation / Last-Modified
    NotModified  bool                    // conditional-GET hit (HTTP 304)
}
```

**Closest analog for HTTP fetcher** (sister `client.go` lines 110-168 — `makeRequest`):
```go
// Phase 2 http fetcher mirrors:
//  - http.NewRequestWithContext
//  - drainAndClose via defer
//  - REL-04 contract (drain before close on every code path)
//  - 1 MB cap is REPLACED by ACH_PLUGIN_MAX_SIZE_MIB enforcement
//    (D-12: io.LimitReader(body, max+1); delete staging on overshoot)
```

**Auth-Secret-read pattern** (D-11 — informer cache, no controller-runtime Get):
```go
// In the reconciler (NOT in fetcher.go):
var secret corev1.Secret
if err := r.Get(ctx, types.NamespacedName{
    Namespace: r.Namespace, Name: spec.AuthSecretRef.Name,
}, &secret); err != nil {
    // SecretNotFound → SourceReachable=False, reason=Unauthorized
    return ctrl.Result{}, fmt.Errorf("auth secret: %w", err)
}
// Hand the *corev1.Secret to fetcher.Fetch; the fetcher extracts the
// per-source-type key (PAT for github/gitlab/bitbucket, access-key-id
// + secret-access-key for s3, SA JSON for gcs, header value for http).
```

**Plugin-size-cap pattern** (D-12 — applied in the reconciler around `io.Copy`, not in the fetcher):
```go
const max = int64(pluginMaxSizeMiB) << 20
limited := io.LimitReader(result.Body, max+1)
n, err := io.Copy(stagingFile, limited)
if n > max {
    _ = os.Remove(stagingFile.Name())
    return ctrl.Result{}, &OversizeError{Bytes: n, Cap: max}
    // Reconciler maps to SourceReachable=False, reason=PluginTooLarge.
}
```

**SDK choice (per D-04, lifted into go.mod):**
- `github.com/google/go-github/v62` (github tarball + metadata)
- `github.com/xanzy/go-gitlab` (gitlab metadata) + `github.com/go-git/go-git/v5` (tarball materialization)
- `github.com/ktrysmt/go-bitbucket` (bitbucket metadata)
- `github.com/aws/aws-sdk-go-v2/service/s3` (s3 + endpoint override)
- `cloud.google.com/go/storage` (gcs)
- stdlib `net/http` for the http source — REUSE `drainAndClose` from `internal/litellm/transport.go` (or copy the pattern into `internal/sources/http/`)

---

### `internal/snapshot/litellm_snapshot.go` (runnable, event-driven) — NEW

**Analog:** `../ach_litellm/internal/connection/cache.go` + `../ach_litellm/internal/connection/snapshot.go` (the `manager.Runnable` + `atomic.Pointer[Snapshot]` pattern is the closest existing match).

**Snapshot value type** (mirror sister `snapshot.go` lines 52-106):
```go
// LiteLLMSnapshot is the immutable refresh result. Loads on the
// hot path are atomic.Pointer.Load + dereference — lock-free.
type LiteLLMSnapshot struct {
    Models     map[string]struct{} // model_name set
    MCPServers map[string]struct{} // server_name set
    A2AAgents  map[string]struct{} // agent_name set
    RefreshedAt time.Time
    Stale       bool                // true if last refresh failed; prior snapshot preserved (D-14)
}
```

**Atomic-pointer cache + Runnable** (mirror sister `cache.go` lines 44-86, 99-104, 228-233):
```go
type Snapshotter struct {
    client  litellm.Client
    log     logr.Logger
    snap    atomic.Pointer[LiteLLMSnapshot]
    interval time.Duration
}

func NewSnapshotter(c litellm.Client, log logr.Logger) *Snapshotter {
    return &Snapshotter{
        client: c, log: log,
        interval: 5 * time.Minute, // Hub §6.4
    }
}

func (s *Snapshotter) Snapshot() LiteLLMSnapshot {
    if p := s.snap.Load(); p != nil { return *p }
    return LiteLLMSnapshot{}
}

func (s *Snapshotter) Start(ctx context.Context) error {
    t := time.NewTicker(s.interval)
    defer t.Stop()
    s.refresh(ctx) // initial population
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-t.C:
            s.refresh(ctx)
        }
    }
}
```

**Refresh with stale-preservation on LiteLLM-unreachable** (D-14 — preserve prior snapshot, increment `litellm_unreachable_total`):
```go
func (s *Snapshotter) refresh(ctx context.Context) {
    models, errM := s.client.ListModels(ctx)
    mcps, errC   := s.client.ListMCPServers(ctx)
    agents, errA := s.client.ListA2AAgents(ctx)
    if errM != nil || errC != nil || errA != nil {
        s.log.Info("litellm snapshot: upstream unreachable, preserving prior snapshot",
            "modelsErr", errM, "mcpErr", errC, "agentsErr", errA)
        if cur := s.snap.Load(); cur != nil {
            stale := *cur
            stale.Stale = true
            s.snap.Store(&stale)
        }
        // metrics.LitellmUnreachableTotal.WithLabelValues("operator").Inc()
        return
    }
    s.snap.Store(&LiteLLMSnapshot{
        Models:     toSet(models),
        MCPServers: toSet(mcps),
        A2AAgents:  toSet(agents),
        RefreshedAt: time.Now(),
    })
}
```

**Manager registration** (mirror sister `cache.go` lines 213-233 + how `cmd/main.go` calls `mgr.Add(cache)`):
```go
// In cmd/operator/main.go:
snapshotter := snapshot.NewSnapshotter(realLiteLLM, ctrl.Log.WithName("litellm-snapshot"))
if err := mgr.Add(snapshotter); err != nil { os.Exit(1) }
```

**Lock-free read on Environment reconcile** (mirror sister `cache.go` lines 99-104):
```go
// In EnvironmentReconciler.Reconcile:
snap := r.Snapshotter.Snapshot()
unresolved := []string{}
for _, m := range env.Spec.Runtime.Models {
    if _, ok := snap.Models[m]; !ok { unresolved = append(unresolved, "model:"+m) }
}
// (same for MCPServers, A2AAgents) → env.Status.UnresolvedRuntime
```

---

### `internal/orphan/runnable.go` (runnable, batch) — NEW

**Analog:** `../ach_litellm/internal/connection/cache.go` lines 228-233 (the `Start(ctx) error` shape). The orphan loop is a fresh design but the manager.Runnable lifecycle is identical.

**Skeleton** (D-15, D-16):
```go
type Runnable struct {
    client   litellm.Client
    db       *pgxpool.Pool
    auditLog *slog.Logger        // the D-17 audit handler
    interval time.Duration       // ACH_ORPHAN_CLEANUP_INTERVAL, ≥5m
    log      logr.Logger
}

func NewRunnable(c litellm.Client, db *pgxpool.Pool, audit *slog.Logger,
    interval time.Duration, log logr.Logger) *Runnable {
    return &Runnable{client: c, db: db, auditLog: audit,
        interval: interval, log: log}
}

func (r *Runnable) Start(ctx context.Context) error {
    t := time.NewTicker(r.interval)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return nil
        case <-t.C:        r.tick(ctx)
        }
    }
}

func (r *Runnable) tick(ctx context.Context) {
    // 1. List ACH-managed litellm_user_id set from DB.
    userIDs, err := db.ListACHManagedLitellmUsers(ctx, r.db)
    if err != nil { r.log.Error(err, "orphan: DB list users"); return }
    // 2. For each user, ListUserKeys, filter orphans (Hub §18.4):
    //    - key not in active personal_keys / environment_keys
    //    - key ≥ 10min old
    //    - owning user is ACH-managed (already true by enumeration)
    for _, uid := range userIDs {
        keys, err := r.client.ListUserKeys(ctx, uid)
        if err != nil {
            r.log.Info("orphan: litellm unreachable; aborting tick", "err", err)
            return // §18.4: abort cleanly on unreachable
        }
        for _, k := range keys {
            if isOrphan(ctx, r.db, k) {
                if err := r.client.RevokeKey(ctx, k.KeyID); err != nil {
                    r.emitAudit(k.KeyID, "litellm_unreachable")
                    return // abort tick on first unreachable
                }
                r.emitAudit(k.KeyID, "success")
            }
        }
    }
}
```

**Interval validation in `cmd/operator/main.go`** (D-15 — refuse to start on below-minimum):
```go
interval := config.EnvOr("ACH_ORPHAN_CLEANUP_INTERVAL", "1h")
parsed, err := time.ParseDuration(interval)
if err != nil || parsed < 5*time.Minute {
    setupLog.Error(err, "fatal: ACH_ORPHAN_CLEANUP_INTERVAL must be ≥5m (OP-15 / Hub §18.4)",
        "value", interval)
    os.Exit(1)
}
```

---

### `internal/audit/handler.go` (utility, transform) — NEW

**Analog:** NONE (stdlib `log/slog`). Memory pattern `feedback_litellm_operator_no_redaction_filter` ([[reference]] in user memory) drives the design: handler ships records raw; callers compose audit-safe events (no plaintext keys, no body content).

**Skeleton** (D-17, D-18):
```go
// Package audit is the operator's dedicated audit log surface.
// Audit events flow through a stdout-JSON slog.Handler that adds
// a distinguishing top-level attribute {"audit": true}. Kubernetes
// log collection (fluent-bit / Loki) picks them up alongside ops
// logs; downstream filtering by audit=true separates audit from
// ops without a second destination (D-17).
package audit

import (
    "io"
    "log/slog"
)

// NewLogger returns a *slog.Logger with a JSON handler writing to w
// (typically os.Stdout). Every emitted record carries audit=true at
// the top level so log shippers can split via that predicate.
func NewLogger(w io.Writer) *slog.Logger {
    h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})
    return slog.New(h).With(slog.Bool("audit", true))
}
```

**Phase 2 caller** (D-18 — orphan cleanup is the only emitter in Phase 2):
```go
auditLog.Info("operator.orphan-cleanup",
    "target.kind", "litellm_key",
    "target.name", keyID,
    "outcome", "success",          // or "litellm_unreachable"
    "request_id", reqID,           // optional in Phase 2
)
```

**Phase 3 will reuse this exact logger** for pk_/ek_ lifecycle events — that's the point of landing the shape now (specifics §"Orphan-cleanup audit logger is the first slog handler with `audit:true`").

---

### `internal/controller/ach/plugin_controller.go` (controller, event-driven) — MODIFY

**Analog for the existing shell:** current `ach/internal/controller/ach/plugin_controller.go` (lines 70-118 — fetch/delete-path/finalizer-add/steady-state shape).

**Analog for the Phase 2 expansion:** `../ach_litellm/internal/litellm/client.go` lines 110-168 (`makeRequest` — the closest sister pattern for "HTTP fetch + structured error handling + drain-on-defer + write to DB").

**Phase 1 steady-state stub** (current file lines 108-118):
```go
// Steady state — minimal SourceReachable=Unknown stub (CRD-07).
// Phase 2's real upstream fetch flips this to True/False+reason.
if err := r.writeStatus(
    ctx, &cr,
    "SourceReachable", metav1.ConditionUnknown, "Initializing",
    "Phase 1 stub: Phase 2 wires the real upstream probe",
); err != nil {
    logger.Error(err, "status update failed", "type", "SourceReachable")
}
return ctrl.Result{}, nil
```

**Phase 2 steady-state expansion** (replace lines 108-118 of current file):
```go
// 0. Resolve fetcher for spec.source.type.
fetcher, err := sources.For(cr.Spec.Source)
if err != nil {
    return r.markCondition(ctx, &cr, "SourceReachable", false,
        "InvalidConfig", err.Error())
}

// 1. Read auth secret from informer cache.
var secret corev1.Secret
if err := r.Get(ctx, types.NamespacedName{
    Namespace: r.Namespace, Name: cr.Spec.Source.AuthSecretRef().Name,
}, &secret); err != nil {
    return r.markCondition(ctx, &cr, "SourceReachable", false,
        "Unauthorized", "auth secret missing")
}

// 2. Conditional staleness check — skip refresh if interval hasn't elapsed.
//    (read external_refs.last_successful_refresh from DB)

// 3. Fetch upstream.
result, err := fetcher.Fetch(ctx, cr.Spec.Source, &secret)
if err != nil {
    return r.markCondition(ctx, &cr, "SourceReachable", false,
        classifyFetchErr(err), err.Error())
}
defer result.Body.Close()
if result.NotModified {
    // 304 — keep prior cached file; just rearm RequeueAfter.
    return ctrl.Result{RequeueAfter: cr.Spec.Refresh.Interval.Duration}, nil
}

// 4. Stage in .tmp/<random> — D-12 size-cap enforcement.
staging, err := os.CreateTemp(filepath.Join(r.CacheRoot, ".tmp"), "stg-")
if err != nil { return ctrl.Result{}, err }
defer staging.Close()

max := int64(r.PluginMaxSizeMiB) << 20
limited := io.LimitReader(result.Body, max+1)
n, err := io.Copy(staging, limited)
if err != nil {
    _ = os.Remove(staging.Name())
    return ctrl.Result{}, err
}
if n > max {
    _ = os.Remove(staging.Name())
    return r.markCondition(ctx, &cr, "SourceReachable", false,
        "PluginTooLarge", fmt.Sprintf("size %d exceeds cap %d", n, max))
}

// 5. fsync + atomic rename(2) — the §10.3 load-bearing barrier.
if err := staging.Sync(); err != nil {
    _ = os.Remove(staging.Name())
    return ctrl.Result{}, err
}
final := filepath.Join(r.CacheRoot, "plugin", cr.Name+".tar.gz")
if err := os.Rename(staging.Name(), final); err != nil {
    _ = os.Remove(staging.Name())
    return ctrl.Result{}, fmt.Errorf("§10.3 rename: %w", err)
}

// 6. DB UPDATE: external_refs row + force-refresh annotation removal.
if err := db.UpsertExternalRef(ctx, r.DB, db.ExternalRef{
    Kind: "plugin", Name: cr.Name,
    StorageLocation: final,
    LastSuccessfulRefresh: time.Now(),
    MaxStalenessSeconds: int64(cr.Spec.Refresh.MaxStaleness.Duration.Seconds()),
}); err != nil {
    return ctrl.Result{}, err
}

// 7. Annotation removal in same Update as status (D-07).
delete(cr.Annotations, "ach.ackstorm.ai/force-refresh")
if err := r.Update(ctx, &cr); err != nil { return ctrl.Result{}, err }

// 8. Status condition + RequeueAfter (D-06).
_ = r.markCondition(ctx, &cr, "SourceReachable", true, "Synced", "")
return ctrl.Result{RequeueAfter: cr.Spec.Refresh.Interval.Duration}, nil
```

**Reconciler struct extension** (add fields beyond Phase 1's `client.Client / Scheme / Namespace / Log / CacheRoot`):
```go
type PluginReconciler struct {
    client.Client
    Scheme           *runtime.Scheme
    Namespace        string
    Log              logr.Logger
    CacheRoot        string
    DB               *pgxpool.Pool          // Phase 2: needed for external_refs UPSERT
    PluginMaxSizeMiB int                    // Phase 2: D-12 cap enforcement
    Fetchers         sources.Registry       // Phase 2: source-type → Fetcher resolver
}
```

---

### `internal/controller/ach/prompt_controller.go` / `artifact_controller.go` (controller, event-driven) — MODIFY

**Analog:** Plugin controller above. Differences:

| Concern | Plugin | Prompt | Artifact |
|---------|--------|--------|----------|
| Cache path | `plugin/<name>.tar.gz` | `prompt/<name>` (raw bytes) | `artifact/<name>` OR `artifact/<name>.tar.gz` per `spec.scope` |
| Size cap | `ACH_PLUGIN_MAX_SIZE_MIB` enforcement | none | none |
| `external_refs.kind` | `"plugin"` | `"prompt"` | `"artifact"` |

Prompt/Artifact controllers share the rest of the Plugin steady-state body verbatim (fetch → stage → fsync → rename → DB-UPDATE → annotation removal → RequeueAfter).

---

### `internal/controller/ach/pluginmarketplace_controller.go` (controller, event-driven) — MODIFY

**Analog (existing shell):** current `ach/internal/controller/ach/pluginmarketplace_controller.go` (Phase 1 — finalizer-only).

**Phase 2 three-stage refresh** (D-09, D-10; Hub §12.4):

**Stage 1 — fetch + parse marketplace.json:**
```go
// Fetch upstream marketplace.json (uniform Fetcher contract).
// On Stage-1 failure: SourceReachable=False or Synced=False with
// reason in {Unreachable, Unauthorized, NotFound, UpstreamInvalid,
// InvalidConfig}. NO marketplace_plugins rows are touched.
result, err := fetcher.Fetch(ctx, cr.Spec.Source, &secret)
if err != nil { return r.markSynced(ctx, &cr, false, classifyFetchErr(err), err.Error()) }
defer result.Body.Close()
raw, _ := io.ReadAll(io.LimitReader(result.Body, 5<<20)) // marketplace.json hard cap 5 MiB
var mkt ClaudeCodePluginMarketplace
if err := json.Unmarshal(raw, &mkt); err != nil {
    return r.markSynced(ctx, &cr, false, "UpstreamInvalid", err.Error())
}
```

**Stage 1.5 — apply RE2 include/exclude filters** (D-07 from CONTEXT: anchored `^`, compile-failure → InvalidConfig, zero-match include → UpstreamInvalid, zero-match exclude → silent no-op):
```go
includeRe, err := regexp.Compile("^" + cr.Spec.Include)
if err != nil { return r.markSynced(ctx, &cr, false, "InvalidConfig", "include: " + err.Error()) }
// (same for excludeRe)
filtered := applyFilters(mkt.Plugins, includeRe, excludeRe)
if cr.Spec.Include != "" && len(filtered.includeMatches) == 0 {
    return r.markSynced(ctx, &cr, false, "UpstreamInvalid",
        "include pattern matched zero plugins")
}
```

**Stage 1.6 — cross-marketplace name-conflict resolution** (D-08 from CONTEXT — alphabetical priority; loser sets `Synced=False, reason=NameConflict`):
```go
var otherMkts achv1alpha1.PluginMarketplaceList
_ = r.List(ctx, &otherMkts, client.InNamespace(r.Namespace))
losers := computeNameConflictLosers(cr.Name, filtered, otherMkts)
if len(losers) > 0 {
    // Reject this marketplace's losing names; if all names lose, mark Synced=False.
}
```

**Stage 2 — serial per-plugin materialization** (D-09 — one at a time; first 5 failures verbatim in `status.message`):
```go
var failures []pluginFailure
for _, plugin := range filtered.Plugins {
    if err := r.materializeOnePlugin(ctx, &cr, plugin); err != nil {
        failures = append(failures, pluginFailure{Name: plugin.Name, Reason: err.Error()})
        // Continue — single-plugin failure does NOT abort stage-2 (D-10).
    }
}
```

**Status-message format** (D-10 — load-bearing observability surface; truncate at first 5):
```go
msg := ""
if len(failures) > 0 {
    parts := []string{}
    for i, f := range failures {
        if i == 5 { parts = append(parts, fmt.Sprintf("+%d more", len(failures)-5)); break }
        parts = append(parts, fmt.Sprintf("%s: %s", f.Name, f.Reason))
    }
    msg = fmt.Sprintf("stage-2: %d plugin(s) failed: %s", len(failures), strings.Join(parts, ", "))
}
// Synced=True is preserved even with stage-2 partial failures (D-10);
// only Stage-1 failure flips Synced=False.
_ = r.markSynced(ctx, &cr, true, "Synced", msg)
```

**Stage 3 — DELETE sweep of vanished names** (D-09 from CONTEXT, Hub §12.4):
```go
upstreamNames := setOf(filtered.Plugins)
existingRows, _ := db.ListMarketplacePlugins(ctx, r.DB, cr.Name)
for _, row := range existingRows {
    if _, kept := upstreamNames[row.Name]; !kept {
        if err := os.Remove(row.StorageLocation); err != nil && !errors.Is(err, fs.ErrNotExist) {
            r.Log.Error(err, "stage-3 cache delete", "row", row.Name)
        }
        _ = db.DeleteMarketplacePlugin(ctx, r.DB, cr.Name, row.Name)
    }
}
```

---

### `internal/controller/ach/environment_controller.go` (controller, event-driven) — MODIFY

**Analog (existing shell):** current `ach/internal/controller/ach/environment_controller.go` (lines 100-166 — Reconcile envelope is unchanged).

**Phase 2 extension** — replace the steady-state block at lines 151-165 with:
```go
// Steady state: ExecutionResourcesResolved derivation per Hub §6.4.
snap := r.Snapshotter.Snapshot()
unresolved := []string{}
for _, m := range env.Spec.Runtime.Models {
    if _, ok := snap.Models[m]; !ok {
        unresolved = append(unresolved, "model:"+m)
    }
}
for _, mcp := range env.Spec.Runtime.MCPServers {
    if _, ok := snap.MCPServers[mcp]; !ok {
        unresolved = append(unresolved, "mcp:"+mcp)
    }
}
for _, agent := range env.Spec.Runtime.A2AAgents {
    if _, ok := snap.A2AAgents[agent]; !ok {
        unresolved = append(unresolved, "agent:"+agent)
    }
}
env.Status.UnresolvedRuntime = unresolved
status := metav1.ConditionTrue
reason := "AllResolved"
msg := ""
if len(unresolved) > 0 {
    status = metav1.ConditionFalse
    reason = "UnresolvedRuntime"
    msg = fmt.Sprintf("%d unresolved: %s", len(unresolved), strings.Join(unresolved, ", "))
}
if err := r.writeStatus(ctx, &env, "ExecutionResourcesResolved",
    status, reason, msg); err != nil {
    logger.Error(err, "status update failed", "type", "ExecutionResourcesResolved")
}

// D-08: 5-minute requeue per Hub §6.4.
return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
```

**Reconciler struct extension** — add `Snapshotter *snapshot.Snapshotter` field. The `LiteLLM litellm.Client` field stays — Phase 2 still uses it for `DeleteAccessGroup` / `DeleteTag` during §6.5 deletion (now against the real REST client).

---

### `cmd/operator/main.go` (controller-manager entrypoint) — MODIFY

**Analog (existing shell):** current `ach/cmd/operator/main.go`.

**Phase 2 modifications** (additive — preserve everything Phase 1 already wired):

**1. Swap NoopClient ctor for real RESTClient** (current lines 201-205):
```go
// PHASE 1 (replace these lines):
noopLiteLLM := litellm.NewNoopClient(ctrl.Log.WithName("litellm").WithName("noop"))

// PHASE 2:
liteLLMBaseURL, err := config.MustEnvNonEmpty("ACH_LITELLM_BASE_URL")
if err != nil { setupLog.Error(err, "fatal: ACH_LITELLM_BASE_URL required (D-02)"); os.Exit(1) }
liteLLMMasterKey, err := config.MustEnvNonEmpty("ACH_LITELLM_MASTER_KEY")
if err != nil { setupLog.Error(err, "fatal: ACH_LITELLM_MASTER_KEY required (D-02)"); os.Exit(1) }
realLiteLLM := litellm.NewRESTClient(liteLLMBaseURL, liteLLMMasterKey,
    ctrl.Log.WithName("litellm"))
```

**2. Validate ACH_ORPHAN_CLEANUP_INTERVAL** (D-15 — refuse below 5m):
```go
intervalStr := config.EnvOr("ACH_ORPHAN_CLEANUP_INTERVAL", "1h")
orphanInterval, err := time.ParseDuration(intervalStr)
if err != nil || orphanInterval < 5*time.Minute {
    setupLog.Error(err, "fatal: ACH_ORPHAN_CLEANUP_INTERVAL must be ≥5m (D-15 / Hub §18.4)")
    os.Exit(1)
}
```

**3. Audit-logger init** (D-17):
```go
auditLog := audit.NewLogger(os.Stdout)
```

**4. Snapshotter + Orphan Runnable registration** (after manager construction; before reconciler `SetupWithManager` calls):
```go
snapshotter := snapshot.NewSnapshotter(realLiteLLM,
    ctrl.Log.WithName("litellm-snapshot"))
if err := mgr.Add(snapshotter); err != nil {
    setupLog.Error(err, "unable to add LiteLLM snapshot Runnable"); os.Exit(1)
}

orphanRunnable := orphan.NewRunnable(realLiteLLM, dbPool, auditLog,
    orphanInterval, ctrl.Log.WithName("orphan-cleanup"))
if err := mgr.Add(orphanRunnable); err != nil {
    setupLog.Error(err, "unable to add orphan-cleanup Runnable"); os.Exit(1)
}
```

**5. Secret informer cache warmup** (D-11 — controller-runtime auto-installs the informer when Reconcile calls `r.Get(&corev1.Secret{})`; the explicit warm-up is for fast first-reconcile):
```go
// Pre-warm the corev1.Secret informer so the first Plugin reconcile
// doesn't pay a one-second cache-fill latency.
if _, err := mgr.GetCache().GetInformer(context.Background(), &corev1.Secret{}); err != nil {
    setupLog.Error(err, "unable to install Secret informer"); os.Exit(1)
}
```

**6. Cache-reset-on-empty-PVC** (D from CONTEXT "Cache reconstruction on PVC loss"):
```go
// OP-11: if cache root is empty, NULL out last_successful_refresh
// so every reconciler reissues the upstream fetch.
empty, _ := cachefs.IsEmpty(cacheRoot) // helper to be added to cachefs
if empty {
    if err := db.ResetExternalRefRefreshOnEmptyCache(context.Background(), dbPool); err != nil {
        setupLog.Error(err, "unable to reset external_refs.last_successful_refresh on empty cache")
        os.Exit(1)
    }
    setupLog.Info("PVC was empty on startup — external_refs.last_successful_refresh reset")
}
```

**7. Inject new reconciler fields** (Plugin / Prompt / Artifact / PluginMarketplace need `DB`, `PluginMaxSizeMiB`, `Fetchers`; Environment needs `Snapshotter`):
```go
if err = (&achcontroller.PluginReconciler{
    Client:           mgr.GetClient(),
    Scheme:           mgr.GetScheme(),
    Namespace:        watchNS,
    Log:              ctrl.Log.WithName("controller").WithName("Plugin"),
    CacheRoot:        cacheRoot,
    DB:               dbPool,                  // NEW
    PluginMaxSizeMiB: pluginMaxSizeMiB,        // NEW
    Fetchers:         fetcherRegistry,         // NEW
}).SetupWithManager(mgr); err != nil { ... }
// (same Snapshotter injection for EnvironmentReconciler)
```

**8. Replace `_ = noopLiteLLM`** — the existing Environment reconciler line 309 (`LiteLLM: noopLiteLLM`) becomes `LiteLLM: realLiteLLM`.

---

### `internal/db/db.go` (utility, CRUD) — MODIFY

**Analog (existing shell):** current `ach/internal/db/db.go` (Phase 1 — `Open` + `Migrate` only).

**Phase 2 additions** — append to the package; do not touch `Open` / `Migrate`:

```go
// ExternalRef mirrors the external_refs table row (Hub §16).
type ExternalRef struct {
    Kind                   string
    Name                   string
    StorageLocation        string
    LastSuccessfulRefresh  time.Time
    NextRefreshAt          time.Time
    MaxStalenessSeconds    int64
}

// UpsertExternalRef inserts-or-updates by (kind, name). Called from
// Plugin/Prompt/Artifact reconcilers after a successful rename(2).
// Idempotent — re-reconciles republish the same path.
func UpsertExternalRef(ctx context.Context, db *pgxpool.Pool, r ExternalRef) error {
    _, err := db.Exec(ctx, `
        INSERT INTO external_refs (kind, name, storage_location,
            last_successful_refresh, next_refresh_at, max_staleness_seconds)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (kind, name) DO UPDATE SET
            storage_location          = EXCLUDED.storage_location,
            last_successful_refresh   = EXCLUDED.last_successful_refresh,
            next_refresh_at           = EXCLUDED.next_refresh_at,
            max_staleness_seconds     = EXCLUDED.max_staleness_seconds
    `, r.Kind, r.Name, r.StorageLocation, r.LastSuccessfulRefresh,
       r.NextRefreshAt, r.MaxStalenessSeconds)
    return err
}

// UpsertMarketplacePlugin: same shape against marketplace_plugins
// (primary key marketplace_name, name).
func UpsertMarketplacePlugin(...) error { ... }

// ListMarketplacePlugins returns all rows for a given marketplace_name.
// Stage-3 DELETE sweep enumerates this set and removes vanished rows.
func ListMarketplacePlugins(ctx context.Context, db *pgxpool.Pool,
    marketplaceName string) ([]MarketplacePlugin, error) { ... }

// DeleteMarketplacePlugin: row-level DELETE for stage-3 sweep.
func DeleteMarketplacePlugin(...) error { ... }

// ResetExternalRefRefreshOnEmptyCache: OP-11 cache-loss recovery —
// NULL out last_successful_refresh so every reconciler reissues fetch.
func ResetExternalRefRefreshOnEmptyCache(ctx context.Context,
    db *pgxpool.Pool) error {
    _, err := db.Exec(ctx,
        `UPDATE external_refs SET last_successful_refresh = NULL`)
    return err
}

// ListACHManagedLitellmUsers returns the union of
// personal_keys.litellm_user_id and environment_keys.litellm_user_id
// for active rows. Used by orphan cleanup (D-16).
func ListACHManagedLitellmUsers(ctx context.Context,
    db *pgxpool.Pool) ([]string, error) {
    rows, err := db.Query(ctx, `
        SELECT DISTINCT litellm_user_id FROM (
            SELECT litellm_user_id FROM personal_keys WHERE status='active'
            UNION
            SELECT litellm_user_id FROM environment_keys WHERE status='active'
        ) AS u
    `)
    // ...
}
```

**Parameterized-query discipline** (Phase 1 carry-forward — see `environment_controller.go` lines 205-217 for the established `pgxpool.Pool.Exec` / `QueryRow.Scan` pattern). No string concatenation; all values bind via `$N`.

---

### `internal/cachefs/bootstrap.go` (utility, file-I/O) — MODIFY

**Analog (existing shell):** current `ach/internal/cachefs/bootstrap.go`.

**Phase 2 additions** — append two helpers; do not touch `EnsureLayout`:

```go
// IsEmpty returns true if root exists and contains no entries other
// than the .tmp/ staging directory. Called from cmd/operator/main.go
// to drive OP-11 (cache-loss recovery via external_refs reset).
func IsEmpty(root string) (bool, error) {
    entries, err := os.ReadDir(root)
    if err != nil { return false, err }
    for _, e := range entries {
        if e.Name() == ".tmp" { continue }
        // Any non-.tmp entry containing files means the cache is populated.
        sub, err := os.ReadDir(filepath.Join(root, e.Name()))
        if err == nil && len(sub) > 0 { return false, nil }
    }
    return true, nil
}

// SweepTmp removes .tmp/<entry> files older than maxAge. Phase 2
// reconcilers create .tmp/ entries via os.CreateTemp; a crash between
// CreateTemp and rename(2) leaves orphans. The sweep runs hourly from
// a manager.Runnable.
func SweepTmp(root string, maxAge time.Duration) error {
    tmp := filepath.Join(root, ".tmp")
    entries, err := os.ReadDir(tmp)
    if err != nil { return err }
    cutoff := time.Now().Add(-maxAge)
    for _, e := range entries {
        info, err := e.Info()
        if err != nil { continue }
        if info.ModTime().Before(cutoff) {
            _ = os.Remove(filepath.Join(tmp, e.Name()))
        }
    }
    return nil
}
```

---

### `config/rbac/operator_role.yaml` (config, RBAC) — NO-OP

**Verified:** the current file at lines 44-46 already grants:
```yaml
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
```

Phase 1 landed this rule as "reserved forward-compat" specifically for Phase 2's Secret-reads-via-informer-cache. D-11 is already satisfied; Phase 2 must NOT touch the rule.

The planner SHOULD include a verification step that `kubectl auth can-i get secrets --as=system:serviceaccount:ach-system:ach-operator` returns `yes`.

---

## Shared Patterns

### Auth-secret read (informer-cache)

**Source:** Phase 1 reconcilers already use `r.Get(ctx, types.NamespacedName{...}, &cr)` for CR fetches; Phase 2 extends to `corev1.Secret` via the same controller-runtime cached `client.Client`.

**Apply to:** every reconciler doing an upstream fetch (Plugin, Prompt, Artifact, PluginMarketplace).

**Code excerpt:**
```go
var secret corev1.Secret
if err := r.Get(ctx, types.NamespacedName{
    Namespace: r.Namespace, Name: spec.AuthSecretRef.Name,
}, &secret); err != nil {
    if apierrors.IsNotFound(err) {
        return r.markCondition(ctx, &cr, "SourceReachable", false,
            "Unauthorized", "auth secret not found")
    }
    return ctrl.Result{}, err
}
```

### Error classification (fetcher → SourceReachable.reason)

**Source:** `../ach_litellm/internal/litellm/errors.go` lines 64-75 (`classify(status)`); sister maps HTTP status → `KindAuth401 / KindTransient / KindPermanent`.

**Apply to:** every fetcher's error returns + every reconciler's status writes.

**Mapping table** (Hub §6.6 / §10 / §11 enum):
| Fetcher condition | reason |
|---|---|
| HTTP 401 / SDK auth error | `Unauthorized` |
| HTTP 404 / object key missing | `NotFound` |
| HTTP 5xx / TCP reset / context deadline | `Unreachable` |
| HTTP 200 but unparsable JSON | `UpstreamInvalid` |
| Regex compile failure | `InvalidConfig` |
| io.Copy(staging) > max cap | `PluginTooLarge` |
| now − last_successful_refresh > max_staleness | `StaleCacheExpired` (Content Service, Phase 5) |

### REL-04 drain-and-close discipline

**Source:** `../ach_litellm/internal/litellm/transport.go` lines 124-130 (`drainAndClose`).

**Apply to:** every HTTP fetcher returning a body. Reconcilers that consume `result.Body` must `defer drainAndClose(result.Body)` (or in their case, simply `defer result.Body.Close()` since `io.Copy` already drains).

### Atomic-publish via rename(2)

**Source:** Hub §10.3 (no sister analog — sister has no filesystem cache). Phase 2 pattern is canonical from the spec.

**Apply to:** every reconciler with cached content (Plugin, Prompt, Artifact, PluginMarketplace stage-2).

**Code excerpt:** see the Plugin pattern above (steps 4-5: `os.CreateTemp(.tmp/) → io.Copy → Sync → Rename`).

### Manager-Runnable lifecycle

**Source:** `../ach_litellm/internal/connection/cache.go` lines 228-233 (`Start(ctx) error`).

**Apply to:** `internal/snapshot/litellm_snapshot.go` (Snapshotter) and `internal/orphan/runnable.go` (orphan loop).

**Code excerpt:**
```go
func (X *X) Start(ctx context.Context) error {
    t := time.NewTicker(X.interval)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return nil
        case <-t.C:        X.tick(ctx)
        }
    }
}
```

### Env-var validation at startup (fail-fast)

**Source:** current `ach/cmd/operator/main.go` lines 137-173 (`MustEnvNonEmpty` + `MustEnvIntPositive`).

**Apply to:** every new Phase 2 env var (`ACH_LITELLM_BASE_URL`, `ACH_LITELLM_MASTER_KEY`, `ACH_ORPHAN_CLEANUP_INTERVAL`). All required values use `MustEnvNonEmpty` and `os.Exit(1)` on failure with a `setupLog.Error` line.

### Compile-time interface assertion

**Source:** current `ach/internal/litellm/noop.go` line 67 — `var _ Client = (*NoopClient)(nil)`.

**Apply to:** Phase 2 widens the Client interface; the same assertion at the bottom of `noop.go` will break the build if `NoopClient` forgets a new method. Add a matching assertion at the bottom of `internal/litellm/client.go`:
```go
var _ Client = (*RESTClient)(nil)
```

### LITELLM_OPERATOR_* → ACH_LITELLM_* rename table

**Apply to:** every file lifted from `../ach_litellm/internal/litellm/`. The planner must produce this exact rename map in the implementation plan for reviewer sanity (per CONTEXT specifics §"`ACH_LITELLM_*` prefix is the user's preferred normalization"):

| Sister const / env-var | Sister file / line | ACH replacement |
|---|---|---|
| `LITELLM_OPERATOR_AUTH_HEADER` | `client.go` line 46 | `ACH_LITELLM_AUTH_HEADER` |
| `LITELLM_OPERATOR_DANGEROUSLY_LOG_BODIES` | `transport.go` line 26 | `ACH_LITELLM_DANGEROUSLY_LOG_BODIES` |
| `LITELLM_MASTER_KEY` (mentioned in doc.go) | `doc.go` line 38 | `ACH_LITELLM_MASTER_KEY` |
| (new — no sister equivalent) | — | `ACH_LITELLM_BASE_URL` |
| (new — no sister equivalent) | — | `ACH_ORPHAN_CLEANUP_INTERVAL` |

---

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `internal/audit/handler.go` | utility (slog handler) | transform | Net-new audit-logging surface. Stdlib `log/slog` `JSONHandler` + a top-level `audit=true` attribute is the entire pattern; no sister analog exists. Memory pattern [[feedback_litellm_operator_no_redaction_filter]] in user memory drives the design choice (discipline over scrubbing — handler ships raw, callers compose audit-safe events). |

Source-type fetchers (`internal/sources/<type>/fetcher.go`) are listed under "role-match" rather than "no analog" because the `makeRequest` shape in `../ach_litellm/internal/litellm/client.go` is a viable structural reference even though the SDKs differ — the planner can lift the `defer drainAndClose` + `http.NewRequestWithContext` + `io.LimitReader` discipline directly.

---

## Metadata

**Analog search scope:**
- `/home/jcm/Projects/ach/` (Phase 1 code)
- `/home/jcm/Projects/ach_litellm/` (canonical lift source per memory `reference_ach_litellm_sister_project`)

**Files scanned (this repo):**
- 6 controller files (`internal/controller/ach/*.go`)
- `cmd/operator/main.go`
- `internal/litellm/client.go`, `noop.go`
- `internal/cachefs/bootstrap.go`
- `internal/db/db.go`
- `internal/config/config.go`
- `api/ach/v1alpha1/external_ref_types.go`
- `db/migrations/000001_init.up.sql`
- `config/rbac/operator_role.yaml`

**Files scanned (sister):**
- All of `internal/litellm/*.go` (10 source files, 2,446 lines incl. tests — verbatim lift target per D-01)
- `internal/connection/cache.go`, `snapshot.go` (manager.Runnable + atomic.Pointer pattern)
- `internal/litellm/mock/mock.go` (httptest mock shape for D-discretion test infra)

**Pattern extraction date:** 2026-05-15

**Memory references applied:**
- `reference_ach_litellm_sister_project` — sister is the canonical kubebuilder/multigroup/Makefile/golangci/RBAC reference, and Phase 2 takes it further with the literal `internal/litellm/` lift
- `feedback_litellm_operator_no_redaction_filter` — drives the audit handler's "discipline over scrubbing" design
- `reference_litellm_autoconfig_predecessor` — predecessor Python daemon informs the LiteLLM endpoint set Phase 2 calls (`/key/info`, `/model/info`, `/v1/mcp/server`, `/v1/agents`)
