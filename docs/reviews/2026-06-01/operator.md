# Operator — Code Quality Review

> **Date:** 2026-06-01 · **Service:** Operator · **Packages:** `controller/ach + operator + orphan + snapshot + sources + connection` · **Lines:** ~9.9k
> **Findings:** 32 raw → **24 verified** · Read-only review, no code changed.
> **Method:** parallel reviewers per package → adversarial verification (grep/read to refute) → synthesis.
> **Axes:** dead code · complexity · over-engineering · optimization · duplication.
> Part of the [full codebase review](./README.md).

---

# ACH Operator — Code-Quality Review

## Dedup notes
- The **ConflictWithUIRow status-writer** appears as three separate findings (`policy_reconcilers` BIP+5-siblings, `content_reconcilers` writeConflictStatus triplication, and the environment branch). All collapse into **one root issue** (D1): a single canonical message constant + generic writer eliminates all 6 copies. Listed once under duplication.
- The `content_reconcilers` Reconcile-body triplication (D2) and the `content_reconcilers` SetupWithManager triplication (D5) are distinct seams (orchestration shell vs. controller wiring) of the same three files; kept separate but cross-referenced — a single generic driver subsumes D1's content slice, D2, D5, and the buildSourceSpec/extractAuthSecretRef pairing.

---

## dead_code (sev: low)

| # | Location | Problem | Fix |
|---|----------|---------|-----|
| DC1 | `internal/snapshot/snapshot.go:75-82,111-118,226-227` | `litellmUnreachableCount` counter + `LiteLLMUnreachableCount()` getter accumulated every failed tick but never read in prod (metric never wired) | Leave a TODO (documented Phase-5 deferral) or drop field+getter until the `litellm_unreachable_total` metric is actually wired |
| DC2 | `internal/operator/resync/runnable.go:217-234` | `Describe()`, `intervalOrDefault()`, `runnable` iface + assertion exist only for trivial-coverage tests; clamp already inline in `Start()` | Delete all four + their two tests; drop unused `fmt` import |
| DC3 | `internal/connection/snapshot.go:8-13` | `Snapshot.Generation` written at all 6 `Rebuild` sites, read nowhere | Remove field + the 6 `Generation: conn.Generation` literals |
| DC4 | `internal/controller/ach/external_ref_refresh.go:149-153` | `ExternalRefRefreshDeps.Log` set by all 3 callers, never read on any path (doc admits "reserved for future") | Drop field + the 3 `Log: logger` assignments |
| DC5 | `internal/controller/ach/environment_controller.go:81` | `EnvironmentReconciler.Log` populated in operator.go but never read (all paths use `log.FromContext`/passed `logger`) | Remove field + `Log:` wiring in operator.go:384 |
| DC6 | `internal/controller/ach/environment_controller.go:80` | `EnvironmentReconciler.Namespace` set-but-never-read (real scoping is at manager cache level) | Drop field + assignment; note: doc-comment is actually correct (cache-level scoping), so just remove the field |
| DC7 | `internal/sources/github/fetcher.go:269-275` | `setHTTPClientForTesting` never called (no test reaches the tarball HTTP leg) | Delete setter + field doc ref at fetcher.go:50 (same dead setter also in `bitbucket/fetcher.go:287`) |
| DC8 | `internal/controller/ach/plugin_controller.go:291-296,323-325` | Two doc-comments transposed during extraction: `writePluginConflictStatus`'s godoc is spliced above `reconcileDeletion`; orphan fragment above the real func | Swap the two comment blocks to their correct functions |

---

## over_engineering (sev: low)

| # | Location | Problem | Fix |
|---|----------|---------|-----|
| OE1 | `internal/controller/ach/conditions.go:149-250` | `setExternalRefCondition` is a single-internal-caller wrapper; its doc promises a per-condition-message extension point that no code uses (and actual per-condition sites call `apimeta.SetStatusCondition` directly) | Keep helper for its 2 internal callsites but delete the false "reconcilers call this directly" doc narrative |
| OE2 | `internal/controller/ach/pluginmarketplace_controller.go:598-615` | `classifyFetchErrorMarketplace` is a pure pass-through to `classifyFetchError`; its stated extras (`errUnsupportedPluginSource`, `OversizeError`) are handled elsewhere/already in the base | Delete wrapper; call `classifyFetchError` directly at line 360 |
| OE3 | `internal/connection/interface.go:5-9` | `CacheReader` interface has one impl (`*Cache`) and no test fake — no decoupling/seam value | Type the field+param as `*connection.Cache`, delete `interface.go` |

---

## duplication (sev: medium → low)

