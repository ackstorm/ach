// SPDX-License-Identifier: Apache-2.0

package pluginpack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

// buildTarGz produces an in-memory tar.gz with the given path→content
// map. Copied from internal/controller/ach/marketplace_manifest_test.go
// (buildTarGz, lines 18-43) — the pluginpack package keeps an
// independent copy to stay hermetic (no cyclic import via the ach
// package).
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// Walk in sorted order so the produced tar is deterministic for
	// any tests that care about entry order.
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("Write %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close: %v", err)
	}
	return buf.Bytes()
}

// buildTarGzWithExtras allows adding non-TypeReg entries (symlinks,
// dirs, devices) alongside regular files. Used for the symlink/device-
// drop test.
type tarEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
}

func buildTarGzMixed(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o644,
			Typeflag: e.typeflag,
		}
		if e.typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if e.typeflag == tar.TypeDir {
			hdr.Mode = 0o755
		}
		if e.typeflag == tar.TypeSymlink || e.typeflag == tar.TypeLink {
			hdr.Linkname = e.linkname
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader %q: %v", e.name, err)
		}
		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("Write %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close: %v", err)
	}
	return buf.Bytes()
}

// readTarGzNames returns the (name, typeflag) pairs of a tar.gz.
type outEntry struct {
	name     string
	typeflag byte
	body     []byte
}

func readTarGz(t *testing.T, in []byte) []outEntry {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var out []outEntry
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, _ := io.ReadAll(tr)
		out = append(out, outEntry{
			name:     hdr.Name,
			typeflag: hdr.Typeflag,
			body:     body,
		})
	}
	return out
}

func nameSet(es []outEntry) map[string]struct{} {
	out := map[string]struct{}{}
	for _, e := range es {
		out[e.name] = struct{}{}
	}
	return out
}

// cavemanManifestJSON is the live caveman plugin.json (2026-05-28),
// trimmed to the runtime-relevant fields.
const cavemanManifestJSON = `{
  "name": "caveman",
  "description": "Ultra-compressed communication mode.",
  "author": {"name": "Julius Brussee", "url": "https://example.test"},
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "node \"${CLAUDE_PLUGIN_ROOT}/src/hooks/caveman-activate.js\"", "timeout": 5}]}
    ],
    "UserPromptSubmit": [
      {"hooks": [{"type": "command", "command": "node \"${CLAUDE_PLUGIN_ROOT}/src/hooks/caveman-mode-tracker.js\"", "timeout": 5}]}
    ]
  }
}`

func TestFilter_CavemanShape_HappyPath(t *testing.T) {
	files := map[string]string{
		".claude-plugin/plugin.json":        cavemanManifestJSON,
		"src/hooks/caveman-activate.js":     "// runtime entry-point",
		"src/hooks/caveman-mode-tracker.js": "// runtime peer",
		"agents/agent.md":                   "agent body",
		"commands/cmd.md":                   "command body",
		"skills/skill.md":                   "skill body",
		"LICENSE":                           "Apache-2.0",
		"README.md":                         "# caveman",
		// Multi-runtime noise — must be DROPPED.
		".codex/config.toml":             "noise",
		".junie/junie.yaml":              "noise",
		"AGENTS.md":                      "noise",
		"GEMINI.md":                      "noise",
		"gemini-extension.json":          "noise",
		"tests/test_runtime.py":          "noise",
		"plugins/caveman/plugin.json":    "nested mirror noise",
		".gitkeep":                       "",
		"src/plugins/opencode/index.lua": "noise",
	}
	in := buildTarGz(t, files)
	out, err := Filter(in)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	entries := readTarGz(t, out)
	got := nameSet(entries)

	// Expected runtime-relevant files.
	want := []string{
		".claude-plugin/plugin.json",
		"src/hooks/caveman-activate.js",
		"src/hooks/caveman-mode-tracker.js",
		"agents/agent.md",
		"commands/cmd.md",
		"skills/skill.md",
		"LICENSE",
		"README.md",
	}
	for _, n := range want {
		if _, ok := got[n]; !ok {
			t.Errorf("expected %q in filtered tar; missing", n)
		}
	}

	// Drop list — these MUST NOT appear.
	drop := []string{
		".codex/config.toml",
		".junie/junie.yaml",
		"AGENTS.md",
		"GEMINI.md",
		"gemini-extension.json",
		"tests/test_runtime.py",
		"plugins/caveman/plugin.json",
		".gitkeep",
		"src/plugins/opencode/index.lua",
	}
	for _, n := range drop {
		if _, ok := got[n]; ok {
			t.Errorf("unexpected %q in filtered tar (should be dropped)", n)
		}
	}
}

