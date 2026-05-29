// SPDX-License-Identifier: Apache-2.0

// `ach login` drives the device-code SSO flow (06-CONTEXT.md D-02:
// POST /platform/auth/cli/init → user visits browser → POST
// /platform/auth/cli/token poll loop) per CLI spec §5.1 UX verbatim
// (D-03). On success, mutates ~/.config/ach/config.yaml at mode 0600
// per D-04 — sets `default:` when previously absent, overwrites prior
// `pk:` on existing deployment (prior server-side key expires per Hub
// §7.1 7-day sliding window).
//
// Synthetic mode (D-03 / CLI-07): when ACH_BASE_URL + ACH_API_KEY are
// both set, refuses to run with exit 1 (spec §3.3). Full enforcement
// of CLI-07 lives in W3-P1 via internal/cli/synthetic; this command
// does the minimal inline check now so the dependency arrow is
// "synthetic enforces, login asserts".
//
// CLI-04 plaintext lifecycle: the pk_ plaintext flows from
// devicecode.TokenResponse.Plaintext into config.File.Deployments[name].PK
// via config.Save (yaml write to mode-0600 file). The ONLY stdout
// emission of the pk is the masked tail `pk_****<last-4>` via
// config.Mask, printed exactly once at success.

package cmd

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/devicecode"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// deploymentNamePattern enforces DNS-1123-style names so the config
// key namespace stays well-formed (path-safe, yaml-key-safe).
var deploymentNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// defaultLoginPollInterval is the fallback poll cadence when the
// server's InitResponse.PollInterval is 0 or absent (the server's
// canonical value is 2s per 06-PATTERNS.md).
const defaultLoginPollInterval = 2 * time.Second

// defaultLoginExpiresIn is the fallback total-timeout when the server
// omits ExpiresIn (server canonical is 300s = 5min per D-02).
const defaultLoginExpiresIn = 5 * time.Minute

