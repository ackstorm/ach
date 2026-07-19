// SPDX-License-Identifier: Apache-2.0

// `ach-cli env hydrate` ships in two dispatch modes per the Phase 7 W3-05
// refactor (D-03 + D-04):
//
//   - Engine path (default) — invokes the full
//     internal/cli/hydrate.Run(ctx, Opts) 14-step commit sequence:
//     workspace lock, state.json v2 + drift, manifest fetch, safe
//     extract + auto-claim cascade, adapter dispatch, atomic state
//     write. Engine flags exposed: --include-runtime / --only-runtime
//     / --sync / --force / --dry-run / --wait / --lock-timeout / --output
//     / --allow-symlinks / --target / --global.
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
//   - D-11: mutex credential sources (--api-key, --env-key,
//     ACH_API_KEY, ACH_ENV_KEY). Explicit closed list — adding a new
//     source requires editing assertMutexCreds.
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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/achfile"
	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/synthetic"
	"github.com/ackstorm/ach/internal/keys"
)

// pkWarning is the pk- credential advisory for the --raw path, which has no
// success summary to host it. The engine path folds the same guidance into the
// summary's Tips footer instead (see summaryFromResult / hydrateTips), so this
// const is now emitted only when --raw short-circuits the engine. Gated by
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

// swapHydrateHTTPClientForTest is the test helper that swaps
// hydrateHTTPClient for the lifetime of t.
func swapHydrateHTTPClientForTest(t interface {
	Helper()
	Cleanup(func())
}, c *http.Client) {
	t.Helper()
	previous := hydrateHTTPClient
	hydrateHTTPClient = c
	t.Cleanup(func() { hydrateHTTPClient = previous })
}

// hydrateRunFn is the engine-dispatch test seam. Production callers
// leave the default (= hydrate.Run); unit tests targeting the
// engine path can swap it for a fake that records the Opts struct
// without spinning up a full lock + state.json + manifest fetch
// pipeline. Mirrors the swapHydrateHTTPClientForTest pattern.
var hydrateRunFn = hydrate.Run

