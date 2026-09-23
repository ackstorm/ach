# LiteLLM permission model — measured, not inferred

Every statement here was measured against the live ackstorm LiteLLM proxy on
2026-07-21 (ACH v0.6.16 / alitellm-operator v0.7.27). Re-measure before
trusting it against a different LiteLLM version.

## 1. Access groups only ADD. They never restrict.

A key's effective permission set is the union of its team's grants and the
grants of every access group it reaches. There is no deny list on either side
and no intersection semantics.

Measured: team `test` has `models: ['openai']` (excludes
`gemini.gemini-flash-latest`); access group `test-env` grants that model; a key
in both got HTTP 200 on it.

## 2. A group has two faces; only the mirror enforces.

- group side: `access_group.assigned_team_ids` / `assigned_key_ids`
- mirror:     `team.access_group_ids` / `key.access_group_ids`

Enforcement reads the mirror. A group with `assigned_team_ids: []` but a
populated mirror still grants. LiteLLM writes the mirror only for teams/keys
that ENTER or LEAVE the group side, so an idempotent PUT of an unchanged list
does NOT repair a broken mirror — that is why the Environment reconciler runs a
two-PUT delta repair (v0.6.16).

## 3. Key-level restriction is not a usable limiter.

`key.models` does restrict correctly. Key-level MCP restriction is rejected:

    POST /key/generate  {"team_id":"run","object_permission":{"mcp_servers":["mcp-slack"]}}
    403  Key requests MCP servers not allowed by team 'run': ['mcp-slack'].
         Team allows: []. Global (allow_all_keys) servers: [].

LiteLLM validates a key's requested MCP servers against `team.object_permission`
ONLY. Servers the team reaches through access groups do not count. A team whose
MCP access all comes from groups cannot narrow MCP at key level at all.

## 4. A key with no team is fail-open on models.

Measured on a teamless key:

| dimension | outside any group | inside the `test-env` group |
|---|---|---|
| models    | ALL allowed | ALL allowed — the group does not narrow it |
| MCP tools | 0 | 28 = exactly `mcp-slack` |
| agents    | 7 (all) | 1 = exactly `finops-advisor` |

MCP and agents scope correctly; models stay wide open.

## 5. Empty means EVERYTHING for models and agents.

An empty or absent `models` list means every model. An empty or absent `agents`
list means every agent. Both fail OPEN. `mcp_servers` is the exception and fails
closed. This is why ACH's shell team carries sentinels rather than empty lists:

    models                                 = ["no-default-models"]
    object_permission.mcp_servers          = []
    object_permission.mcp_access_groups    = []
    object_permission.mcp_tool_permissions = {}
    object_permission.agents               = ["00000000-0000-0000-0000-000000000000"]
    object_permission.agent_access_groups  = []

### The models sentinel must be `no-default-models` (measured 2026-09-22, e2e cluster)

Any impossible name denies. Only LiteLLM's OWN value stays out of the catalog:

| team `models:` | `GET /v1/models` | `POST /v1/chat/completions` |
|---|---|---|
| `["__deny_all__"]` | **1 row** — the sentinel leaks into the catalog as a phantom model | 403 `team not allowed to access model. This team can only access models=['__deny_all__']` |
| `["no-default-models"]` | **0 rows** | 403, identical shape |

Both deny equally; `no-default-models` is a value LiteLLM recognises and
filters out of the model list, an invented one is echoed back. ACH wrote
`__deny_all__` until 2026-09-22, so every caller in a shell team saw a bogus
model by that name — visible to ach-agent, OpenCode, anything listing models.

The switch is a two-sided rule, in `internal/litellm/shellteam.go`:
**writing** emits only `ShellTeamDenyAllModel` ("no-default-models");
**reading** accepts both (`IsDenyAllModel`) wherever ACH detects a deny-all
state — shell-team adoption (`IsShellTeamShaped` / `IsUserShellShaped`) and
the console's `provisioning` projection — because a shell written by an older
ACH still carries the legacy value until something rewrites it.
`ShellTeamDrifted` is the deliberate exception: it compares against the
current value only, so a legacy shell reads as DRIFTED and gets migrated.
`/model_group/info` (what the console reads) was NOT part of this measurement
— the 0-row result above is `/v1/models` — which is the other reason the
console still filters by name.

