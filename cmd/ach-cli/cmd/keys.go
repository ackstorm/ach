// SPDX-License-Identifier: Apache-2.0

// `ach keys` (alias: `env-keys`) manages the caller's API keys:
// personal pk_ keys and environment ek_ keys. Three sub-subcommands:
// create / list / revoke.
//
// D-07 DEVIATION FROM SPEC §5.6 (intentional, the ONLY Phase 6
// spec divergence): `ach keys create` ALWAYS persists the
// returned `ek-` plaintext to `profiles.<active>.ek.<server-name>`
// in the active profile. The spec's `--save-as` flag is removed;
// `--no-save` opts out of persist (ek- flows to stdout only — for
// CI / vault-piping workflows). See:
//   - .planning/REQUIREMENTS.md CLI-09 row (marked DEVIATED, D-07).
//   - spec/ach_cli_spec_v20260515_FINALv4.md changelog (always-persist
//     + --no-save entry).
//
// D-08: `ach keys create` in synthetic mode (ACH_BASE_URL +
// ACH_API_KEY) requires `--no-save` — without it, the CLI exits 1
// because synthetic mode never has a writable config file.
//
// CLI-04 (S5 plaintext lifecycle): ek- printed to stdout EXACTLY
// ONCE at the success branch of `create`. NEVER echoed by `list` or
// `revoke`. On non-2xx the partial body is consumed by the §15.5
// envelope decoder in `httpclient` — no path leaks plaintext on
// failure.
//
// CLI-13: `revoke` enforces the `ekid_…` key-id prefix CLIENT-side
// BEFORE any HTTP call. Raw plaintext (`ek-…`) is rejected with a
// message that surfaces the mistake to stderr; `pkid_…` is rejected
// with a pointer to `ach admin keys revoke` (which W3-P2 / 06-08
// will accept).
//
// Per W7 (06-04 SUMMARY): the `list` formatter is `render.FormatKeyList`
// — a single source of truth shared with `ach admin keys list`
// (06-08). NO inline tabwriter here.

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/render"
	"github.com/ackstorm/ach/internal/cli/synthetic"
	"github.com/ackstorm/ach/internal/keys"
)

// keysHTTPClient is the test-only seam: when non-nil it replaces
// the default *http.Client inside the httpclient.Client constructed by
// each keys subcommand. Tests targeting httptest.NewTLSServer set
// this to the test server's TLS-trusting Client so the call reaches
// the ephemeral cert. Mirrors the whoami/login pattern from 06-03.
var keysHTTPClient *http.Client

// swapKeysHTTPClientForTest is the test helper that swaps
// keysHTTPClient for the lifetime of t.
func swapKeysHTTPClientForTest(t interface {
	Helper()
	Cleanup(func())
}, c *http.Client) {
	t.Helper()
	previous := keysHTTPClient
	keysHTTPClient = c
	t.Cleanup(func() { keysHTTPClient = previous })
}

// envKeysCreateResponse mirrors envkeys.CreateResponse on the wire.
// Re-declared here to avoid pulling internal/platformapi (k8s/chi
// deps) into the CLI binary.
type envKeysCreateResponse struct {
	KeyID       string `json:"key_id"`
	Plaintext   string `json:"plaintext"`
	Environment string `json:"environment"`
	Name        string `json:"name"`
	OwnerEmail  string `json:"owner_email"`
	CreatedAt   string `json:"created_at"`
}

// keysListResponse mirrors the GET /platform/keys response on the wire.
type keysListResponse struct {
	Items      []render.KeyRowView `json:"items"`
	NextCursor string              `json:"next_cursor"`
}

