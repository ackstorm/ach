// SPDX-License-Identifier: Apache-2.0

// `ach-cli env hydrate` ships in two dispatch modes per the Phase 7 W3-05
// refactor (D-03 + D-04):
//
//   - Engine path (default) — invokes the full
//     internal/cli/hydrate.Run(ctx, Opts) 14-step commit sequence:
//     workspace lock, state.json v2 + drift, manifest fetch, safe
//     extract + auto-claim cascade, adapter dispatch, atomic state
//     write. Engine flags exposed: --only-runtime / --sync / --force /
//     --dry-run / --wait / --lock-timeout / --dir / --allow-symlinks /
//     --target / --global.
//
//   - --raw (hidden) — preserves the Phase 6 surface-only POST+stream
//     contract so the W3-P3 e2e golden-diff anchor (`examples/hydrate
//     .json`) keeps passing byte-for-byte. The flag is registered then
//     hidden via cmd.Flags().MarkHidden("raw") so --help does not
//     advertise it. The 07-W4-02 e2e test (not this plan) updates the
//     golden-diff caller to pass --raw.
//
// D-04 verbatim: `--raw` short-circuits BEFORE any engine call so the
// byte-equal POST+stream behavior survives. The legacy runHydrateRaw
// function is the Phase 6 body extracted verbatim; the new runHydrate
// dispatcher snapshots inputs and routes via flagRaw.
//
// Decisions baked in (Phase 6 set, preserved through the engine path
// too):
//   - D-09: surface-only `--raw` path — no on-disk write, no diff.
//   - D-10: stderr §6.6 pk- advisory (pk- is not Environment-scoped) —
//     on the --raw path it is emitted AFTER successful output so it does
//     not bury the streamed bytes; the engine path folds the same guidance
//     into the summary Tips footer. Suppressed by --no-warnings in both.
//   - Credentials come from resolveCred (--profile / --key / ACH_KEY).
//   - D-12: <name> positional argument REQUIRED for pk-; OPTIONAL for ek-.
//   - D-15: --verbose dumps a redacted header set to stderr.
//
// Adapter registration: the 4 platform adapters
// (claudecode/codex/gemini/opencode) self-register via init() side
// effects in their subpackages; the blank-imports live in
// adapters_register.go in this package so this file stays focused on
// cobra wiring + dispatch.

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/conflict"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/synthetic"
	"github.com/ackstorm/ach/internal/keys"
)

// pkWarning is the pk- credential advisory for the --raw path, which has no
// success summary to host it. The engine path folds the same guidance into its
// first-run hints instead (see hydrateHints), so this const is now emitted
// only when --raw short-circuits the engine. Gated by
// --no-warnings in both paths. The trailing newline is part of the const so
// Fprint composes cleanly.
const pkWarning = "warning: pk- is not Environment-scoped; use an ek- key for " +
	"Environment-bound workloads (CI / agents)\n"

// hydrateHTTPClient is a test-only seam: when non-nil it replaces the
// default *http.Client inside the httpclient.Client built by hydrate.
// Production callers leave this nil and inherit the foundation
// 60s-timeout default. Tests targeting an httptest.NewTLSServer set
// this to the test server's TLS-trusting Client so https://127.0.0.1
// with an ephemeral cert is reachable.
var hydrateHTTPClient *http.Client

// hydrateRunFn is the engine-dispatch test seam. Production callers
// leave the default (= hydrate.Run); unit tests targeting the
// engine path can swap it for a fake that records the Opts struct
// without spinning up a full lock + state.json + manifest fetch
// pipeline. Mirrors the swapHTTPClientForTest seam.
var hydrateRunFn = hydrate.Run

