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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/conflict"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/gitignore"
	"github.com/ackstorm/ach/internal/cli/hydrate"
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

// parseTargets is the ONE --target / ACH_TARGET parser (environment hydrate and
// local install/uninstall): values split on commas AND repeat, each part
// resolves through hydrate.ResolvePlatform (adapter ids + aliases; an unknown
// part's error lists every registered id), deduped in order. An effectively
// empty value is an error.
func parseTargets(values []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := hydrate.ResolvePlatform(part)
			if err != nil {
				return nil, err
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	if len(out) == 0 {
		return nil, &exit.CodedError{
			Code: exit.General,
			Msg:  "--target is empty: provide one or more platform ids (e.g. claude-code,codex)",
		}
	}
	return out, nil
}

// resolveRoot returns the install root directory.
// Priority: --global → $HOME; --dir if set; else os.Getwd().
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

// location is where an install lives: the tool's global root (-g) or one
// project directory, stored absolute.
type location struct {
	global bool
	dir    string
}

func newLocation(global bool, root string) location {
	if global {
		return location{global: true}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return location{dir: root}
}

// holds reports whether e was installed at l. A record with neither a
// directory nor -g (an installed.json written by an older ach-cli) matches
// every location, so it can still be updated and uninstalled.
func (l location) holds(e store.InstalledEntry) bool {
	if !e.Global && e.Dir == "" {
		return true
	}
	return e.Global == l.global && e.Dir == l.dir
}

// remapGlobalWrites resolves adapter-owned --global destinations before local
// conflict detection and commit. Project-scope paths remain rule-relative.
func remapGlobalWrites(global bool, root, adapterID string, writes []manager.PlannedWrite) {
	if !global {
		return
	}
	for i := range writes {
		writes[i].Path = adapter.RemapGlobalPath(adapterID, root, writes[i].Path)
	}
}

func conflictRoot(global bool, root, adapterID string) string {
	if !global {
		return root
	}
	return adapter.GlobalRoot(adapterID, root)
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

// opencodeMCPKeyPrefix is the dotted-key prefix of the MCP servers a plugin
// deep-merges into .opencode/opencode.json (recorded in FileRec.Keys).
const opencodeMCPKeyPrefix = "mcp.servers."

// opencodeRestart records whether a run wrote or removed an MCP server entry
// for opencode (an opencode record with `mcp.servers.*` keys).
type opencodeRestart struct{ on bool }

func (o *opencodeRestart) note(target string, recs ...[]store.FileRec) {
	if target != opencodeTarget {
		return
	}
	for _, rs := range recs {
		for _, r := range rs {
			for _, k := range r.Keys {
				o.on = o.on || strings.HasPrefix(k, opencodeMCPKeyPrefix)
			}
		}
	}
}

// flush prints the restart hint once, when a server entry changed.
func (o *opencodeRestart) flush(w io.Writer) {
	if o.on {
		_, _ = fmt.Fprintln(w, "hint: "+opencodeRestartHint)
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
		Msg:  fmt.Sprintf("repo %q not registered (run: ach-cli local repo add)", name),
	}
}

// collisionWarn writes a stderr warning for each just-written file (recs) whose
// relative path is already owned by a DIFFERENT installed entry at the same
// target — surfacing the otherwise-silent last-wins overwrite. Local install has
// no managed conflict policy (the governed `environment hydrate` flow does, via
// --conflict namespace|skip|overwrite|refuse); this at least tells the user a
// previously-installed file was clobbered instead of losing it silently.
//
// Call AFTER Commit and BEFORE recording the new entry, so the just-installed
// ref is not yet present in installed and cannot collide with itself.
// ownersAt maps each path a DIFFERENT installed entry at the given location
// records to its owning ref, whatever its target: several tools write the same
// .agents/skills tree. Shared by the pre-commit conflict resolver
// (manager.ResolveConflicts) and the post-commit collisionWarn so both agree on
// what counts as "already owned". The installing ref is excluded so a
// re-install never collides with itself.
func ownersAt(installed *store.InstalledFile, l location, ref string) map[string]string {
	owners := make(map[string]string)
	for _, e := range installed.Installed {
		if e.Ref == ref || !l.holds(e) {
			continue
		}
		for _, f := range e.Files {
			owners[f.RelPath] = e.Ref
		}
	}
	return owners
}

// mergeDeep is FileRec.Merge for a key-merged file (JSON/TOML), shared by key
// with other installs; any other value is a whole file.
const mergeDeep = "deep"

// heldElsewhere reports the whole files that records at l other than the ones
// being removed (skip) still hold: uninstalling one tool keeps a file another
// tool sharing its path still uses. A key-merged file is never held: each
// install removes only its own keys from it.
func heldElsewhere(installed *store.InstalledFile, l location, skip []store.InstalledEntry) map[string]bool {
	held := map[string]bool{}
	for _, e := range installed.Installed {
		if !l.holds(e) || slices.ContainsFunc(skip, func(s store.InstalledEntry) bool {
			return s.Ref == e.Ref && s.Target == e.Target && s.Global == e.Global && s.Dir == e.Dir
		}) {
			continue
		}
		for _, f := range e.Files {
			if f.Merge != mergeDeep {
				held[f.RelPath] = true
			}
		}
	}
	return held
}

// refuseUnowned stops an install or update (exit 7) that would replace a file
// no installed package at location records, for any target: the user's own, or
// another tool's. --force replaces it.
func refuseUnowned(
	force bool, verb, root, ref, target string, writes []manager.PlannedWrite,
	installed *store.InstalledFile, l location,
) error {
	if force {
		return nil
	}
	owned := map[string]bool{}
	for _, e := range installed.Installed {
		if !l.holds(e) {
			continue
		}
		for _, f := range e.Files {
			owned[f.RelPath] = true
		}
	}
	if err := manager.CheckUnowned(root, writes, owned); err != nil {
		return &exit.CodedError{
			Code: exit.CollisionRefuse,
			Msg: fmt.Sprintf("%s: %s → %s: %v\nhint: move it away, or re-run with --force to replace it",
				verb, ref, target, err),
		}
	}
	return nil
}

// reportConflictActions prints one line per namespace/skip resolution applied by
// manager.ResolveConflicts (overwrite is reported post-commit by collisionWarn;
// refuse aborts before reaching here).
func reportConflictActions(w io.Writer, ref, target string, actions []manager.ConflictAction) {
	for _, a := range actions {
		switch a.Policy {
		case conflict.Namespace:
			_, _ = fmt.Fprintf(w, "↳ %s → %s: namespaced %s → %s (clash with %s)\n",
				ref, target, a.Path, a.NewPath, a.Owner)
		case conflict.Skip:
			_, _ = fmt.Fprintf(w, "↳ %s → %s: skipped %s (kept %s's)\n",
				ref, target, a.Path, a.Owner)
		}
	}
}

func collisionWarn(
	w io.Writer, installed *store.InstalledFile, l location, ref, target string, recs []store.FileRec,
) {
	owners := ownersAt(installed, l, ref)
	for _, r := range recs {
		other, ok := owners[r.RelPath]
		if !ok {
			continue
		}
		// Only a MergeReplace collision is a genuine last-wins clobber. A Deep
		// (keyed JSON/TOML) merge is ADDITIVE — the prior install's content is
		// preserved alongside this one — so it must not raise a false
		// "overwrote" alarm (the .claude/settings.json MCP accretion).
		// FileRec.Merge: ""=replace (back-compat), "deep".
		if r.Merge == mergeDeep {
			continue
		}
		_, _ = fmt.Fprintf(w,
			"! %s → %s: overwrote %s previously installed by %s\n",
			ref, target, r.RelPath, other)
	}
}

// upsertInstalled replaces any existing InstalledEntry with the same
// Ref+Target+Kind at the entry's location, or appends a new one. Kind is
// included to prevent a plugin and a skill that share the same
// name@repo+target from colliding.
func upsertInstalled(f *store.InstalledFile, entry store.InstalledEntry) {
	refreshSharedRecs(f, entry)
	for i, e := range f.Installed {
		if e.Ref == entry.Ref && e.Target == entry.Target && e.Kind == entry.Kind &&
			(location{global: entry.Global, dir: entry.Dir}).holds(e) {
			f.Installed[i] = entry
			return
		}
	}
	f.Installed = append(f.Installed, entry)
}

// refreshSharedRecs copies the hash of every whole-file record of entry into
// the records the same item holds for another target at the same location and
// path. Several targets write one .agents/skills tree; with one hash per file,
// the next update or uninstall of the other target does not take this write
// for a local edit.
func refreshSharedRecs(f *store.InstalledFile, entry store.InstalledEntry) {
	written := map[string]string{}
	for _, r := range entry.Files {
		if r.Merge != mergeDeep {
			written[r.RelPath] = r.Hash
		}
	}
	at := location{global: entry.Global, dir: entry.Dir}
	for i, e := range f.Installed {
		if e.Ref != entry.Ref || e.Target == entry.Target || !at.holds(e) {
			continue
		}
		for j, r := range e.Files {
			if h, ok := written[r.RelPath]; ok && r.Merge != mergeDeep {
				f.Installed[i].Files[j].Hash = h
			}
		}
	}
}

// removeInstalled removes all entries at l matching ref, kind and (if
// non-empty) target from the file, returning the removed entries.
func removeInstalled(f *store.InstalledFile, l location, ref, target string, kind pkgKind) []store.InstalledEntry {
	var removed []store.InstalledEntry
	kept := f.Installed[:0]
	for _, e := range f.Installed {
		if installMatches(e, l, ref, target, kind) {
			removed = append(removed, e)
		} else {
			kept = append(kept, e)
		}
	}
	f.Installed = kept
	return removed
}

// findInstalled returns the matching entries WITHOUT mutating the store — the
// non-destructive twin of removeInstalled used by the `--dry-run` preview.
func findInstalled(f *store.InstalledFile, l location, ref, target string, kind pkgKind) []store.InstalledEntry {
	var found []store.InstalledEntry
	for _, e := range f.Installed {
		if installMatches(e, l, ref, target, kind) {
			found = append(found, e)
		}
	}
	return found
}

// installMatches reports whether entry e is the requested ref+kind at l,
// optionally narrowed to a single target ("" = any target).
func installMatches(e store.InstalledEntry, l location, ref, target string, kind pkgKind) bool {
	return e.Ref == ref && e.Kind == string(kind) && (target == "" || e.Target == target) && l.holds(e)
}

// reportDryRunWrites prints the planned writes for one ref→target without
// touching disk: "write" for file-owned (MergeReplace) resources, "merge" for
// the co-owned deep configs (.mcp.json, settings, …).
func reportDryRunWrites(w io.Writer, ref, target string, writes []manager.PlannedWrite) {
	_, _ = fmt.Fprintf(w, "[dry-run] %s → %s (%d files):\n", ref, target, len(writes))
	for _, pw := range writes {
		verb := "write"
		if pw.Merge != adapter.MergeReplace {
			verb = "merge"
		}
		_, _ = fmt.Fprintf(w, "    %-6s %s\n", verb, pw.Path)
	}
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

<name@repo> identifies a %s by name and the registered repo it came from.
Use 'ach-cli local repo add' to register a repo first.
`, kindStr, kindStr),
		RunE: helpOrUnknownSubcommand,
	}
	parent.AddCommand(
		newPkgInstallCmd(kind),
		newPkgUninstallCmd(kind),
		newPkgUpdateCmd(kind),
		newPkgOutdatedCmd(kind),
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
		flagDryRun   bool
		flagForce    bool
	)
	kindStr := string(kind)
	c := &cobra.Command{
		Use:        "install <name@repo>...",
		SuggestFor: []string{"add"},
		Short:      fmt.Sprintf("Fetch and install a %s from a registered repo", kindStr),
		Args:       cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (retErr error) {
			ctx := cmd.Context()

			targets, err := parseTargets(flagTargets)
			if err != nil {
				return err
			}

			root, err := resolveRoot(flagGlobal, flagDest)
			if err != nil {
				return err
			}
			here := newLocation(flagGlobal, root)

			policy, err := conflict.Parse(flagConflict)
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
			// A later refusal or failure must not leave files written by an
			// earlier ref or target untracked.
			dirty := false
			defer saveInstalledOnError(&retErr, &dirty, installed)

			// One fetch cache for the whole invocation: a repo (esp. a
			// marketplace) is cloned once and reused across every plugin/skill
			// installed from it, instead of re-cloning per item.
			fetchCache := manager.NewFetchCache()

			// Accumulate the top-level adapter dirs/files written across every
			// target so the project .gitignore can exclude the credential-bearing
			// config after the install completes.
			var ignoreEntries []string
			var restart opencodeRestart

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

				var plans []targetPlan
				for _, targetID := range targets {
					vlogf(cmd.ErrOrStderr(), flagVerbose, "  projecting → %s\n", targetID)
					writes, err := manager.Project(rr.StageDir, targetID, pluginPrefix(kind, name))
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("install: project for %s: %v", targetID, err),
						}
					}
					remapGlobalWrites(flagGlobal, root, targetID, writes)

					// Conflict resolution (pre-commit): de-collide against files
					// owned by OTHER installed refs at this target per --conflict.
					// Only MergeReplace writes clash; additive merges pass through.
					owners := ownersAt(installed, here, ref)
					namespaceRoot := conflictRoot(flagGlobal, root, targetID)
					resolved, actions, err := manager.ResolveConflicts(writes, owners, policy, name, namespaceRoot)
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("install: %s → %s: %v", ref, targetID, err),
						}
					}

					// Report namespace/skip resolutions regardless of recs count.
					reportConflictActions(cmd.ErrOrStderr(), ref, targetID, actions)

					// --dry-run: show the projection plan and write nothing.
					if flagDryRun {
						reportDryRunWrites(cmd.OutOrStdout(), ref, targetID, resolved)
						continue
					}
					plans = append(plans, targetPlan{targetID: targetID, resolved: resolved, actions: actions})
				}

				// Containment and unowned files are decided for every target
				// before the first write, so a refusal on a later target
				// leaves nothing behind.
				for _, pl := range plans {
					if err := manager.CheckCommit(root, flagGlobal, pl.resolved); err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("install: %s → %s: %v", ref, pl.targetID, err),
						}
					}
					if err := refuseUnowned(flagForce, "install", root, ref, pl.targetID, pl.resolved, installed, here); err != nil {
						return err
					}
				}
				for _, pl := range plans {
					targetID, resolved, actions := pl.targetID, pl.resolved, pl.actions

					recs, err := manager.Commit(root, flagGlobal, resolved)
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("install: commit for %s: %v", targetID, err),
						}
					}

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

					collisionWarn(cmd.ErrOrStderr(), installed, here, ref, targetID, recs)

					entry := store.InstalledEntry{
						Ref:         ref,
						Repo:        repoName,
						Name:        name,
						Kind:        kindStr,
						Target:      targetID,
						Dir:         here.dir,
						Global:      here.global,
						ResolvedSHA: rr.ResolvedSHA,
						Files:       recs,
						InstalledAt: nowFn().UTC().Format(time.RFC3339),
					}
					upsertInstalled(installed, entry)
					dirty = true
					ignoreEntries = collectIgnoreEntries(ignoreEntries, recs)
					restart.note(targetID, recs)

					_, _ = fmt.Fprintf(cmd.OutOrStdout(),
						"✓ %s → %s (%d files)\n", ref, targetID, len(recs))
				}
			}

			if flagDryRun {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[dry-run] no changes written")
				return nil
			}

			if err := store.SaveInstalled(installed); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("install: save installed: %v", err)}
			}
			ensureProjectGitignore(cmd.ErrOrStderr(), root, flagGlobal, ignoreEntries)
			restart.flush(cmd.ErrOrStderr())
			return nil
		},
	}
	c.Flags().StringSliceVar(&flagTargets, "target", nil,
		"Tool(s) to install for, comma-separated or repeated: antigravity, claude-code, codex, cursor, opencode, pi")
	c.Flags().BoolVarP(&flagGlobal, "global", "g", false, "Install to the tool's global config root")
	c.Flags().StringVar(&flagDest, "dir", "", "Project root directory (default: cwd)")
	c.Flags().StringVar(&flagConflict, "conflict", "namespace",
		"Clash policy when another install owns a target path: namespace|skip|overwrite|refuse")
	c.Flags().BoolVar(&flagVerbose, "verbose", false, "Narrate per-repo clone + per-target projection progress")
	c.Flags().BoolVar(&flagDryRun, "dry-run", false, "Resolve + project and print the plan, but write nothing")
	c.Flags().BoolVar(&flagForce, "force", false, "Replace files that already exist and ach-cli did not install")
	_ = c.MarkFlagRequired("target")
	return c
}

// targetPlan is one install target after projection and conflict resolution,
// before anything is written.
type targetPlan struct {
	targetID string
	resolved []manager.PlannedWrite
	actions  []manager.ConflictAction
}

// updatePlan is one installed entry's re-projection, before the old files are
// removed.
type updatePlan struct {
	entry    store.InstalledEntry
	resolved []manager.PlannedWrite
	actions  []manager.ConflictAction
}

// saveInstalledOnError persists the installed record when a run fails after
// it already wrote files, so those files stay tracked.
func saveInstalledOnError(retErr *error, dirty *bool, installed *store.InstalledFile) {
	if *retErr != nil && *dirty {
		_ = store.SaveInstalled(installed)
	}
}

// planUpdate projects the new version for one installed entry's target and
// resolves conflicts, touching nothing on disk.
func planUpdate(
	stageDir, root string, global bool, kind pkgKind, name, ref string,
	e store.InstalledEntry, installed *store.InstalledFile, l location, policy conflict.Policy,
) (updatePlan, error) {
	writes, err := manager.Project(stageDir, e.Target, pluginPrefix(kind, name))
	if err != nil {
		return updatePlan{}, &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("update: project for %s: %v", e.Target, err),
		}
	}
	remapGlobalWrites(global, root, e.Target, writes)
	owners := ownersAt(installed, l, ref)
	resolved, actions, err := manager.ResolveConflicts(writes, owners, policy, name, conflictRoot(global, root, e.Target))
	if err != nil {
		return updatePlan{}, &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("update: %s → %s: %v", ref, e.Target, err),
		}
	}
	return updatePlan{entry: e, resolved: resolved, actions: actions}, nil
}

// checkUpdatePlans refuses an update whose old files or new writes leave the
// project through a link, or whose new writes would replace a file no
// installed package records (unless force), before anything is removed.
func checkUpdatePlans(
	root string, global, force bool, ref string, plans []updatePlan, installed *store.InstalledFile, l location,
) error {
	for _, pl := range plans {
		err := func() error {
			if _, err := manager.UninstallPlan(root, global, pl.entry.Files); err != nil {
				return err
			}
			return manager.CheckCommit(root, global, pl.resolved)
		}()
		if err != nil {
			return &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("update: %s → %s: %v", ref, pl.entry.Target, err),
			}
		}
		if err := refuseUnowned(force, "update", root, ref, pl.entry.Target, pl.resolved, installed, l); err != nil {
			return err
		}
	}
	return nil
}

// ---- uninstall --------------------------------------------------------------

func newPkgUninstallCmd(kind pkgKind) *cobra.Command {
	var (
		flagTargets []string
		flagGlobal  bool
		flagDest    string
		flagDryRun  bool
	)
	kindStr := string(kind)
	c := &cobra.Command{
		Use:        "uninstall <name@repo>...",
		SuggestFor: []string{"remove", "rm"},
		Short:      fmt.Sprintf("Remove an installed %s", kindStr),
		Args:       cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveRoot(flagGlobal, flagDest)
			if err != nil {
				return err
			}
			here := newLocation(flagGlobal, root)

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

			// In --dry-run, look up entries non-destructively (no mutation of
			// `installed`) and classify via UninstallPlan instead of removing.
			lookup := removeInstalled
			if flagDryRun {
				lookup = findInstalled
			}

			// processEntry uninstalls one matched entry, or — in --dry-run —
			// prints the read-only plan (per-file remove/modify/skip/absent).
			var restart opencodeRestart
			processEntry := func(e store.InstalledEntry, held map[string]bool) error {
				files := slices.DeleteFunc(slices.Clone(e.Files), func(f store.FileRec) bool { return held[f.RelPath] })
				if flagDryRun {
					plan, perr := manager.UninstallPlan(root, flagGlobal, files)
					if perr != nil {
						return &exit.CodedError{Code: exit.General,
							Msg: fmt.Sprintf("uninstall: %s ← %s: %v", e.Ref, e.Target, perr)}
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(),
						"[dry-run] would uninstall %s ← %s (%d files):\n", e.Ref, e.Target, len(e.Files))
					for _, v := range plan {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "    %-6s %s\n", v.Op, v.RelPath)
					}
					return nil
				}
				skipped, unErr := manager.Uninstall(root, flagGlobal, files)
				if unErr != nil {
					return &exit.CodedError{Code: exit.General,
						Msg: fmt.Sprintf("uninstall: %s ← %s: %v", e.Ref, e.Target, unErr)}
				}
				for _, s := range skipped {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "! skipped user-modified file: %s\n", s)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ uninstalled %s ← %s\n", e.Ref, e.Target)
				restart.note(e.Target, e.Files)
				return nil
			}

			for _, arg := range args {
				if !strings.Contains(arg, "@") {
					return &exit.CodedError{
						Code: exit.General,
						Msg:  fmt.Sprintf("uninstall: %q: expected format <name@repo>", arg),
					}
				}
				ref := arg // full "name@repo"

				// scopes: all targets ("") when no --target, else each requested id.
				scopes := []string{""}
				if len(targets) > 0 {
					scopes = targets
				}
				for _, tid := range scopes {
					entries := lookup(installed, here, ref, tid, kind)
					if len(entries) == 0 {
						if tid == "" {
							_, _ = fmt.Fprintf(cmd.OutOrStdout(),
								"%s %q not installed (nothing to remove)\n", kindStr, ref)
						} else {
							_, _ = fmt.Fprintf(cmd.OutOrStdout(),
								"%s %q not installed for %s (nothing to remove)\n", kindStr, ref, tid)
						}
						continue
					}
					held := heldElsewhere(installed, here, entries)
					for _, e := range entries {
						if err := processEntry(e, held); err != nil {
							return err
						}
					}
				}
			}

			if flagDryRun {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[dry-run] no changes written")
				return nil
			}
			if err := store.SaveInstalled(installed); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("uninstall: save installed: %v", err)}
			}
			restart.flush(cmd.ErrOrStderr())
			return nil
		},
	}
	c.Flags().StringSliceVar(&flagTargets, "target", nil,
		"Limit uninstall to these tools, comma-separated or repeated (optional)")
	c.Flags().BoolVarP(&flagGlobal, "global", "g", false, "Uninstall from the tool's global config root")
	c.Flags().StringVar(&flagDest, "dir", "", "Project root directory (default: cwd)")
	c.Flags().BoolVar(&flagDryRun, "dry-run", false, "Print the removal plan, but change nothing")
	return c
}

// ---- update -----------------------------------------------------------------

func newPkgUpdateCmd(kind pkgKind) *cobra.Command {
	var (
		flagGlobal   bool
		flagDest     string
		flagConflict string
		flagVerbose  bool
		flagForce    bool
	)
	kindStr := string(kind)
	c := &cobra.Command{
		Use:        "update [<name@repo>...]",
		SuggestFor: []string{"upgrade"},
		Short:      fmt.Sprintf("Re-resolve and re-install %ss (all if no args)", kindStr),
		RunE: func(cmd *cobra.Command, args []string) (retErr error) {
			ctx := cmd.Context()

			// Root for re-install; uninstall uses same root derived from stored files.
			// For update we need a root — use $HOME when --global, --dir if set, else cwd.
			root, err := resolveRoot(flagGlobal, flagDest)
			if err != nil {
				return err
			}
			here := newLocation(flagGlobal, root)

			policy, err := conflict.Parse(flagConflict)
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: "update: " + err.Error()}
			}

			installed, err := store.LoadInstalled()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("update: load installed: %v", err)}
			}
			// A later refusal or failure must not leave files written by an
			// earlier ref or target untracked.
			dirty := false
			defer saveInstalledOnError(&retErr, &dirty, installed)

			repos, err := store.LoadRepos()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("update: load repos: %v", err)}
			}

			// One fetch cache for the whole invocation (see install).
			fetchCache := manager.NewFetchCache()

			// Accumulate ignore entries for re-projected files (an update may
			// introduce a new adapter dir/file the original install lacked).
			var ignoreEntries []string
			var restart opencodeRestart

			// Determine which refs to update.
			var targets []string
			if len(args) == 0 {
				// Collect all distinct refs of this kind at this location.
				seen := map[string]struct{}{}
				for _, e := range installed.Installed {
					if e.Kind == kindStr && here.holds(e) {
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

				entries := findInstalled(installed, here, ref, "", kind)
				if len(entries) == 0 {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %q not installed, skipping\n", kindStr, ref)
					continue
				}

				var plans []updatePlan
				for _, e := range entries {
					if rr.ResolvedSHA == e.ResolvedSHA {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(),
							"%s %s ← %s up to date (%s)\n", kindStr, ref, e.Target, shortSHA(rr.ResolvedSHA))
						continue
					}

					// Conflict resolution runs against OTHER refs' files at the
					// target (ownersAt excludes the same ref).
					pl, err := planUpdate(rr.StageDir, root, flagGlobal, kind, name, ref, e, installed, here, policy)
					if err != nil {
						return err
					}
					plans = append(plans, pl)
				}

				// Containment (old files and new writes) and unowned files
				// are decided for every target before anything is removed,
				// so a refusal keeps the old install and its record.
				if err := checkUpdatePlans(root, flagGlobal, flagForce, ref, plans, installed, here); err != nil {
					return err
				}

				for _, pl := range plans {
					e, resolved, actions := pl.entry, pl.resolved, pl.actions

					// Uninstall old files.
					skipped, err := manager.Uninstall(root, flagGlobal, e.Files)
					if err != nil {
						return &exit.CodedError{
							Code: exit.General,
							Msg:  fmt.Sprintf("update: uninstall old %s %s: %v", ref, e.Target, err),
						}
					}
					for _, s := range skipped {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "! skipped user-modified file: %s\n", s)
					}
					// A file the user edited stays as it is: its write is
					// dropped and its old record kept, so it stays tracked.
					resolved = slices.DeleteFunc(slices.Clone(resolved), func(w manager.PlannedWrite) bool {
						return slices.Contains(skipped, w.Path)
					})
					kept := slices.DeleteFunc(slices.Clone(e.Files), func(f store.FileRec) bool {
						return !slices.Contains(skipped, f.RelPath)
					})

					recs, err := manager.Commit(root, flagGlobal, resolved)
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

					collisionWarn(cmd.ErrOrStderr(), installed, here, ref, e.Target, recs)

					newEntry := store.InstalledEntry{
						Ref:         ref,
						Repo:        repoName,
						Name:        name,
						Kind:        kindStr,
						Target:      e.Target,
						Dir:         here.dir,
						Global:      here.global,
						ResolvedSHA: rr.ResolvedSHA,
						Files:       append(recs, kept...),
						InstalledAt: nowFn().UTC().Format(time.RFC3339),
					}
					upsertInstalled(installed, newEntry)
					dirty = true
					ignoreEntries = collectIgnoreEntries(ignoreEntries, recs)
					restart.note(e.Target, recs, e.Files)

					_, _ = fmt.Fprintf(cmd.OutOrStdout(),
						"✓ updated %s → %s (%s → %s, %d files)\n",
						ref, e.Target, shortSHA(e.ResolvedSHA), shortSHA(rr.ResolvedSHA), len(recs))
				}
			}

			if err := store.SaveInstalled(installed); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("update: save installed: %v", err)}
			}
			ensureProjectGitignore(cmd.ErrOrStderr(), root, flagGlobal, ignoreEntries)
			restart.flush(cmd.ErrOrStderr())
			return nil
		},
	}
	c.Flags().BoolVarP(&flagGlobal, "global", "g", false, "Update in the tool's global config root")
	c.Flags().StringVar(&flagDest, "dir", "", "Project root directory (default: cwd)")
	c.Flags().StringVar(&flagConflict, "conflict", "namespace",
		"Clash policy when another install owns a target path: namespace|skip|overwrite|refuse")
	c.Flags().BoolVar(&flagVerbose, "verbose", false, "Narrate per-repo clone + per-target projection progress")
	c.Flags().BoolVar(&flagForce, "force", false, "Replace files that already exist and ach-cli did not install")
	return c
}

// ---- outdated ---------------------------------------------------------------

// newPkgOutdatedCmd reports, read-only, whether each installed ref is behind its
// source. It re-resolves the latest SHA (like update) but writes nothing — no
// extract, no commit, no installed.json mutation.
func newPkgOutdatedCmd(kind pkgKind) *cobra.Command {
	var flagVerbose bool
	kindStr := string(kind)
	c := &cobra.Command{
		Use:   "outdated [<name@repo>...]",
		Short: fmt.Sprintf("Check installed %ss against their source (read-only; all if no args)", kindStr),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			installed, err := store.LoadInstalled()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("outdated: load installed: %v", err)}
			}
			repos, err := store.LoadRepos()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("outdated: load repos: %v", err)}
			}

			// Distinct refs of this kind, optionally filtered to the args.
			argSet := map[string]bool{}
			for _, a := range args {
				argSet[a] = true
			}
			var refs []string
			seen := map[string]bool{}
			for _, e := range installed.Installed {
				if e.Kind != kindStr || (len(argSet) > 0 && !argSet[e.Ref]) {
					continue
				}
				if !seen[e.Ref] {
					seen[e.Ref] = true
					refs = append(refs, e.Ref)
				}
			}
			if len(refs) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "no %ss installed\n", kindStr)
				return nil
			}

			fetchCache := manager.NewFetchCache()
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "REF\tTARGET\tLOCATION\tCURRENT\tLATEST\tSTATUS")

			for _, ref := range refs {
				atIdx := strings.LastIndex(ref, "@")
				if atIdx < 0 {
					return &exit.CodedError{Code: exit.General,
						Msg: fmt.Sprintf("outdated: %q: expected format <name@repo>", ref)}
				}
				name, repoName := ref[:atIdx], ref[atIdx+1:]

				repo, err := findRepo(repoName, repos)
				if err != nil {
					return err
				}
				lens := preferredLens(kind, repo.Provides)
				if lens == "" {
					return &exit.CodedError{Code: exit.General,
						Msg: fmt.Sprintf("outdated: repo %q does not provide %s or %s-marketplace", repoName, kindStr, kindStr)}
				}
				token, _ := store.LoadToken(repo.Name)
				vlogf(cmd.ErrOrStderr(), flagVerbose, "→ resolving %s (%s lens, repo %s)…\n", ref, lens, repo.Name)
				rr, err := manager.ResolveWithCache(ctx, repo, token, name, lens, fetchCache)
				if err != nil {
					return cloneExitErr("outdated resolve", err)
				}
				_ = os.RemoveAll(rr.StageDir) // read-only: only the SHA is needed

				for _, e := range installed.Installed {
					if e.Ref != ref || e.Kind != kindStr {
						continue
					}
					status := "up to date"
					if rr.ResolvedSHA != e.ResolvedSHA {
						status = "outdated"
					}
					_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
						ref, e.Target, locationLabel(e), shortSHA(e.ResolvedSHA), shortSHA(rr.ResolvedSHA), status)
				}
			}
			_ = tw.Flush()
			return nil
		},
	}
	c.Flags().BoolVar(&flagVerbose, "verbose", false, "Narrate per-repo resolution progress")
	return c
}

// ---- list -------------------------------------------------------------------

// locationLabel is the LOCATION column of list and outdated: the project
// directory, "global" for -g, or "-" when the record carries no location.
func locationLabel(e store.InstalledEntry) string {
	switch {
	case e.Global:
		return scopeGlobal
	case e.Dir != "":
		return e.Dir
	default:
		return "-"
	}
}

// repoRegistered reports whether a local repo of that name is registered; an
// unreadable repos.json counts as registered so it never masks real output.
func repoRegistered(name string) bool {
	repos, err := store.LoadRepos()
	if err != nil {
		return true
	}
	return slices.ContainsFunc(repos.Repos, func(r store.RepoEntry) bool { return r.Name == name })
}

func newPkgListCmd(kind pkgKind) *cobra.Command {
	var flagRepo string
	kindStr := string(kind)
	c := &cobra.Command{
		Use:   "list",
		Short: fmt.Sprintf("Show installed %ss (from installed.json)", kindStr),
		Long: fmt.Sprintf(`Show installed %ss.

Lists only locally-installed items recorded in installed.json. To see
all available %ss in a remote repo catalog, you would need to re-clone
the repo (not supported in v1 list — run 'ach-cli local repo update' to refresh).
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
				if flagRepo != "" && !repoRegistered(flagRepo) {
					return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf(
						"repo %q is not registered; `ach-cli local repo list` shows the registered ones", flagRepo)}
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "no %ss installed\n", kindStr)
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "REF\tTARGET\tLOCATION\tSHA\tINSTALLED_AT")
			for _, e := range rows {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					e.Ref, e.Target, locationLabel(e), shortSHA(e.ResolvedSHA), e.InstalledAt)
			}
			_ = w.Flush()
			return nil
		},
	}
	c.Flags().StringVar(&flagRepo, "repo", "", "Filter installed items by repo name")
	return c
}

// pluginPrefix is the namespace a local install gives a component: a plugin's
// commands and skills carry its name, a standalone skill keeps its own.
func pluginPrefix(kind pkgKind, name string) string {
	if kind == kindPlugin {
		return name
	}
	return ""
}
