# Environment `spec.notice` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional `spec.notice` free-text field to the `Environment` CRD that is surfaced to the user after `ach-cli env hydrate` and in `env describe` / `env list`.

**Architecture:** ACH is DB-as-SoT: the operator is the only Kubernetes watcher and writes a projection row; platform-api reads that row to serve `/platform/hydrate` and `/platform/environments`. So `notice` rides the full vertical slice — CR spec → operator projection → `environments` table column → two platform-api response shapes (`HydrateResponse` + `EnvironmentView`) → two CLI decoders (`manifest.Manifest` + `render.EnvView`) → three render sites. The field is plain text, optional, `MaxLength=2048`; empty renders nothing anywhere.

**Tech Stack:** Go (controller-runtime, kubebuilder markers, pgx/v5, cobra), Postgres projection table, golang-migrate file source.

---

## Touchpoint map (for orientation)

| Layer | File | Change |
|---|---|---|
| CR spec | `api/ach/v1alpha1/environment_types.go` | add `Notice` to `EnvironmentSpec` + regen |
| DB schema | `db/migrations/000012_environment_notice.{up,down}.sql` | new `notice` column |
| DB writer/reader | `internal/db/environments.go` | `EnvironmentRow.Notice` + upsert + 2 SELECTs |
| Operator | `internal/controller/ach/environment_controller.go:535` | map `env.Spec.Notice` into row |
| platform-api read | `internal/platformapi/store/adapter.go` | `EnvironmentView.Notice` + `RowToView` |
| platform-api hydrate | `internal/platformapi/hydrate/handler.go` | `HydrateResponse.Notice` + resp build |
| CLI hydrate decode | `internal/cli/manifest/manifest.go` | `Manifest.Notice` (strict decode) |
| CLI env decode/render | `internal/cli/render/render.go` | `EnvView.Notice` + list/describe formatters |
| CLI hydrate render | `internal/cli/hydrate/result.go` + `commit.go` + `cmd/ach-cli/cmd/hydrate.go` | `Result.Notice` + summary block |
| Fixtures/docs | `config/samples/`, `test/e2e/cluster/05-environment/demo.yaml`, `docs/api-reference/` | sample + e2e + regen |

**Display-redaction note:** the shell tooling redacts the literal token `manifest` to `ln` and some `.Environment` references in greps — the real package is `internal/cli/manifest` (type `manifest.Manifest`) and the real struct field is `Result.Environment`. Trust the code blocks below, not raw grep echoes.

---

## Task 1: Add `Notice` to the CRD spec + regenerate

**Files:**
- Modify: `api/ach/v1alpha1/environment_types.go` (EnvironmentSpec, after `AuthorizedTeams`)
- Regenerated (do not hand-edit): `config/crd/bases/ach.ackstorm.ai_environments.yaml`, `deploy/helm/ach/crd-sources/ach.ackstorm.ai_environments.yaml`, `api/ach/v1alpha1/zz_generated.deepcopy.go` (no change expected — string field), `docs/api-reference/`

- [ ] **Step 1: Add the field to `EnvironmentSpec`**

In `api/ach/v1alpha1/environment_types.go`, inside `type EnvironmentSpec struct`, add immediately after the `AuthorizedTeams []string` field (keep it last in the struct):

```go
	// Notice is an optional free-text advisory shown to the user after
	// `ach-cli env hydrate` and in `env describe` / `env list`. Use it for
	// operational reminders ("re-login after key rotation") or model guidance
	// ("works best with the openai-* models"). Plain text, not interpreted;
	// empty (the default) renders nothing anywhere.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Notice string `json:"notice,omitempty"`
```

- [ ] **Step 2: Regenerate CRD manifests, chart CRD, deepcopy, API-ref docs**

