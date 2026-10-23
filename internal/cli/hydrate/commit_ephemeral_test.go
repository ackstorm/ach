// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"
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