| # | Location | Problem | Fix | Sev |
|---|----------|---------|-----|-----|
| D1 | `backendidentitypolicy_controller.go:178` + `plugin/prompt/artifact_controller.go` (writeConflictStatus) + `litellmconnection:213` + `environment_controller.go:866` | ConflictWithUIRow status-writer + magic message `"projection row owned by UI; operator declines to overwrite"` duplicated across **6 reconcilers** (5 inline writers + literal in all 6) | One exported message const + generic `writeConflictWithUIRowStatus[T,PT](ctx,c,cr,condType,observedGen)` in `conditions.go` (mirror existing `retryStatusUpdate[T]`) | **med** |
| D2 | `plugin_controller.go:95-289` + `prompt`/`artifact` Reconcile bodies | Three content reconcilers' `Reconcile` skeletons ~95% identical (Get/finalizer/§10.3 gate/failure-switch/dual-write/status); only CR type, kind string, scope arg, SizeCap, projection fns differ | Generic `reconcileExternalRefCR[T client.Object](ctx,r,cr,cfg)` driver; per-kind shrinks to building `cfg` (~300 lines cut) | **med** |
| D3 | `internal/sources/{github,gitlab,bitbucket}/fetcher.go` (`extractToken`) | Auth-token extraction copy-pasted verbatim ×3, differs only by provider literal + error string (security-sensitive "never leak absent value" must stay in sync) | `sources.ExtractBearerToken(provider, ref, secret)` in `internal/sources`; 3 callers delegate | **med** |
| D4 | `internal/sources/{github,gitlab,bitbucket}/git_transport.go` (`fetchViaGit` + `resolvedTransport`) | `fetchViaGit` flow identical ×3 (only clone-URL build + error prefix differ); `resolvedTransport` byte-identical ×3 | `git.FetchViaProvider(ctx,cloneURL,ref,token,priorRev)` shared helper; `resolvedTransport` → one free func | **med** |
| D5 | `environment_controller.go:178-190` vs `302-313` | Projection-write + status-update + 5-min requeue tail duplicated between nil-Snapshotter back-compat branch and steady-state branch (only stale-requeue differs) | `persistAndRequeue(ctx,&env,available,logger,requeueAfter)` helper; both branches call it | med→**low** (back-compat branch slated for removal) |
| D6 | `plugin/prompt/artifact_controller.go` (writeConflictStatus funcs) | `writePluginConflictStatus`/`Prompt`/`Artifact` byte-for-byte identical but for CR type — *subsumed by D1* | Replace with the D1 generic writer | low |
| D7 | `plugin/prompt/artifact_controller.go` (SetupWithManager) | `SetupWithManager` triplicated except `For()` type + `Named()` literal (pattern shared by 6 of 7 reconcilers; litellm diverges with a `.Watches`) | `setupExternalRefController(mgr,obj,name,resync)` helper | low |
| D8 | `external_ref_refresh.go:467-524` (`buildSourceSpec`+`extractAuthSecretRef`) | Pair invoked back-to-back at 4 sites with byte-identical 7-arg tuples; 7-positional sig repeated 8× — sync hazard when a source type is added | Collapse to one helper returning `(SourceSpec, *AuthSecretRef)`, or pass a spec-subobject struct once (note: `buildSourceSpec` has 1 standalone test caller) | low |
| D9 | `internal/sources/{github,gitlab,s3,gcs,http}/fetcher.go` (status ladder) | HTTP-status→sentinel ladder (401/403→Unauthorized, 404→NotFound, ≥500→Unreachable, 4xx→UpstreamInvalid) reimplemented in **5** fetchers | `sources.ClassifyHTTPStatus(provider,op,status)`; each extracts its SDK-specific status int then delegates (http is looser fit) | low |
| D10 | `internal/operator/resync/runnable.go:101-197` | 7 per-Kind `sweep*` methods byte-identical but for `*List` type, `Channels` field, log string | Generic `sweepKind[L client.ObjectList]` (+ small Items accessor); `sweepAll` → 7 one-liners (~80→~20 lines) | low |
| D11 | `internal/sources/{github,gitlab,bitbucket,http}/...` (`drainAndClose`) | REL-04 drain-and-close helper redeclared in 4 subpackages (comments admit it's "duplicated from internal/litellm") | Export `sources.DrainAndClose(io.ReadCloser)` once; delete 4 copies (no new dep edge) | low |
| D12 | `internal/controller/ach/marketplace_extract.go:50-115` | gzip+tar stream-walk scaffolding duplicated between `extractMarketplaceJSON` + `verifyPluginManifest` (only match target + post-match action differ) | Shared `walkGzTar(r, predicate)` / `findTarEntry` iterator; keep per-entry caps in callbacks | low |
| D13 | `litellmconnection_controller.go:131-177` | 4 failure-snapshot blocks (`Snapshot{Ready:false,Reason:…}`) + paired `writeStatus` differ only by Reason (reason repeated twice/path) | Local `fail(reason,msg)` closure doing both `writeStatus` + `Cache.Rebuild` | low |

---

## Top 5 highest-leverage cleanups

1. **D2 — generic content-reconciler driver** (`reconcileExternalRefCR[T]`). Cuts ~300 duplicated lines across plugin/prompt/artifact and kills the three-way sync hazard on the §10.3 gate, failure-classification, and status contract. **Subsumes D6, D7, and the content slice of D1** — one driver, many wins. Highest gain/effort.
2. **D1 — `writeConflictWithUIRowStatus[T,PT]` + canonical message const.** One generic writer (mirroring the existing `retryStatusUpdate[T]`) erases 6 copies of a hand-synced magic string across all reconciler families. Trivial effort, repo-wide reach.
3. **D3 + D4 — shared git-provider helpers** (`ExtractBearerToken`, `FetchViaProvider`, `resolvedTransport`). Two medium dups in one subsystem; D3 is **security-sensitive** (token-leak wording drift), D4 removes byte-identical transport logic ×3. Bundle them — same files, same review.
4. **D10 — generic `sweepKind[L]` in resync.** 7 byte-identical methods → ~20 lines; makes "add a CR Kind" a one-liner instead of a copy-paste-and-hope. Self-contained file, zero blast radius.
5. **D11 + D9 — `sources.DrainAndClose` + `sources.ClassifyHTTPStatus`.** Two low-risk extractions into the already-imported `internal/sources` parent: collapses 4 drain copies and 5 status ladders with no new dep edges. Cheap, mechanical, and shrinks the per-provider boilerplate surface that D3/D4 also touch.

**Defer:** all `dead_code` items (DC1–DC8) and `over_engineering` (OE1–OE3) are genuine but low-yield — batch them into a single janitorial commit. DC1 stays as a TODO (intentional Phase-5 deferral), not a deletion.
