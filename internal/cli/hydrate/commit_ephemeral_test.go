// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// baseCapturingExtractor records, per ContentRef.Name, the extraction base the
// orchestrator passed to ExtractContent — so a test can prove plugins are
// routed to the ephemeral pluginStageRoot while prompts/artifacts keep the
// <achDir>/<kind> destination. The extract loop is sequential, so no locking.
type baseCapturingExtractor struct {
	bases map[string]string
}

func (e *baseCapturingExtractor) ExtractContent(_ context.Context, ref manifest.ContentRef, base string, _ *state.File) (ExtractResult, error) {
	e.bases[ref.Name] = base
	return ExtractResult{}, nil
}

// mustMkOrphanPlugin creates <achDir>/plugin/<name>/agents/x.md so a test can
// assert the legacy persistent plugin cache is (or is not) dropped on run.
func mustMkOrphanPlugin(t *testing.T, achDir, name string) {
	t.Helper()
	p := filepath.Join(achDir, "plugin", name, "agents")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir orphan plugin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p, "x.md"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write orphan plugin file: %v", err)
	}
}

// TestCommit_PluginsExtractToEphemeralStage proves the no-cache contract: a
// plugin diffTarget is extracted under pluginStageRoot (<achDir>/tmp, swept
// every run) while a prompt diffTarget keeps the <achDir> base. This is what
// makes the orphan cross-plugin-collision bug structurally impossible — the
// projection source only ever contains the current run's plugins.
func TestCommit_PluginsExtractToEphemeralStage(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.fetcher = func(_ context.Context, _ string) (*manifest.Manifest, error) {
		return &manifest.Manifest{
			SchemaVersion: "v1alpha1",
			Environment:   "demo",
			Runtime:       &manifest.RuntimeBlock{},
			Context: &manifest.ContextBlock{
				Plugins: []manifest.ContentRef{{
					Name:        "myplugin",
					DownloadURL: "https://x/content/plugin/myplugin",
				}},
				Prompts: []manifest.ContentRef{{
					Name:        "myprompt",
					DownloadURL: "https://x/content/prompt/myprompt",
				}},
			},
		}, nil
	}
	ext := &baseCapturingExtractor{bases: map[string]string{}}
	c.extractor = ext

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}

	if got, want := ext.bases["myplugin"], c.pluginStageRoot(); got != want {
		t.Errorf("plugin extract base = %q, want pluginStageRoot %q", got, want)
	}
	if got, want := ext.bases["myprompt"], c.achDir; got != want {
		t.Errorf("prompt extract base = %q, want achDir %q (deliverable, not cache)", got, want)
	}
	// pluginStageRoot must sit under <achDir>/tmp so SweepTmp reclaims it.
	if want := filepath.Join(c.achDir, "tmp"); c.pluginStageRoot() != want {
		t.Errorf("pluginStageRoot = %q, want %q", c.pluginStageRoot(), want)
	}
}

// TestCommit_DropsLegacyPluginCache verifies a context hydration removes the
// pre-ephemeral persistent <achDir>/plugin cache (dead weight + stale
// projection source) while leaving prompt/artifact deliverable dirs intact.
func TestCommit_DropsLegacyPluginCache(t *testing.T) {
	c, _, _ := newTestCommit(t)
	mustMkOrphanPlugin(t, c.achDir, "cicd-automation@wshobson-agents")
	// A deliverable dir that must survive.
	artifactDir := filepath.Join(c.achDir, "artifact", "report")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir artifact: %v", err)
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}

	if _, err := os.Stat(filepath.Join(c.achDir, "plugin")); !os.IsNotExist(err) {
		t.Errorf("<achDir>/plugin still present (stat err=%v); legacy cache not dropped", err)
	}
	if _, err := os.Stat(artifactDir); err != nil {
		t.Errorf("<achDir>/artifact removed (%v); deliverables must be left intact", err)
	}
}

// TestCommit_LegacyPluginCacheGated verifies the legacy-cache drop respects
// scope: --only-runtime (plugins out of scope) and --dry-run (read-only) both
// leave <achDir>/plugin untouched.
func TestCommit_LegacyPluginCacheGated(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*commit)
	}{
		{"only-runtime", func(c *commit) { c.opts.OnlyRuntime = true }},
		{"dry-run", func(c *commit) { c.opts.DryRun = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newTestCommit(t)
			tc.mutate(c)
			mustMkOrphanPlugin(t, c.achDir, "kept@scope")

			if _, err := c.run(context.Background()); err != nil {
				t.Fatalf("c.run = %v, want nil", err)
			}
			if _, err := os.Stat(filepath.Join(c.achDir, "plugin")); err != nil {
				t.Errorf("<achDir>/plugin removed under %s (%v); must be preserved out-of-scope", tc.name, err)
			}
		})
	}
}
