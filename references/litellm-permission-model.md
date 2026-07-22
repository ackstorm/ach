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

    models                                = ["__deny_all__"]
    object_permission.mcp_servers         = []
    object_permission.mcp_access_groups   = []
    object_permission.agents              = ["00000000-0000-0000-0000-000000000000"]
    object_permission.agent_access_groups = []

These sentinels cover four axes — `models`, `mcp_servers`, `mcp_access_groups`,
and `agents` (+ `agent_access_groups`). `object_permission`'s other fields
(`vector_stores`, `mcp_tool_permissions`, `mcp_toolsets`, `blocked_tools`,
`search_tools`, `mcp_tool_search_enabled`) are left alone (§9).

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
ACH manages only the four it measured; the rest are left alone.

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
- The environment's grants live only in the access group; the shell team is
  constant boilerplate, identical for every environment.
- `pk_` keys carry no team (`internal/platformapi/auth/sso.go`) and are
  therefore fail-open on models per §4; MCP and agents still scope correctly
  through the access group (`spec.authorizedTeams` → `assigned_team_ids`
  extends what members of those teams can reach from their personal key). This
  is a known, deliberate scope decision as of v0.6.17, not a property to rely
  on.
- ACH must track which EKs belong to an environment so it can enumerate them at
  deletion time (it does: `environment_keys.environment`). Do not rely on
  `team_id` for that mapping — it disappears with the team.
