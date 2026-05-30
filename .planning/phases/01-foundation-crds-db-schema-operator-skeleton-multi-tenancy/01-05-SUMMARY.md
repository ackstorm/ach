---
phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
plan: 05
subsystem: operator
tags: [controller-runtime, finalizer, litellm, pgx, slog, os.Remove, RemoveAll, apimeta]

# Dependency graph
requires:
  - phase: 01-02
    provides: "Six ach.ackstorm.ai/v1alpha1 kinds and the kubebuilder-stub reconcilers at internal/controller/ach/<kind>_controller.go that Plan 05 replaces"
  - phase: 01-03
    provides: "internal/db pgxpool.Pool surface (github.com/jackc/pgx/v5) used by the §6.5 ek_ drain loop"
  - phase: 01-04
    provides: "internal/credhash package — independent (no contact surface in Plan 05)"
provides:
  - "internal/litellm package: Client interface (DeleteAccessGroup, DeleteTag) + NoopClient + doc.go (D-11)"
  - "internal/controller/ach/finalizers.go: six finalizer-name constants (CRD-06)"
  - "Six reconcilers with real Phase 1 reconcile bodies replacing the kubebuilder placeholders"
  - "EnvironmentReconciler implementing Hub §6.5 deletion drain (DeleteAccessGroup → DeleteTag → ek_ drain → RemoveFinalizer) via the litellm.Client interface"
  - "External-reference reconcilers (Plugin, Prompt, Artifact, PluginMarketplace) implementing Hub §10.3 cached-file/subtree cleanup before finalizer removal (OP-12)"
  - "BackendIdentityPolicyReconciler with finalizer add/remove only (no PVC presence; Phase 4 layers real Synced=DuplicateTarget reconciliation on top)"
  - "Compile-time assertion var _ Client = (*NoopClient)(nil) — Phase 2 swap canary"
  - "Idempotent writeStatus helpers per kind using apimeta.SetStatusCondition (CRD-07 stubs)"
  - "W3-concrete §6.5 step-4 ek_ drain loop: cap=10 iterations, 100ms inter-iteration sleep, pgconn class 08/57 transient handling, cap-exhausted slog.Warn"
affects:
  - 01-06 (cmd/operator/main.go — must inject six reconciler dependencies: LiteLLM (NoopClient), DB (pgxpool.Pool), Namespace (watchNS), Log (logger), CacheRoot (ACH_CACHE_ROOT) on four external-ref kinds)
  - 01-09 (RBAC narrowing — controller-gen regenerated config/rbac/role.yaml with the same six-kind + /status + /finalizers verb set, no change versus Plan 02 output)
  - 01-11 (envtest CEL admission + finalizer assertions — exercises the AddFinalizer / RemoveFinalizer paths landed here)
  - 02-* (LiteLLM REST client swap — replaces NoopClient at the cmd/operator/main.go wiring point; reconciler code is unchanged thanks to the interface)
  - 02-* (External-ref refresh — layers fetch/materialize/rename loop on top of the finalizer skeleton; same reconciler files extended, no replacement)

