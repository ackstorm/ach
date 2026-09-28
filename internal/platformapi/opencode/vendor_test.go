// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestPlugin_MatchesUpstreamTag: plugin/ is a pinned, unmodified copy of
// alitellm-auth clients/opencode at the tag in UPSTREAM (upstream owns the
// plugin and its tests). Changes go upstream, then re-vendor; never patch
// the copy. Skips without a sibling ../alitellm-auth checkout or with the
// tag not fetched there (scripts/dev.sh mounts it read-only).
func TestPlugin_MatchesUpstreamTag(t *testing.T) {
	repo := "../../../../alitellm-auth"
	if _, err := os.Stat(repo); err != nil {
		t.Skipf("no sibling alitellm-auth checkout (%v)", err)
	}
	raw, err := os.ReadFile("UPSTREAM")
	if err != nil {
		t.Fatal(err)
	}
	tag := strings.TrimSpace(string(raw))
	out, err := exec.Command("git", "-C", repo, "ls-tree", "-r", "--name-only", tag, "clients/opencode").Output()
	if err != nil {
		t.Skipf("tag %s not available in %s (%v)", tag, repo, err)
	}
	upstream := strings.Fields(string(out))
	var mine []string
	_ = fs.WalkDir(plugin, "plugin", func(p string, d fs.DirEntry, _ error) error {
		if !d.IsDir() {
			mine = append(mine, p)
		}
		return nil
	})
	if len(mine) != len(upstream) {
		t.Fatalf("vendored %v, upstream %s has %v — re-vendor, never patch", mine, tag, upstream)
	}
	for _, up := range upstream {
		want, err := exec.Command("git", "-C", repo, "show", tag+":"+up).Output()
		if err != nil {
			t.Fatalf("git show %s:%s: %v", tag, up, err)
		}
		got, err := plugin.ReadFile("plugin/" + strings.TrimPrefix(up, "clients/opencode/"))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s drifted from alitellm-auth %s (err %v) — re-vendor, never patch", up, tag, err)
		}
	}
}
