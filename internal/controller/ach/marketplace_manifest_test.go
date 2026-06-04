// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

// buildTarGz produces an in-memory tar.gz with the given path→content map.
// Used to synthesize the staged tarball verifyPluginContents walks.
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(body)),
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

func TestVerifyPluginContents_PresentAtRoot(t *testing.T) {
	// Real-world layout: git.tarSubtree always strips the subtree
	// prefix, so the manifest, when present, is always at the tar root
	// — regardless of whether the entry was git-subdir/url with a
	// `path:` field or a whole-repo github/url with no path.
	tgz := buildTarGz(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"x"}`,
		"README.md":                  "hi",
	})
	if err := verifyPluginContents(bytes.NewReader(tgz)); err != nil {
		t.Errorf("verify: %v; want nil", err)
	}
}

func TestVerifyPluginContents_LeadingDotSlashTolerated(t *testing.T) {
	// Some tar writers prefix entries with "./". The verifier must
	// normalize that prefix before comparing.
	tgz := buildTarGz(t, map[string]string{
		"./.claude-plugin/plugin.json": `{}`,
	})
	if err := verifyPluginContents(bytes.NewReader(tgz)); err != nil {
		t.Errorf("verify: %v; want nil with leading ./", err)
	}
}

func TestVerifyPluginContents_ConventionOnlyAccepted(t *testing.T) {
	// plugin-dev shape: no manifest, just convention dirs. Claude Code
	// auto-discovers these; ACH must accept it (real anthropics/claude-code
	// plugin-dev at rev b67fa4f). Regression guard for the marketplace gate.
	tgz := buildTarGz(t, map[string]string{
		"README.md":           "docs",
		"agents/foo.md":       "# foo agent",
		"commands/bar.md":     "# bar command",
		"skills/baz/SKILL.md": "# baz skill",
	})
	if err := verifyPluginContents(bytes.NewReader(tgz)); err != nil {
		t.Errorf("verify: %v; want nil for convention-only plugin", err)
	}
}

func TestVerifyPluginContents_RootSkillAccepted(t *testing.T) {
	tgz := buildTarGz(t, map[string]string{"SKILL.md": "# single-skill plugin"})
	if err := verifyPluginContents(bytes.NewReader(tgz)); err != nil {
		t.Errorf("verify: %v; want nil for root SKILL.md plugin", err)
	}
}

func TestVerifyPluginContents_McpConfigOnlyAccepted(t *testing.T) {
	tgz := buildTarGz(t, map[string]string{".mcp.json": `{"mcpServers":{}}`})
	if err := verifyPluginContents(bytes.NewReader(tgz)); err != nil {
		t.Errorf("verify: %v; want nil for .mcp.json-only plugin", err)
	}
}

func TestVerifyPluginContents_NoComponentsRejected(t *testing.T) {
	tgz := buildTarGz(t, map[string]string{
		"README.md": "no manifest here",
	})
	err := verifyPluginContents(bytes.NewReader(tgz))
	if err == nil {
		t.Fatal("expected error on missing manifest")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of sources.ErrUpstreamInvalid", err)
	}
	if !strings.Contains(err.Error(), "recognized component") {
		t.Errorf("err message should mention recognized component; got %q", err.Error())
	}
}

func TestVerifyPluginContents_BuriedInSubdirRejected(t *testing.T) {
	// Manifest must be at the tar root — a tar that buries it under a
	// subdirectory (which would happen if git.tarSubtree ever stopped
	// stripping the subtree, or if the upstream repo nested the
	// manifest one level too deep) MUST be rejected. The first path
	// segment "plugins" is not a recognized convention dir, so this
	// pins the contract: only the tar-root location counts.
	tgz := buildTarGz(t, map[string]string{
		"plugins/x/.claude-plugin/plugin.json": `{"name":"x"}`,
	})
	err := verifyPluginContents(bytes.NewReader(tgz))
	if err == nil {
		t.Fatal("expected error: manifest must be at tar root")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of ErrUpstreamInvalid", err)
	}
}

func TestVerifyPluginContents_CorruptGzip(t *testing.T) {
	// Non-gzip input must surface as wrapped ErrUpstreamInvalid (the
	// gzip.NewReader failure branch).
	err := verifyPluginContents(bytes.NewReader([]byte("not gzip")))
	if err == nil {
		t.Fatal("expected error on corrupt gzip")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of ErrUpstreamInvalid", err)
	}
}