func TestFilter_MissingManifest_StrictFail(t *testing.T) {
	in := buildTarGz(t, map[string]string{
		"README.md":     "# no manifest",
		"commands/x.md": "...",
	})
	_, err := Filter(in)
	if err == nil {
		t.Fatal("expected error on missing manifest")
	}
	if !errors.Is(err, ErrManifestMissing) {
		t.Errorf("err = %v; want wrap of ErrManifestMissing", err)
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of sources.ErrUpstreamInvalid (double %%w)", err)
	}
}

func TestFilter_TraversalInManifestRef_Rejected(t *testing.T) {
	manifest := `{"name":"x","hooks":{"S":[{"hooks":[{"command":"node \"${CLAUDE_PLUGIN_ROOT}/../../../etc/passwd\""}]}]}}`
	in := buildTarGz(t, map[string]string{
		".claude-plugin/plugin.json": manifest,
		"README.md":                  "# x",
	})
	_, err := Filter(in)
	if err == nil {
		t.Fatal("expected error on traversal reference")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of sources.ErrUpstreamInvalid", err)
	}
	// Traversal-rejection is a distinct channel from missing-manifest.
	// Both wrap ErrUpstreamInvalid, but only manifest-absent uses the
	// typed ErrManifestMissing sentinel.
	if errors.Is(err, ErrManifestMissing) {
		t.Errorf("err = %v; should NOT wrap ErrManifestMissing (traversal is a distinct path)", err)
	}
}

