// SPDX-License-Identifier: Apache-2.0

// pkgcmd.go provides the shared cobra command factory used by both
// `ach-cli plugin` and `ach-cli skill`. The two commands differ only in
// resource kind and lens preference; all behaviour is parameterised by
// a pkgKind value passed at construction time.
//
// Lens-selection rules:
//   - plugin kind → prefer "plugin-marketplace" if the repo provides it, else "plugin"
//   - skill  kind → prefer "skill-marketplace"  if the repo provides it, else "skill"
//
// If the repo provides neither lens for the requested kind an error is returned.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/gitignore"
	"github.com/ackstorm/ach/internal/cli/localpkg/discover"
	"github.com/ackstorm/ach/internal/cli/localpkg/manager"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
)

// pkgKind parameterises install/uninstall/update/list by resource type.
type pkgKind string

const (
	kindPlugin pkgKind = "plugin"
	kindSkill  pkgKind = "skill"
)

// preferredLens returns the best lens to use for the given kind
// given the repo's declared capabilities. Returns "" when the repo
// provides none of the lenses relevant to kind.
func preferredLens(kind pkgKind, caps []store.Capability) string {
	var primary, fallback string
	switch kind {
	case kindPlugin:
		primary, fallback = discover.LensPluginMarketplace, discover.LensPlugin
	case kindSkill:
		primary, fallback = discover.LensSkillMarketplace, discover.LensSkill
	}
	hasMain, hasFallback := false, false
	for _, c := range caps {
		if c.Lens == primary {
			hasMain = true
		}
		if c.Lens == fallback {
			hasFallback = true
		}
	}
	if hasMain {
		return primary
	}
	if hasFallback {
		return fallback
	}
	return ""
}

// parseTargets splits --target values (comma-separated AND/OR repeatable) into
// canonical adapter IDs. Returns a CodedError{General} if any target is unknown.
func parseTargets(targets []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	for _, t := range targets {
		for _, part := range strings.Split(t, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			ad, ok := adapter.Lookup(part)
			if !ok {
				return nil, &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("unknown target adapter %q (known: claude, codex, gemini, opencode)", part),
				}
			}
			id := ad.ID()
			if _, dup := seen[id]; !dup {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// resolveRoot returns the install root directory.
// Priority: --global → $HOME; --dest if set; else os.Getwd().
func resolveRoot(global bool, dest string) (string, error) {
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("resolve home dir: %v", err)}
		}
		return home, nil
	}
	if dest != "" {
		return dest, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("getwd: %v", err)}
	}
	return cwd, nil
}

// collectIgnoreEntries appends the top-level .gitignore pattern (e.g. ".claude/")
// for each just-installed file to dst, so the credential-bearing agent config is
// kept out of git.
func collectIgnoreEntries(dst []string, recs []store.FileRec) []string {
	for _, r := range recs {
		if e := gitignore.TopLevelEntry(r.RelPath); e != "" {
			dst = append(dst, e)
		}
	}
	return dst
}

// ensureProjectGitignore writes the ach-managed .gitignore block for the
// installed entries under root. Project scope only (--global writes under $HOME,
// no repo to guard); best-effort — a failure warns but never fails the command.
func ensureProjectGitignore(w io.Writer, root string, global bool, entries []string) {
	if global || len(entries) == 0 {
		return
	}
	wrote, err := gitignore.Ensure(root, entries)
	if err != nil {
		_, _ = fmt.Fprintf(w, "warning: could not update .gitignore: %v\n", err)
		return
	}
	if wrote {
		_, _ = fmt.Fprintln(w,
			"notice: updated .gitignore (ach-cli block: agent config carries credentials)")
	}
}

// findRepo looks up a named repo from the store; returns CodedError{General} if absent.
func findRepo(name string, repos *store.ReposFile) (store.RepoEntry, error) {
	for _, r := range repos.Repos {
		if r.Name == name {
			return r, nil
		}
	}
	return store.RepoEntry{}, &exit.CodedError{
		Code: exit.General,
		Msg:  fmt.Sprintf("repo %q not registered (run: ach-cli repo add)", name),
	}
}