// newHydrateCmd returns a fresh `ach-cli env hydrate` cobra.Command. Factory
// shape matches login/whoami/logout so tests can construct an isolated
// tree per t.Run.
func newHydrateCmd() *cobra.Command {
	var (
		// Phase 6 surface — preserved.
		flagNoWarnings bool
		flagInsecure   bool
		flagCred       credFlags

		// Phase 7 engine flags (D-03).
		flagOnlyRuntime   bool
		flagSync          bool
		flagForce         bool
		flagDryRun        bool
		flagWait          bool
		flagLockTimeout   time.Duration
		flagDir           string
		flagAllowSymlinks bool
		flagTarget        []string
		flagGlobal        bool
		flagConflict      string
		flagOnly          string
		flagModels        bool

		// D-04 hidden flag — surface preserved for the W3-P3 golden-
		// diff anchor.
		flagRaw bool
	)

	cmd := &cobra.Command{
		Use:   "hydrate [env]",
		Args:  cobra.MaximumNArgs(1),
		Short: "Install an Environment's content and MCP/A2A config into your agent tool",
		Long: `Download an environment's content (prompts, skills, artifacts) into your
agent tool's config directory, and wire up its runtime (MCP / A2A; models
only with --models).

Scope:
  (default)           Project context (prompts / plugins / artifacts /
                      skills) AND runtime (MCP servers / A2A agents).
  --only-runtime      Project ONLY runtime entries (excludes context).
  --only <kind>/<name>
                      Install just one plugin or skill (plugin/<name> or
                      skill/<name>) and merge it into the existing state.
                      No runtime is written; needs an environment and
                      cannot be combined with --sync, --only-runtime.
  --models            Also route the tool's model traffic through ACH
                      (Claude Code apiKeyHelper + ANTHROPIC_BASE_URL, Codex
                      model provider; needs ach-cli on PATH). Off by
                      default: the tool keeps its own model login.

Behavior:
  --sync              Remove files no longer in the environment.
  --force             Overwrite local edits and bypass safety refusals.
  --dry-run           Show what would change without writing anything.
  --allow-symlinks    Permit symlinks in downloaded archives (unsafe).
  --conflict <p>      Two plugins writing the same file: namespace (default,
                      prefix with the plugin name) | skip | overwrite | refuse.

Locking:
  --wait              Wait indefinitely if another hydrate holds the lock.
  --lock-timeout <d>  Wait up to <d> (e.g. 30s, 5m). Conflicts with --wait.

Location:
  --dir <dir>         Workspace root (default: current directory).
  -g, --global        Hydrate under $HOME/.ach/<env> instead of ./.ach.
  --target <id[,id…]> Agent target(s): claude-code / codex / gemini-cli /
                      opencode / pi (comma-separated or repeated;
                      ACH_TARGET is the same). Omitted: autodetected from
                      the workspace.

Credentials:
  --key <name|ek-…>   A key saved in the profile, or a raw ek-… key
                      (ACH_KEY is the same). Otherwise the profile's own
                      session (ach-cli login) is used.

The positional <name> is the environment; with a key it is optional (the
key is already scoped to one environment). Without <name>, every environment
already hydrated in this folder (.ach/) is updated with its recorded targets.

Example:
  ach-cli env hydrate frontend-dev --only plugin/code-review -g --target claude-code

Exit codes:
  0 success   1 usage/credential error   2 local edits would be lost
  3 not authorized   4 wrong environment for this key   5 state mismatch
  6 network error   7 file-name collision   8 config read/write error`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conflict, err := conflict.Parse(flagConflict)
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: err.Error()}
			}
			var only *hydrate.Item
			if flagOnly != "" {
				if only, err = hydrate.ParseItem(flagOnly); err != nil {
					return &exit.CodedError{Code: exit.General, Msg: err.Error()}
				}
			}
			env := ""
			if len(args) > 0 {
				env = args[0]
			}
			return runHydrate(cmd, hydrateInputs{
				environment:   env,
				noWarnings:    flagNoWarnings,
				verbose:       flagCred.Verbose,
				insecure:      flagInsecure,
				cred:          flagCred,
				onlyRuntime:   flagOnlyRuntime,
				sync:          flagSync,
				force:         flagForce,
				dryRun:        flagDryRun,
				wait:          flagWait,
				lockTimeout:   flagLockTimeout,
				output:        flagDir,
				allowSymlinks: flagAllowSymlinks,
				platform:      flagTarget,
				global:        flagGlobal,
				conflict:      conflict,
				raw:           flagRaw,
				only:          only,
				models:        flagModels,
			})
		},
	}

	// Phase 6 surface flags — preserved.
	cmd.Flags().BoolVar(&flagNoWarnings, "no-warnings", false,
		"Suppress the tip to use an environment key (ek-) instead of your sign-in")
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false,
		"Allow a plaintext http:// Hub URL (credentials sent unencrypted; localhost still requires this)")
	registerCredFlags(cmd, &flagCred)

	// Phase 7 engine flags (D-03).
	cmd.Flags().BoolVar(&flagOnlyRuntime, "only-runtime", false,
		"Reconcile ONLY runtime entries (skip context)")
	cmd.Flags().BoolVar(&flagSync, "sync", false,
		"Delete state entries no longer in the environment (deepest-first)")
	cmd.Flags().BoolVar(&flagForce, "force", false,
		"Bypass drift refusal, environment guard, and schema mismatch")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false,
		"Run read+diff steps but skip state write and content extract")
	cmd.Flags().BoolVar(&flagWait, "wait", false,
		"Block indefinitely on workspace lock contention")
	cmd.Flags().DurationVar(&flagLockTimeout, "lock-timeout", 0,
		"Wait up to <d> for workspace lock (mutually exclusive with --wait)")
	cmd.Flags().StringVar(&flagDir, "dir", "",
		"Workspace root override (default: cwd)")
	cmd.Flags().BoolVar(&flagAllowSymlinks, "allow-symlinks", false,
		"Permit symlinks in downloaded archives (unsafe)")
	cmd.Flags().StringSliceVar(&flagTarget, "target", nil,
		"Override platform autodetection; "+
			"comma-separated or repeated for several targets, e.g. codex,opencode "+
			"(claude-code / codex / gemini-cli / opencode / pi + case-folded aliases)")
	cmd.Flags().BoolVarP(&flagGlobal, "global", "g", false,
		"Use $HOME/.ach/<env> scope instead of cwd/.ach")
	cmd.Flags().StringVar(&flagConflict, "conflict", "namespace",
		"Cross-plugin collision policy: namespace|skip|overwrite|refuse")
	cmd.Flags().StringVar(&flagOnly, "only", "",
		"Install just one item, plugin/<name> or skill/<name>, merging it into the existing state")
	cmd.Flags().BoolVar(&flagModels, "models", false,
		"Route the tool's model traffic through ACH (apiKeyHelper / model provider wiring)")

	// D-04 hidden: --raw preserves the Phase 6 POST+stream byte-for-byte
	// contract. Hidden so --help advertises only the engine surface.
	cmd.Flags().BoolVar(&flagRaw, "raw", false,
		"(hidden) Phase 6 surface-only POST+stream — preserved for W3-P3 e2e golden-diff anchor")
	if err := cmd.Flags().MarkHidden("raw"); err != nil {
		// MarkHidden returns an error only when the flag is not
		// registered — defensive panic to surface a future refactor
		// that breaks the registration order.
		panic(fmt.Sprintf("MarkHidden(raw) failed: %v", err))
	}

	return cmd
}

