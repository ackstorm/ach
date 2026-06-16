# ach-mcp-echo — JWT-validating MCP echo backend

A runnable reference + e2e fixture for ACH's `/mcp` JWT trust path.

Boots an MCP server (Streamable-HTTP) on `MOCK_BIND_ADDRESS` (default
`:9090`) exposing a single tool, `echo`, which:

1. Receives a `text: string` argument.
2. Verifies the incoming `Authorization: Bearer <jwt>` against the
   Forwarder's `/.well-known/jwks.json` (Ed25519 / EdDSA).
3. Validates the standard claim set (`iss`, `aud`, `exp`) against
   environment-configured expectations.
4. Returns the echoed text plus the verified claims as JSON.

> **JWT claims.** Backends verify `iss` / `aud` / `exp` only — `nbf` is
> **absent** (intentional; `exp` is the sole time bound), so do not require it.
> `sub` is the bare `<owner-email>` (no namespace prefix); the additive `email`
> claim mirrors `sub`.

## Environment

| Var | Required | Default | Purpose |
|---|---|---|---|
| `MOCK_BIND_ADDRESS` | no | `:9090` | HTTP bind address |
| `ACH_JWKS_URL` | yes | — | Forwarder JWKS URL (e.g. `http://ach-forwarder.ach-system.svc/.well-known/jwks.json`) |
| `ACH_EXPECTED_ISS` | yes | — | Required `iss` claim (= `ACH_BASE_URL` configured on the Forwarder) |
| `ACH_EXPECTED_AUD` | yes | — | Comma-separated list of accepted `aud` claims (e.g. `mcp:demo-mcp-echo`) |
| `ACH_JWKS_REFRESH` | no | `5m` | Min interval between background JWKS refreshes |

## Endpoints

| Path | Auth | Purpose |
|---|---|---|
| `/` | JWT | MCP Streamable-HTTP endpoint (echo tool lives here) |
| `/healthz` | none | Liveness/readiness |
| `/__capture/last` | none | Last request snapshot (test introspection) |
| `/__capture/reset` | none | Reset capture buffer (test introspection) |

## Standalone run

```bash
ACH_JWKS_URL=https://forwarder.example/.well-known/jwks.json \
ACH_EXPECTED_ISS=https://hub.example \
ACH_EXPECTED_AUD=mcp:demo-mcp-echo \
go run ./test/e2e/mcp-echo
```

## E2E

Built into `ach-mcp-echo:e2e` by `make e2e-mcp-echo-build`, deployed by
the Helm chart when `testMocks.mcpEcho.enabled=true`, exercised by
`TestPhase4JWTValidate` in `test/e2e/phase4_jwt_validate_test.go`.
