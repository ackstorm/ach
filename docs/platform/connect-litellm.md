# Connect ACH to LiteLLM

LiteLLM stays the gateway to models, MCP servers and A2A agents. ACH decides
who reaches which of them, and uses the LiteLLM master key to do it. This page
points ACH at LiteLLM and lists what to configure inside LiteLLM.

You need LiteLLM 1.108.0 or later, reachable from the ACH namespace, and its
master key in a Secret; see the [prerequisites](prerequisites.md#litellm).

## What ACH writes in LiteLLM

| ACH component | Writes |
|---|---|
| Operator | For each Environment, an access group and a team `ach-env-<name>`. For each user, the link between their team `ach-user-<email>` and the Environments they may use. Budgets, as tag budgets |
| platform-api | Users, their team memberships at sign-in, and one LiteLLM key for each user and each environment key |
| Forwarder | Nothing. It replaces the caller's ACH credential with that caller's own LiteLLM key and tags each request with the user, the Environment and the key, so LiteLLM applies the budgets |

ACH never serves LiteLLM's own UI or admin API (`/ui`, `/key/*`, `/health`,
`/model/*`). Publish LiteLLM on its own host for those.

## Point ACH at LiteLLM

Set [`litellmConnection`](../reference/helm-values.md#litellmconnection) in
`values.yaml`:

```yaml
litellmConnection:
  endpoint: http://litellm.litellm.svc.cluster.local:4000
  masterKeySecretRef:
    name: litellm-master-key
    key: master_key
    namespace: ""      # set only if the Secret is in another namespace
```

The chart gives the endpoint and the key to every component. The operator
creates the resource `LiteLLMConnection/default` from these values when it
starts: do not add one to your own manifests.

## What to configure in LiteLLM

- **Models, MCP servers and A2A agents.** Register them in LiteLLM.
  Environments refer to them by name.
- **Teams.** Create the teams your Environments authorize, by alias. ACH
  creates only the team `default`.
- **Master key.** With the LiteLLM Helm chart the value is `masterkey`, in
  lower case. A misspelt key makes that chart generate a random one, and every
  ACH call to LiteLLM then answers 401.
- **Guardrails** attached to a team need a LiteLLM Enterprise licence. An
  Environment that lists guardrails on an unlicensed LiteLLM never becomes
  available. Budgets need no licence.
- **Model aliases.** Use `router_settings.model_group_alias` in LiteLLM. ACH
  does not rename the model in a `/v1` request.
- **MCP servers that check the caller's identity** need
  `extra_headers: ["authorization"]` and `allow_all_keys: true` on their
  LiteLLM registration. Without them the identity token ACH adds does not
  reach the server, and its tool list is empty.
  [Writing an MCP backend](../integrate/write-an-mcp-backend.md) has the
  detail.

## Forwarder credential headers

The forwarder reads the caller's credential from these request headers, in
order; the first one present decides:

```yaml
forwarder:
  headers:
    - {name: x-ach-key, mode: resolve}
    - {name: x-api-key, mode: resolve}
```

This is the default. A list you set replaces it, so repeat the entries you
keep.

| Mode | The header holds | The forwarder |
|---|---|---|
| `resolve` | An ACH credential: a key or a session token | Replaces it with the caller's own LiteLLM key |
| `passthrough` | A LiteLLM key | Forwards it unchanged. The request has no ACH user and no ACH budget |

Two common additions:

```yaml
forwarder:
  headers:
    - {name: x-ach-key, mode: resolve}
    - {name: x-api-key, mode: resolve}
    - {name: x-goog-api-key, mode: resolve}         # Gemini clients that hold an ACH key
    - {name: x-genai-api-key, mode: passthrough}    # LiteLLM keys under a name of your own
```

- An ACH key is recognised in any listed header, also a `passthrough` one, and
  never reaches LiteLLM.
- `authorization` cannot be listed. `Authorization: Bearer` is always read: an
  ACH session token or an ACH key is replaced, and anything else passes to
  LiteLLM unchanged.
- A request with no listed header and no `Authorization` credential answers
  401.

`platformApi.headers` has the same shape and accepts `resolve` only.

## Gemini model prefix

A Gemini client asks for a model by its Google name, for example
`gemini-2.5-pro`. LiteLLM often registers the same model with a prefix, such
as `gemini.gemini-2.5-pro`. List the prefixes to remove:

```yaml
forwarder:
  gemini:
    stripModelPrefixes: ["gemini.", "gemini/", "vertex_ai."]
```

The forwarder removes the first prefix that matches from the model name in a
`/gemini` request. It leaves `/v1` requests alone.

## Verify

```bash
kubectl -n ach get litellmconnection default       # READY is True
ach-cli admin list models                          # the models LiteLLM serves
```

Then send one request with an ACH credential:

```bash
curl -fsS https://ach.example.com/v1/chat/completions \
  -H "Authorization: Bearer $(ach-cli token)" -H 'content-type: application/json' \
  -d '{"model": "<a model of one of your Environments>", "messages": [{"role": "user", "content": "hello"}]}'
```

## Related

- [`forwarder`](../reference/helm-values.md#forwarder) in the Helm values
  reference.
- [Troubleshoot an ACH install](troubleshoot.md#verification-failures)
