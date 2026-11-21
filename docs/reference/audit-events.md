# Audit events

An audit event is one JSON line on stdout that carries `"audit":true`. Two
sources write them:

- **ACH** (platform-api, content-service and the operator) records keys,
  hydrates, admin actions and content fetches: see [Actions](#actions).
- **LiteLLM** records every model, MCP tool and A2A agent call, through the
  callback ACH ships: see [Calls](#calls). The forwarder writes no audit
  events.

ACH ships no log destination: the cluster's log collector reads stdout and
routes the audit lines wherever you keep them.

## Record

```json
{
  "time": "2026-11-20T09:14:03.512345678Z",
  "level": "INFO",
  "msg": "platform.hydrate",
  "audit": true,
  "action": "platform.hydrate",
  "outcome": "created",
  "actor": "ach/ana@example.com",
  "request_id": "req_01JD3QH6Z2W8K4T5N9XA1B2C3D",
  "key.id": "ekid_7f3c2a91",
  "target.kind": "environment",
  "target.name": "finance",
  "environment": "finance",
  "source.ip": "10.0.4.17",
  "source.user_agent": "curl/8.5.0",
  "route": "/platform/hydrate"
}
```

The line is written on one row with no space after `:` or `,`, so the text
`"audit":true` matches every audit event; it is wrapped here to be read. Names
with a dot (`key.id`, `target.kind`) are flat keys, not nested objects.

| Field | Value | Present |
|---|---|---|
| `time` | RFC 3339 timestamp | always |
| `level` | `INFO` | always |
| `msg` | Same value as `action` | always |
| `audit` | `true` | always |
| `action` | What was done; see [Actions](#actions) and [Calls](#calls) | always |
| `outcome` | Result code; see [Outcomes](#outcomes) | always |
| `actor` | Who did it; see [Actor](#actor) | always |
| `request_id` | `req_<ulid>`, the same id an ACH error response carries in `request_id` | platform-api and content-service events |
| `key.id` | `pkid_…` or `ekid_…`: the id of the key used or acted on, never the key | when a key was resolved |
| `target.kind`, `target.name` | The resource the request acted on | when the action has one |
| `environment` | The Environment the calling key is bound to | when the key is bound to one |
| `source.ip` | Client address; the first `X-Forwarded-For` hop when the header is set. On a call line, the address LiteLLM recorded for the request | when known |
| `source.user_agent` | `User-Agent` header | when sent |
| `route` | Route pattern the request matched | ACH events, when matched |

An event never holds a key, a token, a request or response body, or raw error text.
`actor`, `source.ip` and a `user` target are personal data.

### Actor

| Written by | `actor` |
|---|---|
| platform-api, content-service | `<namespace>/<email>`: the namespace ACH runs in and the owner email of the calling key; `<namespace>/-` when no key was resolved |
| platform-api, on `platform.ek.exchange` | `<namespace>/<ServiceAccount username>` once the token is verified, empty before |
| operator | `<namespace>/system:operator` |
| LiteLLM | The LiteLLM user of the calling key, with no namespace. For a key ACH created, the owner's email |

## Actions

| Action | Written by | Written when | Target | More fields |
|---|---|---|---|---|
| `platform.ek.create` | platform-api | An environment key is requested | `environment` | |
| `platform.ek.revoke` | platform-api | An environment key is revoked, by its owner or an admin | `environment` | |
| `platform.ek.suspend`, `platform.ek.resume` | platform-api | An environment key is suspended or resumed | `environment` | |
| `platform.ek.budget` | platform-api | The budget of an environment key changes | `environment` | |
| `platform.ek.exchange` | platform-api | A ServiceAccount token is exchanged, from the body check onwards | `environment` | `exchange.identity`, `exchange.requester`, `exchange.pod_name`, `exchange.pod_uid`; `exchange.reason` on a refusal |
| `platform.pk.revoke` | platform-api | A personal key is revoked, by its owner or an admin | | |
| `platform.hydrate` | platform-api | An Environment is hydrated | `environment` | |
| `platform.admin.refresh` | platform-api | An admin forces a refresh of a resource | the resource kind and name | |
| `platform.admin.authz` | platform-api | A caller without admin rights is refused on an admin route | | |
| `platform.admin.users.revoke_keys` | platform-api | An admin revokes every key of a user | `user`, the email | `revoked_count` |
| `platform.admin.user.budget` | platform-api | An admin sets a user's budget | `user`, the email | |
| `platform.admin.user.limits` | platform-api | An admin sets a user's limits | `user`, the email | |
| `platform.environments.list`, `platform.environments.get` | platform-api | LiteLLM cannot be reached while listing or reading Environments | `environment` on `get` | |
| `platform.authn` | platform-api | Authentication fails on an internal error | | |
| `platform.recover` | platform-api | A request handler panics | | |
| `content.get` | content-service | Content is fetched, served or refused | `instruction`, `plugin`, `artifact` or `skill`, and its name | |
| `operator.orphan-cleanup` | operator | The hourly orphan-key reaper revokes a key, fails to, or skips a run | `litellm_key` and the LiteLLM token id, or `tick` for a skipped run | `user_id`, `candidate_count` |

## Calls

LiteLLM writes these once
[the audit callback is installed](../platform/connect-litellm.md#audit-model-tool-and-agent-calls).
One line per call, after the call ends:

```json
{
  "time": "2026-11-20T09:14:07.120394Z",
  "level": "INFO",
  "msg": "litellm.tool.call",
  "audit": true,
  "action": "litellm.tool.call",
  "outcome": "success",
  "actor": "ana@example.com",
  "key.id": "ekid_7f3c2a91",
  "environment": "finance",
  "team": "ach-env-finance",
  "target.kind": "mcp_server",
  "target.name": "gitlab",
  "mcp.tool": "create_merge_request",
  "cost": 0.0,
  "policy": [{"name": "credential-filter", "mode": "pre_mcp_call", "status": "success"}],
  "source.ip": "10.0.4.17",
  "source.user_agent": "claude-cli/2.1.0",
  "trace_id": "7f3c5d0e9b2a4c61a91e0d4f5b6c7a81",
  "litellm.call_id": "0b6f2c1e-5d1a-4c57-9a55-2f1f6f0c9a11",
  "litellm.call_type": "call_mcp_tool"
}
```

| Action | Written when | `target.kind` | `target.name` |
|---|---|---|---|
| `litellm.model.call` | A model is called on any LiteLLM model route | `model` | The model name the caller asked for |
| `litellm.tool.call` | An MCP tool is called. Listing a server's tools is not recorded | `mcp_server` | The MCP server |
| `litellm.agent.call` | A message is sent to an A2A agent | `agent` | The agent |

`outcome` is `success`, `guardrail_blocked` when a guardrail stopped the call,
or `failed` for any other error. A call LiteLLM refuses is `failed` too: a model
outside the key's access is recorded with `error.class`
`ModelAccessDeniedProxyException` and `error.code` `403`.

| Field | Value | Present |
|---|---|---|
| `key.id` | The alias of the LiteLLM key. ACH sets it to the `pkid_…` or `ekid_…` of the key it created | when the key has an alias |
| `environment` | Taken from the key's team, `ach-env-<environment>` | environment keys |
| `team` | Alias of the key's LiteLLM team | when the key has a team |
| `mcp.tool` | Name of the tool called | `litellm.tool.call` |
| `model.name`, `model.provider` | The model and provider LiteLLM routed the call to | `litellm.model.call` |
| `tokens.prompt`, `tokens.completion` | Token counts | when LiteLLM counted any |
| `cost` | Cost LiteLLM computed for the call, in USD | when LiteLLM computed one |
| `policy` | One `{name, mode, status}` entry per guardrail that ran. `status` is LiteLLM's own, for example `success` or `guardrail_intervened` | when a guardrail ran |
| `error.class`, `error.code` | LiteLLM's error class and status code | on a failure |
| `trace_id` | LiteLLM trace id | always |
| `litellm.call_id` | Id of the call, returned to the client in `x-litellm-call-id` | always |
| `litellm.call_type` | LiteLLM's own call type, for example `acompletion` | always |

The identity fields come from the key LiteLLM authenticated, so a caller cannot
set them. A call made with a key ACH did not create is recorded too, with that
key's LiteLLM user, alias and team.

A request the forwarder refuses never reaches LiteLLM and writes no line here:
an unknown, expired or suspended ACH key, or a target outside the key's
Environment. The forwarder's request log has its status and `request_id`.

## Outcomes

`outcome` is a snake_case code. These eight mean the request was carried out:

| Outcome | Meaning |
|---|---|
| `created` | A key was created; also a completed hydrate or refresh |
| `revoked` | A key was revoked |
| `suspended`, `resumed` | A key changed state |
| `updated` | A budget or limit was changed |
| `issued` | A token exchange returned a token |
| `forwarded` | Content was served |
| `success` | A model, tool or agent call completed |

Any other value is a refusal or a failure, and releases add codes. Write alert
rules against the eight above, not against a list of failures.

| Kind | Outcomes |
|---|---|
| The caller is refused | `unauthorized_team`, `unauthorized_content`, `not_admin`, `not_key_owner`, `not_granted`, `wrong_environment`, `missing_environment`, `environment_not_found`, `content_not_found`, `invalid_key_type`, `invalid_key_format`, `invalid_argument`, `invalid_token`, `expired_or_revoked`, `key_suspended`, `key_limit_reached`, `cannot_revoke_active_key`, `rate_limited`, `idp_refused`, `guardrail_blocked` |
| ACH or a dependency fails | `internal_error`, `not_ready`, `litellm_unreachable`, `litellm_rejected`, `db_insert_failed`, `default_team_missing`, `stale_cache_expired`, `revoke_failed`, `skipped_empty_active_set`, `skipped_circuit_breaker` |
| A call fails for any other reason | `failed` |

## Collect them

Select the lines where `audit` is `true`:

```bash
kubectl -n ach logs deploy/ach-platform-api | jq -cR 'fromjson? | select(.audit == true)'
kubectl -n litellm logs deploy/litellm | jq -cR 'fromjson? | select(.audit == true)'
```

[Collect audit events](../platform/collect-audit-events.md) has the queries and
the collector rule for Grafana Loki, and what to do in other collectors.
