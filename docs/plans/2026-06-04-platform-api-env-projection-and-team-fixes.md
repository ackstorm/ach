# Platform-API Env Projection + Team-Match Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix three platform-api defects surfaced by a prod incident: (1) a resurrected CR (soft-deleted, drain wedged, then recreated) stays hidden because the projection upsert never clears a stale `deletion_timestamp` — present on **5 of 6** origin-gated projection tables (`environments`, `plugins`, `artifacts`, `prompts`, `litellm_connections`; `backend_identity_policies` is the lone correct impl); (2) the env-visibility / hydrate team-authorization check compares LiteLLM team **IDs** against team **aliases**, producing false-negative `403`/empty-list for non-admin users; (3) ACH-minted LiteLLM virtual keys carry no `key_alias`, making them hard to attribute when debugging in the LiteLLM UI/DB.

**Architecture:** All three are server-side changes in the operator's projection write path and the platform-api auth/teams packages. No CRD/schema migration. Fix 1 is one SQL line + a comment + an integration test. Fix 2 adds a `ListAllTeams` LiteLLM client method and resolves caller team IDs → aliases (returning both forms so the intersection matches regardless of which identifier `authorized_teams` holds). Fix 3 sets `KeyAlias = <pkid_/ekid_>` on the two `KeyGenerate` call sites.

**Tech Stack:** Go, pgx/v5, controller-runtime, chi, testcontainers-go (db integration tests), stdlib `testing`.

---

## Background — incident root cause (read before starting)

Prod cluster `pro-ack-ai-platform`, env `platform`:

- The `platform` **Environment CR was healthy** (no `deletionTimestamp`, `Available=True`), but its Postgres projection row carried a stale `deletion_timestamp` (set during an earlier delete attempt whose drain wedged on the operator `ErrNotFound` issue) that a later **live** reconcile never cleared. `env list` filters `WHERE deletion_timestamp IS NULL` → the env was invisible; `hydrate` (lookup-by-name, no deletion filter) still worked. **→ Fix 1.** A manual `UPDATE environments SET deletion_timestamp = NULL` was applied in prod as a hotfix; Fix 1 makes the code self-heal on the next resurrection.
- **The same defect exists on `plugins`, `artifacts`, `prompts`, `litellm_connections`** — all soft-delete (`deletion_timestamp = now()`), all upsert with `ON CONFLICT DO UPDATE` that omits the column, all filter `deletion_timestamp IS NULL` on read. `backend_identity_policies` is the ONLY table that clears it on live upsert (copy its `DO UPDATE SET`). For the content-served kinds the blast radius is **worse than environments**: `plugins.ResolvePluginByName` and the artifact reader filter `IS NULL` on the **per-request** path, so a resurrected plugin/artifact is invisible to the content-service resolver (hydrate 404 / marketplace fallback), not merely hidden from a list. **→ Fix 1 is a sweep over all 5 tables.**
- The reporting user is an **admin** (in `/etc/ach/admins/admins.txt`), so hydrate's team gate was skipped for them. A **non-admin** member of an authorized team would have been wrongly denied: `LookupCallerTeams` returns LiteLLM team **IDs** (`team-platform`, `6a31a295-…`); `authorized_teams` stores **aliases** (`{default,run,dream}`). `HasIntersect` compares raw strings → never matches even though `6a31a295-…` *is* the team aliased `default`. **→ Fix 2.**
- ACH keys in LiteLLM have empty `key_alias`; only `metadata.ach_key_id` identifies them. Setting `key_alias = <pkid_/ekid_>` makes them greppable in the LiteLLM UI/DB. **Debug-only — NOT used for lookup/search.** **→ Fix 3.**

**Out of scope (deferred, do NOT attempt here):** making ACH keys discoverable by the ackstorm `auth_user_map.sso_key_swapper` (which keys off `key_alias LIKE %email%` + `metadata.email`). That "coexist with live token-factory users" decision is tracked separately. Fix 3 deliberately sets `key_alias` to the ACH key id, **not** the email, so it does not accidentally wire ACH keys into the swapper.

**Toolchain reminder (CLAUDE.md):** host has no Go — every `make` target auto-routes into the devtools container; use `./scripts/dev.sh go ...` for raw go. Never prefix a `make` target with `./scripts/dev.sh`.

