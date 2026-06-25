// SPDX-License-Identifier: Apache-2.0

package discover_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"sort"
	"testing"

	"github.com/ackstorm/ach/internal/cli/localpkg/discover"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
	"github.com/ackstorm/ach/internal/featuregate"
)

// mkTarGz builds a gzipped tar from a map[path]content.
func mkTarGz(files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Size:     int64(len(body)),
			Mode:     0o644,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			panic(err)
		}
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	if err := gz.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// skillFM returns a minimal valid SKILL.md frontmatter for the given name.
// name must be a valid agentskills.io name (lowercase alnum + hyphens).
func skillFM(name string) string {
	return "---\nname: " + name + "\ndescription: a valid description for " + name + "\n---\nbody\n"
}

// containsCap reports whether caps contains c (order-insensitive).
func containsCap(caps []store.Capability, c store.Capability) bool {
	for _, x := range caps {
		if x == c {
			return true
		}
	}
	return false
}

// sortCaps returns a sorted copy of caps for deterministic comparison.
func sortCaps(caps []store.Capability) []store.Capability {
	out := make([]store.Capability, len(caps))
	copy(out, caps)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lens != out[j].Lens {
			return out[i].Lens < out[j].Lens
		}
		return out[i].Count < out[j].Count
	})
	return out
}

// (a) plugin-marketplace: tar contains .claude-plugin/marketplace.json with 2 plugins.
func TestDetect_PluginMarketplace(t *testing.T) {
	if !featuregate.PluginsEnabled {
		t.Skip("plugins disabled via featuregate.PluginsEnabled")
	}
	marketplaceJSON := `{"name":"m","owner":{"name":"o"},"plugins":[{"name":"p1","source":{"source":"github","repo":"a/b"}},{"name":"p2","source":{"source":"github","repo":"c/d"}}]}`
	tarball := mkTarGz(map[string]string{
		".claude-plugin/marketplace.json": marketplaceJSON,
	})

	caps, _, err := discover.Detect(tarball, "")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	want := store.Capability{Lens: discover.LensPluginMarketplace, Count: 2}
	if !containsCap(caps, want) {
		t.Errorf("expected caps to contain %+v, got %v", want, sortCaps(caps))
	}
}

// (b) skill-marketplace: tar with skills/foo/SKILL.md and skills/bar/SKILL.md.
func TestDetect_SkillMarketplace(t *testing.T) {
	tarball := mkTarGz(map[string]string{
		"skills/foo/SKILL.md": skillFM("foo"),
		"skills/bar/SKILL.md": skillFM("bar"),
	})

	caps, _, err := discover.Detect(tarball, "")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	want := store.Capability{Lens: discover.LensSkillMarketplace, Count: 2}
	if !containsCap(caps, want) {
		t.Errorf("expected caps to contain %+v, got %v", want, sortCaps(caps))
	}
}

// (c) plugin (direct): tar with commands/x.md (no marketplace.json).
func TestDetect_Plugin(t *testing.T) {
	if !featuregate.PluginsEnabled {
		t.Skip("plugins disabled via featuregate.PluginsEnabled")
	}
	tarball := mkTarGz(map[string]string{
		"commands/x.md": "# x",
	})

	caps, _, err := discover.Detect(tarball, "")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	want := store.Capability{Lens: discover.LensPlugin, Count: 1}
	if !containsCap(caps, want) {
		t.Errorf("expected caps to contain %+v, got %v", want, sortCaps(caps))
	}
}

// (d) skill (direct): bare SKILL.md at root.
// VerifySkillContents accepts SKILL.md at root.
// VerifyPluginContents also accepts SKILL.md (it's in recognizedRootFiles).
// Since no marketplace.json is present, plugin lens is also enabled.
// We assert it contains {skill,1}; we do not over-constrain on plugin presence.
func TestDetect_Skill(t *testing.T) {
	tarball := mkTarGz(map[string]string{
		"SKILL.md": skillFM("solo"),
	})

	caps, _, err := discover.Detect(tarball, "")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	want := store.Capability{Lens: discover.LensSkill, Count: 1}
	if !containsCap(caps, want) {
		t.Errorf("expected caps to contain %+v, got %v", want, sortCaps(caps))
	}
}

