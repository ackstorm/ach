# Environment `spec.description` + narrow `notice` to hydrate-only

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add `Environment.spec.description` (catalog metadata shown in `ach-cli env list` + `env describe`), and narrow the existing `spec.notice` so it is shown **only after `ach-cli env hydrate`** (remove it from `env list` / `env describe`).

**Architecture:** Mirrors the v0.4.4 `notice` slice. `description` rides the DB-SoT path into the **env list/describe** responses (`EnvironmentView`), NOT the hydrate response. `notice` keeps its hydrate path (`HydrateResponse` + `manifest.Manifest` + hydrate summary) and loses its list/describe surfaces.

**Surfacing matrix (target):**

| Surface | Shows |
|---|---|
| `ach-cli env list` | `description` (truncated column) |
| `ach-cli env describe` | `description` (full block) |
| after `ach-cli env hydrate` | `notice` (block in summary) |

**Tech Stack:** Go (kubebuilder, pgx/v5, cobra), Postgres projection, golang-migrate.

---

## Touchpoint map

| Layer | File | Change |
|---|---|---|
| CRD | `api/ach/v1alpha1/environment_types.go` | add `Description`; fix `Notice` doc comment (hydrate-only) + regen |
| DB | `db/migrations/000013_environment_description.{up,down}.sql` | new `description` column |
| DB | `internal/db/environments.go` | `EnvironmentRow.Description` + upsert `$16` + both reads |
| Operator | `internal/controller/ach/environment_controller.go` | map `env.Spec.Description` |
| platform-api | `internal/platformapi/store/adapter.go` | `EnvironmentView`: **swap** `Notice`→`Description` + `RowToView` |
| CLI render | `internal/cli/render/render.go` | `EnvView`: swap `Notice`→`Description`; list DESCRIPTION column; describe Description block; rename `truncateNotice`→`truncateField` |
| Fixtures | `test/e2e/cluster/05-environment/demo.yaml`, `config/samples/ach_v1alpha1_environment.yaml` | add `description:` (keep `notice:`) |

**Unchanged (notice hydrate path stays intact):** `HydrateResponse.Notice`, `internal/cli/manifest/manifest.go` (`Manifest.Notice`), `cmd/ach-cli/cmd/hydrate.go` (`renderHydrateSummary` notice block + `firstNotice`), `examples/hydrate.json` golden (hydrate still emits `notice`, never emits `description`).

**Redaction note:** shell greps display-redact `manifest`→`ln` and some `.Environment`/method tokens. Trust the code blocks.

---

## Task 1: CRD — add `Description`, fix `Notice` doc, regenerate

**Files:** `api/ach/v1alpha1/environment_types.go` (+ regenerated CRD/chart/api-ref)

- [ ] **Step 1: Update the `Notice` doc comment** (it currently claims list/describe surfaces — now hydrate-only)

Replace the existing `Notice` doc comment block so it reads:
```go
	// Notice is an optional free-text advisory shown to the user ONLY after
	// `ach-cli env hydrate` (it is not surfaced in `env list` / `env describe`
	// — use Description for catalog metadata). Use it for operational reminders
	// ("re-login after key rotation") or model guidance ("works best with the
	// openai-* models"). Plain text, not interpreted; empty renders nothing.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Notice string `json:"notice,omitempty"`
```

- [ ] **Step 2: Add the `Description` field** immediately after `Notice` (keep it last)

```go
	// Description is optional catalog metadata describing what this Environment
	// is. It is surfaced in `ach-cli env list` (truncated) and `env describe`
	// (full) — the browse-time "what is this" text, distinct from Notice's
	// post-hydrate advisory. Plain text, not interpreted; empty renders nothing.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Description string `json:"description,omitempty"`
```

- [ ] **Step 3: Regenerate** (these targets do NOT auto-route on this host — use `./scripts/dev.sh make`)

```bash
./scripts/dev.sh make gen-code gen-manifests helm-sync gen-crd-ref-docs
```
Verify both CRD YAMLs gained a `description` property with `maxLength: 2048`:
```bash
grep -n 'description:' config/crd/bases/ach.ackstorm.ai_environments.yaml | head
```

- [ ] **Step 4: Confirm chart drift gate**

```bash
make helm-sync-check
```
Expected exit 0 after committing.

- [ ] **Step 5: Commit**
```bash
git add api/ach/v1alpha1/environment_types.go config/crd/bases/ deploy/helm/ach/crd-sources/ docs/api-reference/
git commit -m "feat(api): add Environment spec.description; scope notice to hydrate-only"
```

