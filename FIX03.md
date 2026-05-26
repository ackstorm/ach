# FIX03 — quick wins from the end-to-end sweep

Session scratchpad for fast fixes discovered while testing every CRD surface against the live e2e cluster (2026-05-26). Items here are either already resolved (kept as a session changelog) or small enough to land before the session ends.

Heavier blockers from the same sweep live in `TODO.md` §7-§10 (Environment AccessGroupSynced, Content Service streaming, Ready rollup, CLI commands).

---

## J.1 — LiteLLM `/key/generate` `metadata` field not populated

**Status**: DONE.

**Where**: `internal/platformapi/auth/sso.go` step 6b, `internal/platformapi/envkeys/handler.go` env-keys create, `internal/litellm/types.go` `KeyGenerateRequest`.

**Symptom**: LiteLLM admin UI + audit logs carry no ACH-side identifier for ACH-minted virtual keys. Orphan-cleanup reconciler matches by `user_id` + creation-time only — fragile.

**Fix**: add `Metadata map[string]string \`json:"metadata,omitempty"\`` to `KeyGenerateRequest`. Populate at both call sites:

```json
{
  "ach_key_id":      "pkid_01HW...",
  "ach_key_type":    "pk",
  "ach_owner_email": "kilgore@kilgore.trout",
  "ach_environment": "demo"
}
```

Orphan-cleanup reconciler reads back via `/key/list` to validate ACH ↔ LiteLLM mapping deterministically.

---

## J.2 — `hydrate-demo.sh` step 1 not idempotent → LiteLLM `default` team accumulation

**Status**: DONE.

**Where**: `examples/hydrate-demo.sh` step 1.

**Symptom**: Every invocation calls `POST /team/new team_alias=default` unconditionally. LiteLLM allows alias duplicates with fresh UUIDs → N+1 teams labeled `default` after N runs.

**Fix**: `GET /team/list?team_alias=default` first; only `POST /team/new` if empty. (No client-side dedup loop; the operator-side `provisionUser` already tolerates multiple teams via `ListTeamsByAlias` ordering — but the canonical UAT path should be deterministic.)

---

## J.4 — `isDuplicateAddErr` predicate cannot match real LiteLLM 1.83 wrapper

**Status**: DONE.

**Where**: `internal/platformapi/auth/sso.go::isDuplicateAddErr`.

**Symptom**: every existing-user SSO callback returns 500 `default_team_missing`. Operator log shows `sso.callback: TeamMemberAdd on existing-user path failed err="litellm: 400 on POST /team/member_add (code=400)"`.

**Root cause**: `internal/litellm/restclient.go:152` formats 4xx errors as `litellm: %d on %s %s (code=%s)` — the response body is dropped entirely. The predicate searched for `"already"` substring, which is never present in the error string.

**Fix**: match on `(path == "/team/member_add" + status == 400)` instead of a body substring. Our SSO code path is the only caller of `/team/member_add` and always sends a well-formed envelope, so a 400 from that path is realistically only the duplicate-add case.

**Test fixture update**: `sso_test.go::TestCallbackHandler_DuplicateTeamMemberAddSwallowed` was emitting the obsolete `"litellm: status: 400 body: user already in team"` string. Updated to mirror the real wrapper format.

---

## J.5 — operator bootstraps LiteLLM `default` team on startup

**Status**: DONE.

**Where**: `internal/litellm/client.go` (interface), `internal/litellm/team.go` (impl), `internal/controller/ach/litellmconnection_controller.go` (call site).

**Symptom**: a freshly-hydrated cluster (production OR e2e) has zero `default`-alias teams in LiteLLM. The first SSO callback returns `default_team_missing` 500. Until now `scripts/cluster.sh` was a candidate fix, but production has no cluster.sh — the operator must self-bootstrap.

**Fix**:
- Added `EnsureDefaultTeam(ctx context.Context) error` to the `litellm.Client` interface.
- Implemented on `RESTClient`: idempotent `ListTeamsByAlias(defaultTeamAlias)` → empty → `CreateTeam({team_alias: defaultTeamAlias})`. The `defaultTeamAlias` constant is hardcoded to `"default"` today; TODO §15 tracks making this configurable per-deployment.
- `NoopClient` returns nil. All test fakes (8 files) get a no-op `EnsureDefaultTeam` shim.
- `connection.Client` wrapper delegates to the underlying snapshot client.
- `LiteLLMConnectionReconciler` calls `client.EnsureDefaultTeam(ctx)` immediately after the probe-success path. Failure is logged + tolerated (next reconcile retries every 5 minutes); the LiteLLMConnection CR's Synced=True does NOT depend on it.

**Bonus**: `scripts/cluster.sh::hydrate_fixtures` is now cleaner — no shell-level team seed, just the Secret + LiteLLMConnection CR. The operator handles the rest.

---

## J.3 — CLAUDE.md `examples/` reference audit

**Status**: DONE.

**Where**: `CLAUDE.md` "Repository layout" section + "MANDATORY Reading Table".

**Symptom**: `examples/` now contains 9 working CRs + driver script + golden JSON + README. CLAUDE.md doesn't promote any of this as a discovery surface.

**Fix**:
- Verify the existing `examples/` line in repo-layout still matches reality (kindex of files).
- Add a row to the "MANDATORY Reading Table": `Working on... examples / new CR fixtures | examples/README.md`.
- Re-audit after TODO §5 (marketplace re-model) rewrites example 5.
