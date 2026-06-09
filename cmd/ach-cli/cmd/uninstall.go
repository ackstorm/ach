// SPDX-License-Identifier: Apache-2.0

// `ach-cli env uninstall` (LIFE-01, D-25/D-27/D-28/D-29) is a THIN cobra leaf
// that tears down the active workspace+environment projection by reusing
// the existing hydrate.Sync inverse-merge engine — it creates NO second
// deletion path.
//
// Flow:
//  1. Reject the --include-runtime + --only-runtime clash (D-26).
//  2. Resolve the <ach-dir>/state.json scope exactly as the hydrate
//     orchestrator does (state.ResolvePath; achDir=dir(statePath);
//     toolRoot=workspaceCwd, $HOME under --global).
//  3. Load prev state. Missing → "nothing installed", exit 0.
//  4. Acquire the <ach-dir>/lock flock (fail-fast, or timeout under
//     --lock-timeout), mirroring commit.go:step1Lock. Serializes against
//     concurrent ach-cli mutation (T-04-06).
//  5. Build the scope-filtered survivor File (hydrate.BuildScopedEmpty)
//     and feed it to Sync as the set-difference target. Sync owns the
//     entire inverse model: deepest-first delete, deep-key subtraction,
//     composite marker removal, and the drift-wins gate that preserves
//     user-edited co-owned files unless --force (T-04-02/T-04-03).
//  6. State cleanup (D-28): --dry-run writes nothing; a full teardown
//     removes state.json; a scoped uninstall rewrites it via the atomic
//     state.Save to retain the un-removed rows (T-04-04).
//
// Every failure is wrapped in *exit.CodedError; the leaf never os.Exit.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/lock"
	"github.com/ackstorm/ach/internal/cli/state"
)

// uninstallSyncFn is the engine-dispatch test seam. Production callers
// inherit hydrate.Sync; unit tests swap it for a fake that records the
// (prev, scopedEmpty) inputs without touching disk. Mirrors
// hydrate.go's hydrateRunFn / commit.go's syncFn convention.
var uninstallSyncFn = hydrate.Sync

// uninstallInputs is the resolved-flag snapshot the RunE body consumes,
// keeping the flow flat (low cyclomatic complexity).
type uninstallInputs struct {
	environment    string
	force          bool
	dryRun         bool
	global         bool
	platform       string
	lockTimeout    time.Duration
	includeRuntime bool
	onlyRuntime    bool
	output         string
}

