# User Guide

TODO: Per-CRD usage guides — `Environment`, `Plugin`, `PluginMarketplace`, `Artifact`, `Prompt`, `BackendIdentityPolicy`.

## Choosing a key: `pk_` vs `ek_`

- **`pk_` (Personal Key)** — dev / personal use. Authorizes against the UNION of
  your capabilities; NOT bound to any Environment (no access-group scoping, no
  Environment attribution tag). Convenient for local experimentation.
- **`ek_` (Environment Key)** — agents / CI / workloads. Environment-scoped:
  capability-gated to one Environment and carries its attribution tag. Use this
  for anything reproducible or governed.

A recommended Prometheus alert: watch `forwarder_requests_total{key_type="pk"}`
on runtime routes to catch `pk_` used where an `ek_` belongs. There is no
server-side `pk_`-forbid toggle (honoring the frozen permanent decision);
enforcement, if wanted, is the deployer's LiteLLM choice.
