// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/achfile"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// errNothingHydrated is returned by deriveManifest when the workspace has no
// hydrated environments to derive ach.yaml from.
var errNothingHydrated = errors.New("nothing hydrated in this workspace yet")

// newEnvSaveCmd builds `ach-cli env save`: derive ach.yaml from the realized
// hydrate state under .ach/<env>/ and write it to the workspace root.
func newEnvSaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "save",
		Short: "Write ach.yaml from the environments already hydrated in this workspace",
		Long: "Derives a committed ach.yaml from the environments hydrated under " +
			".ach/, so a teammate can clone and run `ach-cli env hydrate` with no " +
			"arguments. ach.yaml contains environment names only — no secrets.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
			}
			m, err := deriveManifest(cwd)
			if errors.Is(err, errNothingHydrated) {
				return &exit.CodedError{
					Code: exit.General,
					Msg: "nothing hydrated in this workspace yet — run " +
						"`ach-cli env hydrate <name>` first, then `ach-cli env save`",
				}
			}
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
			}
			if err := m.WriteTo(cwd); err != nil {
				return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Wrote %s (%d environment(s)):\n", achfile.FileName, len(m.Environments))
			for _, e := range m.Environments {
				if len(e.Targets) == 0 {
					_, _ = fmt.Fprintf(out, "  - %s (targets: autodetect)\n", e.Name)
				} else {
					_, _ = fmt.Fprintf(out, "  - %s (targets: %v)\n", e.Name, e.Targets)
				}
			}
			_, _ = fmt.Fprintf(out, "Commit %s so teammates can `ach-cli env hydrate`.\n", achfile.FileName)
			return nil
		},
	}
}

// deriveManifest builds an achfile.Manifest from the hydrate state under
// <cwd>/.ach/. Environments are grouped by state.File.Environment; targets are
// the sorted-unique canonical adapter ids (state.File.Adapter.ID). Returns
// errNothingHydrated when no hydrated environment is found.
func deriveManifest(cwd string) (*achfile.Manifest, error) {
	files, err := loadAllWorkspaceStates(cwd)
	if err != nil {
		return nil, err
	}
	byEnv := map[string]map[string]bool{}
	for _, f := range files {
		if f == nil || f.Environment == "" {
			continue
		}
		if byEnv[f.Environment] == nil {
			byEnv[f.Environment] = map[string]bool{}
		}
		if f.Adapter.ID != "" {
			byEnv[f.Environment][f.Adapter.ID] = true
		}
	}
	if len(byEnv) == 0 {
		return nil, errNothingHydrated
	}
	names := make([]string, 0, len(byEnv))
	for n := range byEnv {
		names = append(names, n)
	}
	sort.Strings(names)
	entries := make([]achfile.Entry, 0, len(names))
	for _, n := range names {
		targets := make([]string, 0, len(byEnv[n]))
		for id := range byEnv[n] {
			targets = append(targets, id)
		}
		sort.Strings(targets)
		entries = append(entries, achfile.Entry{Name: n, Targets: targets})
	}
	return &achfile.Manifest{Version: 1, Environments: entries}, nil
}
