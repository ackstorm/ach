// SPDX-License-Identifier: Apache-2.0

// `ach hydrate` is the headline Phase 6 subcommand: POST
// /platform/hydrate and stream the response body byte-for-byte to
// stdout. The single load-bearing demo path — `ach login` + `ach
// hydrate --environment demo > hydrate.json` byte-equals
// examples/hydrate.json (the Wave-3 e2e golden artifact).
//
// Scope: SURFACE ONLY per 06-CONTEXT.md D-09. The full hydrate engine
// (workspace lock, atomic state.json v2, dual-hash drift, adapter
// dispatch, safe tar extraction, --include-runtime / --only-runtime /
// --sync / --force / --dry-run) is Phase 7 (CLI-14..21 / STATE-*).
// Do NOT scope-creep into engine territory here.
//
// Decisions baked in:
//   - D-09: surface-only (no on-disk write, no diff, no state file).
//   - D-10: stderr §6.6 pk_ warning emitted BEFORE the HTTP call;
//     suppressed by --no-warnings.
//   - D-11: mutex credential sources (--api-key, --env-key,
//     ACH_API_KEY, ACH_ENV_KEY). >1 set → exit 1 with conflict list
//     BEFORE any I/O. Explicit closed list — no flag-aliasing,
//     no env-prefix scan. Adding a new credential source requires
//     editing this list.
//   - D-12: --environment REQUIRED for pk_; OPTIONAL for ek_. The
//     client-side check defends against a server-side
//     400 missing_environment round-trip.
//   - D-15: --verbose dumps a redacted header set to stderr.
//
// Byte-for-byte stdout discipline: the response body flows through
// io.Copy(os.Stdout, resp.Body) via httpclient.Client.DoRaw (foundation
// API from 06-01). The CLI MUST NOT json.Unmarshal+Marshal the body —
// re-marshaling would silently mutate whitespace, key ordering, and
// trailing newlines, breaking the Wave-3 golden-diff anchor.

package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/keys"
)

// pkWarning is the spec §6.6 stderr warning emitted when `ach hydrate`
// runs with a pk_ credential. Text is verbatim from
// spec/ach_cli_spec_v20260515_FINALv4.md §6.6 (mirrors Hub spec §15.3).
// The trailing newline is part of the const so Fprintf composes cleanly.
const pkWarning = `warning: hydrating with pk_; runtime spend is attributed to your
user/Team budgets, not the Environment budget (Hub spec §8.6).
For Environment-scoped workloads, use ek_:
    ach env-keys create <environment> --name <alias>
`

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