**Both shells repair, by different routes.** An `ach-env-<name>` shell is
repaired by the operator's `ensureShellTeam` on every Environment reconcile.
An `ach-user-<email>` shell has no reconciler: `MintPK`
(`internal/platformapi/auth/mint.go`) owns it, and since 2026-09-22 a
duplicate-team answer from `CreateTeam` (i.e. every login after the first)
triggers `repairUserShell` — one `GET /team/info`, and one `POST /team/update`
only when the team is ACH-owned (marker or shell shape, the same rule
`ensureShellTeam` follows) AND `ShellTeamDrifted` says so. Before that the
user shell was created-only, so ANY drift on it — not just the legacy
sentinel — was permanent, which is fail-OPEN (an empty models or agents list
means everything). Every failure in that path is logged and swallowed: the
shell already exists and already denies, so the fallback is "unchanged", and
a cosmetic repair must never cost the person a usable `pk_`.

These sentinels cover four axes — `models`, `mcp_servers` (+
`mcp_access_groups` and `mcp_tool_permissions`, both of which grant servers on
the same code path, §12), and `agents` (+ `agent_access_groups`).
`object_permission`'s other fields (`vector_stores`, `mcp_toolsets`,
`blocked_tools`, `search_tools`, `mcp_tool_search_enabled`) are left alone —
§12 says why each one is safe to leave.

Measured on env `test-env` (`gemini.gemini-flash-latest`, `mcp-slack`,
`finops-advisor`):

| shell team | models | MCP | agents |
|---|---|---|---|
| empty (`models: []`, no `agents`) | bedrock + openrouter **allowed** — fail open | 28 = mcp-slack | 1 = finops-advisor |
| with sentinels | gemini 200, bedrock + openrouter denied | 28 = mcp-slack | 1 = finops-advisor, others denied |

## 6. A team is the only thing that reliably caps a key.

Two configurations scope all three dimensions correctly: a team carrying the
three lists directly, and a deny-all shell team with the access group attached
to it. ACH uses the shell because it keeps the grants in ONE place (the access
group) — no second copy of the lists, no second drift surface. Adding the access
group to the key itself changed nothing measurable and is forbidden (see 7).

## 7. Agent-permission collapse bug — do not put a key in both.

When a key has a team AND belongs to an access group, and the team's agent list
differs from the group's, the effective agent set collapses to EVERY agent on
the proxy instead of the union. Measured: team granting `support-triage` + group
granting `finops-advisor` produced all 7 agents, and `architecture-reviewer` —
granted by neither — returned HTTP 200 on `POST /a2a/{id}/message/send`. With
identical lists the bug does not appear.

Treat "key has a team and also belongs to a group that grants agents" as unsafe.
ACH never adds an `ek_` to an access group.

## 8. Deletion order is load-bearing.

Deleting the shell team FIRST leaves its keys answering 200 for up to ~60s
(LiteLLM key-cache TTL), and during that window the key cannot be revoked by any
route — `POST /key/delete` (by `keys` and by `tokens`), `POST /key/block` and
`POST /key/update` all return 404 while the key still serves traffic. A key never
used before the team was deleted is rejected immediately, so the window only
exists for recently-active keys. Orphaned keys keep the deleted team's
restrictions (models outside the list stayed denied), so this is a
revocation-LATENCY problem, not privilege escalation — but a deleted environment
can still serve traffic for a minute.

Correct order, which ACH's Environment finalizer implements:

1. `POST /key/delete` for every EK of the environment
2. delete the access group
3. `POST /team/delete` for the shell team

Deleting the keys first makes revocation immediate and verifiable: `key/delete`
returns 200 and the very next request returns 401. A 404 inside the orphan
window must NEVER be logged as a successful revocation.

## 9. API shapes (read from LiteLLM source, `main` @ 2026-07-21)

`object_permission` is `LiteLLM_ObjectPermissionBase` (`litellm/proxy/_types.py`),
every key optional: `mcp_servers`, `mcp_access_groups`, `mcp_tool_permissions`,
`mcp_toolsets`, `blocked_tools`, `vector_stores`, `agents`,
`agent_access_groups`, `models`, `search_tools`, `mcp_tool_search_enabled`.
ACH manages the five it measured (§5); the rest are left alone (§12).

| endpoint | accepts `object_permission` | returns it inline |
|---|---|---|
| `POST /team/new` | yes | **no** (`include` omits the relation) |
| `POST /team/update` | yes | **yes** |
| `GET /team/info?team_id=` | — | **yes** (`include={"object_permission": True}`) |
| `GET /v2/team/list`, `GET /team/list` | — | **NO — serialises as `null`**, only `object_permission_id` is present |

So **drift detection cannot use the team list**: it must read `GET /team/info`
per shell team (or trust the `/team/update` response). A `null` there means "the
endpoint did not resolve the relation", never "the team has no permissions".

