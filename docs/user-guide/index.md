# User Guide

TODO: Per-CRD usage guides — `Environment`, `Plugin`, `PluginMarketplace`, `Artifact`, `Prompt`, `BackendIdentityPolicy`.

## Choosing a key: `pk_` vs `ek_`

- **`pk_` (Personal Key)** — dev / personal use. Authorizes against the UNION of
  your capabilities; NOT bound to any Environment (no access-group scoping, no
  Environment attribution tag). Convenient for local experimentation.
- **`ek_` (Environment Key)** — agents / CI / workloads. Environment-scoped:
  capability-gated to one Environment and carries its attribution tag. Use this
  for anything reproducible or governed.

A recommended Prometheus alert: watch `ach_forwarder_requests_total{key_type="pk"}`
on runtime routes to catch `pk_` used where an `ek_` belongs. There is no
server-side `pk_`-forbid toggle (honoring the frozen permanent decision);
enforcement, if wanted, is the deployer's LiteLLM choice.

## `ek_` lifecycle: expiry, suspend, resume, revoke

An `ek_` can optionally be created with an expiration time, and can be
manually suspended and resumed without losing the credential. This is an
HTTP API surface for now — `ach-cli keys create` has no `--expires-at` flag
and there is no `ach-cli keys suspend`/`resume` subcommand yet (CLI support
is a follow-up); use the routes below directly, or through the web console.

- `POST /platform/keys` accepts an optional `expires_at` (RFC3339, must be in
  the future — a past or malformed value is `400 invalid_argument`). Omitting
  it keeps the existing perpetual behavior. Expiry is enforced by ACH only:
  no LiteLLM-side duration is ever set, and it is not editable after create —
  create a new credential instead.
- `POST /platform/keys/{key_id}/suspend` and `POST /platform/keys/{key_id}/resume`
  disable and re-enable an `ek_` in place. Both are idempotent when the key
  is already in the requested state. Resume fails `409` on a revoked or
  expired key, and `403 unauthorized_team` if you no longer hold the
  Environment's access. The backing credential is never deleted or
  re-minted — the same `ek_` keeps working once resumed.
- `DELETE /platform/keys/{key_id}` revokes from any state and is idempotent;
  it cannot be undone.

> Suspension may take up to 60 seconds to apply. Requests already in
> progress may continue.

## Spend ceilings

Every ACH credential can carry a spend ceiling. They are enforced by LiteLLM
on three independent counters, and a request is refused as soon as ANY of
them is over budget — there is no hierarchy, whichever is crossed first wins:

| Ceiling | Covers | Set by |
|---|---|---|
| Your own | your `pk_` **and** every `ek_` you own, in every Environment | the deployer, as a default applied at your first login |
| Environment | the pooled spend of every `ek_` issued for one Environment, across all owners | an admin, via `Environment.spec.budget` in the CR |
| Per key | one `ek_` on its own | you, when you create the key or at any time after |

Set a ceiling when you create a key:

```bash
curl -X POST https://<ach>/platform/keys \
  -H "Authorization: Bearer $ACH_TOKEN" -H 'Content-Type: application/json' \
  -d '{"environment":"demo","name":"ci","budget":{"max_budget":25,"budget_duration":"30d"}}'
```

…or change it later (the key keeps working; only the ceiling moves):

```bash
curl -X PATCH https://<ach>/platform/keys/<key_id>/budget \
  -H "Authorization: Bearer $ACH_TOKEN" -H 'Content-Type: application/json' \
  -d '{"max_budget":50}'
```

`max_budget` is in LiteLLM's spend unit (USD) and must be `>= 0`; `0` is a
real ceiling that refuses everything once any spend lands. `budget_duration`
(e.g. `"30d"`) is the reset window — omit it for a budget that never resets.
Only the key's owner may change its budget.

When a ceiling is reached, the request fails with **HTTP 429** and a body
naming the counter that blocked:

```json
{"detail":"Budget has been exceeded! Tag=key:ekid_… Current cost: 25.4, Max budget 25.0"}
```

`Tag=user:…` means your own ceiling, `Tag=environment:…` the Environment's
pooled one, and `Tag=key:…` that single key's. Raise the named ceiling (or
wait for its reset window) and traffic resumes within about ten seconds.
Two caveats: the comparison is **post-paid**, so the request that crosses
the line is served and the NEXT one is refused; and revoking a key deletes
its ceiling along with it.

`GET /platform/keys` reports one of five effective states, in priority order
when more than one applies:

| State | Meaning | Recovery |
|---|---|---|
| `revoked` | Permanently withdrawn. | Cannot be recovered; create a new credential. |
| `expired` | Its `expires_at` has passed. | Create a new credential; expiry is not editable. |
| `suspended` | Manually disabled by the owner. | `POST .../resume`, subject to current access and validity. |
| `invalid` | Owner no longer has access to the Environment. | Automatically usable again once access returns — no action needed. |
| `active` | Enabled, unexpired, not revoked, owner has access. | None required. |

`?status=` filters on this effective state; an unrecognized value is
`400 invalid_argument`. Because the filter is applied AFTER the effective
state is derived (an `invalid` row is still persisted `active`), a filtered
page can hold fewer than `limit` items even while `next_cursor` is
non-empty — keep paging until `next_cursor` is empty rather than stopping
at a short page.

**Release notes:** once any `ek_` has been suspended or given an expiry, the
database cannot be rolled back below this release (D-29) — see
`references/troubleshooting.md` for the exact refusal text and recovery.