func TestFilter_SymlinkAndDeviceEntries_Dropped(t *testing.T) {
	in := buildTarGzMixed(t, []tarEntry{
		{name: ".claude-plugin/plugin.json", body: `{"name":"x"}`, typeflag: tar.TypeReg},
		{name: "README.md", body: "# x", typeflag: tar.TypeReg},
		// Symlink with a name that WOULD otherwise pass the whitelist.
		{name: "commands/evil-symlink", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
		// Character device with a name that WOULD otherwise pass.
		{name: "agents/evil-char", typeflag: tar.TypeChar},
		// FIFO.
		{name: "skills/evil-fifo", typeflag: tar.TypeFifo},
		// Regular file that is whitelisted — sanity that other entries
		// still get through.
		{name: "commands/legit.md", body: "ok", typeflag: tar.TypeReg},
	})
	out, err := Filter(in)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	got := nameSet(readTarGz(t, out))
	for _, drop := range []string{"commands/evil-symlink", "agents/evil-char", "skills/evil-fifo"} {
		if _, ok := got[drop]; ok {
			t.Errorf("%q must be dropped (non-regular tar entry)", drop)
		}
	}
	if _, ok := got["commands/legit.md"]; !ok {
		t.Errorf("commands/legit.md should be present")
	}
}

func TestFilter_ExplicitDirEntries_Emitted(t *testing.T) {
	files := map[string]string{
		".claude-plugin/plugin.json":    cavemanManifestJSON,
		"src/hooks/caveman-activate.js": "// runtime",
		"agents/agent.md":               "ag",
		"commands/cmd.md":               "cm",
		"skills/skill.md":               "sk",
	}
	in := buildTarGz(t, files)
	out, err := Filter(in)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	entries := readTarGz(t, out)
	wantDirs := []string{
		".claude-plugin/",
		"src/",
		"src/hooks/",
		"agents/",
		"commands/",
		"skills/",
	}
	dirs := map[string]bool{}
	for _, e := range entries {
		if e.typeflag == tar.TypeDir {
			dirs[e.name] = true
		}
	}
	for _, d := range wantDirs {
		if !dirs[d] {
			t.Errorf("expected explicit TypeDir entry for %q; missing", d)
		}
	}
}

func TestFilter_JSONNullAndNumeric_NoCrash(t *testing.T) {
	manifest := `{
  "name": "x",
  "timeout": 5,
  "hook": null,
  "active": true,
  "hooks": {
    "S": [
      {"timeout": 5, "command": "node \"${CLAUDE_PLUGIN_ROOT}/src/hooks/x.js\""}
    ]
  }
}`
	in := buildTarGz(t, map[string]string{
		".claude-plugin/plugin.json": manifest,
		"src/hooks/x.js":             "// runtime",
		"src/hooks/peer.js":          "// peer",
		"unrelated.txt":              "drop",
	})
	out, err := Filter(in)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	got := nameSet(readTarGz(t, out))
	if _, ok := got["src/hooks/x.js"]; !ok {
		t.Errorf("src/hooks/x.js missing")
	}
	if _, ok := got["src/hooks/peer.js"]; !ok {
		t.Errorf("src/hooks/peer.js (sibling under manifest-referenced parent) missing")
	}
	if _, ok := got["unrelated.txt"]; ok {
		t.Errorf("unrelated.txt should be dropped")
	}
}

func TestFilter_WhitelistEdges(t *testing.T) {
	files := map[string]string{
		".claude-plugin/plugin.json":  `{"name":"x"}`,
		"LICENSE.txt":                 "Apache",
		"nested/LICENSE":              "should drop — not at root",
		".gitkeep":                    "should drop",
		"plugins/caveman/plugin.json": "nested mirror — should drop",
		"README.md":                   "ok",
		"agents/a.md":                 "ok",
	}
	in := buildTarGz(t, files)
	out, err := Filter(in)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	got := nameSet(readTarGz(t, out))
	if _, ok := got["LICENSE.txt"]; !ok {
		t.Errorf("LICENSE.txt at root must be INCLUDED")
	}
	if _, ok := got["README.md"]; !ok {
		t.Errorf("README.md must be INCLUDED")
	}
	if _, ok := got["agents/a.md"]; !ok {
		t.Errorf("agents/a.md (root convention dir) must be INCLUDED")
	}
	if _, ok := got["nested/LICENSE"]; ok {
		t.Errorf("nested/LICENSE must be DROPPED (root-only whitelist)")
	}
	if _, ok := got[".gitkeep"]; ok {
		t.Errorf(".gitkeep must be DROPPED")
	}
	if _, ok := got["plugins/caveman/plugin.json"]; ok {
		t.Errorf("plugins/caveman/plugin.json (nested mirror) must be DROPPED")
	}
}

// Ensure leading "./"-prefixed entries are normalized correctly (some
// tar producers emit this).
func TestFilter_LeadingDotSlashNormalized(t *testing.T) {
	manifestRaw := `{"name":"x"}`
	in := buildTarGzMixed(t, []tarEntry{
		{name: "./.claude-plugin/plugin.json", body: manifestRaw, typeflag: tar.TypeReg},
		{name: "./README.md", body: "# x", typeflag: tar.TypeReg},
	})
	out, err := Filter(in)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	got := nameSet(readTarGz(t, out))
	for _, want := range []string{".claude-plugin/plugin.json", "README.md"} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected %q after ./-normalization; got %v", want, got)
		}
	}
}

// Smoke test: malformed gzip surfaces as wrapped ErrUpstreamInvalid.
func TestFilter_CorruptGzip(t *testing.T) {
	_, err := Filter([]byte("not gzip at all"))
	if err == nil {
		t.Fatal("expected error on corrupt gzip")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of sources.ErrUpstreamInvalid", err)
	}
}