// (e) no match: tar with only a readme.
func TestDetect_NoMatch(t *testing.T) {
	tarball := mkTarGz(map[string]string{
		"readme.txt": "hi",
	})

	caps, _, err := discover.Detect(tarball, "")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if len(caps) != 0 {
		t.Errorf("expected empty caps, got %v", sortCaps(caps))
	}
}

// TestDetect_SkillMarketplaceWithHint verifies the skillsRootHint path.
// The tarball must have entries at multiple top-level dirs so detectArchiveRoot
// returns "" (no single archive-root wrapper), allowing relUnderSubPath to find
// the hint prefix correctly.
func TestDetect_SkillMarketplaceWithHint(t *testing.T) {
	tarball := mkTarGz(map[string]string{
		"custom/alpha/SKILL.md": skillFM("alpha"),
		// A second top-level dir breaks the single-root assumption so
		// detectArchiveRoot returns "" and the subPath hint is applied properly.
		"README.md": "repo readme",
	})

	caps, skillsRoot, err := discover.Detect(tarball, "custom")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	want := store.Capability{Lens: discover.LensSkillMarketplace, Count: 1}
	if !containsCap(caps, want) {
		t.Errorf("expected caps to contain %+v, got %v", want, sortCaps(caps))
	}
	// An explicit hint flows through and is returned verbatim.
	if skillsRoot != "custom" {
		t.Errorf("skillsRoot = %q, want %q", skillsRoot, "custom")
	}
}

// TestDetect_SkillMarketplaceAutodetectRoot verifies that a skills/<d>/SKILL.md
// layout discovered with no hint reports skillsRoot == "skills" so the caller
// can persist the autodetected root (Bug B regression guard).
func TestDetect_SkillMarketplaceAutodetectRoot(t *testing.T) {
	tarball := mkTarGz(map[string]string{
		"skills/pdf/SKILL.md": skillFM("pdf"),
		"README.md":           "repo readme",
	})

	caps, skillsRoot, err := discover.Detect(tarball, "")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	want := store.Capability{Lens: discover.LensSkillMarketplace, Count: 1}
	if !containsCap(caps, want) {
		t.Errorf("expected caps to contain %+v, got %v", want, sortCaps(caps))
	}
	if skillsRoot != "skills" {
		t.Errorf("skillsRoot = %q, want %q", skillsRoot, "skills")
	}
}

// TestDetect_MarketplaceAtRoot verifies marketplace.json at root (not under .claude-plugin/).
func TestDetect_MarketplaceAtRoot(t *testing.T) {
	if !featuregate.PluginsEnabled {
		t.Skip("plugins disabled via featuregate.PluginsEnabled")
	}
	marketplaceJSON := `{"name":"m","owner":{"name":"o"},"plugins":[{"name":"p1","source":{"source":"github","repo":"a/b"}}]}`
	tarball := mkTarGz(map[string]string{
		"marketplace.json": marketplaceJSON,
	})

	caps, _, err := discover.Detect(tarball, "")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	want := store.Capability{Lens: discover.LensPluginMarketplace, Count: 1}
	if !containsCap(caps, want) {
		t.Errorf("expected caps to contain %+v, got %v", want, sortCaps(caps))
	}
}

// TestDetect_NonNilEmpty verifies Detect returns non-nil empty slice (not nil).
func TestDetect_NonNilEmpty(t *testing.T) {
	tarball := mkTarGz(map[string]string{
		"readme.txt": "nothing useful here",
	})
	caps, _, err := discover.Detect(tarball, "")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if caps == nil {
		t.Error("expected non-nil slice, got nil")
	}
}
