// SPDX-License-Identifier: Apache-2.0

// `ach-cli environment status` is the read-only installed-resource inventory surface
// (LIFE-03 / D-31). It is a STATIC read of <ach-dir>/state.json v2 — NO
// network, NO re-derivation, NO drift column. It loads the state file for
// the active workspace+environment scope, walks the projection buckets
// (Prompts / Plugins / Artifacts / RuntimeFiles / Adapter.Files), derives
// each entry's Kind from its owning bucket (state.FileEntry carries no
// kind field) and Environment from File.Environment, then renders a
// KIND / TARGET / ENVIRONMENT table by default or machine JSON under
// -o json. The plugin inventory is a FLAT list over Plugins[] (D-25 — no
// per-plugin grouping, no owner tag).
//
// Empty/missing state.json yields the stable empty-state output
// ("No resources installed" for the table, "[]" for -o json) and exit 0 —
// list never writes, never mutates, and never panics on a fresh workspace
// (state.Load returns (nil,nil) on a missing file).

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/hydrate"
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

// newEnvStatusCmd returns the `ach-cli environment status` leaf — a thin,
// static-read cobra command. All formatting lives in the render package
// (env-list convention); RunE only resolves the state path, loads, walks
// the buckets into []render.StateEntryView, and delegates rendering.
func newEnvStatusCmd() *cobra.Command {
	var (
		out         outputFlag
		flagGlobal  bool
		flagFiles   bool
		flagDir     string
		flagOffline bool
		cred        credFlags
	)
	c := &cobra.Command{
		Use:   "status [env]",
		Short: "Show what is installed here and whether it is up to date",
		Long: `Show the installed/projected resources recorded in state.json, and whether
each Environment changed since it was hydrated.

By default it prints, per Environment, its targets and scope, the installed
items grouped by kind, and every projected file you edited since the last
hydrate. It then asks the server whether each Environment changed since that
hydrate: "up to date", or + added to the Environment, - removed from it,
~ new content. It downloads nothing. --offline skips the server.

Use --files (-f) to list every projected file with its on-disk TARGET path,
or -o json for machine-readable output; both are local only.

Without [env] (project scope) it lists every hydrated environment; -g
(global scope) requires [env].

An empty or missing state.json prints "No resources installed" (or an
empty JSON array under -o json) and exits 0.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env := ""
			if len(args) > 0 {
				env = args[0]
			}
			if flagGlobal && env == "" {
				return &exit.CodedError{Code: exit.General, Msg: "an environment is required with -g"}
			}

			cwd, err := resolveListWorkspaceCwd()
			if err != nil {
				return &exit.CodedError{
					Code:    exit.General,
					Msg:     fmt.Sprintf("list: resolve workspace directory: %v", err),
					Wrapped: err,
				}
			}
			if flagDir != "" {
				cwd = flagDir
			}

			// Per-environment namespacing (spec §8.1): in project scope with no
			// [env], enumerate EVERY .ach/<env>/state.json so a multi-env
			// project lists all its installed sets. A specific [env] (always
			// present under -g) resolves a single environment.
			var files []*state.File
			if env == "" {
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
				// files — enumerate and show them all.
				paths, err := state.ListStatePaths(cwd, env, flagGlobal)
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

			if out.v == outputJSON {
				js, jerr := render.FormatStateListJSON(entries)
				if jerr != nil {
					return &exit.CodedError{
						Code:    exit.General,
						Msg:     fmt.Sprintf("list: render JSON: %v", jerr),
						Wrapped: jerr,
					}
				}
				_, _ = fmt.Fprint(cmd.OutOrStdout(), js)
				return nil
			}

			if len(entries) == 0 {
				where := filepath.Join(cwd, ".ach")
				if flagGlobal {
					where = "the global scope ($HOME/.ach)"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"No resources installed in %s; run `ach-cli environment hydrate <env>` (see `ach-cli environment list`)\n", where)
				return nil
			}
			if flagFiles {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatStateList(entries, true))
				return nil
			}
			if err := renderStatus(newUI(cmd.OutOrStdout(), false), files, cwd, flagGlobal); err != nil {
				return err
			}
			if flagOffline {
				return nil
			}
			return checkVersions(cmd, cred, files)
		},
	}
	registerOutputFlag(c, &out, "table", "json")
	c.Flags().BoolVarP(&flagGlobal, "global", "g", false, "Use $HOME/.ach/<env> scope instead of cwd/.ach")
	c.Flags().BoolVarP(&flagFiles, "files", "f", false, "List every projected file instead of a per-resource summary")
	c.Flags().StringVar(&flagDir, "dir", "", "Workspace root override (default: cwd)")
	c.Flags().BoolVar(&flagOffline, "offline", false, "Show local state only; do not ask the server what changed")
	registerCredFlags(c, &cred)
	return c
}

// checkVersions asks the server for each Environment's version and prints
// "up to date", or what changed since the version recorded at the last
// hydrate of each target.
func checkVersions(cmd *cobra.Command, cred credFlags, files []*state.File) error {
	c, err := resolveCred(cmd.Context(), cred)
	if err != nil {
		return err
	}
	if err := config.ValidateSecureURL(c.BaseURL, config.InsecureFromEnv()); err != nil {
		return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}
	hc := newAPIClient(c.BaseURL, c.Bearer, hydrateHTTPClient, cred.Verbose, cmd.ErrOrStderr())

	byEnv := map[string][]*state.File{}
	var envs []string
	for _, f := range files {
		if byEnv[f.Environment] == nil {
			envs = append(envs, f.Environment)
		}
		byEnv[f.Environment] = append(byEnv[f.Environment], f)
	}
	sort.Strings(envs)

	u := newUI(cmd.OutOrStdout(), cred.Verbose)
	u.verb("Checking", "%s against the server", countNoun(len(envs), "environment", "environments"))
	failed := 0
	for _, env := range envs {
		current, err := fetchEnvVersion(cmd.Context(), hc, env)
		if err != nil {
			failed++
			u.verb(env, "%s %s", u.paint(red, "error"), err)
			continue
		}
		var changes []hydrate.ItemChange
		seen := map[hydrate.ItemChange]bool{}
		unrecorded := false
		for _, f := range byEnv[env] {
			if f.EnvVersion == nil {
				unrecorded = true
				break
			}
			for _, ch := range versionChanges(f.EnvVersion, current) {
				if !seen[ch] {
					seen[ch] = true
					changes = append(changes, ch)
				}
			}
		}
		sortChanges(changes)
		switch {
		case unrecorded:
			u.verb(env, "version unknown: hydrate once to record its version")
		case len(changes) == 0:
			u.verb(env, "up to date")
		default:
			u.items(env, changes)
		}
	}
	if failed > 0 {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("%d of %d environment(s) could not be checked (--offline skips the server)", failed, len(envs)),
		}
	}
	return nil
}

// loadAllWorkspaceStates enumerates <cwd>/.ach/<env>/state-<platform>.json
// across every per-environment subdir and returns the loaded *state.File set
// (skipping unreadable / wrong-schema files so one bad env never blocks the
// listing). An absent .ach/ yields an empty slice (→ "No resources
// installed"). A legacy flat .ach/state.json is ignored.
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
		// Each env dir may hold several per-platform state-<platform>.json files;
		// load them all.
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

// renderStatus is the default `environment status` view: per Environment, its targets
// and scope, the installed items grouped by kind, and every projected file the
// user edited since the last hydrate (a missing file is not "edited": the next
// hydrate re-creates it).
func renderStatus(u *ui, files []*state.File, cwd string, global bool) error {
	toolRoot, scope := cwd, scopeProject
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("resolve $HOME: %v", err), Wrapped: err}
		}
		toolRoot, scope = home, scopeGlobal
	}
	// Prompts/artifacts resolve against <achDir>/.. = <root>/.ach, as in
	// the engine's reconcile-vs-disk step.
	wsRoot := filepath.Join(toolRoot, ".ach")

	byEnv := map[string][]*state.File{}
	var envs []string
	for _, f := range files {
		if byEnv[f.Environment] == nil {
			envs = append(envs, f.Environment)
		}
		byEnv[f.Environment] = append(byEnv[f.Environment], f)
	}
	sort.Strings(envs)

	anyEdited := false
	for _, env := range envs {
		var targets []string
		names := map[string][]string{}
		var kinds []string
		seen := map[[2]string]bool{}
		var edited []string
		for _, f := range byEnv[env] {
			if f.Adapter.ID != "" {
				targets = append(targets, f.Adapter.ID)
			}
			for _, it := range hydrate.ItemsOf(f) {
				if seen[[2]string{it.Kind, it.Name}] {
					continue
				}
				seen[[2]string{it.Kind, it.Name}] = true
				if names[it.Kind] == nil {
					kinds = append(kinds, it.Kind)
				}
				names[it.Kind] = append(names[it.Kind], it.Name)
			}
			edited = append(edited, editedFiles(f.Plugins, toolRoot)...)
			edited = append(edited, editedFiles(f.Skills, toolRoot)...)
			edited = append(edited, editedFiles(f.Prompts, wsRoot)...)
			edited = append(edited, editedFiles(f.Artifacts, wsRoot)...)
		}
		sort.Strings(targets)
		u.verb("Environment", "%s (%s · %s)", env, strings.Join(targets, ", "), scope)
		for _, k := range kinds {
			ns := names[k]
			more := ""
			if len(ns) > 6 {
				more = fmt.Sprintf("  (+%d)", len(ns)-6)
				ns = ns[:6]
			}
			u.verb("", "%s %-7s %s%s", u.paint(green, "✓"), k, strings.Join(ns, ", "), more)
		}
		for _, p := range edited {
			if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
				p = rel
			}
			u.verb("", "%s %-7s %s", u.paint(red, "!"), "edited", p)
		}
		anyEdited = anyEdited || len(edited) > 0
	}
	if anyEdited {
		u.verb("hint", "ach-cli environment hydrate --force  — discards local edits")
	}
	return nil
}

// editedFiles returns the plain (non-merged) projected files whose on-disk
// content no longer matches the hash recorded at hydrate time.
func editedFiles(entries []state.FileEntry, root string) []string {
	var out []string
	for _, e := range entries {
		if e.Merge != "" && e.Merge != "replace" {
			continue
		}
		p := adapter.ResolveDest(root, e.Target)
		if h, err := hash.HashFile(p); err == nil && h != e.Hash {
			out = append(out, p)
		}
	}
	return out
}
