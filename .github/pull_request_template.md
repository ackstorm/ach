## Summary

<!-- What does this PR change, and why? Link any issue (e.g. #34). -->

## Testing

<!-- How was this verified? E.g. `make test-unit`, `make test-envtest`,
     `make e2e-full`, manual steps. Paste relevant output if useful. -->

## New CR kind? (kind-lifecycle checklist — see `references/adding-a-cr-kind.md`)

<!-- Skip this section if no new `*.ach.ackstorm.ai` CR kind is added.
     A new kind MUST satisfy every applicable row IN THIS PR — no second-class
     kinds. Tick what applies to the kind's archetype; mark N/A rows explicitly. -->

- [ ] **CRD + CEL** — `api/v1alpha1/<kind>_types.go` + validation/CEL markers; `make gen-code` + `make gen-crd-ref-docs` run
- [ ] **Projection table + `with_tx_notify`** — operator-only writer, `NOTIFY ach_<kind>_changed` in-tx
- [ ] **Status conditions** — incl. `Synced=False reason=NameConflict` on duplicate identity (G15); `origin`/`locked` if UI-writable
- [ ] **Reconciler + finalizer** — `internal/controller/ach/<kind>_controller.go`; finalizer cleans projection + delete `NOTIFY`
- [ ] **Content pipeline (F1)** — fetch → Stage-2 gate → cache → content-service serve (narrow-at-fetch for objects; whole-repo for discovery)
- [ ] **Admin inventory** — kind appears in platform-api admin object inventory
- [ ] **Admin refresh** — admin-triggered re-fetch/re-check path
- [ ] **Hydrate** — projected by `ach-cli env hydrate`
- [ ] **Metrics** — reconcile + serve Prometheus metrics
- [ ] **Docs / spec** — `CLAUDE.md` + `references/`/`docs/` + frozen spec updated in this PR
- [ ] **Tests** — unit + envtest + e2e fixtures where the kind participates