// collisionWarn writes a stderr warning for each just-written file (recs) whose
// relative path is already owned by a DIFFERENT installed entry at the same
// target — surfacing the otherwise-silent last-wins overwrite. Local install has
// no managed conflict policy (the governed `env hydrate` flow does, via
// --conflict namespace|skip|overwrite|refuse); this at least tells the user a
// previously-installed file was clobbered instead of losing it silently.
//
// Call AFTER Commit and BEFORE recording the new entry, so the just-installed
// ref is not yet present in installed and cannot collide with itself.
// ownersAt maps each target-relative path owned by a DIFFERENT installed entry
// at the given target to its owning ref. Shared by the pre-commit conflict
// resolver (manager.ResolveConflicts) and the post-commit collisionWarn so both
// agree on what counts as "already owned". The installing ref is excluded so a
// re-install never collides with itself.
func ownersAt(installed *store.InstalledFile, ref, target string) map[string]string {
	owners := make(map[string]string)
	for _, e := range installed.Installed {
		if e.Ref == ref || e.Target != target {
			continue
		}
		for _, f := range e.Files {
			owners[f.RelPath] = e.Ref
		}
	}
	return owners
}

// reportConflictActions prints one line per namespace/skip resolution applied by
// manager.ResolveConflicts (overwrite is reported post-commit by collisionWarn;
// refuse aborts before reaching here).
func reportConflictActions(w io.Writer, ref, target string, actions []manager.ConflictAction) {
	for _, a := range actions {
		switch a.Policy {
		case manager.ConflictNamespace:
			_, _ = fmt.Fprintf(w, "↳ %s → %s: namespaced %s → %s (clash with %s)\n",
				ref, target, a.Path, a.NewPath, a.Owner)
		case manager.ConflictSkip:
			_, _ = fmt.Fprintf(w, "↳ %s → %s: skipped %s (kept %s's)\n",
				ref, target, a.Path, a.Owner)
		}
	}
}

func collisionWarn(w io.Writer, installed *store.InstalledFile, ref, target string, recs []store.FileRec) {
	owners := ownersAt(installed, ref, target)
	for _, r := range recs {
		other, ok := owners[r.RelPath]
		if !ok {
			continue
		}
		// Only a MergeReplace collision is a genuine last-wins clobber. Composite
		// (marker-bounded block) and Deep (keyed JSON/TOML) merges are ADDITIVE —
		// the prior install's content is preserved alongside this one — so they
		// must not raise a false "overwrote" alarm (the .claude/settings.json MCP
		// accretion, and the AGENTS.md→CLAUDE.md composite back when it was
		// projected). FileRec.Merge: ""=replace (back-compat), "composite", "deep".
		if r.Merge == "composite" || r.Merge == "deep" {
			continue
		}
		_, _ = fmt.Fprintf(w,
			"! %s → %s: overwrote %s previously installed by %s\n",
			ref, target, r.RelPath, other)
	}
}

// upsertInstalled replaces any existing InstalledEntry with the same Ref+Target+Kind,
// or appends a new one. Kind is included to prevent a plugin and a skill that share
// the same name@repo+target from colliding.
func upsertInstalled(f *store.InstalledFile, entry store.InstalledEntry) {
	for i, e := range f.Installed {
		if e.Ref == entry.Ref && e.Target == entry.Target && e.Kind == entry.Kind {
			f.Installed[i] = entry
			return
		}
	}
	f.Installed = append(f.Installed, entry)
}

