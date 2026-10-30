# LiteLLM wiring and the forwarder

## Contents

- What ACH does with LiteLLM
- `litellmConnection`
- What to configure in LiteLLM
- Forwarder credential headers
- Gemini model prefix strip
- Version notes

## What ACH does with LiteLLM

LiteLLM stays the model, MCP and A2A gateway; ACH governs who reaches what:

- The operator turns each Environment into a LiteLLM access group plus a
  deny-all "shell" team (`ach-env-<name>`), and attaches each entitled user's
  shell team (`ach-user-<email>`). It also writes budgets onto LiteLLM tags
  and reaps orphaned LiteLLM keys this release created.
- platform-api provisions users and teams at login and mints each user's and
  each `ek-` key's own LiteLLM virtual key.
- The forwarder replaces the caller's ACH credential with that caller's own
  LiteLLM key and stamps `x-litellm-tags: user:<email>[,environment:<env>,key:<id>]`
  so LiteLLM enforces every ceiling.

ACH talks to LiteLLM with the master key. It never proxies LiteLLM's own UI or
admin API (`/ui`, `/key/*`, `/health`, `/model/*`): publish LiteLLM on its own
host for those.

## `litellmConnection`

Configure LiteLLM once; the chart fans it out:

```yaml
litellmConnection:
  enabled: true
  endpoint: http://litellm.litellm.svc.cluster.local:4000
  masterKeySecretRef:
    name: litellm-master-key
    key: master_key
    namespace: ""      # only when the Secret lives outside the release namespace
```

- platform-api and content-service get `ACH_LITELLM_BASE_URL` and
  `ACH_LITELLM_MASTER_KEY` (from the Secret).
- The operator gets the endpoint and the Secret *reference*
  (`ACH_LITELLM_CONNECTION_*`) and creates `LiteLLMConnection/default` in the
  release namespace on boot. Helm cannot create that CR itself in the same
  release that installs its CRD, so do not add one to your manifests.
- The forwarder reads the connection from Postgres and gets RBAC to read only
  that Secret (a cross-namespace Role when `namespace` is set).

Check: `kubectl -n ach get llmconn default` shows `READY=True`. The operator
also creates the LiteLLM team with alias `default` once the connection is
ready.

## What to configure in LiteLLM

- **Master key** in the Secret above. When LiteLLM's Helm chart is used, the
  key is the chart value `masterkey` (lowercase); a typo there makes the chart
  generate a random one and every ACH call 401s.
- **Models, MCP servers, A2A agents** registered in LiteLLM (UI, API or its
  config file). Environments reference them by name; a name that does not
  exist leaves the Environment `AccessGroupSynced=False`.
- **Tag budgets** need no LiteLLM Enterprise licence. **Team-scoped guardrails**
  do: an Environment listing `spec.runtime.guardrails` on an unlicensed LiteLLM
  never becomes Available.
- **MCP backends that verify ACH's identity JWT** need, on their LiteLLM MCP
  server registration, `extra_headers: ["authorization"]` and
  `allow_all_keys: true`, or the JWT never reaches the backend (symptom: an
  empty tool list). That is per backend, documented with the
  `BackendIdentityPolicy` resource.
- **`X-Forwarded-*` from the forwarder** (only for backends that build URLs
  from them): set both in `general_settings`, scoped to the forwarder's Pod
  CIDR, never `0.0.0.0/0`:

  ```yaml
  general_settings:
    use_x_forwarded_for: true
    mcp_trusted_proxy_ranges: ["<forwarder Pod CIDR>"]
  ```

- **Model aliases for `/v1`**: use LiteLLM's `router_settings.model_group_alias`.
  The forwarder never rewrites the model name in a `/v1` body.

## Forwarder credential headers

```yaml
forwarder:
  headers:
    - name: x-ach-key        # ach-cli, hydrate, ach-agent
      mode: resolve
    - name: x-api-key        # Anthropic SDKs / Claude Code apiKeyHelper
      mode: resolve
```

The list is the set of request headers the forwarder treats as credentials, in
order; the first one present decides. It REPLACES the default list, it does
not extend it.

- `resolve`: the value is an ACH credential (`pk-`/`ek-` key). The forwarder
  resolves it, removes the header and forwards the caller's own LiteLLM key as
  `x-litellm-api-key`.
- `passthrough`: the value is a raw LiteLLM key, forwarded as it came and also
  mirrored into `x-litellm-api-key`; no ACH identity, no ACH budgets.
- A `pk-`/`ek-` value is resolved in ANY slot (passthrough included) and never
  reaches LiteLLM.
- `authorization` cannot be listed. `Authorization: Bearer` is honoured only
  for ACH's own OAuth token, a `pk-`/`ek-` key, or a trusted-IdP token
  (`references/auth.md`); anything else there is passed to LiteLLM untouched.
- An undeclared header still reaches LiteLLM but does not count as a
  credential: a request carrying only that is 401.

Common additions:

```yaml
forwarder:
  headers:
    - {name: x-ach-key, mode: resolve}
    - {name: x-api-key, mode: resolve}
    - {name: x-goog-api-key, mode: resolve}         # native Gemini clients holding an ek- key
    - {name: x-genai-api-key, mode: passthrough}    # raw LiteLLM keys under a customer-facing name
```

`platformApi.headers` has the same shape but accepts `resolve` only (it does not
proxy); a `passthrough` entry there refuses to start.

## Gemini model prefix strip

Native Gemini clients address `/gemini/.../models/<bare-google-name>:...`,
while LiteLLM often registers the model as `<vendor>.<model>` or
`<vendor>/<model>`:

```yaml
forwarder:
  gemini:
    stripModelPrefixes: ["gemini.", "gemini/", "vertex_ai."]
```

Applied only on `/gemini`, to the path segment after `/models/`, in declared
order, first match wins. Empty (default) = no rewrite.

## Version notes

ACH relies on LiteLLM's virtual keys, teams, access groups, tag budgets and
MCP gateway. Use a LiteLLM version at least as new as the one the ACH release
was tested against (its e2e LiteLLM values pin the image), and re-test
Environments going Available after any LiteLLM upgrade.