# Tech tracking
tech-stack:
  added:
    - "github.com/go-logr/logr (promoted from indirect → direct via go mod tidy; was already transitively present)"
  patterns:
    - "Interface-as-contract for swappable dependencies — sister project ach_litellm/internal/connection/interface.go pattern lifted; reconciler Cache/LiteLLM field typed as interface, never concrete"
    - "Compile-time interface satisfaction assertion — `var _ Client = (*NoopClient)(nil)` at file end (sister noop_controller.go line 188 pattern)"
    - "§6.5 drain sequencing — DeleteAccessGroup BEFORE DeleteTag BEFORE drain BEFORE RemoveFinalizer; any return-on-err before RemoveFinalizer keeps the finalizer in place for retry"
    - "§10.3 external-ref cleanup sequencing — os.Remove (or os.RemoveAll for PluginMarketplace subtree) BEFORE RemoveFinalizer; IsNotExist tolerated (idempotent on a missing cached file)"
    - "Per-kind <kindLower>Finalizer constants in a shared internal/controller/ach/finalizers.go — single source of truth, no string-literal references in reconciler bodies"
    - "Idempotent writeStatus helper per reconciler — apimeta.SetStatusCondition (transition-time-preserving) + ObservedGeneration bump + r.Status().Update; CRD-07 closed-set reasons only"
    - "pgconn error class inspection for transient classification — classes 08 (connection exception) and 57 (operator intervention) return raw err for default exponential backoff; other classes wrap via fmt.Errorf for log visibility"
    - "Cap-exhausted slog.Warn continuation — bounded retry loops log loud at cap (here 10) and proceed rather than indefinitely block the K8s deletion"
    - "Multigroup controller path internal/controller/ach/ used uniformly across all six reconcilers and the shared finalizers.go (continues Plan 02's kubebuilder v4 deviation)"

key-files:
  created:
    - internal/litellm/client.go (Client interface with DeleteAccessGroup + DeleteTag)
    - internal/litellm/noop.go (NoopClient implementing Client + var _ Client = (*NoopClient)(nil) assertion)
    - internal/litellm/doc.go (package contract — Phase 1 → Phase 2 swap discipline)
    - internal/controller/ach/finalizers.go (six const finalizer names)
  modified:
    - internal/controller/ach/environment_controller.go (§6.5 drain — LiteLLM interface, DB pgxpool drain loop, finalizer add/remove, CRD-07 status stub)
    - internal/controller/ach/plugin_controller.go (§10.3 plugin/<name>.tar.gz cleanup + finalizer)
    - internal/controller/ach/prompt_controller.go (§10.3 prompt/<name> cleanup + finalizer)
    - internal/controller/ach/artifact_controller.go (§10.3 artifact/<name> + artifact/<name>.tar.gz cleanup + finalizer)
    - internal/controller/ach/pluginmarketplace_controller.go (§10.3 marketplace/<name>/ RemoveAll subtree + finalizer)
    - internal/controller/ach/backendidentitypolicy_controller.go (finalizer add/remove only; no PVC cleanup)
    - go.mod (go-logr/logr promoted indirect → direct)

key-decisions:
  - "Plan-text path `internal/controller/<kind>_controller.go` is the multigroup path `internal/controller/ach/<kind>_controller.go` — kubebuilder v4 + multigroup: true established by Plan 02. The new finalizers.go also lives at internal/controller/ach/finalizers.go to share package `ach`."
  - "EnvironmentReconciler.DB field is *pgxpool.Pool — nilable in Phase 1. drainEkRows() handles nil DB as a log+skip case so envtest/unit tests work without a real Postgres pool; Plan 06 injects the real pool from cmd/operator/main.go."
  - "ArtifactReconciler attempts BOTH object-scope (artifact/<name>) and directory-scope (artifact/<name>.tar.gz) cache paths on deletion. Reading spec.scope on deletion would require an extra CR read post-DeletionTimestamp; cheaper and idempotent to attempt both with IsNotExist tolerance. Phase 2's refresh loop will record the published path in status (Hub §10.3 step 5) so cleanup can become exact."
  - "BackendIdentityPolicyReconciler emits no status condition in Phase 1 — §6.6 only admits Synced=DuplicateTarget for this kind (CRD-07 closed set), which is a Phase 4 outcome (OP-14/OP-16). Writing a stub Initializing reason here would conflict with the closed set."
  - "pgconn error class inspection — UPDATE/SELECT errors with SQLSTATE class 08 (connection exception) or 57 (operator intervention) return the raw error for controller-runtime's default exponential-backoff requeue; all other classes wrap via fmt.Errorf so the operator log carries the drain step name plus the wrapped error. Both paths requeue (finalizer stays); the distinction is operator visibility, not retry semantics."
  - "Cap-exhausted drain logs slog.Warn and proceeds to RemoveFinalizer — Phase 3+ pathological ek_ INSERT streams could otherwise block K8s deletion indefinitely. Phase 1 invariant: the cap-exhausted path is unreachable because zero ek_ rows exist; the slog.Warn is forward-looking contract."
  - "writeStatus is duplicated per reconciler rather than lifted to a shared helper — Go generics for a Conditions-bearing interface would be cleaner, but the per-kind status type variation (Environment vs ExternalRef-embedding kinds vs BackendIdentityPolicy) makes the parameterized signature awkward for Phase 1 stubs. Phase 2's real reconcile bodies will revisit this when each kind's writeStatus diverges with kind-specific status fields."
  - "Plan-text RestClient/net/http references in doc comments were rephrased after the Task 1 grep gate (`grep -rE 'net/http|http\\.Client|RestClient|\\.Do\\('` returning matches in package comments). Comments now say 'real implementation' / 'wire traffic' so the canary returns clean exit 1 (no matches). Substantive content unchanged."