// removeInstalled removes all entries matching ref and (if non-empty) target
// and kind from the file, returning the removed entries.
func removeInstalled(f *store.InstalledFile, ref, target string, kind pkgKind) []store.InstalledEntry {
	var removed []store.InstalledEntry
	kept := f.Installed[:0]
	for _, e := range f.Installed {
		match := e.Ref == ref && e.Kind == string(kind)
		if match && (target == "" || e.Target == target) {
			removed = append(removed, e)
		} else {
			kept = append(kept, e)
		}
	}
	f.Installed = kept
	return removed
}

// shortSHA returns the first 7 characters of a SHA, or the full string.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// vlogf writes a progress line to w only when verbose is set. install/update
// use it to narrate the per-repo clone + per-target projection so a long
// multi-plugin install (dominated by the git clones) shows what it is doing.
func vlogf(w io.Writer, verbose bool, format string, args ...any) {
	if verbose {
		_, _ = fmt.Fprintf(w, format, args...)
	}
}

// ---- newPkgCmd: the parameterised parent + children factory -----------------

// newPkgCmd builds a cobra parent command for `ach-cli plugin` or `ach-cli skill`
// (determined by kind). It registers install/uninstall/update/list children.
func newPkgCmd(kind pkgKind) *cobra.Command {
	kindStr := string(kind)

	parent := &cobra.Command{
		Use:   kindStr,
		Short: fmt.Sprintf("Manage locally installed %ss", kindStr),
		Long: fmt.Sprintf(`Manage locally installed %ss.

Children:
  install    Fetch and install a %s from a registered repo
  uninstall  Remove an installed %s
  update     Re-resolve and re-install (or all if no args given)
  list       Show installed %ss (from installed.json; not remote catalog)

<name@repo> identifies a %s by name and the registered repo it came from.
Use 'ach-cli repo add' to register a repo first.
`, kindStr, kindStr, kindStr, kindStr, kindStr),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	parent.AddCommand(
		newPkgInstallCmd(kind),
		newPkgUninstallCmd(kind),
		newPkgUpdateCmd(kind),
		newPkgListCmd(kind),
	)
	return parent
}

// ---- install ----------------------------------------------------------------

func newPkgInstallCmd(kind pkgKind) *cobra.Command {
	var (
		flagTargets  []string
		flagGlobal   bool
		flagDest     string
		flagConflict string
		flagVerbose  bool
	)
	kindStr := string(kind)
	c := &cobra.Command{
		Use:   "install <name@repo>...",
		Short: fmt.Sprintf("Fetch and install a %s from a registered repo", kindStr),
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			targets, err := parseTargets(flagTargets)
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				return &exit.CodedError{Code: exit.General, Msg: "install: at least one --target is required"}
			}

			root, err := resolveRoot(flagGlobal, flagDest)
			if err != nil {
				return err
			}

			policy, err := manager.ParseConflictPolicy(flagConflict)
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: "install: " + err.Error()}
			}

			repos, err := store.LoadRepos()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("install: load repos: %v", err)}
			}

			installed, err := store.LoadInstalled()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("install: load installed: %v", err)}
			}

			// One fetch cache for the whole invocation: a repo (esp. a
			// marketplace) is cloned once and reused across every plugin/skill
			// installed from it, instead of re-cloning per item.
			fetchCache := manager.NewFetchCache()

			// Accumulate the top-level adapter dirs/files written across every
			// target so the project .gitignore can exclude the credential-bearing
			// config after the install completes.
			var ignoreEntries []string

			for _, arg := range args {
				atIdx := strings.LastIndex(arg, "@")
				if atIdx < 0 {
					return &exit.CodedError{
						Code: exit.General,
						Msg:  fmt.Sprintf("install: %q: expected format <name@repo>", arg),
					}
				}
				name := arg[:atIdx]
				repoName := arg[atIdx+1:]

				repo, err := findRepo(repoName, repos)
				if err != nil {
					return err
				}

				lens := preferredLens(kind, repo.Provides)
				if lens == "" {
					return &exit.CodedError{
						Code: exit.General,
						Msg: fmt.Sprintf(
							"install: repo %q does not provide %s or %s-marketplace",
							repoName, kindStr, kindStr,
						),
					}
				}

				token, _ := store.LoadToken(repo.Name)

				ref := name + "@" + repoName
				vlogf(cmd.ErrOrStderr(), flagVerbose, "→ resolving %s (%s lens, repo %s)…\n", ref, lens, repo.Name)
				rr, err := manager.ResolveWithCache(ctx, repo, token, name, lens, fetchCache)
				if err != nil {
					return cloneExitErr("install resolve", err)
				}
				defer func() { _ = os.RemoveAll(rr.StageDir) }() //nolint:gocritic
				vlogf(cmd.ErrOrStderr(), flagVerbose, "  resolved %s @ %s\n", ref, shortSHA(rr.ResolvedSHA))

				for _, targetID := range targets {
					vlogf(cmd.ErrOrStderr(), flagVerbose, "  projecting → %s\n", targetID)
					writes, err := manager.Project(rr.StageDir, targetID)
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("install: project for %s: %v", targetID, err),
						}
					}

					// Conflict resolution (pre-commit): de-collide against files
					// owned by OTHER installed refs at this target per --conflict.
					// Only MergeReplace writes clash; additive merges pass through.
					owners := ownersAt(installed, ref, targetID)
					resolved, actions, err := manager.ResolveConflicts(writes, owners, policy, name)
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("install: %s → %s: %v", ref, targetID, err),
						}
					}

					recs, err := manager.Commit(root, flagGlobal, targetID, name, resolved)
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("install: commit for %s: %v", targetID, err),
						}
					}

					// Report namespace/skip resolutions regardless of recs count.
					reportConflictActions(cmd.ErrOrStderr(), ref, targetID, actions)

					// A 0-file projection means nothing matched this adapter's
					// rules (e.g. a root-SKILL.md-only repo resolved via the
					// plugin lens, or a plugin with nothing for this adapter).
					// On INSTALL nothing was actually installed, so we warn and
					// do NOT record an installed.json entry — there is nothing to
					// track or later uninstall. (Contrast the UPDATE path, which
					// keeps the record because the old files were already removed
					// and the resolved SHA still needs tracking.) Suppress the
					// "nothing matched" note when every write was skipped by
					// policy — the skip lines above already explain the 0 count.
					if len(recs) == 0 {
						if len(actions) == 0 {
							_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
								"! %s → %s: 0 files projected (nothing matched %s's rules — wrong kind/lens or empty resource)\n",
								ref, targetID, targetID)
						}
						continue
					}

					collisionWarn(cmd.ErrOrStderr(), installed, ref, targetID, recs)

					entry := store.InstalledEntry{
						Ref:         ref,
						Repo:        repoName,
						Name:        name,
						Kind:        kindStr,
						Target:      targetID,
						ResolvedSHA: rr.ResolvedSHA,
						Files:       recs,
						InstalledAt: nowFn().UTC().Format(time.RFC3339),
					}
					upsertInstalled(installed, entry)
					ignoreEntries = collectIgnoreEntries(ignoreEntries, recs)

					_, _ = fmt.Fprintf(cmd.OutOrStdout(),
						"✓ %s → %s (%d files)\n", ref, targetID, len(recs))
				}
			}

			if err := store.SaveInstalled(installed); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("install: save installed: %v", err)}
			}
			ensureProjectGitignore(cmd.ErrOrStderr(), root, flagGlobal, ignoreEntries)
			return nil
		},
	}
	c.Flags().StringArrayVar(&flagTargets, "target", nil,
		"Adapter(s) to install for (comma-separated or repeatable): claude, codex, gemini, opencode")
	c.Flags().BoolVar(&flagGlobal, "global", false, "Install to $HOME instead of --dest / cwd")
	c.Flags().StringVar(&flagDest, "dest", "", "Destination root directory (default: cwd)")
	c.Flags().StringVar(&flagConflict, "conflict", "namespace",
		"Clash policy when another install owns a target path: namespace|skip|overwrite|refuse")
	c.Flags().BoolVar(&flagVerbose, "verbose", false, "Narrate per-repo clone + per-target projection progress")
	_ = c.MarkFlagRequired("target")
	return c
}

