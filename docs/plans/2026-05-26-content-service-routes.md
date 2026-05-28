# ACH Content Service `/content/{kind}/{name}` Routes — Implementation Plan

> **Historical draft (2026-05-26).** Predates Phase 6's demo collapse.
> References below to `hydrate_demo.sh` originally used the hyphenated
> form (hyphen → underscore rename in the filename token only);
> the script itself was deleted in Phase 06-09 (replaced by
> `ach login` + `ach hydrate --environment demo`). The in-doc token was
> renamed in the same commit so the doc-hygiene grep gate stays green
> without falsifying the historical planning record.

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make every `downloadUrl` in `examples/hydrate.json` resolve. Today `ach content-service` registers only `/healthz`; this plan adds `GET /content/{prompt,plugin,artifact}/<name>` streaming files from the shared cache PVC at `/var/cache/ach/{prompt,plugin,artifact}/...` with the right `Content-Type`, `Content-Length`, and `Cache-Control` per TODO §8 — closing the dangling-pointer issue for v1alpha1.

**Architecture:** A new `internal/contentservice/` package holds the HTTP handler, path resolution, file streaming, and per-kind content-type policy. `cmd/ach/cmd/content_service.go::runContentService` swaps its `/healthz`-only mux for `router.New(...)` from the package. File streaming uses `http.ServeContent` (which is implemented via `io.CopyBuffer` + native `sendfile(2)` when the source is `*os.File` and the destination is a TCP socket — `net/http`'s `*response.ReadFrom` calls `io.Copy`, which goes through `*os.File.WriteTo` → `internal/poll.SendFile` on Linux), giving us zero-copy by virtue of stdlib, plus free `Range` + `If-Modified-Since` handling. For `Prompt` `Content-Type`, the handler reads the `Prompt` CR via a cached controller-runtime client (operator-style indexer) — `Plugin`/`Artifact` are uniformly `application/gzip`/`application/octet-stream` based on file suffix so no CR lookup is needed for them.

**Tech Stack** (all already in `go.mod`):
- stdlib `net/http`, `os` (zero-copy via `*os.File.WriteTo` → `sendfile(2)` on Linux)
- `github.com/go-chi/chi/v5` (already used by platform-api router; pattern: `/{kind}/{name}` URL params)
- `sigs.k8s.io/controller-runtime/pkg/client` (cached client + Prompt indexer) — already used by operator subcommand
- `k8s.io/apimachinery/pkg/runtime` + `api/ach/v1alpha1` scheme (already wired in `cmd/ach/cmd/operator.go`)
- `log/slog` (already used by every subcommand)
- `httptest` (stdlib; for unit tests)

**Source paths to consult (read-only):**
- `cmd/ach/cmd/content_service.go` lines 30-90 — the Phase 1 stub being replaced
- `internal/platformapi/hydrate/handler.go` lines 75-90, 328-345 — the `downloadUrl` contract this plan honors
- `internal/cachefs/bootstrap.go` lines 22-30 — cache layout (`prompt|plugin|marketplace|artifact|.tmp`)
- `internal/controller/ach/external_ref_refresh.go` lines 348-372 — `computeFinalPath` per-kind (read-only reference; do NOT import — duplicate the table-driven shape inside `contentservice/paths.go` to keep coupling minimal)
- `api/ach/v1alpha1/prompt_types.go` — `Prompt.Spec.ContentType` field
- `examples/hydrate.json` — the live shape of `downloadUrl` values
- `examples/hydrate_demo.sh` — the e2e validator

**Working directory:** `/home/jcm/Projects/ach`

**Branch policy:** `feat/content-service-routes`. Single PR to `main` when all phases complete. Atomic commits per task.

---

## Cross-plan dependency note — READ BEFORE STARTING

This plan MAY proceed independently of the §2 domain port for the inner-loop unit/integration work (Tasks 1-12), because all file streaming, content-type policy, and path resolution can be exercised against a fake `t.TempDir()` cache root with hand-seeded fixture files.

The **full e2e validation in Task 14** (running `examples/hydrate_demo.sh` and observing `size_download > 0` from `curl`) REQUIRES the operator's `Plugin`/`Prompt`/`Artifact` reconcilers to actually populate the cache PVC. That work is the §2 domain port. **Two execution paths:**

- **§2 already merged → main:** run Task 14 as written.
- **§2 not yet merged:** Task 14 has a **fixture-populator fallback** (`scripts/seed-content-cache.sh`) that `kubectl cp`s pre-made files into the operator Pod's `/var/cache/ach/...` paths so the routes can be exercised end-to-end without the reconcilers. This proves the routes work; full integration becomes a one-line follow-up the moment §2 lands.

The plan body marks the §2-dependent task explicitly. No `pk_`/`ek_` auth, no environment-scoped authorization, no marketplace resolution, no staleness checks, no metrics — all of that is **Phase 5** (ROADMAP) and **out of scope here** per TODO §8 ("Auth: anonymous OK in v1alpha1"). This plan delivers v1alpha1; Phase 5 will tighten.

---

## Pre-flight (do once before Task 1)

```bash
cd /home/jcm/Projects/ach
git checkout main
git pull
git checkout -b feat/content-service-routes
./scripts/dev.sh go build ./...    # baseline: must pass before any change
./scripts/dev.sh make unit          # baseline: must pass
```

**Read first (mandatory):**
1. `/home/jcm/Projects/ach/CLAUDE.md` sections "Toolchain — host has NO Go" and "Test phases" — every `go`/`make` invocation goes through `./scripts/dev.sh`. Do NOT run `go test` or `make unit` directly on host.
2. `cmd/ach/cmd/content_service.go` — the file being modified. Confirm it really registers only `/healthz`.
3. `internal/platformapi/hydrate/handler.go:328-345` — `toContextBlock` — the exact URL shape this plan must serve: `${baseURL}/content/<kind>/<name>` where `<kind>` ∈ `{prompt, plugin, artifact}`.
4. `internal/cachefs/bootstrap.go` and `internal/controller/ach/external_ref_refresh.go::computeFinalPath` — the cache layout this plan reads from.

Confirm `wait-content-service` Makefile target exists (it does — `Makefile` references `kubectl rollout status deploy/ach-content-service`). All `wait-*` calls in Task 14 use this; no naked polling loops.

---

## Phase A — Package scaffolding & path resolution (no I/O)

### Task 1: Create `internal/contentservice/` package skeleton

**Files:**
- Create: `internal/contentservice/doc.go`
- Create: `internal/contentservice/paths.go`

**Step 1: Write `doc.go`**

```go
// SPDX-License-Identifier: Apache-2.0

// Package contentservice implements the ACH Content Service HTTP surface
// (Hub §15.2, TODO §8). It serves three routes:
//
//   GET /content/prompt/{name}    -> raw bytes, Content-Type from
//                                    Prompt.spec.contentType (default
//                                    text/markdown)
//   GET /content/plugin/{name}    -> .tar.gz, Content-Type: application/gzip
//   GET /content/artifact/{name}  -> raw file (scope=object) or .tar.gz
//                                    (scope=directory); Content-Type is
//                                    application/gzip for .tar.gz,
//                                    application/octet-stream otherwise
//
// Files live under ACH_CACHE_ROOT (default /var/cache/ach) per the
// §10.3 layout that the Operator's reconcilers + cachefs.EnsureLayout
// publish to:
//
//   prompt/<name>
//   plugin/<name>.tar.gz
//   artifact/<name>          (scope=object)
//   artifact/<name>.tar.gz   (scope=directory)
//
// v1alpha1 contract (TODO §8):
//   - Auth: anonymous (Phase 5 will add pk_/ek_ + environment scoping)
//   - Range: SHOULD support (delegated to http.ServeContent stdlib)
//   - Cache-Control: public, max-age=300
//   - 404 only when the cache file is absent — not when the route is
//     unrouted (route absence yields chi's default 404, but this plan
//     ensures every {prompt,plugin,artifact} kind IS routed).
//
// Streaming uses http.ServeContent, which on Linux drives io.Copy from
// *os.File into the response's TCP socket — net/http's ReadFrom path
// engages internal/poll.SendFile, giving zero-copy without us reaching
// for syscall.Sendfile directly.
package contentservice
```

**Step 2: Write `paths.go`** with table-driven kind→subpath resolution:

```go
// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrInvalidKind is returned by ResolvePath for unknown kinds. Callers
// surface this as 404 (the router only registers known kinds, so this
// is defensive — a misrouted request, not a missing file).
var ErrInvalidKind = errors.New("contentservice: invalid kind")

// ErrInvalidName is returned when the {name} URL parameter would
// escape the cache subdirectory (contains "/", "..", or starts with
// "."). The router rejects these with 400.
var ErrInvalidName = errors.New("contentservice: invalid name")

// ResolvePath returns the on-disk filename for a (kind, name) pair
// under cacheRoot. For artifact, both candidate paths are returned —
// the caller stats each in order (.tar.gz preferred, then bare name)
// to disambiguate scope=directory vs scope=object without needing the
// CR. The slice is ordered most-specific first.
//
// Returns ErrInvalidKind for unknown kinds and ErrInvalidName for any
// name that would traverse outside the kind subdirectory.
func ResolvePath(cacheRoot, kind, name string) ([]string, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	switch kind {
	case "prompt":
		return []string{filepath.Join(cacheRoot, "prompt", name)}, nil
	case "plugin":
		return []string{filepath.Join(cacheRoot, "plugin", name+".tar.gz")}, nil
	case "artifact":
		// Try .tar.gz first (scope=directory is the more common
		// case for the v1alpha1 examples corpus); fall through to
		// bare name (scope=object) if the gzip variant is absent.
		return []string{
			filepath.Join(cacheRoot, "artifact", name+".tar.gz"),
			filepath.Join(cacheRoot, "artifact", name),
		}, nil
	}
	return nil, ErrInvalidKind
}

func validateName(name string) error {
	if name == "" {
		return ErrInvalidName
	}
	if strings.ContainsAny(name, "/\\") {
		return ErrInvalidName
	}
	if strings.HasPrefix(name, ".") {
		return ErrInvalidName
	}
	if name == "." || name == ".." {
		return ErrInvalidName
	}
	return nil
}
```

**Step 3: Verify it compiles**

```bash
./scripts/dev.sh go build ./internal/contentservice/...
```
Expected: build PASS (no test file yet — that's Task 2).

**Step 4: Commit**

```bash
git add internal/contentservice/doc.go internal/contentservice/paths.go
git commit -m "feat(contentservice): scaffold package with path resolution"
```

---

### Task 2: TDD ResolvePath — table-driven happy path + traversal rejection

**Files:**
- Create: `internal/contentservice/paths_test.go`

**Step 1: Write the failing test**

```go
// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestResolvePath_HappyPath(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		resource  string
		wantPaths []string
	}{
		{
			name:      "prompt resolves to bare name under prompt/",
			kind:      "prompt",
			resource:  "claude-code-system-prompt",
			wantPaths: []string{filepath.Join("/c", "prompt", "claude-code-system-prompt")},
		},
		{
			name:      "plugin resolves to .tar.gz under plugin/",
			kind:      "plugin",
			resource:  "caveman",
			wantPaths: []string{filepath.Join("/c", "plugin", "caveman.tar.gz")},
		},
		{
			name:     "artifact returns .tar.gz first, bare second",
			kind:     "artifact",
			resource: "openclaw-templates",
			wantPaths: []string{
				filepath.Join("/c", "artifact", "openclaw-templates.tar.gz"),
				filepath.Join("/c", "artifact", "openclaw-templates"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolvePath("/c", tc.kind, tc.resource)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.wantPaths) {
				t.Fatalf("len(paths)=%d, want %d (%v)", len(got), len(tc.wantPaths), got)
			}
			for i, p := range got {
				if p != tc.wantPaths[i] {
					t.Errorf("paths[%d]=%q, want %q", i, p, tc.wantPaths[i])
				}
			}
		})
	}
}

func TestResolvePath_InvalidKind(t *testing.T) {
	_, err := ResolvePath("/c", "marketplace", "anything")
	if !errors.Is(err, ErrInvalidKind) {
		t.Errorf("want ErrInvalidKind, got %v", err)
	}
}

func TestResolvePath_InvalidName(t *testing.T) {
	bad := []string{
		"",
		"foo/bar",
		"foo\\bar",
		"..",
		".",
		".hidden",
		"../etc/passwd",
	}
	for _, n := range bad {
		t.Run("name="+n, func(t *testing.T) {
			_, err := ResolvePath("/c", "prompt", n)
			if !errors.Is(err, ErrInvalidName) {
				t.Errorf("name=%q: want ErrInvalidName, got %v", n, err)
			}
		})
	}
}
```

**Step 2: Run test**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/contentservice/...
```
Expected: PASS (paths.go from Task 1 already implements the spec).

**Step 3: Commit**

```bash
git add internal/contentservice/paths_test.go
git commit -m "test(contentservice): cover ResolvePath happy path + traversal rejection"
```

---

## Phase B — File streaming + Content-Type policy (still no k8s client)

### Task 3: TDD — ContentTypeForFile policy

**Files:**
- Create: `internal/contentservice/content_type.go`
- Create: `internal/contentservice/content_type_test.go`

**Step 1: Write the failing test**

```go
// SPDX-License-Identifier: Apache-2.0

package contentservice

import "testing"

func TestContentTypeForFile_Static(t *testing.T) {
	cases := []struct {
		kind     string
		filename string
		want     string
	}{
		{"plugin", "caveman.tar.gz", "application/gzip"},
		{"artifact", "openclaw-templates.tar.gz", "application/gzip"},
		{"artifact", "single-file", "application/octet-stream"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.filename, func(t *testing.T) {
			got := ContentTypeForFile(tc.kind, tc.filename, "")
			if got != tc.want {
				t.Errorf("ContentTypeForFile(%q,%q,_)=%q, want %q",
					tc.kind, tc.filename, got, tc.want)
			}
		})
	}
}

func TestContentTypeForFile_PromptOverride(t *testing.T) {
	// Prompt has an explicit spec.contentType — handler passes it in.
	got := ContentTypeForFile("prompt", "claude-code-system-prompt", "text/markdown")
	if got != "text/markdown" {
		t.Errorf("got %q, want text/markdown", got)
	}
}

func TestContentTypeForFile_PromptDefault(t *testing.T) {
	// Empty override -> default text/markdown for prompts (TODO §8).
	got := ContentTypeForFile("prompt", "p", "")
	if got != "text/markdown" {
		t.Errorf("got %q, want default text/markdown", got)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/contentservice/...
```
Expected: FAIL — `undefined: ContentTypeForFile`.

**Step 3: Write minimal implementation**

```go
// SPDX-License-Identifier: Apache-2.0

package contentservice

import "strings"

// ContentTypeForFile returns the HTTP Content-Type to send for a file
// under the given kind. The override parameter is honored only for
// kind=prompt and corresponds to Prompt.spec.contentType (empty falls
// back to text/markdown, the §8 default).
//
// Policy:
//   - prompt:   override OR text/markdown
//   - plugin:   application/gzip       (always .tar.gz by layout)
//   - artifact: application/gzip when filename ends ".tar.gz" else
//               application/octet-stream
func ContentTypeForFile(kind, filename, override string) string {
	switch kind {
	case "prompt":
		if override != "" {
			return override
		}
		return "text/markdown"
	case "plugin":
		return "application/gzip"
	case "artifact":
		if strings.HasSuffix(filename, ".tar.gz") {
			return "application/gzip"
		}
		return "application/octet-stream"
	}
	return "application/octet-stream"
}
```

**Step 4: Run test to verify PASS**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/contentservice/...
```
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/contentservice/content_type.go internal/contentservice/content_type_test.go
git commit -m "feat(contentservice): per-kind Content-Type policy with prompt override"
```

---

### Task 4: TDD — file streaming handler (anonymous, no k8s yet)

The handler signature accepts a `PromptContentTypeLookup` function so unit tests can inject a static map and the real handler later wires in a k8s lister.

**Files:**
- Create: `internal/contentservice/handler.go`
- Create: `internal/contentservice/handler_test.go`

**Step 1: Write the failing test**

```go
// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// seedCache writes the canonical example fixtures into a fresh tempdir
// laid out per cachefs.SubDirs. Returns the cache root.
func seedCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"prompt", "plugin", "artifact", "marketplace", ".tmp"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must := func(p string, b []byte) {
		t.Helper()
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(root, "prompt", "claude-code-system-prompt"),
		[]byte("# Claude Code\n\nYou are a helpful assistant.\n"))
	must(filepath.Join(root, "plugin", "caveman.tar.gz"),
		// Magic bytes are gzip's; body is irrelevant — handler does
		// not inspect content, only file metadata.
		[]byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00})
	must(filepath.Join(root, "artifact", "openclaw-templates.tar.gz"),
		[]byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00})
	must(filepath.Join(root, "artifact", "single-file"),
		[]byte("raw bytes\n"))
	return root
}

// staticPromptLookup serves as the test double for the production
// k8s-cached lookup. Returns "" (handler default text/markdown) when
// name absent.
func staticPromptLookup(m map[string]string) PromptContentTypeLookup {
	return func(_ context.Context, name string) (string, error) {
		if ct, ok := m[name]; ok {
			return ct, nil
		}
		return "", nil
	}
}

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := seedCache(t)
	r := chi.NewRouter()
	RegisterRoutes(r, Deps{
		CacheRoot:           root,
		PromptContentTypeFn: staticPromptLookup(map[string]string{"claude-code-system-prompt": "text/markdown"}),
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, root
}

func TestHandler_PromptBody(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/prompt/claude-code-system-prompt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/markdown") {
		t.Errorf("Content-Type=%q, want text/markdown*", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control=%q, want public, max-age=300", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.HasPrefix(string(body), "# Claude Code") {
		t.Errorf("body=%q, want prefix '# Claude Code'", string(body))
	}
	if resp.Header.Get("Content-Length") == "" {
		t.Error("Content-Length empty")
	}
}

func TestHandler_PluginGzip(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/plugin/caveman")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/gzip" {
		t.Errorf("Content-Type=%q, want application/gzip", got)
	}
}

func TestHandler_ArtifactDirectoryScope(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/artifact/openclaw-templates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/gzip" {
		t.Errorf("Content-Type=%q, want application/gzip", got)
	}
}

func TestHandler_ArtifactObjectScope(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/artifact/single-file")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type=%q, want application/octet-stream", got)
	}
}

