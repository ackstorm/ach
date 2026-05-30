# Phase 5: Content Service + Cross-component Observability - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-05-27
**Phase:** 5-content-service-cross-component-observability
**Areas discussed:** Streaming + header policy; Authz pipeline shape + caches (incl. spec-v4 §5.2 scope decision); /metrics topology + shared metrics pkg; Existing handler — wrap vs rewrite

---

## Streaming + header policy

| Option | Description | Selected |
|--------|-------------|----------|
| Custom serve | Replace http.ServeContent with open/Stat/headers/io.Copy. Linux sendfile(2) engages via *os.File.WriteTo. Range/INM/IMS never inspected. Cleaner contract surface; aligns 1:1 with SC#1. | ✓ |
| ServeContent + strip request headers | Delete conditional/range headers from r.Header before ServeContent; override Cache-Control to no-store. Indirect; verifying "ignored" requires stdlib source reading. | |
| Hybrid io.Copy + manual mtime | Drop ServeContent, manual headers, io.CopyBuffer(w,f,nil) explicit nil buf. Equivalent to A in practice. | |

**User's choice:** Custom serve (Recommended).
**Notes:** Implies dropping http.ServeContent entirely; sendfile(2) E2E gate via strace added to test plan (D-20). Cache-Control flipped from current `public, max-age=300` (commit 3266513) to `no-store` per SC#1.

---

## Authz pipeline shape + caches — sub-discussion A: gate ordering

| Option | Description | Selected |
|--------|-------------|----------|
| Locked spec order | 1. Authn 2. Env 3. Teams 4. Content resolve (CRD/marketplace) 5. Allowlist 6. Staleness. Spec §15.6 v10-fix order: 404 = no resource cluster-wide, 403 = resource exists but not granted to this Environment. | |
| Swap 4 ↔ 5 (cheaper-first) | Run context allowlist BEFORE Plugin §12.3 resolution. Saves Postgres roundtrip when name not in context list. Diverges from spec §15.6 — unknown name yields 403 instead of 404. | ✓ |
| Other ordering | User-defined. | |

**User's choice:** Swap 4 ↔ 5 (cheaper-first).
**Notes (verbatim from user):** "cheaper-first, but remember (I think is in spec file) we do not check CRD for anyhting, or source of truth is postgres". Triggered the spec-v4 §5.2 reversal discovery (see sub-discussion B below). Side effect: a name absent from both `Environment.spec.context` AND any projection row returns `403 unauthorized_content` (allowlist fires first) instead of `404 content_not_found`. Deliberate info-leak narrowing — Environment grant state never leaks "cluster has this resource". Captured in CONTEXT.md D-04 + flagged for VERIFICATION dashboard.

---

## Authz pipeline shape + caches — sub-discussion B: Phase 5 scope after spec-v4 §5.2 reversal

User-prompted spec re-read. Found `spec/ach_hub_spec_v20260515_FINALv4.md` line 13: "Platform API, Forwarder, and Content Service no longer hold informers over ACH CRDs; they read CRD spec/status from Postgres. Only the ACH Operator watches Kubernetes." This invalidates the informer-first read-model assumed by Phase 4 CONTEXT.md AND requires Phase 5 to add a DB projection layer for the ACH CRDs that Content Service reads (Environment, Plugin, Prompt, Artifact).

| Option | Description | Selected |
|--------|-------------|----------|
| Fold projection into Phase 5 | Add Operator K8s→Postgres reconcilers + DB tables for Environment/Plugin/Prompt/Artifact. CS reads only Postgres. Forwarder/Platform-API informer reads stay as-is (Phase 4 already shipped); flagged for Phase 5b. Single coherent Phase 5 delivering spec-v4 contract for the CS surface. | ✓ |
| Split: 5a projection → 5b CS+Obs | Insert Phase 5a (DB projection + Operator reconcilers) BEFORE Phase 5 (CS+Obs). Phase 5 reads cleanly from Postgres. Smaller blast radius but two discuss+plan+execute cycles. | |
| Keep informer reads | Accept spec drift; CS uses informer like Phase 4 forwarder. Defer DB projection. Diverges from spec v4. | |
| Other / different framing | User-defined. | |

**User's choice:** Fold projection into Phase 5 (Recommended).
**Notes:** Phase 5 is now: full §15.6 CS surface + §18.5 metrics on all 4 services + DB projection layer for Environment/Plugin/Prompt/Artifact + 3 new Operator projection reconcilers (Plugin/Prompt/Artifact; Environment reconciler extended in place). Forwarder + Platform-API informer→Postgres migration deferred to Phase 5b candidate (CONTEXT.md `<deferred>`). Status dual-write contract: Postgres authoritative, K8s subresource best-effort (spec v4 line 14).

---

## Authz pipeline shape + caches — sub-discussion C: cache layers