// newHydrateCmd returns a fresh `ach-cli env hydrate` cobra.Command. Factory
// shape matches login/whoami/logout so tests can construct an isolated
// tree per t.Run.
func newHydrateCmd() *cobra.Command {
	var (
		// Phase 6 surface — preserved.
		flagNoWarnings bool
		flagVerbose    bool
		flagInsecure   bool
		flagAPIKey     string
		flagEnvKey     string
		flagProfile    string

		// Phase 7 engine flags (D-03).
		flagIncludeRuntime bool
		flagOnlyRuntime    bool
		flagSync           bool
		flagForce          bool
		flagDryRun         bool
		flagWait           bool
		flagLockTimeout    time.Duration
		flagOutput         string
		flagAllowSymlinks  bool
		flagTarget         string
		flagGlobal         bool
		flagConflict       string

		// D-04 hidden flag — surface preserved for the W3-P3 golden-
		// diff anchor.
		flagRaw bool
	)

	cmd := &cobra.Command{
		Use:   "hydrate <name>",
		Args:  cobra.MaximumNArgs(1),
		Short: "Materialize workspace artifacts (engine) or stream raw manifest (--raw)",
		Long: `Download an environment's content (prompts, skills, artifacts) into your
agent tool's config directory, and wire up its runtime (models / MCP / A2A).

Scope:
  --include-runtime   Also project the environment's runtime entries
                      (models / mcpServers / a2aAgents), not just context.
  --only-runtime      Project ONLY runtime entries (excludes context).
                      Default: context only (prompts / artifacts / skills).

Behavior:
  --sync              Remove files no longer in the environment.
  --force             Overwrite local edits and bypass safety refusals.
  --dry-run           Show what would change without writing anything.
  --allow-symlinks    Permit symlinks in downloaded archives (unsafe).

Locking:
  --wait              Wait indefinitely if another hydrate holds the lock.
  --lock-timeout <d>  Wait up to <d> (e.g. 30s, 5m). Conflicts with --wait.

Location:
  --output <dir>      Workspace root (default: current directory).
  --global            Hydrate under $HOME/.ach/<env> instead of ./.ach.
  --target <id[,id…]> Agent target(s): claude-code / codex / gemini-cli /
                      opencode / pimono (comma-separated for several).
                      Omitted: autodetected from the workspace.

Credentials (pick one; more than one is an error):
  --api-key <pk-|ek->   Use this key directly.
  --env-key <label>     Use a saved ek- from the active profile.
  ACH_API_KEY / ACH_ENV_KEY   Environment-variable equivalents.
  Otherwise the active profile's pk- (from ach login) is used.

The positional <name> is the environment. It is required with a pk- key;
with an ek- key it is optional (the key is already environment-scoped).

Exit codes:
  0 success   1 usage/credential error   2 local edits would be lost
  3 not authorized   4 wrong environment for this key   5 state mismatch
  6 network error   7 file-name collision   8 config read/write error`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conflict, err := hydrate.ParseConflictPolicy(flagConflict)
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: err.Error()}
			}
			env := ""
			if len(args) > 0 {
				env = args[0]
			}
			return runHydrate(cmd, hydrateInputs{
				environment:    env,
				noWarnings:     flagNoWarnings,
				verbose:        flagVerbose,
				insecure:       flagInsecure,
				flagAPIKey:     flagAPIKey,
				flagEnvKey:     flagEnvKey,
				flagProfile:    flagProfile,
				includeRuntime: flagIncludeRuntime,
				onlyRuntime:    flagOnlyRuntime,
				sync:           flagSync,
				force:          flagForce,
				dryRun:         flagDryRun,
				wait:           flagWait,
				lockTimeout:    flagLockTimeout,
				output:         flagOutput,
				allowSymlinks:  flagAllowSymlinks,
				platform:       flagTarget,
				global:         flagGlobal,
				conflict:       conflict,
				raw:            flagRaw,
			})
		},
	}

	// Phase 6 surface flags — preserved.
	cmd.Flags().BoolVar(&flagNoWarnings, "no-warnings", false,
		"Suppress the pk- credential warning")
	cmd.Flags().BoolVar(&flagVerbose, "verbose", false,
		"Dump redacted request headers to stderr")
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false,
		"Allow a plaintext http:// Hub URL (credentials sent unencrypted; localhost still requires this)")
	cmd.Flags().StringVar(&flagAPIKey, "api-key", "",
		"Override credential (pk-… or ek-… raw plaintext)")
	cmd.Flags().StringVar(&flagEnvKey, "env-key", "",
		"ek- label resolved against profiles.<active>.ek.<label>")
	cmd.Flags().StringVar(&flagProfile, "profile", "",
		"Override profile selection")

	// Phase 7 engine flags (D-03).
	cmd.Flags().BoolVar(&flagIncludeRuntime, "include-runtime", false,
		"Reconcile direct runtime entries (mcp/a2a/models); plugin MCPs always project via context")
	cmd.Flags().BoolVar(&flagOnlyRuntime, "only-runtime", false,
		"Reconcile ONLY runtime entries (mutually exclusive with --include-runtime)")
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
	cmd.Flags().StringVar(&flagOutput, "output", "",
		"Workspace root override (default: cwd)")
	cmd.Flags().BoolVar(&flagAllowSymlinks, "allow-symlinks", false,
		"Permit symlinks in downloaded archives (unsafe)")
	cmd.Flags().StringVar(&flagTarget, "target", "",
		"Override platform autodetection; comma-separated for several targets, e.g. codex,opencode "+
			"(claude-code / codex / gemini-cli / opencode / pimono + case-folded aliases)")
	cmd.Flags().BoolVar(&flagGlobal, "global", false,
		"Use $HOME/.ach/<env> scope instead of cwd/.ach")
	cmd.Flags().StringVar(&flagConflict, "conflict", "namespace",
		"Cross-plugin collision policy: namespace|skip|overwrite|refuse")

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

// hydrateCredSource is the closed-enum list of credential sources used
// by the D-11 mutex check. Adding a new source requires editing this
// list (visible in code review). NO flag-aliasing, NO env-prefix scan.
type hydrateCredSource struct {
	name  string
	value string
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
	flagAPIKey  string
	flagEnvKey  string
	flagProfile string

	// Phase 7 engine fields (D-03).
	includeRuntime bool
	onlyRuntime    bool
	sync           bool
	force          bool
	dryRun         bool
	wait           bool
	lockTimeout    time.Duration
	output         string
	allowSymlinks  bool
	platform       string
	global         bool
	conflict       hydrate.ConflictPolicy

	// D-04 hidden raw flag.
	raw bool

	envAPIKey      string
	envEnvKey      string
	envBaseURL     string
	envProfile     string
	envEnvironment string
	envPlatform    string
}