---

## Task 1: Clear stale `deletion_timestamp` on live upsert — sweep all 5 tables

Do `environments` first as the fully-worked TDD example (Steps 1-5), then apply the identical one-line edit to the other four (Step 6). The fix in every table is: add `deletion_timestamp = NULL,` to the `ON CONFLICT DO UPDATE SET` clause and correct the misleading "preserved" comment. Reference impl to copy: `internal/db/backend_identity_policies.go` (already does this — note its inline comment about CS-09 + the `ListAllBIPs` `IS NULL` filter).

**Files (all 5 tables):**
- Modify: `internal/db/environments.go` (`upsertEnvironmentSQL` + `UpsertEnvironment` doc comment)
- Modify: `internal/db/plugins.go` (`upsertPluginSQL` DO UPDATE SET + comment)
- Modify: `internal/db/artifacts.go` (upsert DO UPDATE SET + comment)
- Modify: `internal/db/prompts.go` (upsert DO UPDATE SET + comment)
- Modify: `internal/db/litellm_connections.go` (upsert DO UPDATE SET + comment)
- Test: `internal/db/environments_test.go` (+ one analogous test per table; integration-tagged)

**Step 1: Write the failing test**

Add to `internal/db/environments_test.go` (same package/build-tag `//go:build integration` as the file; reuse whatever pool/migrate helper the existing `TestUpsertEnvironment_InsertThenUpdate` uses — copy its setup verbatim):

```go
// TestUpsertEnvironment_ClearsStaleDeletionTimestamp proves a LIVE reconcile
// un-sets a drain marker left by a prior soft-delete (incident 2026-06-04:
// a resurrected Environment stayed hidden from env list because the upsert
// preserved the old deletion_timestamp).
func TestUpsertEnvironment_ClearsStaleDeletionTimestamp(t *testing.T) {
	ctx, pool := newEnvTestPool(t) // copy the exact helper used by TestUpsertEnvironment_InsertThenUpdate

	row := EnvironmentRow{
		Namespace:       "ach",
		Name:            "resurrect-me",
		AuthorizedTeams: []string{"default"},
		ResourceVersion: "1",
	}
	if err := UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}
	// Soft-delete sets deletion_timestamp.
	if err := SoftDeleteEnvironment(ctx, pool, "ach", "resurrect-me"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// Live reconcile of the recreated CR (same name) upserts again.
	row.ResourceVersion = "2"
	if err := UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("live upsert: %v", err)
	}

	got, err := GetEnvironmentByName(ctx, pool, "ach", "resurrect-me")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DeletionTimestamp != nil {
		t.Fatalf("expected deletion_timestamp cleared on live upsert, got %v", *got.DeletionTimestamp)
	}
}
```

> If `SoftDeleteEnvironment` / `GetEnvironmentByName` / the pool helper have different names, match the actual symbols in `internal/db/environments.go` and `environments_test.go` — do NOT invent names.

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test -tags=integration ./internal/db/... -run TestUpsertEnvironment_ClearsStaleDeletionTimestamp -count=1 -v`
Expected: FAIL — `expected deletion_timestamp cleared on live upsert, got 2026-…` (the stale marker survives).

**Step 3: Write minimal implementation**

In `internal/db/environments.go`, add one line to the `ON CONFLICT DO UPDATE SET` clause (between `updated_at = now(),` and `locked = TRUE`):

```sql
	    updated_at                             = now(),
	    deletion_timestamp                     = NULL,
	    locked                                 = TRUE
	WHERE environments.origin = 'cr'