`POST /team/update` merges `object_permission` **per key** (shallow
`dict.update` over the existing row in `object_permission_utils.py`): keys you
omit are preserved, keys you send replace that list wholesale, and clearing a key
requires sending it explicitly as `null`.

`POST /team/delete` body: `{"team_ids": ["<id>"]}` (`DeleteTeamRequest`).

`POST /key/generate` accepts `team_id`, and the membership check
(`_team_key_generation_check`) **returns early for `PROXY_ADMIN`** — the master
key ACH uses. So a key may be minted into a team the `user_id` does not belong
to. `POST /key/update` is the asymmetric one: moving an existing key to a team
raises `403 User=… is not a member of the team=…` and is NOT admin-exempt.

Team `models: []` means ALL models (`_check_model_access_helper`:
`len(filtered_models) == 0 and len(models) == 0 → all_model_access`). LiteLLM's
`no-default-models` sentinel is USER-level only — it has no enforcement site in
the team model-access path — which is why ACH uses an impossible model name.

## 10. Consequences for ACH

- `ek_` keys are minted with `team_id: ach-env-<environment>` and NOTHING else —
  no `models`, no `object_permission`, no `access_group_ids`.
- `ek_` expiry is a deliberate decision, not an oversight: no `Duration` is set
  on mint, so the key never expires — its lifetime is the Environment's. It is
  revoked only by explicit `DELETE /platform/keys/{id}` or by deleting the
  Environment (the shell-team delete cascades to its keys). This is the
  opposite of `pk_` below (168h sliding window, re-minted on every SSO login)
  because an `ek_` has no renewal path — a finite window with no re-mint would
  silently break long-running agents once it elapsed.
- A `pk_`'s 168h window is **slid on both sides**. ACH slides its own
  `personal_keys.expires_at` in `PkCheckAndExtend`; because LiteLLM stamps the
  key's expiry once at `/key/generate` time, `keystore.NewLiteLLMPkExtendHook`
  must mirror each slide with `POST /key/update {"key": <litellm_token>,
  "duration": "168h"}`. Skip the mirror and the LiteLLM key expires at mint+7d
  while ACH keeps honouring the `pk_` up to the 90-day cap: `ach-cli` reports
  the key active, platform-api accepts it, and only the forwarded LLM call
  fails — 401 from LiteLLM. Measured in prod 2026-07-31 before the fix.
- The environment's grants live only in the access group; the shell team is
  constant boilerplate, identical for every environment.
- `pk_` keys are now minted with `team_id: ach-user-<email>` — a
  per-user deny-all shell, symmetric to the per-Environment one. The shell
  carries no grants of its own; the operator (sole writer of
  `assigned_team_ids`) attaches every entitled Environment's access group onto
  it, live-resolved from `GET /team/info` member `user_id == email`
  (`internal/controller/ach/environment_usershells.go`). One pk_ therefore
  reaches exactly the union of the caller's entitlements — no more, no less.
  `platform-api` provisions the shell (idempotent `POST /team/new`, 400
  "already exists" = success) and sets the key's own `duration` to match
  `pkExpiryWindow` at mint time (`internal/platformapi/auth/sso.go`).
  **Residual (locked, decision iii):** PKs minted BEFORE this change keep
  `team_id=NULL` + `expires:None` — not migrated — and stay fail-open on
  models until revoked by hand.
- ACH must track which EKs belong to an environment so it can enumerate them at
  deletion time (it does: `environment_keys.environment`). Do not rely on
  `team_id` for that mapping — it disappears with the team.

## 11. Guardrails (measured against api.ackstorm.ai / LiteLLM v1.93.0, 2026-07-28)

- Access groups carry **no** guardrail field; `object_permission` has none
  either. Only the key and the team can carry guardrails.
- Team attachment is `metadata.guardrails`. Enforcement unions key ∪ team ∪
  project and then **appends** the request body — a caller can add but never
  subtract.
- `guardrails` is in `LiteLLM_ManagementEndpoint_MetadataFields_Premium`: a
  non-empty write is **403** without an Enterprise licence, at attach time and
  again per request. Empty/omitted is exempt. Both halves measured directly
  2026-07-29 (scratch teams + keys, since deleted):

  | action | unlicensed result |
  |---|---|
  | `POST /team/new` top-level `"guardrails": ["x"]` | 403 `...Enterprise users: guardrails` |
  | `POST /team/new` `"guardrails": []` or key absent | 200 |
  | `POST /team/update` `metadata: {"guardrails": ["x"]}` | **200 — NOT gated** |
  | `/chat/completions` with a key in a team carrying `metadata.guardrails` | **403 every request** |
  | same, team with no team-scoped guardrails | 200, `x-litellm-applied-guardrails: credential-filter` |

  The `metadata` write path being ungated is why the LiteLLM UI can save a team
  guardrail on an unlicensed proxy and brick that team — the failure only shows
  up per request. ACH is unaffected: it writes the top-level field, so it fails
  loudly at attach instead.