// runHydrate is the RunE body. Flow:
//
//  1. Read env-var snapshot into the inputs struct.
//  2. D-11 mutex gate + synthetic.GuardCommand (BEFORE any I/O).
//  3. assertScopeFlags — mutual exclusion of --include-runtime /
//     --only-runtime and --wait / --lock-timeout.
//  4. Resolve credential (synthetic OR config-disk path).
//  5. D-12 pk-/<name> positional argument gate.
//  6. plaintext-transport warning if http://.
//  7. Dispatch: flagRaw → runHydrateRaw (Phase 6 verbatim);
//     otherwise → runHydrateEngine (full 14-step commit sequence).
//  8. Emit the pk- warning after successful output so it does not bury the
//     hydrate summary.
func runHydrate(cmd *cobra.Command, in hydrateInputs) error {
	in.envAPIKey = os.Getenv("ACH_API_KEY")
	in.envEnvKey = os.Getenv("ACH_ENV_KEY")
	in.envBaseURL = os.Getenv("ACH_BASE_URL")
	in.envProfile = os.Getenv("ACH_PROFILE")
	in.envEnvironment = os.Getenv("ACH_ENVIRONMENT")
	in.envPlatform = os.Getenv("ACH_PLATFORM")

	// D-11 mutex BEFORE any I/O.
	if err := assertMutexCreds(in.flagAPIKey, in.flagEnvKey, in.envAPIKey, in.envEnvKey); err != nil {
		return err
	}
	// CLI-07 synthetic gate.
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateHydrate,
		APIKeyFlag:  in.flagAPIKey,
		EnvKeyFlag:  in.flagEnvKey,
		ProfileFlag: in.flagProfile,
	}); err != nil {
		return err
	}

	// Phase 7 scope-flag mutual exclusion: --include-runtime + --only-runtime,
	// and --wait + --lock-timeout. Both dispatch modes run this.
	//
	// NOT covered: --raw + engine flags. A --raw + --include-runtime combo is
	// incoherent (no runtime in the raw response surface), but it is NOT
	// rejected — runHydrateRaw takes no includeRuntime param, so the flag is
	// silently ignored on the raw path. Deliberate (#85): --raw is hidden
	// (MarkHidden, D-04) and exists only as the frozen Phase 6 byte-for-byte
	// golden-diff anchor; the only callers are e2e tests, none of which pass an
	// engine flag alongside it. A guard here would be unreachable, and the raw
	// path is deliberately frozen. If --raw is ever un-hidden, add the check.
	if err := assertScopeFlags(in); err != nil {
		return err
	}

	baseURL, bearer, err := resolveBearer(in)
	if err != nil {
		return err
	}

	// D-12: pk- classification + <name> positional argument gate.
	prefix, classifyErr := keys.ClassifyBearer(bearer)
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
	// --raw has no ach.yaml manifest path and no per-env state namespacing;
	// a pk- key additionally needs the env name to scope the verbatim stream
	// (an ek- binds its own Environment, so ek-/--raw/empty stays allowed).
	// The non-raw (engine) path no longer errors on an empty env here: when
	// BOTH the positional <name> and ACH_ENVIRONMENT are absent it falls
	// through to the ach.yaml manifest dispatch (runHydrateManifest), which
	// supplies a non-empty env per listed entry — and emits its own
	// required-arg error when no manifest exists.
	if in.raw && prefix == keys.PrefixPk && effectiveEnv == "" {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "<name> positional argument is required when using a pk- key (CLI-06 / spec §5.7)",
		}
	}

	// G19: refuse a plaintext http:// Hub URL (from profile, --base-url, or
	// ACH_BASE_URL) unless the user opted into insecure transport (--insecure
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
		// stderr. The engine path folds the same guidance into the summary's
		// Tips footer (summaryFromResult), so it is emitted there, not here.
		if runErr == nil && prefix == keys.PrefixPk && !in.noWarnings {
			_, _ = fmt.Fprint(cmd.ErrOrStderr(), pkWarning)
		}
	} else if effectiveEnv != "" {
		runErr = runHydrateEngine(cmd, in, baseURL, bearer, effectiveEnv)
	} else {
		runErr = runHydrateManifest(cmd, in, baseURL, bearer)
	}
	return runErr
}

