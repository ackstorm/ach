// SPDX-License-Identifier: Apache-2.0

// `ach-cli list` is the read-only installed-resource inventory surface
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

// newListCmd returns the `ach-cli list` leaf — a thin, static-read cobra
// command. All formatting lives in the render package (env-list
// convention); RunE only resolves the state path, loads, walks the
// buckets into []render.StateEntryView, and delegates rendering.
func newListCmd() *cobra.Command {
	var (
		flagJSON        bool
		flagGlobal      bool
		flagPlatform    string
		flagEnvironment string
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "List installed/projected resources from state.json",
		Long: `List the installed/projected resources recorded in state.json.

list is a STATIC read of the workspace (or global) state.json — no
network call, no re-derivation, no drift detection. It prints one row
per projected resource with its KIND, on-disk TARGET path, and source
ENVIRONMENT. Use --json for machine-readable output.

An empty or missing state.json prints "No resources installed" (or an
empty JSON array under --json) and exits 0.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = flagPlatform // reserved for future platform-scope resolution (D-31 scope axis)

			cwd, err := resolveListWorkspaceCwd()
			if err != nil {
				return &exit.CodedError{
					Code:    exit.General,
					Msg:     fmt.Sprintf("list: resolve workspace directory: %v", err),
					Wrapped: err,
				}
			}

			statePath, err := state.ResolvePath(cwd, flagEnvironment, flagGlobal)
			if err != nil {
				return &exit.CodedError{
					Code:    exit.ConfigFile,
					Msg:     fmt.Sprintf("list: resolve state path: %v", err),
					Wrapped: err,
				}
			}

			f, err := state.Load(statePath)
			if err != nil {
				return &exit.CodedError{
					Code:    exit.ConfigFile,
					Msg:     fmt.Sprintf("list: read state: %v", err),
					Wrapped: err,
				}
			}

			entries := buildStateEntryViews(f)

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

			_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatStateList(entries))
			return nil
		},
	}
	c.Flags().BoolVar(&flagJSON, "json", false, "Machine-readable JSON output")
	c.Flags().BoolVarP(&flagGlobal, "global", "g", false, "Use $HOME/.ach/<env> scope instead of cwd/.ach")
	c.Flags().StringVar(&flagEnvironment, "environment", "", "Environment name (REQUIRED with --global)")
	c.Flags().StringVar(&flagPlatform, "platform", "", "Override platform scope resolution")
	return c
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
			})
		}
	}
	appendBucket("prompt", f.Prompts)
	appendBucket("plugin", f.Plugins)
	appendBucket("artifact", f.Artifacts)
	appendBucket("runtime", f.RuntimeFiles)
	appendBucket("adapter", f.Adapter.Files)
	return out
}

func init() {
	rootCmd.AddCommand(newListCmd())
}
