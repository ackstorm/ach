---
phase: 02-external-refs-marketplace-operator-reconciliation
reviewed: 2026-05-18T06:14:54Z
depth: standard
files_reviewed: 78
files_reviewed_list:
  - api/ach/v1alpha1/external_ref_types.go
  - cmd/operator/main.go
  - config/crd/bases/ach.ackstorm.ai_artifacts.yaml
  - config/crd/bases/ach.ackstorm.ai_pluginmarketplaces.yaml
  - config/crd/bases/ach.ackstorm.ai_plugins.yaml
  - config/crd/bases/ach.ackstorm.ai_prompts.yaml
  - db/migrations/000002_phase2.down.sql
  - db/migrations/000002_phase2.up.sql
  - internal/audit/doc.go
  - internal/audit/handler.go
  - internal/audit/handler_test.go
  - internal/cachefs/sweep.go
  - internal/cachefs/sweep_test.go
  - internal/config/config.go
  - internal/config/config_test.go
  - internal/controller/ach/artifact_controller.go
  - internal/controller/ach/conditions.go
  - internal/controller/ach/external_ref_refresh.go
  - internal/controller/ach/external_ref_refresh_test.go
  - internal/controller/ach/main_wiring_envtest_test.go
  - internal/controller/ach/marketplace_conflict.go
  - internal/controller/ach/marketplace_conflict_test.go
  - internal/controller/ach/marketplace_filters.go
  - internal/controller/ach/marketplace_filters_test.go
  - internal/controller/ach/marketplace_parse.go
  - internal/controller/ach/marketplace_parse_test.go
  - internal/controller/ach/plugin_controller.go
  - internal/controller/ach/pluginmarketplace_controller.go
  - internal/controller/ach/pluginmarketplace_envtest_test.go
  - internal/controller/ach/prompt_controller.go
  - internal/db/active_keys.go
  - internal/db/active_keys_test.go
  - internal/db/external_refs.go
  - internal/db/external_refs_test.go
  - internal/db/litellm_users.go
  - internal/db/litellm_users_test.go
  - internal/db/marketplace_plugins.go
  - internal/db/marketplace_plugins_test.go
  - internal/db/phase2_helpers_test.go
  - internal/litellm/agents.go
  - internal/litellm/agents_test.go
  - internal/litellm/client_test.go
  - internal/litellm/doc.go
  - internal/litellm/errors.go
  - internal/litellm/keyinfo.go
  - internal/litellm/mcp.go
  - internal/litellm/mcp_test.go
  - internal/litellm/model.go
  - internal/litellm/model_test.go
  - internal/litellm/noop.go
  - internal/litellm/restclient.go
  - internal/litellm/team.go
  - internal/litellm/team_test.go
  - internal/litellm/transport.go
  - internal/litellm/transport_test.go
  - internal/litellm/types.go
  - internal/orphan/doc.go
  - internal/orphan/runnable.go
  - internal/orphan/runnable_test.go
  - internal/snapshot/doc.go
  - internal/snapshot/snapshot.go
  - internal/snapshot/snapshot_test.go
  - internal/sources/bitbucket/fetcher.go
  - internal/sources/bitbucket/fetcher_test.go
  - internal/sources/errors.go
  - internal/sources/errors_test.go
  - internal/sources/gcs/fetcher.go
  - internal/sources/gcs/fetcher_test.go
  - internal/sources/github/fetcher.go
  - internal/sources/github/fetcher_test.go
  - internal/sources/github/transport.go
  - internal/sources/gitlab/fetcher.go
  - internal/sources/gitlab/fetcher_test.go
  - internal/sources/http/fetcher.go
  - internal/sources/http/fetcher_test.go
  - internal/sources/registry/registry_test.go
  - internal/sources/s3/fetcher.go
  - internal/sources/s3/fetcher_test.go
  - internal/sources/sources.go
findings:
  critical: 3
  warning: 9
  info: 6
  total: 18