// assertScopeFlags enforces the spec §6.3 scope-flag mutual exclusions:
//
//   - --include-runtime + --only-runtime  → exit 1.
//   - --wait + --lock-timeout             → exit 1.
//
// Both rejections cite the offending flag pair in the error message so
// the user can pick one and re-run.
func assertScopeFlags(in hydrateInputs) error {
	if in.includeRuntime && in.onlyRuntime {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "--include-runtime and --only-runtime are mutually exclusive",
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
//   - ACH_PLATFORM set → hydrate.ResolvePlatform(value) → canonical id.
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

	// Classify the bearer once: drives both the pk- x-ach-environment header
	// (below) and the summary's header facts + Tips footer (summaryFromResult).
	// A classify error leaves the prefix zero — neither path acts on it.
	bearerPrefix, _ := keys.ClassifyBearer(bearer)

	limits, err := extract.LoadLimits()
	if err != nil {
		return &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("load bomb-defense limits: %v", err),
			Wrapped: err,
		}
	}

	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		Verbose:    in.verbose,
		Stderr:     cmd.ErrOrStderr(),
		HTTPClient: hydrateHTTPClient,
	}

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
			return &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("invalid environment %q: %v", effectiveEnv, err),
			}
		}
		hc.ExtraHeaders = http.Header{"x-ach-environment": {effectiveEnv}}
	}

	// Hydrate each resolved platform in turn. --target accepts a comma-
	// separated list (mirroring local `plugin install --target a,b`); hc +
	// limits above are platform-independent and shared across the loop.
	// Fail-fast: a failing target returns immediately (earlier targets already
	// committed). Results are collected and rendered ONCE after the loop so a
	// multi-target run shows a single shared header + Tips, not a repeated
	// per-target summary.
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
			IncludeRuntime:    in.includeRuntime,
			OnlyRuntime:       in.onlyRuntime,
			Sync:              in.sync,
			Force:             in.force,
			Conflict:          in.conflict,
			DryRun:            in.dryRun,
			AllowSymlinks:     in.allowSymlinks,
			Output:            in.output,
			Wait:              in.wait,
			LockTimeout:       in.lockTimeout,
			BaseURL:           baseURL,
			Bearer:            bearer,
			Verbose:           in.verbose,
			Stdout:            cmd.OutOrStdout(),
			Stderr:            cmd.ErrOrStderr(),
			Extractor:         ext,
			AdapterDispatcher: ad,
		}

		res, err := hydrateRunFn(cmd.Context(), opts)
		if err != nil {
			return err
		}
		results = append(results, res)
	}
	if !in.dryRun {
		meta := summaryMeta{
			global:     in.global,
			output:     in.output,
			keyPrefix:  bearerPrefix,
			noWarnings: in.noWarnings,
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), renderHydrateSummary(results, meta))
	}
	return nil
}

// runHydrateManifest drives the manifest path: with no positional <name> and
// no ACH_ENVIRONMENT, it loads ach.yaml from the workspace root and hydrates
// each listed environment best-effort (a failing env is recorded and the run
// continues). It reuses runHydrateEngine per env, so each env hydrates exactly
// as a standalone `env hydrate <name>` would. Exits non-zero if any env failed.
func runHydrateManifest(cmd *cobra.Command, in hydrateInputs, baseURL, bearer string) error {
	root := in.output
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
		}
		root = wd
	}
	m, err := achfile.Load(root)
	if errors.Is(err, os.ErrNotExist) {
		return &exit.CodedError{
			Code: exit.General,
			Msg: "<name> positional argument is required: the hydrate engine " +
				"namespaces state by environment (.ach/<name>/); pass <name>, set " +
				"ACH_ENVIRONMENT, or create ach.yaml with `ach-cli env save`",
		}
	}
	if err != nil {
		return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}

	type envResult struct {
		name string
		err  error
	}
	results := make([]envResult, 0, len(m.Environments))
	for _, e := range m.Environments {
		perEnv := in
		// Target precedence: --target flag and ACH_PLATFORM both override the
		// manifest entry; the entry only fills in when neither is set.
		if perEnv.platform == "" && perEnv.envPlatform == "" && len(e.Targets) > 0 {
			perEnv.platform = strings.Join(e.Targets, ",")
		}
		runErr := runHydrateEngine(cmd, perEnv, baseURL, bearer, e.Name)
		results = append(results, envResult{name: e.Name, err: runErr})
	}

	failed := 0
	stderr := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(stderr, "\nHydrated %d environment(s) from ach.yaml:\n", len(results))
	for _, r := range results {
		if r.err != nil {
			failed++
			_, _ = fmt.Fprintf(stderr, "  - %s → FAIL: %v\n", r.name, r.err)
		} else {
			_, _ = fmt.Fprintf(stderr, "  - %s → OK\n", r.name)
		}
	}
	if failed > 0 {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("%d of %d environment(s) failed to hydrate", failed, len(results)),
		}
	}
	return nil
}