---

## Task 2: Migration `000013` — `description` column

**Files:** `db/migrations/000013_environment_description.{up,down}.sql`

- [ ] **Step 1: up**
`db/migrations/000013_environment_description.up.sql`:
```sql
-- 000013: environments.description — optional catalog metadata surfaced by
-- ach-cli env list / env describe (env spec.description). Plain text; NOT NULL
-- DEFAULT '' so existing rows and the read-side scans never see SQL NULL.
ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
```

- [ ] **Step 2: down**
`db/migrations/000013_environment_description.down.sql`:
```sql
ALTER TABLE environments
    DROP COLUMN IF EXISTS description;
```

- [ ] **Step 3: Verify migration applies (integration)**
```bash
./scripts/dev.sh go test -tags=integration ./internal/db/ -run TestUpsertEnvironment -v
```
Expected: PASS (the db integration tests apply the full migration set through 000013). A migration SQL error is a real failure; an infra/docker error is not — distinguish them.

- [ ] **Step 4: Commit**
```bash
git add db/migrations/000013_environment_description.up.sql db/migrations/000013_environment_description.down.sql
git commit -m "feat(db): add environments.description projection column (migration 000013)"
```

---

## Task 3: DB writer/reader — carry `description`

**Files:** `internal/db/environments.go`; test `internal/db/environments_description_test.go` (new)

Current state: `notice` is bound as `$15` (last bound param before `now()`); `description` becomes `$16`.

- [ ] **Step 1: Failing integration round-trip test**

Create `internal/db/environments_description_test.go`:
```go
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package db_test

import (
	"context"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertEnvironment_DescriptionRoundTrips asserts spec.description survives
// the upsert → GetEnvironmentByName → ListEnvironments projection round-trip.
func TestUpsertEnvironment_DescriptionRoundTrips(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	row := db.EnvironmentRow{
		Namespace:        "ach-system",
		Name:             "desc-demo",
		AuthorizedTeams:  []string{"default"},
		ContextPrompts:   []string{},
		ContextPlugins:   []string{},
		ContextArtifacts: []string{},
		ContextSkills:    []string{},
		RuntimeModels:    []string{},
		RuntimeMCPServers: []string{},
		RuntimeA2AAgents: []string{},
		ResourceVersion:  "1",
		Description:      "production env for the data team",
	}
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ach-system", "desc-demo")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName: got=%v err=%v", got, err)
	}
	if got.Description != row.Description {
		t.Errorf("Get description = %q, want %q", got.Description, row.Description)
	}
	list, err := db.ListEnvironments(ctx, pool, "ach-system")
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	for _, e := range list {
		if e.Name == "desc-demo" && e.Description != row.Description {
			t.Errorf("List description = %q, want %q", e.Description, row.Description)
		}
	}
}
```
NOTE: the helper is `setupPostgresForPhase2(t, ctx)` (confirmed in `internal/db/phase2_helpers_test.go`). The empty-slice array fields are REQUIRED — the `environments` schema has NOT NULL array columns.

Run it; confirm FAIL (no `Description` field).

- [ ] **Step 2: Add `Description` to `EnvironmentRow`** (after `Notice string`):
```go
	// Description is spec.description — optional catalog metadata (may be "").
	Description string
```

- [ ] **Step 3: Add `description` to the upsert as `$16`**

In `upsertEnvironmentSQL`, change the column list `... context_skills, notice, updated_at ...` to:
```
	     resource_version, context_skills, notice, description, updated_at, origin, locked)
```
Change the VALUES `$13, $14, $15, now(), 'cr', TRUE)` to:
```
	        $13, $14, $15, $16, now(), 'cr', TRUE)
```
Add to ON CONFLICT DO UPDATE SET (after the `notice = EXCLUDED.notice,` line):
```
	    description                            = EXCLUDED.description,
```
In `UpsertEnvironmentTx`, append `row.Description` after `row.Notice`:
```go
		row.ContextSkills,
		row.Notice,
		row.Description,
	)
```

- [ ] **Step 4: Add `description` to both reads**

In `GetEnvironmentByName` AND `ListEnvironments`, change the SELECT fragment `context_skills, notice,` to `context_skills, notice, description,`, and add `&r.Description` right after `&r.Notice` in each `Scan`.

- [ ] **Step 5:** Run the test → PASS. `gofmt -l` the two files; fix if listed.

