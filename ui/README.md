# ACH console (React)

Imported as-is from `ackstorm/alitellm-auth` `src/ui` @ `4e38245` (Apache-2.0) — see `references/upstream-sync.md`.

- `make ui-build` — builds into `internal/platformapi/console/dist` (embedded by `go build`; `make build-image` runs it in the Dockerfile `ui` stage).
- `make test-ui` — `tsc` type-check + vitest.
- Dev: `npm --prefix ui run dev` proxies `/platform`, `/openwork`, `/api/den` to a platform-api on `localhost:8080`.
