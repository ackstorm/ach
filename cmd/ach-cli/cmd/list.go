// SPDX-License-Identifier: Apache-2.0

// `ach-cli env status` is the read-only installed-resource inventory surface
// (LIFE-03 / D-31). It is a STATIC read of <ach-dir>/state.json v2 — NO
// network, NO re-derivation, NO drift column. It loads the state file for
// the active workspace+environment scope, walks the projection buckets
// (Prompts / Plugins / Artifacts / RuntimeFiles / Adapter.Files), derives
// each entry's Kind from its owning bucket (state.FileEntry carries no
// kind field) and Environment from File.Environment, then renders a
// KIND / TARGET / ENVIRONMENT table by default or machine JSON under
// --json. The plugin inventory is a FLAT list over Plugins[] (D-25 — no
// per-plugin grouping, no owner tag).
//
// Empty/missing state.json yields the stable empty-state output
// ("No resources installed" for the table, "[]" for --json) and exit 0 —
// list never writes, never mutates, and never panics on a fresh workspace
// (state.Load returns (nil,nil) on a missing file).

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/render"
	"github.com/ackstorm/ach/internal/cli/state"
)

// listWorkspaceCwd is a test-only package-level seam. nil → os.Getwd at
// RunE time (production). Tests set it to a fixed temp dir so workspace-
// scope state resolution is deterministic without chdir.
var listWorkspaceCwd func() (string, error)

// resolveListWorkspaceCwd returns the workspace cwd, honoring the
// listWorkspaceCwd test seam when set.
func resolveListWorkspaceCwd() (string, error) {
	if listWorkspaceCwd != nil {
		return listWorkspaceCwd()
	}
	return os.Getwd()
}

