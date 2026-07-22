# Cap Personal Keys with a per-user deny-all shell team

Design spec. Follow-up to #159 (v0.6.17, "cap Environment Keys with a
per-Environment deny-all shell team"). #159 is correct and verified on the live
cluster; nothing in it is undone here. This is the symmetric half for **Personal
Keys (`pk_`)**, plus the operator-side attachment that makes one PK cover the
union of a user's entitlements.

## Problem (measured)

`internal/platformapi/auth/sso.go:435` mints the PK with **no `team_id`**. On the
live proxy the resulting key (`pkid_01ky4s6xw2r33m8pgzxs7vcx4a`,
`ai.run@ackstorm.com`, member of teams `default` + `run`) has `team_id: None`,
`models: []`, `access_group_ids: []`, `object_permission: null`,
`expires: None`. A teamless PK is broken in **both** directions at once:

| check | observed | expected |
|---|---|---|
| `claude-sonnet-5` | **allowed** (fail-open) | denied — no team of the user grants `anthropic` |
| MCP tools | **0** | the tools of the environments the user is entitled to |
| agents | **7**, `architecture-reviewer` invoked 200 (fail-open) | only the entitled `a2aAgents` |

It exceeds the user on models + agents (empty lists fail OPEN) and reaches
nothing on MCP (the environment access groups are attached to `run`/`dream`; the
key is in neither). Same failure class the EK shell fixed in #159.

The measured LiteLLM semantics this rests on are recorded in
`references/litellm-permission-model.md` (do not re-derive): access groups only
ADD; a team is the only reliable ceiling; a key has exactly one `team_id`;
`GET /team/info` is the one read that resolves `object_permission` and member
lists; groups stack/compose across teams (agents/tools union) and resolve
per-request (attach/detach is immediate, no cache window); deleting a team does
NOT revoke immediately (~60s window); putting a key in an access group via
`assigned_key_ids` collapses its agent set to EVERY agent (Hazard 1 — never do
it); LiteLLM silently accepts a `team_id` that does not exist and the key
behaves fail-open (Hazard 4).

## Design

Teams are identity containers; access groups carry every grant. Symmetric to
#159:

| object | one per | purpose | holds |
|---|---|---|---|
| `ach-env-<environment>` | `Environment` | deny-all shell | `ek_` — #159 |
| `ach-user-<email>` | user (lazy, on first `pk_`) | deny-all shell | `pk_` — this change |
| access group `<environment>` | `Environment` | the env's grants | no keys, ever |

A PK sits in its deny-all `ach-user-<email>` shell (models `["__deny_all__"]`,
`object_permission.agents ["00000000-…-0"]`, empty MCP lists — same sentinels as
the env shell, same reason). It reaches nothing until the operator attaches the
access group of each Environment the user is entitled to onto that shell; the key
then inherits the union of those grants through the group→team mirror. Because a
key has exactly one `team_id`, a user whose several teams are authorized for
different Environments cannot be served by dropping the PK into one human team —
the per-user shell is precisely what lets one PK cover the union.

### The `assigned_team_ids` union is a pure function, computed only in the operator

`assigned_team_ids` is a whole-list PUT. For Environment `E` the operator always
rebuilds it whole:

```
desired(E.assigned_team_ids) =
    { resolved ids of E.spec.authorizedTeams }   # human teams — unchanged, keeps lk-* keys working
  ∪ { ach-env-<E> }                              # the env shell — #159
  ∪ { ach-user-<m> : m ∈ members(E.authorizedTeams), the shell exists }
```

**The operator is the sole writer of `assigned_team_ids`** (Option A). This
dissolves the lost-update race (Hazard 3) outright — there is no second writer,
so the spec's "serialise / RMW-retry" is unnecessary. `reconcileAccessGroup`
already builds this union for the first two sets; this change adds the third.

Membership is resolved **live** each reconcile, and cheaply, on one measured
fact: `provisionUser` sets the LiteLLM `user_id = email`
(`sso.go:617`). So for each authorized team, `GET /team/info` yields members
whose `user_id` **is** the email — no `/user/info` fan-out. Derive
`ach-user-<email>`, confirm the shell exists in the `ListAllTeams` `byAlias`
map already read this pass (respects lazy creation + Hazard 4), add its id.
Live resolution means **removal converges**: a user dropped from an authorized
team disappears from the next reconcile's union → the PUT detaches their shell →
their PK loses the grant immediately (measured: detach is per-request).