// ---- uninstall --------------------------------------------------------------

func newPkgUninstallCmd(kind pkgKind) *cobra.Command {
	var (
		flagTargets []string
		flagGlobal  bool
		flagDest    string
	)
	kindStr := string(kind)
	c := &cobra.Command{
		Use:   "uninstall <name@repo>...",
		Short: fmt.Sprintf("Remove an installed %s", kindStr),
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveRoot(flagGlobal, flagDest)
			if err != nil {
				return err
			}

			// Parse optional target filter(s).
			var targets []string
			if len(flagTargets) > 0 {
				targets, err = parseTargets(flagTargets)
				if err != nil {
					return err
				}
			}

			installed, err := store.LoadInstalled()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("uninstall: load installed: %v", err)}
			}

			for _, arg := range args {
				atIdx := strings.LastIndex(arg, "@")
				if atIdx < 0 {
					return &exit.CodedError{
						Code: exit.General,
						Msg:  fmt.Sprintf("uninstall: %q: expected format <name@repo>", arg),
					}
				}
				ref := arg // full "name@repo"

				if len(targets) == 0 {
					// No --target given: remove all targets for this ref.
					removed := removeInstalled(installed, ref, "", kind)
					if len(removed) == 0 {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(),
							"%s %q not installed (nothing to remove)\n", kindStr, ref)
						continue
					}
					for _, e := range removed {
						skipped, unErr := manager.Uninstall(root, e.Files)
						if unErr != nil {
							return &exit.CodedError{
								Code: exit.General,
								Msg:  fmt.Sprintf("uninstall: %s: %v", ref, unErr),
							}
						}
						for _, s := range skipped {
							_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
								"! skipped user-modified file: %s\n", s)
						}
						_, _ = fmt.Fprintf(cmd.OutOrStdout(),
							"✓ uninstalled %s ← %s\n", ref, e.Target)
					}
				} else {
					// --target(s) given: remove only the specified targets.
					anyRemoved := false
					for _, tid := range targets {
						removed := removeInstalled(installed, ref, tid, kind)
						if len(removed) == 0 {
							_, _ = fmt.Fprintf(cmd.OutOrStdout(),
								"%s %q not installed for %s (nothing to remove)\n", kindStr, ref, tid)
							continue
						}
						anyRemoved = true
						for _, e := range removed {
							skipped, unErr := manager.Uninstall(root, e.Files)
							if unErr != nil {
								return &exit.CodedError{
									Code: exit.General,
									Msg:  fmt.Sprintf("uninstall: %s ← %s: %v", ref, tid, unErr),
								}
							}
							for _, s := range skipped {
								_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
									"! skipped user-modified file: %s\n", s)
							}
							_, _ = fmt.Fprintf(cmd.OutOrStdout(),
								"✓ uninstalled %s ← %s\n", ref, e.Target)
						}
					}
					_ = anyRemoved
				}
			}

			if err := store.SaveInstalled(installed); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("uninstall: save installed: %v", err)}
			}
			return nil
		},
	}
	c.Flags().StringArrayVar(&flagTargets, "target", nil, "Limit uninstall to this adapter (optional)")
	c.Flags().BoolVar(&flagGlobal, "global", false, "Uninstall from $HOME root")
	c.Flags().StringVar(&flagDest, "dest", "", "Root directory (default: cwd)")
	return c
}