Run:
```bash
make gen-code gen-manifests helm-sync gen-crd-ref-docs
```
Expected: `git status` shows modified `config/crd/bases/ach.ackstorm.ai_environments.yaml`, `deploy/helm/ach/crd-sources/ach.ackstorm.ai_environments.yaml`, and a `docs/api-reference/` file. Confirm the CRD YAML now contains a `notice` property:
```bash
grep -n 'notice' config/crd/bases/ach.ackstorm.ai_environments.yaml deploy/helm/ach/crd-sources/ach.ackstorm.ai_environments.yaml
```
Expected: both files print a `notice:` schema block with `maxLength: 2048` and `type: string`.

- [ ] **Step 3: Verify the chart-drift gate is satisfied**

Run:
```bash
make helm-sync-check
```
Expected: exit 0 (no uncommitted CRD drift — `helm-sync` already ran in Step 2).

- [ ] **Step 4: Commit**

```bash
git add api/ach/v1alpha1/environment_types.go config/crd/bases/ deploy/helm/ach/crd-sources/ docs/api-reference/
git commit -m "feat(api): add Environment spec.notice post-hydrate advisory field"
```

---

## Task 2: Add the `notice` projection column

**Files:**
- Create: `db/migrations/000012_environment_notice.up.sql`
- Create: `db/migrations/000012_environment_notice.down.sql`

Migrations load from `db/migrations/` via `db.Migrate(connStr, path)` (golang-migrate file source) — a new numbered pair is picked up automatically; no embed/registration code to touch.

- [ ] **Step 1: Write the up migration**

`db/migrations/000012_environment_notice.up.sql`:
```sql
-- 000012: environments.notice — optional post-hydrate advisory surfaced by
-- ach-cli (env spec.notice). Plain text; NOT NULL DEFAULT '' so existing rows
-- and the read-side scans never see SQL NULL.
ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS notice text NOT NULL DEFAULT '';
```

- [ ] **Step 2: Write the down migration**

`db/migrations/000012_environment_notice.down.sql`:
```sql
ALTER TABLE environments
    DROP COLUMN IF EXISTS notice;
```

- [ ] **Step 3: Verify the migration applies + rolls back (integration)**

Run the db integration suite (testcontainers spin a real Postgres and apply every migration through 000012):
```bash
make test-integration
```
Expected: PASS — migration set applies cleanly through 000012 (any existing `internal/db` integration test exercises `db.Migrate`, which fails loudly on a bad migration).

- [ ] **Step 4: Commit**

```bash
git add db/migrations/000012_environment_notice.up.sql db/migrations/000012_environment_notice.down.sql
git commit -m "feat(db): add environments.notice projection column (migration 000012)"
```

---

## Task 3: Carry `notice` through the DB writer/reader

**Files:**
- Modify: `internal/db/environments.go`
- Test: `internal/db/environments_notice_test.go` (new, integration-tagged)

- [ ] **Step 1: Write the failing integration round-trip test**

Create `internal/db/environments_notice_test.go`:
```go
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package db_test

import (
	"context"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertEnvironment_NoticeRoundTrips asserts spec.notice survives the
// upsert → GetEnvironmentByName → ListEnvironments projection round-trip.
func TestUpsertEnvironment_NoticeRoundTrips(t *testing.T) {
	ctx := context.Background()
	pool := setupPostgresForEnvTest(t) // see note below

	row := db.EnvironmentRow{
		Namespace:       "ach-system",
		Name:            "notice-demo",
		AuthorizedTeams: []string{"default"},
		ResourceVersion: "1",
		Notice:          "remember to re-login after key rotation",
	}
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}

	got, err := db.GetEnvironmentByName(ctx, pool, "ach-system", "notice-demo")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName: got=%v err=%v", got, err)
	}
	if got.Notice != row.Notice {
		t.Errorf("Get notice = %q, want %q", got.Notice, row.Notice)
	}

	list, err := db.ListEnvironments(ctx, pool, "ach-system")
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	var found bool
	for _, e := range list {
		if e.Name == "notice-demo" {
			found = true
			if e.Notice != row.Notice {
				t.Errorf("List notice = %q, want %q", e.Notice, row.Notice)
			}
		}
	}
	if !found {
		t.Fatal("notice-demo not in ListEnvironments output")
	}
}
```