```

Then update the `UpsertEnvironment` doc comment (`environments.go:74-78`) — the current text says deletion_timestamp "is preserved", which is the defect. Replace that paragraph with:

```go
// UpsertEnvironment inserts-or-updates a row keyed by (namespace, name).
// The ON CONFLICT DO UPDATE replaces every non-PK column. A live reconcile
// (this path only runs for a CR with no metadata.deletionTimestamp) clears
// deletion_timestamp to NULL — a recreated CR reusing a soft-deleted name
// MUST drop the stale drain marker, else env list (WHERE deletion_timestamp
// IS NULL) hides the resurrected env forever (incident 2026-06-04). The
// drain marker is owned solely by SoftDeleteEnvironment, which runs on the
// deletion branch and never overlaps this upsert. updated_at is force-set
// to now() in the UPDATE branch.
```

> CS-09 note for the reviewer: clearing here is safe because the upsert path is the **non-deletion** reconcile branch (see `environment_controller.go` `writeEnvironmentProjection` call site, ~line 319). The drain sequence runs on the deletion branch (`SoftDeleteEnvironment`), which does not call this upsert, so a genuine in-progress drain is never un-marked by a stray live reconcile (a CR with `deletionTimestamp` set cannot take the upsert branch).

**Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh go test -tags=integration ./internal/db/... -run 'TestUpsertEnvironment' -count=1 -v`
Expected: PASS (new test + existing `TestUpsertEnvironment_InsertThenUpdate` + `TestSoftDeleteEnvironment_PreservesRow` all green).

**Step 5: Commit**

```bash
git add internal/db/environments.go internal/db/environments_test.go
git commit -m "fix(operator): clear stale deletion_timestamp on live env upsert

A recreated Environment reusing a soft-deleted name kept the old
deletion_timestamp, hiding it from env list (WHERE deletion_timestamp IS
NULL) while hydrate (lookup-by-name) still served it. The live upsert now
resets the drain marker; SoftDeleteEnvironment remains its sole writer."
```

**Step 6: Apply the identical fix to `plugins`, `artifacts`, `prompts`, `litellm_connections`**

For EACH of the four files: add `deletion_timestamp = NULL,` to the `ON CONFLICT DO UPDATE SET` (alongside `updated_at = now(),` / `locked = TRUE`), and replace the `// ... replaces every non-PK column EXCEPT deletion_timestamp (preserved per CS-09) ...` comment with the BIP-style wording (live upsert clears the drain marker; the SoftDelete* function is its sole writer; the deletion branch never overlaps the upsert branch). Add a per-table integration test mirroring Step 1 (insert → `SoftDelete<Kind>` → live `Upsert<Kind>` → assert `DeletionTimestamp == nil`); reuse the same pool helper. For `plugins`/`artifacts` ALSO assert the per-request resolver sees the row again after the live upsert (e.g. `ResolvePluginByName` returns the plugin, not the marketplace fallback) — that path's `IS NULL` filter is the real blast radius.

Run after each: `./scripts/dev.sh go test -tags=integration ./internal/db/... -run 'Upsert|Resolve' -count=1 -v` → PASS.

Commit (one commit for the four, or one per table):

```bash
git add internal/db/plugins.go internal/db/artifacts.go internal/db/prompts.go \
        internal/db/litellm_connections.go internal/db/*_test.go
git commit -m "fix(operator): clear stale deletion_timestamp on live upsert (plugins/artifacts/prompts/litellm_connections)

Same defect as environments across all origin-gated projection tables:
the upsert preserved a soft-delete marker, so a resurrected CR stayed
invisible to IS-NULL-filtered reads (for plugins/artifacts the per-request
content resolver, not just list). backend_identity_policies already did
this correctly; bring the rest in line."
```

---

## Task 2: Match teams by ID *and* alias (fix non-admin false-negative)

**Files:**
- Modify: `internal/litellm/client.go:47-…` (add `ListAllTeams` to the `Client` interface)
- Modify: `internal/litellm/team.go` (implement `ListAllTeams` on `*RESTClient`)
- Modify: `internal/litellm/noop.go` (add `NoopClient.ListAllTeams` stub)
- Modify: `internal/platformapi/teams/lookup.go:36-48` (`LookupCallerTeams` resolves IDs → aliases, returns both)
- Modify: `internal/platformapi/teams/lookup_test.go` (extend `fakeLiteLLM` stub; update `TestLookupCallerTeamsHappy`)
- Test: `internal/platformapi/teams/lookup_test.go` (new cases)

**Why both forms:** `authorized_teams` holds aliases today (`api/ach/v1alpha1/environment_types.go:12`, copied verbatim by the operator). But a deployer could write a raw team ID into `spec.authorizedTeams`. Returning `{id, alias}` per caller team makes `HasIntersect(authorized_teams, callerTeams)` match either convention — no behavior change for admins (they skip the check), strict improvement for everyone else.