// ---- update -----------------------------------------------------------------

func newPkgUpdateCmd(kind pkgKind) *cobra.Command {
	var (
		flagGlobal   bool
		flagDest     string
		flagConflict string
		flagVerbose  bool
	)
	kindStr := string(kind)
	c := &cobra.Command{
		Use:   "update [<name@repo>...]",
		Short: fmt.Sprintf("Re-resolve and re-install %ss (all if no args)", kindStr),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Root for re-install; uninstall uses same root derived from stored files.
			// For update we need a root — use $HOME when --global, --dest if set, else cwd.
			root, err := resolveRoot(flagGlobal, flagDest)
			if err != nil {
				return err
			}

			policy, err := manager.ParseConflictPolicy(flagConflict)
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: "update: " + err.Error()}
			}

			installed, err := store.LoadInstalled()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("update: load installed: %v", err)}
			}

			repos, err := store.LoadRepos()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("update: load repos: %v", err)}
			}

			// One fetch cache for the whole invocation (see install).
			fetchCache := manager.NewFetchCache()

			// Accumulate ignore entries for re-projected files (an update may
			// introduce a new adapter dir/file the original install lacked).
			var ignoreEntries []string

			// Determine which refs to update.
			var targets []string
			if len(args) == 0 {
				// Collect all distinct refs of this kind.
				seen := map[string]struct{}{}
				for _, e := range installed.Installed {
					if e.Kind == kindStr {
						if _, ok := seen[e.Ref]; !ok {
							seen[e.Ref] = struct{}{}
							targets = append(targets, e.Ref)
						}
					}
				}
			} else {
				targets = args
			}

			if len(targets) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "no %ss installed\n", kindStr)
				return nil
			}

			for _, ref := range targets {
				atIdx := strings.LastIndex(ref, "@")
				if atIdx < 0 {
					return &exit.CodedError{
						Code: exit.General,
						Msg:  fmt.Sprintf("update: %q: expected format <name@repo>", ref),
					}
				}
				name := ref[:atIdx]
				repoName := ref[atIdx+1:]

				repo, err := findRepo(repoName, repos)
				if err != nil {
					return err
				}

				lens := preferredLens(kind, repo.Provides)
				if lens == "" {
					return &exit.CodedError{
						Code: exit.General,
						Msg: fmt.Sprintf(
							"update: repo %q does not provide %s or %s-marketplace",
							repoName, kindStr, kindStr,
						),
					}
				}

				token, _ := store.LoadToken(repo.Name)

				vlogf(cmd.ErrOrStderr(), flagVerbose, "→ resolving %s (%s lens, repo %s)…\n", ref, lens, repo.Name)
				rr, err := manager.ResolveWithCache(ctx, repo, token, name, lens, fetchCache)
				if err != nil {
					return cloneExitErr("update resolve", err)
				}
				defer func() { _ = os.RemoveAll(rr.StageDir) }() //nolint:gocritic
				vlogf(cmd.ErrOrStderr(), flagVerbose, "  resolved %s @ %s\n", ref, shortSHA(rr.ResolvedSHA))

				// Find all installed entries for this ref+kind.
				var entries []store.InstalledEntry
				for _, e := range installed.Installed {
					if e.Ref == ref && e.Kind == kindStr {
						entries = append(entries, e)
					}
				}
				if len(entries) == 0 {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %q not installed, skipping\n", kindStr, ref)
					continue
				}

				for _, e := range entries {
					if rr.ResolvedSHA == e.ResolvedSHA {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(),
							"%s %s ← %s up to date (%s)\n", kindStr, ref, e.Target, shortSHA(rr.ResolvedSHA))
						continue
					}

					// Uninstall old files.
					skipped, err := manager.Uninstall(root, e.Files)
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("update: uninstall old %s %s: %v", ref, e.Target, err),
						}
					}
					for _, s := range skipped {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "! skipped user-modified file: %s\n", s)
					}

					// Re-install.
					writes, err := manager.Project(rr.StageDir, e.Target)
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("update: project for %s: %v", e.Target, err),
						}
					}
					// Conflict resolution against OTHER refs' files at this target
					// (the ref's own old files were removed by Uninstall above and
					// ownersAt excludes the same ref, so no self-collision).
					owners := ownersAt(installed, ref, e.Target)
					resolved, actions, err := manager.ResolveConflicts(writes, owners, policy, name)
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("update: %s → %s: %v", ref, e.Target, err),
						}
					}

					recs, err := manager.Commit(root, flagGlobal, e.Target, name, resolved)
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("update: commit for %s: %v", e.Target, err),
						}
					}

					reportConflictActions(cmd.ErrOrStderr(), ref, e.Target, actions)

					// A 0-file projection means nothing matched this adapter's
					// rules. On UPDATE we still warn — but, unlike INSTALL, we
					// KEEP the record below: the old files were already removed
					// by the Uninstall above and the new resolved SHA still needs
					// tracking, so the entry must be upserted (now with empty
					// Files) to keep installed.json consistent with disk. Suppress
					// the "nothing matched" note when every write was policy-skipped.
					if len(recs) == 0 && len(actions) == 0 {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"! %s → %s: 0 files projected (nothing matched %s's rules — wrong kind/lens or empty resource)\n",
							ref, e.Target, e.Target)
					}

					collisionWarn(cmd.ErrOrStderr(), installed, ref, e.Target, recs)

					newEntry := store.InstalledEntry{
						Ref:         ref,
						Repo:        repoName,
						Name:        name,
						Kind:        kindStr,
						Target:      e.Target,
						ResolvedSHA: rr.ResolvedSHA,
						Files:       recs,
						InstalledAt: nowFn().UTC().Format(time.RFC3339),
					}
					upsertInstalled(installed, newEntry)
					ignoreEntries = collectIgnoreEntries(ignoreEntries, recs)

					_, _ = fmt.Fprintf(cmd.OutOrStdout(),
						"✓ updated %s → %s (%s → %s, %d files)\n",
						ref, e.Target, shortSHA(e.ResolvedSHA), shortSHA(rr.ResolvedSHA), len(recs))
				}
			}

			if err := store.SaveInstalled(installed); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("update: save installed: %v", err)}
			}
			ensureProjectGitignore(cmd.ErrOrStderr(), root, flagGlobal, ignoreEntries)
			return nil
		},
	}
	c.Flags().BoolVar(&flagGlobal, "global", false, "Update from $HOME root")
	c.Flags().StringVar(&flagDest, "dest", "", "Destination root directory (default: cwd)")
	c.Flags().StringVar(&flagConflict, "conflict", "namespace",
		"Clash policy when another install owns a target path: namespace|skip|overwrite|refuse")
	c.Flags().BoolVar(&flagVerbose, "verbose", false, "Narrate per-repo clone + per-target projection progress")
	return c
}