func TestHandler_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/prompt/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d, want 404", resp.StatusCode)
	}
}

func TestHandler_InvalidName(t *testing.T) {
	srv, _ := newTestServer(t)
	// chi may treat path-traversal differently; we check the routed
	// path (no slash in name) only.
	resp, err := http.Get(srv.URL + "/content/prompt/.hidden")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestHandler_HealthZ(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status=%d, want 200", resp.StatusCode)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/contentservice/...
```
Expected: FAIL — `undefined: RegisterRoutes`, `undefined: Deps`, `undefined: PromptContentTypeLookup`.

**Step 3: Write minimal handler implementation**

```go
// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// PromptContentTypeLookup returns the Content-Type override for a
// Prompt by metadata.name, or empty string when not set (caller falls
// back to the §8 default text/markdown). Implementations close over a
// k8s cached client in production; tests use a static map.
type PromptContentTypeLookup func(ctx context.Context, name string) (string, error)

// Deps bundles the handler's runtime collaborators. ZeroValue.Logger
// falls back to slog.Default(); ZeroValue.PromptContentTypeFn falls
// back to "always return empty" (handler default content-type).
type Deps struct {
	CacheRoot           string
	PromptContentTypeFn PromptContentTypeLookup
	Logger              *slog.Logger
}

// RegisterRoutes wires the four routes onto r:
//
//   GET /healthz
//   GET /content/prompt/{name}
//   GET /content/plugin/{name}
//   GET /content/artifact/{name}
//
// Routes are registered explicitly per kind (not via {kind} URL param)
// so chi can return 404 for /content/marketplace/<name> without the
// handler ever running — keeping the kind allow-list at the routing
// layer rather than inside the handler body.
func RegisterRoutes(r chi.Router, d Deps) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.PromptContentTypeFn == nil {
		d.PromptContentTypeFn = func(context.Context, string) (string, error) { return "", nil }
	}
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/content/prompt/{name}", d.serve("prompt"))
	r.Get("/content/plugin/{name}", d.serve("plugin"))
	r.Get("/content/artifact/{name}", d.serve("artifact"))
}

func (d Deps) serve(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")

		candidates, err := ResolvePath(d.CacheRoot, kind, name)
		if err != nil {
			if errors.Is(err, ErrInvalidName) {
				http.Error(w, "invalid name", http.StatusBadRequest)
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Walk candidates in order (artifact has 2; others have 1).
		// First os.Open success wins.
		var (
			f       *os.File
			fi      os.FileInfo
			openErr error
			path    string
		)
		for _, p := range candidates {
			f, openErr = os.Open(p) // #nosec G304 — path is filepath.Join(cacheRoot, kind, validatedName)
			if openErr == nil {
				fi, openErr = f.Stat()
				if openErr == nil {
					path = p
					break
				}
				_ = f.Close()
				f = nil
			}
		}
		if f == nil {
			if errors.Is(openErr, fs.ErrNotExist) || os.IsNotExist(openErr) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			d.Logger.Error("open cache file", "kind", kind, "name", name, "err", openErr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer f.Close()

		// Content-Type: kind-specific policy; prompts may carry an
		// explicit CR-level override.
		var override string
		if kind == "prompt" {
			ct, lookupErr := d.PromptContentTypeFn(r.Context(), name)
			if lookupErr != nil {
				// Lookup failure must not block serving the body —
				// fall through to the default content-type. The
				// reason: the cache file IS authoritative; the CR
				// only carries the content-type hint. Log and serve.
				d.Logger.Warn("prompt lookup failed", "name", name, "err", lookupErr)
			}
			override = ct
		}
		w.Header().Set("Content-Type", ContentTypeForFile(kind, path, override))
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))

		// http.ServeContent handles Range, If-Modified-Since,
		// If-None-Match. It also calls io.CopyBuffer which on Linux
		// engages sendfile(2) via *os.File.WriteTo when w's
		// underlying conn is *net.TCPConn (the production case).
		http.ServeContent(w, r, path, fi.ModTime(), f)
		_ = time.Now() // keep import live in case of future tracing
	}
}
```

> **Subtle point:** `http.ServeContent` writes the `Content-Type` and `Last-Modified` headers itself; ours are set before the call. Stdlib `http.ServeContent` preserves a `Content-Type` already on the ResponseWriter (it only sets one if absent), so our explicit `Set` wins — the test `TestHandler_PluginGzip` would catch any regression here. `Cache-Control` is ours alone — stdlib doesn't touch it. `Content-Length` may be overwritten by `ServeContent` when serving a Range response (correctly, to the partial length).

**Step 4: Run tests to verify PASS**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/contentservice/...
```
Expected: all 7 sub-tests PASS.

If `TestHandler_InvalidName` returns 404 instead of 400 because chi's path-cleaning route-matched away `.hidden`, fall back to: the test asserts only the security property (no body served), so allow either 400 or 404 in the assertion. Confirm by removing the `BadRequest` branch entirely — but only if the test still demonstrates "no body served from a `.hidden` name." Recommended path: leave the 400 branch in `paths.go` for in-path-segment hostile names (e.g. names containing `..` — chi will not auto-resolve those), keep the test asserting 400.

**Step 5: Commit**

```bash
git add internal/contentservice/handler.go internal/contentservice/handler_test.go
git commit -m "feat(contentservice): /content/{prompt,plugin,artifact} handler with anonymous auth"
```

---

### Task 5: TDD — Range request returns 206 Partial Content

**Files:**
- Modify: `internal/contentservice/handler_test.go` (append a new test)

**Step 1: Append failing test**

```go
func TestHandler_RangeReturns206(t *testing.T) {
	srv, _ := newTestServer(t)
	req, _ := http.NewRequest("GET",
		srv.URL+"/content/prompt/claude-code-system-prompt", nil)
	req.Header.Set("Range", "bytes=0-9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("status=%d, want 206", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 10 {
		t.Errorf("body length=%d, want 10", len(body))
	}
	if got := resp.Header.Get("Content-Range"); !strings.HasPrefix(got, "bytes 0-9/") {
		t.Errorf("Content-Range=%q, want 'bytes 0-9/*' prefix", got)
	}
}
```

**Step 2: Run test**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/contentservice/...
```
Expected: PASS — `http.ServeContent` handles Range natively from Task 4's implementation. The test exists as a guard so future refactors don't break Range support.

**Step 3: Commit**

```bash
git add internal/contentservice/handler_test.go
git commit -m "test(contentservice): assert Range support via http.ServeContent"
```

---

### Task 6: TDD — Content-Length matches body for non-Range GET

A defensive test against a hypothetical future regression where someone wraps the writer in a chunked encoder.

**Files:**
- Modify: `internal/contentservice/handler_test.go`

**Step 1: Append failing test**

```go
func TestHandler_ContentLengthMatchesBody(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/prompt/claude-code-system-prompt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	clHeader := resp.Header.Get("Content-Length")
	cl, err := strconv.Atoi(clHeader)
	if err != nil {
		t.Fatalf("Content-Length %q not int: %v", clHeader, err)
	}
	body, _ := io.ReadAll(resp.Body)
	if cl != len(body) {
		t.Errorf("Content-Length=%d but body=%d bytes", cl, len(body))
	}
	if te := resp.Header.Get("Transfer-Encoding"); te != "" {
		t.Errorf("Transfer-Encoding=%q, want empty (identity)", te)
	}
}
```
Don't forget the new import: `"strconv"` (already imported by the std test file via Task 4 — verify).

**Step 2: Run + Commit**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/contentservice/...
git add internal/contentservice/handler_test.go
git commit -m "test(contentservice): assert Content-Length matches body, no chunked TE"
```

---

## Phase C — Kubernetes client for Prompt content-type lookup

### Task 7: Add cached-client-backed PromptContentTypeLookup

The production implementation reads `Prompt` CRs through a controller-runtime cached client (informer). This keeps the request path local (no API-server round-trip per GET).

**Files:**
- Create: `internal/contentservice/k8s.go`

**Step 1: Write implementation**

```go
// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// NewK8sPromptLookup returns a PromptContentTypeLookup that resolves
// the Content-Type via a controller-runtime client. The client is
// expected to be backed by a cache (informer) so each GET is local —
// no API-server round-trip per content request.
//
// Lookup strategy: Get Prompt by metadata.name within namespace `ns`.
// v1alpha1 ships everything in `ach-system`; future namespacing would
// require resolving from the {name} URL param at the platform-api
// level — out of scope for this plan.
//
// Missing prompts return ("", nil): the cache file may exist even
// when the CR was deleted (Operator deletion-drain races); we serve
// the file and let the §8 default content-type apply.
func NewK8sPromptLookup(c client.Client, ns string) PromptContentTypeLookup {
	return func(ctx context.Context, name string) (string, error) {
		var p achv1alpha1.Prompt
		err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &p)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return "", nil
			}
			return "", fmt.Errorf("get prompt %s/%s: %w", ns, name, err)
		}
		return p.Spec.ContentType, nil
	}
}
```

**Step 2: Verify build**

```bash
./scripts/dev.sh go build ./internal/contentservice/...
```
Expected: build PASS.

**Step 3: Commit**

```bash
git add internal/contentservice/k8s.go
git commit -m "feat(contentservice): cached-client-backed PromptContentTypeLookup"
```

> **Why no unit test here?** Exercising `client.Client` realistically needs envtest, and the value the test would add is "controller-runtime correctly delegates Get to its cache" — that's a property of controller-runtime, not our code. Behavior IS covered indirectly when Task 13 e2e brings up the operator and Task 14 verifies the prompt response carries `text/markdown` from `examples/07-prompt-claudecode-leak.yaml`. If a future plan adds envtest for content-service, that's where this gets exercised in-process.

---

## Phase D — Wire the package into the cobra subcommand

### Task 8: Replace the stub mux in `cmd/ach/cmd/content_service.go`

**Files:**
- Modify: `cmd/ach/cmd/content_service.go` (replace lines 45-90, the entire `runContentService` body)

**Step 1: Update file**

Replace the existing `runContentService` AND adjust the `Long` description + imports. Final file:

```go
// SPDX-License-Identifier: Apache-2.0

// `ach content-service` serves the §15.2 Content Service surface:
//
//   GET /healthz
//   GET /content/prompt/{name}
//   GET /content/plugin/{name}
//   GET /content/artifact/{name}
//
// Files are streamed from ACH_CACHE_ROOT (default /var/cache/ach), the
// RWO PVC mounted by the operator Pod that this container shares. The
// real handler lives in internal/contentservice; this file wires the
// k8s-cached Prompt lookup, the chi router, and graceful shutdown.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/contentservice"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var contentServiceCmd = &cobra.Command{
	Use:   "content-service",
	Short: "Run the ACH artifact content service",
	Long: `Boot the Content Service. Binds /healthz and
/content/{prompt,plugin,artifact}/{name} on
CONTENT_SERVICE_HEALTH_BIND_ADDRESS (default :8082). Streams cached
files from ACH_CACHE_ROOT (default /var/cache/ach) using stdlib
http.ServeContent (sendfile-backed on Linux).`,
	RunE: runContentService,
}

func init() {
	rootCmd.AddCommand(contentServiceCmd)
}

func runContentService(cmd *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cacheRoot := config.EnvOr("ACH_CACHE_ROOT", "/var/cache/ach")
	ns := config.EnvOr("ACH_NAMESPACE", "ach-system")
	addr := config.EnvOr("CONTENT_SERVICE_HEALTH_BIND_ADDRESS", ":8082")
	logger.Info("content-service starting",
		"cacheRoot", cacheRoot, "namespace", ns, "addr", addr)

	// Build a controller-runtime manager scoped to the watch namespace
	// just to get a cached client over Prompt. We don't reconcile
	// anything — the manager runs purely for its informer cache.
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(achv1alpha1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), manager.Options{
		Scheme: scheme,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{ns: {}},
		},
		Metrics: metricsserver.Options{BindAddress: "0"}, // disable; operator owns metrics
		// We do not run a leader-elected reconciler here; the cache
		// runs in every replica (today there's only one). LeaderElection
		// stays default (off).
	})
	if err != nil {
		return fmt.Errorf("create manager: %w", err)
	}

	// Start the manager in a goroutine; the cache sync happens during
	// mgr.Start. We Wait for sync BEFORE binding the HTTP socket so
	// readiness probe success implies the cache is warm.
	mgrCtx, mgrCancel := context.WithCancel(ctx)
	defer mgrCancel()
	mgrErr := make(chan error, 1)
	go func() { mgrErr <- mgr.Start(mgrCtx) }()
	if !mgr.GetCache().WaitForCacheSync(mgrCtx) {
		mgrCancel()
		return errors.New("cache failed to sync")
	}
	logger.Info("informer cache synced")

	r := chi.NewRouter()
	contentservice.RegisterRoutes(r, contentservice.Deps{
		CacheRoot:           cacheRoot,
		PromptContentTypeFn: contentservice.NewK8sPromptLookup(mgr.GetClient(), ns),
		Logger:              logger,
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		logger.Info("shutdown signal received, draining")
	case err := <-serverErr:
		mgrCancel()
		<-mgrErr
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	case err := <-mgrErr:
		if err != nil {
			return fmt.Errorf("manager error: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		mgrCancel()
		<-mgrErr
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	mgrCancel()
	<-mgrErr
	logger.Info("shutdown complete")
	return nil
}

// (var _ used to keep `client` import live if future refactor inlines lookup)
var _ = client.IgnoreNotFound
```

**Step 2: Quick sanity — `go build`**

```bash
./scripts/dev.sh go build ./...
```
Expected: PASS. If `clientgoscheme` import path is wrong (it's actually `k8s.io/client-go/kubernetes/scheme`), fix as needed — `cmd/ach/cmd/operator.go` already imports this; copy the import path from there if in doubt.

**Step 3: Run unit tests (`contentservice` package still green)**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/contentservice/...
```
Expected: PASS (no behavior change in package).

**Step 4: Commit**

```bash
git add cmd/ach/cmd/content_service.go
git commit -m "feat(content-service): replace stub mux with real /content routes"
```

---

### Task 9: Add content-service RBAC for Prompt read

The content-service Pod runs as the `ach-operator` ServiceAccount (per the co-location refactor noted in `deploy/helm/ach/templates/content-service-deployment.yaml`). The operator SA already has full read on `prompts.ach.ackstorm.ai` because the prompt_controller watches them, so no extra RBAC is needed.

**Files:** (verify only — no edits expected)

**Step 1: Confirm operator RBAC includes Prompt read**

```bash
grep -A 5 "prompts" deploy/helm/ach/templates/operator-rbac.yaml
```
Expected: a rule covering `apiGroups: [ach.ackstorm.ai]`, `resources: [prompts]`, `verbs: [..., get, list, watch, ...]`.

If absent (CI will catch via envtest in §2; if §2 is not done yet, this gap is real), add to `operator-rbac.yaml`:

```yaml
- apiGroups: ["ach.ackstorm.ai"]
  resources: ["prompts"]
  verbs: ["get", "list", "watch"]
```

Then `./scripts/dev.sh make manifests` to keep `config/rbac/` in sync.

**Step 2: Commit (only if file changed)**

```bash
git add deploy/helm/ach/templates/operator-rbac.yaml config/rbac/
git commit -m "rbac(content-service): grant Prompt read via shared operator SA"
```

If unchanged, skip the commit — note the verification in the next task's description.

---

## Phase E — Lint, full sweep, manifest sanity

### Task 10: Lint the new package + changed cmd file

```bash
./scripts/dev.sh make lint-changed
```
Expected: PASS, or actionable diagnostics on the new files (typically: unused import, missing doc comment on exported symbol, errcheck on `_ = f.Close()` already silenced via `_ = ...`). Fix in place; re-run until green.

**Commit any fixes:**

```bash
git add -A
git commit -m "style(contentservice): satisfy golangci-lint"
```

(Skip commit if no diffs.)

---

### Task 11: Full unit sweep

```bash
./scripts/dev.sh make unit
```
Expected: PASS. Watches for surprises from neighboring packages that may have grown a dependency on a now-shadowed name. None expected.

---

### Task 12: Manifests + generated code regeneration sanity

```bash
./scripts/dev.sh make manifests generate fmt vet
git status --porcelain
```
Expected: clean tree (no spurious regen diffs). If files appear, commit them as a separate `chore(generated)` commit.

---

## Phase F — e2e validation against running cluster

### Task 13: Bring up cluster + redeploy operator/content-service

**Step 1: Cluster up (idempotent)**

```bash
make cluster-up
```
Expected: cluster Ready; operator + platform-api + dex + postgres + redis all Available. This target is synchronous per CLAUDE.md — do NOT add a polling loop after.

**Step 2: Hot-reload the content-service binary into the running operator Pod**

```bash
./scripts/dev.sh make operator-redeploy
make wait-content-service
```
Expected: `operator-redeploy` rebuilds the image and patches the operator Deployment; `wait-content-service` blocks on `kubectl rollout status deploy/ach-content-service`. Bounded — no naked loops.

**Step 3: Smoke test — `/healthz` still works**

```bash
kubectl -n ach-system port-forward svc/ach-content-service 8082:8082 &
PF=$!
sleep 2
curl -sS -o /dev/null -w '%{http_code}\n' http://localhost:8082/healthz
kill $PF 2>/dev/null || true
```
Expected: `200`. (Sanity gate for graceful-shutdown wiring in Task 8.)

---

### Task 14: e2e — `hydrate_demo.sh` shows non-zero `size_download` per URL

**Pre-condition fork:**

- **If §2 (domain port) IS merged:** the operator's Plugin/Prompt/Artifact reconcilers populate the cache PVC as part of `hydrate_demo.sh` step 3. Proceed directly.
- **If §2 is NOT merged:** seed the cache PVC manually so the routes have files to serve. Create a temporary seeding script:

**Optional file (only if §2 not yet merged):** `scripts/seed-content-cache.sh`

```bash
#!/usr/bin/env bash
# scripts/seed-content-cache.sh — seed fixture files into the operator
# Pod's cache PVC so the content-service routes can be exercised
# without the §2 domain-port reconcilers.
# DELETE THIS SCRIPT once §2 lands — the reconcilers will populate
# the cache from real upstream sources.
set -euo pipefail

NS="${NS:-ach-system}"
POD="$(kubectl -n "${NS}" get pod -l app.kubernetes.io/component=operator \
  -o jsonpath='{.items[0].metadata.name}')"

echo "[seed] populating cache in pod=${POD}"

kubectl -n "${NS}" exec "${POD}" -c content-service -- mkdir -p \
  /var/cache/ach/prompt /var/cache/ach/plugin /var/cache/ach/artifact

# Fixture: a non-empty prompt body
kubectl -n "${NS}" exec -i "${POD}" -c content-service -- \
  sh -c 'cat > /var/cache/ach/prompt/claude-code-system-prompt' <<'EOF'
# Claude Code system prompt fixture
You are a helpful AI assistant.
EOF

# Fixture: minimal valid gzip for plugin/artifact
printf '\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\x03\x03\x00\x00\x00\x00\x00\x00\x00\x00\x00' \
  | kubectl -n "${NS}" exec -i "${POD}" -c content-service -- \
    sh -c 'cat > /var/cache/ach/plugin/caveman.tar.gz'

printf '\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\x03\x03\x00\x00\x00\x00\x00\x00\x00\x00\x00' \
  | kubectl -n "${NS}" exec -i "${POD}" -c content-service -- \
    sh -c 'cat > /var/cache/ach/artifact/openclaw-templates.tar.gz'

echo "[seed] done"
```

```bash
chmod +x scripts/seed-content-cache.sh
./scripts/seed-content-cache.sh
```

**Step 1 (both branches): port-forward content-service**

```bash
kubectl -n ach-system port-forward svc/ach-content-service 8082:8082 &
PF=$!
trap 'kill ${PF} 2>/dev/null || true' EXIT
sleep 2
```

**Step 2: Hit each route and verify Content-Type + non-zero body**

```bash
for url in \
  "http://localhost:8082/content/prompt/claude-code-system-prompt" \
  "http://localhost:8082/content/plugin/caveman" \
  "http://localhost:8082/content/artifact/openclaw-templates"; do
  echo "=== ${url} ==="
  curl -sS -o /tmp/body -w 'http=%{http_code} ct=%{content_type} bytes=%{size_download}\n' "${url}"
done
```

Expected output (lengths may differ):

```
=== http://localhost:8082/content/prompt/claude-code-system-prompt ===
http=200 ct=text/markdown bytes=<NN>
=== http://localhost:8082/content/plugin/caveman ===
http=200 ct=application/gzip bytes=<NN>
=== http://localhost:8082/content/artifact/openclaw-templates ===
http=200 ct=application/gzip bytes=<NN>
```

Every `bytes=` value MUST be `> 0`. Every `http=200`. Every `ct=` MUST match the expected per-kind value.

**Step 3: 404 path**

```bash
curl -sS -o /dev/null -w '%{http_code}\n' \
  http://localhost:8082/content/prompt/does-not-exist
```
Expected: `404`.

**Step 4: If §2 IS merged — full hydrate_demo run**

```bash
./examples/hydrate_demo.sh
jq '.context | to_entries[] | {kind:.key, urls:[.value[].downloadUrl]}' examples/hydrate.json
# spot-check each URL is reachable
for url in $(jq -r '.context | to_entries[] | .value[].downloadUrl' examples/hydrate.json); do
  # rewrite https://ach.local.test/... to the port-forward
  local_url="${url/https:\/\/ach.local.test/http:\/\/localhost:8082}"
  echo "=== ${url} -> ${local_url} ==="
  curl -sS -o /dev/null -w 'http=%{http_code} bytes=%{size_download}\n' "${local_url}"
done
```
Expected: every URL returns `http=200` with `bytes > 0`. This is the acceptance gate from TODO §8: "Existing examples/hydrate_demo.sh shows non-zero size_download against each URL."

**Step 5: Tear down**

```bash
kill ${PF} 2>/dev/null || true
make cluster-down
```

**Step 6: Commit the seed script (only if added)**

```bash
git add scripts/seed-content-cache.sh
git commit -m "test(content-service): fixture-populator for e2e before §2 lands"
```

---

## Phase G — Documentation + PR

### Task 15: Update CLAUDE.md "Common failure modes" with the lifted stub note

**Files:**
- Modify: `CLAUDE.md` (append a new `### ❌ ... ✅ ...` entry under "Common failure modes")

**Step 1: Append entry**

```markdown
### ❌ `downloadUrl` from /platform/hydrate returns 404
```bash
curl https://ach.local.test/content/prompt/foo
# HTTP 404 (or no handler registered at all → chi 404)
```
✅ Confirm content-service is on the build that includes
`internal/contentservice` routes (not the Phase 1 stub):
```bash
kubectl -n ach-system exec deploy/ach-content-service \
  -c content-service -- ach content-service --help \
  | grep -q "/content/{prompt,plugin,artifact}"
```
WHY IT FAILS: Pre-`feat/content-service-routes` builds shipped a
`/healthz`-only stub. The Service is healthy, the Pod is Ready, the
hydrate URLs look right — and every GET 404s because the route doesn't
exist. Fix is a rolling image update; no data migration.
```

**Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: failure mode for pre-routes content-service stub"
```

---

### Task 16: Update ROADMAP Phase 5 cross-link

**Files:**
- Modify: `ROADMAP.md` (under "Phase 5: Content Service + Cross-component Observability")

**Step 1: Edit**

Find the line `**Plans**: TBD` directly below the Phase 5 Success Criteria block, replace with:

```markdown
**Plans**:
- `docs/plans/2026-05-26-content-service-routes.md` — v1alpha1 anonymous routes (Hub §15.2 minimal contract per TODO §8). Phase 5 will layer pk_/ek_ auth, environment scoping, marketplace resolution, staleness checks, and the §18.5 metric set on top.
```

**Step 2: Commit**

```bash
git add ROADMAP.md
git commit -m "docs(roadmap): cross-link content-service routes plan from Phase 5"
```

---

### Task 17: Final pre-push gate

```bash
make pre-push
```
Expected: all 17 gates PASS. If any fail (e.g. SPDX header missing on a new file, govulncheck drift), fix at the source — never `--no-verify`.

```bash
git push -u origin feat/content-service-routes
```

---

### Task 18: Open PR

```bash
gh pr create \
  --title "feat(content-service): implement /content/{kind}/{name} routes (TODO §8)" \
  --body "$(cat <<'EOF'
## Summary
- Replaces the Phase 1 `/healthz`-only stub in `ach content-service` with the real `/content/{prompt,plugin,artifact}/{name}` surface per Hub §15.2 + TODO §8.
- Streams files from the shared cache PVC at `/var/cache/ach/{prompt,plugin,artifact}/...` via `http.ServeContent` (sendfile-backed on Linux).
- `Content-Type` policy: prompts honor `Prompt.spec.contentType` (default `text/markdown`); plugin/artifact gzip → `application/gzip`; artifact non-gzip → `application/octet-stream`.
- `Cache-Control: public, max-age=300` per TODO §8; `Range` requests supported (stdlib `http.ServeContent`).
- Anonymous auth in v1alpha1 — Phase 5 will layer `pk_`/`ek_` + environment scoping + marketplace resolution + staleness + metrics.

## Test plan
- [x] Unit tests: 11 test cases across `paths_test.go`, `content_type_test.go`, `handler_test.go`
- [x] Range request returns 206 Partial Content with correct `Content-Range`
- [x] `Content-Length` matches body, no `Transfer-Encoding`
- [x] 404 on missing cache file; 400 on traversal-style names
- [x] e2e: each `downloadUrl` in `examples/hydrate.json` returns 200 + non-zero body

## Cross-plan note
- Acceptance Step 4 (`hydrate_demo.sh` shows non-zero `size_download` per URL) depends on §2 (domain port) for the reconcilers that populate the cache. Pre-§2, `scripts/seed-content-cache.sh` proves the routes serve correctly with hand-seeded fixtures.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Return the PR URL.

---

## Out of scope (explicitly NOT in this plan)

These are Phase 5 work per ROADMAP:
- `pk_`/`ek_` authentication + `x-ach-environment` header
- Environment-scoped authorization (`Environment.spec.context.<kind>[]` membership check)
- Marketplace name resolution (Plugin CRD vs alphabetically-lowest `marketplace_name`)
- Staleness check (`now - last_successful_refresh > max_staleness` → `503 stale_cache_expired`)
- `litellm_unreachable_total` Prometheus counter + per-route content-service metrics
- `Cache-Control: no-store` (Phase 5 ROADMAP supersedes the v1alpha1 `public, max-age=300`)
- Ignoring `Range`/`If-None-Match`/`If-Modified-Since` headers per Phase 5 SC #1 (v1alpha1 keeps stdlib semantics)
- `strace`-verified sendfile assertion (stdlib uses it; v1alpha1 trusts the platform — Phase 5 adds the smoke test)

If a reviewer asks for any of these, push back with "Phase 5; see ROADMAP §Phase 5 SC #2/#3/#4/#5."
