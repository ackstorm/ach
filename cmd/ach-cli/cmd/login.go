// SPDX-License-Identifier: Apache-2.0

// `ach login` drives the device-code SSO flow (06-CONTEXT.md D-02:
// POST /platform/auth/cli/init → user visits browser → POST
// /platform/auth/cli/token poll loop) per CLI spec §5.1 UX verbatim
// (D-03). On success, mutates ~/.config/ach/config.yaml at mode 0600
// per D-04 — sets `default:` when previously absent, overwrites prior
// `pk:` on existing profile (prior server-side key expires per Hub
// §7.1 7-day sliding window).
//
// Synthetic mode (D-03 / CLI-07): when ACH_BASE_URL + ACH_API_KEY are
// both set, refuses to run with exit 1 (spec §3.3). Full enforcement
// of CLI-07 lives in W3-P1 via internal/cli/synthetic; this command
// does the minimal inline check now so the dependency arrow is
// "synthetic enforces, login asserts".
//
// CLI-04 plaintext lifecycle: the pk- plaintext flows from
// devicecode.TokenResponse.Plaintext into config.File.Profiles[name].PK
// via config.Save (yaml write to mode-0600 file). The ONLY stdout
// emission of the pk is the masked tail `pk-****<last-4>` via
// config.Mask, printed exactly once at success.

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/devicecode"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// profileNamePattern enforces DNS-1123-style names so the config
// key namespace stays well-formed (path-safe, yaml-key-safe).
var profileNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

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
		flagProfile    string
		flagBaseURL    string
		flagNoBrowser  bool
		flagNoWarnings bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate against a Hub via device-code SSO and persist pk-",
		Long: `Authenticate against an ACH Hub via the device-code SSO flow.

Flow:
  1. POST /platform/auth/cli/init to mint a session_id + verification_url.
  2. Open the verification_url in the browser (or print it with --no-browser).
  3. Poll POST /platform/auth/cli/token until the SSO round-trip lands
     the pk- on the server.
  4. Persist the pk- to ~/.config/ach/config.yaml at mode 0600.

Interactive prompts (skipped when --profile / --base-url are set):
  Profile name  DNS-1123 label, e.g. "prod"
  URL              https://hub.example.com (http:// or https://; http:// warns)

Synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) refuses to run
with exit 1 per CLI spec §3.3.

Flags:
  --profile <name>   Skip the profile-name prompt
  --base-url <url>      Skip the URL prompt (http:// or https://)
  --no-browser          Print verification_url instead of opening browser
  --no-warnings         Suppress config-file file-mode warnings to stderr
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd, flagProfile, flagBaseURL, flagNoBrowser, flagNoWarnings)
		},
	}

	cmd.Flags().StringVar(&flagProfile, "profile", "", "Profile name to write (DNS-1123 label)")
	cmd.Flags().StringVar(&flagBaseURL, "base-url", "", "Hub URL (http:// or https://)")
	cmd.Flags().BoolVar(&flagNoBrowser, "no-browser", false, "Print verification_url; do not open the browser")
	cmd.Flags().BoolVar(&flagNoWarnings, "no-warnings", false, "Suppress file-mode warnings to stderr")

	return cmd
}

// runLogin is the RunE body, extracted so newLoginCmd's closure stays
// short.
func runLogin(cmd *cobra.Command, profile, baseURL string, noBrowser, noWarnings bool) error {
	ctx := cmd.Context()

	// Step 1 — synthetic-mode gate via the centralized 06-07 helper.
	// GateLogin denies under synthetic; the same call also rejects
	// half-set (ACH_BASE_URL set without credential) before any
	// device-code request fires.
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateLogin,
		ProfileFlag: profile,
	}); err != nil {
		return err
	}

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	stdin := cmd.InOrStdin()

	// Step 2 — resolve profile name (flag or interactive prompt).
	name, err := resolveProfileName(profile, stdin, stdout)
	if err != nil {
		return err
	}

	// Step 3 — resolve URL (flag or interactive prompt). Accepts
	// http:// or https://.
	url, err := resolveBaseURL(baseURL, stdin, stdout)
	if err != nil {
		return err
	}
	if !noWarnings && strings.HasPrefix(url, "http://") {
		_, _ = fmt.Fprintf(stderr,
			"warning: profile %q uses plaintext http:// — credentials are sent "+
				"unencrypted (safe only on trusted/internal networks)\n", url)
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
		// ErrInvalidURLScheme / ErrConfigParse / unreadable file → exit 8.
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if file == nil {
		file = &config.File{}
	}
	if file.Profiles == nil {
		file.Profiles = map[string]*config.Profile{}
	}

	// Step 5 — device-code init.
	initResp, err := devicecode.Init(ctx, url)
	if err != nil {
		return err
	}

	// Step 6 — decide how to surface the login URL: open the browser,
	// print-and-wait (remote/headless), or cancel. Interactive TTYs get
	// a pre-open prompt; non-interactive sessions keep the legacy
	// behavior (auto-open unless --no-browser). Both open and print
	// branches still poll — only the browser shell-out differs.
	switch resolvePreOpen(noBrowser, stdin, stdout) {
	case actCancel:
		return &exit.CodedError{Code: exit.General, Msg: "login canceled"}
	case actOpen:
		if openErr := devicecode.Opener(initResp.VerificationURL); openErr != nil {
			_, _ = fmt.Fprintf(stderr, "warning: open browser failed (%v); print URL below\n", openErr)
		}
	case actPrint:
		// No shell-out; the next step always prints the URL for copy/paste.
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
	// map on this profile (only `pk:` overwrite per D-04).
	existing := file.Profiles[name]
	dep := &config.Profile{
		URL: url,
		PK:  tokenResp.Plaintext,
	}
	if existing != nil {
		dep.EK = existing.EK
	}
	file.Profiles[name] = dep
	if file.Default == "" {
		file.Default = name
	}
	if err := config.Save(configPath, file); err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}

	// Step 10 — success line. CLI-04: pk- printed ONLY as the masked
	// tail. The full plaintext lives in tokenResp.Plaintext →
	// file.Profiles[name].PK → on-disk yaml only.
	_, _ = fmt.Fprintf(stdout, "Logged in as %s (%s)\n", tokenResp.OwnerEmail, config.Mask(tokenResp.Plaintext))
	return nil
}

// resolveProfileName returns the flag value when set; otherwise
// prompts via stdin. Validates against the DNS-1123 label pattern.
func resolveProfileName(flagVal string, stdin io.Reader, stdout io.Writer) (string, error) {
	name := strings.TrimSpace(flagVal)
	if name == "" {
		v, err := readLine("Profile name: ", stdin, stdout)
		if err != nil {
			return "", err
		}
		name = v
	}
	if name == "" || !profileNamePattern.MatchString(name) {
		return "", &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("profile name %q is invalid; expected DNS-1123 label (lower-case [a-z0-9-])", name),
		}
	}
	return name, nil
}

// rwPair adapts a separate reader + writer into the single io.ReadWriter
// that term.NewTerminal expects (raw-mode keystrokes in, echo out).
type rwPair struct {
	io.Reader
	io.Writer
}

// readLine reads one line for an interactive prompt. On a TTY it uses a
// raw-mode line editor (golang.org/x/term) so arrow keys / Home / End /
// backspace edit the line in place; Ctrl-C / Ctrl-D abort with "login
// canceled". On a non-TTY (pipe / CI / tests) — or if raw mode cannot be
// entered — it falls back to the original plain bufio.Scanner read with
// the prompt printed to stdout. The terminal is always restored via defer
// before the function returns, so the raw window is scoped to this read.
func readLine(prompt string, stdin io.Reader, stdout io.Writer) (string, error) {
	sf, ok := stdin.(*os.File)
	if !ok || !isTerminal(stdin) || !isTerminal(stdout) {
		return scanLine(prompt, stdin, stdout)
	}
	fd := int(sf.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return scanLine(prompt, stdin, stdout)
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	t := term.NewTerminal(rwPair{Reader: stdin, Writer: stdout}, prompt)
	if w, h, gerr := term.GetSize(fd); gerr == nil {
		_ = t.SetSize(w, h)
	}
	line, rerr := t.ReadLine()
	if rerr != nil {
		// Ctrl-C / Ctrl-D both surface as io.EOF (see x/term terminal.go) —
		// treat either as a user abort.
		if errors.Is(rerr, io.EOF) {
			return "", &exit.CodedError{Code: exit.General, Msg: "login canceled"}
		}
		return "", &exit.CodedError{Code: exit.General, Msg: rerr.Error(), Wrapped: rerr}
	}
	return strings.TrimSpace(line), nil
}

// scanLine is the cooked-mode / non-TTY fallback: print the prompt, read
// one line with bufio.Scanner. No cursor-movement editing (the terminal's
// own line discipline still handles backspace).
func scanLine(prompt string, stdin io.Reader, stdout io.Writer) (string, error) {
	_, _ = fmt.Fprint(stdout, prompt)
	s := bufio.NewScanner(stdin)
	if s.Scan() {
		return strings.TrimSpace(s.Text()), s.Err()
	}
	return "", s.Err()
}

// resolveBaseURL returns the flag value when set; otherwise prompts.
// Accepts http:// or https://; rejects any other scheme. http:// is
// allowed for local/internal hubs — runLogin emits a plaintext-transport
// warning when the resolved URL is http://.
func resolveBaseURL(flagVal string, stdin io.Reader, stdout io.Writer) (string, error) {
	url := strings.TrimSpace(flagVal)
	if url == "" {
		v, err := readLine("URL: ", stdin, stdout)
		if err != nil {
			return "", err
		}
		url = v
	}
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", &exit.CodedError{
			Code: exit.General,
			Msg:  "url must be http:// or https://",
		}
	}
	return url, nil
}

// openAction is the resolved decision for how to surface the login URL.
type openAction int

const (
	// actOpen shells out to the browser opener.
	actOpen openAction = iota
	// actPrint prints the URL and waits (remote/headless) — no shell-out.
	actPrint
	// actCancel aborts the login before polling.
	actCancel
)

// resolvePreOpen decides whether to open the browser, print-and-wait, or
// cancel. `--no-browser` is the explicit non-interactive override → always
// actPrint. Otherwise, on a fully interactive TTY (both stdin and stdout),
// the user is prompted; on any non-interactive session (pipe / CI / test)
// the legacy auto-open behavior (actOpen) is kept so nothing blocks on a
// prompt that can never be answered.
func resolvePreOpen(noBrowser bool, stdin io.Reader, stdout io.Writer) openAction {
	if noBrowser {
		return actPrint
	}
	if !isTerminal(stdin) || !isTerminal(stdout) {
		return actOpen
	}
	return promptPreOpen(stdin, stdout)
}

// promptPreOpen renders the three-way menu and maps the reply to an
// openAction. Split out from resolvePreOpen (which owns the TTY gate) so
// the parse can be unit-tested with plain buffers. Empty input (Enter) or
// any unrecognized token → actOpen, the safe default.
func promptPreOpen(stdin io.Reader, stdout io.Writer) openAction {
	_, _ = fmt.Fprintln(stdout, "How would you like to complete login?")
	_, _ = fmt.Fprintln(stdout, "  1) Open the login page in your browser")
	_, _ = fmt.Fprintln(stdout, "  2) Remote / no browser — print the URL and wait (--no-browser)")
	_, _ = fmt.Fprintln(stdout, "  3) Cancel")
	_, _ = fmt.Fprint(stdout, "> ")

	choice := "1"
	s := bufio.NewScanner(stdin)
	if s.Scan() {
		if t := strings.TrimSpace(s.Text()); t != "" {
			choice = t
		}
	}
	switch choice {
	case "2":
		return actPrint
	case "3":
		return actCancel
	default:
		return actOpen
	}
}

func init() {
	rootCmd.AddCommand(newLoginCmd())
}
