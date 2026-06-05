// SPDX-License-Identifier: Apache-2.0

// `ach-cli hydrate` ships in two dispatch modes per the Phase 7 W3-05
// refactor (D-03 + D-04):
//
//   - Engine path (default) — invokes the full
//     internal/cli/hydrate.Run(ctx, Opts) 14-step commit sequence:
//     workspace lock, state.json v2 + drift, manifest fetch, safe
//     extract + auto-claim cascade, adapter dispatch, atomic state
//     write. Engine flags exposed: --include-runtime / --only-runtime
//     / --sync / --force / --dry-run / --wait / --lock-timeout / --output
//     / --allow-symlinks / --platform / --global.
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
//   - D-10: stderr §6.6 pk- warning emitted BEFORE the HTTP call;
//     suppressed by --no-warnings. Applies to BOTH dispatch modes.
//   - D-11: mutex credential sources (--api-key, --env-key,
//     ACH_API_KEY, ACH_ENV_KEY). Explicit closed list — adding a new
//     source requires editing assertMutexCreds.
//   - D-12: --environment REQUIRED for pk-; OPTIONAL for ek-.
//   - D-15: --verbose dumps a redacted header set to stderr.
//
// Adapter registration: the 4 platform adapters
// (claudecode/codex/gemini/opencode) self-register via init() side
// effects in their subpackages; the blank-imports live in
// adapters_register.go in this package so this file stays focused on
// cobra wiring + dispatch.

package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/synthetic"
	"github.com/ackstorm/ach/internal/keys"
)

// pkWarning is the spec §6.6 stderr warning emitted when `ach-cli hydrate`
// runs with a pk- credential. Trimmed from the original §6.6 verbatim text
// (the budget-attribution prose was redundant for users) to a single
// actionable line. The trailing newline is part of the const so Fprintf
// composes cleanly.
const pkWarning = "warning: hydrating with pk-; for Environment-scoped workloads, use ek-\n"

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

