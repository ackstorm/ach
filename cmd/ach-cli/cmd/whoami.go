// SPDX-License-Identifier: Apache-2.0

// `ach whoami` is the read-only identity command. Default invocation
// inspects ~/.config/ach/config.yaml only — no HTTP call. With
// --verify it performs an asymmetric remote check per CLI spec §5.3 /
// D-13:
//
//   - pk_ → GET /platform/environments?limit=1
//   - ek_ → POST /platform/hydrate {} with Accept-Encoding: gzip
//
// Exit codes per D-14: 0 on 2xx, 3 on 401, 6 on network failure. The
// pk_ vs ek_ branch uses internal/keys.ClassifyBearer for prefix
// classification (already shipped Phase 3).
//
// Synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) is supported
// transparently in W1 — the bearer comes from env via ClassifyBearer
// and the same asymmetric verify branches apply. Full mutex
// enforcement on --api-key/--env-key/ACH_API_KEY/ACH_ENV_KEY lands in
// W3-P1 (06-07) via the synthetic.GuardCommand extension.

package cmd

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/synthetic"
	"github.com/ackstorm/ach/internal/keys"
)

// whoamiHTTPClient is a test-only seam: when non-nil it replaces the
// default *http.Client inside the httpclient.Client built by whoami.
// Tests targeting an httptest.NewTLSServer set this to the test
// server's TLS-trusting Client so verify can reach the ephemeral cert.
var whoamiHTTPClient *http.Client

// swapHTTPClientForTest is the test helper that swaps whoamiHTTPClient
// for the lifetime of t.
func swapHTTPClientForTest(t interface {
	Helper()
	Cleanup(func())
}, c *http.Client) {
	t.Helper()
	previous := whoamiHTTPClient
	whoamiHTTPClient = c
	t.Cleanup(func() { whoamiHTTPClient = previous })
}

