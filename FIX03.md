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

## J.3 — CLAUDE.md `examples/` reference audit

**Status**: DONE.

**Where**: `CLAUDE.md` "Repository layout" section + "MANDATORY Reading Table".

**Symptom**: `examples/` now contains 9 working CRs + driver script + golden JSON + README. CLAUDE.md doesn't promote any of this as a discovery surface.

**Fix**:
- Verify the existing `examples/` line in repo-layout still matches reality (kindex of files).
- Add a row to the "MANDATORY Reading Table": `Working on... examples / new CR fixtures | examples/README.md`.
- Re-audit after TODO §5 (marketplace re-model) rewrites example 5.