status: issues_found
fixes_applied:
  blocker_and_warning_fixed: 12
  info_deferred: 6
  fixed_commits:
    CR-01: c37c234
    CR-02: 5fa3401
    CR-03: 4687a46
    WR-01: 6e83226
    WR-02: d4d90dc
    WR-03: 72db0ec
    WR-04: 4687a46  # rolled into CR-03 commit (audit/doc.go shape update)
    WR-05: 90588f4
    WR-06: 87ee66e
    WR-07: c2b1ee8
    WR-08: 39155bb  # also fixes IN-04 (.tmp exemption tightening)
    WR-09: e85c40c
  info_status:
    IN-01: deferred  # comment-only nit; reword on next pass
    IN-02: deferred  # comment vs %w semantics; defer until live hashing surface
    IN-03: deferred  # no-op (documented for future maintainers)
    IN-04: fixed_in_WR-08  # incidentally addressed by sweep.go IsEmpty refactor
    IN-05: deferred  # test-discipline nit
    IN-06: fixed_in_CR-03  # incidentally addressed by failure-path forbidden-attr assertions
---

# Phase 2: Code Review Report

**Reviewed:** 2026-05-18T06:14:54Z
**Depth:** standard
**Files Reviewed:** 78
**Status:** issues_found
**Fixes Applied:** 2026-05-18 — 12/12 Blocker+Warning fixed; 2/6 Info incidentally fixed (IN-04 via WR-08, IN-06 via CR-03); 4/6 Info deferred (out of fix scope). See `fixes_applied:` block in frontmatter for per-finding commit SHAs.

## Summary

Phase 2 lands a substantial body of work — six per-source-type fetchers, a three-stage PluginMarketplace reconciler, the LiteLLM snapshot Runnable, the orphan-cleanup Runnable, and the §10.3 staging/rename pipeline shared by Plugin/Prompt/Artifact. Test coverage is broad: pure-Go unit tests for parsers/filters/conflict resolution, envtest harnesses for materialize and reconcile flows, and tagged integration tests for db helpers.

The implementation generally honors the load-bearing invariants — parameterized SQL throughout, HMAC-pepper fail-fast at startup, HTTPS-only (LIFTED 2026-05-18 (Phase 02.1 commit 45b7558)) enforcement on the generic HTTP source, REL-04 drain+close discipline on every fetcher response path, OP-11 empty-PVC recovery wired correctly, and the snapshot Runnable using `atomic.Pointer` for lock-free publication. The orphan loop's race-defender (`OrphanAgeFloor`) and abort-on-unreachable semantics are correct.

However, the review surfaced three blockers:

1. The **GitLab fetcher buffers the entire upstream archive into operator memory** before the size-cap LimitReader can enforce a bound — defeating the size cap's purpose and exposing the operator to memory exhaustion. (CR-01)
2. The **Bitbucket fetcher interpolates `Workspace`, `Repo`, and `Ref` directly into URLs without URL-escaping**. CRD validation enforces only `minLength: 1` — there is no pattern restriction — so a CR author can craft path-traversal / cross-host inputs that corrupt the request URL. (CR-02)
3. The **orphan-cleanup Runnable emits `err.Error()` as an attribute on the audit log** for two of the three audit-event paths. Combined with the audit handler's documented "no scrubbing" contract and the Phase 2 emitter contract (only `{target.kind, target.name, outcome, request_id}`), this is a Hub §16.1 invariant violation surface — LiteLLM error wrappers transitively contain the request URL+path, and there is no guarantee future error shapes won't include body fragments. (CR-03)

Beyond the blockers, the review surfaced 9 warnings (notably: S3 endpoint override bypasses the HTTPS-only (LIFTED 2026-05-18 (Phase 02.1 commit 45b7558)) invariant; reconciler annotation-clear runs after a failed status update; `pepper` is validated but never consumed in Phase 2; documentation/code drift between `audit/doc.go` and the orphan emitter shape) and 6 info-level quality items.

## Critical Issues

### CR-01: GitLab fetcher buffers entire archive in memory before size-cap can act

**Status:** fixed_in: c37c234
**File:** `internal/sources/gitlab/fetcher.go:111-126`
**Issue:** `client.Repositories.Archive(...)` returns `archiveBytes []byte` — the SDK reads the FULL response body into memory before returning. The fetcher then wraps those bytes in `io.NopCloser(bytes.NewReader(archiveBytes))` so the caller's `materializeExternalRef` (`internal/controller/ach/external_ref_refresh.go:259-271`) can apply `io.LimitReader(result.Body, deps.SizeCapBytes+1)`. But the bytes are ALREADY in operator-process memory by then — the LimitReader cannot bound peak memory.