- **Global `default_on` guardrails are NOT gated** and run with no licence and
  no ACH configuration. Only team/key/request-scoped guardrails are premium.
  Naming a `default_on` guardrail in an Environment therefore buys nothing and
  costs you the gate above.
- Unknown guardrail names are accepted and silently never run — **fail-open**.
- Discovery needs BOTH list endpoints; neither is a superset. Measured: config
  `[]`, v2 both live guardrails.
- `mode` serialises as a bare string on one guardrail and an array on another
  in the same response.
- Opt-out (`opted_out_global_guardrails`, `disable_global_guardrails`) applies
  **only** to `default_on` guardrails; a team-attached non-default guardrail
  has no opt-out at any level.
- `x-litellm-applied-guardrails` names what actually ran, per request.
- Team metadata mixes non-string values alongside ACH's ownership markers —
  the reason `teamMetadataStrings` exists (§12).
- **Not measured**: whether `POST /team/update` omitting `guardrails` keeps
  the prior set (this doc's general "omitted = keep" claim in §? for other
  team fields) or clears it. Our unlicensed instance 403s on any non-empty
  guardrail write, so removal-by-omission was never observable end-to-end.
  ACH does not rely on this either way — `TeamUpdateRequest.Guardrails` has
  no `omitempty`; the field is always sent explicitly (`[]` when clearing).

## 12. Team metadata is a shared blob — LiteLLM writes non-string fields into it.