**Note on `setupPostgresForEnvTest`:** reuse the existing per-package Postgres testcontainer harness in `internal/db` (the same helper the other `//go:build integration` db tests call — grep `internal/db/*_test.go` for the `setupPostgres`-style helper that returns a `*pgxpool.Pool` after `db.Migrate`). If the existing helper has a different name/signature, call it instead of `setupPostgresForEnvTest`; do not add a second harness.

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
./scripts/dev.sh go test -tags=integration ./internal/db/ -run TestUpsertEnvironment_NoticeRoundTrips -v
```
Expected: FAIL — `EnvironmentRow` has no `Notice` field (compile error), or notice not persisted.

- [ ] **Step 3: Add `Notice` to `EnvironmentRow`**

In `internal/db/environments.go`, in `type EnvironmentRow struct`, add after the `ContextSkills []string` field:
```go
	// Notice is spec.notice — optional post-hydrate advisory text (may be "").
	Notice string
```

- [ ] **Step 4: Add `notice` to the upsert SQL + binding**

In `upsertEnvironmentSQL`, change the INSERT column list and VALUES to bind `notice` as `$15` (currently `context_skills` is `$14` and `updated_at` is `now()`):

Column list — change:
```
	     resource_version, context_skills, updated_at, origin, locked)
```
to:
```
	     resource_version, context_skills, notice, updated_at, origin, locked)
```

VALUES — change:
```
	        $13, $14, now(), 'cr', TRUE)
```
to:
```
	        $13, $14, $15, now(), 'cr', TRUE)
```

ON CONFLICT DO UPDATE SET — add this line after the `context_skills = EXCLUDED.context_skills,` line:
```
	    notice                                 = EXCLUDED.notice,
```

In `UpsertEnvironmentTx`, append `row.Notice` as the final positional argument (after `row.ContextSkills`):
```go
	return upsertReturning(ctx, tx, upsertEnvironmentSQL, "UpsertEnvironment("+row.Namespace+"/"+row.Name+")",
		row.Namespace, row.Name,
		row.AuthorizedTeams, row.ContextPrompts, row.ContextPlugins, row.ContextArtifacts,
		row.RuntimeModels, row.RuntimeMCPServers, row.RuntimeA2AAgents,
		row.AvailableCondition, row.AccessGroupSyncedCondition,
		row.ExecutionResourcesResolvedCondition,
		row.ResourceVersion,
		row.ContextSkills,
		row.Notice,
	)
```

- [ ] **Step 5: Add `notice` to both read SELECTs + scans**

In `GetEnvironmentByName`, change the SELECT to add `notice` right after `context_skills,`:
```
		SELECT namespace, name,
		       authorized_teams, context_prompts, context_plugins, context_artifacts,
		       context_skills, notice,
		       runtime_models, runtime_mcp_servers, runtime_a2a_agents,
		       available_condition, access_group_synced_condition,
		       execution_resources_resolved_condition,
		       deletion_timestamp, resource_version, updated_at
		  FROM environments
		 WHERE namespace = $1 AND name = $2