A malicious or misconfigured GitLab project archive could exhaust the operator pod's memory before any size check executes. This contradicts CLAUDE.md's "Content Service streams via sendfile(2) and never buffers a full body" invariant; while this code is in the Operator (not Content Service), the same streaming discipline must apply because the operator is the load-bearing single-replica process.

The comment at `gitlab/fetcher.go:106-109` acknowledges the buffering as a known SDK constraint, but no mitigation is in place (no streaming-archive endpoint via raw HTTP, no pre-flight `HEAD` size check). All five other source fetchers stream correctly via `resp.Body`.

**Fix:** Replace the SDK-archive call with a direct HTTP GET against the GitLab archive endpoint, similar to how the Bitbucket fetcher constructs `archiveURL` from `defaultBitbucketWeb`:

```go
// Authenticated HTTPS GET against gitlab archive URL — keeps the body streaming.
archiveURL := fmt.Sprintf("%s/api/v4/projects/%s/repository/archive.tar.gz?sha=%s",
    host, url.PathEscape(f.spec.Project), url.QueryEscape(sha))
httpReq, _ := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, archiveURL, nil)
httpReq.Header.Set("PRIVATE-TOKEN", string(token))
resp, err := httpClient.Do(httpReq)
// ... drain+close on non-2xx; return resp.Body as the streaming Body.
```

This mirrors the bitbucket pattern (which also calls the SDK only for the SHA resolution and uses raw HTTP for the archive). Until the streaming fix lands, set a hard input size limit before the SDK call by checking the project's last-commit metadata for unusually large repos — but the proper fix is to stream.

### CR-02: Bitbucket fetcher constructs URLs from unescaped CR-provided strings

**Status:** fixed_in: 5fa3401
**File:** `internal/sources/bitbucket/fetcher.go:82-83, 104-105`
**Issue:** The fetcher builds two URLs via `fmt.Sprintf`:

```go
commitURL := fmt.Sprintf("%s/2.0/repositories/%s/%s/commit/%s",
    defaultBitbucketAPI, f.spec.Workspace, f.spec.Repo, f.spec.Ref)
...
archiveURL := fmt.Sprintf("%s/%s/%s/get/%s.tar.gz",
    defaultBitbucketWeb, f.spec.Workspace, f.spec.Repo, sha)
```

`f.spec.Workspace`, `f.spec.Repo`, and `f.spec.Ref` are CR-provided strings. The BitbucketSource CRD (`config/crd/bases/ach.ackstorm.ai_plugins.yaml`) enforces only `minLength: 1` — there is no pattern restriction. A CR author can therefore set `Workspace: "evil-host.example/path"` or `Ref: "main?token=x"` and corrupt the resulting URL, potentially redirecting the fetch to an attacker-controlled host or smuggling query parameters past the Bearer auth header (T-02-02-02 / T-02-02-07 mitigations exist only against credentials in the URL, not against attacker-controlled URL segments).

GitHub avoids this by parsing `Repo` into `owner/name` and passing both to `go-github`, which performs internal escaping. GitLab/S3/GCS use their SDKs. The Bitbucket fetcher is the only one assembling URLs manually from CR-provided strings.

The Ref field has even less validation — Bitbucket allows `feature/branch` refs that legitimately contain `/`, which immediately produces an attacker-controlled URL path segment.

**Fix:** URL-escape every interpolated segment, and add a CRD pattern constraint on `Workspace` / `Repo`:

```go
// Use url.PathEscape on every path segment.
commitURL := fmt.Sprintf("%s/2.0/repositories/%s/%s/commit/%s",
    defaultBitbucketAPI,
    url.PathEscape(f.spec.Workspace),
    url.PathEscape(f.spec.Repo),
    url.PathEscape(f.spec.Ref))

// Also reject Workspace/Repo with embedded '/', '?', '#' even before URL escape
// via CRD pattern: ^[a-z0-9][-a-z0-9_]*$ (or whatever Bitbucket actually accepts).
```

The same audit applies to `f.spec.Repo` in the gitlab path. The CRD additions should be merged into the OpenAPI schemas at `config/crd/bases/ach.ackstorm.ai_{plugins,prompts,artifacts,pluginmarketplaces}.yaml`.