- [ ] **Step 6: Commit**
```bash
git add internal/db/environments.go internal/db/environments_description_test.go
git commit -m "feat(db): carry environments.description through upsert + reads"
```

---

## Task 4: Operator — map `spec.description`

**Files:** `internal/controller/ach/environment_controller.go`

- [ ] **Step 1:** In the `achdb.EnvironmentRow{...}` literal, add after `Notice: env.Spec.Notice,`:
```go
		Description:                          env.Spec.Description,
```
Run `./scripts/dev.sh gofmt -w internal/controller/ach/environment_controller.go`.

- [ ] **Step 2:** `./scripts/dev.sh go build ./internal/controller/...` → clean.

- [ ] **Step 3: Commit**
```bash
git add internal/controller/ach/environment_controller.go
git commit -m "feat(operator): project Environment spec.description into the row"
```

---

## Task 5: platform-api — swap list/describe surface from `notice` to `description`

**Files:** `internal/platformapi/store/adapter.go`; rename `adapter_notice_test.go` → `adapter_description_test.go`

`notice` leaves `EnvironmentView` (list/describe wire); `description` takes its place. The hydrate handler/`HydrateResponse` is NOT touched.

- [ ] **Step 1: Edit `EnvironmentView`** — replace the `Notice string json:"notice,omitempty"` field with:
```go
	Description       string                   `json:"description,omitempty"`
```

- [ ] **Step 2: Edit `RowToView`** — replace `Notice: r.Notice,` with:
```go
		Description:     r.Description,
```

- [ ] **Step 3: Convert the test** — replace `internal/platformapi/store/adapter_notice_test.go` with `internal/platformapi/store/adapter_description_test.go`:
```go
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestRowToView_DescriptionPassThrough asserts the env detail view surfaces the
// row's description verbatim (notice is no longer on the list/describe wire).
func TestRowToView_DescriptionPassThrough(t *testing.T) {
	v := RowToView(db.EnvironmentRow{
		Namespace:   "ach-system",
		Name:        "demo",
		Description: "production env for the data team",
	})
	if v.Description != "production env for the data team" {
		t.Errorf("Description = %q, want pass-through", v.Description)
	}
}
```
Remove the old `adapter_notice_test.go` (`git rm`).

- [ ] **Step 4: Verify no other consumer of `EnvironmentView.Notice` exists** before relying on its removal:
```bash
rg -n 'EnvironmentView\b' internal/ | rg -i notice
rg -n '\.Notice\b' internal/platformapi/
```
Expected: no remaining reference to `EnvironmentView`'s notice field (the hydrate path uses `HydrateResponse.Notice` + `db.EnvironmentRow.Notice`, which are untouched). If a consumer turns up, STOP and report.

- [ ] **Step 5:** `./scripts/dev.sh go test ./internal/platformapi/store/ ./internal/platformapi/hydrate/ -v` → PASS (hydrate tests still green — notice path intact).

- [ ] **Step 6: Commit**
```bash
git add internal/platformapi/store/adapter.go internal/platformapi/store/adapter_description_test.go
git rm internal/platformapi/store/adapter_notice_test.go 2>/dev/null; git add -A internal/platformapi/store/
git commit -m "feat(platform-api): surface description (not notice) in env list/describe views"
```

---

## Task 6: CLI render — description in list/describe, drop notice from them

**Files:** `internal/cli/render/render.go`, `internal/cli/render/render_test.go`

The hydrate summary (`cmd/ach-cli/cmd/hydrate.go`) and `manifest.Manifest` are NOT touched — notice still renders post-hydrate.

- [ ] **Step 1: `EnvView`** — replace the `Notice string json:"notice,omitempty"` field with:
```go
	Description string `json:"description,omitempty"`
```

- [ ] **Step 2: Rename helper + retarget** — rename `truncateNotice` to `truncateField` (generic) and update its doc comment to "collapses a field to its first non-empty line and caps it at max runes". Update the single call site.

- [ ] **Step 3: `FormatEnvList`** — swap the NOTICE column for DESCRIPTION:
```go
	_, _ = fmt.Fprintln(tw, "NAME\tNAMESPACE\tSTATUS\tDESCRIPTION")
	for _, e := range envs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, e.Namespace, e.Status, truncateField(e.Description, 40))
	}
```

- [ ] **Step 4: `FormatEnvDescribe`** — replace the `if env.Notice != "" { ... "Notice:" ... }` block with:
```go
	if env.Description != "" {
		_, _ = fmt.Fprintf(&sb, "Description:\n  %s\n", strings.ReplaceAll(env.Description, "\n", "\n  "))
	}
```