// ---- list -------------------------------------------------------------------

func newPkgListCmd(kind pkgKind) *cobra.Command {
	var flagRepo string
	kindStr := string(kind)
	c := &cobra.Command{
		Use:   "list",
		Short: fmt.Sprintf("Show installed %ss (from installed.json)", kindStr),
		Long: fmt.Sprintf(`Show installed %ss.

Lists only locally-installed items recorded in installed.json. To see
all available %ss in a remote repo catalog, you would need to re-clone
the repo (not supported in v1 list — run 'ach-cli repo update' to refresh).
`, kindStr, kindStr),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			installed, err := store.LoadInstalled()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("list: load installed: %v", err)}
			}

			var rows []store.InstalledEntry
			for _, e := range installed.Installed {
				if e.Kind != kindStr {
					continue
				}
				if flagRepo != "" && e.Repo != flagRepo {
					continue
				}
				rows = append(rows, e)
			}

			if len(rows) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "no %ss installed\n", kindStr)
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "REF\tTARGET\tSHA\tINSTALLED_AT")
			for _, e := range rows {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					e.Ref, e.Target, shortSHA(e.ResolvedSHA), e.InstalledAt)
			}
			_ = w.Flush()
			return nil
		},
	}
	c.Flags().StringVar(&flagRepo, "repo", "", "Filter installed items by repo name")
	return c
}