### CR-03: Orphan-cleanup audit events leak underlying error text on failure paths

**Status:** fixed_in: 4687a46
**File:** `internal/orphan/runnable.go:188-194, 207-213`
**Issue:** The orphan-cleanup Runnable emits `"err", err.Error()` as an audit attribute on the `litellm_unreachable` and `revoke_failed` paths. The `err` value originates from `litellm.Client.ListUserKeys` / `litellm.Client.RevokeKey` — both go through `RESTClient.makeRequest`.

Three problems compound:

1. **Documentation/code drift.** `internal/audit/doc.go:39-41` states "Phase 2's only emitter (Plan 02-08 orphan cleanup) limits attributes to {target.kind, target.name, outcome, request_id}." But the production code emits `user_id` and `err` (no `request_id`). The doc and the emitter disagree on the shape of an audit event.

2. **No-redaction invariant.** `audit/doc.go:34-38` documents: "the handler emits records raw — it does NOT scrub plaintext, body content, or header values. Audit-safety is the CALLER's responsibility." The caller (this Runnable) does NOT scrub `err.Error()` — it passes it raw. While `makeRequest` is careful to omit response bodies from error strings (`internal/litellm/restclient.go:138, 158, 166`), the `transport error: %w` wrap at line 138 includes the underlying `err.Error()` from net/http. That string is bounded but not guaranteed to be free of body fragments on every Go runtime version / network failure mode.

3. **The success-path test forbids `err`, but no test guards the failure paths.** `orphan/runnable_test.go:401-405` explicitly asserts that the SUCCESS audit event contains no `"err"`, `"bearer"`, `"body"`, etc. But the `TestRunnable_TickOnce_LiteLLMUnreachable` and `TestRunnable_TickOnce_RevokeFailureContinues` tests do NOT make the same assertion on the failure-path events — they only check `outcome` and `target.kind`. The forbidden-attribute discipline is therefore enforced only for the path that's already safe.

This violates Hub §16.1 / §18.2 "plaintext key values MUST NOT be persisted anywhere (DB, Redis, logs, **audit**, metrics, traces)." Even if today's `err.Error()` is clean, the audit channel cannot trust unredacted error text because the audit handler explicitly does not scrub.

**Fix:**

```go
// In runnable.go: replace `err.Error()` with a bounded, sanitized error classifier.
// One approach: emit only the classification, not the message.
if errors.Is(err, context.DeadlineExceeded) {
    r.Audit.Info("operator.orphan-cleanup",
        "target.kind", "tick",
        "outcome", OutcomeLiteLLMUnreachable,
        "user_id", uid,
        "err.class", "timeout")
} else if _, is401 := litellm.IsAuth401(err); is401 {
    // ... etc.
}
```

Better: drop `err` from the audit event entirely (the operational log already captures `err` for diagnostics — line 193-194). The audit channel should carry only the closed-enum outcome + identifiers; diagnostic detail belongs in the operational log.

Either way, also:
- Update `internal/audit/doc.go:39-41` so the documented attribute set matches the emitted one (or vice versa).
- Add `err`, `bearer`, `body`, `credential_hash` to the forbidden-attribute assertion in `TestRunnable_TickOnce_LiteLLMUnreachable` and `TestRunnable_TickOnce_RevokeFailureContinues`.

## Warnings

### WR-01: S3 spec.Endpoint allows non-HTTPS override

**Status:** fixed_in: 6e83226
**File:** `internal/sources/s3/fetcher.go:92-97`, `api/ach/v1alpha1/external_ref_types.go:209-210`
**Issue:** The S3Source.Endpoint field has no `+kubebuilder:validation:Pattern=^https://` annotation. The S3 fetcher honors `f.spec.Endpoint` verbatim:

```go
if f.spec.Endpoint != "" {
    o.BaseEndpoint = aws.String(f.spec.Endpoint)
    o.UsePathStyle = true
}
```

This contradicts CLAUDE.md's "HTTPS-only via deployment-configured `ACH_BASE_URL` — no HTTP escape hatch." The HTTP source enforces HTTPS strictly (`internal/sources/http/fetcher.go:65-68`); S3 does not. While the comment in s3 fetcher says "Endpoint override is honored for MinIO and S3-compatible alternatives," the spec does not allow an HTTP escape hatch.