// hydrateInputs is the resolved flag + env snapshot used by every step
// of runHydrate. Centralizing the read-once-via-os.Getenv discipline
// here keeps the flow function flat (low cyclomatic complexity) and
// makes the closed-list nature of D-11 visible at the field level.
type hydrateInputs struct {
	environment string
	noWarnings  bool
	verbose     bool
	insecure    bool
	cred        credFlags

	// Phase 7 engine fields (D-03).
	onlyRuntime   bool
	sync          bool
	force         bool
	dryRun        bool
	wait          bool
	lockTimeout   time.Duration
	output        string // --dir
	allowSymlinks bool
	platform      []string // --target
	global        bool
	conflict      conflict.Policy
	only          *hydrate.Item // --only
	models        bool          // --models

	// D-04 hidden raw flag.
	raw bool

	envEnvironment string
	envTarget      string

	// cloneDir is set by `env clone`: adds the "cd <dir>" update hint.
	cloneDir string
}

// runHydrate is the RunE body. Flow:
//
//  1. Read env-var snapshot into the inputs struct.
//  2. (credential sources are resolved by resolveCred in step 4).
//  3. assertScopeFlags — mutual exclusion of --wait / --lock-timeout.
//  4. Resolve credential (synthetic OR config-disk path).
//  5. D-12 pk-/<name> positional argument gate.
//  6. plaintext-transport warning if http://.
//  7. Dispatch: flagRaw → runHydrateRaw (Phase 6 verbatim);
//     otherwise → runHydrateEngine (full 14-step commit sequence).
//  8. Emit the pk- warning after successful output so it does not bury the
//     hydrate summary.
func runHydrate(cmd *cobra.Command, in hydrateInputs) error {
	in.envEnvironment = os.Getenv("ACH_ENVIRONMENT")
	in.envTarget = os.Getenv("ACH_TARGET")

	// Phase 7 flag mutual exclusion: --wait + --lock-timeout. Both dispatch
	// modes run this.
	//
	// NOT covered: --raw + engine flags. A --raw + --only-runtime combo is
	// incoherent (no projection on the raw response surface), but it is NOT
	// rejected — runHydrateRaw takes no scope param, so the flag is
	// silently ignored on the raw path. Deliberate (#85): --raw is hidden
	// (MarkHidden, D-04) and exists only as the frozen Phase 6 byte-for-byte
	// golden-diff anchor; the only callers are e2e tests, none of which pass an
	// engine flag alongside it. A guard here would be unreachable, and the raw
	// path is deliberately frozen. If --raw is ever un-hidden, add the check.
	if err := assertScopeFlags(in); err != nil {
		return err
	}

	in.cred.Insecure = in.insecure
	c, err := resolveCred(cmd.Context(), in.cred, synthetic.GateAPI)
	if err != nil {
		return err
	}
	baseURL, bearer := c.BaseURL, c.Bearer

	// D-12: pk- classification + <name> positional argument gate.
	prefix, classifyErr := classifyBearer(bearer)
	if classifyErr != nil {
		return &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("invalid credential format: %v", classifyErr),
			Wrapped: classifyErr,
		}
	}
	effectiveEnv := in.environment
	if effectiveEnv == "" {
		effectiveEnv = in.envEnvironment
	}
	// --raw has no workspace path and no per-env state namespacing;
	// a pk- key additionally needs the env name to scope the verbatim stream
	// (an ek- binds its own Environment, so ek-/--raw/empty stays allowed).
	// The non-raw (engine) path no longer errors on an empty env here: when
	// BOTH the positional <name> and ACH_ENVIRONMENT are absent it falls
	// through to runHydrateWorkspace, which re-hydrates every env recorded
	// under .ach/ — and emits its own required-arg error when there is none.
	if in.raw && prefix == keys.PrefixPk && effectiveEnv == "" {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "<name> positional argument is required when using a pk- key (CLI-06 / spec §5.7)",
		}
	}

	// G19: refuse a plaintext http:// Hub URL (from the profile or ACH_URL)
	// unless the user opted into insecure transport (--insecure
	// flag OR ACH_INSECURE env). localhost is NOT exempt (decision B). This
	// runs BEFORE any engine call so no credential leaves over plaintext.
	if err := config.ValidateSecureURL(baseURL, in.insecure || config.InsecureFromEnv()); err != nil {
		return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}

	// D-04 dispatch. --raw short-circuits BEFORE any engine call so
	// the W3-P3 golden-diff anchor survives byte-for-byte.
	var runErr error
	if in.raw {
		runErr = runHydrateRaw(cmd, baseURL, bearer, effectiveEnv, in.verbose)
		// --raw has no success summary to host advisories; keep the pk- tip on
		// stderr. The engine path folds the same guidance into its first-run
		// hints (hydrateHints), so it is emitted there, not here.
		if runErr == nil && prefix == keys.PrefixPk && !in.noWarnings {
			_, _ = fmt.Fprint(cmd.ErrOrStderr(), pkWarning)
		}
	} else if effectiveEnv != "" {
		runErr = runHydrateEngine(cmd, in, baseURL, bearer, effectiveEnv)
	} else {
		runErr = runHydrateWorkspace(cmd, in, baseURL, bearer)
	}
	return runErr
}

