# Acknowledged govulncheck residuals

**Date:** 2026-05-21
**Toolchain at acknowledgement:** Go 1.26.3 (`Dockerfile.devtools` + `go.mod` `toolchain` directive), `golang.org/x/net@v0.53.0`
**Scanner:** `govulncheck@v1.3.0` (pinned in `Dockerfile.devtools::GOVULNCHECK_VERSION`)
**Invocation:** `./scripts/dev.sh govulncheck ./...`

## Purpose

This file lists reachable advisories that have been reviewed and
explicitly acknowledged as accepted residual risk. The gate script
`scripts/govulncheck-gate.sh` enforces a 1:1 match between actual
reachable advisories and the rows below — any deviation (new advisory
appearing or an acknowledged advisory clearing) blocks `make security`
and `make pre-push`.

An empty list means the gate expects ZERO reachable advisories. Any
new reachable advisory must be:

1. Fixed by patching the dependency or stdlib, OR
2. Added below with a reviewer-approved justification.

## Acknowledged advisories (3)

| # | OSV ID | CVE | Module | Symbol the operator touches | Fixed in | Justification |
|---|--------|-----|--------|------------------------------|----------|---------------|
| 1 | GO-2026-5037 | — | crypto/x509 (stdlib) | TLS cert verification on outbound HTTPS (source fetcher / LiteLLM / k8s client) | go1.26.4 | Inefficient candidate-hostname parsing in `crypto/x509`. Stdlib-only; no first-party fix possible until the toolchain is bumped to go1.26.4. Quadratic-parse DoS requires attacker-controlled cert hostnames on a path ACH initiates — low exposure. Toolchain bump tracked separately. |
| 2 | GO-2026-5038 | — | mime (stdlib) | `mime.WordDecoder.DecodeHeader` via controller-runtime client HTTP header decode | go1.26.4 | Quadratic complexity in `WordDecoder.DecodeHeader`. Stdlib-only; reachable through `client.Get` header parsing. Fixed in go1.26.4 — bump tracked separately. |
| 3 | GO-2026-5039 | — | net/textproto (stdlib) | `textproto.Reader.ReadMIMEHeader` via `httpclient.DecodeServerError` (`io.ReadAll`) | go1.26.4 | Arbitrary inputs included in errors without escaping in `net/textproto`. Stdlib-only error-formatting issue on the CLI HTTP error path. Fixed in go1.26.4 — bump tracked separately. |

**Reachable count: 3.**

## History

- 2026-06-03: Acked three new stdlib HIGH advisories surfaced by a govulncheck
  DB refresh — `GO-2026-5037` (crypto/x509), `GO-2026-5038` (mime),
  `GO-2026-5039` (net/textproto). All fixed in go1.26.4; the toolchain is
  go1.26.3 and a bump (Dockerfile.devtools + `go.mod` `toolchain`) is tracked
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
  1:1 as a pre-push gate (gate 13 in `scripts/pre-push-check.sh`).
- `Dockerfile.devtools` — pinned `GOVULNCHECK_VERSION` + base Go version.
- `go.mod` — `toolchain go1.26.3` directive pins CI's Go runtime to
  match the devtools image.
