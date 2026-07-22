# Environment Keys via a per-Environment deny-all shell team — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cap every Environment Key (`ek_`) with a dedicated LiteLLM team (`ach-env-<environment>`) that denies everything by default, so an EK reaches exactly the models / MCP servers / A2A agents its Environment grants — instead of today's broken `access_groups` field that LiteLLM silently ignores and leaves the key fail-open on models.

**Architecture:** LiteLLM access groups only ever ADD permissions; a team is the only thing that reliably caps a key. The Environment reconciler therefore also reconciles a **deny-all shell team** per Environment — no grants of its own, only sentinels (`models: ["__deny_all__"]`, `object_permission.agents: ["00000000-0000-0000-0000-000000000000"]`, empty MCP lists) — and attaches it to the environment's access group exactly like any authorized team. The environment's real grants stay in the access group (one copy, one drift surface) and reach the key through the group→team mirror. Platform-API then mints EKs with `team_id: <shell team>` and nothing else: no `models`, no `object_permission`, no `access_group_ids`, and the key is never added to `assigned_key_ids`. The Personal Key (`pk_`) path is unchanged.

**Tech Stack:** Go 1.2x, controller-runtime, chi, pgx/v5, LiteLLM proxy admin API (`/team/*`, `/key/*`, `/v1/access_group`).

## Global Constraints

- **Shell team alias:** `ach-env-<environment-name>` — exact, no namespace, no suffix.
- **Sentinels are not optional.** An empty/absent `models` list means EVERY model; an empty/absent `agents` list means EVERY agent. Both fail open. `mcp_servers` is the exception — empty fails closed. Values, verbatim:
  - models sentinel: `__deny_all__`
  - agent sentinel: `00000000-0000-0000-0000-000000000000`
- **Never add an EK to the access group.** No `assigned_key_ids` writes, no `access_group_ids` on the key. Beyond being redundant it triggers LiteLLM's agent-permission collapse bug (key with a team AND a group whose agent lists differ ⇒ effective agent set becomes EVERY agent on the proxy).
- **`assigned_team_ids` is a whole-list PUT serving two purposes** (authorized teams for the pk_ path, the shell for the ek_ path). Always rebuild the full union; a reconcile driven by a change to `spec.authorizedTeams` must not drop the shell and vice versa.
- **Environment deletion order is load-bearing** — `POST /key/delete` for every EK of the environment FIRST, then the access group, then `POST /team/delete` for the shell. Deleting the team first leaves keys answering 200 for up to ~60s (LiteLLM key-cache TTL) while `key/delete`, `key/block` and `key/update` all return 404 — an unrevocable live key. Never report a revocation that returned 404 in that window as success.
- **Pre-existing `ek_` keys are NOT migrated** (decision 2026-07-21). They keep `team_id = NULL` and stay fail-open on models until revoked and re-issued. This must be stated in the docs, not silently left out.
- **Shell teams are NOT hidden** from the admin runtime catalog / team lists (decision 2026-07-21). No filtering anywhere.
- Every new `*.go` file starts with `// SPDX-License-Identifier: Apache-2.0`.
- Docs are updated in the SAME commit as the behaviour change (repo doc-hygiene rule).
- All build/test commands auto-route into the devtools container. Never prefix a `make` target with `./scripts/dev.sh`.

## Background: what was measured on the live proxy (2026-07-21)

These are measurements, not inferences. Task 1 captures them permanently.

- A key's effective permissions = union of its team's grants and every access group it reaches. There is no deny list and no intersection semantics anywhere.
- A group has two faces; only the mirror enforces. `access_group.assigned_team_ids` is the group side; `team.access_group_ids` is the mirror, and enforcement reads the mirror. LiteLLM writes the mirror only for teams that ENTER or LEAVE the group side, so an idempotent PUT of an unchanged list cannot repair a broken mirror (the v0.6.16 two-PUT delta repair exists for exactly this).
- `key.models` restricts correctly, but key-level MCP restriction is rejected outright: `POST /key/generate {"team_id":"run","object_permission":{"mcp_servers":["mcp-slack"]}}` → `403 Key requests MCP servers not allowed by team 'run': ['mcp-slack']. Team allows: []`. LiteLLM validates a key's MCP request against `team.object_permission` ONLY — servers the team reaches through access groups do not count.
- A teamless key: models ALL allowed (a group does not narrow it), MCP tools 0 outside a group / exactly the group's servers inside it, agents 7 (all) outside / exactly the group's agent inside it. So MCP and agents scope correctly; models stay wide open. **That is today's ek_.**
- Shell team WITHOUT sentinels (`models: []`, no agents key): bedrock + openrouter allowed — fail open. Shell team WITH sentinels: only the environment's model answers 200, the others return `team_model_access_denied`; MCP = exactly the environment's servers; agents = exactly the environment's agent, others denied.
- LiteLLM agent-permission bug: when a key has a team AND belongs to an access group whose agent list differs from the team's, the effective agent set collapses to every agent on the proxy (measured: an agent granted by neither returned 200 on `POST /a2a/{id}/message/send`). Identical lists ⇒ no bug. This is why EKs must never join the group.

## Current-state facts (verified in this tree, 2026-07-21)

- `internal/platformapi/envkeys/handler.go:425` sends `AccessGroups: []string{env.Name}` → serialises as `access_groups`, a field LiteLLM does not accept (the real one is `access_group_ids`), carrying environment NAMES where UUIDs are required. Silently ignored today.
- `internal/controller/ach/environment_controller.go:693-754` already resolves mcpServers / a2aAgents / authorizedTeams names → IDs and fails the reconcile loudly with `AccessGroupSynced=False reason=UnresolvedReferences` on any miss. **Spec requirement 5 is already satisfied — no work needed.**
- The plugin resync sweep is already gated: `pluginCh`/`mpCh` stay nil when `featuregate.PluginsEnabled=false` (`cmd/ach/cmd/operator.go:435-444`) and `sweepKind` returns before `List` on a nil channel (`internal/operator/resync/runnable.go:126-129`, present since v0.6.5). **The "also pending" item in the spec is already done — no work needed.**
- `litellm.RESTClient.makeRequest` treats **DELETE + 404 as success** (`internal/litellm/restclient.go:161`). `DeleteAccessGroupByID` uses `DELETE /v1/access_group/{id}`. If the live proxy's real route is `/v1/unified_access_group/{id}`, ACH has been silently NOT deleting access groups. Task 8 verifies this against the live proxy; it is out of scope for the code change.
- `drainEkRows` (`environment_controller.go:1171`) only flips DB rows to `revoked`; it never calls LiteLLM. Task 5 adds the actual revocation.
- `CreateTeam` exists on `*RESTClient` but is NOT on the `litellm.Client` interface. `UpdateTeam` / `DeleteTeam` do not exist at all.
- Test fakes embed `*litellm.NoopClient`, so adding methods to `NoopClient` keeps every fake compiling.

## File Structure

| File | Responsibility |
|---|---|
| `references/litellm-permission-model.md` (create) | The measured LiteLLM permission semantics — the reference that stops the next agent re-deriving this. |
| `internal/litellm/types.go` (modify) | `TeamObjectPermission`; `NewTeamRequest.ObjectPermission`; `TeamUpdateRequest`; `TeamListEntry.ObjectPermission`; `KeyGenerateRequest`: drop `AccessGroups`, add `TeamID`. |
| `internal/litellm/team.go` (modify) | `UpdateTeam` (POST /team/update), `DeleteTeam` (POST /team/delete), `GetTeamInfo` (GET /team/info — the only read carrying `object_permission`). |
| `internal/litellm/shellteam.go` (create) | The shell-team contract in ONE place: alias, sentinels, desired-state builder, drift predicate. Shared by operator + platform-api. |
| `internal/litellm/client.go` (modify) | Interface: `CreateTeam`, `UpdateTeam`, `DeleteTeam`. |
| `internal/litellm/noop.go` (modify) | Stubs for the three new interface methods. |
| `internal/connection/client.go` (modify) | Delegations for the three new interface methods. |
| `internal/controller/ach/environment_shellteam.go` (create) | `ensureShellTeam` + `deleteShellTeam` + the `ShellTeamFailed` condition. |
| `internal/controller/ach/environment_controller.go` (modify) | Wire the shell into `reconcileAccessGroup`'s team union; new deletion order + `revokeEnvironmentKeys`. |
| `internal/platformapi/teams/lookup.go` (modify) | `LookupTeamIDByAlias`. |
| `internal/platformapi/envkeys/handler.go` (modify) | Mint the EK into the shell team; drop the bogus `access_groups`. |
| `CLAUDE.md`, `references/troubleshooting.md` (modify) | Doc hygiene, in the same commits. |

---

### Task 1: Capture the measured LiteLLM permission model as a reference

**Files:**
- Create: `references/litellm-permission-model.md`
- Modify: `CLAUDE.md` (MANDATORY Reading Table + the `references/` list in the intro blockquote)

No test — this is documentation. Its "test" is that the next task's implementer can read it and not need the live proxy.

- [ ] **Step 1: Write the reference doc**

