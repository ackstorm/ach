# /v1/models chat filter — internal callers stay unfiltered Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (inline — this change is tests + docs, well under 150 lines; do NOT use subagent-driven-development). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Guarantee that ACH's own model views (the console Models page, the console Environment view, `ach-cli env describe`) are never narrowed by the new forwarder `GET /v1/models` chat-only default, and that any future ACH-internal call to the forwarder's `/v1/models` passes `?types=all`.

**Architecture:** Review of v0.13.4 (`c018e3e9` + `b0afb172`) found that **no ACH-internal caller goes through the forwarder filter today**, so no production code changes:

| Internal view | Data path | Touches forwarder `/v1/models`? |
|---|---|---|
| Console Models page (`ui/src/routes/Models.tsx`) | `GET /platform/console/capabilities?scope=personal` → `litellm.UserView.ListModelGroups` = LiteLLM `GET /model_group/info` as the user's own key (`internal/platformapi/console/handlers.go:271`) | No — direct to LiteLLM, every mode returned; the UI's mode chips start empty (= no filter) |
| Console Environment view | `capabilities?scope=environment` → `environments` projection row (`handlers.go:311`) | No — model *names* from Postgres, no mode concept |
| `ach-cli env describe` | `GET /platform/environments/{name}` + `/platform/hydrate` (`cmd/ach-cli/cmd/env.go:116`) | No — projection |
| OpenCode config / hydrate | `/model_group/info` as user; hydrate writes explicit model lists | No |
| Operator / admin runtime catalog | master-key `GET /v1/model/info` (`internal/litellm/model.go:24`) — note `/v1/model/info`, not `/v1/models` | No |
| ach-runtime harness | prices via `/v2/model/info`; no `/v1/models` call outside its tests | No |

So the plan **locks that invariant in** instead of adding `?types=all` to callers that do not exist: (1) a unit test proving the personal console catalog keeps non-chat models, (2) a source-scan guard test that fails the build if any non-test ACH source ever calls `/v1/models` without `types=all`, (3) the rule written where the next engineer will read it.

**Tech Stack:** Go 1.26 stdlib `testing`, `io/fs`/`filepath.WalkDir`; existing `fakeCatalog` in `internal/platformapi/console/handlers_test.go`.

## Global Constraints

- Host has NO Go: run tests via `make test-unit-pkg PKG=...` (auto-routes into devtools). Never prefix with `./scripts/dev.sh`.
- Every new `*.go` file starts with `// SPDX-License-Identifier: Apache-2.0`.
- No production behaviour change: forwarder default stays chat/completion-only for external clients (LibreChat).
- Docs update in the SAME commit as the code (CLAUDE.md "Documentation hygiene").
- `internal/platformapi/opencode/skill/SKILL.md`: checked — user-facing `/v1/models` behaviour is unchanged, so no edit.
- Gates: per-package unit tests + `make qa-lint-changed`. No `make e2e-full`: tests + docs only, no runtime surface moves (`git diff` will show only `_test.go` and `.md`).

---

### Task 1: Console personal catalog keeps every model mode

**Files:**
- Test: `internal/platformapi/console/handlers_test.go` (add after `TestCapabilities_PersonalUsesTheUsersOwnKey`, ~line 216)

**Interfaces:**
- Consumes: existing `testDeps(t)`, `fakeCatalog{models: ...}`, `do(t, d, path, ctx)`, `pkCtx(t, email, admin)`, `litellm.ModelGroupInfo{Name, Providers, Mode *string}`.
- Produces: nothing.

- [ ] **Step 1: Write the test**

```go
// TestCapabilities_PersonalKeepsEveryMode: the console Models page is ACH's
// own view and must list what the forwarder's GET /v1/models hides by default
// (embedding, transcription, image…). It reads /model_group/info directly,
// never the forwarder; this pins that it applies no mode filter of its own.
func TestCapabilities_PersonalKeepsEveryMode(t *testing.T) {
	d := testDeps(t)
	var models []litellm.ModelGroupInfo
	for _, m := range []string{"chat", "embedding", "audio_transcription", "image_generation"} {
		mode := m
		models = append(models, litellm.ModelGroupInfo{Name: "m-" + m, Providers: []string{"openai"}, Mode: &mode})
	}
	d.AsUser = func(string) UserReads { return &fakeCatalog{models: models} }
	rec := do(t, d, "/platform/console/capabilities?scope=personal", pkCtx(t, "u@x.com", false))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, m := range models {
		if !strings.Contains(rec.Body.String(), `"`+m.Name+`"`) {
			t.Fatalf("%s missing from the console catalog: %s", m.Name, rec.Body)
		}
	}
}
```

- [ ] **Step 2: Run it**

Run: `make test-unit-pkg PKG=./internal/platformapi/console/`
Expected: PASS (this is a characterisation test — the behaviour already holds). Sanity-check it can fail: temporarily add `if g.Mode != nil && *g.Mode != "chat" { continue }` inside the loop at `handlers.go:278`, re-run, expect FAIL `m-embedding missing from the console catalog`, then revert that line (`git diff internal/platformapi/console/handlers.go` must be empty).