// assertScopeFlags enforces the flag mutual exclusions:
//
//   - --wait + --lock-timeout → exit 1.
//   - --only + --sync / --only-runtime / --raw, or --only without an
//     environment → exit 1.
//
// The rejection cites the offending flag pair in the error message so
// the user can pick one and re-run.
func assertScopeFlags(in hydrateInputs) error {
	if in.only != nil {
		for _, c := range []struct {
			set  bool
			flag string
		}{{in.sync, "--sync"}, {in.onlyRuntime, "--only-runtime"}, {in.raw, "--raw"}} {
			if c.set {
				return &exit.CodedError{Code: exit.General, Msg: "--only and " + c.flag + " are mutually exclusive"}
			}
		}
		if in.environment == "" && in.envEnvironment == "" {
			return &exit.CodedError{Code: exit.General,
				Msg: "--only needs an environment: ach-cli env hydrate <env> --only <kind>/<name>"}
		}
	}
	if in.wait && in.lockTimeout > 0 {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "--wait and --lock-timeout are mutually exclusive",
		}
	}
	return nil
}

// runHydrateEngine builds the hydrate.Opts struct and dispatches to
// hydrateRunFn (= hydrate.Run by default). Platform resolution:
//   - --target set → hydrate.ResolvePlatform(value) → canonical id.
//   - ACH_TARGET set → hydrate.ResolvePlatform(value) → canonical id.
//   - else → hydrate.Autodetect against cwd (workspace) OR
//     os.UserHomeDir() (global).
//
// The engine wiring (Extractor + AdapterDispatcher) is provided by
// hydrate.NewWiring; commit.go's newCommit consumes them via interface
// fields. NB: until commit.go's run() actually calls
// extractor/adapter (W1 stubs), the wiring is wired-but-unused — the
// W1 → W3 contract is that this plan makes them available, the
// orchestrator's step 7+10 wiring lights them up.
//
// See internal/cli/hydrate/commit.go for the orchestrator's W1-stub
// short-circuit behavior; both impls are passed regardless so a future
// commit.go uplink does not need a cobra-layer change.
func runHydrateEngine(cmd *cobra.Command, in hydrateInputs, baseURL, bearer, effectiveEnv string) error {
	platformIDs, err := resolvePlatformsOrAutodetect(in, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	meta := newSummaryMeta(in, bearer)
	u := newUI(cmd.OutOrStdout(), in.verbose)
	hydrateHeader(u, effectiveEnv, platformIDs, meta)

	// The engine's notes/warnings are held back and re-emitted after the
	// Finished line (--verbose streams them live: they are progress there).
	var engineErr bytes.Buffer
	var stderr io.Writer = &engineErr
	if in.verbose {
		stderr = cmd.ErrOrStderr()
	}
	start := time.Now()
	results, err := hydrateEnv(cmd, in, baseURL, bearer, effectiveEnv, platformIDs, stderr)
	if err != nil {
		_, _ = engineErr.WriteTo(cmd.ErrOrStderr())
		return err
	}
	renderHydrate(u, newUI(cmd.ErrOrStderr(), in.verbose), results, meta, time.Since(start), &engineErr)
	return nil
}

// hydrateEnv runs the engine for one Environment across platformIDs and
// returns one Result per target. Fail-fast: a failing target returns
// immediately (earlier targets already committed).
func hydrateEnv(cmd *cobra.Command, in hydrateInputs, baseURL, bearer, effectiveEnv string,
	platformIDs []string, stderr io.Writer) ([]hydrate.Result, error) {
	// Classify the bearer once: drives the pk- x-ach-environment header below.
	// A classify error leaves the prefix zero — nothing acts on it.
	bearerPrefix, _ := classifyBearer(bearer)

	limits, err := extract.LoadLimits()
	if err != nil {
		return nil, &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("load bomb-defense limits: %v", err),
			Wrapped: err,
		}
	}

	hc := newAPIClient(baseURL, bearer, hydrateHTTPClient, in.verbose, cmd.ErrOrStderr())

	// CLI-03: pk- content GETs must carry the target Environment in an
	// x-ach-environment header so the Content Service can resolve scope
	// (resolveEnv in internal/contentservice/authz.go returns 400
	// missing_environment for a pk- request without it). An ek- binds its
	// own Environment, so the header is omitted — see the credential-
	// agnostic contract documented on internal/cli/extract.FetchContent.
	// The surface manifest POST carries the Environment in its body; only
	// these per-artifact content GETs rely on the header. effectiveEnv is
	// guaranteed non-empty for pk- by the D-12 gate in runHydrate.
	if bearerPrefix == keys.PrefixPk {
		// Security 2.10 (defense-in-depth): reject CRLF / NUL / control bytes
		// in the env name before assigning to a header value. The Go stdlib's
		// http.Transport already rejects these at write time, but failing
		// early gives a clean error envelope instead of a transport panic.
		if err := validateEnvHeaderValue(effectiveEnv); err != nil {
			return nil, &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("invalid environment %q: %v", effectiveEnv, err),
			}
		}
		hc.ExtraHeaders = http.Header{"x-ach-environment": {effectiveEnv}}
	}

	// --target accepts a comma-separated list (mirroring local `plugin install
	// --target a,b`); hc + limits above are platform-independent and shared.
	results := make([]hydrate.Result, 0, len(platformIDs))
	for _, platformID := range platformIDs {
		// NewWiring constructs the default Extractor + AdapterDispatcher;
		// both are threaded into Opts so commit.run()'s steps 7-10 invoke
		// real impls (07-W5-01 gap closure).
		ext, ad := hydrate.NewWiring(hc, platformID, limits, in.allowSymlinks, in.force, in.global, in.conflict)

		opts := hydrate.Opts{
			Environment:       effectiveEnv,
			Platform:          platformID,
			Global:            in.global,
			OnlyRuntime:       in.onlyRuntime,
			Sync:              in.sync,
			Force:             in.force,
			Conflict:          in.conflict,
			Only:              in.only,
			Models:            in.models,
			DryRun:            in.dryRun,
			AllowSymlinks:     in.allowSymlinks,
			Output:            in.output,
			Wait:              in.wait,
			LockTimeout:       in.lockTimeout,
			BaseURL:           baseURL,
			Bearer:            bearer,
			Verbose:           in.verbose,
			Stdout:            cmd.OutOrStdout(),
			Stderr:            stderr,
			Extractor:         ext,
			AdapterDispatcher: ad,
		}

		res, err := hydrateRunFn(cmd.Context(), opts)
		if err != nil {
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

// runHydrateWorkspace drives the bare `env hydrate` (no positional <name>, no
// ACH_ENVIRONMENT): it re-hydrates every environment already recorded under
// <root>/.ach/ (what `env clone` or an earlier `env hydrate` left behind), each
// with its recorded targets, best-effort (a failing env is recorded and the
// run continues), one row per env. Exits non-zero if any env failed.
func runHydrateWorkspace(cmd *cobra.Command, in hydrateInputs, baseURL, bearer string) error {
	root := in.output
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
		}
		root = wd
	}
	envs, err := hydratedEnvs(root)
	if err != nil {
		return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}
	if len(envs) == 0 {
		return &exit.CodedError{
			Code: exit.General,
			Msg: "nothing hydrated in this folder yet: pass <name> (or set " +
				"ACH_ENVIRONMENT), or create the folder with `ach-cli env clone <name>`",
		}
	}

	u := newUI(cmd.OutOrStdout(), in.verbose)
	u.verb("Hydrating", "%s from .ach/", countNoun(len(envs), "environment", "environments"))
	var notes bytes.Buffer
	start := time.Now()
	failed := 0
	for _, e := range envs {
		perEnv := in
		// Target precedence: --target flag and ACH_TARGET both override the
		// recorded targets; those only fill in when neither is set.
		if len(perEnv.platform) == 0 && perEnv.envTarget == "" && len(e.targets) > 0 {
			perEnv.platform = e.targets
		}
		var results []hydrate.Result
		platformIDs, runErr := resolvePlatformsOrAutodetect(perEnv, &notes)
		if runErr == nil {
			results, runErr = hydrateEnv(cmd, perEnv, baseURL, bearer, e.name, platformIDs, &notes)
		}
		changes := mergedChanges(results)
		switch {
		case runErr != nil:
			failed++
			lines := strings.Split(runErr.Error(), "\n")
			u.verb(e.name, "%s %s", u.paint(red, "error"), lines[0])
			for _, l := range lines[1:] {
				u.verb("", "%s", l)
			}
		case len(changes) == 0:
			u.verb(e.name, "up to date")
		default:
			u.items(e.name, changes)
		}
	}
	u.verb("Finished", "%d of %d in %s", len(envs)-failed, len(envs), elapsed(time.Since(start)))
	newUI(cmd.ErrOrStderr(), in.verbose).relay(&notes)
	if failed > 0 {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("%d of %d environment(s) failed to hydrate", failed, len(envs)),
		}
	}
	return nil
}

