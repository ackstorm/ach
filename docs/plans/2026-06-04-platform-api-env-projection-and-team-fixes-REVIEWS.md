# Cross-AI Plan Review — platform-api env-projection + team fixes

reviewed_at: 2026-06-04
plan: `docs/plans/2026-06-04-platform-api-env-projection-and-team-fixes.md`

Reviewers attempted:
- **gemini** (gemini-2.5-pro) ✓ completed
- **codex** ✗ skipped — CLI not logged in (`codex login status` → "Not logged in"; 401 Unauthorized)
- **opencode** ✗ skipped — `opencode run` hung ~29 min with 0 bytes output (non-interactive mode unreliable here); killed

## Gemini Review (gemini-2.5-pro)

### Summary
High-quality, well-researched plan that correctly identifies the root causes of three
production issues and proposes safe, targeted, test-driven fixes. Plan claims about the
codebase match the live source precisely; the CS-09 drain race analysis is sound; proposed
tests are robust failing-first tests. Implementation path is clear and addresses full scope,
including the 5-table sweep.

### Concerns
- **MEDIUM — Unhandled pagination in `ListAllTeams`:** `page_size=500`, no follow. If teams
  exceed 500, returns a silent partial list → ID→alias resolution fails for later-page teams →
  reintroduces the original false-negative. (Plan acknowledged YAGNI; reviewer wants the
  overflow visible.) → **Folded into the plan: WARN on `TotalPages > 1`.**
- **LOW — Empty `TeamAlias`:** `aliasByID` could store `""`. → **Already mitigated in the
  plan** — the map-build guards `if t.TeamAlias != ""` and the `add()` helper skips `""`.
  Reviewer's suggested fix is the guard already present; no action.

### Strengths (reviewer)
- Code analysis matches the live codebase; identifying `backend_identity_policies.go` as the
  correct reference impl is astute.
- Independently traced `environment_controller.go` to confirm the live-upsert branch is
  mutually exclusive with the deletion/finalizer branch → clearing `deletion_timestamp` on the
  live path is safe (no drain race).
- Correctly confirms `external_refs` / `marketplace_plugins` are unaffected (no
  `deletion_timestamp` column).

### Risk Assessment: **LOW**

## Disposition
- MED pagination finding → applied to plan Task 2 (`ListAllTeams` WARNs on multi-page).
- LOW empty-alias finding → already handled; no change.
- No HIGH findings. CS-09 drain safety independently confirmed. Plan stands.