```
and the `Scan` to add `&r.Notice` right after `&r.ContextSkills,`:
```go
	if err := pool.QueryRow(ctx, sql, ns, name).Scan(
		&r.Namespace, &r.Name,
		&r.AuthorizedTeams, &r.ContextPrompts, &r.ContextPlugins, &r.ContextArtifacts,
		&r.ContextSkills, &r.Notice,
		&r.RuntimeModels, &r.RuntimeMCPServers, &r.RuntimeA2AAgents,
		&r.AvailableCondition, &r.AccessGroupSyncedCondition,
		&r.ExecutionResourcesResolvedCondition,
		&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt,
	); err != nil {
```

In `ListEnvironments`, apply the identical SELECT change (add `context_skills, notice,`) and the identical scan change (`&r.ContextSkills, &r.Notice,`) inside the `rows.Next()` loop.

- [ ] **Step 6: Run the test to verify it passes**

Run:
```bash
./scripts/dev.sh go test -tags=integration ./internal/db/ -run TestUpsertEnvironment_NoticeRoundTrips -v
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/db/environments.go internal/db/environments_notice_test.go
git commit -m "feat(db): carry environments.notice through upsert + reads"
```

---

## Task 4: Map `spec.notice` into the projection row (operator)

**Files:**
- Modify: `internal/controller/ach/environment_controller.go` (the `achdb.EnvironmentRow{...}` literal at ~line 535)

The env projection is covered end-to-end by e2e (Task 8); the operator-side mapping has no isolated unit seam, so this task is a one-line change verified by `make test-envtest` compiling + the e2e fixture.

- [ ] **Step 1: Add the mapping**

In `internal/controller/ach/environment_controller.go`, in the `row := achdb.EnvironmentRow{...}` literal, add after `ResourceVersion: env.ResourceVersion,`:
```go
		Notice:                              env.Spec.Notice,
```
(Align the value with the surrounding gofmt'd column; run `./scripts/dev.sh gofmt -w internal/controller/ach/environment_controller.go` if alignment shifts.)

- [ ] **Step 2: Verify it compiles**

Run:
```bash
./scripts/dev.sh go build ./internal/controller/...
```
Expected: builds clean (no unit assertion at this layer — e2e is the integration proof).

- [ ] **Step 3: Commit**

```bash
git add internal/controller/ach/environment_controller.go
git commit -m "feat(operator): project Environment spec.notice into the row"
```

---

## Task 5: Surface `notice` in both platform-api responses

**Files:**
- Modify: `internal/platformapi/store/adapter.go` (`EnvironmentView` + `RowToView`)
- Modify: `internal/platformapi/hydrate/handler.go` (`HydrateResponse` + resp build)
- Test: `internal/platformapi/store/adapter_notice_test.go` (new)
- Test: extend `internal/platformapi/hydrate/handler_test.go` if present, else new `handler_notice_test.go`

- [ ] **Step 1: Write the failing `RowToView` test**

Create `internal/platformapi/store/adapter_notice_test.go`:
```go
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestRowToView_NoticePassThrough asserts the env detail view surfaces the
// row's notice verbatim.
func TestRowToView_NoticePassThrough(t *testing.T) {
	v := RowToView(db.EnvironmentRow{
		Namespace: "ach-system",
		Name:      "demo",
		Notice:    "works best with openai-* models",
	})
	if v.Notice != "works best with openai-* models" {
		t.Errorf("Notice = %q, want pass-through", v.Notice)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run:
```bash
./scripts/dev.sh go test ./internal/platformapi/store/ -run TestRowToView_NoticePassThrough -v
```
Expected: FAIL — `EnvironmentView` has no `Notice` field (compile error).

- [ ] **Step 3: Add `Notice` to `EnvironmentView` + `RowToView`**

In `internal/platformapi/store/adapter.go`, in `type EnvironmentView struct`, add after the `Locked bool` field:
```go
	Notice string `json:"notice,omitempty"`
```
In `RowToView`, add to the `view := EnvironmentView{...}` literal (after `ResourceVersion: r.ResourceVersion,`):
```go
		Notice:          r.Notice,
```

- [ ] **Step 4: Run the store test to verify it passes**

Run:
```bash
./scripts/dev.sh go test ./internal/platformapi/store/ -run TestRowToView_NoticePassThrough -v
```
Expected: PASS.

- [ ] **Step 5: Add `Notice` to `HydrateResponse` + resp build**

In `internal/platformapi/hydrate/handler.go`, in `type HydrateResponse struct`, add after `Context ContextBlock json:"context"`:
```go
	Notice string `json:"notice,omitempty"`
```
In `HydrateHandler`, in the `resp := HydrateResponse{...}` literal (the variable `env` in scope is the `*db.EnvironmentRow`), add after `Context: toContextBlockFromRow(env, deps.BaseURL),`:
```go
			Notice:        env.Notice,
```

- [ ] **Step 6: Add a hydrate-handler notice assertion**

If `internal/platformapi/hydrate/handler_test.go` exists with a happy-path test that decodes a `HydrateResponse`, extend it to assert `resp.Notice`. Otherwise create `internal/platformapi/hydrate/handler_notice_test.go` mirroring the existing handler-test harness (same `Deps`/`store.Store` fake the package already uses) and assert that an environment row carrying `Notice: "hi"` produces a response body whose decoded `notice` == `"hi"`. Reuse the package's existing test fixtures/helpers — do not introduce a second fake store.

- [ ] **Step 7: Run the hydrate package tests**

Run:
```bash
./scripts/dev.sh go test ./internal/platformapi/hydrate/ -v
```
Expected: PASS (including the new notice assertion).

- [ ] **Step 8: Commit**

```bash
git add internal/platformapi/store/adapter.go internal/platformapi/store/adapter_notice_test.go internal/platformapi/hydrate/handler.go internal/platformapi/hydrate/handler_notice_test.go
git commit -m "feat(platform-api): surface environment notice in hydrate + env views"
```

---

## Task 6: Decode `notice` in the CLI wire types

**Files:**
- Modify: `internal/cli/manifest/manifest.go` (`Manifest` — strict `DisallowUnknownFields` decode)
- Modify: `internal/cli/render/render.go` (`EnvView`)
- Test: `internal/cli/manifest/manifest_notice_test.go` (new)
- Test: extend `internal/cli/render/render_test.go`

The hydrate command decodes into `manifest.Manifest` with `DisallowUnknownFields()`, so the server emitting a new `notice` key **fails decode** unless `Manifest` declares it. This task is mandatory for hydrate not to regress.

- [ ] **Step 1: Write the failing manifest-decode test**

Create `internal/cli/manifest/manifest_notice_test.go`:
```go
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"strings"
	"testing"
)

// TestDecode_NoticeAccepted asserts the strict decoder accepts and binds the
// notice field (regression guard: DisallowUnknownFields would 400 otherwise).
func TestDecode_NoticeAccepted(t *testing.T) {
	body := `{"schemaVersion":"v1alpha1","environment":"demo",` +
		`"runtime":{"models":[],"mcpServers":[],"a2aAgents":[]},` +
		`"context":{"prompts":[],"plugins":[],"artifacts":[],"skills":[]},` +
		`"notice":"re-login after rotation"}`
	m, err := Decode(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m.Notice != "re-login after rotation" {
		t.Errorf("Notice = %q, want decoded", m.Notice)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run:
```bash
./scripts/dev.sh go test ./internal/cli/manifest/ -run TestDecode_NoticeAccepted -v
```
Expected: FAIL — strict decode rejects unknown field `notice` (and `m.Notice` undefined).

- [ ] **Step 3: Add `Notice` to `Manifest`**

In `internal/cli/manifest/manifest.go`, in `type Manifest struct`, add after `Context *ContextBlock json:"context"`:
```go
	Notice string `json:"notice,omitempty"`
```

- [ ] **Step 4: Run it to verify it passes**

Run:
```bash
./scripts/dev.sh go test ./internal/cli/manifest/ -run TestDecode_NoticeAccepted -v
```
Expected: PASS.

- [ ] **Step 5: Add `Notice` to `EnvView`**

In `internal/cli/render/render.go`, in `type EnvView struct`, add after the `Status string` field:
```go
	Notice    string `json:"notice,omitempty"`
```

- [ ] **Step 6: Build to confirm no breakage**

Run:
```bash
./scripts/dev.sh go build ./internal/cli/...
```
Expected: builds clean.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/manifest/manifest.go internal/cli/manifest/manifest_notice_test.go internal/cli/render/render.go
git commit -m "feat(cli): decode environment notice in manifest + env view wire types"
```

---

## Task 7: Render the notice (env list, env describe, hydrate summary)

**Files:**
- Modify: `internal/cli/render/render.go` (`FormatEnvList`, `FormatEnvDescribe`, + a `truncateNotice` helper)
- Modify: `internal/cli/hydrate/result.go` (`Result.Notice`)
- Modify: `internal/cli/hydrate/commit.go` (set `result.Notice`)
- Modify: `cmd/ach-cli/cmd/hydrate.go` (`summaryFromResult` / `summaryFromResultsCompact` notice block)
- Test: extend `internal/cli/render/render_test.go`

- [ ] **Step 1: Write the failing render tests**

Append to `internal/cli/render/render_test.go`:
```go
// TestFormatEnvList_NoticeTruncated asserts the list shows a truncated,
// single-line notice in the NOTICE column.
func TestFormatEnvList_NoticeTruncated(t *testing.T) {
	out := FormatEnvList([]EnvView{
		{Name: "demo", Namespace: "ach-system", Status: "Available",
			Notice: "first line of the notice\nsecond line that must not appear"},
	})
	if !strings.Contains(out, "NOTICE") {
		t.Errorf("list missing NOTICE column header:\n%s", out)
	}
	if strings.Contains(out, "second line") {
		t.Errorf("list leaked multi-line notice:\n%s", out)
	}
	if !strings.Contains(out, "first line") {
		t.Errorf("list dropped the notice first line:\n%s", out)
	}
}

// TestFormatEnvDescribe_NoticeFull asserts describe renders the full notice
// in a dedicated block.
func TestFormatEnvDescribe_NoticeFull(t *testing.T) {
	out := FormatEnvDescribe(
		EnvView{Name: "demo", Namespace: "ach-system", Status: "Available",
			Notice: "line one\nline two"},
		nil, false)
	if !strings.Contains(out, "Notice:") {
		t.Errorf("describe missing Notice block:\n%s", out)
	}
	if !strings.Contains(out, "line one") || !strings.Contains(out, "line two") {
		t.Errorf("describe dropped notice content:\n%s", out)
	}
}
```
(If `render_test.go` does not already import `"strings"`, it does — the existing env tests use it.)

- [ ] **Step 2: Run them to verify they fail**

Run:
```bash
./scripts/dev.sh go test ./internal/cli/render/ -run 'TestFormatEnvList_NoticeTruncated|TestFormatEnvDescribe_NoticeFull' -v
```
Expected: FAIL — no NOTICE column / no Notice block yet.

- [ ] **Step 3: Add the `truncateNotice` helper + wire `FormatEnvList`**

In `internal/cli/render/render.go`, add the helper (near the other small helpers at the bottom of the file):
```go
// truncateNotice collapses a notice to its first non-empty line and caps it at
// max runes (appending "…" when truncated) so it fits one table cell.
func truncateNotice(s string, max int) string {
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
```
Change `FormatEnvList` to add a NOTICE column:
```go
func FormatEnvList(envs []EnvView) string {
	if len(envs) == 0 {
		return "No environments visible\n"
	}
	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tNAMESPACE\tSTATUS\tNOTICE")
	for _, e := range envs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, e.Namespace, e.Status, truncateNotice(e.Notice, 40))
	}
	_ = tw.Flush()
	return sb.String()
}
```

- [ ] **Step 4: Wire `FormatEnvDescribe`**

In `FormatEnvDescribe`, after the `if env.Status != "" { ... }` header block and BEFORE the `if !hydrateAvailable || h == nil {` branch, add:
```go
	if env.Notice != "" {
		_, _ = fmt.Fprintf(&sb, "Notice:\n  %s\n", strings.ReplaceAll(env.Notice, "\n", "\n  "))
	}
```

- [ ] **Step 5: Run the render tests to verify they pass**

Run:
```bash
./scripts/dev.sh go test ./internal/cli/render/ -run 'TestFormatEnvList_NoticeTruncated|TestFormatEnvDescribe_NoticeFull' -v
```
Expected: PASS. Also run the whole package to confirm the existing `FormatEnvList`/`FormatEnvDescribe` tests still pass (the added column may need their golden strings updated):
```bash
./scripts/dev.sh go test ./internal/cli/render/ -v
```
Expected: PASS. **If pre-existing `FormatEnvList` tests assert an exact table string,** update their expectations to include the new `NOTICE` header/cell (this is an intended wire change, not a regression).

- [ ] **Step 6: Thread `Notice` into the hydrate `Result`**

In `internal/cli/hydrate/result.go`, in `type Result struct`, add after the `Environment string` field block:
```go
	// Notice is the environment's spec.notice, surfaced verbatim in the
	// post-hydrate success summary. Empty when the environment sets none.
	Notice string
```
In `internal/cli/hydrate/commit.go`, immediately after the manifest is fetched and the summaries are set (the lines `result.RuntimeSummary = c.runtimeSummary(m)` / `result.ContextSummary = c.contextSummary(m)`), add:
```go
	result.Notice = m.Notice
```

- [ ] **Step 7: Render the notice block in the hydrate summary**

In `cmd/ach-cli/cmd/hydrate.go`, render a `Notice` block in both summary shapes. The cleanest single insertion point is `renderHydrateSummary`, which has all results in hand — append the block to the returned string so it shows for both single- and multi-target runs.

Change `renderHydrateSummary`:
```go
func renderHydrateSummary(results []hydrate.Result, meta summaryMeta) string {
	var body string
	if len(results) == 1 {
		body = summaryFromResult(results[0], meta)
	} else {
		body = summaryFromResultsCompact(results, meta)
	}
	if notice := firstNotice(results); notice != "" {
		body += "\n  Notice\n    " + strings.ReplaceAll(notice, "\n", "\n    ") + "\n"
	}
	return body
}

// firstNotice returns the first non-empty environment notice across results
// (all targets hydrate the same environment, so the notice is identical when
// present).
func firstNotice(results []hydrate.Result) string {
	for _, r := range results {
		if r.Notice != "" {
			return r.Notice
		}
	}
	return ""
}
```
(Confirm `strings` is imported in `hydrate.go` — it is, used by `renderHydrateSummary` already.)

- [ ] **Step 8: Add a hydrate-summary render test**

Append to the matching test file for `hydrate.go` (e.g. `cmd/ach-cli/cmd/hydrate_test.go` — use the file that already tests `renderHydrateSummary`/`summaryFromResult`):
```go
// TestRenderHydrateSummary_NoticeBlock asserts the post-hydrate summary
// appends the environment notice when present, and omits it when empty.
func TestRenderHydrateSummary_NoticeBlock(t *testing.T) {
	withNotice := renderHydrateSummary(
		[]hydrate.Result{{Environment: "demo", PlatformID: "claude-code", Notice: "re-login first"}},
		summaryMeta{noWarnings: true})
	if !strings.Contains(withNotice, "Notice") || !strings.Contains(withNotice, "re-login first") {
		t.Errorf("missing notice block:\n%s", withNotice)
	}
	none := renderHydrateSummary(
		[]hydrate.Result{{Environment: "demo", PlatformID: "claude-code"}},
		summaryMeta{noWarnings: true})
	if strings.Contains(none, "Notice") {
		t.Errorf("notice block rendered for empty notice:\n%s", none)
	}
}
```

- [ ] **Step 9: Run the CLI tests**

Run:
```bash
./scripts/dev.sh go test ./internal/cli/render/ ./internal/cli/hydrate/ ./cmd/ach-cli/... -v
```
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/cli/render/render.go internal/cli/render/render_test.go internal/cli/hydrate/result.go internal/cli/hydrate/commit.go cmd/ach-cli/cmd/hydrate.go cmd/ach-cli/cmd/hydrate_test.go
git commit -m "feat(cli): render environment notice in hydrate summary, env describe, env list"
```

---

## Task 8: Sample, e2e fixture, and end-to-end proof

**Files:**
- Modify: `config/samples/ach_v1alpha1_environment.yaml`
- Modify: `test/e2e/cluster/05-environment/demo.yaml`

- [ ] **Step 1: Add `notice` to the kustomize sample**

In `config/samples/ach_v1alpha1_environment.yaml`, under `spec:` (alongside `authorizedTeams`), add:
```yaml
  notice: |
    This environment works best with the openai-* models.
    Remember to re-login after rotating your keys.
```

- [ ] **Step 2: Add `notice` to the e2e demo fixture**

In `test/e2e/cluster/05-environment/demo.yaml`, under `spec:`, add a single-line notice (keeps any exact-match assertions simple):
```yaml
  notice: "demo environment — re-login after key rotation"
```

- [ ] **Step 3: Verify the field survives the full vertical slice (e2e)**

Run the full e2e gate (kind + Helm + projection + hydrate). The fixture's notice must flow CR → projection → `/platform/hydrate`:
```bash
make e2e-full
```
Expected: green. With the cluster kept up, manually confirm the notice reaches the hydrate response:
```bash
./scripts/dev.sh kubectl -n ach-system get environment demo -o jsonpath='{.spec.notice}'
```
Expected: prints `demo environment — re-login after key rotation`. If the e2e suite has a hydrate-response assertion test for `demo` (grep `test/e2e` for a decode of the hydrate body), add a `Notice` field to its local decode struct and assert it equals the fixture string; otherwise the kubectl check + the unit/integration coverage above is the proof and no new e2e test is required.

- [ ] **Step 4: Commit**

```bash
git add config/samples/ach_v1alpha1_environment.yaml test/e2e/cluster/05-environment/demo.yaml
git commit -m "test(e2e): exercise Environment spec.notice through projection + hydrate"
```

---

## Task 9: Docs + final gates

**Files:**
- Modify: `examples/README.md` (only if it documents Environment spec fields) and/or `references/` if a notice-specific note is warranted
- (No CLAUDE.md change required — the doc-hygiene table maps "CRD field" → `docs/api-reference/` + `examples/`, both already updated)

- [ ] **Step 1: Update example/reference docs if they enumerate env spec fields**

Grep for where Environment spec fields are documented for users:
```bash
rg -n 'authorizedTeams|spec.context|Environment' examples/README.md references/*.md | head
```
If a section lists `spec.*` fields, add a one-line `notice` entry describing it as the post-hydrate advisory. If nothing enumerates the fields, skip (the auto-generated `docs/api-reference/` from Task 1 is the canonical reference).

- [ ] **Step 2: Run the unit + lint gates**

Run:
```bash
make test-unit
make qa-lint-changed
```
Expected: both PASS (SPDX headers present on the two new `*.go` test files — `manifest_notice_test.go`, `adapter_notice_test.go`, `environments_notice_test.go`, and any new handler notice test all start with `// SPDX-License-Identifier: Apache-2.0`).

- [ ] **Step 3: Run envtest (controller change)**

Run:
```bash
make test-envtest
```
Expected: PASS (operator compiles + reconciles with the new field).

- [ ] **Step 4: Run the full publication gate**

Run (host-only, never via `./scripts/dev.sh`):
```bash
make pre-push
```
Expected: all 18 gates PASS — notably `helm-sync-check` (chart CRD in sync), SPDX headers, `go mod tidy` clean, full lint + unit.

- [ ] **Step 5: Commit any doc changes**

```bash
git add examples/README.md references/ 2>/dev/null; git commit -m "docs: document Environment spec.notice" || echo "no doc changes to commit"
```

---

## Verification summary

- **Unit (`make test-unit`):** `manifest.Decode` accepts+binds `notice`; `RowToView` passes notice through; `FormatEnvList` truncates to one capped line; `FormatEnvDescribe` renders the full block; `renderHydrateSummary` appends a Notice block iff non-empty.
- **Integration (`make test-integration`):** migration 000012 applies; `notice` round-trips through `UpsertEnvironment` → `GetEnvironmentByName` → `ListEnvironments`.
- **envtest (`make test-envtest`):** operator builds + reconciles with `spec.notice`.
- **e2e (`make e2e-full`):** the demo fixture's notice is readable on the CR and flows into the projection; manual kubectl/hydrate spot-check confirms the wire.
- **Gates:** `make helm-sync-check`, `make qa-lint-changed`, `make pre-push` (18 gates) green.

## Out of scope

- Markdown rendering, severity levels (info/warn), or per-target notices — `notice` is a single plain string.
- Persisting the notice into `.ach/<env>/state.json` — it is transient (printed at hydrate, re-fetched each run).
- Localization / templating of the notice text.
- A `description` field distinct from `notice` (the user chose a single dedicated `notice`).