ACH stores its shell-team ownership markers (`ShellTeamManagedMetadataKey`,
`ShellTeamManagedEnvKey`/`UserShellManagedMetadataValue`) as string entries in
`TeamListEntry.Metadata`. That JSON object is NOT exclusive to ACH: saving a
team in the LiteLLM UI (any save, no enterprise feature required) writes
LiteLLM's own management fields into the SAME blob — `guardrails` (`[]`),
`model_rpm_limit`/`model_tpm_limit` (`{}`), `disable_global_guardrails`
(`false`), `allowed_passthrough_routes`/`opted_out_global_guardrails`/
`soft_budget_alerting_emails` (`[]`) — even when nothing enterprise-related is
configured (LiteLLM issue #20304). Measured against two live teams saved
through the UI on 2026-07-28.

Consequence: decoding the blob as `map[string]string` is wrong.
`encoding/json` fails the WHOLE document on the first type mismatch, so one UI
save of a team makes `json.Unmarshal` fail, both ownership checks
(`IsShellTeamManaged`/`IsUserShellManaged`) fall through their fail-safe `false`
branch, and the operator disowns its own shell team — recreating a duplicate on
the next reconcile, unbounded. ACH decodes into `map[string]any` and keeps only
the string-valued entries (`internal/litellm/shellteam.go`
`teamMetadataStrings`), so LiteLLM's non-string siblings are silently ignored
instead of poisoning the whole decode. Apply the same pattern to any future
code reading `TeamListEntry.Metadata` — never `map[string]string` on that
field.

## 13. The JWT `groups` claim exports team ALIASES, never shell teams.

The Forwarder's `/mcp` + `/a2a` identity JWT
(`docs/developer-guide/jwt-forwarder.md`) carries whatever LiteLLM's
`team_alias` resolves to (falling back to `team_id` when the alias is
empty) in its optional `groups` claim; ACH performs no UUID→alias
translation of its own, so an `authorizedTeams` entry authored as a raw
team UUID ships through unchanged. That's the only
identifier ACH exports to a backend for group-owned-resource authorization.
`ach-env-<name>` and `ach-user-<email>` (§6, §10) are ACH's own permission
plumbing and are filtered out before minting
(`internal/forwarder/proxy/groups.go`) — they never leave the forwarder, so
a backend never sees ACH's internal shell-team names.

## 12. Which object_permission fields can GRANT (measured against LiteLLM v1.99.1, 2026-09-09)

Read from the v1.99.1 tree, prompted by a live incident: an admin granted an
MCP server to a shell team through the LiteLLM UI, the operator reverted
`mcp_servers` on the next reconcile, and the grant kept working anyway.

`mcp_tool_permissions` is a per-server map of allowed tool names, so it reads
as a NARROWING field. It is also a GRANT: a team's allowed-server set is

    expand_permission_list(mcp_servers)
    | legacy mcp_access_groups servers
    | expand_tool_permissions(mcp_tool_permissions).keys()      # ← the grant
    | team access_group_ids → access_mcp_server_ids

(`_team_granted_servers`, `_experimental/mcp_server/auth/user_api_key_auth_mcp.py`).
An entry there reaches its server even when `mcp_servers` is the empty deny-all
list, and `get_allowed_tools_for_server` then caps that server's tools to the
map's values — so a leftover entry both **widens** the server set past what the
Environment granted and **narrows** the tools of a server the access group
granted in full. Neither is visible in the UI's "MCP Servers / Access Groups"
panel. This is why the shell team now sends `mcp_tool_permissions = {}` and
`ShellTeamDrifted` treats any content as drift.

The other unmanaged fields, and why each is safe to leave alone at v1.99.1:

| field | grants? | why ACH leaves it |
|---|---|---|
| `mcp_toolsets` | key path only | absent from `_team_granted_servers` and from the team branch of `get_allowed_tools_for_server` — inert on a TEAM |
| `models` | no | no reader in the auth path; team model access is `team.models` (already sentinelled) + access-group `access_model_names` |
| `vector_stores` | yes, but | strict allow-list: `_object_permission_allows_vector_store` returns False on empty/null, so the empty default is already closed |
| `search_tools`, `mcp_tool_search_enabled` | no | no enforcement reader in the auth path |
| `blocked_tools` | no | deny-list — a hand-edit can only restrict |

Re-check this table on a LiteLLM upgrade: `models` and `search_tools` are
inert TODAY, not inert by design.

### Access-group grants are cached per replica for 600s

`get_access_object` caches the group under `access_group_id:<id>` with
`DEFAULT_ACCESS_GROUP_CACHE_TTL` (default **600**, `litellm/constants.py`).
`PUT /v1/access_group/{id}` re-caches only on the replica that served the
write, so with N replicas an Environment's new grant is live on one pod and
absent on the others until their own TTL expires. Symptom: the operator
reports `AccessGroupSynced=True`, the group row is correct, and a share of
requests still 403 for up to ten minutes. It is cache, not the permission
model — do not go looking for a mirror bug. Deployments should pin the TTL
down (ackstorm prod runs `DEFAULT_ACCESS_GROUP_CACHE_TTL=60`).

## 14. EK effective states — what LiteLLM sees vs what ACH enforces

The console EK state model (unified-console-spec §7.1) adds two ACH-only
mechanisms — manual suspension and optional expiry — on top of the existing
revoke. None of the three new states below are anything LiteLLM knows about:
the backing LiteLLM virtual key is untouched by a suspend, a resume, or an
expiry — only ACH's own `environment_keys.status`/`expires_at` and the
resolver cache move.

| UI state | API value | Meaning | Recovery |
|---|---|---|---|
| Active | `active` | Enabled, unexpired, not revoked, and owner has Environment access. | None required. |
| Suspended | `suspended` | Manually disabled by the owner. | Explicit resume, subject to current access and validity. |
| Expired | `expired` | Optional expiration time has been reached. | Create a new credential; expiry is not editable in v1. |
| Invalid | `invalid` | Owner no longer has authorization for the Environment. | Automatically usable when access returns, unless another condition prevents it. |
| Revoked | `revoked` | Permanently withdrawn. | Cannot be recovered; create a new credential. |

Effective-state priority: **Revoked → Expired → Suspended → Invalid → Active.**
Persisted: `status ∈ {active, suspended, revoked}` + nullable `expires_at`
(migration `000022_ek_state`). `expired` and `invalid` are derived, never
written — `expired` from `expires_at` against the clock, `invalid` from the
same `authorizedTeams ∩ TeamsResolver(owner_email)` rule pk_ traffic already
runs (D-30).

**ACH-only, LiteLLM untouched:**
- Suspend/resume flips `status` and `DEL`s the resolver cache entry; no
  `POST /key/block` or `/key/update` is ever sent to LiteLLM. The backing key
  keeps answering LiteLLM's own admin/UI reads throughout — only ACH's
  resolver refuses it.
- `expires_at` is enforced by ACH alone: no LiteLLM `Duration`/`duration` is
  ever set on an `ek_` (this repo's long-standing §10 decision that an `ek_`
  has no LiteLLM-side expiry — the optional console expiry sits entirely on
  top of that, in ACH's own cache-hit and resolve-time checks).
- `invalid` never mutates the row: when access returns, the SAME key
  authorizes again on the next TeamsResolver cache refresh — no re-mint.
- No admin bypass: the caller-scoped list (`GET /platform/keys`) and resume
  (`POST .../resume`) both derive the verdict from the key OWNER's actual
  Environment membership — an admin caller's own `ek_` is never treated as
  having access it does not have.

---

## 15. Budgets live on tags (measured 2026-09-22, kind cluster, LiteLLM as shipped in `test/e2e/cluster/01-base`)

These were **measured, not derived**. Do NOT re-litigate them; do re-measure
if a LiteLLM upgrade lands.

Everything here was measured on an **unlicensed** LiteLLM (the e2e values
carry no `LITELLM_LICENSE`): tag budgets are NOT Enterprise-gated. What is
Enterprise-gated is the **key object's** `tags` field at `/key/generate`
(hence `isEnterpriseTagsRejection` in the keys handler) — a different surface
from the `x-litellm-tags` request header and the `/tag/*` + `/budget/*` APIs
this section describes.

### Why tags and not teams or users

| Fact | Evidence |
|---|---|
| A LiteLLM **user object**'s `max_budget` is **not enforced** for keys that belong to a team (neither `pk_` nor `ek_`), even with `spend > max_budget`. | 200 throughout |
| A **team** `max_budget` and a **team-member** `max_budget_in_team` DO cap keys minted into that team. Not used: tags cover the same ground and also cross Environments. | 429 measured on both |

ACH therefore writes **no `max_budget` onto any LiteLLM team or user
object**. Teams stay pure scoping (deny-all shells + access groups).

### The three ACH tag namespaces

| Tag | Caps | Written by |
|---|---|---|
| `user:<normalized email>` | the person's whole footprint — their `pk_` AND every `ek_` they own, across Environments | platform-api — `provisionUser` SEEDS it at login from `platformApi.userDefaults` (`ACH_USER_MAX_BUDGET` / `ACH_USER_BUDGET_DURATION`) **only while the tag has no budget**; an admin retunes it with `PATCH /platform/admin/users/{email}/budget` |
| `environment:<name>` | the POOLED spend of every `ek_` issued for that Environment, all owners together | the operator, from `Environment.spec.budget` (condition `BudgetSynced`) |
| `key:<ACH key id>` | one `ek_` on its own | platform-api — `POST /platform/keys {budget}` at create, `PATCH /platform/keys/{id}/budget` later |

**The chart default is a seed, not a per-login reassertion.**
`provisionUser` reads `/tag/info` first and writes `platformApi.userDefaults`
only when the tag carries no budget object — so an admin's
`PATCH /platform/admin/users/{email}/budget` survives every later login. Two
consequences: one extra `/tag/info` per login (logins are rare; the upsert
already costs three calls), and **changing the chart default no longer
retunes anyone already seeded** — that is the admin route's job. Both shapes
of "no budget" seed: no tag at all (`TagInfo` → nil) and the budgetless row
LiteLLM auto-creates from traffic. A failed `/tag/info` fails the login: it
cannot tell "unbudgeted" from "already budgeted", and either guess is worse
(seed and you clobber an admin's ceiling; skip and a new user is uncapped).

**A never-logged-in user is a legitimate target.** `UpsertTagBudget` creates
the budget object and binds the tag whether or not LiteLLM already knows the
name — and since LiteLLM auto-creates budgetless tag rows from traffic, a
tag's presence says nothing about whether the person exists. The admin route
therefore never probes for the user: pre-setting a ceiling is the only way to
cap someone from their first request.

The forwarder stamps them on EVERY authenticated request, in that order, via
`x-litellm-tags` (`internal/forwarder/proxy/tags.go`). A `pk_` carries only
`user:`; a request with no ACH identity carries none.

### Enforcement

| Fact | Evidence |
|---|---|
| A tag budget is a **budget object**: `LiteLLM_TagTable.budget_id` → `LiteLLM_BudgetTable {max_budget, budget_duration, budget_reset_at, soft_budget, tpm_limit, rpm_limit, max_parallel_requests}`. `/tag/info` returns it nested as `litellm_budget_table`. | `SELECT tag_name, budget_id FROM "LiteLLM_TagTable"` → non-null FK; `/tag/info` body |
| Tags stamped via the `x-litellm-tags` header are enforced on `/v1/chat/completions`, the Gemini-compatible `/v1beta/models/<m>:generateContent`, the `/gemini` passthrough, and `/mcp/<server>` tools/call. | 429 on all four with a tag over budget |
| Enforcement runs **before** the upstream call (a `/gemini` passthrough that would 500 upstream still 429s). | 429 vs 500 baseline |
| Several tags on one request are enforced **independently and in parallel**; the first tag whose spend exceeds its budget blocks, with **no hierarchy**. Raising that tag's budget unblocks; the next tag to cross then blocks. | user=10/env=0.6/key=1.1, cost 0.25/call: blocked at 0.75 by `environment:`; env→5 unblocks; blocked again at 1.25 by `key:`; user→0.1 blocks immediately |
| The comparison is `spend > max_budget` (**post-paid**): the request that crosses the line is served, the NEXT one is refused with `{"detail":"Budget has been exceeded! Tag=<tag> Current cost: <x>, Max budget <y>"}` (HTTP 429). | measured body |
| Budget changes take effect in ≈10 s (cache) on an idle proxy, no key or team touch needed. A **raised** ceiling took **longer than 30 s** to start serving again on the e2e box — allow ~2 min before calling an unblock broken. | measured |
| Tag spend accumulates in `LiteLLM_DailyTagSpend`; the `LiteLLM_TagTable.spend` column is NOT the enforcement counter. | forced `TagTable.spend` had no effect; organic `DailyTagSpend` did |
| MCP calls can carry cost: register the server with `mcp_info.mcp_server_cost_info.default_cost_per_query` (or `tool_name_to_cost_per_query`) and each `tools/call` books that amount to key + tag spend. Without it an MCP call costs 0 and consumes no budget. `PUT /v1/mcp/server` updates a registration **in place** (same `server_id`), so pricing an existing server does not disturb an access group that binds it by id. | key spend 0.5 after 2 calls at 0.25 |
| **On the e2e cluster a costed MCP server is the ONLY priced path.** `ach-mock` answers every completion with `usage {0,0,0}`, so `/v1` traffic books spend **0** however much you send — `LiteLLM_DailyTagSpend` showed 86 api_requests at spend 0 — and `spend > max_budget` is then false even against a ceiling of 0. A budget test that drives `demo-model` can never go 429. | `select tag, spend, api_requests from "LiteLLM_DailyTagSpend"` |
| `max_budget: 0` is storable and bindable — `/budget/new` returns `max_budget: 0.0` and `/tag/info` reports it on the bound tag. 0 is a real "refuse everything once any spend lands" ceiling, not a synonym for unset. | measured |
| LiteLLM's `x-litellm-api-key` header requires the `Bearer ` prefix on `/mcp` (the forwarder already does this, `proxy.go`). | 401 "Malformed API Key" without it |

### Writing a budget: the friendly-id upsert (`internal/litellm/tags.go`)

Every ACH budget object uses **the tag name as its `budget_id`**
(`user:<email>`, `environment:<env>`, `key:<id>`) so `/budget/list` is
legible instead of a wall of uuids.

| Probe | Result |
|---|---|
| `POST /budget/new {"budget_id":"user:probe@kilgore.trout", "max_budget":5, "budget_duration":"30d"}` | accepted — `:` and `@` are legal in a `budget_id` |
| `POST /budget/new` on an existing id | HTTP **400** `{"detail":{"error":"Budget with id 'X' already exists."}}` |
| `POST /budget/update {"budget_id":"<missing>"}` | HTTP **200**, body `null` — a silent no-op. **New-first is mandatory**; update-first would quietly do nothing. |
| `POST /tag/new {"name":N,"budget_id":N}` | binds; `/tag/info` then reports `litellm_budget_table.budget_id == N` |
| `POST /tag/update {"name":N,"budget_id":N}` | HTTP **500** — `BudgetNewRequest() got multiple values` when the budget exists, `Foreign key constraint failed on LiteLLM_TagTable_budget_id` when it does not. **An existing tag can NEVER be re-bound to a budget object.** |
| `POST /tag/update {"name":N,"max_budget":9}` (inline) | succeeds and mints a **uuid** budget — the path ACH deliberately does NOT take |
| `POST /tag/info` on a tag LiteLLM does not know | HTTP **500** with `{"detail":"404: Tags not found: ['<tag>']"}` — **not** a 200 with an empty map. The handler's 404 is re-wrapped as a 500, so neither the status nor an error string that excludes the body carries the 404: the body is the only signal (`internal/litellm.tagAbsent`). Reading this as an outage breaks `UpsertTagBudget` for **every** tag that does not exist yet — which is every tag the first time ACH budgets it. |
| `POST /tag/delete {"name":N}` | deletes the tag row only; a linked budget row is **orphaned** — always pair it with `POST /budget/delete` |
| `POST /budget/delete {"id":N}` | deletes it; a repeat returns `null` with no error (idempotent) |

**Tags auto-create from traffic, budgetless.** LiteLLM writes a tag row the
first time a name appears in `x-litellm-tags` (description *"This is just a
spend tag that was passed dynamically in a request. It does not control any
LLM models."*). So "the tag already exists without our budget" is the NORMAL
case, not an edge case. Because `/tag/update` cannot bind a budget, such a
tag must be **deleted and recreated** to receive one — which
`UpsertTagBudget` does. **Spend survives the rebind**: enforcement counts
`LiteLLM_DailyTagSpend`, keyed by tag name in its own table.

The resulting upsert, in order: `POST /budget/new` (fallback
`POST /budget/update`) → `POST /tag/info` → return if already bound → else
`POST /tag/delete` → `POST /tag/new {name, budget_id}`. Steady state is
three calls.

### Lifecycle

- **Environment delete** reaps `environment:<name>` AND its budget object,
  unconditionally — not gated on `spec.budget`, because the budgetless row
  LiteLLM auto-created from traffic must go too. It also deletes the legacy
  bare `<env>` tag; the two names are different tags, do not conflate them.
- **`ek_` revoke** reaps `key:<id>` and its budget object, best-effort: the
  credential is already dead, so a failed tag delete never fails the revoke.
  Both the owner-scoped (`DELETE /platform/keys/{id}`) and the admin routes go
  through `litellm.DeleteKeyBudget`. The operator's **orphan reaper** does
  NOT: it revokes LiteLLM keys it finds unmanaged, by LiteLLM key id, and has
  no ACH `key_id` to build the tag name from — a key reaped that way leaves
  its `key:<id>` tag and budget object behind. Harmless (nothing stamps that
  tag once the key is gone) but not tidy.
- **Dropping `spec.budget`** from a live Environment does NOT remove the
  ceiling — nothing deletes the tag until the Environment is deleted.
- Nothing reaps a `user:<email>` tag; a user's ceiling outlives their keys,
  which is the point of it.

### What the console shows

`GET /platform/console/stats` reports `budget` from the caller's OWN
`user:<email>` tag (`source: "tag"`), read with the MASTER client —
`/tag/info` is an admin route and the tag is ACH-owned metadata about the
caller, not a LiteLLM user read. A failed read degrades to
`source: "unknown"` and never fails the response. The §10.4 `team_member`
source is gone: it was unreachable in ACH's topology.

## 16. `model_group_alias` is invisible to every catalog endpoint (measured 2026-09-23, api.ackstorm.ai + kind cluster)

A LiteLLM `model_group_alias` routes requests but **does not appear in any
model catalog LiteLLM serves**. Measured on the ackstorm proxy against two
alias names known to work for inference (`gemini-3.7-flash`,
`gemini-3.8-flash`), with `gemini-flash-latest` — a genuinely registered
model — as the control:

| Read | alias name | control (`gemini-flash-latest`) |
|---|---|---|
| `GET /v1/models` | absent | present |
| `GET /model_group/info` (list) | absent | present |
| `GET /model_group/info?model_group=<name>` | **0 rows** | 1 row |
| `GET /v1/model/info` | n/a — master-key only, 0 rows for a virtual key | — |

The per-name query returning nothing is the decisive one: this is not a
listing filter or a key-scoping artifact, the alias simply is not a model
group as far as the read API is concerned.

**Where LiteLLM does declare them:** `GET /router/settings` exposes a
`model_group_alias` field among its `fields[]` descriptors. Confirmed on the
e2e cluster with the master key (value `{}` there — no aliases configured).
The endpoint is **proxy-admin only**: a non-admin virtual key gets 401, not
404. Siblings in the same family (`/model/settings`, `/get/config/callbacks`,
`/config/field/info`) behave the same way.

**Why ACH cares.** The Environment reconciler resolves
`spec.runtime.models` by exact string match against the Snapshotter's set,
which is built from `GET /v1/model/info` → `model_name`
(`internal/litellm/model.go`, `internal/snapshot/snapshot.go`). An
Environment naming an alias therefore reports
`ExecutionResourcesResolved=False` forever, however well the alias works for
inference. **This is cosmetic**, and the blast radius is smaller than it
looks: `reconcileAccessGroup` never inspects `spec.runtime.models` (it
writes the names through unfiltered), `ek_` creation gates on
`AccessGroupSynced` alone, and neither existing traffic nor hydrate is
affected. Only the rolled-up `Available` goes False.

**ACH's chosen answer is NOT to read the alias table.** Aliases are a `/v1`
concern (LiteLLM resolves them there itself). For the `/gemini` native
route, where the model rides in the URL path, the forwarder strips
configured vendor prefixes instead — see `forwarder.gemini.
stripModelPrefixes` in CLAUDE.md. Environments should name the REAL
registered model (`<vendor>.<model>`), which resolves cleanly.