**Step 1: Write the failing test (resolution returns id + alias)**

In `internal/platformapi/teams/lookup_test.go`, first extend the `fakeLiteLLM` stub (the compile-time canary `var _ litellm.Client = (*fakeLiteLLM)(nil)` forces this):

```go
// add field to fakeLiteLLM struct:
//   listAllTeams func() ([]litellm.TeamListEntry, error)

func (f *fakeLiteLLM) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	if f.listAllTeams != nil {
		return f.listAllTeams()
	}
	return nil, nil
}
```

Then add the new test:

```go
// TestLookupCallerTeamsResolvesAliases — user's teams are LiteLLM IDs; the
// helper resolves each to its alias via ListAllTeams and returns BOTH forms
// so HasIntersect matches authorized_teams whether they hold ids or aliases.
func TestLookupCallerTeamsResolvesAliases(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfo: func(string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: "a@b.c",
				Teams: []string{"team-platform", "6a31a295"}}, nil
		},
		listAllTeams: func() ([]litellm.TeamListEntry, error) {
			return []litellm.TeamListEntry{
				{TeamID: "team-platform", TeamAlias: "platform"},
				{TeamID: "6a31a295", TeamAlias: "default"},
				{TeamID: "zzz", TeamAlias: "run"},
			}, nil
		},
	}
	got, err := LookupCallerTeams(context.Background(), ll, "a@b.c")
	if err != nil {
		t.Fatalf("LookupCallerTeams: %v", err)
	}
	want := map[string]bool{"team-platform": true, "platform": true, "6a31a295": true, "default": true}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want keys %#v", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected team token %q in %#v", g, got)
		}
	}
}
```

Also update the existing `TestLookupCallerTeamsHappy` (lookup_test.go:62): it currently asserts the verbatim `{team-a, team-b}` return. Give its `fakeLiteLLM` a `listAllTeams` mapping `team-a→alias-a`, `team-b→alias-b`, and assert the result set is `{team-a, alias-a, team-b, alias-b}` (order-independent — switch the assertion to a set check like the new test).

**Step 2: Run test to verify it fails**

Run: `make test-unit-pkg PKG=./internal/platformapi/teams/...`
Expected: FAIL to compile first (`fakeLiteLLM` missing nothing now, but `litellm.Client` lacks `ListAllTeams`), then once the interface exists, FAIL on assertions because `LookupCallerTeams` still returns IDs only.

**Step 3a: Add the `ListAllTeams` client method**

In `internal/litellm/client.go`, add to the `Client` interface (next to `ListTeamsByAlias`):

```go
	// ListAllTeams issues GET /v2/team/list (no alias filter) and returns
	// every team's id+alias. Used by platformapi/teams to resolve caller
	// team IDs → aliases for the authorized_teams intersection.
	ListAllTeams(ctx context.Context) ([]TeamListEntry, error)
```

In `internal/litellm/team.go`, implement (mirror `ListTeamsByAlias`, drop the alias filter):

```go
// ListAllTeams issues GET /v2/team/list?page_size=500 and returns every
// team. No client-side filter (cf. ListTeamsByAlias). Empty slice is not
// an error. Single-page only by design — a multi-page result WARNs rather
// than silently truncating (silent truncation would drop alias resolutions
// and re-introduce the team false-negative this fix closes; cross-AI review
// MED finding 2026-06-04).
func (c *RESTClient) ListAllTeams(ctx context.Context) ([]TeamListEntry, error) {
	raw, err := c.makeRequest(ctx, "GET", "/v2/team/list?page_size=500", nil)
	if err != nil {
		return nil, err
	}
	var list TeamListResponse
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /v2/team/list: %w", err)
	}
	if list.TotalPages > 1 {
		// Use the client's existing logger (match how RESTClient logs
		// elsewhere — slog/logr); do NOT silently truncate.
		c.logger.Warn("litellm: team list exceeds one page (page_size=500); alias resolution may be incomplete — implement pagination",
			"total_pages", list.TotalPages, "total", list.Total)
	}
	return list.Teams, nil
}
```