Create `references/litellm-permission-model.md` with exactly this content:

```markdown
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
- `pk_` keys are unchanged: `spec.authorizedTeams` → `assigned_team_ids` extends
  what members of those teams can reach from their personal key, which is what
  access groups are good at, and agents scope correctly on that path.
- ACH must track which EKs belong to an environment so it can enumerate them at
  deletion time (it does: `environment_keys.environment`). Do not rely on
  `team_id` for that mapping — it disappears with the team.
```

- [ ] **Step 2: Index it in CLAUDE.md**

In the intro blockquote listing `references/`, add `litellm-permission-model.md` to the list. In the MANDATORY Reading Table, add this row immediately after the `Debugging a service/domain failure` row:

```markdown
| LiteLLM teams / access groups / key scoping | `references/litellm-permission-model.md` (measured semantics — do NOT re-derive) |
```

- [ ] **Step 3: Commit**

```bash
git add references/litellm-permission-model.md CLAUDE.md
git commit -m "docs(litellm): record the measured team/access-group permission model"
```

---

### Task 2: LiteLLM client — team object_permission, team update/delete, key team_id

**Files:**
- Modify: `internal/litellm/types.go`
- Modify: `internal/litellm/team.go`
- Modify: `internal/litellm/client.go`
- Modify: `internal/litellm/noop.go`
- Modify: `internal/connection/client.go`
- Modify: `internal/platformapi/envkeys/handler.go:425` (drop the now-deleted field so the tree compiles)
- Test: `internal/litellm/team_test.go` (append)

**Interfaces:**
- Produces:
  - `type TeamObjectPermission struct { MCPServers, MCPAccessGroups, Agents, AgentAccessGroups []string }`
  - `NewTeamRequest.ObjectPermission *TeamObjectPermission`
  - `type TeamUpdateRequest struct { TeamID string; Models []string; ObjectPermission *TeamObjectPermission }`
  - `TeamListEntry.ObjectPermission *TeamObjectPermission`
  - `KeyGenerateRequest.TeamID string` (and `KeyGenerateRequest.AccessGroups` is REMOVED)
  - `Client.CreateTeam(ctx, *NewTeamRequest) (*TeamListEntry, error)`
  - `Client.UpdateTeam(ctx, *TeamUpdateRequest) (*TeamListEntry, error)`
  - `Client.DeleteTeam(ctx, teamID string) error`
  - `Client.GetTeamInfo(ctx, teamID string) (*TeamListEntry, error)` — the ONLY read that carries `object_permission`

- [ ] **Step 1: Write the failing tests**

Append to `internal/litellm/team_test.go`:

```go
// TestUpdateTeamRequestBody asserts POST /team/update carries the team_id,
// the models sentinel, and the full object_permission block — the deny-all
// shell-team contract. A dropped object_permission key would silently leave
// the team fail-open on agents.
func TestUpdateTeamRequestBody(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"team_id":"t-1","team_alias":"ach-env-demo"}`))
	}))
	defer srv.Close()

	c := NewRESTClient(srv.URL, "sk-master", logr.Discard())
	out, err := c.UpdateTeam(context.Background(), &TeamUpdateRequest{
		TeamID:           "t-1",
		Models:           []string{"__deny_all__"},
		ObjectPermission: &TeamObjectPermission{Agents: []string{"00000000-0000-0000-0000-000000000000"}},
	})
	if err != nil {
		t.Fatalf("UpdateTeam: %v", err)
	}
	if out.TeamID != "t-1" {
		t.Fatalf("TeamID = %q, want t-1", out.TeamID)
	}
	if gotPath != "/team/update" {
		t.Fatalf("path = %q, want /team/update", gotPath)
	}
	if gotBody["team_id"] != "t-1" {
		t.Fatalf("body team_id = %v, want t-1", gotBody["team_id"])
	}
	op, ok := gotBody["object_permission"].(map[string]any)
	if !ok {
		t.Fatalf("body has no object_permission object: %v", gotBody)
	}
	agents, _ := op["agents"].([]any)
	if len(agents) != 1 || agents[0] != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("object_permission.agents = %v, want the nil-UUID sentinel", op["agents"])
	}
	// mcp_servers must serialise even when empty — absent means "every server"
	// on some LiteLLM paths, and we always want the explicit closed list.
	if _, present := op["mcp_servers"]; !present {
		t.Fatalf("object_permission.mcp_servers missing from body: %v", op)
	}
}

// TestDeleteTeamRequestBody asserts POST /team/delete sends {"team_ids":[id]}.
func TestDeleteTeamRequestBody(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewRESTClient(srv.URL, "sk-master", logr.Discard())
	if err := c.DeleteTeam(context.Background(), "t-9"); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	if gotPath != "/team/delete" {
		t.Fatalf("path = %q, want /team/delete", gotPath)
	}
	ids, _ := gotBody["team_ids"].([]any)
	if len(ids) != 1 || ids[0] != "t-9" {
		t.Fatalf("team_ids = %v, want [t-9]", gotBody["team_ids"])
	}
}

