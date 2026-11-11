// SPDX-License-Identifier: Apache-2.0

package gitprovider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitsrc "github.com/ackstorm/ach/internal/gitfetch"
)

func TestSchemeForProvider(t *testing.T) {
	cases := map[string]gitsrc.AuthScheme{
		"gitlab":    gitsrc.AuthBasicOAuth2,
		"github":    gitsrc.AuthBearer,
		"bitbucket": gitsrc.AuthBearer,
		"":          gitsrc.AuthBearer,
	}
	for provider, want := range cases {
		if got := schemeForProvider(provider); got != want {
			t.Errorf("schemeForProvider(%q) = %v; want %v", provider, got, want)
		}
	}
}

// twoCommitRepo builds a local bare repo whose main carries two commits.
func twoCommitRepo(t *testing.T) (bare, first, second string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH; skipping")
	}
	work, bare := t.TempDir(), filepath.Join(t.TempDir(), "r.git")
	run := func(dir string, args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(work, "init", "-q", "-b", "main")
	run(work, "config", "user.email", "t@t")
	run(work, "config", "user.name", "t")
	commit := func(content string) string {
		if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		run(work, "add", "f.txt")
		run(work, "commit", "-q", "-m", content)
		return run(work, "rev-parse", "HEAD")
	}
	first, second = commit("one"), commit("two")
	run(work, "clone", "-q", "--bare", work, bare)
	run(bare, "config", "uploadpack.allowReachableSHA1InWant", "true")
	return bare, first, second
}

func TestFetchViaProvider_Pin(t *testing.T) {
	bare, first, second := twoCommitRepo(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name, commit, prior string
		wantRev             string
		wantNotModified     bool
	}{
		{"unpinned serves the head", "", "", second, false},
		{"pinned serves the commit", first, "", first, false},
		{"pinned and already served", first, first, first, true},
		{"unpinned and head already served", "", second, second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := FetchViaProvider(ctx, "github", bare, "main", tc.commit, "", tc.prior, "")
			if err != nil {
				t.Fatal(err)
			}
			if res.Body != nil {
				defer res.Body.Close()
			}
			if res.UpstreamRev != tc.wantRev || res.LatestUpstreamRev != second || res.NotModified != tc.wantNotModified {
				t.Fatalf("rev=%s latest=%s notModified=%v", res.UpstreamRev, res.LatestUpstreamRev, res.NotModified)
			}
		})
	}
}