// newLoginCmd returns a fresh `ach login` cobra.Command. The factory
// shape (instead of a package-level var) lets tests construct an
// isolated tree per t.Run to avoid global cobra state leaks across
// table cases.
func newLoginCmd() *cobra.Command {
	var (
		flagDeployment string
		flagBaseURL    string
		flagNoBrowser  bool
		flagNoWarnings bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate against a Hub via device-code SSO and persist pk_",
		Long: `Authenticate against an ACH Hub via the device-code SSO flow.

Flow:
  1. POST /platform/auth/cli/init to mint a session_id + verification_url.
  2. Open the verification_url in the browser (or print it with --no-browser).
  3. Poll POST /platform/auth/cli/token until the SSO round-trip lands
     the pk_ on the server.
  4. Persist the pk_ to ~/.config/ach/config.yaml at mode 0600.

Interactive prompts (skipped when --deployment / --base-url are set):
  Deployment name  DNS-1123 label, e.g. "prod"
  URL              https://hub.example.com (https:// required)

Synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) refuses to run
with exit 1 per CLI spec §3.3.

Flags:
  --deployment <name>   Skip the deployment-name prompt
  --base-url <url>      Skip the URL prompt (https:// only)
  --no-browser          Print verification_url instead of opening browser
  --no-warnings         Suppress config-file file-mode warnings to stderr
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd, flagDeployment, flagBaseURL, flagNoBrowser, flagNoWarnings)
		},
	}

	cmd.Flags().StringVar(&flagDeployment, "deployment", "", "Deployment name to write (DNS-1123 label)")
	cmd.Flags().StringVar(&flagBaseURL, "base-url", "", "Hub URL (https:// only)")
	cmd.Flags().BoolVar(&flagNoBrowser, "no-browser", false, "Print verification_url; do not open the browser")
	cmd.Flags().BoolVar(&flagNoWarnings, "no-warnings", false, "Suppress file-mode warnings to stderr")

	return cmd
}

// runLogin is the RunE body, extracted so newLoginCmd's closure stays
// short.
func runLogin(cmd *cobra.Command, deployment, baseURL string, noBrowser, noWarnings bool) error {
	ctx := cmd.Context()

	// Step 1 — synthetic-mode gate via the centralized 06-07 helper.
	// GateLogin denies under synthetic; the same call also rejects
	// half-set (ACH_BASE_URL set without credential) before any
	// device-code request fires.
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:           synthetic.GateLogin,
		DeploymentFlag: deployment,
	}); err != nil {
		return err
	}

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	stdin := cmd.InOrStdin()

	// Step 2 — resolve deployment name (flag or interactive prompt).
	name, err := resolveDeploymentName(deployment, stdin, stdout)
	if err != nil {
		return err
	}

	// Step 3 — resolve URL (flag or interactive prompt). Validate
	// https://.
	url, err := resolveBaseURL(baseURL, stdin, stdout)
	if err != nil {
		return err
	}

	// Step 4 — load existing config (best effort; nil-on-absent OK).
	configPath, err := config.Path()
	if err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	warn := func(format string, args ...any) {
		if noWarnings {
			return
		}
		_, _ = fmt.Fprintf(stderr, "warning: "+format+"\n", args...)
	}
	file, err := config.LoadWith(configPath, warn)
	if err != nil {
		// ErrNonHTTPSURL / ErrConfigParse / unreadable file → exit 8.
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if file == nil {
		file = &config.File{}
	}
	if file.Deployments == nil {
		file.Deployments = map[string]*config.Deployment{}
	}

	// Step 5 — device-code init.
	initResp, err := devicecode.Init(ctx, url)
	if err != nil {
		return err
	}

	// Step 6 — open browser (or fall back to print).
	if !noBrowser {
		if openErr := devicecode.Opener(initResp.VerificationURL); openErr != nil {
			_, _ = fmt.Fprintf(stderr, "warning: open browser failed (%v); print URL below\n", openErr)
		}
	}

	// Step 7 — print verification_url (always: helps copy-paste even
	// when the browser opened successfully).
	_, _ = fmt.Fprintf(stdout, "Visit %s to complete login\n", initResp.VerificationURL)

	// Step 8 — poll /token until success / terminal / timeout.
	pollInterval := time.Duration(initResp.PollInterval) * time.Second
	if pollInterval <= 0 {
		pollInterval = defaultLoginPollInterval
	}
	totalTimeout := time.Duration(initResp.ExpiresIn) * time.Second
	if totalTimeout <= 0 {
		totalTimeout = defaultLoginExpiresIn
	}
	tokenResp, err := devicecode.PollToken(ctx, url, initResp.SessionID, pollInterval, totalTimeout)
	if err != nil {
		return err
	}

	// Step 9 — mutate + save config. Preserve any pre-existing EK
	// map on this deployment (only `pk:` overwrite per D-04).
	existing := file.Deployments[name]
	dep := &config.Deployment{
		URL: url,
		PK:  tokenResp.Plaintext,
	}
	if existing != nil {
		dep.EK = existing.EK
	}
	file.Deployments[name] = dep
	if file.Default == "" {
		file.Default = name
	}
	if err := config.Save(configPath, file); err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}

	// Step 10 — success line. CLI-04: pk_ printed ONLY as the masked
	// tail. The full plaintext lives in tokenResp.Plaintext →
	// file.Deployments[name].PK → on-disk yaml only.
	_, _ = fmt.Fprintf(stdout, "Logged in as %s (%s)\n", tokenResp.OwnerEmail, config.Mask(tokenResp.Plaintext))
	return nil
}

// resolveDeploymentName returns the flag value when set; otherwise
// prompts via stdin. Validates against the DNS-1123 label pattern.
func resolveDeploymentName(flagVal string, stdin io.Reader, stdout io.Writer) (string, error) {
	name := strings.TrimSpace(flagVal)
	if name == "" {
		_, _ = fmt.Fprint(stdout, "Deployment name: ")
		s := bufio.NewScanner(stdin)
		if s.Scan() {
			name = strings.TrimSpace(s.Text())
		}
	}
	if name == "" || !deploymentNamePattern.MatchString(name) {
		return "", &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("deployment name %q is invalid; expected DNS-1123 label (lower-case [a-z0-9-])", name),
		}
	}
	return name, nil
}

// resolveBaseURL returns the flag value when set; otherwise prompts.
// Validates the https:// prefix.
func resolveBaseURL(flagVal string, stdin io.Reader, stdout io.Writer) (string, error) {
	url := strings.TrimSpace(flagVal)
	if url == "" {
		_, _ = fmt.Fprint(stdout, "URL: ")
		s := bufio.NewScanner(stdin)
		if s.Scan() {
			url = strings.TrimSpace(s.Text())
		}
	}
	if !strings.HasPrefix(url, "https://") {
		return "", &exit.CodedError{
			Code: exit.General,
			Msg:  "url must be https:// (CLI-02 / D-04)",
		}
	}
	return url, nil
}

func init() {
	rootCmd.AddCommand(newLoginCmd())
}