A deployer who legitimately needs `http://minio.internal:9000` for MinIO would face the same constraint. The right answer is to make this a deliberate, deployment-wide opt-in (e.g., an `ACH_S3_ALLOW_HTTP_ENDPOINT=true` operator-pod env var) rather than letting every CR author bypass HTTPS unilaterally.

**Fix:** Either (a) add `+kubebuilder:validation:Pattern=^https://` to `S3Source.Endpoint` and document MinIO-with-TLS as the only supported pattern, or (b) gate non-HTTPS endpoints behind an operator-pod env-var check at fetcher construction time:

```go
func New(spec *achv1alpha1.S3Source) (*Fetcher, error) {
    if spec == nil { /* ... */ }
    if spec.Endpoint != "" && !strings.HasPrefix(spec.Endpoint, "https://") {
        if os.Getenv("ACH_S3_ALLOW_HTTP_ENDPOINT") != "true" {
            return nil, fmt.Errorf("s3: endpoint %q is not https: %w", spec.Endpoint, sources.ErrUpstreamInvalid)
        }
    }
    return &Fetcher{spec: spec}, nil
}
```

### WR-02: Plugin reconciler runs annotation Update after a failed Status Update

**Status:** fixed_in: d4d90dc
**File:** `internal/controller/ach/plugin_controller.go:222-233` (and parallel sites in `prompt_controller.go:172-181`, `artifact_controller.go:194-203`, `pluginmarketplace_controller.go:340-350`)
**Issue:** The reconciler calls `r.Status().Update(ctx, &cr)` and logs the error if it fails, then proceeds to remove the force-refresh annotation via `r.Update(ctx, &cr)`. If the status update fails (e.g., 409 Conflict from a concurrent reconcile), the in-memory `cr` will have a stale `ResourceVersion`, and the subsequent `r.Update` is guaranteed to ALSO fail with a 409. The second failure is logged but otherwise ignored — leading to repeated reconcile cycles where the annotation never clears.

This isn't load-bearing for correctness (the annotation eventually clears on the next successful reconcile cycle), but it pollutes logs and complicates debugging.

**Fix:** Skip the annotation-clear step when the status update failed:

```go
if err := r.Status().Update(ctx, &cr); err != nil {
    logger.Error(err, "status update failed")
    return ctrl.Result{RequeueAfter: requeue}, nil // skip annotation clear; retry next tick
}

// D-07: clear force-refresh annotation if present.
if _, hasAnnotation := cr.Annotations["ach.ackstorm.ai/force-refresh"]; hasAnnotation {
    // ... existing logic
}
```

### WR-03: `pepper` env var is validated and consumed but never wired into any hashing path

**Status:** fixed_in: 72db0ec  (validation lifted into internal/credhash/pepperenv)
**File:** `cmd/operator/main.go:142-161`
**Issue:** `MustEnvNonEmpty("ACH_CREDENTIAL_HASH_PEPPER")` is called and the value is held in a local. The placeholder-prefix check defends against deploying with `REPLACE-ME-WITH-RANDOM-...`. Then `_ = pepper` (line 161) explicitly discards the value with a comment: "Phase 1 has no live hashing surface; the value is held by the process and never logged."

But Phase 2 also has no live hashing surface for credentials (the personal_keys/environment_keys write paths are Phase 3). The pepper value is read into a string in process memory, validated, then thrown away. If the deployer rotates the pepper between Phase 2 and Phase 3, there's no contract that the pepper validated at Phase 2 startup IS the same value Phase 3 will use at runtime.

This is a "design smell" rather than a defect — the pepper-loading happens at the wrong layer. A future Phase 3 might re-read the env var, but the load-bearing invariant is unobservable from the code today.

**Fix:** Add a TODO comment naming the Phase 3 consumer, and consider lifting the env-var validation into a `credhash.NewPepperFromEnv()` constructor that Phase 3's hashing wrapper consumes. The validation logic shouldn't live in `main.go` — it should live with the package that uses it.

### WR-04: `internal/audit/doc.go` documents an emitter shape that doesn't match the production code