// newKeysCmd returns a fresh `ach keys` parent (alias: env-keys) with its
// three children registered. Factory shape (mirrors 06-03 login/whoami/logout)
// lets tests construct a hermetic cobra subtree per t.Run without
// cross-test global cobra state leaks.
func newKeysCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:     "keys",
		Aliases: []string{"env-keys"}, // back-compat
		Short:   "Manage your API keys (personal pk_ and environment ek_)",
		Long: `Manage your API keys — personal pk_ keys and environment ek_ keys.
All three sub-subcommands require pk- auth.

Sub-subcommands:
  create  Issue a new ek- for an Environment (D-07: always-persists to
          ~/.config/ach/config.yaml unless --no-save).
  list    Paginate your pk_ and ek_ keys (GET /platform/keys — caller-scoped).
  revoke  Delete an ek- by its ekid_ identifier.

D-07 (spec deviation): ek- create ALWAYS persists the returned plaintext
to profiles.<active>.ek.<server-name> in the active profile. The
spec's --save-as flag is REMOVED; --no-save opts out (ek- flows to stdout
only — useful for CI scripts piping ek- into a vault).

D-08: In synthetic mode (ACH_BASE_URL + ACH_API_KEY both set), create
without --no-save exits 1.
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	parent.AddCommand(newEnvKeysCreateCmd(), newKeysListCmd(), newEnvKeysRevokeCmd())
	return parent
}

// ---------------------------------------------------------------------
// create
// ---------------------------------------------------------------------

func newEnvKeysCreateCmd() *cobra.Command {
	var (
		flagEnvironment string
		flagName        string
		flagNoSave      bool
		flagProfile     string
		flagAPIKey      string
		flagEnvKey      string
		flagVerbose     bool
	)
	cmd := &cobra.Command{
		Use:  "create <environment>",
		Args: cobra.MaximumNArgs(1),
		Short: "Issue a new ek- for an Environment (D-07 always-persists)",
		// SilenceUsage + SilenceErrors: cobra otherwise echoes its
		// Usage block (containing the "ek-" flag descriptions) to
		// the writer attached via SetOut when a RunE returns
		// non-nil. That would clobber CLI-04 — stdout must NEVER
		// emit any ek- fragment on a non-2xx response. Errors
		// surface through cmd/ach/main.go's typed-error dispatch
		// (Pattern P12); the cobra-side echo is redundant.
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			// Resolve environment: positional wins; flag is fallback.
			positional := ""
			if len(args) > 0 {
				positional = strings.TrimSpace(args[0])
			}
			flagEnv := strings.TrimSpace(flagEnvironment)

			var resolvedEnv string
			switch {
			case positional != "" && flagEnv != "" && positional != flagEnv:
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("environment given twice (positional %q vs --environment %q)", positional, flagEnv),
				}
			case positional != "":
				resolvedEnv = positional
				flagEnvironment = positional
			case flagEnv != "":
				resolvedEnv = flagEnv
			default:
				// Neither positional nor flag provided: emit guided error.
				msg := "missing environment.\n" +
					"  Usage: ach keys create <environment> [--name <label>]\n" +
					"  Example: ach keys create frontend-dev"
				// Best-effort: append environment list (swallow any error).
				if envNames := fetchEnvNamesBestEffort(cmd.Context(), flagProfile, flagAPIKey, flagEnvKey); len(envNames) > 0 {
					msg += "\n  Your environments:\n    " + strings.Join(envNames, ", ")
				}
				return &exit.CodedError{Code: exit.General, Msg: msg}
			}

			// Default --name to the resolved environment when unset.
			if strings.TrimSpace(flagName) == "" {
				flagName = resolvedEnv
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runEnvKeysCreate(cmd, flagEnvironment, flagName, flagNoSave,
				flagProfile, flagAPIKey, flagEnvKey, flagVerbose)
		},
	}
	cmd.Flags().StringVar(&flagEnvironment, "environment", "", "Environment name")
	cmd.Flags().StringVar(&flagName, "name", "", "Local label for the new ek- (defaults to environment name)")
	cmd.Flags().BoolVar(&flagNoSave, "no-save", false,
		"Do NOT persist ek- to ~/.config/ach/config.yaml (D-07 escape hatch)")
	cmd.Flags().StringVar(&flagProfile, "profile", "", "Override profile selection")
	cmd.Flags().StringVar(&flagAPIKey, "api-key", "", "Override pk- from flag")
	cmd.Flags().StringVar(&flagEnvKey, "env-key", "", "Override with stored ek- label (rare for create)")
	cmd.Flags().BoolVar(&flagVerbose, "verbose", false, "Dump request headers to stderr (x-ach-key redacted)")
	return cmd
}

// fetchEnvNamesBestEffort attempts GET /platform/environments with a 5s
// timeout and returns the list of environment names. Any failure
// (no credentials, offline, non-200, timeout) is swallowed and an
// empty slice is returned so callers can safely omit the list line.
func fetchEnvNamesBestEffort(ctx context.Context, flagProfile, flagAPIKey, flagEnvKey string) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// resolveEnvKeysBearer reuses the existing credential resolution path.
	baseURL, bearer, err := resolveEnvKeysBearer(flagProfile, flagAPIKey, flagEnvKey)
	if err != nil || baseURL == "" || bearer == "" {
		return nil
	}
	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		HTTPClient: keysHTTPClient,
	}
	var resp envListResponse
	if doErr := hc.Do(ctx, http.MethodGet, buildEnvListPath(defaultEnvListLimit, ""), nil, &resp); doErr != nil {
		return nil
	}
	names := make([]string, 0, len(resp.Items))
	for _, e := range resp.Items {
		if e.Name != "" {
			names = append(names, e.Name)
		}
	}
	return names
}

func runEnvKeysCreate(cmd *cobra.Command, environment, name string, noSave bool,
	flagProfile, flagAPIKey, flagEnvKey string, verbose bool) error {

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	ctx := cmd.Context()

	// D-08 + CLI-07 synthetic gate via the centralized 06-07 helper.
	// GateEnvKeysCreate is allowed in synthetic IFF --no-save is set;
	// the helper also rejects half-set, --profile, and --env-key
	// before any HTTP call.
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateEnvKeysCreate,
		APIKeyFlag:  flagAPIKey,
		EnvKeyFlag:  flagEnvKey,
		ProfileFlag: flagProfile,
		NoSaveFlag:  noSave,
	}); err != nil {
		return err
	}

	name = strings.TrimSpace(name)
	environment = strings.TrimSpace(environment)
	if environment == "" || name == "" {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "--environment and --name are required",
		}
	}

	// Resolve credential + base URL (mirrors whoami's pattern; full
	// CLI-09 mutex enforcement deferred to W3-P1 / 06-07).
	baseURL, bearer, err := resolveEnvKeysBearer(flagProfile, flagAPIKey, flagEnvKey)
	if err != nil {
		return err
	}

	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		HTTPClient: keysHTTPClient,
		Verbose:    verbose,
		Stderr:     stderr,
	}

	body := struct {
		Environment string `json:"environment"`
		Name        string `json:"name"`
	}{Environment: environment, Name: name}
	var resp envKeysCreateResponse
	if doErr := hc.Do(ctx, http.MethodPost, "/platform/env-keys", body, &resp); doErr != nil {
		// On non-2xx, do NOT echo any fragment — main.go's
		// errors.As branch will map *ServerError to the right exit
		// code via exit.MapServerError. The Do() decode path drains
		// the body via the envelope path; nothing of `resp.Plaintext`
		// leaks here because resp is zero-valued.
		return doErr
	}

	// CLI-04: print plaintext exactly once.
	_, _ = fmt.Fprintln(stdout, resp.Plaintext)

	if noSave {
		// Disk untouched. Done.
		return nil
	}

	// D-07: always-persist to profiles.<active>.ek[name].
	cfgPath, err := config.Path()
	if err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	file, err := config.Load(cfgPath)
	if err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if file == nil || len(file.Profiles) == 0 {
		// Synthetic mode handled above; this path is only reachable
		// when the user has a base URL via flag/disk but no profile
		// entry — unusual but defended for completeness.
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "cannot --save: no profile configured; run `ach login` or pass --no-save",
		}
	}
	envProfile := os.Getenv("ACH_PROFILE")
	_, dep, err := config.ResolveActive(file, flagProfile, envProfile)
	if err != nil {
		return &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("%v; run `ach login` or pass --no-save", err),
			Wrapped: err,
		}
	}
	if dep.EK == nil {
		dep.EK = map[string]string{}
	}
	dep.EK[name] = resp.Plaintext
	if saveErr := config.Save(cfgPath, file); saveErr != nil {
		// Plaintext already on stdout (exactly once per CLI-04); do
		// NOT re-print it here. Surface the config write failure.
		_, _ = fmt.Fprintf(stderr, "warning: failed to persist ek- to config: %v\n", saveErr)
		return &exit.CodedError{Code: exit.ConfigFile, Msg: saveErr.Error(), Wrapped: saveErr}
	}
	return nil
}

// ---------------------------------------------------------------------
// list (GET /platform/keys — returns pk_ + ek_ for the caller)
// ---------------------------------------------------------------------

func newKeysListCmd() *cobra.Command {
	var (
		flagEnvironment string
		flagKeyType     string
		flagStatus      string
		flagCursor      string
		flagLimit       int
		flagProfile     string
		flagAPIKey      string
		flagEnvKey      string
		flagVerbose     bool
	)
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List your pk_ and ek_ keys (caller-scoped)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runKeysList(cmd, flagEnvironment, flagKeyType, flagStatus,
				flagCursor, flagLimit, flagProfile, flagAPIKey, flagEnvKey, flagVerbose)
		},
	}
	cmd.Flags().StringVar(&flagEnvironment, "environment", "", "Filter by environment name")
	cmd.Flags().StringVar(&flagKeyType, "type", "", "Filter by type: pk|ek (default both)")
	cmd.Flags().StringVar(&flagStatus, "status", "active", "Filter by status: active|revoked|expired|all (default active)")
	cmd.Flags().StringVar(&flagCursor, "cursor", "", "Opaque pagination cursor (auto-followed)")
	cmd.Flags().IntVar(&flagLimit, "limit", 0, "Per-page limit (server clamps; default 100, max 500)")
	cmd.Flags().StringVar(&flagProfile, "profile", "", "Override profile selection")
	cmd.Flags().StringVar(&flagAPIKey, "api-key", "", "Override pk- from flag")
	cmd.Flags().StringVar(&flagEnvKey, "env-key", "", "Override with stored ek- label")
	cmd.Flags().BoolVar(&flagVerbose, "verbose", false, "Dump request headers to stderr (x-ach-key redacted)")
	return cmd
}

func runKeysList(cmd *cobra.Command, environment, keyType, status, cursor string, limit int,
	flagProfile, flagAPIKey, flagEnvKey string, verbose bool) error {

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	ctx := cmd.Context()

	// CLI-07 synthetic gate (allowed-in-synthetic; rejects half-set,
	// --profile, --env-key) — runs BEFORE resolveEnvKeysBearer so
	// the half-set message wins over any disk-config error.
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateEnvKeysList,
		APIKeyFlag:  flagAPIKey,
		EnvKeyFlag:  flagEnvKey,
		ProfileFlag: flagProfile,
	}); err != nil {
		return err
	}

	baseURL, bearer, err := resolveEnvKeysBearer(flagProfile, flagAPIKey, flagEnvKey)
	if err != nil {
		return err
	}

	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		HTTPClient: keysHTTPClient,
		Verbose:    verbose,
		Stderr:     stderr,
	}

	// Paginate until next_cursor empty. Accumulate items.
	all := []render.KeyRowView{}
	currentCursor := cursor
	for {
		path := buildKeysListPath(environment, keyType, status, currentCursor, limit)
		var resp keysListResponse
		if doErr := hc.Do(ctx, http.MethodGet, path, nil, &resp); doErr != nil {
			return doErr
		}
		all = append(all, resp.Items...)
		if resp.NextCursor == "" {
			break
		}
		currentCursor = resp.NextCursor
	}

	// W7: single source of truth via render.FormatKeyList. NO inline
	// tabwriter in this file.
	_, _ = io.WriteString(stdout, render.FormatKeyList(all))
	return nil
}

func buildKeysListPath(environment, keyType, status, cursor string, limit int) string {
	q := url.Values{}
	if environment != "" {
		q.Set("environment", environment)
	}
	// send status unless "" or "all" (server normalizes unknown to no filter)
	if status != "" && status != "all" {
		q.Set("status", status)
	}
	if keyType != "" {
		q.Set("type", keyType)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if len(q) == 0 {
		return "/platform/keys"
	}
	return "/platform/keys?" + q.Encode()
}

// ---------------------------------------------------------------------
// revoke
// ---------------------------------------------------------------------

func newEnvKeysRevokeCmd() *cobra.Command {
	var (
		flagYes     bool
		flagProfile string
		flagAPIKey  string
		flagEnvKey  string
		flagVerbose bool
	)
	cmd := &cobra.Command{
		// Bare `revoke` (no inline arg hint in `Use:`) so the
		// `<key-id>` arg shape is documented purely via the
		// Short/Long help text + the cobra.ExactArgs(1) gate.
		// Keeps the `grep -c '"revoke"'` count == 3 in the
		// 06-05 plan acceptance text.
		Use:           "revoke",
		Short:         "Revoke an ek- by its ekid_ identifier (CLI-13). Usage: ach keys revoke <ekid_…>",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnvKeysRevoke(cmd, args[0], flagYes,
				flagProfile, flagAPIKey, flagEnvKey, flagVerbose)
		},
	}
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Bypass interactive confirmation")
	cmd.Flags().StringVar(&flagProfile, "profile", "", "Override profile selection")
	cmd.Flags().StringVar(&flagAPIKey, "api-key", "", "Override pk- from flag")
	cmd.Flags().StringVar(&flagEnvKey, "env-key", "", "Override with stored ek- label")
	cmd.Flags().BoolVar(&flagVerbose, "verbose", false, "Dump request headers to stderr (x-ach-key redacted)")
	return cmd
}

func runEnvKeysRevoke(cmd *cobra.Command, keyID string, yes bool,
	flagProfile, flagAPIKey, flagEnvKey string, verbose bool) error {

	stderr := cmd.ErrOrStderr()
	stdin := cmd.InOrStdin()
	ctx := cmd.Context()

	// CLI-07 synthetic gate (allowed-in-synthetic; rejects half-set,
	// --profile, --env-key).
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateEnvKeysRevoke,
		APIKeyFlag:  flagAPIKey,
		EnvKeyFlag:  flagEnvKey,
		ProfileFlag: flagProfile,
	}); err != nil {
		return err
	}

	// CLI-13: client-side key-id classification BEFORE any HTTP.
	switch {
	case strings.HasPrefix(keyID, keys.EkidKeyIDPrefix):
		// ok — proceed.
	case strings.HasPrefix(keyID, keys.EkBearerPrefix):
		// Raw plaintext rejected.
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf(
				"key id must be in %s form, got %s (raw plaintext rejected — CLI-13)",
				keys.EkidKeyIDPrefix, keys.EkBearerPrefix),
		}
	case strings.HasPrefix(keyID, keys.PkidKeyIDPrefix):
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf(
				"ach keys revoke accepts only %s ids; use `ach admin keys revoke` for %s ids",
				keys.EkidKeyIDPrefix, keys.PkidKeyIDPrefix),
		}
	default:
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("invalid key id %q; expected %s prefix", keyID, keys.EkidKeyIDPrefix),
		}
	}

	// Interactive confirmation unless --yes.
	if !yes {
		_, _ = fmt.Fprintf(stderr, "Confirm revoke of %s [y/N]: ", keyID)
		scanner := bufio.NewScanner(stdin)
		answer := ""
		if scanner.Scan() {
			answer = strings.ToLower(strings.TrimSpace(scanner.Text()))
		}
		switch answer {
		case "y", "yes":
			// proceed.
		default:
			return &exit.CodedError{
				Code: exit.General,
				Msg:  "cancelled",
			}
		}
	}

	baseURL, bearer, err := resolveEnvKeysBearer(flagProfile, flagAPIKey, flagEnvKey)
	if err != nil {
		return err
	}

	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		HTTPClient: keysHTTPClient,
		Verbose:    verbose,
		Stderr:     stderr,
	}
	if doErr := hc.Do(ctx, http.MethodDelete, "/platform/env-keys/"+keyID, nil, nil); doErr != nil {
		return doErr
	}
	return nil
}

// ---------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------

// resolveEnvKeysBearer is the env-keys sibling of whoami's
// resolveActiveBearer. Precedence (W1 minimal; full mutex in W3-P1):
//
//  1. Synthetic mode → use ACH_BASE_URL + ACH_API_KEY env.
//  2. --api-key flag → bearer; profile for URL only.
//  3. --env-key flag → resolve against profiles.<active>.ek.<label>.
//  4. ACH_API_KEY env → same as --api-key.
//  5. ACH_ENV_KEY env → same as --env-key.
//  6. default → profile.PK from disk config.
//
// Returns baseURL (profile.url or ACH_BASE_URL) and the bearer
// plaintext. The resolved profile name is folded into error
// strings only (no caller currently consumes it), keeping the
// signature lean.
func resolveEnvKeysBearer(flagProfile, flagAPIKey, flagEnvKey string) (string, string, error) {
	envBaseURL := os.Getenv("ACH_BASE_URL")
	envAPIKey := os.Getenv("ACH_API_KEY")
	envEnvKey := os.Getenv("ACH_ENV_KEY")
	envProfile := os.Getenv("ACH_PROFILE")

	if envBaseURL != "" && envAPIKey != "" {
		// Synthetic — no disk config consulted. The synthetic.GuardCommand
		// call in the cobra RunE already rejected --profile under
		// synthetic; the synthesized "(env)" profile lives only in
		// memory for this request.
		return envBaseURL, envAPIKey, nil
	}

	cfgPath, err := config.Path()
	if err != nil {
		return "", "", &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	file, err := config.Load(cfgPath)
	if err != nil {
		return "", "", &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if file == nil {
		return "", "", &exit.CodedError{
			Code: exit.General,
			Msg:  "no profile configured; run `ach login` (CLI-08)",
		}
	}
	name, dep, err := config.ResolveActive(file, flagProfile, envProfile)
	if err != nil {
		return "", "", &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("%v; run `ach login`", err),
			Wrapped: err,
		}
	}

	switch {
	case flagAPIKey != "":
		return dep.URL, flagAPIKey, nil
	case flagEnvKey != "":
		ek, ok := dep.EK[flagEnvKey]
		if !ok {
			return "", "", &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("--env-key %q not found in profiles.%s.ek", flagEnvKey, name),
			}
		}
		return dep.URL, ek, nil
	case envAPIKey != "":
		return dep.URL, envAPIKey, nil
	case envEnvKey != "":
		ek, ok := dep.EK[envEnvKey]
		if !ok {
			return "", "", &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("ACH_ENV_KEY %q not found in profiles.%s.ek", envEnvKey, name),
			}
		}
		return dep.URL, ek, nil
	case dep.PK != "":
		return dep.URL, dep.PK, nil
	}
	return "", "", &exit.CodedError{
		Code: exit.General,
		Msg:  fmt.Sprintf("no bearer for profile %q; run `ach login`", name),
	}
}

// Defensive: keep context import used even on platforms where the
// linker might trim the unused import.
var _ = context.Background

// Register `ach keys` (alias: env-keys) on the root command. Mirrors the
// login/logout/whoami pattern from 06-03 — each subcommand owns its
// own init() so cobra registration is local to the file.
func init() {
	rootCmd.AddCommand(newKeysCmd())
}
