// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"reflect"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

// fullPrev builds a representative populated v2 state.File covering all
// five buckets so each scope assertion can prove the right survivors.
func fullPrev() *state.File {
	return &state.File{
		SchemaVersion: "2",
		Environment:   "prod",
		Deployment:    "main",
		Prompts: []state.FileEntry{
			{Target: ".ach/prompts/a.md", Hash: "h1", SourceHash: "s1"},
		},
		Plugins: []state.FileEntry{
			{Target: "CLAUDE.md", Hash: "h2", SourceHash: "s2", Merge: "composite", Keys: []string{"foo"}},
		},
		Artifacts: []state.FileEntry{
			{Target: ".ach/artifacts/bin", Hash: "h3", SourceHash: "s3"},
		},
		RuntimeFiles: []state.FileEntry{
			{Target: ".mcp.json", Hash: "h4", SourceHash: "s4", Merge: "deep", Keys: []string{"mcpServers.x"}},
		},
		Adapter: state.AdapterSection{
			ID: "claude-code",
			Files: []state.FileEntry{
				{Target: ".claude/settings.json", Hash: "h5", SourceHash: "s5", Merge: "deep", Keys: []string{"mcp.y"}},
			},
		},
	}
}

func bucketCounts(f *state.File) (ctx, runtime int) {
	ctx = len(f.Prompts) + len(f.Plugins) + len(f.Artifacts)
	runtime = len(f.RuntimeFiles) + len(f.Adapter.Files)
	return ctx, runtime
}

func TestBuildScopedEmpty(t *testing.T) {
	t.Run("includeRuntime_full_teardown_empties_all_buckets", func(t *testing.T) {
		prev := fullPrev()
		got := BuildScopedEmpty(prev, true, false)
		ctx, runtime := bucketCounts(got)
		if ctx != 0 || runtime != 0 {
			t.Fatalf("full teardown must empty all buckets, got context=%d runtime=%d", ctx, runtime)
		}
		if got.SchemaVersion != "2" {
			t.Fatalf("SchemaVersion = %q, want \"2\"", got.SchemaVersion)
		}
		if got.Environment != "prod" || got.Deployment != "main" {
			t.Fatalf("Environment/Deployment not carried: env=%q dep=%q", got.Environment, got.Deployment)
		}
	})

	t.Run("default_context_only_retains_runtime", func(t *testing.T) {
		prev := fullPrev()
		got := BuildScopedEmpty(prev, false, false)
		if len(got.Prompts) != 0 || len(got.Plugins) != 0 || len(got.Artifacts) != 0 {
			t.Fatalf("context buckets must be empty (context removed), got %+v", got)
		}
		if len(got.RuntimeFiles) != 1 {
			t.Fatalf("RuntimeFiles must be retained, got %d", len(got.RuntimeFiles))
		}
		if len(got.Adapter.Files) != 1 {
			t.Fatalf("Adapter.Files must be retained, got %d", len(got.Adapter.Files))
		}
		if got.Adapter.ID != "claude-code" {
			t.Fatalf("Adapter.ID must survive when runtime is retained, got %q", got.Adapter.ID)
		}
	})

	t.Run("onlyRuntime_retains_context", func(t *testing.T) {
		prev := fullPrev()
		got := BuildScopedEmpty(prev, false, true)
		if len(got.Prompts) != 1 || len(got.Plugins) != 1 || len(got.Artifacts) != 1 {
			t.Fatalf("context buckets must be retained, got prompts=%d plugins=%d artifacts=%d",
				len(got.Prompts), len(got.Plugins), len(got.Artifacts))
		}
		if len(got.RuntimeFiles) != 0 || len(got.Adapter.Files) != 0 {
			t.Fatalf("runtime buckets must be empty (runtime removed), got runtimeFiles=%d adapterFiles=%d",
				len(got.RuntimeFiles), len(got.Adapter.Files))
		}
	})

	t.Run("does_not_mutate_prev", func(t *testing.T) {
		prev := fullPrev()
		snapshot := fullPrev() // identical independent copy
		// Run all three flag combinations against the same prev.
		_ = BuildScopedEmpty(prev, true, false)
		_ = BuildScopedEmpty(prev, false, false)
		_ = BuildScopedEmpty(prev, false, true)
		if !reflect.DeepEqual(prev, snapshot) {
			t.Fatalf("BuildScopedEmpty mutated prev.\n got: %+v\nwant: %+v", prev, snapshot)
		}
	})

	t.Run("nil_prev_yields_empty_v2_file", func(t *testing.T) {
		got := BuildScopedEmpty(nil, false, false)
		if got == nil {
			t.Fatal("nil prev must yield a non-nil empty File")
		}
		if got.SchemaVersion != "2" {
			t.Fatalf("SchemaVersion = %q, want \"2\"", got.SchemaVersion)
		}
		ctx, runtime := bucketCounts(got)
		if ctx != 0 || runtime != 0 {
			t.Fatalf("nil prev must yield all-empty buckets, got context=%d runtime=%d", ctx, runtime)
		}
	})

	t.Run("retained_slices_do_not_alias_prev", func(t *testing.T) {
		prev := fullPrev()
		got := BuildScopedEmpty(prev, false, false) // retains runtime
		if len(got.RuntimeFiles) == 0 {
			t.Fatal("precondition: expected retained RuntimeFiles")
		}
		got.RuntimeFiles[0].Target = "MUTATED"
		if prev.RuntimeFiles[0].Target == "MUTATED" {
			t.Fatal("returned RuntimeFiles aliases prev's backing array")
		}
	})
}