// newHydrateCmd returns a fresh `ach hydrate` cobra.Command. Factory
// shape matches login/whoami/logout so tests can construct an isolated
// tree per t.Run.
func newHydrateCmd() *cobra.Command {
	var (
		flagEnvironment string
		flagNoWarnings  bool
		flagVerbose     bool
		flagAPIKey      string
		flagEnvKey      string
		flagDeployment  string
	)

	cmd := &cobra.Command{
		Use:   "hydrate",
		Short: "POST /platform/hydrate and stream the response JSON to stdout",
		Long: `Issue POST /platform/hydrate against the active deployment and
write the server's response body byte-for-byte to stdout.

This is the Phase 6 surface-only form (D-09): no on-disk write, no
diff, no state file, no adapter dispatch — those land in Phase 7.

Credential resolution (D-11 mutex — all four sources mutually
exclusive; >1 set → exit 1):
  --api-key <pk_|ek_>      Override credential (raw plaintext)
  --env-key <label>        Reference deployments.<active>.ek.<label>
  ACH_API_KEY=<pk_|ek_>    Env var equivalent of --api-key
  ACH_ENV_KEY=<label>      Env var equivalent of --env-key

If none of the above is set, the CLI uses the active deployment's
pk: field from ~/.config/ach/config.yaml (run ach login first).

--environment is REQUIRED when the resolved credential is a pk_ (D-12);
OPTIONAL for ek_ (server-side mismatch yields 403 wrong_environment →
exit 1).

A stderr warning is emitted when the resolved credential is a pk_
(spec §6.6); suppress with --no-warnings.

Synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) WORKS for pk_
runs. --env-key / ACH_ENV_KEY are REJECTED in synthetic mode (no
config file to dereference the label against).

Exit codes (spec §9.3):
  0  success
  1  client-side gate (mutex creds, missing --environment, 403/400 etc.)
  3  401 / 403 not_admin / unauthorized_team
  6  503 / 504 / transport error
  8  config file parse or write error
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHydrate(cmd, flagEnvironment, flagNoWarnings, flagVerbose,
				flagAPIKey, flagEnvKey, flagDeployment)
		},
	}

	cmd.Flags().StringVar(&flagEnvironment, "environment", "",
		"Target Environment name (REQUIRED for pk_, OPTIONAL for ek_)")
	cmd.Flags().BoolVar(&flagNoWarnings, "no-warnings", false,
		"Suppress the §6.6 pk_ stderr warning")
	cmd.Flags().BoolVar(&flagVerbose, "verbose", false,
		"Dump redacted request headers to stderr")
	cmd.Flags().StringVar(&flagAPIKey, "api-key", "",
		"Override credential (pk_… or ek_… raw plaintext)")
	cmd.Flags().StringVar(&flagEnvKey, "env-key", "",
		"ek_ label resolved against deployments.<active>.ek.<label>")
	cmd.Flags().StringVar(&flagDeployment, "deployment", "",
		"Override deployment selection")

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
	environment    string
	noWarnings     bool
	verbose        bool
	flagAPIKey     string
	flagEnvKey     string
	flagDeployment string

	envAPIKey      string
	envEnvKey      string
	envBaseURL     string
	envDeployment  string
	envEnvironment string
}

// runHydrate is the RunE body. Flow:
//
//  1. Snapshot inputs.
//  2. assertMutexCreds — D-11 closed-list mutex gate (before I/O).
//  3. assertSyntheticConstraints — D-11 / spec §3.3 (--env-key /
//     --deployment forbidden when ACH_BASE_URL is set).
//  4. resolveBearer — synthetic OR config-disk path.
//  5. assertPKEnvironment — D-12 (--environment required for pk_).
//  6. Emit pk_ warning if needed (D-10 / spec §6.6).
//  7. POST + io.Copy(stdout, body) byte-for-byte.
func runHydrate(cmd *cobra.Command, environment string, noWarnings, verbose bool,
	flagAPIKey, flagEnvKey, flagDeployment string) error {
	in := hydrateInputs{
		environment:    environment,
		noWarnings:     noWarnings,
		verbose:        verbose,
		flagAPIKey:     flagAPIKey,
		flagEnvKey:     flagEnvKey,
		flagDeployment: flagDeployment,
		envAPIKey:      os.Getenv("ACH_API_KEY"),
		envEnvKey:      os.Getenv("ACH_ENV_KEY"),
		envBaseURL:     os.Getenv("ACH_BASE_URL"),
		envDeployment:  os.Getenv("ACH_DEPLOYMENT"),
		envEnvironment: os.Getenv("ACH_ENVIRONMENT"),
	}

	// D-11 mutex BEFORE any I/O.
	if err := assertMutexCreds(in.flagAPIKey, in.flagEnvKey, in.envAPIKey, in.envEnvKey); err != nil {
		return err
	}
	if err := assertSyntheticConstraints(in); err != nil {
		return err
	}

	baseURL, bearer, err := resolveBearer(in)
	if err != nil {
		return err
	}

	// D-12: pk_ classification + --environment gate.
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
			Msg:  "--environment is required when using a pk_ key (CLI-06 / spec §5.7)",
		}
	}

	stderr := cmd.ErrOrStderr()
	if prefix == keys.PrefixPk && !in.noWarnings {
		_, _ = fmt.Fprint(stderr, pkWarning)
	}

	return postAndStream(cmd, baseURL, bearer, effectiveEnv, in.verbose)
}

// assertSyntheticConstraints enforces the spec §3.3 / D-11 rules that
// apply when ACH_BASE_URL is set: --deployment / ACH_DEPLOYMENT and
// --env-key / ACH_ENV_KEY are all rejected. Half-synthetic invocations
// (ACH_BASE_URL set, no ACH_API_KEY) hit the --env-key arm too.
func assertSyntheticConstraints(in hydrateInputs) error {
	if in.envBaseURL == "" {
		return nil
	}
	synthetic := in.envAPIKey != "" || in.flagAPIKey != ""
	if synthetic && (in.flagDeployment != "" || in.envDeployment != "") {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "synthetic mode (ACH_BASE_URL + ACH_API_KEY) rejects --deployment / ACH_DEPLOYMENT (CLI spec §3.3)",
		}
	}
	if in.flagEnvKey != "" || in.envEnvKey != "" {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "--env-key requires a config-file-resolved deployment; not available with ACH_BASE_URL set",
		}
	}
	return nil
}

// resolveBearer returns (baseURL, bearer) for the request, dispatching
// between synthetic mode (env-only) and config-disk mode. The disk
// path applies CLI-08 precedence (--deployment / ACH_DEPLOYMENT /
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
			Msg:  "no deployment configured; run `ach login` or set ACH_API_KEY + ACH_BASE_URL (CLI-08)",
		}
	}
	name, dep, err := config.ResolveActive(file, in.flagDeployment, in.envDeployment)
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
func pickBearer(in hydrateInputs, name string, dep *config.Deployment) (string, error) {
	switch {
	case in.flagAPIKey != "":
		return in.flagAPIKey, nil
	case in.flagEnvKey != "":
		ek, ok := dep.EK[in.flagEnvKey]
		if !ok {
			return "", &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("--env-key %q not found in deployments.%s.ek", in.flagEnvKey, name),
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
				Msg:  fmt.Sprintf("ACH_ENV_KEY %q not found in deployments.%s.ek", in.envEnvKey, name),
			}
		}
		return ek, nil
	case dep.PK != "":
		return dep.PK, nil
	}
	return "", nil
}

// postAndStream issues POST /platform/hydrate and streams the response
// body byte-for-byte to stdout. NO json.Unmarshal / json.Marshal —
// the W3-P3 e2e golden-diff test depends on byte-equal output vs
// examples/hydrate.json. effectiveEnv == "" omits the body environment
// field (ek_ + no --environment).
func postAndStream(cmd *cobra.Command, baseURL, bearer, effectiveEnv string, verbose bool) error {
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

func init() {
	rootCmd.AddCommand(newHydrateCmd())
}