// newWhoamiCmd returns a fresh `ach whoami` cobra.Command.
func newWhoamiCmd() *cobra.Command {
	var (
		flagVerify  bool
		flagVerbose bool
		flagProfile string
		flagAPIKey  string
		flagEnvKey  string
	)

	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Print the active identity (no remote check unless --verify)",
		Long: `Print the identity block for the active profile.

Default (no --verify) reads ~/.config/ach/config.yaml and prints:
  Profile:  <name>
  URL:         <url>
  Key:         <prefix>_****<last-4>
  (no remote check)

With --verify, performs an asymmetric remote check per CLI spec §5.3:
  pk_  → GET  /platform/environments?limit=1
  ek_  → POST /platform/hydrate {}  (Accept-Encoding: gzip; body discarded)

Exit codes (D-14):
  0  success
  3  401 invalid_key / 403 not_admin
  6  network failure
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doWhoami(cmd, flagVerify, flagVerbose, flagProfile, flagAPIKey, flagEnvKey)
		},
	}

	cmd.Flags().BoolVar(&flagVerify, "verify", false, "Probe the server with the resolved key")
	cmd.Flags().BoolVar(&flagVerbose, "verbose", false, "Dump request headers to stderr (x-ach-key redacted)")
	cmd.Flags().StringVar(&flagProfile, "profile", "", "Override profile selection")
	cmd.Flags().StringVar(&flagAPIKey, "api-key", "", "Override pk_ from flag (synthetic-mode path)")
	cmd.Flags().StringVar(&flagEnvKey, "env-key", "", "ek_ label resolved against profiles.<active>.ek.<label>")

	return cmd
}

// doWhoami is the RunE body.
func doWhoami(cmd *cobra.Command, verify, verbose bool, profile, apiKey, envKey string) error {
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	// CLI-07 synthetic gate (allowed-in-synthetic; rejects half-set,
	// --profile, --env-key) — runs BEFORE resolveActiveBearer so
	// the centralized half-set message wins over any disk-config
	// disposition.
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateWhoami,
		APIKeyFlag:  apiKey,
		EnvKeyFlag:  envKey,
		ProfileFlag: profile,
	}); err != nil {
		return err
	}

	// Resolve the active profile + bearer credential.
	name, dep, bearer, err := resolveActiveBearer(profile, apiKey, envKey)
	if err != nil {
		return err
	}

	identity := formatIdentityBlock(name, dep, bearer)
	if !verify {
		_, _ = fmt.Fprint(stdout, identity, "(no remote check)\n")
		return nil
	}

	// --verify: classify pk_ vs ek_ and call the right endpoint.
	prefix, classifyErr := keys.ClassifyBearer(bearer)
	if classifyErr != nil {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("classify bearer: %v", classifyErr),
		}
	}

	hc := &httpclient.Client{
		BaseURL:    dep.URL,
		APIKey:     bearer,
		Verbose:    verbose,
		Stderr:     stderr,
		HTTPClient: whoamiHTTPClient,
	}

	ctx := cmd.Context()
	switch prefix {
	case keys.PrefixPk:
		if doErr := hc.Do(ctx, http.MethodGet, "/platform/environments?limit=1", nil, nil); doErr != nil {
			return mapVerifyError(doErr)
		}
	case keys.PrefixEk:
		// Set Accept-Encoding: gzip (CLI-11) via the foundation
		// ExtraHeaders field (06-01 contract — no inline httpclient
		// extension here). DoRaw is used so we can discard the body
		// after status check per CLI-11.
		hc.ExtraHeaders = http.Header{"Accept-Encoding": {"gzip"}}
		resp, doErr := hc.DoRaw(ctx, http.MethodPost, "/platform/hydrate", struct{}{})
		if doErr != nil {
			return mapVerifyError(doErr)
		}
		// CLI-11: body is discarded after status check.
		_ = resp.Body.Close()
	default:
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("unknown bearer prefix %q", prefix),
		}
	}

	_, _ = fmt.Fprint(stdout, identity, "Verified: yes\n")
	return nil
}

// resolveActiveBearer applies the CLI-08 precedence for the profile
// + a minimal bearer resolution chain (W1 scope; full mutex
// enforcement lands in W3-P1):
//
//  1. Synthetic mode (ACH_BASE_URL + ACH_API_KEY) — use the env pk_
//     directly; profile-flag/env REJECTED with exit 1.
//  2. --api-key flag — use it as the bearer, profile for URL only.
//  3. --env-key flag — resolve against profiles.<active>.ek.<label>.
//  4. ACH_API_KEY env — same as --api-key.
//  5. ACH_ENV_KEY env — same as --env-key.
//  6. default — profile's pk: from config.
//
// Returns the resolved profile NAME (for the identity block),
// *Profile (URL + optional EK map), bearer plaintext.
func resolveActiveBearer(flagProfile, flagAPIKey, flagEnvKey string) (string, *config.Profile, string, error) {
	envBaseURL := os.Getenv("ACH_BASE_URL")
	envAPIKey := os.Getenv("ACH_API_KEY")
	envEnvKey := os.Getenv("ACH_ENV_KEY")
	envProfile := os.Getenv("ACH_PROFILE")

	// Synthetic-mode bearer synthesis. The synthetic.GuardCommand call
	// in doWhoami already rejected --profile / --env-key / half-set
	// for this code path; here we just synthesize a one-off Profile
	// from the env bearer + URL. The label "(env)" matches
	// synthetic.SyntheticProfileLabel (Phase 7 consumes that const
	// directly when writing state.json).
	if envBaseURL != "" && envAPIKey != "" {
		dep := &config.Profile{URL: envBaseURL}
		return synthetic.SyntheticProfileLabel, dep, envAPIKey, nil
	}

	// Disk-config path: load + resolve active.
	configPath, err := config.Path()
	if err != nil {
		return "", nil, "", &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	file, err := config.Load(configPath)
	if err != nil {
		return "", nil, "", &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if file == nil {
		return "", nil, "", &exit.CodedError{
			Code: exit.General,
			Msg:  "no profile configured; run `ach login` (CLI-08)",
		}
	}
	name, dep, err := config.ResolveActive(file, flagProfile, envProfile)
	if err != nil {
		return "", nil, "", &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("%v; run `ach login`", err),
		}
	}

	// Bearer resolution.
	switch {
	case flagAPIKey != "":
		return name, dep, flagAPIKey, nil
	case flagEnvKey != "":
		ek, ok := dep.EK[flagEnvKey]
		if !ok {
			return "", nil, "", &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("--env-key %q not found in profiles.%s.ek", flagEnvKey, name),
			}
		}
		return name, dep, ek, nil
	case envAPIKey != "":
		return name, dep, envAPIKey, nil
	case envEnvKey != "":
		ek, ok := dep.EK[envEnvKey]
		if !ok {
			return "", nil, "", &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("ACH_ENV_KEY %q not found in profiles.%s.ek", envEnvKey, name),
			}
		}
		return name, dep, ek, nil
	case dep.PK != "":
		return name, dep, dep.PK, nil
	}
	return "", nil, "", &exit.CodedError{
		Code: exit.General,
		Msg:  fmt.Sprintf("no bearer for profile %q; run `ach login`", name),
	}
}

// formatIdentityBlock renders the four-line identity header used by
// both the no-net default AND --verify (the latter appends "Verified: yes").
func formatIdentityBlock(name string, dep *config.Profile, bearer string) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Profile: %s\n", name)
	_, _ = fmt.Fprintf(&sb, "URL: %s\n", dep.URL)
	_, _ = fmt.Fprintf(&sb, "Key: %s\n", config.Mask(bearer))
	return sb.String()
}

// mapVerifyError converts a *httpclient.ServerError (decoded §15.5
// envelope) OR a transport error into the right exit code per D-14.
func mapVerifyError(err error) error {
	// *httpclient.ServerError → main.go's errors.As branch maps via
	// exit.MapServerError, so just return the error as-is.
	var sErr *httpclient.ServerError
	if asErr := errorsAs(err, &sErr); asErr {
		return err
	}
	// Anything else is a transport / network failure → exit 6.
	return &exit.CodedError{
		Code:    exit.Network,
		Msg:     err.Error(),
		Wrapped: err,
	}
}

// errorsAs wraps errors.As for the targeted *ServerError type. Kept
// as a one-liner indirection so the test can stub it if needed.
func errorsAs(err error, target **httpclient.ServerError) bool {
	if err == nil {
		return false
	}
	for unwrap := err; unwrap != nil; {
		if t, ok := unwrap.(*httpclient.ServerError); ok {
			*target = t
			return true
		}
		// Unwrap chain.
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
	rootCmd.AddCommand(newWhoamiCmd())
}