// mergedChanges concatenates every target's item changes, dropping repeats
// (the same plugin added for two targets is one line).
func mergedChanges(results []hydrate.Result) []hydrate.ItemChange {
	seen := map[hydrate.ItemChange]bool{}
	var out []hydrate.ItemChange
	for _, r := range results {
		for _, c := range r.Changes {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// hydratedEnv is one environment recorded under .ach/ with the sorted,
// de-duplicated canonical adapter ids it was hydrated for.
type hydratedEnv struct {
	name    string
	targets []string
}

// hydratedEnvs lists the environments hydrated under <cwd>/.ach/, sorted by
// name, grouping each env's per-platform state files into its targets.
func hydratedEnvs(cwd string) ([]hydratedEnv, error) {
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
	out := make([]hydratedEnv, 0, len(byEnv))
	for name, ids := range byEnv {
		targets := make([]string, 0, len(ids))
		for id := range ids {
			targets = append(targets, id)
		}
		sort.Strings(targets)
		out = append(out, hydratedEnv{name: name, targets: targets})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// firstNotice returns the first non-empty environment notice across results
// (all targets hydrate the same environment, so the notice is identical when
// present).
func firstNotice(results []hydrate.Result) string {
	for _, r := range results {
		if r.Notice != "" {
			return r.Notice
		}
	}
	return ""
}

// summaryMeta carries the scope + credential context the post-hydrate output
// needs: the header's scope, the Models line and the first-run hints
// (suppressed by --no-warnings). Computed at the cobra layer from the resolved
// inputs so renderHydrate stays a pure renderer.
type summaryMeta struct {
	global     bool              // --global → home-root scope
	output     string            // --dir ("" when unset)
	keyPrefix  keys.BearerPrefix // pk-/ek- classification of the bearer
	oauth      bool              // the bearer is an OAuth access token (rendered configs carry no credential)
	noWarnings bool              // --no-warnings → drop the hints
	models     bool              // --models → model-endpoint helper wiring requested
	dryRun     bool              // --dry-run → nothing was written
	cloneDir   string            // env clone's folder → "cd <dir>" hint
}

func newSummaryMeta(in hydrateInputs, bearer string) summaryMeta {
	prefix, _ := classifyBearer(bearer)
	return summaryMeta{
		global:     in.global,
		output:     in.output,
		keyPrefix:  prefix,
		oauth:      keys.LooksLikeJWS(bearer),
		noWarnings: in.noWarnings,
		models:     in.models,
		dryRun:     in.dryRun,
		cloneDir:   in.cloneDir,
	}
}

// Scope labels the workspace verbs print in their header.
const (
	scopeProject = "project"
	scopeGlobal  = "global"
)

// scopeLabel is the header parenthetical: where files land (+ dry run).
func scopeLabel(meta summaryMeta) string {
	scope := scopeProject
	switch {
	case meta.global:
		scope = scopeGlobal
	case meta.cloneDir != "":
		// clone's folder is the project; --dir is only how clone points at it.
	case meta.output != "":
		scope = "output " + meta.output
	}
	if meta.dryRun {
		scope += ", dry run"
	}
	return scope
}

// hydrateHeader prints the opening line before the engine runs, so a slow
// hydrate shows what it is doing.
func hydrateHeader(u *ui, env string, targets []string, meta summaryMeta) {
	if len(targets) == 1 {
		u.verb("Hydrating", "%s → %s (%s)", env, targets[0], scopeLabel(meta))
		return
	}
	u.verb("Hydrating", "%s (%s)", env, scopeLabel(meta))
}

// renderHydrate prints everything after the header: the item changes (one
// block per target when there are several), the Finished line, then the
// Models line, the Environment's notice, the engine's held-back notes (on eu,
// stderr) and, on a first hydrate only, the hints.
func renderHydrate(u, eu *ui, results []hydrate.Result, meta summaryMeta, took time.Duration,
	engineErr *bytes.Buffer) {
	u.targetItems(results, "up to date")
	u.verb("Finished", "%s in %s", finishedSummary(results, meta), elapsed(took))

	for _, r := range results {
		if r.RuntimeSummary.Models > 0 && modelsState(r, meta) == modelsNotWired {
			u.verb("Models", "%d available, not routed — add --models", r.RuntimeSummary.Models)
			break
		}
	}
	if notice := firstNotice(results); notice != "" {
		lines := strings.Split(strings.TrimRight(notice, "\n"), "\n")
		u.verb("Notice", "%s", lines[0])
		for _, l := range lines[1:] {
			u.verb("", "%s", l)
		}
	}
	eu.relay(engineErr)
	if !meta.noWarnings {
		for _, h := range hydrateHints(results, meta) {
			u.verb("hint", "%s", h)
		}
	}
}

// sameChanges reports whether every target changed the same items (one
// list then reads for all of them).
func sameChanges(results []hydrate.Result) bool {
	for _, r := range results[1:] {
		if !slices.Equal(r.Changes, results[0].Changes) {
			return false
		}
	}
	return true
}

// finishedSummary is the Finished line's result: what a first hydrate laid
// down, or how many items changed on a re-run.
func finishedSummary(results []hydrate.Result, meta summaryMeta) string {
	if meta.dryRun {
		return "nothing written (dry run)"
	}
	if len(results) == 1 {
		return targetSummary(results[0])
	}
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, r.PlatformID+" "+targetSummary(r))
	}
	return strings.Join(parts, " · ")
}

func targetSummary(r hydrate.Result) string {
	switch {
	case r.FirstRun:
		return countNoun(len(r.Changes), "item", "items") + ", " + countNoun(r.FilesWritten, "file", "files")
	case len(r.Changes) == 0:
		return "up to date"
	default:
		return fmt.Sprintf("%d changed, %d unchanged", len(r.Changes), r.Unchanged)
	}
}

// helperTargets are the adapters that render the --models helper wiring
// (claudecode settings.json, codex model_provider).
var helperTargets = map[string]bool{"claude-code": true, "codex": true}

// modelsWiring is whether this target's model endpoint can be wired locally
// (a person hydrate on a helper target) and, if so, whether --models did.
// Access itself is a server-side LiteLLM access-group either way.
type modelsWiring int

const (
	modelsServerSide modelsWiring = iota // ek_ / non-helper target: nothing local
	modelsWired
	modelsNotWired
)

func modelsState(res hydrate.Result, meta summaryMeta) modelsWiring {
	person := meta.oauth || meta.keyPrefix == keys.PrefixPk
	switch {
	case !person || !helperTargets[res.PlatformID]:
		return modelsServerSide
	case meta.models:
		return modelsWired
	default:
		return modelsNotWired
	}
}

// mcpLoginCmd is each tool's one-time MCP sign-in command.
var mcpLoginCmd = map[string]string{
	"claude-code": "claude mcp login <name>",
	"codex":       "codex mcp login <name>",
	"opencode":    "opencode mcp auth <name>",
}

// hydrateHints are the next steps worth saying once: after a clone (how to
// update), and on a first hydrate (MCP sign-in for a person, ek- for a pk-).
func hydrateHints(results []hydrate.Result, meta summaryMeta) []string {
	var hints []string
	if meta.cloneDir != "" {
		hints = append(hints, fmt.Sprintf("cd %s && ach-cli env hydrate  — to update later", meta.cloneDir))
	}
	first := false
	var logins []string
	for _, r := range results {
		first = first || r.FirstRun
		if c, ok := mcpLoginCmd[r.PlatformID]; ok && addsMCP(r) {
			logins = append(logins, c)
		}
	}
	if !first {
		return hints
	}
	switch {
	case meta.oauth:
		// An OAuth session also classifies as pk-, but the ek- hint is for
		// unattended workloads, never a person's interactive session.
		if len(logins) > 0 {
			hints = append(hints, "MCP servers sign in on first use: "+strings.Join(logins, " / "))
		}
	case meta.keyPrefix == keys.PrefixPk:
		hints = append(hints, "pk- is not Environment-scoped; Environment workloads (CI/agents) want an ek- key")
	}
	return hints
}

func addsMCP(r hydrate.Result) bool {
	for _, c := range r.Changes {
		if c.Op == hydrate.OpAdd && c.Kind == "mcp" {
			return true
		}
	}
	return false
}

func countNoun(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// resolvePlatformsOrAutodetect dispatches platform resolution per
// D-06: explicit --target > ACH_TARGET env > autodetect cwd
// (workspace) > autodetect $HOME (global). Explicit --target / ACH_TARGET
// accept a comma-separated list (mirroring local `plugin install --target
// a,b`), so this returns one-or-more canonical platform ids; autodetect
// always yields exactly one. Returns a typed CodedError on autodetect
// ambiguity / unknown id.
func resolvePlatformsOrAutodetect(in hydrateInputs, stderr io.Writer) ([]string, error) {
	if len(in.platform) > 0 {
		return parseTargets(in.platform)
	}
	if in.envTarget != "" {
		return parseTargets([]string{in.envTarget})
	}

	root := in.output
	if root == "" {
		if in.global {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, &exit.CodedError{
					Code:    exit.General,
					Msg:     fmt.Sprintf("resolve $HOME for --global autodetect: %v", err),
					Wrapped: err,
				}
			}
			root = home
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return nil, &exit.CodedError{
					Code:    exit.General,
					Msg:     fmt.Sprintf("resolve cwd for autodetect: %v", err),
					Wrapped: err,
				}
			}
			root = cwd
		}
	}
	id, err := hydrate.Autodetect(root, stderr)
	if err != nil {
		return nil, err
	}
	return []string{id}, nil
}

// runHydrateRaw is the Phase 6 surface-only POST+stream body extracted
// verbatim. Preserved as the --raw dispatch target so the W3-P3 e2e
// golden-diff anchor (`examples/hydrate.json`) keeps passing
// byte-for-byte. The 07-W4-02 e2e test (not this plan) updates its
// hydrate caller to pass --raw.
//
// NO json.Unmarshal / json.Marshal — the byte-equal contract depends
// on io.Copy. effectiveEnv == "" omits the body environment field (ek-
// + no --environment).
func runHydrateRaw(cmd *cobra.Command, baseURL, bearer, effectiveEnv string, verbose bool) error {
	var body any
	if effectiveEnv != "" {
		body = map[string]string{"environment": effectiveEnv}
	} else {
		body = struct{}{}
	}
	hc := newAPIClient(baseURL, bearer, hydrateHTTPClient, verbose, cmd.ErrOrStderr())
	resp, err := hc.DoRaw(cmd.Context(), http.MethodPost, "/platform/hydrate", body)
	if err != nil {
		return mapHydrateError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(cmd.OutOrStdout(), resp.Body); err != nil {
		return &exit.CodedError{
			Code:    exit.Network,
			Msg:     fmt.Sprintf("stream response body: %v", err),
			Wrapped: err,
		}
	}
	return nil
}

// mapHydrateError converts a *httpclient.ServerError (decoded §15.5
// envelope) OR a transport-side error into the right exit code per
// §9.3. Server errors flow through unchanged so cmd/ach/main.go's
// errors.As branch maps via exit.MapServerError; transport errors are
// wrapped as Network (exit 6).
func mapHydrateError(err error) error {
	var sErr *httpclient.ServerError
	if errors.As(err, &sErr) {
		return err
	}
	return &exit.CodedError{
		Code:    exit.Network,
		Msg:     err.Error(),
		Wrapped: err,
	}
}

// validateEnvHeaderValue rejects characters that have no business in a
// well-formed environment name and would compromise the x-ach-environment
// header (CRLF injection, header smuggling). Allowed: printable ASCII
// excluding control bytes. The platform-api enforces the canonical
// environment regex; this is a defense-in-depth gate so a buggy local
// effectiveEnv computation cannot smuggle a CR/LF.
func validateEnvHeaderValue(env string) error {
	if env == "" {
		return fmt.Errorf("empty")
	}
	for i := 0; i < len(env); i++ {
		c := env[i]
		if c == '\r' || c == '\n' || c == 0 {
			return fmt.Errorf("control byte 0x%02x at position %d", c, i)
		}
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("control byte 0x%02x at position %d", c, i)
		}
	}
	return nil
}
