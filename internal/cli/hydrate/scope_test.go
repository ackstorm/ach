// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

// fullPrev builds a representative populated v2 state.File covering all
// five buckets so each scope assertion can prove the right survivors.
func fullPrev() *state.File {
	return &state.File{
		SchemaVersion: "3",
		Environment:   "prod",
		Profile:       "main",
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
		if got.SchemaVersion != "3" {
			t.Fatalf("SchemaVersion = %q, want \"2\"", got.SchemaVersion)
		}
		if got.Environment != "prod" || got.Profile != "main" {
			t.Fatalf("Environment/Profile not carried: env=%q dep=%q", got.Environment, got.Profile)
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
		if got.SchemaVersion != "3" {
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

func TestParseItem(t *testing.T) {
	for in, want := range map[string]Item{
		"plugin/x": {Kind: kindPlugin, Name: "x"},
		"skill/x":  {Kind: kindSkill, Name: "x"},
	} {
		got, err := ParseItem(in)
		if err != nil || got == nil || *got != want {
			t.Errorf("ParseItem(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"prompt/x", "plugin/", "/x", "x", "plugin/a/b", ""} {
		if got, err := ParseItem(in); err == nil || err.Error() != "--only must be plugin/<name> or skill/<name>" {
			t.Errorf("ParseItem(%q) = %v, %v; want the usage error", in, got, err)
		}
	}
}

func TestBuildItemRemoved(t *testing.T) {
	prev := &state.File{SchemaVersion: "3", Environment: "demo", Profile: "p",
		Plugins: []state.FileEntry{
			{Target: "a1", Source: "a"}, {Target: "b1", Source: "b"}, {Target: "a2", Source: "a"},
		},
		Skills:       []state.FileEntry{{Target: "s1", Source: "s"}, {Target: "d1", Source: "demo-a"}},
		RuntimeFiles: []state.FileEntry{{Target: "r1"}},
		Adapter:      state.AdapterSection{ID: "claude-code", Files: []state.FileEntry{{Target: "ad1"}}},
	}
	before := fmt.Sprintf("%+v", *prev)

	out, ok := BuildItemRemoved(prev, Item{Kind: kindPlugin, Name: "a"})
	if !ok {
		t.Fatal("ok = false; want true")
	}
	if len(out.Plugins) != 1 || out.Plugins[0].Source != "b" {
		t.Errorf("Plugins = %+v; want only b", out.Plugins)
	}
	if len(out.Skills) != 2 || len(out.RuntimeFiles) != 1 || len(out.Adapter.Files) != 1 ||
		out.Adapter.ID != "claude-code" || out.Environment != "demo" || out.Profile != "p" {
		t.Errorf("other buckets not carried: %+v", out)
	}

	// A cross-environment de-collided skill is recorded as <env>-<name>.
	out, ok = BuildItemRemoved(prev, Item{Kind: kindSkill, Name: "a"})
	if !ok || len(out.Skills) != 1 || out.Skills[0].Source != "s" {
		t.Errorf("skill/a: ok=%v Skills=%+v; want the demo-a row removed", ok, out.Skills)
	}

	if _, ok := BuildItemRemoved(prev, Item{Kind: kindSkill, Name: "zzz"}); ok {
		t.Error("absent item: ok = true; want false")
	}
	if after := fmt.Sprintf("%+v", *prev); after != before {
		t.Errorf("prev mutated:\n%s\n%s", before, after)
	}
}
