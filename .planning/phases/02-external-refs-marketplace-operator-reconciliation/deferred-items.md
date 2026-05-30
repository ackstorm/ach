# Phase 02 Deferred Items

Pre-existing issues discovered during plan execution but not fixed in-scope. Each item lists the discovering plan, the file, and the recommended follow-up.

- **02-08** — `internal/sources/http/fetcher_test.go` lines 329-334: struct-literal field alignment is non-canonical; `go fmt` re-aligns on every `make test-integration` run. Reverted in this plan to keep the commit focused. Fold into the next plan that legitimately edits this file (likely 02-02 follow-up or a Phase 2 cleanup pass).