// Sanity: the helper produces something we can re-parse — defends
// against a regression where outBuf has a malformed header.
func TestFilter_OutputIsValidTarGz(t *testing.T) {
	in := buildTarGz(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"x"}`,
		"README.md":                  "hi",
	})
	out, err := Filter(in)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if !strings.HasPrefix(string(out[:3]), "\x1f\x8b\x08") {
		t.Errorf("output not gzipped; first bytes = %x", out[:3])
	}
	// Re-reading must succeed.
	_ = readTarGz(t, out)
}

// TestFilter_EntryCount_BoundaryAndOver asserts the off-by-one fix:
// a tarball with EXACTLY pluginTarballMaxEntries entries is accepted,
// the (max+1)-th is rejected. Uses a test-only override of the var so
// the boundary is testable without allocating 50000 tar headers per
// run.
func TestFilter_EntryCount_BoundaryAndOver(t *testing.T) {
	// Override the package-level limit for this test only; restore in
	// a t.Cleanup so parallel runs aren't disrupted (no t.Parallel
	// here).
	originalMax := pluginTarballMaxEntries
	pluginTarballMaxEntries = 5
	t.Cleanup(func() { pluginTarballMaxEntries = originalMax })

	buildTarballWithDirs := func(extraDirs int) []byte {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		manifest := `{"name":"x"}`
		_ = tw.WriteHeader(&tar.Header{
			Name: ".claude-plugin/plugin.json", Mode: 0o644,
			Size: int64(len(manifest)), Typeflag: tar.TypeReg,
		})
		_, _ = tw.Write([]byte(manifest))
		for i := 0; i < extraDirs; i++ {
			_ = tw.WriteHeader(&tar.Header{
				Name:     "skills/d" + string(rune('0'+i)) + "/",
				Typeflag: tar.TypeDir,
			})
		}
		_ = tw.Close()
		_ = gz.Close()
		return buf.Bytes()
	}

	// At-boundary: 1 manifest + 4 dirs = 5 = pluginTarballMaxEntries.
	if _, err := Filter(buildTarballWithDirs(pluginTarballMaxEntries - 1)); err != nil {
		t.Fatalf("Filter rejected exactly pluginTarballMaxEntries=%d entries (expected accept): %v",
			pluginTarballMaxEntries, err)
	}

	// Over-boundary: 1 manifest + 5 dirs = 6 = pluginTarballMaxEntries+1.
	_, err := Filter(buildTarballWithDirs(pluginTarballMaxEntries))
	if err == nil {
		t.Fatalf("Filter accepted pluginTarballMaxEntries+1 entries (expected reject)")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("expected wrapped sources.ErrUpstreamInvalid; got %v", err)
	}
}

// TestFilter_PerEntrySizeCap asserts the per-entry header-size guard
// rejects a tarball whose non-manifest entry advertises more than
// tarEntryMaxBytes. Defends against archive bombs that claim a huge
// Size in the tar header.
func TestFilter_PerEntrySizeCap(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	manifest := `{"name":"x"}`
	if err := tw.WriteHeader(&tar.Header{
		Name: ".claude-plugin/plugin.json", Mode: 0o644,
		Size: int64(len(manifest)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("WriteHeader manifest: %v", err)
	}
	if _, err := tw.Write([]byte(manifest)); err != nil {
		t.Fatalf("Write manifest: %v", err)
	}
	// A whitelisted entry (root README.md) but with a pathological
	// declared size. tar.Writer will refuse to write a body smaller
	// than Size, so produce a header-only entry by closing the writer
	// after WriteHeader without writing the body — the Filter must
	// reject on the declared header size before trying to read.
	if err := tw.WriteHeader(&tar.Header{
		Name:     "README.md",
		Mode:     0o644,
		Size:     tarEntryMaxBytes + 1,
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("WriteHeader README: %v", err)
	}
	// Write zero-length padding the tar.Writer requires for the
	// declared size, but we don't actually want gigabytes; close the
	// writer with an underfilled entry, which is what triggers the
	// reader-side header check first (the size cap fires before any
	// body read).
	_ = tw.Close()
	_ = gz.Close()
	_, err := Filter(buf.Bytes())
	if err == nil {
		t.Fatalf("Filter accepted entry advertising %d bytes (expected reject at cap %d)",
			tarEntryMaxBytes+1, tarEntryMaxBytes)
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("expected wrapped sources.ErrUpstreamInvalid; got %v", err)
	}
}
