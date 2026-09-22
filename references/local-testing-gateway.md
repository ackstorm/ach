# Local Unified testing, SSO login & Gateway Reference

This document outlines the local unifed gateway architecture and testing procedures on Kind for the Agent Capability Hub (ACH) platform.

---

## 1. Local Unified Gateway Architecture

Routing now lives in **two layers**. Production routing is owned by the
in-binary **`ach-gateway`** pod (`ach gateway` mode, `internal/gateway`): a
dumb reverse proxy that fronts every production-real surface
(`/platform /content /v1 /v2 /gemini /mcp /a2a /.well-known`) behind one
in-cluster Service. In prod the public Ingress targets `ach-gateway`
directly — there is no nginx.

In local/e2e testing on Kind, the **`ach-local-gateway`** nginx pod is now
an **e2e-only shim**, not the primary router. Kind binds one host port, so
the shim preserves the single `ach.e2e.local:8080` origin the SSO cookie
round-trip and metrics-scrape harness depend on. It serves the two dev
**kludges** locally (`/dex`, `/metrics/<svc>`) and **falls through
everything else to `ach-gateway`** — so e2e traffic exercises the real
gateway exactly as prod will.

```
                          host  http://ach.e2e.local:8080
                                        │  (NodePort 30080)
                              ┌──────────▼──────────┐
                              │  ach-local-gateway  │  e2e-only shim
                              │     (Nginx Pod)     │  (stands in for the
                              └──────────┬──────────┘   prod Ingress)
                  ┌──────────────────────┼──────────────────────┐
       /dex/ (kludge)        /metrics/<svc> (kludge)   everything else
        ▼                     ▼                          ▼
   ┌─────────┐           ┌──────────────┐         ┌────────────────┐
   │   Dex   │           │ per-svc      │         │   ach-gateway  │  prod router
   │ :5556   │           │ /metrics     │         │ (ach gateway)  │
   └─────────┘           └──────────────┘         └───────┬────────┘
                                          ┌───────────────┼───────────────┐
                          ▼ (/platform)   ▼ (/content)                    ▼ (/v1,/v2,/gemini,/mcp,/a2a,/.well-known)
                ┌────────────────────┐ ┌────────────────────┐ ┌────────────────────┐
                │  ach-platform-api  │ │  content-service   │ │   ach-forwarder    │
                │   (Service: 80)    │ │  (Service: 8082)   │ │   (Service: 80)    │
                └────────────────────┘ └────────────────────┘ └────────────────────┘
```

Reachable under the single localhost port (`8080`):
* **Platform API:** `http://ach.e2e.local:8080/platform/` — shim → `ach-gateway` → platform-api
* **Content Service:** `http://ach.e2e.local:8080/content/` — shim → `ach-gateway` → content-service
* **LLM Forwarder:** `http://ach.e2e.local:8080/{v1,v2,gemini,mcp,a2a}/`, JWKS at `/.well-known/` — shim → `ach-gateway` → forwarder
* **SSO (Dex):** `http://ach.e2e.local:8080/dex/` — **DEV KLUDGE, served by the shim, never by `ach-gateway`.** Prod reaches Dex directly via `ACH_DEX_ISSUER_URL` (e.g. `https://auth.ackstorm.ai`) with no gateway involvement.
* **Per-service metrics:** `http://ach.e2e.local:8080/metrics/{forwarder,content,platform,operator}` — **DEV KLUDGE, served by the shim.** Distinct routes because a bare `/metrics` can't disambiguate four services. The e2e harness exports these as `ACH_{FORWARDER,CONTENT,PLATFORM,OPERATOR}_METRICS_URL`; `/metrics/operator` is backed by the `ach-operator-metrics` Service. **`ach-gateway` has NO `/metrics` route by design** — keeping metrics off the prod router means the prod Ingress physically cannot leak them.
* **Shim health:** `http://ach.e2e.local:8080/healthz` returns `200 ok` directly from nginx (no upstream); it backs the shim pod's probes. `ach-gateway` serves its own local `/healthz` for its pod probes.

---

## 2. Deploying & Starting the Gateway

### Deployment Manifest
The gateway is packaged in `test/e2e/cluster/03-test-backends/ach-local-gateway.yaml`.
The cluster hydration script (`scripts/cluster.sh`) automatically deploys and rolls out this gateway as part of the `hydrate_all` loop.