| Option | Description | Selected |
|--------|-------------|----------|
| Redis 60s for Env+Teams; direct DB for resolve+staleness | Env row cached 60s (`ach:env:<ns>/<name>`); TeamsResolver (Phase 4 D-17) reused 60s; §12.3 resolve hits Postgres every request; staleness hits Postgres every request. Matches SC#3 + CS-10 verbatim. 2 cached + 2 DB per success path. | ✓ |
| All-Postgres (no Redis for env) | Drop env caching; ~4 DB roundtrips per success path. Simpler invariant. | |
| Cache resolution too (~1s window) | Tiny TTL on §12.3 result for burst absorption. Diverges from SC#3 "Postgres on every request". | |
| Other | User-defined. | |

**User's choice:** Redis 60s for Env + Teams; direct DB for resolution + staleness (Recommended).
**Notes:** New `internal/contentservice/envcache/` package owns the Env Redis cache (D-07). KeyResolver + TeamsResolver reused verbatim from Phases 3/4 — no keystore changes in Phase 5.

---

## /metrics topology + shared metrics pkg

| Option | Description | Selected |
|--------|-------------|----------|
| Main chi mux + shared internal/metrics pkg | Each service exposes /metrics on main traffic listener. New `internal/metrics/` package owns Registry + collector factories + shared `litellm_unreachable_total`. Forwarder's existing internal/forwarder/metrics/ becomes thin shim. | ✓ |
| ctrl-rt metricsserver only | Keep controller-runtime metricsserver (:8443) on all 4. Register custom collectors via global registry. No chi /metrics route. Non-standard port. | |
| Per-service metrics package (no shared) | Each service owns its own metrics package; litellm_unreachable_total duplicated; risk of label drift. | |

**User's choice:** Main chi mux + shared internal/metrics pkg (Recommended).
**Notes:** Operator remains on ctrl-rt metricsserver (:8443) since it doesn't run chi — adds ACH-namespaced collectors to the ctrl-rt global registry. Other 3 services mount /metrics on main chi router. Histogram buckets per D-11 (DefBuckets for forwarder, tail-extended for content_service).

---

## Existing handler — wrap vs rewrite

| Option | Description | Selected |
|--------|-------------|----------|
| Rewrite serve() end-to-end | Replace handler.go with pipeline-style serve() + 6-gate authz + custom-serve stream. Discard ServeContent + max-age=300. Old handler_test.go rewritten. ~600-800 LoC handler + supporting files. | ✓ |
| Wrap existing serve() | Keep inner streamer; add outer middleware chain. Preserves §8 fixtures. Inner ServeContent still needs Range/IMS patching — partial rewrite anyway. | |
| Adapter handler + delete old | New handler in internal/contentservice/v2/; delete old in follow-up commit. Two-step cut-over. | |

**User's choice:** Rewrite serve() end-to-end (Recommended).
**Notes:** Files RETAINED: `paths.go` (ResolvePath logic), `content_type.go` (ContentTypeForFile). Files REWRITTEN: `handler.go`, `handler_test.go`. Files NEW: `pipeline.go`, `authz.go`, `envcache/cache.go`, `stream.go`, `errors.go`. File REMOVED: `k8s.go` (PromptContentTypeLookup obsolete — content_type comes from `prompts.content_type` column).

---

## Claude's Discretion

- `http.Server.WriteTimeout = 0` on CS traffic listener (D-Discretion in CONTEXT.md) — overrides Phase 3 D-03 timeout matrix for large artifact tarballs.
- HEAD method left as chi default 405 (CS-01 says HEAD MAY be supported but not contractual).
- Helm scrape annotations (D-12) chosen over ServiceMonitor as default; example ServiceMonitor under `examples/`.
- Histogram bucket extension for `content_service_request_duration_seconds` (D-11) tail to 60s for artifact tarballs.
- Operator runs migrations on startup via Phase 1 D-18 mechanism (new `000004_cs_projection.up.sql`).
- Status dual-write order: DB FIRST (load-bearing), K8s status best-effort SECOND (spec v4 line 14).
- Content Service ServiceAccount RBAC drop: remove `get/list/watch` on ACH CRDs (spec v4 line 21).
- One audit event per CS GET via Phase 3 OBS-01 hook pattern.
- chi explicit per-kind routes (no `{kind}` URL param) — Phase 1 §8 pattern.

## Deferred Ideas

- **Phase 5b candidate**: Forwarder + Platform API informer→Postgres migration. Phase 4 D-08 + Phase 3 D-20 informers stay; spec v4 §5.2 reversal applies to them too but moving in Phase 5 doubles blast radius.
- **§20 backlog**: HTTP Range / 206 Partial Content; ETag / If-None-Match conditional GET; HA Content Service multi-replica; HA Operator multi-replica.
- **Permanently dropped**: CS informer cache over ACH CRDs (spec v4); BIP DuplicateTarget reconciler (Phase 4 TODO.md §6); Range/IMS/INM honor in v1alpha1; pk_ server-side runtime-forbid toggle.
- **Engineer-pending verification debt**: `scripts/uat-g1.sh`, `scripts/uat-phase3.sh`, possibly `scripts/uat-phase4.sh` — NOT Phase 5 blockers but Phase 5 E2E extends the same harness.