- [ ] **Step 5: Tests** — in `internal/cli/render/render_test.go`, rename/retarget the two notice tests to description:
  - `TestFormatEnvList_NoticeTruncated` → `TestFormatEnvList_DescriptionTruncated`: build `EnvView{... Description: "first line of the notice\nsecond line..."}`, assert header contains `DESCRIPTION`, first line present, second line absent.
  - `TestFormatEnvDescribe_NoticeFull` → `TestFormatEnvDescribe_DescriptionFull`: `EnvView{... Description:"line one\nline two"}`, assert `Description:` block + both lines.
  Keep all other render tests. (The hydrate-summary notice test lives in `cmd/ach-cli/cmd/hydrate_test.go` and stays.)

- [ ] **Step 6:** Run:
```bash
./scripts/dev.sh go test ./internal/cli/render/ ./internal/cli/hydrate/ ./cmd/ach-cli/... -v
```
Expected: PASS. If a pre-existing `FormatEnvList`/`FormatEnvDescribe` test asserted the NOTICE column/block, retarget it to DESCRIPTION (intended change). `gofmt -l`; fix if listed.

- [ ] **Step 7: Commit**
```bash
git add internal/cli/render/render.go internal/cli/render/render_test.go
git commit -m "feat(cli): render description in env list/describe; notice now hydrate-only"
```

---

## Task 7: Fixtures + e2e proof

**Files:** `config/samples/ach_v1alpha1_environment.yaml`, `test/e2e/cluster/05-environment/demo.yaml`

The `examples/hydrate.json` golden is UNCHANGED (hydrate still emits `notice`, never `description`).

- [ ] **Step 1: Sample** — in `config/samples/ach_v1alpha1_environment.yaml`, add under `spec:` (alongside the existing `notice:`):
```yaml
  description: "Example environment for the kustomize sample."
```

- [ ] **Step 2: Demo fixture** — in `test/e2e/cluster/05-environment/demo.yaml`, add under `spec:` (keep the existing `notice:` line):
```yaml
  description: "demo environment — data-team sandbox"
```

- [ ] **Step 3: e2e** — run the full gate:
```bash
make e2e-full
```
Expected: green, including the unchanged byte-exact `hydrate_golden_diff` (notice still flows; description is absent from hydrate output so the golden holds). The `env_list` subtest still passes (substring check on `demo`). With the cluster up, spot-check:
```bash
./scripts/dev.sh kubectl -n ach-system get environment demo -o jsonpath='{.spec.description}'
```
Expected: prints `demo environment — data-team sandbox`. If `hydrate_golden_diff` fails, the golden drifted — inspect whether `description` leaked into `HydrateResponse` (it must NOT).

- [ ] **Step 4: Commit**
```bash
git add config/samples/ach_v1alpha1_environment.yaml test/e2e/cluster/05-environment/demo.yaml
git commit -m "test(e2e): exercise Environment spec.description in env list/describe"
```

---

## Task 8: Gates + release

- [ ] **Step 1:** `make test-unit` → PASS
- [ ] **Step 2:** `make qa-lint-changed` → exit 0
- [ ] **Step 3:** `make test-envtest` → PASS
- [ ] **Step 4:** `make pre-push` (18 gates, host-only) → Failures: 0. Watch for spurious `go.sum` bloat from tooling — `git checkout go.sum` if `go.mod` is unchanged and the feature added no deps.
- [ ] **Step 5:** Push, open PR, `gh pr merge --squash --admin --delete-branch`, sync main.
- [ ] **Step 6:** `make release-cut VERSION=0.4.5`; watch `release.yml` to success.

---

## Verification summary

- **Unit:** `RowToView` passes description through; `FormatEnvList` shows a truncated DESCRIPTION column; `FormatEnvDescribe` shows a full Description block; hydrate-summary notice block + `manifest.Decode` notice still green (untouched).
- **Integration:** migration 000013 applies; description round-trips through upsert/reads.
- **envtest:** operator builds + reconciles with `spec.description`.
- **e2e:** `description` readable on the CR; `hydrate_golden_diff` unchanged (notice-only hydrate); `env_list` green.
- **Gates:** helm-sync-check, lint, pre-push (18) green.

## Out of scope
- `description` in the hydrate response (it's a catalog field, not a hydrate concern).
- Markdown/severity/localization.
- Removing `notice` from the DB/hydrate path (it stays — only its list/describe CLI/wire surfaces are removed).