> Pagination itself stays YAGNI at current scale (5 teams), but the overflow is now **visible in logs** instead of a silent partial list (cross-AI review MED finding). If `RESTClient` has no `logger` field, match whatever logging facility its other methods use (or `slog.Default()`); the WARN is the contract, the exact logger is not.

In `internal/litellm/noop.go`, add the `NoopClient` stub (mirror its `ListTeamsByAlias`):

```go
func (c *NoopClient) ListAllTeams(_ context.Context) ([]TeamListEntry, error) {
	return nil, nil
}
```

Run `make build-all` and fix EVERY other `litellm.Client` implementor the compiler flags (generated mocks, operator fakes). The interface widening ripples — chase all of them before moving on.

**Step 3b: Resolve IDs → aliases in `LookupCallerTeams`**

Rewrite `internal/platformapi/teams/lookup.go` `LookupCallerTeams` body (keep the 404/empty/error semantics exactly):

```go
func LookupCallerTeams(ctx context.Context, ll litellm.Client, email string) ([]string, error) {
	info, err := ll.UserInfoByEmail(ctx, email)
	if err != nil {
		if isNotFound(err) {
			return []string{}, nil
		}
		return nil, err
	}
	if info == nil || len(info.Teams) == 0 {
		return []string{}, nil
	}

	// info.Teams are LiteLLM team IDs; authorized_teams stores aliases.
	// Resolve each ID to its alias and return BOTH so HasIntersect matches
	// regardless of which identifier the env authorized by (incident
	// 2026-06-04: a member of team alias "default" was denied because the
	// raw team-id string never equalled the alias string).
	teams, err := ll.ListAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	aliasByID := make(map[string]string, len(teams))
	for _, t := range teams {
		if t.TeamAlias != "" {
			aliasByID[t.TeamID] = t.TeamAlias
		}
	}

	out := make([]string, 0, len(info.Teams)*2)
	seen := make(map[string]struct{}, len(info.Teams)*2)
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, id := range info.Teams {
		add(id)               // raw id (matches authorized_teams that hold ids)
		add(aliasByID[id])    // resolved alias (matches the common alias case)
	}
	return out, nil
}
```

Update the doc comment above `LookupCallerTeams` to state it now returns both team IDs and aliases (and that the `ListAllTeams` round-trip is the natural seam for the Phase-4 Redis cache mentioned in the existing comment).

**Step 4: Run tests to verify they pass**

Run: `make test-unit-pkg PKG=./internal/platformapi/teams/...`
Expected: PASS (`TestLookupCallerTeamsResolvesAliases`, updated `…Happy`, and the unchanged 404/empty/transport cases).

Then the wider sweep so no other caller broke:
Run: `make test-unit`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/litellm/client.go internal/litellm/team.go internal/litellm/noop.go \
        internal/platformapi/teams/lookup.go internal/platformapi/teams/lookup_test.go
git commit -m "fix(platform-api): match authorized teams by id and alias