// renderHydrateSummary picks the summary shape by target count: a single target
// keeps the full per-domain block (summaryFromResult), while 2+ targets collapse
// to one shared header + a compact one-line-per-target body + a single Tips
// footer (summaryFromResultsCompact) — the repeated header/Tips of N full blocks
// reads as noise.
func renderHydrateSummary(results []hydrate.Result, meta summaryMeta) string {
	var body string
	if len(results) == 1 {
		body = summaryFromResult(results[0], meta)
	} else {
		body = summaryFromResultsCompact(results, meta)
	}
	if notice := firstNotice(results); notice != "" {
		body += "\n  Notice\n    " + strings.ReplaceAll(notice, "\n", "\n    ") + "\n"
	}
	return body
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

// summaryFromResultsCompact renders the multi-target summary: one shared header
// (environment + scope/key facts, identical across targets), one dense line per
// target (platform id + its non-zero resource/file counts joined by " · "), and
// a single shared Tips footer.
func summaryFromResultsCompact(results []hydrate.Result, meta summaryMeta) string {
	var b strings.Builder

	env := ""
	for _, r := range results {
		if r.Environment != "" {
			env = r.Environment
			break
		}
	}
	if env != "" {
		fmt.Fprintf(&b, "Hydrated %q", env)
	} else {
		fmt.Fprint(&b, "Hydrated")
	}
	if facts := scopeKeyFacts(meta); facts != "" {
		fmt.Fprintf(&b, " (%s)", facts)
	}
	fmt.Fprint(&b, "\n\n")

	width := 0
	for _, r := range results {
		if len(r.PlatformID) > width {
			width = len(r.PlatformID)
		}
	}
	for _, r := range results {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, r.PlatformID, strings.Join(compactSegments(r), " · "))
	}

	if !meta.noWarnings {
		if tips := hydrateTips(meta); len(tips) > 0 {
			fmt.Fprintln(&b)
			fmt.Fprintln(&b, "  Tips")
			for _, t := range tips {
				fmt.Fprintf(&b, "    • %s\n", t)
			}
		}
	}
	return b.String()
}

// compactSegments flattens one hydrate Result's non-zero tallies into the dot-
// joined segments of its compact line, in the same domain order the full
// summary uses (runtime → plugins+kinds → prompts → artifacts → standalone
// skills → files). Standalone skills (spec.context.skills) are distinct from
// the plugin-projected skills already counted in ProjectedByKind.
func compactSegments(r hydrate.Result) []string {
	var segs []string
	rs := r.RuntimeSummary
	if rs.Models > 0 {
		segs = append(segs, countNoun(rs.Models, "model", "models"))
	}
	if rs.MCPServers > 0 {
		segs = append(segs, countNoun(rs.MCPServers, "mcp server", "mcp servers"))
	}
	if rs.A2AAgents > 0 {
		segs = append(segs, countNoun(rs.A2AAgents, "a2a agent", "a2a agents"))
	}
	cs := r.ContextSummary
	// Plugin segment only when there's a real plugin count — ProjectedByKind
	// can carry component tallies, but a "0 plugins" prefix reads as a bug.
	if cs.Plugins > 0 {
		segs = append(segs, countNoun(cs.Plugins, "plugin", "plugins"))
	}
	segs = append(segs, kindSegments(r.ProjectedByKind)...)
	if cs.Prompts > 0 || cs.PromptFiles > 0 {
		n := cs.Prompts
		if n == 0 {
			n = cs.PromptFiles
		}
		segs = append(segs, countNoun(n, "prompt", "prompts"))
	}
	// Artifacts: fall back to the file count when the resource count is 0
	// (mirrors prompts) so we never print "0 artifacts" while files exist.
	if cs.Artifacts > 0 || cs.ArtifactFiles > 0 {
		n := cs.Artifacts
		if n == 0 {
			n = cs.ArtifactFiles
		}
		segs = append(segs, countNoun(n, "artifact", "artifacts"))
	}
	// Standalone context.skills only when they would NOT collide with the
	// plugin-projected "skills" already in ProjectedByKind — two bare "skills"
	// segments on one line are ambiguous (and may double-count).
	if cs.Skills > 0 && r.ProjectedByKind["skills"] == 0 {
		segs = append(segs, countNoun(cs.Skills, "skill", "skills"))
	}
	segs = append(segs, countNoun(r.FilesWritten, "file", "files"))
	return segs
}