patterns-established:
  - "litellm.Client interface — Phase 2 RestClient swap is a one-line edit in cmd/operator/main.go; reconciler code unchanged because every field is typed as the interface"
  - "Reconcile body shape: Fetch → DeletionPath(drain+RemoveFinalizer) → AddFinalizerPath → SteadyStateStatusStub → return Result{} (no RequeueAfter per PATTERNS.md REL-02 invariant)"
  - "§6.5 sequencing as code: any err before RemoveFinalizer keeps finalizer for retry; the explicit ordering plus return-on-err contract is the threat-model mitigation for T-05-02"
  - "External-ref §10.3 cleanup uniformity: os.Remove(or os.RemoveAll) + errors.Is(err, fs.ErrNotExist) tolerance + RemoveFinalizer; identical idiom across four kinds with only the path differing"
  - "Per-reconciler Setup using ctrl.NewControllerManagedBy(mgr).For(...).Named('ach-<kind>').Complete(r) — Phase 2 layers Watches/WatchesRawSource on top without renaming"
  - "writeStatus helper signature (ctx, *CR, condType, status, reason, message) error — uniform across all five reconcilers that write status; one signature per kind because of the *CR type variation"

requirements-completed: [OP-02, OP-12, CRD-06, CRD-07]
# Plan frontmatter listed OP-04 in requirements_addressed but OP-04
# is about rename(2) failure surfacing on the external-ref refresh
# loop — that loop lives in Phase 2 (OP-03). This plan does not
# touch rename(2) handling. OP-04 stays open for Phase 2.

# Metrics
duration: ~10min
completed: 2026-05-15
---

# Phase 1 Plan 5: Operator Reconcilers — Finalizers + §6.5 Drain + §10.3 Cleanup Summary

**Six reconcilers (Environment, Plugin, PluginMarketplace, Artifact, Prompt, BackendIdentityPolicy) wired with CRD-06 finalizer add/remove + the Hub §6.5 Environment deletion drain (DeleteAccessGroup → DeleteTag → ek_ drain → RemoveFinalizer) behind an `internal/litellm.Client` interface (Phase 1 NoopClient) + the Hub §10.3 cached-file/subtree cleanup before finalizer removal on the four external-reference CRDs.**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-05-15T15:53:00Z
- **Completed:** 2026-05-15T16:03:00Z
- **Tasks:** 4 (plus 1 chore commit for go.mod tidy)
- **Files modified:** 4 created + 6 modified + go.mod = 11

## Accomplishments

