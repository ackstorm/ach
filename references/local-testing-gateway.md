# Local Unified testing, SSO login & Gateway Reference

This document outlines the local unifed gateway architecture and testing procedures on Kind for the Agent Capability Hub (ACH) platform.

---

## 1. Local Unified Gateway Architecture

Routing now lives in **two layers**. Production routing is owned by the
in-binary **`ach-gateway`** pod (`ach gateway` mode, `internal/gateway`): a
dumb reverse proxy that fronts every production-real surface
(`/platform /content /v1 /gemini /mcp /a2a /.well-known`) behind one
in-cluster Service. In prod the public Ingress targets `ach-gateway`
directly — there is no nginx.

In local/e2e testing on Kind, the **`ach-local-gateway`** nginx pod is now
an **e2e-only shim**, not the primary router. Kind binds one host port, so
the shim preserves the single `localhost:8080` origin the SSO cookie
round-trip and metrics-scrape harness depend on. It serves the two dev
**kludges** locally (`/dex`, `/metrics/<svc>`) and **falls through
everything else to `ach-gateway`** — so e2e traffic exercises the real
gateway exactly as prod will.

```
                          host  http://localhost:8080
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
                          ▼ (/platform)   ▼ (/content)                    ▼ (/v1,/gemini,/mcp,/a2a,/.well-known)
                ┌────────────────────┐ ┌────────────────────┐ ┌────────────────────┐
                │  ach-platform-api  │ │  content-service   │ │   ach-forwarder    │
                │   (Service: 80)    │ │  (Service: 8082)   │ │   (Service: 80)    │
                └────────────────────┘ └────────────────────┘ └────────────────────┘
```

Reachable under the single localhost port (`8080`):
* **Platform API:** `http://localhost:8080/platform/` — shim → `ach-gateway` → platform-api
* **Content Service:** `http://localhost:8080/content/` — shim → `ach-gateway` → content-service
* **LLM Forwarder:** `http://localhost:8080/{v1,gemini,mcp,a2a}/`, JWKS at `/.well-known/` — shim → `ach-gateway` → forwarder
* **SSO (Dex):** `http://localhost:8080/dex/` — **DEV KLUDGE, served by the shim, never by `ach-gateway`.** Prod reaches Dex directly via `ACH_DEX_ISSUER_URL` (e.g. `https://auth.ackstorm.ai`) with no gateway involvement.
* **Per-service metrics:** `http://localhost:8080/metrics/{forwarder,content,platform,operator}` — **DEV KLUDGE, served by the shim.** Distinct routes because a bare `/metrics` can't disambiguate four services. The e2e harness exports these as `ACH_{FORWARDER,CONTENT,PLATFORM,OPERATOR}_METRICS_URL`; `/metrics/operator` is backed by the `ach-operator-metrics` Service. **`ach-gateway` has NO `/metrics` route by design** — keeping metrics off the prod router means the prod Ingress physically cannot leak them.
* **Shim health:** `http://localhost:8080/healthz` returns `200 ok` directly from nginx (no upstream); it backs the shim pod's probes. `ach-gateway` serves its own local `/healthz` for its pod probes.

---

## 2. Deploying & Starting the Gateway

### Deployment Manifest
The gateway is packaged in `test/e2e/cluster/03-test-backends/ach-local-gateway.yaml`.
The cluster hydration script (`scripts/cluster.sh`) automatically deploys and rolls out this gateway as part of the `hydrate_all` loop.

To apply or update the gateway manually:
```bash
kubectl apply -f test/e2e/cluster/03-test-backends/ach-local-gateway.yaml
```

### Reaching the Gateway on `localhost:8080`

The gateway Service is **`type: NodePort` (nodePort `30080`)** and
`scripts/kind-config.yaml` publishes it via an `extraPortMapping`
(hostPort `8080` → node containerPort `30080`). So on any cluster created
with the current kind-config, the whole platform is reachable at
`http://localhost:8080` **with no port-forward** — the unified SSO +
`/platform` + `/content` + `/v1` paths all route through nginx.

> The `extraPortMapping` only binds at `kind create`. A cluster created
> **before** this change won't have it — recreate it
> (`make cluster-down && make cluster-up`) to publish `:8080`, or use the
> port-forward fallback below in the meantime.

**Fallback (cluster without the mapping):**
```bash
kubectl -n ach-system port-forward svc/ach-local-gateway 8080:80
```

---

## 3. End-to-End SSO Login & Key Generation

Because ACH uses secure `__Host-` prefix cookies (`__Host-ach_sso`) for OIDC State / PKCE verification, browser clients and python scripts will normally reject these cookies when running over plain `http://localhost`.

To test the SSO and generate a personal key (`pk_...`) locally over HTTP:

1. **The Python Script:**
   Below is a Python script that overrides the cookie's `secure` flag, allowing it to negotiate the PKCE Dex redirect over plain HTTP successfully:

   ```python
   # sso-login.py
   import requests
   import json

   session = requests.Session()

   # Step 1: Initiate OAuth Login
   login_resp = session.get("http://localhost:8080/platform/auth/login", allow_redirects=False)
   dex_url = login_resp.headers.get("Location")

   # Step 2: Override the 'Secure' cookie flag to allow HTTP localhost transmission
   for cookie in session.cookies:
       cookie.secure = False

   # Step 3: Rewrite internal cluster DNS to localhost
   dex_url_local = dex_url.replace("dex.dex-system.svc.cluster.local:5556", "localhost:8080")

   # Step 4: Perform login (Dex mock automatically authenticates)
   # We follow redirects manually because of internal k8s domain names
   current_url = dex_url_local
   while True:
       current_url = current_url.replace("dex.dex-system.svc.cluster.local:5556", "localhost:8080")
       for cookie in session.cookies:
           cookie.secure = False
       resp = session.get(current_url, allow_redirects=False)
       if resp.status_code in (301, 302, 303, 307, 308):
           current_url = requests.compat.urljoin(current_url, resp.headers.get("Location"))
       else:
           break

   # Step 5: Read the minted personal key!
   data = resp.json()
   print("SSO Login Succeeded! Personal Key details:")
   print(json.dumps(data, indent=2))
   ```

2. **Verify on the LLM Forwarder:**
   Once you obtain the personal key plaintext (e.g. `pk_xxxx...`), you can execute any OpenAI-compatible API request on the same unified port:

   ```bash
   curl -H "x-ach-key: pk_xxxx..." \
        -H "Content-Type: application/json" \
        http://localhost:8080/v1/models
   ```
   This is proxied by Nginx to the Forwarder, which resolves the key to its LiteLLM virtual key, and impersonates it securely inside LiteLLM!
