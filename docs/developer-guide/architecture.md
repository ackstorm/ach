# Architecture

ACH runs as one image, `ghcr.io/ackstorm/ach`, started in a different mode per
Deployment. Postgres holds ACH's state; LiteLLM serves the models and tools;
Dex connects your identity provider.

```
 CRDs ──reconcile──▶ operator ──rows + NOTIFY──▶ Postgres ◀── read + LISTEN ──┐
                       │ (content service                                     │
                       │  runs beside it)                                     │
                       ▼                                                      │
                     LiteLLM ◀── forwarder ◀── tools (Claude Code, Codex, …)  │
                       ▲            │                                         │
                       └──────── platform-api ◀── ach-cli, console ──────────┘
                                    │
                                   Dex ──▶ your identity provider
```

| Mode | Role |
|------|------|
| `operator` | Reconciles the ACH resources, fetches content, writes LiteLLM access groups, teams and budgets, and projects everything into Postgres. |
| `content-service` | Serves fetched content to `ach-cli`. Runs as a second container in the operator Pod by default. |
| `platform-api` | Sign-in (an OAuth 2.1 server in front of Dex), keys, hydrate manifests, admin endpoints and the web console. |
| `forwarder` | The data path: checks the caller's credential, tags the request for budgets, and passes it to LiteLLM with the caller's own LiteLLM key. Mints per-backend identity tokens for MCP and A2A. |
| `gateway` | Optional. One address in front of platform-api, forwarder and content service. |
| `migrate` | Runs the Postgres schema migrations. |

## Dependencies

| Component | Used for |
|-----------|----------|
| Kubernetes | Runs ACH; the resources are CRDs. |
| Postgres | ACH's source of truth: keys, Environments, content index. Back it up: keys cannot be re-created from the CRDs. |
| Valkey | Credential cache and sign-in state. |
| LiteLLM (1.108 or later) | Models, MCP servers, A2A agents, budgets. ACH holds its master key. |
| Dex | Connects any identity provider through one OIDC interface. |

## Request path

1. A tool sends a request with an ACH credential to the forwarder.
2. The forwarder resolves the credential to a person (and an Environment for
   an `ek_`), removes it, and adds the caller's own LiteLLM key and budget
   tags.
3. LiteLLM checks the key's team and access groups and serves the request.
4. For an MCP server or A2A agent with a BackendIdentityPolicy, the forwarder
   adds a short-lived token naming the caller.

The full contracts are in the [JWT Forwarder guide](jwt-forwarder.md).
