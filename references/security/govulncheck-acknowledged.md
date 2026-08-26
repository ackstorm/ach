# Acknowledged govulncheck residuals

**Date:** 2026-08-26
**Toolchain at acknowledgement:** Go 1.26.6 (`Dockerfile.devtools` + `go.mod` `toolchain` directive), `golang.org/x/net@v0.53.0`
**Scanner:** `govulncheck@v1.3.0` (pinned in `Dockerfile.devtools::GOVULNCHECK_VERSION`)
**Invocation:** `./scripts/dev.sh govulncheck ./...`

## Purpose

This file lists reachable advisories that have been reviewed and
explicitly acknowledged as accepted residual risk. The gate script
`scripts/govulncheck-gate.sh` enforces a **one-directional** rule: every
*reachable* advisory must have an acknowledged row below — a NEW
(unacknowledged) reachable advisory blocks `make security` and `make pre-push`.
A *cleared* advisory (an acknowledged row that no longer reaches) does NOT
block; the gate emits a NOTE asking you to prune the stale row to keep this
list honest. (With an empty list the behavior is identical to the old 1:1
match: zero reachable passes, any new one blocks.)

An empty list means the gate expects ZERO reachable advisories. Any
new reachable advisory must be:

1. Fixed by patching the dependency or stdlib, OR
2. Added below with a reviewer-approved justification.

## Acknowledged advisories (0)

_None._ The list is empty — the gate expects ZERO reachable advisories.

**Reachable count: 0.**

## History

- 2026-08-26: Bumped Go toolchain 1.26.5 → **1.26.6** (`go.mod` `toolchain` +
  `Dockerfile.devtools` pinned to `golang:1.26.6-bookworm`). Cleared seven
  stdlib advisories surfaced by a govulncheck DB refresh: `GO-2026-5026`,
  `GO-2026-5972`, `GO-2026-6088`, `GO-2026-6089`, `GO-2026-6090`,
  `GO-2026-6091` and `GO-2026-6218`. All fixed in go1.26.6; stdlib-only and
  independent of any first-party change. `govulncheck ./...` now reports 0
  reachable. Ack-list stays empty; gate expects 0.
- 2026-07-13: Bumped Go toolchain 1.26.4 → **1.26.5** (`go.mod` `toolchain` +
  `Dockerfile.devtools` pinned to `golang:1.26.5-bookworm`). Cleared
  `GO-2026-5856` (crypto/tls Encrypted Client Hello privacy leak), surfaced by
  a govulncheck DB refresh. Stdlib-only, independent of any first-party
  change. `govulncheck ./...` now reports 0 reachable. Ack-list stays empty;
  gate expects 0.
- 2026-06-03 (later): Bumped Go toolchain 1.26.3 → **1.26.4** (`go.mod`
  `toolchain` + `Dockerfile.devtools` pinned to `golang:1.26.4-bookworm`).
  This cleared all three stdlib advisories acked earlier the same day —
  `GO-2026-5037` (crypto/x509), `GO-2026-5038` (mime), `GO-2026-5039`
  (net/textproto). `govulncheck ./...` now reports 0 reachable. Ack-list
  emptied; gate expects 0.
- 2026-06-03: Acked three new stdlib HIGH advisories surfaced by a govulncheck
  DB refresh — `GO-2026-5037` (crypto/x509), `GO-2026-5038` (mime),
  `GO-2026-5039` (net/textproto). All fixed in go1.26.4; the toolchain was
  go1.26.3 and a bump (Dockerfile.devtools + `go.mod` `toolchain`) was tracked
  separately. These are stdlib-only, independent of any first-party change.
- 2026-05-21: Bumped Go toolchain to 1.26.3 (via `toolchain go1.26.3`
  in `go.mod`) and `golang.org/x/net` to `v0.53.0`. Cleared the only
  remaining residual (`GO-2026-4918` — HTTP/2 `SETTINGS_MAX_FRAME_SIZE`
  infinite loop in `x/net`).

## Verification

Re-run after any dependency bump or Go base change:

```bash
./scripts/dev.sh govulncheck ./... 2>&1 | grep -c '^Vulnerability'
# Expected: 0
```

If any advisory appears, fix it or extend this file with a
reviewer-approved row + justification before merge.

## Cross-references

- `scripts/govulncheck-gate.sh` — the wrapper that enforces this list
  one-directionally (new reachable advisories block; cleared rows only warn)
  as a pre-push gate (gate 13 in `scripts/pre-push-check.sh`).
- `Dockerfile.devtools` — pinned `GOVULNCHECK_VERSION` + base Go version.
- `go.mod` — `toolchain go1.26.6` directive pins CI's Go runtime to
  match the devtools image.