**Status:** fixed_in: 4687a46  (rolled into CR-03 commit; doc updated to match emitter shape)
**File:** `internal/audit/doc.go:39-41` vs `internal/orphan/runnable.go:188-225`
**Issue:** The audit package doc says:

> "Phase 2's only emitter (Plan 02-08 orphan cleanup) limits attributes to {target.kind, target.name, outcome, request_id}."

But the production code emits `user_id` (always) and `err` (failure paths), and never emits `request_id`. This is documentation rot — the documented contract is wrong.

**Fix:** Update the audit doc to match the production emitter shape:

```
Phase 2's only emitter (Plan 02-08 orphan cleanup) emits:
{target.kind, target.name (when applicable), outcome, user_id}.
A future request_id field is reserved for Phase 3 Platform API events.
```

Or, alternatively, change the production code to align with the doc.

### WR-05: `applyFilters` always processes the full plugin set even when include is empty (wasted allocation)

**Status:** fixed_in: 90588f4
**File:** `internal/controller/ach/marketplace_filters.go:91-103`
**Issue:** When `include == nil`:

```go
if include == nil {
    stage1 = append(stage1, plugins...)
    includeMatchedAny = true
}
```

This allocates a fresh slice and copies every plugin. Then the exclude stage iterates over `stage1` again. The function could short-circuit when both include and exclude are nil (`applyFilters_NeitherSet`). The current code path is O(n) on the no-filter case where it should be O(1) plus aliasing.

This is a quality/clarity issue, not a correctness defect, but the comment "Exclude stage" implies an early-return optimization that isn't there.

**Fix:**

```go
func applyFilters(plugins []ClaudeCodeMarketplacePlugin, include, exclude []*regexp.Regexp) (kept []ClaudeCodeMarketplacePlugin, includeMatchedAny bool) {
    if include == nil && exclude == nil {
        return plugins, true
    }
    // ... existing logic
}
```

### WR-06: Stage-3 cache-file remove ignores non-IsNotExist errors silently

**Status:** fixed_in: 87ee66e  (fail-loud — return error so workqueue retries; symmetric with DB-delete failure path)
**File:** `internal/controller/ach/pluginmarketplace_controller.go:314-317`
**Issue:**

```go
if err := os.Remove(cachePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
    logger.Error(err, "stage-3 cache file remove", "name", row.Name)
    // Continue — DB delete is the load-bearing record.
}
```

If the file remove fails with EBUSY, EACCES, or any other non-NotExist error, the code logs and continues to delete the DB row. After this, the operator has dropped the marketplace_plugins row but the cached file still exists on disk. The Content Service will continue serving the stale file even though the marketplace no longer lists it.

The comment "DB delete is the load-bearing record" is technically true for serve-side authorization (Content Service consults the DB), but the orphan file remains in cache, consuming space and surfacing in any `cache size` audit.

**Fix:** Add a sweep step on the next reconcile: when listing prior_rows and the on-disk file exists but no DB row references it, re-attempt the remove. Alternatively, fail-loud on a non-NotExist remove error so the reconciler re-tries; the trade-off is that a single bad file blocks the whole marketplace's reconcile cycle.

### WR-07: Stage-1 `markSyncedFalse` with `ReasonUnauthorized` passes literal Secret name in the message