- `internal/litellm` package shipped with the D-11 interface contract and a working NoopClient — Phase 2's REST client swap is a one-line wiring edit, not a reconciler edit. Compile-time assertion `var _ Client = (*NoopClient)(nil)` is the canary.
- Six finalizer-name constants lifted to a shared `internal/controller/ach/finalizers.go`; no inline string literals in any reconciler body. The `<kindPlural>.ach.ackstorm.ai/finalizer` form is exactly CRD-06.
- `EnvironmentReconciler` implements the Hub §6.5 drain in textual order — `r.LiteLLM.DeleteAccessGroup` (line 118) → `r.LiteLLM.DeleteTag` (122) → `r.drainEkRows()` → `controllerutil.RemoveFinalizer` (133). Any error before RemoveFinalizer keeps the finalizer in place via controller-runtime's exponential backoff (T-05-02 mitigation).
- §6.5 step-4 `ek_` drain loop is real Phase 1 code (D-12) with the W3-concrete spec: 10-iteration cap, 100ms inter-iteration `time.Sleep`, `pgconn.PgError` class 08/57 transient handling, cap-exhausted `slog.Warn` continuation. Trivially exits on first iteration in Phase 1 (zero rows extant); the loop body is exercised the moment Plan 06 wires the real `*pgxpool.Pool`.
- Four external-ref reconcilers (Plugin, Prompt, Artifact, PluginMarketplace) implement Hub §10.3 cleanup: `os.Remove` (or `os.RemoveAll` for the PluginMarketplace subtree per OP-12) BEFORE `RemoveFinalizer`. `errors.Is(err, fs.ErrNotExist)` tolerated on every path — the operations are idempotent on a missing cached file.
- ArtifactReconciler attempts both object-scope (`artifact/<name>`) and directory-scope (`artifact/<name>.tar.gz`) paths on deletion; the spec is the source of truth but reading it post-DeletionTimestamp would cost an extra Get — attempting both paths is cheaper and equally correct.
- `BackendIdentityPolicyReconciler` registers/removes the finalizer with no PVC cleanup body; the kind has no §10.3 cached form. Phase 4 layers real `Synced=DuplicateTarget` reconciliation on top without a CRD migration.
- Every reconciler ships a kind-specific `writeStatus` idempotent helper using `apimeta.SetStatusCondition` + `r.Status().Update`. Phase 1 emits only CRD-07-closed-set "Initializing" stubs (or none, for BackendIdentityPolicy); Phase 2 flips them with real probe outcomes.

## Task Commits

Each task was committed atomically:

1. **Task 1: internal/litellm Client interface + NoopClient + doc** — `e8ad3d3` (feat)
2. **Task 2: shared finalizer-name constants** — `77beabb` (feat)
3. **Task 3: EnvironmentReconciler §6.5 drain** — `440e703` (feat)
4. **Task 4: five external-ref reconcilers + BackendIdentityPolicy** — `83f199b` (feat)

**Tidy commit:** `2944b23` (chore: go-logr/logr promoted indirect → direct)

## Files Created

- **internal/litellm/client.go** — Client interface (DeleteAccessGroup, DeleteTag); two methods, both ctx-accepting; Apache-2.0 boilerplate; stdlib `context` import only.
- **internal/litellm/noop.go** — NoopClient struct (Log logr.Logger field) + NewNoopClient constructor + two methods that emit `Log.Info("stub: would …", "name", name)` + return nil; `var _ Client = (*NoopClient)(nil)` compile-time assertion at file end.
- **internal/litellm/doc.go** — package doc documenting the Phase 1 / Phase 2 swap discipline, the D-11 / §6.5 step-2 / step-3 mapping, and the no-REST-plumbing Phase 1 invariant.
- **internal/controller/ach/finalizers.go** — six `<kindLower>Finalizer` constants in a single `const (...)` block. The grep gate `grep -c "ach.ackstorm.ai/finalizer" internal/controller/ach/finalizers.go` returns exactly 6.

## Files Modified