// newHydrateCmd returns a fresh `ach-cli hydrate` cobra.Command. Factory
// shape matches login/whoami/logout so tests can construct an isolated
// tree per t.Run.
func newHydrateCmd() *cobra.Command {
	var (
		// Phase 6 surface — preserved.
		flagEnvironment string
		flagNoWarnings  bool
		flagVerbose     bool
		flagAPIKey      string
		flagEnvKey      string
		flagProfile     string

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
		flagPlatform       string
		flagGlobal         bool
		flagConflict       string

		// D-04 hidden flag — surface preserved for the W3-P3 golden-
		// diff anchor.
		flagRaw bool
	)

	cmd := &cobra.Command{
		Use:   "hydrate",
		Short: "Materialize workspace artifacts (engine) or stream raw manifest (--raw)",
		Long: `Materialize workspace artifacts via the Phase 7 hydrate engine.

The engine performs the full 14-step commit sequence under a workspace
lock: state.json v2 reconciliation, drift detection, manifest fetch,
safe tar extraction with bomb-defense caps, three-tier auto-claim
collision cascade, adapter dispatch (5-platform closed set), atomic
state write.

Scope filters (CLI spec §6.3 / STATE-10):
  --include-runtime   Reconcile the Environment's DIRECT runtime entries
                      (models / mcpServers / a2aAgents) alongside context,
                      projecting them into the adapter config + the
                      <ach-dir> runtime mirror.
  --only-runtime      Reconcile ONLY runtime entries (mutually
                      exclusive with --include-runtime).
                      Default: context only (prompts / plugins /
                      artifacts).

  Note: plugin-contributed MCP servers are part of CONTEXT and always
  project (default). --include-runtime governs the Environment's directly
  attached mcp/a2a/models, NOT plugin MCPs.

Behavior toggles:
  --sync              STATE-05 inverse-merge deletion of state entries
                      missing from the fresh manifest (deepest-first;
                      drift-bearing files preserved unless --force).
  --force             Bypass drift refusal, environment guard, and
                      schema mismatch (CLI spec §6.7).
  --dry-run           Run every read+diff step but skip state write
                      and content extract.
  --allow-symlinks    Relax SAFE-01 tar policy's symlink reject
                      (unsafe escape hatch).

Locking:
  --wait              Block indefinitely on workspace lock contention.
  --lock-timeout <d>  Wait up to <d> for the lock (e.g. 30s, 5m).
                      Mutually exclusive with --wait.

I/O:
  --output <dir>      Workspace root override (default: cwd).
  --global            Use $HOME/.ach/<env> scope instead of cwd/.ach.
  --platform <id>     Override platform autodetection (claude-code /
                      codex / gemini-cli / opencode / pimono +
                      case-folded aliases). When omitted, the engine scans cwd
                      (or $HOME under --global) and picks the
                      single match; zero or multiple matches → exit 1.

Credential resolution (D-11 mutex — all four sources mutually
exclusive; >1 set → exit 1):
  --api-key <pk-|ek->      Override credential (raw plaintext)
  --env-key <label>        Reference profiles.<active>.ek.<label>
  ACH_API_KEY=<pk-|ek->    Env var equivalent of --api-key
  ACH_ENV_KEY=<label>      Env var equivalent of --env-key

If none of the above is set, the CLI uses the active profile's pk: field
from ~/.config/ach/config.yaml. Seed that profile with ach login (SSO) or,
on a headless box, ach config add --api-key <pk-|ek->. To skip disk config
entirely, export ACH_BASE_URL + ACH_API_KEY (synthetic mode) — every
command then uses them with no per-command --api-key.

--environment is REQUIRED when the resolved credential is a pk- (D-12);
OPTIONAL for ek- (server-side mismatch yields 403 wrong_environment →
exit 1).

A stderr warning is emitted when the resolved credential is a pk-
(spec §6.6); suppress with --no-warnings.

Exit codes (spec §9.3):
  0  success
  1  client-side gate (mutex creds, missing --environment, autodetect
     ambiguity, scope-flag conflict, etc.)
  2  drift refused (STATE-04 four-outcome truth table)
  3  401 / 403 not_admin / unauthorized_team
  4  environment guard mismatch (STATE-03)
  5  schema mismatch (state.json or manifest)
  6  503 / 504 / transport error
  7  collision refuse (SAFE-04 auto-claim)
  8  config file parse or write error
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conflict, err := hydrate.ParseConflictPolicy(flagConflict)
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: err.Error()}
			}
			return runHydrate(cmd, hydrateInputs{
				environment:    flagEnvironment,
				noWarnings:     flagNoWarnings,
				verbose:        flagVerbose,
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
				platform:       flagPlatform,
				global:         flagGlobal,
				conflict:       conflict,
				raw:            flagRaw,
			})
		},
	}

	// Phase 6 surface flags — preserved.
	cmd.Flags().StringVar(&flagEnvironment, "environment", "",
		"Target Environment name (REQUIRED for pk-, OPTIONAL for ek-)")
	cmd.Flags().BoolVar(&flagNoWarnings, "no-warnings", false,
		"Suppress the §6.6 pk- stderr warning")
	cmd.Flags().BoolVar(&flagVerbose, "verbose", false,
		"Dump redacted request headers to stderr")
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
		"STATE-05 inverse-merge deletion of stale state entries (deepest-first)")
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
		"Relax SAFE-01 tar policy's symlink reject (unsafe escape hatch)")
	cmd.Flags().StringVar(&flagPlatform, "platform", "",
		"Override platform autodetection (claude-code / codex / gemini-cli / opencode / pimono + case-folded aliases)")
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
//  5. D-12 pk-/--environment gate.
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

	// Phase 7 scope-flag mutual exclusion. Applies in both dispatch
	// modes — a --raw + --include-runtime combo is incoherent (no
	// runtime in the raw response surface), but rejecting both
	// together at the cobra layer keeps the failure mode consistent.
	if err := assertScopeFlags(in); err != nil {
		return err
	}

	baseURL, bearer, err := resolveBearer(in)
	if err != nil {
		return err
	}

	// D-12: pk- classification + --environment gate.
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
	if prefix == keys.PrefixPk && effectiveEnv == "" {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "--environment is required when using a pk- key (CLI-06 / spec §5.7)",
		}
	}
	// The hydrate ENGINE namespaces state by environment
	// (.ach/<environment>/ in both project and --global scope per spec §8.1),
	// so --environment is required for any engine run regardless of credential
	// kind (D1). --raw is exempt: it short-circuits before the engine/state
	// path (Phase 6 verbatim POST+stream).
	if !in.raw && effectiveEnv == "" {
		return &exit.CodedError{
			Code: exit.General,
			Msg: "--environment is required: the hydrate engine namespaces state by " +
				"environment (.ach/<environment>/); pass --environment or set ACH_ENVIRONMENT",
		}
	}

	stderr := cmd.ErrOrStderr()
	// Plaintext-transport warning: http:// profile URLs are accepted
	// (config.validateProfiles no longer rejects them), but credentials
	// ride unencrypted. Emit a one-line stderr warning unless suppressed.
	if !in.noWarnings && strings.HasPrefix(baseURL, "http://") {
		_, _ = fmt.Fprintf(stderr,
			"warning: profile %q uses plaintext http:// — credentials are sent "+
				"unencrypted (safe only on trusted/internal networks)\n", baseURL)
	}

	// D-04 dispatch. --raw short-circuits BEFORE any engine call so
	// the W3-P3 golden-diff anchor survives byte-for-byte.
	var runErr error
	if in.raw {
		runErr = runHydrateRaw(cmd, baseURL, bearer, effectiveEnv, in.verbose)
	} else {
		runErr = runHydrateEngine(cmd, in, baseURL, bearer, effectiveEnv)
	}
	if runErr != nil {
		return runErr
	}

	if prefix == keys.PrefixPk && !in.noWarnings {
		_, _ = fmt.Fprint(stderr, pkWarning)
	}
	return nil
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
//   - --platform set → hydrate.ResolvePlatform(value) → canonical id.
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
	platformID, err := resolvePlatformOrAutodetect(in, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

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
	if prefix, perr := keys.ClassifyBearer(bearer); perr == nil && prefix == keys.PrefixPk {
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
	if !in.dryRun {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), summaryFromResult(res))
	}
	return nil
}

// summaryFromResult renders the post-hydrate success summary printed to
// stdout. The summary groups Environment resources by hydrate domain (runtime
// vs context) and keeps marketplace/plugin provenance out of the default view.
func summaryFromResult(res hydrate.Result) string {
	var b strings.Builder
	if res.Environment != "" {
		fmt.Fprintf(&b, "Hydrated %s for %s\n\n", res.Environment, res.PlatformID)
	} else {
		fmt.Fprintf(&b, "Hydrated for %s\n\n", res.PlatformID)
	}

	if hasRuntimeSummary(res.RuntimeSummary) {
		fmt.Fprintln(&b, "  Runtime")
		fmt.Fprintf(&b, "    ✓ Models: %d\n", res.RuntimeSummary.Models)
		fmt.Fprintf(&b, "    ✓ MCP servers: %d\n", res.RuntimeSummary.MCPServers)
		fmt.Fprintf(&b, "    ✓ A2A agents: %d\n\n", res.RuntimeSummary.A2AAgents)
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
	return b.String()
}

func hasRuntimeSummary(s hydrate.RuntimeSummary) bool {
	return s.Models > 0 || s.MCPServers > 0 || s.A2AAgents > 0
}

func hasContextSummary(s hydrate.ContextSummary) bool {
	return s.Plugins > 0 || s.Prompts > 0 || s.Artifacts > 0 || s.Skills > 0 ||
		s.PromptFiles > 0 || s.ArtifactFiles > 0 || s.SkillFiles > 0
}

func formatKindCounts(counts map[string]int) string {
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
	return strings.Join(parts, ", ")
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

// resolvePlatformOrAutodetect dispatches platform resolution per
// D-06: explicit --platform > ACH_PLATFORM env > autodetect cwd
// (workspace) > autodetect $HOME (global). Returns the canonical
// platform id on success, or a typed CodedError on autodetect
// ambiguity / unknown id.
func resolvePlatformOrAutodetect(in hydrateInputs, stderr io.Writer) (string, error) {
	if in.platform != "" {
		return hydrate.ResolvePlatform(in.platform)
	}
	if in.envPlatform != "" {
		return hydrate.ResolvePlatform(in.envPlatform)
	}

	root := in.output
	if root == "" {
		if in.global {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", &exit.CodedError{
					Code:    exit.General,
					Msg:     fmt.Sprintf("resolve $HOME for --global autodetect: %v", err),
					Wrapped: err,
				}
			}
			root = home
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return "", &exit.CodedError{
					Code:    exit.General,
					Msg:     fmt.Sprintf("resolve cwd for autodetect: %v", err),
					Wrapped: err,
				}
			}
			root = cwd
		}
	}
	return hydrate.Autodetect(root, stderr)
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
	file, err := config.Load(configPath)
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

func init() {
	rootCmd.AddCommand(newHydrateCmd())
}