// newUninstallCmd returns a fresh `ach-cli env uninstall` cobra.Command.
// Factory shape matches the other ach-cli leaves so tests construct an
// isolated tree per t.Run.
func newUninstallCmd() *cobra.Command {
	var (
		flagForce          bool
		flagDryRun         bool
		flagGlobal         bool
		flagTarget         string
		flagLockTimeout    time.Duration
		flagIncludeRuntime bool
		flagOnlyRuntime    bool
		flagOutput         string
	)

	cmd := &cobra.Command{
		Use:   "uninstall <name>",
		Args:  cobra.ExactArgs(1),
		Short: "Remove the projected resource set for the active workspace+environment",
		Long: `Tear down the projected resources ach-cli env hydrate installed for the
active workspace+environment, reusing the same inverse-merge engine that
hydrate --sync uses. uninstall removes the WHOLE projection in scope
(no per-plugin selection, D-25); preview with --dry-run.

The positional <name> is the target Environment — REQUIRED, it namespaces
the <ach-dir> in project and --global scope.

Scope (mirrors hydrate, D-26):
  (default)           Remove context resources only (prompts / plugins /
                      artifacts), leaving runtime config in place.
  --include-runtime   Also strip runtime config (models / mcpServers).
  --only-runtime      Strip ONLY runtime config (mutually exclusive with
                      --include-runtime).

Co-owned files (.mcp.json, .codex/config.toml, .opencode/opencode.json,
CLAUDE.md / GEMINI.md) are inverse-merged: only the engine's keys/marker
blocks are removed; other contributors' and the user's keys survive.
User-edited projected files are preserved unless --force (drift-wins).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(cmd, uninstallInputs{
				environment:    args[0],
				force:          flagForce,
				dryRun:         flagDryRun,
				global:         flagGlobal,
				platform:       flagTarget,
				lockTimeout:    flagLockTimeout,
				includeRuntime: flagIncludeRuntime,
				onlyRuntime:    flagOnlyRuntime,
				output:         flagOutput,
			})
		},
	}

	// Reused hydrate flags (D-29) plus the D-26 scope pair.
	cmd.Flags().BoolVar(&flagForce, "force", false,
		"Bypass drift refusal — remove user-edited projected files too")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false,
		"Print planned removals but write nothing to disk")
	cmd.Flags().BoolVar(&flagGlobal, "global", false,
		"Use $HOME/.ach/<env> scope instead of cwd/.ach")
	cmd.Flags().StringVar(&flagTarget, "target", "",
		"Override platform autodetection (claude-code / codex / gemini-cli / opencode / pimono + case-folded aliases)")
	cmd.Flags().DurationVar(&flagLockTimeout, "lock-timeout", 0,
		"Wait up to <d> for the workspace lock instead of failing fast")
	cmd.Flags().BoolVar(&flagIncludeRuntime, "include-runtime", false,
		"Also strip runtime config (models / mcpServers) alongside context")
	cmd.Flags().BoolVar(&flagOnlyRuntime, "only-runtime", false,
		"Strip ONLY runtime config (mutually exclusive with --include-runtime)")
	cmd.Flags().StringVar(&flagOutput, "output", "",
		"Workspace root override (default: cwd)")

	return cmd
}

// runUninstall is the RunE body kept deliberately thin: it owns scope /
// lock / state-cleanup orchestration only — all removal logic lives in
// hydrate.Sync.
func runUninstall(cmd *cobra.Command, in uninstallInputs) error {
	// (a) D-26 scope-flag mutual exclusion.
	if in.includeRuntime && in.onlyRuntime {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "--include-runtime and --only-runtime are mutually exclusive",
		}
	}

	// (b) Resolve scope exactly as commit.go:newCommit.
	workspaceCwd := in.output
	if workspaceCwd == "" && !in.global {
		wd, err := os.Getwd()
		if err != nil {
			return &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("resolve working directory: %v", err),
				Wrapped: err,
			}
		}
		workspaceCwd = wd
	}
	base, err := state.ResolvePath(workspaceCwd, in.environment, in.global)
	if err != nil {
		return &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("resolve <ach-dir>: %v", err),
			Wrapped: err,
		}
	}
	achDir := filepath.Dir(base)
	toolRoot := workspaceCwd
	if in.global {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("resolve $HOME for --global scope: %v", herr),
				Wrapped: herr,
			}
		}
		toolRoot = home
	}

	// (c) Enumerate every state file for the env — one per-platform
	// state-<platform>.json per hydrated target (+ a legacy state.json). Missing
	// → nothing installed, clean exit 0.
	statePaths, err := state.ListStatePaths(workspaceCwd, in.environment, in.global)
	if err != nil {
		return &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("enumerate state files: %v", err),
			Wrapped: err,
		}
	}
	if len(statePaths) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "nothing installed; no state.json found")
		return nil
	}

	// (d) Acquire the env lock ONCE (achDir is shared across the env's
	// per-platform states) mirroring commit.go:step1Lock.
	lease, err := acquireUninstallLock(cmd, achDir, in.lockTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = lease.Release() }()

	// (e-g) Tear down each platform's state in turn: scope-filtered survivor set
	// → inverse-merge teardown → state cleanup. Shared workspace content (prompts/
	// artifacts) removed by the first state is a graceful no-op for the rest.
	var totalPruned, totalPreserved int
	for _, sp := range statePaths {
		prev, lerr := state.Load(sp)
		if lerr != nil {
			return &exit.CodedError{
				Code:    exit.ConfigFile,
				Msg:     fmt.Sprintf("read %s: %v", filepath.Base(sp), lerr),
				Wrapped: lerr,
			}
		}
		if prev == nil {
			continue
		}
		scopedEmpty := hydrate.BuildScopedEmpty(prev, in.includeRuntime, in.onlyRuntime)
		stats, serr := uninstallSyncFn(prev, scopedEmpty, achDir, toolRoot, hydrate.SyncOptions{
			Force:  in.force,
			Stderr: cmd.ErrOrStderr(),
			// CR-01: under --dry-run the engine must classify only and
			// mutate nothing. SyncOptions.DryRun gates every os.Remove /
			// WriteAtomic / dir-prune inside Sync; the !in.dryRun guard
			// below covers only the state.json write.
			DryRun: in.dryRun,
		})
		if serr != nil {
			return &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("uninstall: %v", serr),
				Wrapped: serr,
			}
		}
		if !in.dryRun {
			if cerr := cleanupState(sp, scopedEmpty); cerr != nil {
				return cerr
			}
		}
		totalPruned += stats.Pruned
		totalPreserved += stats.Preserved
	}

	// (h) Stats summary.
	dryRunSuffix := ""
	if in.dryRun {
		dryRunSuffix = " (dry-run: nothing written)"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"uninstall: pruned %d, preserved %d%s\n", totalPruned, totalPreserved, dryRunSuffix)
	return nil
}

// acquireUninstallLock mirrors commit.go:step1Lock: ensure <ach-dir>
// exists, then flock fail-fast (or timeout under --lock-timeout).
func acquireUninstallLock(cmd *cobra.Command, achDir string, lockTimeout time.Duration) (lock.Lease, error) {
	if err := os.MkdirAll(achDir, 0o755); err != nil {
		return nil, &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("create <ach-dir>: %v", err),
			Wrapped: err,
		}
	}
	locker := lock.NewLocker(lock.Path(achDir))
	mode := lock.AcquireFailFast
	timeout := time.Duration(0)
	if lockTimeout > 0 {
		mode = lock.AcquireWithTimeout
		timeout = lockTimeout
	}
	lease, err := locker.Acquire(cmd.Context(), mode, timeout)
	if err != nil {
		return nil, &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("acquire workspace lock: %v", err),
			Wrapped: err,
		}
	}
	return lease, nil
}

// cleanupState applies D-28: a full teardown (scopedEmpty has zero
// entries across every bucket) removes state.json so a subsequent `list`
// shows nothing; a scoped uninstall rewrites state retaining the
// survivor rows via the atomic state.Save. os.Remove tolerates an
// already-absent file.
func cleanupState(statePath string, scopedEmpty *state.File) error {
	if len(state.WalkEntries(scopedEmpty)) == 0 {
		if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
			return &exit.CodedError{
				Code:    exit.ConfigFile,
				Msg:     fmt.Sprintf("remove state.json after full teardown: %v", err),
				Wrapped: err,
			}
		}
		return nil
	}
	if err := state.Save(statePath, scopedEmpty); err != nil {
		return &exit.CodedError{
			Code:    exit.ConfigFile,
			Msg:     fmt.Sprintf("rewrite state.json after scoped uninstall: %v", err),
			Wrapped: err,
		}
	}
	return nil
}