LookupCallerTeams returned LiteLLM team IDs while authorized_teams holds
aliases, so HasIntersect produced false negatives (empty env list / 403
hydrate) for non-admin members of an authorized team. Resolve each caller
team id to its alias via new ListAllTeams and return both forms."
```

---

## Task 3: Set `key_alias` to the ACH key id on LiteLLM mint (debug attribution)

**Files:**
- Modify: `internal/platformapi/auth/sso.go:419-427` (pk_ mint `KeyGenerateRequest`)
- Modify: `internal/platformapi/envkeys/handler.go:402-413` (ek_ mint `KeyGenerateRequest`)
- Test: the existing unit tests that exercise these handlers and can assert the `KeyGenerateRequest` passed to a fake LiteLLM (search `KeyGenerate` in `internal/platformapi/auth/*_test.go` and `internal/platformapi/envkeys/*_test.go`; add an assertion to the closest existing happy-path mint test)

**Note:** `KeyGenerateRequest.KeyAlias` already exists (`internal/litellm/types.go:296`) and is returned in the response. LiteLLM enforces `key_alias` uniqueness; `pkid_`/`ekid_` are unique ULIDs (and the envkeys WARN-03 retry mints a fresh `ekid_`), so no collision. This is **debug-only** — nothing reads `key_alias` back for routing.

**Step 1: Write the failing test**

In the closest existing pk_ mint test (a test that drives `mintAndPersistPK` / the SSO callback with a fake LiteLLM capturing the request), assert:

```go
// the fake LiteLLM should have been called with KeyAlias == the minted pkid_.
if gotReq.KeyAlias == "" || gotReq.KeyAlias != gotReq.Metadata["ach_key_id"] {
	t.Fatalf("KeyGenerate KeyAlias = %q, want it to equal metadata ach_key_id %q",
		gotReq.KeyAlias, gotReq.Metadata["ach_key_id"])
}
```

Add the equivalent assertion to the ek_ mint test in the envkeys package. If the fake currently discards the request, capture it (store the last `*litellm.KeyGenerateRequest` on the fake).

**Step 2: Run tests to verify they fail**

Run:
```
make test-unit-pkg PKG=./internal/platformapi/auth/...
make test-unit-pkg PKG=./internal/platformapi/envkeys/...
```
Expected: FAIL — `KeyGenerate KeyAlias = "" …`.

**Step 3: Write minimal implementation**

`internal/platformapi/auth/sso.go` — add `KeyAlias: keyID,` to the pk_ request:

```go
	keyResp, err := deps.LiteLLM.KeyGenerate(ctx, &litellm.KeyGenerateRequest{
		UserID:    userID,
		KeyAlias:  keyID, // pkid_… — debug attribution only (not used for lookup)
		MaxBudget: nil,
		Metadata: map[string]string{
			"ach_key_id":      keyID,
			"ach_key_type":    "pk",
			"ach_owner_email": email,
		},
	})
```

`internal/platformapi/envkeys/handler.go` — add `KeyAlias: keyID,` to the ek_ request:

```go
	keyReq := &litellm.KeyGenerateRequest{
		UserID:       userID,
		KeyAlias:     keyID, // ekid_… — debug attribution only (not used for lookup)
		MaxBudget:    nil,
		AccessGroups: []string{env.Name},
		Tags:         []string{env.Name},
		Metadata: map[string]string{
			"ach_key_id":      keyID,
			"ach_key_type":    "ek",
			"ach_owner_email": cr.keyCtx.OwnerEmail,
			"ach_environment": env.Name,
		},
	}
```

**Step 4: Run tests to verify they pass**

Run:
```
make test-unit-pkg PKG=./internal/platformapi/auth/...
make test-unit-pkg PKG=./internal/platformapi/envkeys/...
```
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/platformapi/auth/sso.go internal/platformapi/envkeys/handler.go \
        internal/platformapi/auth internal/platformapi/envkeys
git commit -m "feat(platform-api): set LiteLLM key_alias to ACH key id

Stamp pk_/ek_ virtual keys with key_alias=<pkid_/ekid_> for human
attribution in the LiteLLM UI/DB. Debug-only; nothing reads it back."
```

---

## Final verification gate (before push)

Per CLAUDE.md these touch `internal/controller`/`platformapi`/`db` + `internal/litellm` — run the full local gate:

```bash
make qa-lint-changed
make test-unit
./scripts/dev.sh go test -tags=integration ./internal/db/... -count=1   # Task 1 (needs docker)
make e2e-full                                                            # mandatory for controller/platformapi changes
```

Expected: all green. `make e2e-full` keeps the cluster up — diagnose with `make logs-operator` / `make logs-platform-api` if red. Then let the installed pre-push hook (`make hooks`) gate the push — never `--no-verify`.

## Deferred — track separately (NOT in this plan)

- **Coexist with live token-factory users.** ACH keys are invisible to the ackstorm `auth_user_map.sso_key_swapper` (it keys off `key_alias LIKE %email%` + `metadata.email`). Decide whether ACH should mint swapper-discoverable keys (alias/metadata carry the email) or whether ACH users simply bypass the LiteLLM admin UI. Fix 3 here intentionally does NOT do this (alias = ACH key id, not email).
- **`UpsertEnvironment` test helper:** if `internal/db` has no exported `newEnvTestPool`, reuse the unexported setup from `TestUpsertEnvironment_InsertThenUpdate` (same package) — do not add a new public helper.