To apply or update the gateway manually:
```bash
kubectl apply -f test/e2e/cluster/03-test-backends/ach-local-gateway.yaml
```

### Reaching the Gateway on `ach.e2e.local:8080`

> **One origin, two sides.** `ach.e2e.local` is the single ACH origin for the
> e2e cluster: the devtools container maps it to `127.0.0.1` (`scripts/dev.sh`
> `--add-host`), and inside the cluster CoreDNS rewrites it to the
> `ach-local-gateway` Service on port 8080 (`coredns-rewrite.yaml`). That is
> what lets platform-api advertise hydrate download URLs that agent pods can
> actually fetch. **On the host itself** (outside `./scripts/dev.sh`) add
> `127.0.0.1 ach.e2e.local` to `/etc/hosts`; plain `localhost:8080` still
> reaches nginx, but the SSO `__Host-` cookie and Dex callback are registered
> for `ach.e2e.local`, so log in through that name.

The gateway Service is **`type: NodePort` (nodePort `30080`)** and
`scripts/kind-config.yaml` publishes it via an `extraPortMapping`
(hostPort `8080` → node containerPort `30080`). So on any cluster created
with the current kind-config, the whole platform is reachable at
`http://ach.e2e.local:8080` **with no port-forward** — the unified SSO +
`/platform` + `/content` + `/v1` + `/v2` paths all route through nginx.

> The `extraPortMapping` only binds at `kind create`. A cluster created
> **before** this change won't have it — recreate it
> (`make cluster-down && make cluster-up`) to publish `:8080`, or use the
> port-forward fallback below in the meantime.

**Fallback (cluster without the mapping):**
```bash
kubectl -n ach-system port-forward svc/ach-local-gateway 8080:8080
```

---

## 3. End-to-End login

Login is the OAuth AS (`/platform/oauth/*`); the Dex mock connector signs in
without a prompt. Two ways, both in the e2e suite:

1. **Loopback ceremony** (what `ach-cli login` option 1 and every MCP client
   do) — `oauthLogin` in `test/e2e/oauth_login_helpers_test.go`: DCR,
   `/authorize` with PKCE, follow the redirect chain (rewriting every hop to
   `ach.e2e.local:8080`), redeem the code at `/token`. Returns the token
   pair; the access token goes wherever a `pk_` used to (`x-ach-key`,
   `Authorization: Bearer`).
2. **Device grant** (`ach-cli login --no-browser`, an SSH host) —
   `deviceGrant` in `test/e2e/device_grant_test.go`: `device_authorization`,
   confirm the code on `/platform/oauth/device`, poll `/token`.

By hand, with the real binary against the kept cluster:

```bash
ACH_INSECURE=1 ./bin/ach-cli login --profile demo --base-url http://ach.e2e.local:8080 --no-browser
# open the printed URL, press Confirm (the Dex mock signs in), the CLI finishes
ACH_INSECURE=1 ./bin/ach-cli token | xargs -I{} curl -H "Authorization: Bearer {}" http://ach.e2e.local:8080/v1/models
```

The forwarder resolves the token to the user's `purpose='oauth'` row and
forwards the user's own LiteLLM virtual key.

---

## 4. Console UI dev loop

The React console (`ui/`) has its own dev server — no need to `make ui-build`
+ reroll the operator pod on every UI edit. Port-forward the gateway to
`localhost:8080` (either the fallback above, or the `:8080` NodePort mapping
already reaches it) so the platform-api it fronts is where `vite.config.ts`'s
dev proxy expects it:

```bash
kubectl -n ach-system port-forward svc/ach-local-gateway 8080:8080   # if not already reachable at :8080
npm --prefix ui run dev
```

`npm run dev` serves the SPA on `http://localhost:5173` and proxies
`/platform`, `/openwork`, and `/api/den` to `http://localhost:8080`
(`ui/vite.config.ts` `server.proxy`) — everything else (JS/CSS/HMR) is
served locally by Vite. Log in through the console UI at `:5173` as normal
(`kilgore@kilgore.trout`); the session cookie is set for `localhost` by the
dev server's own origin, not `ach.e2e.local`, so this path is for UI
iteration only — use the full `ach.e2e.local:8080` origin (§2/§3 above) to
exercise the embedded, built console end-to-end.