- **internal/controller/ach/environment_controller.go** — kubebuilder-stub reconciler replaced. New struct fields: `LiteLLM litellm.Client`, `Namespace string`, `Log logr.Logger`, `DB *pgxpool.Pool`. Reconcile body implements §6.5 drain in textual order; the `drainEkRows` private method runs the 10-iter loop with classifyDrainErr's class-08/57 inspection; `writeStatus` writes CRD-07 `AccessGroupSynced=Unknown` stub. RBAC markers (resources / /status / /finalizers) preserved from Plan 02 generation.
- **internal/controller/ach/plugin_controller.go** — finalizer add/remove + `os.Remove(filepath.Join(r.CacheRoot, "plugin", cr.Name+".tar.gz"))` on deletion + IsNotExist tolerance + writeStatus stub.
- **internal/controller/ach/prompt_controller.go** — same pattern with path `prompt/<name>` (raw bytes, no extension).
- **internal/controller/ach/artifact_controller.go** — two `os.Remove` calls on deletion (object-scope + directory-scope), both with IsNotExist tolerance + writeStatus stub.
- **internal/controller/ach/pluginmarketplace_controller.go** — finalizer add/remove + `os.RemoveAll(filepath.Join(r.CacheRoot, "marketplace", cr.Name))` for the entire subtree per OP-12 + writeStatus stub.
- **internal/controller/ach/backendidentitypolicy_controller.go** — finalizer add/remove only; no `os.Remove`/`os.RemoveAll` (no PVC form for this kind). No status write (§6.6 only admits `Synced=DuplicateTarget` here, which is a Phase 4 outcome).
- **go.mod** — `github.com/go-logr/logr` promoted indirect → direct (`go mod tidy` consequence of new direct uses in `internal/litellm/noop.go` and reconciler `Log` fields).

## Reconciler Struct Fields (for Plan 06's main.go injection)

Per the plan's `<output>` instruction:

| Reconciler                       | Fields beyond `client.Client` embed                                                  |
| -------------------------------- | ------------------------------------------------------------------------------------ |
| `EnvironmentReconciler`          | `Scheme *runtime.Scheme`, `LiteLLM litellm.Client`, `Namespace string`, `Log logr.Logger`, `DB *pgxpool.Pool` |
| `PluginReconciler`               | `Scheme`, `Namespace string`, `Log logr.Logger`, `CacheRoot string`                  |
| `PromptReconciler`               | `Scheme`, `Namespace string`, `Log logr.Logger`, `CacheRoot string`                  |
| `ArtifactReconciler`             | `Scheme`, `Namespace string`, `Log logr.Logger`, `CacheRoot string`                  |
| `PluginMarketplaceReconciler`    | `Scheme`, `Namespace string`, `Log logr.Logger`, `CacheRoot string`                  |
| `BackendIdentityPolicyReconciler`| `Scheme`, `Namespace string`, `Log logr.Logger` (no `CacheRoot`, no `LiteLLM`, no `DB`) |

Plan 06's `cmd/operator/main.go` constructs each reconciler with the fields above, sourcing `LiteLLM` from `litellm.NewNoopClient(...)`, `DB` from `db.Open(ctx, ACH_DB_URL)`, `CacheRoot` from `os.Getenv("ACH_CACHE_ROOT")` defaulted via the Plan 07 `cachefs.EnsureLayout` path, `Namespace` from `WATCH_NAMESPACE`, and `Log` from `ctrl.Log.WithName("controller").WithName("<Kind>")`.

## Decisions Made

See `key-decisions` in frontmatter. Brief recap:

1. The plan's literal path `internal/controller/<kind>_controller.go` was carried through Plan 02's multigroup deviation → `internal/controller/ach/<kind>_controller.go`. `finalizers.go` lives there too, package `ach`.
2. `EnvironmentReconciler.DB *pgxpool.Pool` is nilable so envtest/unit tests work without a real Postgres pool; `drainEkRows` skips when nil.
3. Artifact deletion attempts both cache paths (object + directory) rather than reading spec.scope post-DeletionTimestamp.
4. BackendIdentityPolicy emits no status condition in Phase 1 — §6.6 only admits `Synced=DuplicateTarget` (Phase 4 outcome); writing a stub would violate the CRD-07 closed set.
5. `pgconn` error class 08/57 → raw error return (default exponential backoff); other classes → `fmt.Errorf` wrap for log visibility. Both paths still requeue.
6. Cap-exhausted drain logs `slog.Warn` and proceeds to RemoveFinalizer (Phase 3+ forward-looking contract; unreachable in Phase 1).
7. `writeStatus` is duplicated per reconciler — Go generics over a Conditions-bearing interface would be cleaner but the per-kind status type variation makes the signature awkward for Phase 1 stubs. Phase 2 revisits.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Multigroup controller path discovered by Plan 02 carried forward**

- **Found during:** Task 2 (creating `finalizers.go`)
- **Issue:** Plan 01-05's `files_modified` declares paths like `internal/controller/finalizers.go` and `internal/controller/environment_controller.go`. The actual paths from Plan 02 are `internal/controller/ach/` (multigroup convention). Following the plan literally would break the existing test wiring and the `+kubebuilder:scaffold:imports` chain.
- **Fix:** Wrote `finalizers.go` and all six reconciler updates under `internal/controller/ach/`, package `ach`. Identical content to plan intent; only the directory path differs.
- **Files modified:** all in `internal/controller/ach/`.
- **Verification:** `./scripts/dev.sh go build ./...` exits 0 with the multigroup paths; the same plan text against the plan's literal path would have failed compilation because the existing test suite imports `package ach`.
- **Committed in:** `77beabb` (Task 2) — set the precedent; Tasks 3-4 followed.

**2. [Rule 3 - Blocking] Task 1 grep gate required removing literal `RestClient` / `net/http` references from doc comments**

- **Found during:** Task 1 verify (the acceptance criterion `grep -rE "net/http|http\.Client|RestClient|\.Do\(" internal/litellm/` returning no matches)
- **Issue:** Initial doc.go / client.go / noop.go drafts named the Phase 2 swap target explicitly as `*RestClient` for clarity. The grep gate intends to assert no REST plumbing has leaked from Phase 2 into Phase 1, but the regex matches the textual identifier in comments too.
- **Fix:** Rephrased the doc comments to "real implementation" / "Phase 2 concrete type" / "wire traffic". Substantive content unchanged — Phase 1 contract still documented.
- **Files modified:** `internal/litellm/{doc.go,client.go,noop.go}`.
- **Verification:** Re-running the grep returns exit 1 (no matches); `grep` for the structural acceptance markers (`type Client interface`, `type NoopClient struct`, `var _ Client = (*NoopClient)(nil)`) still pass.
- **Committed in:** `e8ad3d3` (Task 1) — fix folded into the same task commit before pushing.

**3. [Rule 2 - Missing critical] `classifyDrainErr` helper added to map pgconn errors per W3 spec**

- **Found during:** Task 3 (writing `drainEkRows`)
- **Issue:** Plan text says "On any transient DB error (`*pgconn.PgError` with `Code` class != `08` and != `57`), return that error from `Reconcile` so controller-runtime requeues" — but the negation makes the class-08/57 case ambiguous (they ARE the transient classes; the negation logic is inverted in the plan prose). The threat-model entry T-05-05 confirms intent: the loop must not infinitely retry on operator-action-required errors; transient classes get raw return, non-transient classes get wrapped return for log visibility.
- **Fix:** Authored `classifyDrainErr(label, err)` per the intent: classes 08 (connection exception) and 57 (operator intervention) → return raw error; all other classes → `fmt.Errorf("%s: %w", label, err)`. Both paths still requeue via controller-runtime; the distinction is operator-log visibility.
- **Files modified:** `internal/controller/ach/environment_controller.go`.
- **Verification:** `go vet` clean; build clean. The Plan 11 verifier will exercise both branches via testcontainers-go Postgres if envtest gains DB integration there.
- **Committed in:** `440e703` (Task 3).