// TestGetTeamInfoDecodesEnvelopeAndFlat asserts both response shapes decode:
// LiteLLM wraps the team under "team_info", but a flat body must not break us.
// GET /team/info is the ONLY read that carries object_permission — the team
// LIST endpoints serialise it as null — so shell-team drift detection depends
// on this decoding correctly.
func TestGetTeamInfoDecodesEnvelopeAndFlat(t *testing.T) {
	bodies := map[string]string{
		"envelope": `{"team_id":"t-1","team_info":{"team_id":"t-1","team_alias":"ach-env-demo","models":["__deny_all__"],"object_permission":{"mcp_servers":[],"agents":["00000000-0000-0000-0000-000000000000"]}}}`,
		"flat":     `{"team_id":"t-1","team_alias":"ach-env-demo","models":["__deny_all__"],"object_permission":{"mcp_servers":[],"agents":["00000000-0000-0000-0000-000000000000"]}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			var gotQuery string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.Query().Get("team_id")
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			c := NewRESTClient(srv.URL, "sk-master", logr.Discard())
			got, err := c.GetTeamInfo(context.Background(), "t-1")
			if err != nil {
				t.Fatalf("GetTeamInfo: %v", err)
			}
			if gotQuery != "t-1" {
				t.Fatalf("team_id query = %q, want t-1", gotQuery)
			}
			if got.TeamAlias != "ach-env-demo" {
				t.Fatalf("TeamAlias = %q", got.TeamAlias)
			}
			if got.ObjectPermission == nil || len(got.ObjectPermission.Agents) != 1 {
				t.Fatalf("ObjectPermission = %+v, want the agent sentinel", got.ObjectPermission)
			}
		})
	}
}

// TestDeleteTeamRejectsEmptyID guards the "delete every team" footgun.
func TestDeleteTeamRejectsEmptyID(t *testing.T) {
	c := NewRESTClient("http://127.0.0.1:1", "sk-master", logr.Discard())
	if err := c.DeleteTeam(context.Background(), ""); err == nil {
		t.Fatal("DeleteTeam(\"\") = nil, want error")
	}
}

// TestKeyGenerateRequestCarriesTeamID asserts the ek_ mint shape: team_id
// present, access_groups gone (LiteLLM never accepted that field).
func TestKeyGenerateRequestCarriesTeamID(t *testing.T) {
	b, err := json.Marshal(&KeyGenerateRequest{UserID: "u@example.com", TeamID: "t-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["team_id"] != "t-1" {
		t.Fatalf("team_id = %v, want t-1", got["team_id"])
	}
	if _, present := got["access_groups"]; present {
		t.Fatalf("access_groups must not be sent: %v", got)
	}
}
```

Check the existing imports at the top of `team_test.go` and add whatever of
`context`, `encoding/json`, `net/http`, `net/http/httptest`, `testing`,
`github.com/go-logr/logr` is missing. If the file's existing tests construct the
REST client with a different helper than `NewRESTClient(url, key, logger)`, use
that same helper — grep the file first and match it.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
make test-unit-pkg PKG=./internal/litellm/
```
Expected: FAIL — `c.UpdateTeam undefined`, `c.DeleteTeam undefined`, `unknown field TeamID in struct literal`.

- [ ] **Step 3: Add the types**

In `internal/litellm/types.go`, add after `TeamListEntry`:

```go
// TeamObjectPermission is LiteLLM's per-team `object_permission` block —
// the ONLY place LiteLLM enforces a key's MCP and agent ceiling from
// (measured 2026-07-21: key-level object_permission on /key/generate is
// rejected outright, and servers a team reaches through access groups do
// not count towards it).
//
// No field carries omitempty: an empty `agents` list means EVERY agent and
// an absent one means the same, so the deny-all shell team MUST transmit
// its lists explicitly. See references/litellm-permission-model.md §5.
type TeamObjectPermission struct {
	MCPServers        []string `json:"mcp_servers"`
	MCPAccessGroups   []string `json:"mcp_access_groups"`
	Agents            []string `json:"agents"`
	AgentAccessGroups []string `json:"agent_access_groups"`
}

// TeamUpdateRequest is the POST /team/update request body. Only the fields
// ACH manages are modelled; every other team property is left untouched by
// omission (LiteLLM treats absent fields as "keep").
type TeamUpdateRequest struct {
	TeamID           string                `json:"team_id"`
	Models           []string              `json:"models,omitempty"`
	ObjectPermission *TeamObjectPermission `json:"object_permission,omitempty"`
}
```

In the same file, add to `NewTeamRequest` (after `Tags`):

```go
	ObjectPermission *TeamObjectPermission `json:"object_permission,omitempty"`
```

Add to `TeamListEntry` (after `AccessGroupIDs`):

```go
	// ObjectPermission is the read-back of the team's MCP/agent ceiling.
	// LiteLLM versions differ on whether the list endpoints inline it or
	// return only an object_permission_id — a nil here means "the response
	// did not carry it", NOT "the team has no permissions", so drift
	// detection must treat nil as unverifiable rather than as drift.
	ObjectPermission *TeamObjectPermission `json:"object_permission,omitempty"`
```

In `KeyGenerateRequest`: DELETE the `AccessGroups []string` field and its paragraph in the doc comment ("AccessGroups carries the LiteLLM access-group name list …"). Add in its place:

```go
	TeamID       string            `json:"team_id,omitempty"`
```

and this paragraph in the doc comment:

```go
// TeamID binds the key to a LiteLLM team, which is the ONLY reliable
// ceiling on a key (measured: a teamless key is fail-open on models, and
// an access group can never narrow it). ek_ creation passes the
// environment's deny-all shell team; nothing else is set. Never also put
// the key in an access group — see references/litellm-permission-model.md §7.
```

- [ ] **Step 4: Add the client methods**

In `internal/litellm/team.go`, append:

```go
// UpdateTeam issues POST /team/update. ACH uses it only to re-assert the
// deny-all shell-team sentinels when a read-back shows they drifted;
// omitted fields are left untouched by LiteLLM.
func (c *RESTClient) UpdateTeam(ctx context.Context, req *TeamUpdateRequest) (*TeamListEntry, error) {
	if req == nil || req.TeamID == "" {
		return nil, fmt.Errorf("litellm: UpdateTeam: empty team_id")
	}
	raw, err := c.makeRequest(ctx, "POST", "/team/update", req)
	if err != nil {
		return nil, err
	}
	var out TeamListEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("litellm: decode POST /team/update: %w", err)
	}
	return &out, nil
}

// DeleteTeam issues POST /team/delete with {"team_ids": [teamID]}.
//
// Deleting a team CASCADES to its keys, so this is a real revocation — the
// Environment finalizer calls it only AFTER revoking the environment's ek_
// keys individually (references/litellm-permission-model.md §8: deleting the
// team first leaves keys serving traffic for ~60s with no route to revoke
// them). The empty-id guard exists because an empty list is a foot-gun.
func (c *RESTClient) DeleteTeam(ctx context.Context, teamID string) error {
	if teamID == "" {
		return fmt.Errorf("litellm: DeleteTeam: empty team_id")
	}
	_, err := c.makeRequest(ctx, "POST", "/team/delete", map[string]any{"team_ids": []string{teamID}})
	return err
}

// GetTeamInfo issues GET /team/info?team_id=<id>.
//
// This is the ONLY LiteLLM read that resolves a team's object_permission: the
// list endpoints (/v2/team/list, /team/list) omit the relation and serialise
// it as null, carrying only object_permission_id. Shell-team drift detection
// therefore cannot use the list pass.
//
// LiteLLM nests the team under "team_info"; the flat form is accepted too so a
// version difference degrades to "no drift detected" instead of a decode error.
func (c *RESTClient) GetTeamInfo(ctx context.Context, teamID string) (*TeamListEntry, error) {
	if teamID == "" {
		return nil, fmt.Errorf("litellm: GetTeamInfo: empty team_id")
	}
	raw, err := c.makeRequest(ctx, "GET", "/team/info?team_id="+url.QueryEscape(teamID), nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		TeamInfo *TeamListEntry `json:"team_info"`
	}
	if uerr := json.Unmarshal(raw, &envelope); uerr == nil && envelope.TeamInfo != nil {
		return envelope.TeamInfo, nil
	}
	var flat TeamListEntry
	if uerr := json.Unmarshal(raw, &flat); uerr != nil {
		return nil, fmt.Errorf("litellm: decode GET /team/info: %w", uerr)
	}
	return &flat, nil
}
```

`net/url` is already imported in `team.go`.

- [ ] **Step 5: Extend the interface, the noop, and the connection wrapper**

In `internal/litellm/client.go`, add to the `Client` interface (next to `EnsureDefaultTeam`):

```go
	// CreateTeam issues POST /team/new. Used by the Environment reconciler
	// to provision the per-Environment deny-all shell team that caps ek_.
	CreateTeam(ctx context.Context, req *NewTeamRequest) (*TeamListEntry, error)

	// UpdateTeam issues POST /team/update — shell-team sentinel repair.
	UpdateTeam(ctx context.Context, req *TeamUpdateRequest) (*TeamListEntry, error)

	// DeleteTeam issues POST /team/delete. Cascades to the team's keys.
	DeleteTeam(ctx context.Context, teamID string) error

	// GetTeamInfo issues GET /team/info?team_id= — the only read that
	// resolves object_permission (the list endpoints return it as null).
	GetTeamInfo(ctx context.Context, teamID string) (*TeamListEntry, error)
```

In `internal/litellm/noop.go`, add stubs in the file's existing style:

```go
// CreateTeam is a stub — Phase 1 logs and returns a synthetic entry so the
// caller's control flow is exercised without LiteLLM connectivity.
func (c *NoopClient) CreateTeam(_ context.Context, req *NewTeamRequest) (*TeamListEntry, error) {
	alias := ""
	if req != nil {
		alias = req.TeamAlias
	}
	c.Log.Info("stub: would create LiteLLM team", "alias", alias)
	return &TeamListEntry{TeamID: alias, TeamAlias: alias}, nil
}

// UpdateTeam is a stub — logs and echoes the requested team.
func (c *NoopClient) UpdateTeam(_ context.Context, req *TeamUpdateRequest) (*TeamListEntry, error) {
	id := ""
	if req != nil {
		id = req.TeamID
	}
	c.Log.Info("stub: would update LiteLLM team", "team_id", id)
	return &TeamListEntry{TeamID: id}, nil
}

// DeleteTeam is a stub — logs and returns nil.
func (c *NoopClient) DeleteTeam(_ context.Context, teamID string) error {
	c.Log.Info("stub: would delete LiteLLM team", "team_id", teamID)
	return nil
}

// GetTeamInfo is a stub — returns an entry with no ObjectPermission, which
// callers must read as "unverifiable", not as "no permissions".
func (c *NoopClient) GetTeamInfo(_ context.Context, teamID string) (*TeamListEntry, error) {
	c.Log.Info("stub: would read LiteLLM team info", "team_id", teamID)
	return &TeamListEntry{TeamID: teamID}, nil
}
```

In `internal/connection/client.go`, add the three delegations in the file's existing style:

```go
func (c *Client) CreateTeam(ctx context.Context, req *litellm.NewTeamRequest) (*litellm.TeamListEntry, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.CreateTeam(ctx, req)
}

func (c *Client) UpdateTeam(ctx context.Context, req *litellm.TeamUpdateRequest) (*litellm.TeamListEntry, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.UpdateTeam(ctx, req)
}

func (c *Client) DeleteTeam(ctx context.Context, teamID string) error {
	client, err := c.current()
	if err != nil {
		return err
	}
	return client.DeleteTeam(ctx, teamID)
}

func (c *Client) GetTeamInfo(ctx context.Context, teamID string) (*litellm.TeamListEntry, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.GetTeamInfo(ctx, teamID)
}
```

- [ ] **Step 6: Unbreak the one caller of the removed field**

In `internal/platformapi/envkeys/handler.go`, delete line `AccessGroups: []string{env.Name},` from the `keyReq` literal (Task 6 replaces it with `TeamID`; for now the field just goes away). Also drop the now-false clause in the comment above it — change

```go
	// attribution. AccessGroups=[<environment>] +
	// Tags=[<environment>] per §6.3 ek_ Environment tag;
	// MaxBudget=nil per KEY-10.
```
to
```go
	// attribution. Tags=[<environment>] per §6.3 ek_ Environment tag;
	// MaxBudget=nil per KEY-10.
```
and in the same file's `CreateHandler` doc comment (step 7 of the 8-step list) change `ACH supplies AccessGroups=[<env>] + MaxBudget=nil (KEY-10).` to `ACH supplies MaxBudget=nil (KEY-10).`

- [ ] **Step 7: Run the tests to verify they pass**

```bash
make test-unit-pkg PKG=./internal/litellm/
make test-unit-pkg PKG=./internal/connection/
make test-unit-pkg PKG=./internal/platformapi/envkeys/
```
Expected: PASS for all three.

- [ ] **Step 8: Lint + commit**

```bash
make qa-lint-changed
git add internal/litellm internal/connection internal/platformapi/envkeys/handler.go
git commit -m "feat(litellm): team object_permission, team update/delete, key team_id"
```

---

### Task 3: The shell-team contract in one place

**Files:**
- Create: `internal/litellm/shellteam.go`
- Test: `internal/litellm/shellteam_test.go`

**Interfaces:**
- Consumes: `TeamObjectPermission`, `NewTeamRequest`, `TeamListEntry` (Task 2).
- Produces:
  - `func ShellTeamAlias(env string) string`
  - `const ShellTeamPrefix = "ach-env-"`
  - `const ShellTeamDenyAllModel = "__deny_all__"`
  - `const ShellTeamDenyAllAgent = "00000000-0000-0000-0000-000000000000"`
  - `func ShellTeamPermissions() *TeamObjectPermission`
  - `func NewShellTeamRequest(env string) *NewTeamRequest`
  - `func ShellTeamDrifted(e TeamListEntry) bool`

- [ ] **Step 1: Write the failing tests**

Create `internal/litellm/shellteam_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import "testing"

func TestShellTeamAlias(t *testing.T) {
	if got := ShellTeamAlias("platform"); got != "ach-env-platform" {
		t.Fatalf("ShellTeamAlias = %q, want ach-env-platform", got)
	}
}

// TestNewShellTeamRequestSentinels is the load-bearing test of this change:
// empty model/agent lists fail OPEN in LiteLLM, so the request must carry
// both sentinels and explicit (non-nil) empty MCP lists.
func TestNewShellTeamRequestSentinels(t *testing.T) {
	req := NewShellTeamRequest("demo")
	if req.TeamAlias != "ach-env-demo" {
		t.Fatalf("TeamAlias = %q", req.TeamAlias)
	}
	if len(req.Models) != 1 || req.Models[0] != ShellTeamDenyAllModel {
		t.Fatalf("Models = %v, want [%s]", req.Models, ShellTeamDenyAllModel)
	}
	op := req.ObjectPermission
	if op == nil {
		t.Fatal("ObjectPermission is nil — the team would allow every agent")
	}
	if len(op.Agents) != 1 || op.Agents[0] != ShellTeamDenyAllAgent {
		t.Fatalf("Agents = %v, want [%s]", op.Agents, ShellTeamDenyAllAgent)
	}
	if op.MCPServers == nil || len(op.MCPServers) != 0 {
		t.Fatalf("MCPServers = %v, want an explicit empty list", op.MCPServers)
	}
	if op.MCPAccessGroups == nil || len(op.MCPAccessGroups) != 0 {
		t.Fatalf("MCPAccessGroups = %v, want an explicit empty list", op.MCPAccessGroups)
	}
	if op.AgentAccessGroups == nil || len(op.AgentAccessGroups) != 0 {
		t.Fatalf("AgentAccessGroups = %v, want an explicit empty list", op.AgentAccessGroups)
	}
}

func TestShellTeamDrifted(t *testing.T) {
	healthy := TeamListEntry{
		Models:           []string{ShellTeamDenyAllModel},
		ObjectPermission: ShellTeamPermissions(),
	}
	if ShellTeamDrifted(healthy) {
		t.Fatal("healthy shell reported as drifted")
	}

	cases := map[string]TeamListEntry{
		"models cleared (fail-open on every model)": {
			Models:           []string{},
			ObjectPermission: ShellTeamPermissions(),
		},
		"a real model was granted directly": {
			Models:           []string{"gemini.gemini-flash-latest"},
			ObjectPermission: ShellTeamPermissions(),
		},
		"agents cleared (fail-open on every agent)": {
			Models:           []string{ShellTeamDenyAllModel},
			ObjectPermission: &TeamObjectPermission{Agents: []string{}},
		},
		"an mcp server was granted directly": {
			Models: []string{ShellTeamDenyAllModel},
			ObjectPermission: &TeamObjectPermission{
				MCPServers: []string{"mcp-slack"},
				Agents:     []string{ShellTeamDenyAllAgent},
			},
		},
	}
	for name, e := range cases {
		if !ShellTeamDrifted(e) {
			t.Errorf("%s: reported as healthy, want drifted", name)
		}
	}

	// A read-back that carries NEITHER field is unverifiable, not drifted —
	// reporting drift there would make the operator write on every reconcile
	// forever against a LiteLLM whose list endpoint omits object_permission.
	if ShellTeamDrifted(TeamListEntry{TeamID: "t-1", TeamAlias: "ach-env-demo"}) {
		t.Fatal("unverifiable read-back reported as drifted")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
make test-unit-pkg PKG=./internal/litellm/
```
Expected: FAIL — `undefined: ShellTeamAlias`, `undefined: NewShellTeamRequest`, `undefined: ShellTeamDrifted`.

- [ ] **Step 3: Write the implementation**

Create `internal/litellm/shellteam.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import "slices"

// The per-Environment deny-all SHELL TEAM is the ceiling on an Environment
// Key. LiteLLM access groups only ever ADD permissions — a team is the only
// thing that reliably caps a key (references/litellm-permission-model.md).
//
// The shell carries NO grants of its own. The Environment's real grants stay
// in its access group, which is attached to the shell exactly like any
// authorized team; the key inherits them through the group→team mirror. That
// keeps one copy of the three lists and one drift surface.
const (
	// ShellTeamPrefix namespaces ACH-owned shell teams inside LiteLLM's flat
	// team-alias space.
	ShellTeamPrefix = "ach-env-"

	// ShellTeamDenyAllModel is a model name that must never exist upstream.
	// An empty `models` list means EVERY model, so "deny all" has to be
	// spelled as "allow exactly this one impossible model".
	ShellTeamDenyAllModel = "__deny_all__"

	// ShellTeamDenyAllAgent is the same trick for agents: an empty or absent
	// `agents` list means every agent, so the list carries the nil UUID.
	// alitellm-operator applies the same sentinel to its teams.
	ShellTeamDenyAllAgent = "00000000-0000-0000-0000-000000000000"
)

// ShellTeamAlias is the LiteLLM team alias for an Environment's shell team.
func ShellTeamAlias(env string) string { return ShellTeamPrefix + env }

// ShellTeamPermissions is the deny-all object_permission block. MCP lists are
// explicit empties (mcp_servers is the one dimension that fails CLOSED when
// empty); the agent list carries the sentinel because empty fails OPEN.
func ShellTeamPermissions() *TeamObjectPermission {
	return &TeamObjectPermission{
		MCPServers:        []string{},
		MCPAccessGroups:   []string{},
		Agents:            []string{ShellTeamDenyAllAgent},
		AgentAccessGroups: []string{},
	}
}

// NewShellTeamRequest is the POST /team/new body for an Environment's shell.
func NewShellTeamRequest(env string) *NewTeamRequest {
	return &NewTeamRequest{
		TeamAlias:        ShellTeamAlias(env),
		Models:           []string{ShellTeamDenyAllModel},
		ObjectPermission: ShellTeamPermissions(),
	}
}

// ShellTeamDrifted reports whether a shell team read back from LiteLLM has
// lost its sentinels (someone edited the team by hand, or a LiteLLM upgrade
// rewrote it). Any drift is a fail-OPEN condition, so the caller repairs it.
//
// A field the read-back did not carry is treated as UNVERIFIABLE, not as
// drift: some LiteLLM versions return only an object_permission_id from the
// list endpoints, and reporting drift there would turn every reconcile into a
// pointless write.
func ShellTeamDrifted(e TeamListEntry) bool {
	if e.Models != nil && !slices.Equal(e.Models, []string{ShellTeamDenyAllModel}) {
		return true
	}
	op := e.ObjectPermission
	if op == nil {
		return false
	}
	return len(op.MCPServers) != 0 ||
		len(op.MCPAccessGroups) != 0 ||
		len(op.AgentAccessGroups) != 0 ||
		!slices.Equal(op.Agents, []string{ShellTeamDenyAllAgent})
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
make test-unit-pkg PKG=./internal/litellm/
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make qa-lint-changed
git add internal/litellm/shellteam.go internal/litellm/shellteam_test.go
git commit -m "feat(litellm): shell-team contract (alias, sentinels, drift predicate)"
```

---

### Task 4: Operator — reconcile the shell team and put it in `assigned_team_ids`

**Files:**
- Create: `internal/controller/ach/environment_shellteam.go`
- Modify: `internal/controller/ach/environment_controller.go` (`reconcileAccessGroup`, ~lines 702-755)
- Test: `internal/controller/ach/environment_shellteam_test.go`
- Modify: `internal/controller/ach/access_group_fake_test.go` (record team create/update)

**Interfaces:**
- Consumes: `litellm.ShellTeamAlias`, `litellm.NewShellTeamRequest`, `litellm.ShellTeamPermissions`, `litellm.ShellTeamDrifted`, `litellm.TeamUpdateRequest`, `Client.GetTeamInfo` (Tasks 2-3).
- Produces: `func (r *EnvironmentReconciler) ensureShellTeam(ctx context.Context, env *achv1alpha1.Environment, existingID string, logger logr.Logger) (string, error)` and the `AccessGroupSynced=False reason=ShellTeamFailed` condition.

**Why `existingID string` and not the list entry:** `GET /v2/team/list` serialises
`object_permission` as `null` (it does not resolve the relation), so the list pass
can only tell us WHETHER the shell exists. Verifying its sentinels needs one
`GET /team/info?team_id=` — done here, once per reconcile, only when the shell
already exists.

- [ ] **Step 1: Teach the envtest fake to record team writes**

In `internal/controller/ach/access_group_fake_test.go`, add these fields to `accessGroupFakeImpl`:

```go
	// Shell-team state: alias → entry, plus call counters so a test can
	// assert create-once / repair-on-drift.
	teamsByID       map[string]litellm.TeamListEntry
	teamCreateCalls map[string]int
	teamUpdateCalls map[string]int
	teamDeleteCalls map[string]int
	lastTeamCreate  map[string]litellm.NewTeamRequest
	teamCreateErr   error
```

Initialise all five maps in `newAccessGroupFake()` (same style as the existing map initialisers) and reset them in `Reset()` alongside the others. Then add the methods:

```go
func (f *accessGroupFakeImpl) CreateTeam(_ context.Context, req *litellm.NewTeamRequest) (*litellm.TeamListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.teamCreateErr != nil {
		return nil, f.teamCreateErr
	}
	f.teamCreateCalls[req.TeamAlias]++
	f.lastTeamCreate[req.TeamAlias] = *req
	id := "id-" + req.TeamAlias
	entry := litellm.TeamListEntry{
		TeamID:           id,
		TeamAlias:        req.TeamAlias,
		Models:           req.Models,
		ObjectPermission: req.ObjectPermission,
	}
	f.teamsByID[id] = entry
	f.teamsByAlias[req.TeamAlias] = []litellm.TeamListEntry{entry}
	return &entry, nil
}

func (f *accessGroupFakeImpl) UpdateTeam(_ context.Context, req *litellm.TeamUpdateRequest) (*litellm.TeamListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamUpdateCalls[req.TeamID]++
	entry := f.teamsByID[req.TeamID]
	entry.TeamID = req.TeamID
	entry.Models = req.Models
	entry.ObjectPermission = req.ObjectPermission
	f.teamsByID[req.TeamID] = entry
	if entry.TeamAlias != "" {
		f.teamsByAlias[entry.TeamAlias] = []litellm.TeamListEntry{entry}
	}
	return &entry, nil
}

func (f *accessGroupFakeImpl) GetTeamInfo(_ context.Context, teamID string) (*litellm.TeamListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.teamsByID[teamID]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

func (f *accessGroupFakeImpl) DeleteTeam(_ context.Context, teamID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamDeleteCalls[teamID]++
	entry := f.teamsByID[teamID]
	delete(f.teamsByID, teamID)
	delete(f.teamsByAlias, entry.TeamAlias)
	return nil
}
```

The fake's `ListAllTeams` must return every entry in `teamsByAlias` (check how it is implemented today — if it only serves `teamsByAlias`, seeding `teamsByAlias` in `CreateTeam` as above is enough; if `ListAllTeams` is inherited from `NoopClient` and returns empty, override it here to flatten `teamsByAlias` into a slice, because the reconciler resolves the shell through `ListAllTeams`).

- [ ] **Step 2: Write the failing test**

Create `internal/controller/ach/environment_shellteam_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"testing"

	"github.com/go-logr/logr"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
)

// TestEnsureShellTeamCreatesWithSentinels: absent shell → one POST /team/new
// carrying both sentinels, and the returned id is the created team's.
func TestEnsureShellTeamCreatesWithSentinels(t *testing.T) {
	fake := newAccessGroupFake()
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	id, err := r.ensureShellTeam(context.Background(), env, "", logr.Discard())
	if err != nil {
		t.Fatalf("ensureShellTeam: %v", err)
	}
	if id != "id-ach-env-demo" {
		t.Fatalf("team id = %q, want id-ach-env-demo", id)
	}
	if fake.teamCreateCalls["ach-env-demo"] != 1 {
		t.Fatalf("CreateTeam calls = %d, want 1", fake.teamCreateCalls["ach-env-demo"])
	}
	req := fake.lastTeamCreate["ach-env-demo"]
	if len(req.Models) != 1 || req.Models[0] != litellm.ShellTeamDenyAllModel {
		t.Fatalf("Models = %v, want the deny-all sentinel", req.Models)
	}
	if req.ObjectPermission == nil || len(req.ObjectPermission.Agents) != 1 {
		t.Fatalf("ObjectPermission = %+v, want the nil-UUID agent sentinel", req.ObjectPermission)
	}
}

// TestEnsureShellTeamIsIdempotent: a healthy existing shell triggers no write.
func TestEnsureShellTeamIsIdempotent(t *testing.T) {
	fake := newAccessGroupFake()
	fake.teamsByID["t-existing"] = litellm.TeamListEntry{
		TeamID:           "t-existing",
		TeamAlias:        "ach-env-demo",
		Models:           []string{litellm.ShellTeamDenyAllModel},
		ObjectPermission: litellm.ShellTeamPermissions(),
	}
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	id, err := r.ensureShellTeam(context.Background(), env, "t-existing", logr.Discard())
	if err != nil {
		t.Fatalf("ensureShellTeam: %v", err)
	}
	if id != "t-existing" {
		t.Fatalf("team id = %q, want t-existing", id)
	}
	if fake.teamCreateCalls["ach-env-demo"] != 0 || fake.teamUpdateCalls["t-existing"] != 0 {
		t.Fatalf("healthy shell was rewritten: create=%d update=%d",
			fake.teamCreateCalls["ach-env-demo"], fake.teamUpdateCalls["t-existing"])
	}
}

// TestEnsureShellTeamRepairsDrift: a shell whose sentinels were cleared is
// fail-open, so it must be repaired with one POST /team/update.
func TestEnsureShellTeamRepairsDrift(t *testing.T) {
	fake := newAccessGroupFake()
	fake.teamsByID["t-drifted"] = litellm.TeamListEntry{
		TeamID:           "t-drifted",
		TeamAlias:        "ach-env-demo",
		Models:           []string{},
		ObjectPermission: &litellm.TeamObjectPermission{Agents: []string{}},
	}
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	if _, err := r.ensureShellTeam(context.Background(), env, "t-drifted", logr.Discard()); err != nil {
		t.Fatalf("ensureShellTeam: %v", err)
	}
	if fake.teamUpdateCalls["t-drifted"] != 1 {
		t.Fatalf("UpdateTeam calls = %d, want 1", fake.teamUpdateCalls["t-drifted"])
	}
	got := fake.teamsByID["t-drifted"]
	if len(got.Models) != 1 || got.Models[0] != litellm.ShellTeamDenyAllModel {
		t.Fatalf("repaired Models = %v", got.Models)
	}
	if got.ObjectPermission == nil || len(got.ObjectPermission.Agents) != 1 {
		t.Fatalf("repaired ObjectPermission = %+v", got.ObjectPermission)
	}
}
```

Construct the Environment inline in each test as shown; do not add a shared helper for two lines.

- [ ] **Step 3: Run the test to verify it fails**

```bash
make test-unit-pkg PKG=./internal/controller/ach/ FOCUS=TestEnsureShellTeam
```
If that target does not accept `FOCUS`, run `make test-unit-pkg PKG=./internal/controller/ach/`.
Expected: FAIL — `r.ensureShellTeam undefined`.

- [ ] **Step 4: Write the implementation**

Create `internal/controller/ach/environment_shellteam.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
)

// ensureShellTeam guarantees the Environment's deny-all shell team exists and
// still carries its sentinels, returning the LiteLLM team id.
//
// The shell is what actually caps an Environment Key: LiteLLM access groups
// only ADD permissions, and a key with no team is fail-open on models
// (references/litellm-permission-model.md §1, §4). The shell holds NO grants —
// the environment's access group is attached to it like any authorized team,
// so the grants stay in exactly one place.
//
// `existingID` is the team id the caller already resolved from its
// ListAllTeams pass ("" ⇒ create). The list response cannot be used to verify
// the sentinels — LiteLLM serialises object_permission as null there — so an
// existing shell costs one extra GET /team/info, and only then.
func (r *EnvironmentReconciler) ensureShellTeam(
	ctx context.Context,
	env *achv1alpha1.Environment,
	existingID string,
	logger logr.Logger,
) (string, error) {
	alias := litellm.ShellTeamAlias(env.Name)

	if existingID == "" {
		created, err := r.LiteLLM.CreateTeam(ctx, litellm.NewShellTeamRequest(env.Name))
		if err != nil {
			return "", fmt.Errorf("create shell team %s: %w", alias, err)
		}
		if created == nil || created.TeamID == "" {
			return "", fmt.Errorf("create shell team %s: LiteLLM returned no team_id", alias)
		}
		logger.Info("created deny-all shell team", "alias", alias, "id", created.TeamID)
		return created.TeamID, nil
	}

	info, ierr := r.LiteLLM.GetTeamInfo(ctx, existingID)
	if ierr != nil {
		return "", fmt.Errorf("read shell team %s: %w", alias, ierr)
	}
	if info != nil && litellm.ShellTeamDrifted(*info) {
		if _, err := r.LiteLLM.UpdateTeam(ctx, &litellm.TeamUpdateRequest{
			TeamID:           existingID,
			Models:           []string{litellm.ShellTeamDenyAllModel},
			ObjectPermission: litellm.ShellTeamPermissions(),
		}); err != nil {
			return "", fmt.Errorf("repair shell team %s: %w", alias, err)
		}
		logger.Info("repaired shell team sentinels", "alias", alias, "id", existingID)
	}
	return existingID, nil
}

// deleteShellTeam removes the Environment's shell team. Idempotent: an absent
// alias is success. MUST run AFTER the environment's ek_ keys were revoked
// individually — deleting the team first leaves its keys serving traffic for
// ~60s with no route to revoke them (references/litellm-permission-model.md §8).
func (r *EnvironmentReconciler) deleteShellTeam(
	ctx context.Context,
	env *achv1alpha1.Environment,
	logger logr.Logger,
) error {
	alias := litellm.ShellTeamAlias(env.Name)
	teams, err := r.LiteLLM.ListTeamsByAlias(ctx, alias)
	if err != nil {
		return fmt.Errorf("lookup shell team %s: %w", alias, err)
	}
	for _, t := range teams {
		if t.TeamID == "" {
			continue
		}
		if derr := r.LiteLLM.DeleteTeam(ctx, t.TeamID); derr != nil {
			return fmt.Errorf("delete shell team %s (id=%s): %w", alias, t.TeamID, derr)
		}
		logger.Info("deleted shell team", "alias", alias, "id", t.TeamID)
	}
	return nil
}

// shellTeamFailed is the closed-set condition for a shell team that could not
// be provisioned or repaired. It rides AccessGroupSynced because the shell is
// part of the same LiteLLM write: without it, ek_ minting is unsafe, so the
// Environment must not report Available.
func shellTeamFailed(env *achv1alpha1.Environment, err error) metav1.Condition {
	return metav1.Condition{
		Type:               "AccessGroupSynced",
		Status:             metav1.ConditionFalse,
		Reason:             "ShellTeamFailed",
		Message:            fmt.Sprintf("LiteLLM shell team %s: %v", litellm.ShellTeamAlias(env.Name), err),
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
}
```

- [ ] **Step 5: Wire it into `reconcileAccessGroup`**

In `internal/controller/ach/environment_controller.go`, inside `reconcileAccessGroup`:

The existing `byAlias` map (built from the `allTeams` pass, ~line 718) already
gives the shell's id when it exists — no change is needed there.

Immediately AFTER the `if len(mcpUnresolved)+len(agentUnresolved)+len(teamUnresolved) > 0 { … }` block (~line 754) — i.e. once the environment is known to be resolvable — insert:

```go
	// The deny-all shell team is what caps this Environment's ek_ keys, and
	// it joins assigned_team_ids in the SAME write as the authorized teams:
	// that list is a whole-list PUT serving both the pk_ path (authorized
	// teams) and the ek_ path (the shell), so it is always rebuilt as the
	// union — a spec change to authorizedTeams must never drop the shell.
	shellAlias := litellm.ShellTeamAlias(env.Name)
	shellID, sErr := r.ensureShellTeam(ctx, env, byAlias[shellAlias], logger)
	if sErr != nil {
		logger.Error(sErr, "shell team reconcile failed", "alias", shellAlias)
		return shellTeamFailed(env, sErr)
	}
	if !slices.Contains(teamIDs, shellID) {
		teamIDs = append(teamIDs, shellID)
	}
```

`slices` is already imported in this file. Everything downstream (create, drift, the two-PUT mirror repair) needs no change: a freshly created shell has no `access_group_ids`, so `mirrorRepairStep` sees it as missing and generates the ENTER delta on the final PUT.

(c) Extend the closed-set list in `reconcileAccessGroup`'s doc comment with:

```go
//   - False/ShellTeamFailed        — the per-Environment deny-all shell team
//     (ach-env-<name>) could not be created or repaired. ek_ keys minted
//     against a missing shell would be fail-open on models, so the
//     Environment must not go Available.
```

- [ ] **Step 6: Run the tests to verify they pass**

```bash
make test-unit-pkg PKG=./internal/controller/ach/
```
Expected: PASS (including the pre-existing access-group tests — if one now fails because `assigned_team_ids` gained the shell id, UPDATE that test's expectation to include `id-ach-env-<env>`; that is the intended new behaviour, not a regression).

- [ ] **Step 7: Envtest**

```bash
make test-envtest-fast
```
Expected: PASS. If an existing envtest asserts an exact `AssignedTeamIDs` slice, extend it with the shell id.

- [ ] **Step 8: Commit**

```bash
make qa-lint-changed
git add internal/controller/ach/
git commit -m "feat(operator): reconcile a deny-all shell team per Environment"
```

---

### Task 5: Operator — revoke EKs first, then group, then shell team

**Files:**
- Modify: `internal/controller/ach/environment_controller.go` (`reconcileDeletion` ~line 508; add `revokeEnvironmentKeys`)
- Test: `internal/controller/ach/environment_shellteam_test.go` (append)

**Interfaces:**
- Consumes: `deleteShellTeam` (Task 4), `litellm.IsHTTPNotFound`, `Client.RevokeKey`.
- Produces: `func (r *EnvironmentReconciler) revokeEnvironmentKeys(ctx context.Context, env *achv1alpha1.Environment, logger logr.Logger) error`

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/ach/environment_shellteam_test.go`:

```go
// TestReconcileDeletionOrder asserts the load-bearing LiteLLM call order on
// Environment deletion: keys → access group → shell team. Deleting the team
// first would leave its keys answering 200 for ~60s with no route to revoke
// them (references/litellm-permission-model.md §8).
func TestReconcileDeletionOrder(t *testing.T) {
	fake := newAccessGroupFake()
	// Seed a shell team so deleteShellTeam has something to delete.
	if _, err := fake.CreateTeam(context.Background(), litellm.NewShellTeamRequest("demo")); err != nil {
		t.Fatalf("seed CreateTeam: %v", err)
	}
	fake.order = nil

	r := &EnvironmentReconciler{LiteLLM: fake} // DB nil ⇒ no ek_ rows to revoke
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	if err := r.revokeEnvironmentKeys(context.Background(), env, logr.Discard()); err != nil {
		t.Fatalf("revokeEnvironmentKeys: %v", err)
	}
	if err := r.LiteLLM.DeleteAccessGroup(context.Background(), env.Name); err != nil {
		t.Fatalf("DeleteAccessGroup: %v", err)
	}
	if err := r.deleteShellTeam(context.Background(), env, logr.Discard()); err != nil {
		t.Fatalf("deleteShellTeam: %v", err)
	}

	if len(fake.order) == 0 || fake.order[len(fake.order)-1] != "DeleteTeam" {
		t.Fatalf("call order = %v, want DeleteTeam last", fake.order)
	}
	for i, call := range fake.order {
		if call == "DeleteTeam" {
			for _, later := range fake.order[i+1:] {
				if later == "RevokeKey" || later == "DeleteAccessGroup" {
					t.Fatalf("call order = %v, want every revoke/group delete BEFORE DeleteTeam", fake.order)
				}
			}
		}
	}
}
```

For this to compile, add to `accessGroupFakeImpl` in `access_group_fake_test.go` an `order []string` field, append the method name at the top of `CreateTeam`/`UpdateTeam`/`DeleteTeam`/`DeleteAccessGroup`/`RevokeKey` (add a `RevokeKey` override that records and returns nil), and reset `order` in `Reset()`. Follow the existing mutex discipline.

- [ ] **Step 2: Run the test to verify it fails**

```bash
make test-unit-pkg PKG=./internal/controller/ach/
```
Expected: FAIL — `r.revokeEnvironmentKeys undefined`.

- [ ] **Step 3: Write `revokeEnvironmentKeys`**

Add to `internal/controller/ach/environment_shellteam.go`:

```go
// revokeEnvironmentKeys revokes every ACTIVE ek_ of this Environment in
// LiteLLM, before anything else in the deletion sequence.
//
// Order is a correctness property, not bookkeeping: deleting the shell team
// (or the access group) first leaves recently-used keys serving traffic until
// LiteLLM's key cache expires (~60s), and inside that window key/delete,
// key/block and key/update all return 404 — the key cannot be revoked by any
// route. Revoking first makes it immediate and verifiable.
//
// A confirmed 404 here (key already gone from LiteLLM, e.g. deleted
// out-of-band) is logged and skipped so a stale row cannot wedge the finalizer
// forever. ANY other error aborts the deletion and requeues: reporting a
// revocation that did not happen is the one outcome that must never occur.
func (r *EnvironmentReconciler) revokeEnvironmentKeys(
	ctx context.Context,
	env *achv1alpha1.Environment,
	logger logr.Logger,
) error {
	if r.DB == nil {
		return nil
	}
	rows, err := r.DB.Query(ctx,
		`SELECT key_id, litellm_token FROM environment_keys
		  WHERE environment=$1 AND status='active' AND litellm_token IS NOT NULL`,
		env.Name,
	)
	if err != nil {
		return classifyDrainErr("ek_ revoke SELECT", err)
	}
	type ekRow struct{ keyID, token string }
	var pending []ekRow
	for rows.Next() {
		var e ekRow
		if scanErr := rows.Scan(&e.keyID, &e.token); scanErr != nil {
			rows.Close()
			return classifyDrainErr("ek_ revoke scan", scanErr)
		}
		pending = append(pending, e)
	}
	rows.Close()
	if rerr := rows.Err(); rerr != nil {
		return classifyDrainErr("ek_ revoke SELECT", rerr)
	}

	revoked := 0
	for _, e := range pending {
		if rvErr := r.LiteLLM.RevokeKey(ctx, e.token); rvErr != nil {
			if litellm.IsHTTPNotFound(rvErr) {
				logger.Info("ek_ absent in LiteLLM at revoke time; skipping",
					"env", env.Name, "key_id", e.keyID)
				continue
			}
			return fmt.Errorf("revoke ek_ %s: %w", e.keyID, rvErr)
		}
		revoked++
	}
	logger.Info("revoked environment keys in LiteLLM",
		"env", env.Name, "revoked", revoked, "total", len(pending))
	return nil
}
```

- [ ] **Step 4: Re-order `reconcileDeletion`**

In `internal/controller/ach/environment_controller.go`, replace the body of `reconcileDeletion` between the finalizer check and `drainEkRows` with:

```go
	// Order is load-bearing — see revokeEnvironmentKeys / §8 of
	// references/litellm-permission-model.md. Keys first, then the group,
	// then the shell team.
	if err := r.revokeEnvironmentKeys(ctx, env, logger); err != nil {
		return ctrl.Result{}, fmt.Errorf("§6.5 step 1 revokeEnvironmentKeys: %w", err)
	}
	if err := r.LiteLLM.DeleteAccessGroup(ctx, env.Name); err != nil {
		return ctrl.Result{}, fmt.Errorf("§6.5 step 2 DeleteAccessGroup: %w", err)
	}
	if err := r.deleteShellTeam(ctx, env, logger); err != nil {
		return ctrl.Result{}, fmt.Errorf("§6.5 step 2b deleteShellTeam: %w", err)
	}
	if err := r.LiteLLM.DeleteTag(ctx, env.Name); err != nil {
		return ctrl.Result{}, fmt.Errorf("§6.5 step 3 DeleteTag: %w", err)
	}
	if err := r.drainEkRows(ctx, env); err != nil {
		return ctrl.Result{}, err
	}
```

Also update the `reconcileDeletion` doc comment ("Runs delete-side-effects on LiteLLM, drains ek_ rows, …") to name the new order: `revokes the environment's ek_ keys in LiteLLM, deletes the access group, deletes the deny-all shell team, deletes the tag, drains ek_ rows, soft-deletes the projection row, then removes the finalizer.`

- [ ] **Step 5: Run the tests to verify they pass**

```bash
make test-unit-pkg PKG=./internal/controller/ach/
make test-envtest-fast
```
Expected: PASS.

- [ ] **Step 6: Document the failure mode + commit**

In `references/troubleshooting.md`, add this section immediately before `### ❌ Hydrate output ≠ examples/hydrate.json`:

```markdown
### ❌ A deleted Environment's `ek_` still answers 200 for up to a minute

Only possible if the shell team (`ach-env-<env>`) was deleted BEFORE its keys —
by hand, or by a build predating v0.6.17. LiteLLM caches key rows for ~60s, and
inside that window the key cannot be revoked by any route: `POST /key/delete`
(by `keys` and by `tokens`), `POST /key/block` and `POST /key/update` all return
404 while the key still serves traffic. The key keeps the deleted team's
restrictions, so it is a revocation-LATENCY problem, not privilege escalation.

There is no fix from outside — wait out the cache. Never read those 404s as
"already revoked": the operator logs `ek_ absent in LiteLLM at revoke time` only
for keys it confirmed gone BEFORE touching the team.

Since v0.6.17 the finalizer order is: revoke every `ek_` → delete the access
group → delete the shell team, so the window does not occur on a normal delete.
```

```bash
git add internal/controller/ach/ references/troubleshooting.md
git commit -m "fix(operator): revoke environment keys before deleting group and shell team"
```

---

### Task 6: Platform-API — mint `ek_` into the shell team

**Files:**
- Modify: `internal/platformapi/teams/lookup.go`
- Modify: `internal/platformapi/envkeys/handler.go` (`mintAndInsert`, ~line 414-450; helper near `isEnterpriseTagsRejection` ~line 916)
- Test: `internal/platformapi/teams/lookup_test.go` (append), `internal/platformapi/envkeys/handler_test.go` (append)

**Interfaces:**
- Consumes: `litellm.ShellTeamAlias` (Task 3), `KeyGenerateRequest.TeamID` (Task 2).
- Produces: `func LookupTeamIDByAlias(ctx context.Context, ll litellm.Client, alias string) (string, error)` — returns `("", nil)` when no team carries that alias.

- [ ] **Step 1: Write the failing tests**

Append to `internal/platformapi/teams/lookup_test.go`:

```go
func TestLookupTeamIDByAlias(t *testing.T) {
	ll := &fakeTeamsClient{teams: []litellm.TeamListEntry{
		{TeamID: "t-1", TeamAlias: "default"},
		{TeamID: "t-2", TeamAlias: "ach-env-demo"},
	}}
	got, err := LookupTeamIDByAlias(context.Background(), ll, "ach-env-demo")
	if err != nil {
		t.Fatalf("LookupTeamIDByAlias: %v", err)
	}
	if got != "t-2" {
		t.Fatalf("id = %q, want t-2", got)
	}

	missing, err := LookupTeamIDByAlias(context.Background(), ll, "ach-env-nope")
	if err != nil {
		t.Fatalf("LookupTeamIDByAlias(absent): %v", err)
	}
	if missing != "" {
		t.Fatalf("absent alias returned %q, want empty string", missing)
	}
}
```

Use whatever fake `lookup_test.go` already defines for `ListAllTeams` instead of `fakeTeamsClient` if the name differs — grep the file first and reuse it, seeding the two entries above.

Append to `internal/platformapi/envkeys/handler_test.go` (model the setup on the existing `TestCreateHandler…` test that asserts `flm.lastKeyGenerateReq`; the fake's `ListAllTeams` must return a team aliased `ach-env-<env used by the test>` with a known id):

```go
// TestCreateEkMintsIntoShellTeam is the whole point of the shell-team change:
// the ek_ must be capped by team_id and must NOT carry any access-group field
// (a key in both a team and a group triggers LiteLLM's agent-collapse bug).
func TestCreateEkMintsIntoShellTeam(t *testing.T) {
	// ... same wiring as the existing create-handler test, with the fake's
	// ListAllTeams returning {TeamID: "t-shell", TeamAlias: "ach-env-<env>"}
	// alongside the caller's authorized team ...

	got := flm.lastKeyGenerateReq
	if got == nil {
		t.Fatal("KeyGenerate was never called")
	}
	if got.TeamID != "t-shell" {
		t.Fatalf("TeamID = %q, want t-shell (the environment's shell team)", got.TeamID)
	}
	if len(got.Models) != 0 {
		t.Fatalf("Models = %v, want none — the team is the ceiling", got.Models)
	}
}

// TestCreateEkRejectsWhenShellTeamMissing: no shell team ⇒ the environment is
// not fully provisioned, and minting would produce a fail-open key.
func TestCreateEkRejectsWhenShellTeamMissing(t *testing.T) {
	// ... same wiring, but the fake's ListAllTeams returns ONLY the caller's
	// authorized team (no ach-env-<env> entry) ...
	// assert: HTTP 503 and the response body's code is "not_ready",
	// and flm.lastKeyGenerateReq == nil (no key was minted).
}
```

Fill both bodies by copying the existing create-handler test's setup verbatim and changing only the assertions — do not invent new helpers.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
make test-unit-pkg PKG=./internal/platformapi/teams/
make test-unit-pkg PKG=./internal/platformapi/envkeys/
```
Expected: FAIL — `undefined: LookupTeamIDByAlias`, and `TeamID = "" want t-shell`.

- [ ] **Step 3: Add `LookupTeamIDByAlias`**

Append to `internal/platformapi/teams/lookup.go`:

```go
// LookupTeamIDByAlias resolves a LiteLLM team alias to its team id via one
// ListAllTeams round-trip. Returns ("", nil) when no team carries the alias —
// absence is a caller decision (the ek_ create path turns it into 503
// not_ready), not an error.
//
// First-wins on duplicate aliases, matching the Environment reconciler.
func LookupTeamIDByAlias(ctx context.Context, ll litellm.Client, alias string) (string, error) {
	if alias == "" {
		return "", nil
	}
	teams, err := ll.ListAllTeams(ctx)
	if err != nil {
		return "", err
	}
	for _, t := range teams {
		if t.TeamAlias == alias && t.TeamID != "" {
			return t.TeamID, nil
		}
	}
	return "", nil
}
```

- [ ] **Step 4: Mint into the shell team**

In `internal/platformapi/envkeys/handler.go`, in `mintAndInsert`, immediately before the `keyReq := &litellm.KeyGenerateRequest{…}` literal, insert:

```go
	// The ek_ is capped by the Environment's deny-all shell team and by
	// NOTHING else: no models, no object_permission, no access-group binding.
	// A key with no team is fail-open on models, and a key that is in both a
	// team and an access group trips LiteLLM's agent-collapse bug — see
	// references/litellm-permission-model.md §4 and §7.
	shellAlias := litellm.ShellTeamAlias(env.Name)
	shellTeamID, stErr := achteams.LookupTeamIDByAlias(ctx, deps.LiteLLM, shellAlias)
	if stErr != nil {
		cr.emitLitellmError(stErr, "envkeys.create: shell team lookup failed")
		return
	}
	if shellTeamID == "" {
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action:    audit.ActionEkCreate,
			Outcome:   audit.OutcomeNotReady,
			Actor:     cr.actor,
			RequestID: reqID,
			Target:    cr.target,
		})
		render.Error(w, http.StatusServiceUnavailable, audit.OutcomeNotReady,
			"environment shell team not yet provisioned", reqID)
		return
	}
```

Add `TeamID: shellTeamID,` to the `keyReq` literal (right after `UserID`).

No membership handling is needed and none must be added: LiteLLM's
`_team_key_generation_check` returns early for `PROXY_ADMIN`, and ACH
authenticates with the master key — so minting a key into a team the `user_id`
does not belong to is accepted. (The asymmetric one is `POST /key/update`, which
DOES require membership to move an existing key; ACH never does that.) Record
that in a comment above `keyReq` so nobody "fixes" it later:

```go
	// No TeamMemberAdd needed: LiteLLM only enforces team membership on
	// /key/generate for non-admin callers, and ACH authenticates with the
	// master key (PROXY_ADMIN). See references/litellm-permission-model.md §9.
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
make test-unit-pkg PKG=./internal/platformapi/teams/
make test-unit-pkg PKG=./internal/platformapi/envkeys/
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
make qa-lint-changed
git add internal/platformapi/
git commit -m "feat(platform-api): mint environment keys into the per-env shell team"
```

---

### Task 7: Docs — architecture, key lifecycle, and the un-migrated old keys

**Files:**
- Modify: `CLAUDE.md`
- Modify: `references/troubleshooting.md`

- [ ] **Step 1: Update CLAUDE.md**

In the service-mode table row for `platform-api`, change the `pk_`/`ek_` lifecycle cell to state the new binding — replace `+ `pk_`/`ek_` lifecycle` with:

```markdown
+ `pk_`/`ek_` lifecycle (an `ek_` is minted ONLY into its Environment's deny-all shell team `ach-env-<env>` — no models, no object_permission, no access-group binding; see `references/litellm-permission-model.md`)
```

In the "Critical paths" list, add after the Environment-reconcile bullet:

```markdown
- Environment reconcile → deny-all shell team `ach-env-<name>` (sentinels: `models=["__deny_all__"]`, `agents=["00000000-0000-0000-0000-000000000000"]`) → joined into the access group's `assigned_team_ids` alongside `spec.authorizedTeams`; `ach-cli keys create` mints the `ek_` into that team, which is the only reliable ceiling on a key
```

In the "Environment two-axis status" bullet under "Repository-specific patterns", append to the `AccessGroupSynced` clause:

```markdown
 — plus the per-Environment deny-all shell team (`ShellTeamFailed` when it cannot be provisioned/repaired)
```

- [ ] **Step 2: Document the un-migrated pre-v0.6.17 keys**

Add to `references/troubleshooting.md`, immediately after the section added in Task 5:

```markdown
### ℹ️ An `ek_` created before v0.6.17 can still reach models its Environment never granted

Keys minted before the shell-team change carry no `team_id`. A LiteLLM key with
no team is fail-open on models — the access group cannot narrow it (MCP and
agents DO scope correctly even then). They were deliberately NOT migrated.

Fix per key: `ach-cli keys revoke <ekid_…>` then `ach-cli keys create`. The new
key is minted into `ach-env-<env>` and capped correctly. To find them:

    psql "$ACH_DB_URL" -c \
      "SELECT key_id, environment, owner_email, created_at FROM environment_keys
        WHERE status='active' AND created_at < '2026-07-21' ORDER BY created_at"
```

(The block above is the literal markdown to paste; keep the indented psql
command indented so it does not nest a fence inside the section.)

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md references/troubleshooting.md
git commit -m "docs: environment keys are capped by a per-env shell team"
```

---

### Task 8: Full gates + live verification

**Files:** none (verification only).

- [ ] **Step 1: Full local test sweep**

```bash
make test-unit
make qa-lint
make test-envtest
```
Expected: all PASS. Fix anything red before continuing.

- [ ] **Step 2: E2E**

```bash
make e2e-full
```
Expected: PASS. This change touches `internal/controller/`, `internal/platformapi/` and `internal/litellm/`, so e2e is mandatory (CI does not run it). The cluster is kept up after the run; `make cluster-down` when done.

- [ ] **Step 3: Security + publication gates**

```bash
make qa-security
make pre-push
```
Expected: PASS (18 gates).

- [ ] **Step 4: Record the live-proxy verification recipe**

These need the LiteLLM master key and a live proxy, so they are for a human, not the gate. They are already captured in the Task 1 reference doc; run them when the release is deployed:

```bash
# 1. shell team exists with sentinels
curl -sH "Authorization: Bearer $LITELLM_MASTER_KEY" \
  "$LITELLM/v2/team/list?page_size=100" \
  | jq '.teams[] | select(.team_alias|startswith("ach-env-")) | {team_alias, models, object_permission, access_group_ids}'

# 2. an EK issued in the shell reaches EXACTLY the environment's grants
#    (a model in spec.runtime.models → 200; one outside → team_model_access_denied)
curl -sH "Authorization: Bearer $EK" "$LITELLM/v1/chat/completions" -d '{"model":"<granted>","messages":[{"role":"user","content":"hi"}]}'
curl -sH "Authorization: Bearer $EK" "$LITELLM/v1/chat/completions" -d '{"model":"<not-granted>","messages":[{"role":"user","content":"hi"}]}'

# 3. MCP: compare against PER-SERVER catalogs, not substrings — several servers
#    ship generic tool names (authenticate, auth_status)
curl -sH "Authorization: Bearer $EK" "$LITELLM/mcp-rest/tools/list" | jq '.tools|length'

# 4. agents: exactly spec.runtime.a2aAgents, and another agent's id is denied
curl -sH "Authorization: Bearer $EK" "$LITELLM/v1/agents" | jq '.[].agent_id'
curl -sH "Authorization: Bearer $EK" -X POST "$LITELLM/a2a/<other-agent-id>/message/send" -d '{}'

# 5. pk_ path unchanged: the group still grants through the team mirror
curl -sH "Authorization: Bearer $LITELLM_MASTER_KEY" "$LITELLM/v1/access_group" \
  | jq '.[] | {access_group_name, assigned_team_ids}'
```

- [ ] **Step 5: Verify the access-group DELETE route (open question, not a code change)**

`DeleteAccessGroupByID` sends `DELETE /v1/access_group/{id}` and `makeRequest`
treats DELETE+404 as success, so a wrong path fails SILENTLY. Confirm which
route the deployed proxy honours:

```bash
AG=$(curl -sH "Authorization: Bearer $LITELLM_MASTER_KEY" "$LITELLM/v1/access_group" | jq -r '.[0].access_group_id')
curl -s -o /dev/null -w "access_group:          %{http_code}\n" -X DELETE -H "Authorization: Bearer $LITELLM_MASTER_KEY" "$LITELLM/v1/access_group/$AG"
curl -s -o /dev/null -w "unified_access_group:  %{http_code}\n" -X DELETE -H "Authorization: Bearer $LITELLM_MASTER_KEY" "$LITELLM/v1/unified_access_group/$AG"
```

(Do this on a THROWAWAY group.) If only `unified_access_group` works, file it as a
separate fix — `accessgroups.go:90` plus the DELETE-404-as-success behaviour is
then hiding a real leak, and it is out of scope for this plan.

---

## Self-review notes

- Spec requirement 0 (whole-list union) → Task 4 step 5b. Requirement 1 (never add EK to the group) → Task 2 (field deleted) + Task 6 (team_id only) + Task 3 doc. Requirement 2 (remove `AccessGroups`) → Task 2 step 3/6. Requirement 3 (sentinels) → Task 3. Requirement 4 (reconcile every change + deletion decision) → Task 4 + Task 5. Requirement 5 (resolve agent names, fail loudly) → already implemented, documented in "Current-state facts".
- The "also pending" resync/featuregate item is already fixed in this tree — deliberately NOT a task.
