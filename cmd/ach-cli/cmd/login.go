// SPDX-License-Identifier: Apache-2.0

// `ach-cli login` signs in to a Hub through its OAuth 2.1 AS. Two ways to
// finish the browser step — a loopback redirect on this machine, or the
// RFC 8628 device grant from any browser — one credential: the profile
// stores the access + refresh token pair; `ach-cli token` prints a fresh
// access token. No key material is ever printed.
//
// Synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) refuses to run with
// exit 1; internal/cli/synthetic enforces, login asserts.

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/oauthlogin"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// profileNamePattern enforces DNS-1123-style names so the config
// key namespace stays well-formed (path-safe, yaml-key-safe).
var profileNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

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
		flagInsecure   bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to a Hub (browser on this machine, or a code from any browser)",
		Long: `Sign in to an ACH Hub.

The CLI is a public OAuth client of the Hub's authorization server. After
the profile-name and URL prompts, on a terminal it asks how to finish:

  1) Open the browser on this machine — the code comes back on a loopback
     redirect, nothing to type.
  2) Another device — the Hub shows a code; open the printed URL in any
     browser, confirm the code, and the CLI picks the login up (RFC 8628
     device grant). This is the way in from an SSH host.
  3) Cancel.

Either way the profile stores a short-lived access token + refresh token;
` + "`ach-cli token`" + ` prints a fresh access token for tools' credential
helpers. Nothing key-shaped is printed.

Interactive prompts (skipped when --profile / --base-url are set):
  Profile name  DNS-1123 label, e.g. "prod" (suggests "default" on first login)
  URL           https://hub.example.com (http:// needs --insecure / ACH_INSECURE)

The URL prompt is also pre-filled from ACH_PLATFORM_URL when set
(precedence: --base-url flag → ACH_PLATFORM_URL env → prompt).
ACH_PLATFORM_URL is a login-only convenience, distinct from ACH_BASE_URL
(which activates synthetic mode) — it never enables synthetic mode.

Synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) refuses to run
with exit 1.

Flags:
  --profile <name>   Skip the profile-name prompt
  --base-url <url>   Skip the URL prompt (http:// or https://)
  --no-browser       Skip the menu: show the code and wait (option 2)
  --no-warnings      Suppress config-file file-mode warnings to stderr
  --insecure         Allow a plaintext http:// Hub URL
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd, flagProfile, flagBaseURL, flagNoBrowser, flagNoWarnings, flagInsecure)
		},
	}

	cmd.Flags().StringVar(&flagProfile, "profile", "", "Profile name to write (DNS-1123 label)")
	cmd.Flags().StringVar(&flagBaseURL, "base-url", "", "Hub URL (http:// or https://)")
	cmd.Flags().BoolVar(&flagNoBrowser, "no-browser", false,
		"Show a code to enter in a browser on another device; do not open a browser here")
	cmd.Flags().BoolVar(&flagNoWarnings, "no-warnings", false, "Suppress file-mode warnings to stderr")
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false,
		"Allow a plaintext http:// Hub URL (credentials sent unencrypted; localhost still requires this)")

	return cmd
}

// runLogin is the RunE body, extracted so newLoginCmd's closure stays
// short.
func runLogin(cmd *cobra.Command, profile, baseURL string, noBrowser, noWarnings, insecure bool) error {
	ctx := cmd.Context()

	// Step 1 — synthetic-mode gate. GateLogin denies under synthetic; the
	// same call also rejects half-set (ACH_BASE_URL set without credential)
	// before any request fires.
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateLogin,
		ProfileFlag: profile,
	}); err != nil {
		return err
	}

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	stdin := cmd.InOrStdin()

	// Step 2 — load existing config first (best effort; nil-on-absent
	// OK). Loaded before the profile prompt so the prompt can suggest
	// the "default" profile name on a true first login (no profile
	// literally named "default" yet).
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
	file, err := config.LoadWithInsecure(configPath, warn, config.InsecureFromEnv())
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

	// Step 3 — resolve profile name (flag or interactive prompt). On a
	// first login (no profile literally named "default" yet) the prompt
	// suggests "default" so a bare Enter accepts it.
	suggested := ""
	if file.Profiles["default"] == nil {
		suggested = "default"
	}
	name, err := resolveProfileName(profile, suggested, stdin, stdout)
	if err != nil {
		return err
	}

	// Step 4 — resolve URL (flag or interactive prompt). Accepts
	// http:// or https://.
	url, err := resolveBaseURL(baseURL, stdin, stdout)
	if err != nil {
		return err
	}
	// G19: refuse a plaintext http:// Hub URL unless the user opted into
	// insecure transport (--insecure flag OR ACH_INSECURE env). localhost is
	// NOT exempt (decision B). https:// always proceeds.
	if err := config.ValidateSecureURL(url, insecure || config.InsecureFromEnv()); err != nil {
		return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}

	// Step 5 — how to finish the browser step.
	var creds *config.OAuthCreds
	client := &oauthlogin.Client{BaseURL: url}
	existing := file.Profiles[name]
	clientID := ""
	if existing != nil && existing.OAuth != nil && existing.URL == url {
		clientID = existing.OAuth.ClientID // cached DCR id; "" re-registers
	}
	switch resolvePreOpen(noBrowser, stdin, stdout) {
	case actCancel:
		return &exit.CodedError{Code: exit.General, Msg: "login canceled"}
	case actOpen:
		_, _ = fmt.Fprintln(stdout, "Opening your browser to sign in…")
		creds, err = client.Login(ctx, clientID)
	case actPrint:
		creds, err = client.DeviceLogin(ctx, clientID, func(code, uri string) {
			_, _ = fmt.Fprintf(stdout, "\nOpen %s and enter the code:\n\n    %s\n\nWaiting for you to sign in…\n", uri, code)
		})
	}
	if err != nil {
		return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("login: %v", err), Wrapped: err}
	}

	// Step 6 — save. Only the profile's own credential changes; any EK map
	// on it is kept.
	dep := &config.Profile{URL: url, OAuth: creds}
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
	_, _ = fmt.Fprintf(stdout, "Logged in (profile %q); run `ach-cli token` to print an access token\n", name)
	return nil
}

// resolveProfileName returns the flag value when set; otherwise prompts
// via stdin. When suggested is non-empty (a first login with no profile
// named "default" yet) the prompt renders "Profile name [suggested]: "
// and a bare Enter (empty input) accepts the suggestion. Validates the
// final name against the DNS-1123 label pattern.
func resolveProfileName(flagVal, suggested string, stdin io.Reader, stdout io.Writer) (string, error) {
	name := strings.TrimSpace(flagVal)
	if name == "" {
		prompt := "Profile name: "
		if suggested != "" {
			prompt = fmt.Sprintf("Profile name [%s]: ", suggested)
		}
		v, err := readLine(prompt, stdin, stdout)
		if err != nil {
			return "", err
		}
		name = strings.TrimSpace(v)
		if name == "" {
			name = suggested
		}
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

// resolveBaseURL returns the flag value when set; otherwise pre-fills
// from ACH_PLATFORM_URL; otherwise prompts. Precedence: --base-url flag →
// ACH_PLATFORM_URL env → interactive prompt. ACH_PLATFORM_URL is a
// login-only convenience and is NOT the synthetic-mode trigger
// (ACH_BASE_URL); it never enables synthetic mode. Accepts http:// or
// https://; rejects any other scheme. http:// is allowed for
// local/internal hubs — runLogin emits a plaintext-transport warning
// when the resolved URL is http://.
func resolveBaseURL(flagVal string, stdin io.Reader, stdout io.Writer) (string, error) {
	url := strings.TrimSpace(flagVal)
	if url == "" {
		// Env pre-fill: ACH_PLATFORM_URL is a login-only convenience,
		// distinct from ACH_BASE_URL (the synthetic-mode trigger).
		if env := strings.TrimSpace(os.Getenv("ACH_PLATFORM_URL")); env != "" {
			url = env
			_, _ = fmt.Fprintf(stdout, "URL: %s (read from env:ACH_PLATFORM_URL)\n", url)
		}
	}
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
	// actOpen: loopback flow, the browser on this machine.
	actOpen openAction = iota
	// actPrint: device grant — show a code, poll (remote/headless).
	actPrint
	// actCancel aborts the login before polling.
	actCancel
)

// resolvePreOpen decides between the loopback flow (actOpen), the device
// grant (actPrint) and cancel. `--no-browser` is the explicit
// non-interactive override → actPrint. Otherwise, on a fully interactive
// TTY (both stdin and stdout) the user is asked; on any non-interactive
// session (pipe / CI / test) actOpen, so nothing blocks on a prompt that
// can never be answered.
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
	_, _ = fmt.Fprintln(stdout, "How would you like to sign in?")
	_, _ = fmt.Fprintln(stdout, "  1) Open the browser on this machine")
	_, _ = fmt.Fprintln(stdout, "  2) Another device — show a code to enter in a browser (--no-browser)")
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