- [ ] **Step 3: Commit** (folded into Task 3's commit — one commit series, see Task 3 Step 4).

---

### Task 2: Guard — ACH never calls its own `/v1/models` without `?types=all`

**Files:**
- Create: `internal/forwarder/proxy/models_selfcall_test.go`

**Interfaces:**
- Consumes: nothing (stdlib only). Repo root is `../../..` from the package dir (`internal/forwarder/proxy`).
- Produces: nothing.

The scan looks only at string-literal starts (`"/v1/models`, `` `/v1/models ``, `'/v1/models`) so comments that merely mention the route (e.g. `internal/litellm/shellteam.go:29`) do not trip it. Tests, docs, `node_modules`, `.gocache`, `dist` and the forwarder's own filter (`models.go`) are out of scope. Today it finds zero hits — it exists so the next internal caller is forced to pass `types=all`.

- [ ] **Step 1: Write the guard**

```go
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// selfCallRe matches a string literal that starts with the forwarder's model
// list route — a real call, not a comment that mentions it.
var selfCallRe = regexp.MustCompile("[\"'`]/v1/models")

// TestNoInternalModelListWithoutTypesAll: GET /v1/models hides non-chat models
// by default (models.go) for chat clients like LibreChat. When ACH itself
// lists models through the forwarder (console, describe, hydrate…) it must
// see the whole catalog, so every such call in non-test source carries
// ?types=all on the same line.
func TestNoInternalModelListWithoutTypesAll(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	skipDir := map[string]bool{".git": true, ".gocache": true, "node_modules": true, "dist": true, "docs": true, "test": true}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		ext := filepath.Ext(name)
		if (ext != ".go" && ext != ".ts" && ext != ".tsx") ||
			strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") ||
			p == filepath.Join(root, "internal", "forwarder", "proxy", "models.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if selfCallRe.MatchString(line) && !strings.Contains(line, "types=all") {
				t.Errorf("%s:%d: internal /v1/models call without ?types=all: %s", p, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Prove it can fail**

Temporarily create `internal/platformapi/zz_selfcall_probe.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package platformapi

const probeModelsPath = "/v1/models"
```

Run: `make test-unit-pkg PKG=./internal/forwarder/proxy/`
Expected: FAIL `internal/platformapi/zz_selfcall_probe.go:5: internal /v1/models call without ?types=all`.
Change the literal to `"/v1/models?types=all"`, re-run → PASS. Delete the probe file (`git status` must not list it).

- [ ] **Step 3: Run clean**

Run: `make test-unit-pkg PKG=./internal/forwarder/proxy/`
Expected: PASS (zero hits in today's tree). If it reports a hit, that is a real internal caller the review missed: add `types=all` to that call (query param merged with any existing ones), then re-run.

---

### Task 3: Write the rule down + commit

**Files:**
- Modify: `internal/forwarder/proxy/models.go:15-19` (the `textModes` doc comment)
- Modify: `docs/developer-guide/jwt-forwarder.md` (end of the "The one body rewrite: `GET /v1/models`" paragraph, ~line 552)
- Modify: `references/troubleshooting.md` (the "A model is missing from `GET /v1/models`" entry, ~line 715)
- Modify: `CLAUDE.md` forwarder row (the sentence ending "`?types=all` disables the filter (`proxy/models.go`, the forwarder's only response-body rewrite).")

- [ ] **Step 1: models.go comment** — replace the `textModes` comment with:

```go
// textModes are the LiteLLM model modes GET /v1/models lists by default —
// what the console labels "chat". A chat client (LibreChat at
// chat.<domain>) would otherwise offer embedding, transcription, image…
// models it cannot use. ?types=<mode>[,<mode>…] adds LiteLLM modes on top;
// ?types=all turns the filter off. ACH's own views (console, env describe)
// read LiteLLM / the projection directly and are never filtered; any future
// ACH-internal call through here MUST pass ?types=all
// (TestNoInternalModelListWithoutTypesAll enforces it).
```

- [ ] **Step 2: jwt-forwarder.md** — append to the paragraph:

```markdown
The filter is for external chat clients only. ACH's own views — the console
Models page (`/model_group/info` as the user), the console Environment view and
`ach-cli env describe` (the `environments` projection) — never go through it and
list every mode. Any ACH-internal call to the forwarder's `/v1/models` must send
`?types=all`; `TestNoInternalModelListWithoutTypesAll`
(`proxy/models_selfcall_test.go`) fails the build otherwise.
```

- [ ] **Step 3: troubleshooting.md + CLAUDE.md**

Append to the troubleshooting entry:

```markdown
The console Models page and `ach-cli env describe` are not affected — they do
not read `/v1/models`; if a model is missing THERE, it is a grant/projection
problem, not this filter.
```

In `CLAUDE.md`, change `` `?types=all` disables the filter (`proxy/models.go`, the forwarder's only response-body rewrite). `` to:

```markdown
`?types=all` disables the filter (`proxy/models.go`, the forwarder's only response-body rewrite); ACH's own views (console, `env describe`) never read it, and any ACH-internal call through it must pass `?types=all` (guard test `proxy/models_selfcall_test.go`).
```

- [ ] **Step 4: Gates + commit**

```bash
make test-unit-pkg PKG=./internal/forwarder/proxy/
make test-unit-pkg PKG=./internal/platformapi/console/
make qa-lint-changed
git diff --stat   # expect only: 2 _test.go files, models.go (comment only), 3 .md files
git add internal/forwarder/proxy/models_selfcall_test.go internal/forwarder/proxy/models.go \
  internal/platformapi/console/handlers_test.go docs/developer-guide/jwt-forwarder.md \
  references/troubleshooting.md CLAUDE.md
git commit -m "test: keep ACH's own model views out of the /v1/models chat filter"
```

Expected: both packages PASS, lint clean.