**Status:** fixed_in: c2b1ee8  (conservative — used %q-quoted fmt.Sprintf so IsNotFound path matches the non-IsNotFound error wrap format; classifyFetchError funneling not done because it's only one site)
**File:** `internal/controller/ach/pluginmarketplace_controller.go:167, 169, 181`
**Issue:**

```go
return r.markSyncedFalse(ctx, &cr, ReasonUnauthorized, "stage-1: marketplace auth Secret "+authRef.Name+": not found", requeue, nil)
```

The Secret name `authRef.Name` is concatenated into a `Synced=False` Condition.Message. This is fine in isolation (Secret names are non-sensitive), but the parallel sites do `fmt.Errorf("stage-1: get marketplace auth Secret %q: %w", authRef.Name, err)` and let `classifyFetchError` build the message — three different code paths build the message three different ways. The pattern is inconsistent.

**Fix:** Funnel all error messages through `classifyFetchError` or a shared formatter so the wire-format for `Condition.Message` is consistent across reason codes.

### WR-08: `IsEmpty` could mis-classify a `Plugin/<emptySubdir>/` layout as populated

**Status:** fixed_in: 39155bb  (recursive subtreeHasFile; tests cover empty-nested-subdir + deeply-nested-file + stray top-level .tmp file. Incidentally fixes IN-04.)
**File:** `internal/cachefs/sweep.go:73-82`
**Issue:** The `IsEmpty` predicate iterates top-level entries (skipping `.tmp`), and for each, calls `os.ReadDir` and treats `len(sub) > 0` as "populated." If a subdirectory contains an EMPTY nested subdir (e.g. `plugin/abc/` with nothing inside), `os.ReadDir(plugin)` returns `[abc]`, `len == 1`, so IsEmpty returns `(false, nil)` — concluding cache is populated.

In production this is unlikely (the reconcilers create files, not empty subdirs), but the behavior is surprising. A truly-empty cache structure with stray empty directories will skip OP-11 recovery.

**Fix:** Recurse one level deeper, OR document the assumption that "any entry under a subdir, regardless of file/dir nature, indicates populated cache" is intentional. Add a unit test for the `plugin/<empty-subdir>/` case so future edits don't change the semantic accidentally.

### WR-09: Marketplace `pluginFailure` records use `ReasonNameConflict` for Plugin-CRD-wins drops, misleading downstream consumers

**Status:** fixed_in: e85c40c  (new ReasonPluginCRDPrecedence constant; dispatch on "Plugin CRD " prefix; envtest TestPMR_PluginCRDBeatsMarketplace assertion updated)
**File:** `internal/controller/ach/pluginmarketplace_controller.go:255-261, 332-338`
**Issue:** When `decisions[i].Kept = false` because a Plugin CRD owns the name (Reason: `"Plugin CRD '...' takes precedence"`), the code still adds to `failures` with `reason: ReasonNameConflict`:

```go
if !d.Kept {
    if strings.HasPrefix(d.Reason, "marketplace ") {
        marketplaceLoserFound = true
    }
    failures = append(failures, pluginFailure{name: d.PluginName, reason: ReasonNameConflict})
    continue
}
```

The status.message will read `"stage-2: 1 plugin(s) failed: foo: NameConflict"` regardless of whether the conflict was Plugin-CRD-wins or marketplace-loses. Comments at lines 333-334 say "Plugin-CRD-wins drops do NOT flip Synced — they're recorded as informational only" but the reason string is the same, so an operator reading `kubectl describe pluginmarketplace foo` cannot tell the two cases apart from the status message alone.

**Fix:** Introduce a distinct reason for Plugin-CRD-wins:

```go
const ReasonPluginCRDPrecedence = "PluginCRDPrecedence"
...
reason := ReasonNameConflict
if strings.HasPrefix(d.Reason, "Plugin CRD ") {
    reason = ReasonPluginCRDPrecedence
}
failures = append(failures, pluginFailure{name: d.PluginName, reason: reason})
```

The Synced-flip logic (only flips on marketplace-loser) stays unchanged.

## Info

### IN-01: `pluginmarketplace_controller.go:295-307` comment contradicts the code

**Status:** deferred  (out of fix scope: blocker+warning only; comment-only nit, no behavior impact)
**File:** `internal/controller/ach/pluginmarketplace_controller.go:294-307`
**Issue:** The comment says:

> "Only Kept plugins that successfully materialized belong in currentNames — failed Stage-2 entries that ALSO had a prior row should be retained..."

But the code at line 305 adds EVERY Kept entry to `currentNames`, regardless of whether Stage-2 succeeded. The behavior is correct (failed Stage-2 entries retain their prior row), but the comment misleads.

**Fix:** Reword the comment: "Every Kept entry goes into currentNames — including Stage-2 failures, so their prior rows aren't swept. The failure is captured in status.message; the prior cached file continues serving until the next successful refresh overwrites it."

### IN-02: `internal/litellm/restclient.go` comment about credentials in URLs is misleading

**Status:** deferred  (out of fix scope: blocker+warning only; comment vs %w semantics — defer until live hashing surface lands in Phase 3)
**File:** `internal/litellm/restclient.go:135-138`
**Issue:** The comment claims:

> "Note we do NOT include err.Error() unredacted; the underlying URL/error wrapper from net/http may contain credential fragments leaked via DNS."

But the very next line uses `%w` to wrap the error, which preserves `err.Error()` in the chain — calling `.Error()` on the wrapped error will surface the underlying message. The redaction is NOT actually happening at the string-formatting layer.

**Fix:** Either (a) genuinely redact the wrapped error (e.g., wrap as `fmt.Errorf("litellm: %s %s: transport error: %v", method, path, sanitize(err))`), or (b) update the comment to say "the URL embedded in net/http errors does not contain credentials since ACH places auth in headers." The current state of the code matches (b); the comment is misleading.

### IN-03: `transport.go:65-71` logs `status="error"` but always logs the path

**Status:** deferred  (out of fix scope: blocker+warning only; reviewer marked "no action required")
**File:** `internal/litellm/transport.go:60-72`
**Issue:** On network error, the redacting RoundTripper logs `path` (the URL path, e.g., `/key/info?user_id=...`). The path can include user_id in query string, which is a non-sensitive identifier. No action needed, but worth noting that `path` is the most informationally-rich attribute in the default-redaction log line, and any query parameter added to a LiteLLM call site will appear here.

**Fix:** None required. Documented for future maintainers.

### IN-04: `IsEmpty` allows a fake-named `.tmp` directory to mask population

**Status:** fixed_in: 39155bb  (incidentally fixed during WR-08 refactor — .tmp exemption now keyed on entry.IsDir() && entry.Name() == ".tmp"; TestIsEmpty_StrayTmpFile guards against regression)
**File:** `internal/cachefs/sweep.go:62-68`
**Issue:** The `.tmp` exemption is keyed on the literal directory name. A reconciler bug or admin misconfiguration that creates a top-level `.tmp` regular file (not directory) would still be skipped by the check. This is a theoretical edge case — production code paths never create such a file.

**Fix:** Tighten the check to `entry.IsDir() && entry.Name() == ".tmp"`. Otherwise stray top-level `.tmp` files (e.g., from a misconfigured init container) would be invisible to IsEmpty.

### IN-05: `applyFilters_AnchorPrependedNotSubstring` test would fail under a future regex engine change

**Status:** deferred  (out of fix scope: blocker+warning only; test-discipline nit)
**File:** `internal/controller/ach/marketplace_filters_test.go:150-162`
**Issue:** The test relies on Go's `regexp` package compiling `^lph` and rejecting `alpha`. This is RE2 behavior by spec, but the comment "The operator-prepended ^ makes it match only at start" is the load-bearing assertion. The test asserts the BEHAVIOR (no match), not the COMPILED PATTERN. Good enough, but a future edit that double-prepends `^` to a pattern already starting with `^` would silently regress to a different behavior.

**Fix:** Add an assertion on `out[0].String()` to lock in the exact prepended pattern: `"^^lph"` is a valid regex but means "two start-anchors" which RE2 treats as equivalent to one — still correct, but worth documenting.

### IN-06: Test `TestRunnable_AuditEventShape` forbids `err` on success, but no parallel test on failure paths

**Status:** fixed_in: 4687a46  (incidentally addressed during CR-03 — assertNoForbiddenAttrs helper extracted and applied to LiteLLMUnreachable, RevokeFailureContinues, MultipleUsers_OneFailsListUserKeys tests)
**File:** `internal/orphan/runnable_test.go:355-420`
**Issue:** The test forbids `key_alias`, `credential_hash`, `bearer`, `body`, `header`, `err` from the success-path audit event. But `TestRunnable_TickOnce_LiteLLMUnreachable` (line 255) and `TestRunnable_TickOnce_RevokeFailureContinues` (line 287) do NOT assert the same forbidden set — they only check `outcome` + `target.kind`. Combined with CR-03 above, this is the test gap that allowed the `err` leak to land.

**Fix:** Extract the forbidden-attribute check from `TestRunnable_AuditEventShape` into a helper and apply it to every audit-event-emitting test. The forbidden set should also be updated to include `err` once CR-03's fix removes the field.

---

_Reviewed: 2026-05-18T06:14:54Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

_Fixes Applied: 2026-05-18 — gsd-code-fixer_
_Scope: Blocker + Warning (12/12 fixed); Info findings IN-04 and IN-06 incidentally fixed; IN-01/02/03/05 deferred (out of fix scope)._