// summaryMeta carries the scope + credential context the post-hydrate summary
// needs to render its header facts (scope + key kind, always shown) and Tips
// footer (actionable hints, suppressed by --no-warnings). Computed at the cobra
// layer from the resolved inputs so summaryFromResult stays a pure renderer.
type summaryMeta struct {
	global     bool              // --global → home-root scope
	output     string            // --output dir ("" when unset)
	keyPrefix  keys.BearerPrefix // pk-/ek- classification of the bearer
	noWarnings bool              // --no-warnings → drop the Tips footer
}

// summaryFromResult renders the post-hydrate success summary printed to
// stdout. The summary groups Environment resources by hydrate domain (runtime
// vs context) and keeps marketplace/plugin provenance out of the default view.
// The header carries scope + key-kind facts; a trailing Tips footer surfaces
// the actionable scope/credential hints unless --no-warnings.
func summaryFromResult(res hydrate.Result, meta summaryMeta) string {
	var b strings.Builder
	facts := scopeKeyFacts(meta)
	if res.Environment != "" {
		fmt.Fprintf(&b, "Hydrated %q environment for %s", res.Environment, res.PlatformID)
	} else {
		fmt.Fprintf(&b, "Hydrated for %s", res.PlatformID)
	}
	if facts != "" {
		fmt.Fprintf(&b, " (%s)", facts)
	}
	fmt.Fprint(&b, "\n\n")

	if hasRuntimeSummary(res.RuntimeSummary) {
		fmt.Fprintln(&b, "  Runtime")
		if res.RuntimeSummary.MCPServers > 0 {
			fmt.Fprintf(&b, "    ✓ MCP servers: %d\n", res.RuntimeSummary.MCPServers)
		}
		if res.RuntimeSummary.A2AAgents > 0 {
			fmt.Fprintf(&b, "    ✓ A2A agents: %d\n", res.RuntimeSummary.A2AAgents)
		}
		if res.RuntimeSummary.Models > 0 {
			// Models are NOT wired locally: access is a server-side LiteLLM
			// access-group behind the gateway, so this is informational (•),
			// never a ✓ "installed" line.
			fmt.Fprintf(&b, "    • Models: %d (served server-side via the gateway — nothing to install locally)\n",
				res.RuntimeSummary.Models)
		}
		fmt.Fprintln(&b)
	}

	if hasContextSummary(res.ContextSummary) || len(res.ProjectedByKind) > 0 {
		fmt.Fprintln(&b, "  Context")
		if res.ContextSummary.Plugins > 0 || len(res.ProjectedByKind) > 0 {
			pluginLine := fmt.Sprintf("    ✓ Plugins: %d total", res.ContextSummary.Plugins)
			if len(res.ProjectedByKind) > 0 {
				pluginLine += " (" + formatKindCounts(res.ProjectedByKind) + ")"
			}
			fmt.Fprintln(&b, pluginLine)
		}
		if res.ContextSummary.Prompts > 0 || res.ContextSummary.PromptFiles > 0 {
			files := res.ContextSummary.Prompts
			if files == 0 {
				files = res.ContextSummary.PromptFiles
			}
			fmt.Fprintf(&b, "    ✓ Prompts: %s\n", countNoun(files, "file", "files"))
		}
		if res.ContextSummary.Artifacts > 0 || res.ContextSummary.ArtifactFiles > 0 {
			artifactLine := fmt.Sprintf("    ✓ Artifacts: %s",
				countNoun(res.ContextSummary.Artifacts, "artifact", "artifacts"))
			if res.ContextSummary.ArtifactFiles > 0 {
				artifactLine += ", " + countNoun(res.ContextSummary.ArtifactFiles, "file", "files")
			}
			fmt.Fprintln(&b, artifactLine)
		}
		if res.ContextSummary.Skills > 0 || res.ContextSummary.SkillFiles > 0 {
			skillLine := fmt.Sprintf("    ✓ Skills: %d total", res.ContextSummary.Skills)
			if res.ContextSummary.SkillFiles > 0 {
				skillLine += ", " + countNoun(res.ContextSummary.SkillFiles, "file", "files")
			}
			fmt.Fprintln(&b, skillLine)
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintln(&b, "  Files")
	fmt.Fprintf(&b, "    ✓ %s written, %s preserved\n",
		countNoun(res.FilesWritten, "file", "files"),
		countNoun(res.FilesPreserved, "file", "files"))

	// Tips footer: actionable scope/credential hints. Suppressed by
	// --no-warnings (the header facts above are NOT — scope and key kind are
	// facts about what happened, not warnings).
	if !meta.noWarnings {
		if tips := hydrateTips(meta); len(tips) > 0 {
			fmt.Fprintln(&b)
			fmt.Fprintln(&b, "  Tips")
			for _, t := range tips {
				fmt.Fprintf(&b, "    • %s\n", t)
			}
		}
	}
	return b.String()
}

// scopeKeyFacts renders the header parenthetical describing where files land
// (scope) and which credential kind was used (key). Always shown — these are
// facts, not warnings. Returns "" only if no scope can be determined (never in
// practice: one of the three scope arms always matches).
func scopeKeyFacts(meta summaryMeta) string {
	parts := make([]string, 0, 2)
	switch {
	case meta.global:
		parts = append(parts, "global scope")
	case meta.output != "":
		parts = append(parts, fmt.Sprintf("output %s", meta.output))
	default:
		parts = append(parts, "project scope")
	}
	switch meta.keyPrefix {
	case keys.PrefixPk:
		parts = append(parts, "pk- key")
	case keys.PrefixEk:
		parts = append(parts, "ek- key")
	}
	return strings.Join(parts, ", ")
}

// hydrateTips collects the actionable hints for the Tips footer, each gated on
// the condition that makes it relevant:
//   - default project scope → how to target $HOME instead.
//   - pk- credential → ek- is the right key for Environment-scoped workloads.
func hydrateTips(meta summaryMeta) []string {
	tips := make([]string, 0, 2)
	if !meta.global && meta.output == "" {
		tips = append(tips, "pass --global to write under $HOME instead of ./.ach")
	}
	if meta.keyPrefix == keys.PrefixPk {
		tips = append(tips, "pk- is not Environment-scoped; Environment workloads (CI/agents) want an ek- key")
	}
	return tips
}

func hasRuntimeSummary(s hydrate.RuntimeSummary) bool {
	return s.Models > 0 || s.MCPServers > 0 || s.A2AAgents > 0
}

func hasContextSummary(s hydrate.ContextSummary) bool {
	return s.Plugins > 0 || s.Prompts > 0 || s.Artifacts > 0 || s.Skills > 0 ||
		s.PromptFiles > 0 || s.ArtifactFiles > 0 || s.SkillFiles > 0
}

func formatKindCounts(counts map[string]int) string {
	return strings.Join(kindSegments(counts), ", ")
}

// kindSegments renders per-kind "N kind" labels (sorted by kind, zero counts
// skipped) as a slice, so callers can join with ", " (verbose summary) or
// " · " (compact multi-target line).
func kindSegments(counts map[string]int) []string {
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if counts[k] <= 0 {
			continue
		}
		parts = append(parts, countNoun(counts[k], kindSingular(k), kindPlural(k)))
	}
	return parts
}

func kindSingular(kind string) string {
	switch kind {
	case "agents":
		return "agent"
	case "commands":
		return "command"
	case "skills":
		return "skill"
	case "rules":
		return "rule"
	case "mcp":
		return "MCP config"
	case "prompts":
		return "prompt"
	case "hooks":
		return "hook"
	default:
		return strings.TrimSuffix(kind, "s")
	}
}

func kindPlural(kind string) string {
	switch kind {
	case "mcp":
		return "MCP configs"
	default:
		return kind
	}
}

func countNoun(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// resolvePlatformsOrAutodetect dispatches platform resolution per
// D-06: explicit --target > ACH_PLATFORM env > autodetect cwd
// (workspace) > autodetect $HOME (global). Explicit --target / ACH_PLATFORM
// accept a comma-separated list (mirroring local `plugin install --target
// a,b`), so this returns one-or-more canonical platform ids; autodetect
// always yields exactly one. Returns a typed CodedError on autodetect
// ambiguity / unknown id.
func resolvePlatformsOrAutodetect(in hydrateInputs, stderr io.Writer) ([]string, error) {
	if in.platform != "" {
		return resolvePlatformList(in.platform)
	}
	if in.envPlatform != "" {
		return resolvePlatformList(in.envPlatform)
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

// resolvePlatformList parses an explicit --target / ACH_PLATFORM value into a
// deduped, order-preserving list of canonical platform ids. The value may be
// comma-separated (e.g. "codex,opencode"); each part is resolved via
// hydrate.ResolvePlatform (alias-aware), and an unknown part surfaces its
// CodedError. An effectively-empty value (all blanks) is an error.
func resolvePlatformList(raw string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
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
	if len(out) == 0 {
		return nil, &exit.CodedError{
			Code: exit.General,
			Msg:  "--target is empty: provide one or more platform ids (e.g. claude-code,codex)",
		}
	}
	return out, nil
}

// resolveBearer returns (baseURL, bearer) for the request, dispatching
// between synthetic mode (env-only) and config-disk mode. The disk
// path applies CLI-08 precedence (--profile / ACH_PROFILE /
// default / sole-entry) then the bearer-source switch.
func resolveBearer(in hydrateInputs) (string, string, error) {
	if in.envBaseURL != "" && (in.envAPIKey != "" || in.flagAPIKey != "") {
		bearer := in.flagAPIKey
		if bearer == "" {
			bearer = in.envAPIKey
		}
		return in.envBaseURL, bearer, nil
	}
	configPath, err := config.Path()
	if err != nil {
		return "", "", &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	// G19: honor the --insecure flag (OR ACH_INSECURE env) when reading the
	// profile, so a localhost http:// profile loads under an explicit opt-in.
	file, err := config.LoadWithInsecure(configPath, nil, in.insecure || config.InsecureFromEnv())
	if err != nil {
		return "", "", &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if file == nil {
		return "", "", &exit.CodedError{
			Code: exit.General,
			Msg:  "no profile configured; run `ach login` or set ACH_API_KEY + ACH_BASE_URL (CLI-08)",
		}
	}
	name, dep, err := config.ResolveActive(file, in.flagProfile, in.envProfile)
	if err != nil {
		return "", "", &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("%v; run `ach login`", err),
			Wrapped: err,
		}
	}
	bearer, err := pickBearer(in, name, dep)
	if err != nil {
		return "", "", err
	}
	if bearer == "" {
		return "", "", &exit.CodedError{
			Code: exit.General,
			Msg:  "no credential resolved; run `ach login` or set ACH_API_KEY",
		}
	}
	return dep.URL, bearer, nil
}

// pickBearer applies the bearer-source switch under the disk-config
// branch. Mutex was already asserted upstream, so at most one of the
// four sources is non-empty.
func pickBearer(in hydrateInputs, name string, dep *config.Profile) (string, error) {
	switch {
	case in.flagAPIKey != "":
		return in.flagAPIKey, nil
	case in.flagEnvKey != "":
		ek, ok := dep.EK[in.flagEnvKey]
		if !ok {
			return "", &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("--env-key %q not found in profiles.%s.ek", in.flagEnvKey, name),
			}
		}
		return ek, nil
	case in.envAPIKey != "":
		return in.envAPIKey, nil
	case in.envEnvKey != "":
		ek, ok := dep.EK[in.envEnvKey]
		if !ok {
			return "", &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("ACH_ENV_KEY %q not found in profiles.%s.ek", in.envEnvKey, name),
			}
		}
		return ek, nil
	case dep.PK != "":
		return dep.PK, nil
	}
	return "", nil
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
	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		Verbose:    verbose,
		Stderr:     cmd.ErrOrStderr(),
		HTTPClient: hydrateHTTPClient,
	}
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

// assertMutexCreds implements the D-11 closed-list mutex check. The
// four sources are EXPLICITLY enumerated — no flag-aliasing, no
// env-prefix scan. Adding a new credential source requires editing
// this list (visible in code review per T-06-06-01).
func assertMutexCreds(flagAPIKey, flagEnvKey, envAPIKey, envEnvKey string) error {
	sources := []hydrateCredSource{
		{name: "--api-key", value: flagAPIKey},
		{name: "--env-key", value: flagEnvKey},
		{name: "ACH_API_KEY", value: envAPIKey},
		{name: "ACH_ENV_KEY", value: envEnvKey},
	}
	var set []string
	for _, s := range sources {
		if s.value != "" {
			set = append(set, s.name)
		}
	}
	if len(set) > 1 {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("conflicting credential sources: %s (D-11 / CLI-09)", strings.Join(set, ", ")),
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
	if asHydrateErr(err, &sErr) {
		return err
	}
	return &exit.CodedError{
		Code:    exit.Network,
		Msg:     err.Error(),
		Wrapped: err,
	}
}

// asHydrateErr unwraps err looking for a *httpclient.ServerError.
// Mirrors the whoami errorsAs helper to avoid cross-file coupling.
func asHydrateErr(err error, target **httpclient.ServerError) bool {
	if err == nil {
		return false
	}
	for unwrap := err; unwrap != nil; {
		if t, ok := unwrap.(*httpclient.ServerError); ok {
			*target = t
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := unwrap.(unwrapper)
		if !ok {
			return false
		}
		unwrap = u.Unwrap()
	}
	return false
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