`ponytail: +len(authorizedTeams) GET /team/info per env reconcile. authorizedTeams
is 2-3 today; batch/cache if it ever grows large.`

### platform-api provisions the shell at mint (Option b), never touches access groups

A `pk_` shell has no CR, so the operator has no per-user reconcile trigger; the
only event that says "this user needs a shell" is the login, in platform-api.
So platform-api, in `mintAndPersistPK`, before `KeyGenerate`:

1. normalise the email (lower-case, trim),
2. **ensure the shell**: `CreateTeam(NewUserShellRequest(email))` with
   `team_id = team_alias = ach-user-<email>`; treat the `400 "already exists"`
   as success (the id is deterministic — it is the alias). Any other error →
   503, no key minted (a key with no live shell is fail-open — Hazard 4).
3. mint with `team_id = ach-user-<email>` **and** the expiry fix below.

platform-api computes no entitlement and writes no access group. Its new work is
one idempotent `CreateTeam` — the same class of imperative LiteLLM call it
already makes at login (`UserNew`, `TeamMemberAdd`).

### Setting `team_id` explicitly (both shell kinds)

Adopt alitellm-operator's convention: pass `team_id` on `POST /team/new` equal to
the alias (measured accepted verbatim, including `.`/`@`:
`ach-user-ai.run@ackstorm.com`). For **user** shells this is load-bearing — two
concurrent first-logins would otherwise mint two UUID shells for one user, and
the operator (first-wins) would attach grants to only one, stranding the other's
PK deny-all. A deterministic id + idempotent create de-races it.

For **env** shells the convention is adopted going forward too (removes the
`byAlias`-miss duplicate-create hazard at `environment_controller.go:780`). No
code migration: the six existing UUID env shells are deleted **by hand** by the
operator (JC), and the reconciler recreates them under the alias convention with
the same idempotent-create handling.

### The expiry fix

`KeyGenerate` currently mints with `expires: None`; only the ACH `personal_keys`
row carries `pkExpiryWindow` (7d). Add a duration field to `KeyGenerateRequest`
and set it = `pkExpiryWindow` so the LiteLLM key does not outlive the ACH record
(`ponytail:` send hours, e.g. `168h`, to avoid day-unit math — verify the unit
against the deployed LiteLLM).

## Changes

**`internal/litellm`**
- `usershell.go` (or extend `shellteam.go`): `UserShellPrefix = "ach-user-"`,
  `UserShellAlias(email)` (normalised), `NewUserShellRequest(email)`
  (`team_id`+alias set, deny-all sentinels), ownership metadata
  `ach_managed=user-shell` + `ach_user=<email>`, `IsUserShellManaged` /
  `IsUserShellShaped`. **DRY**: the deny-all `object_permission` block, the
  `__deny_all__`/nil-UUID sentinels, and `ShellTeamDrifted` (alias-agnostic —
  reads only Models + ObjectPermission) are shared with the env shell, not
  duplicated. Factor a small shared `denyAllTeamRequest(alias, teamID, metadata)`
  builder both `NewShellTeamRequest`/`NewUserShellRequest` wrap.
- `NewShellTeamRequest`: set `TeamID = ShellTeamAlias(env.Name)` (env shells now
  carry the alias id too). `NewTeamRequest.TeamID` already exists.
- `errors_team.go`: `IsDuplicateTeamErr(err)` — mirror `IsDuplicateUserErr`
  (`errors_user.go`): 400/409 + body "already exists".
- `types.go`: `KeyGenerateRequest` gains `Duration string json:"duration,omitempty"`.
  `TeamListEntry` decodes members: `MembersWithRoles []{ UserID string
  json:"user_id" } json:"members_with_roles,omitempty"` + a `MemberEmails()`
  helper (user_id == email). Verify the `/team/info` member shape against real
  LiteLLM.