**4. [Rule 3 - Blocking] go.mod tidied after `go-logr/logr` became a direct dependency**

- **Found during:** Plan-level `./scripts/dev.sh make manifests generate fmt vet`
- **Issue:** Adding `logr.Logger` fields to NoopClient and the six reconcilers promoted `github.com/go-logr/logr` from indirect to direct. `go vet` (called by `make vet` which runs `go fmt + go vet`) updated `go.mod` automatically; leaving the change uncommitted would leak into the next plan's diff.
- **Fix:** Committed the one-line `go.mod` change as a `chore(01-05)`.
- **Files modified:** `go.mod`.
- **Verification:** `git diff` clean after commit.
- **Committed in:** `2944b23` (chore).

---

**Total deviations:** 4 auto-fixed (1 missing-critical W3 transient classification, 3 blocking — multigroup path / grep-gate doc rephrase / go.mod tidy).
**Impact on plan:** All four were verbatim of plan intent; the literal-path / literal-identifier issues are mechanical Plan 02 / grep-canary consequences, and the W3 transient classification was the W3 revision iteration's contract expressed in code.

## Issues Encountered

None beyond the deviations enumerated above. The §6.5 drain sequencing was already crisp in the plan; the §10.3 cleanup paths were already crisp in the spec text; the kubebuilder-stub reconcilers from Plan 02 replaced cleanly under the multigroup path.

## Self-Check: PASSED

- File `internal/litellm/client.go` exists with `type Client interface` (verified by `grep -c`).
- File `internal/litellm/noop.go` exists with `type NoopClient struct` and `var _ Client = (*NoopClient)(nil)` (both verified).
- File `internal/litellm/doc.go` exists with package doc.
- File `internal/controller/ach/finalizers.go` exists with `grep -c "ach.ackstorm.ai/finalizer"` == 6.
- All six reconciler files at `internal/controller/ach/<kind>_controller.go` exist and compile (`./scripts/dev.sh go build ./...` exit 0).
- `./scripts/dev.sh go vet ./...` exit 0.
- `./scripts/dev.sh make manifests generate fmt vet` exit 0 (controller-gen regenerated config/crd and zz_generated.deepcopy.go — no diff in the regenerated YAML because the kind structs were untouched in this plan).
- Commits `e8ad3d3`, `77beabb`, `440e703`, `83f199b`, `2944b23` all present in `git log --oneline`.

## User Setup Required

None — Plan 01-05 introduces no new environment variables or external services. Plan 06's main.go wiring will inject `LiteLLM` / `DB` / `CacheRoot` / `Namespace` / `Log` from existing env knobs (`WATCH_NAMESPACE`, `ACH_DB_URL`, `ACH_CACHE_ROOT`) that Plans 01-01..04+07 already established.

## Next Phase Readiness

- **01-06 (operator main.go split):** Six reconcilers ready to be instantiated. Plan 06 reads the table above and wires each reconciler with the exact field set; the `LiteLLM` injection point is `litellm.NewNoopClient(ctrl.Log.WithName("litellm"))`. Phase 2 swaps that one line for the real REST client.
- **01-08 / 01-09 (manifests / RBAC):** No change versus Plan 02's generated `config/rbac/role.yaml` — the new RBAC markers are identical (resources + /status + /finalizers, kept verbatim from the kubebuilder stubs).
- **01-11 (envtest CEL admission asserts):** AddFinalizer / RemoveFinalizer paths are now exercisable. The verifier can assert that creating an Environment lands the finalizer, deleting it removes the finalizer after the drain stub runs, and the NoopClient was called twice (one access-group, one tag). The Plan 02 placeholder tests still pass with the new reconciler shapes because they only exercise the AddFinalizer path with zero-value `LiteLLM` / `DB` fields (no panic — the deletion path is the only consumer of those, and the placeholder tests never enter it).

---

*Phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy*
*Plan: 01-05*
*Completed: 2026-05-15*