// newEnvStatusCmd returns the `ach-cli env status` leaf — a thin,
// static-read cobra command. All formatting lives in the render package
// (env-list convention); RunE only resolves the state path, loads, walks
// the buckets into []render.StateEntryView, and delegates rendering.
func newEnvStatusCmd() *cobra.Command {
	var (
		flagJSON        bool
		flagGlobal      bool
		flagTarget      string
		flagEnvironment string
		flagFiles       bool
	)
	c := &cobra.Command{
		Use:   "status",
		Short: "Show installed/projected resources from state.json",
		Long: `Show the installed/projected resources recorded in state.json.

env status is a STATIC read of the workspace (or global) state.json — no
network call, no re-derivation, no drift detection. It prints one row
per projected resource with its KIND, on-disk TARGET path, and source
ENVIRONMENT. Use --json for machine-readable output.

An empty or missing state.json prints "No resources installed" (or an
empty JSON array under --json) and exits 0.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = flagTarget // reserved for future platform-scope resolution (D-31 scope axis)

			cwd, err := resolveListWorkspaceCwd()
			if err != nil {
				return &exit.CodedError{
					Code:    exit.General,
					Msg:     fmt.Sprintf("list: resolve workspace directory: %v", err),
					Wrapped: err,
				}
			}

			// Per-environment namespacing (spec §8.1): in project scope with no
			// --environment, enumerate EVERY .ach/<env>/state.json so a multi-env
			// project lists all its installed sets. A specific --environment (or
			// any --global run, which always requires --environment) resolves a
			// single state file.
			var files []*state.File
			if flagEnvironment == "" && !flagGlobal {
				all, lerr := loadAllWorkspaceStates(cwd)
				if lerr != nil {
					return &exit.CodedError{
						Code:    exit.ConfigFile,
						Msg:     fmt.Sprintf("list: enumerate environments: %v", lerr),
						Wrapped: lerr,
					}
				}
				files = all
			} else {
				// One env may now hold several per-platform state-<platform>.json
				// files (+ a legacy state.json) — enumerate and show them all.
				paths, err := state.ListStatePaths(cwd, flagEnvironment, flagGlobal)
				if err != nil {
					return &exit.CodedError{
						Code:    exit.ConfigFile,
						Msg:     fmt.Sprintf("list: resolve state path: %v", err),
						Wrapped: err,
					}
				}
				for _, p := range paths {
					f, lerr := state.Load(p)
					if lerr != nil {
						return &exit.CodedError{
							Code:    exit.ConfigFile,
							Msg:     fmt.Sprintf("list: read state: %v", lerr),
							Wrapped: lerr,
						}
					}
					if f != nil {
						files = append(files, f)
					}
				}
			}

			var entries []render.StateEntryView
			for _, f := range files {
				entries = append(entries, buildStateEntryViews(f)...)
			}

			if flagJSON {
				out, jerr := render.FormatStateListJSON(entries)
				if jerr != nil {
					return &exit.CodedError{
						Code:    exit.General,
						Msg:     fmt.Sprintf("list: render JSON: %v", jerr),
						Wrapped: jerr,
					}
				}
				_, _ = fmt.Fprint(cmd.OutOrStdout(), out)
				return nil
			}

			_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatStateList(entries, flagFiles))
			return nil
		},
	}
	c.Flags().BoolVar(&flagJSON, "json", false, "Machine-readable JSON output")
	c.Flags().BoolVarP(&flagGlobal, "global", "g", false, "Use $HOME/.ach/<env> scope instead of cwd/.ach")
	c.Flags().StringVar(&flagEnvironment, "environment", "",
		"Environment name (REQUIRED with --global; omit in project scope to list ALL envs)")
	c.Flags().StringVar(&flagTarget, "target", "", "Override platform scope resolution")
	c.Flags().BoolVarP(&flagFiles, "files", "f", false, "List every projected file instead of a per-resource summary")
	return c
}

// loadAllWorkspaceStates enumerates <cwd>/.ach/<env>/state.json across every
// per-environment subdir and returns the loaded *state.File set (skipping
// unreadable / wrong-schema files so one bad env never blocks the listing). An
// absent .ach/ yields an empty slice (→ "No resources installed"). A legacy
// FLAT .ach/state.json is intentionally ignored here — it is migrated into the
// namespaced layout on the next hydrate.
func loadAllWorkspaceStates(cwd string) ([]*state.File, error) {
	achRoot := filepath.Join(cwd, ".ach")
	dirents, err := os.ReadDir(achRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*state.File
	for _, d := range dirents {
		if !d.IsDir() {
			continue
		}
		// Each env dir may hold several per-platform state-<platform>.json files
		// (+ a legacy state.json); load them all.
		paths, perr := state.ListStatePaths(cwd, d.Name(), false)
		if perr != nil {
			continue
		}
		for _, p := range paths {
			f, lerr := state.Load(p)
			if lerr != nil || f == nil {
				continue
			}
			out = append(out, f)
		}
	}
	return out, nil
}

// buildStateEntryViews flattens a loaded state.File into the render view
// rows, deriving Kind from the owning bucket and Environment from
// f.Environment. A nil File (missing/fresh state) yields nil → the
// renderers emit their stable empty-state output. The bucket order
// mirrors state.WalkEntries (Prompts → Plugins → Artifacts →
// RuntimeFiles → Adapter.Files); the plugin inventory is a flat list over
// f.Plugins (D-25, no per-plugin grouping).
func buildStateEntryViews(f *state.File) []render.StateEntryView {
	if f == nil {
		return nil
	}
	env := f.Environment
	var out []render.StateEntryView
	appendBucket := func(kind string, entries []state.FileEntry) {
		for _, e := range entries {
			out = append(out, render.StateEntryView{
				Kind:        kind,
				Target:      e.Target,
				Environment: env,
				Source:      e.Source,
			})
		}
	}
	appendBucket("prompt", f.Prompts)
	appendBucket("plugin", f.Plugins)
	appendBucket("artifact", f.Artifacts)
	appendBucket("skill", f.Skills)
	appendBucket("runtime", f.RuntimeFiles)
	appendBucket("adapter", f.Adapter.Files)
	return out
}