- `NoopClient`/fakes: keep compiling (the 6th integration-tagged fake too — the
  Task-2 lesson from #159).

**`internal/platformapi/auth/sso.go`** — `mintAndPersistPK`: ensure user shell
(idempotent, `IsDuplicateTeamErr` → id = alias), set `TeamID` + `Duration` on
`KeyGenerateRequest`. On shell-ensure failure → 503, no mint.

**`internal/controller/ach/environment_controller.go`** — `reconcileAccessGroup`:
after the authorizedTeams→ids + env-shell union, for each authorized team's
resolved id call `GetTeamInfo` → member emails → `UserShellAlias` → add the id
**iff** present in `byAlias`. `GetTeamInfo` error → `resolveFailed` (requeue —
never PUT a union missing shells, which would wrongly detach entitled users).

**No change** to `reconcileDeletion` (user shells are not env-owned; an env's
group delete simply drops the grant from every attached user shell) or to the
`ek_` path.

**Docs (same commit):** `references/litellm-permission-model.md` §10 (was "pk_
fail-open residual" → now "pk_ capped by ach-user-<email>", symmetric to ek_) +
a user-shell section; `CLAUDE.md` shell-team bullet; `references/understanding.md`
pk_ contract; `references/troubleshooting.md` (deny-all window / fail-open PK).

## Decisions locked

- **Human teams untouched** (Hazard 2). The union only ADDS user shells;
  `authorizedTeams` contributes exactly as today, so the `lk-*` keys keep MCP.
- **Migration of the 6 env shells: none in code.** JC deletes the UUID shells by
  hand; the operator recreates them under `team_id=alias`.
- **Attachment is operator-only** (Option A) → Hazard 3 dissolved.
- **Shell provisioning at mint is platform-api's** (Option b) — the absence of a
  user CR forces the create to originate from the login event.
- **Lazy shells** — created only when a PK is minted; no login ⇒ no shell.
- **First-ever PK is deny-all until the next reconcile (≤5 min, fail-closed,
  one-time).** Later logins reuse the already-attached shell. No trigger.
  `ponytail: upgrade to a NOTIFY-driven targeted reconcile (Option C) if
  first-login latency bites.`
- **User shells are never deleted.** Losing entitlement detaches the shell from
  the env group (immediate revoke); the deny-all key then reaches nothing.
  Offboarding-delete and a `personal_keys`-watching operator loop are deferred.

## Hazards (from the spec, addressed)

1. **Never `assigned_key_ids`** — keys live only in their team; grants reach them
   via group→team. Never add a key to a group. ✓
2. **`lk-*` keys** — human teams stay in the union. ✓
3. **Lost-update race** — one writer (operator). ✓
4. **Phantom `team_id` fail-open** — platform-api ensures the shell exists before
   minting; the operator only adds shells present in `byAlias`. ✓

## Residual — existing teamless PKs (LOCKED: leave, document only)

PKs minted before this change keep `team_id=NULL` and `expires: None`, so they
stay fail-open until revoked out-of-band — and the expiry fix here applies only
to keys minted *after* it, so those pre-change keys do not churn out on their
own. **Decision (JC, iii): leave them, document only** — no code migration and no
one-shot revoke sweep. The change caps every *new* PK; the pre-change fail-open
keys are an accepted, documented residual, revocable by hand if/when wanted (and
the deferred `personal_keys` watch could sweep them later). Record this plainly
in `references/litellm-permission-model.md` §10 so it is not mistaken for a bug.

## Verification

Unit: user-shell serialize (team_id=alias, sentinels), `IsDuplicateTeamErr`,
drift reuse, member decode; sso mint (ensure-shell + team_id + duration; dup
handled; ensure-fail → 503); reconcile includes entitled user shells, converges
on member removal, `GetTeamInfo` error → `resolveFailed`.

E2E against real LiteLLM (mirror the spec's checks) — for a PK minted for a user
entitled to `<env>`:
- a model in `<env>.runtime.models` → 200; a model no entitlement grants (e.g.
  `claude-sonnet-5` for a `run` member) → `team_model_access_denied`;
- `GET /mcp-rest/tools/list` = the union of the entitled envs' `mcpServers`
  catalogs, nothing else (compare per-server catalogs, not substrings — several
  servers share generic tool names);
- `GET /v1/agents` = exactly the entitled `a2aAgents`;
  `POST /a2a/{unentitled}/message/send` denied;
- after removing the user from an env's entitlement, the very next request is
  denied, other envs untouched.

## Out of scope / deferred

- `personal_keys`-watching operator loop (would let the operator own shell
  creation + key-move + attachment fully, and could carry the residual
  migration). Add "later if needed."
- Option C (NOTIFY-driven immediate attach) to close the first-login window.
- Offboarding user-shell deletion.
