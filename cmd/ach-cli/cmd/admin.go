// SPDX-License-Identifier: Apache-2.0

// `ach admin` is the operator-facing escape hatch for revoking
// misplaced keys and forcing a content refresh outside the §10.3
// hourly cycle. Three sub-subcommands (2-level parent-with-children
// per Pattern P3 for `keys revoke` + `users revoke-keys`; flat leaf
// for `refresh`):
//
//   - ach admin keys revoke <key-id>             — pkid_… or ekid_…
//   - ach admin users revoke-keys <email>        — bulk per-user revoke
//   - ach admin refresh <kind> <name>            — force-refresh CR
//
// CLI-10: every endpoint exits 3 on `403 not_admin` / `403
// unauthorized_team` / `401 invalid_key` — exit.MapServerError owns
// the translation (Pattern P6). Exit 6 on 503/504; exit 0 on 200;
// exit 1 on client-side validation failure.
//
// CLI-13: `keys revoke` accepts BOTH `pkid_…` AND `ekid_…` key IDs;
// raw `pk-…`/`ek-…` plaintext is rejected client-side BEFORE any HTTP
// call (prevents a misplaced plaintext from landing in the audit
// event Target / appearing in shell history).
//
// D-CONTEXT W3b / spec §15.5: `refresh` validates `kind` against the
// closed set {plugin, prompt, artifact, marketplace}. Other kinds
// the server-side handler supports (`environment`,
// `backendidentitypolicy`, future) are rejected client-side with
// exit 1 — the user-facing CLI deliberately does NOT surface them in
// v1alpha1.
//
// Synthetic mode (CLI-07): admin works normally — admin endpoints
// accept pk- only, and a synthetic pk- + allowlisted email behaves
// identically to a config-loaded pk-. The synthetic.GuardCommand
// call uses GateAdmin to gate --profile / --env-key / half-set
// per the cross-gate rules (see 06-07 SUMMARY for the matrix).
//
// Pattern S5 (no plaintext through logs): the API key flows ONLY
// into httpclient.Client.APIKey; verbose-mode header dumps redact
// `x-ach-key` to `<prefix>_***` via httpclient.Redact (CLI-04).

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/yaml"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/render"
	"github.com/ackstorm/ach/internal/cli/synthetic"
	"github.com/ackstorm/ach/internal/keys"
)

// adminConfirmYes is the string literal users type to confirm a
// destructive admin operation at the interactive y/N prompt. Hoisted
// to a constant so the two prompt sites (keys revoke + users
// revoke-keys) share a single source of truth and goconst stays
// happy.
const adminConfirmYes = "yes"

// adminCredFlags bundles the standard credential-set flags every
// admin subcommand exposes. Hoisted into one type + one
// registration helper so the per-subcommand cobra.Command struct
// stays small (cobra defaults + RunE only) and dupl doesn't trip
// on the otherwise-identical flag declaration blocks across the
// three subcommands.
type adminCredFlags struct {
	Yes     bool
	Profile string
	APIKey  string
	EnvKey  string
	Verbose bool
}

// registerAdminCredFlags wires the standard credential-set flags on
// the given cobra.Command. `withYes=false` for `refresh` (idempotent
// operation — no confirmation prompt). All other admin subcommands
// pass `withYes=true`.
func registerAdminCredFlags(cmd *cobra.Command, f *adminCredFlags, withYes bool) {
	if withYes {
		cmd.Flags().BoolVar(&f.Yes, "yes", false, "Bypass interactive confirmation")
	}
	cmd.Flags().StringVar(&f.Profile, "profile", "", "Override profile selection")
	cmd.Flags().StringVar(&f.APIKey, "api-key", "", "Override pk- from flag")
	cmd.Flags().StringVar(&f.EnvKey, "env-key", "", "Override with stored ek- label")
	cmd.Flags().BoolVar(&f.Verbose, "verbose", false,
		"Dump request headers to stderr (x-ach-key redacted)")
}

// adminConfirm prompts on the given writer (typically stderr) and
// reads a single line from stdin. Returns nil when the user typed
// y/Y/yes; otherwise returns the "cancelled" CodedError so the
// caller can bubble it up unchanged. The `--yes` short-circuit is
// implemented by the caller (skip the call entirely when yes==true).
func adminConfirm(stdin io.Reader, w io.Writer, prompt string) error {
	_, _ = fmt.Fprint(w, prompt)
	scanner := bufio.NewScanner(stdin)
	answer := ""
	if scanner.Scan() {
		answer = strings.ToLower(strings.TrimSpace(scanner.Text()))
	}
	switch answer {
	case "y", adminConfirmYes:
		return nil
	default:
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "cancelled",
		}
	}
}

// adminHTTPClient is the test-only seam: when non-nil it replaces the
// default *http.Client inside the httpclient.Client constructed by
// each admin subcommand. Tests targeting httptest.NewTLSServer set
// this to the test server's TLS-trusting Client so the call reaches
// the ephemeral cert. Mirrors the env_keys/whoami/login pattern from
// 06-03 / 06-05.
var adminHTTPClient *http.Client

// swapAdminHTTPClientForTest swaps adminHTTPClient for the lifetime
// of t. Test-only helper.
func swapAdminHTTPClientForTest(t interface {
	Helper()
	Cleanup(func())
}, c *http.Client) {
	t.Helper()
	previous := adminHTTPClient
	adminHTTPClient = c
	t.Cleanup(func() { adminHTTPClient = previous })
}

// allowedRefreshKinds is the closed-set client-side allow-list per
// D-CONTEXT W3b. The server-side handler additionally accepts
// `pluginmarketplace` (with the canonical kind name) — we surface
// the user-facing name `marketplace` and map it server-side via the
// `kind` request body. For symmetry with the user-facing spec we
// only accept the four names here; other kinds the server might
// support are intentionally NOT exposed.
var allowedRefreshKinds = map[string]struct{}{
	"plugin":      {},
	"prompt":      {},
	"artifact":    {},
	"marketplace": {},
}

// adminRevokeKeyResponse mirrors admin.revokeKeyResponse on the wire.
type adminRevokeKeyResponse struct {
	KeyID  string `json:"key_id"`
	Status string `json:"status"`
}

// adminUserRevokeResponse mirrors admin.userRevokeResponse on the wire.
type adminUserRevokeResponse struct {
	RevokedCount int      `json:"revoked_count"`
	Errors       []string `json:"errors"`
}

// adminRefreshResponse is the body of POST /platform/admin/refresh.
// The server returns {"status":"accepted"} (or empty body in some
// branches); we accept both via the optional field.
type adminRefreshResponse struct {
	Status string `json:"status,omitempty"`
}

// newAdminCmd returns a fresh `ach admin` parent with its three
// children registered. Factory shape (mirrors 06-03/06-04/06-05
// newXCmd factories) so tests construct a hermetic cobra subtree per
// t.Run without cross-test global cobra state leaks.
func newAdminCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "admin",
		Short: "Admin operations (key revocation, force-refresh) — requires allowlisted pk-",
		Long: `Operator-facing admin surface. Every subcommand requires a pk- whose
owner email is in the Platform API allowlist (` + "`" + `ACH_ADMIN_ALLOWLIST` + "`" + `
or the equivalent Helm value). Non-allowlisted callers receive
` + "`403 not_admin`" + ` and the CLI exits 3 (CLI-10).

Subcommands:
  keys revoke <key-id>             Revoke a key by ID (pkid_… or ekid_…).
                                    Raw pk-…/ek-… plaintext is rejected
                                    client-side (CLI-13).
  users revoke-keys <email>        Revoke ALL keys owned by <email>.
                                    Returns {revoked_count, errors}.
  refresh <kind> <name>            Force-refresh an external content
                                    resource. kind ∈ {plugin, prompt,
                                    artifact, marketplace}.
  list <kind|all>                  Read-only inventory of ACH objects
                                    (version + sync status). -o table|json|yaml.
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	parent.AddCommand(
		newAdminKeysCmd(),
		newAdminUsersCmd(),
		newAdminRefreshCmd(),
		newAdminListCmd(),
	)
	return parent
}

// ---------------------------------------------------------------------
// list (read-only object inventory)
// ---------------------------------------------------------------------

// adminListKinds is the closed set of inventory kinds, also the fan-out set
// for `ach admin list all`. Order here is the order `all` renders sections.
var adminListKinds = []string{
	"environments", "plugins", "prompts", "artifacts",
	"marketplaces", "bips", "litellm-connections", "external-refs",
}

func isAdminListKind(k string) bool {
	for _, x := range adminListKinds {
		if x == k {
			return true
		}
	}
	return false
}

// adminEnvItem decodes the subset of GET /platform/environments
// (store.EnvironmentView) the inventory needs. environments has no
// /platform/admin route — an allowlisted pk- sees every row via that handler's
// admin bypass, so the CLI reuses it and maps the result into AdminObjectView.
type adminEnvItem struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	ResourceVersion string `json:"resourceVersion"`
	Origin          string `json:"origin"`
	Locked          bool   `json:"locked"`
}

// envStatusAvailable is the derived Environment status string meaning the
// Available composite condition is True (see store.deriveStatus).
const envStatusAvailable = "Available"

// envStatusToSync collapses the derived Environment Available status into the
// inventory SYNC vocabulary: "Available" stays, a non-empty reason → Degraded
// (reason surfaced), empty/unknown → Pending.
func envStatusToSync(status string) (sync, reason string) {
	switch status {
	case "":
		return "Pending", ""
	case envStatusAvailable:
		return envStatusAvailable, ""
	default:
		return "Degraded", status
	}
}

func (e adminEnvItem) toView() render.AdminObjectView {
	sync, reason := envStatusToSync(e.Status)
	return render.AdminObjectView{
		Kind:       "environment",
		Namespace:  e.Namespace,
		Name:       e.Name,
		Version:    e.ResourceVersion,
		Sync:       sync,
		SyncReason: reason,
		Origin:     e.Origin,
		Locked:     e.Locked,
	}
}

func newAdminListCmd() *cobra.Command {
	f := &adminCredFlags{}
	var output string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List ACH objects (read-only inventory). Usage: ach admin list <kind|all>",
		Long: `Read-only inventory of ACH-defined objects sourced from the Postgres
projections (version + projection-derived sync status). Admin-only (pk-).

kind ∈ {environments, plugins, prompts, artifacts, marketplaces, bips,
litellm-connections, external-refs} or 'all' to fan out across every kind.

SYNC column:
  Available / Degraded(<reason>) / Pending   environments (Available condition)
  fresh / STALE(<age> over) / never          content kinds (refresh staleness)
  projected                                  bips / litellm-connections

Note: prompts/artifacts show 'fresh*' — their refresh tracks name resolution,
not content presence (only plugins are truly content-gated).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminList(cmd, args[0], output, f)
		},
	}
	// withYes=false — read-only, no confirmation prompt.
	registerAdminCredFlags(cmd, f, false)
	cmd.Flags().StringVarP(&output, "output", "o", "table", "Output format: table|json|yaml")
	return cmd
}

func runAdminList(cmd *cobra.Command, kind, output string, f *adminCredFlags) error {
	stderr := cmd.ErrOrStderr()
	stdout := cmd.OutOrStdout()
	ctx := cmd.Context()

	// CLI-07 synthetic gate (admin allowed in synthetic).
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateAdmin,
		APIKeyFlag:  f.APIKey,
		EnvKeyFlag:  f.EnvKey,
		ProfileFlag: f.Profile,
	}); err != nil {
		return err
	}

	kind = strings.TrimSpace(kind)
	if kind != "all" && !isAdminListKind(kind) {
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf("invalid kind %q; expected one of %s, or 'all'",
				kind, strings.Join(adminListKinds, ", ")),
		}
	}
	switch output {
	case "table", "json", "yaml":
	default:
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("invalid --output %q; expected table, json, or yaml", output),
		}
	}

	baseURL, bearer, err := resolveAdminBearer(f.Profile, f.APIKey, f.EnvKey)
	if err != nil {
		return err
	}
	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		HTTPClient: adminHTTPClient,
		Verbose:    f.Verbose,
		Stderr:     stderr,
	}

	grouped := map[string][]render.AdminObjectView{}
	if kind == "all" {
		results := make([][]render.AdminObjectView, len(adminListKinds))
		g, gctx := errgroup.WithContext(ctx)
		for i, k := range adminListKinds {
			g.Go(func() error {
				rows, e := fetchAdminKind(gctx, hc, k)
				if e != nil {
					return e
				}
				results[i] = rows
				return nil
			})
		}
		if waitErr := g.Wait(); waitErr != nil {
			return waitErr
		}
		for i, k := range adminListKinds {
			grouped[k] = results[i]
		}
	} else {
		rows, e := fetchAdminKind(ctx, hc, kind)
		if e != nil {
			return e
		}
		grouped[kind] = rows
	}

	return renderAdminList(stdout, grouped, output)
}

// fetchAdminKind pages through one kind's endpoint (cursor loop mirroring
// runEnvKeysList) and returns the accumulated AdminObjectViews. environments
// is special-cased onto GET /platform/environments + the EnvironmentView map.
func fetchAdminKind(ctx context.Context, hc *httpclient.Client, kind string) ([]render.AdminObjectView, error) {
	out := []render.AdminObjectView{}
	cursor := ""
	for {
		path := buildAdminListPath(kind, cursor)
		if kind == "environments" {
			var resp struct {
				Items      []adminEnvItem `json:"items"`
				NextCursor string         `json:"next_cursor"`
			}
			if err := hc.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
				return nil, err
			}
			for _, it := range resp.Items {
				out = append(out, it.toView())
			}
			if resp.NextCursor == "" {
				break
			}
			cursor = resp.NextCursor
			continue
		}
		var resp struct {
			Items      []render.AdminObjectView `json:"items"`
			NextCursor string                   `json:"next_cursor"`
		}
		if err := hc.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Items...)
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	return out, nil
}

// buildAdminListPath returns the endpoint + optional cursor query for a kind.
func buildAdminListPath(kind, cursor string) string {
	base := "/platform/admin/" + kind
	if kind == "environments" {
		base = "/platform/environments"
	}
	if cursor == "" {
		return base
	}
	q := url.Values{}
	q.Set("cursor", cursor)
	return base + "?" + q.Encode()
}

// renderAdminList writes the grouped inventory in the requested format. table
// goes through the render formatter (Pattern S5 — no inline tabwriter here);
// json/yaml marshal the map directly so machine consumers get the full DTO.
func renderAdminList(stdout io.Writer, grouped map[string][]render.AdminObjectView, output string) error {
	switch output {
	case "json":
		b, err := json.MarshalIndent(grouped, "", "  ")
		if err != nil {
			return &exit.CodedError{Code: exit.General, Msg: "marshal json: " + err.Error()}
		}
		_, _ = stdout.Write(b)
		_, _ = io.WriteString(stdout, "\n")
	case "yaml":
		b, err := yaml.Marshal(grouped)
		if err != nil {
			return &exit.CodedError{Code: exit.General, Msg: "marshal yaml: " + err.Error()}
		}
		_, _ = stdout.Write(b)
	default: // table
		_, _ = io.WriteString(stdout, render.FormatAdminInventory(grouped))
	}
	return nil
}

// ---------------------------------------------------------------------
// keys → revoke
// ---------------------------------------------------------------------

// newAdminKeysCmd returns the intermediate `ach admin keys` parent
// with its single child `revoke`. Two-level nesting per Pattern P3
// because the spec surface is `ach admin keys revoke <key-id>` —
// keys is a noun-grouping under admin, revoke is the verb.
func newAdminKeysCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "keys",
		Short: "Admin key operations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	parent.AddCommand(newAdminKeysRevokeCmd())
	return parent
}

func newAdminKeysRevokeCmd() *cobra.Command {
	f := &adminCredFlags{}
	// SilenceUsage + SilenceErrors per Pattern S5 — cobra would
	// otherwise echo its Usage block (containing flag descriptions
	// referencing pk-/ek-) to the SetOut writer on a non-nil RunE
	// return. cmd/ach/main.go owns the err render via the typed-
	// error dispatch (Pattern P12).
	cmd := &cobra.Command{
		Use:           "revoke",
		Short:         "Revoke a key by ID (pkid_… or ekid_…). Usage: ach admin keys revoke <key-id>",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminKeysRevoke(cmd, args[0], f)
		},
	}
	registerAdminCredFlags(cmd, f, true)
	return cmd
}

func runAdminKeysRevoke(cmd *cobra.Command, keyID string, f *adminCredFlags) error {
	stderr := cmd.ErrOrStderr()
	stdout := cmd.OutOrStdout()
	stdin := cmd.InOrStdin()
	ctx := cmd.Context()

	// CLI-07 synthetic gate (admin allowed in synthetic; --profile /
	// --env-key / half-set still rejected per the cross-gate rules).
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateAdmin,
		APIKeyFlag:  f.APIKey,
		EnvKeyFlag:  f.EnvKey,
		ProfileFlag: f.Profile,
	}); err != nil {
		return err
	}

	// CLI-13: client-side key-id classification BEFORE any HTTP call.
	if err := validateAdminKeyID(keyID); err != nil {
		return err
	}

	if !f.Yes {
		if err := adminConfirm(stdin, stderr,
			fmt.Sprintf("Revoke key %s ? (y/N): ", keyID)); err != nil {
			return err
		}
	}

	baseURL, bearer, err := resolveAdminBearer(f.Profile, f.APIKey, f.EnvKey)
	if err != nil {
		return err
	}

	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		HTTPClient: adminHTTPClient,
		Verbose:    f.Verbose,
		Stderr:     stderr,
	}

	body := struct {
		KeyID string `json:"key_id"`
	}{KeyID: keyID}
	var resp adminRevokeKeyResponse
	if doErr := hc.Do(ctx, http.MethodPost, "/platform/admin/keys/revoke", body, &resp); doErr != nil {
		return doErr
	}
	_, _ = fmt.Fprintf(stdout, "Revoked %s (status: %s)\n", resp.KeyID, resp.Status)
	return nil
}

// validateAdminKeyID enforces the CLI-13 client-side classification:
// only pkid_ / ekid_ key IDs are accepted; raw pk- / ek- plaintext is
// rejected with a clear message; everything else is rejected as
// invalid. Returns nil when the key ID is well-formed.
func validateAdminKeyID(keyID string) error {
	switch {
	case strings.HasPrefix(keyID, keys.PkidKeyIDPrefix),
		strings.HasPrefix(keyID, keys.EkidKeyIDPrefix):
		return nil
	case strings.HasPrefix(keyID, keys.PkBearerPrefix),
		strings.HasPrefix(keyID, keys.EkBearerPrefix):
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf(
				"refusing plaintext key — pass the key id (%s or %s) instead (CLI-13)",
				keys.PkidKeyIDPrefix, keys.EkidKeyIDPrefix),
		}
	default:
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf(
				"invalid key id %q; expected %s or %s prefix",
				keyID, keys.PkidKeyIDPrefix, keys.EkidKeyIDPrefix),
		}
	}
}

// ---------------------------------------------------------------------
// users → revoke-keys
// ---------------------------------------------------------------------

func newAdminUsersCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "users",
		Short: "Admin user operations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	parent.AddCommand(newAdminUsersRevokeKeysCmd())
	return parent
}

func newAdminUsersRevokeKeysCmd() *cobra.Command {
	f := &adminCredFlags{}
	cmd := &cobra.Command{
		Use:           "revoke-keys",
		Short:         "Revoke ALL keys owned by <email>. Usage: ach admin users revoke-keys <email>",
		Long:          "Bulk-revoke every pk- and ek- owned by the given email. Returns {revoked_count, errors}.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminUsersRevokeKeys(cmd, args[0], f)
		},
	}
	registerAdminCredFlags(cmd, f, true)
	return cmd
}

func runAdminUsersRevokeKeys(cmd *cobra.Command, email string, f *adminCredFlags) error {
	stderr := cmd.ErrOrStderr()
	stdout := cmd.OutOrStdout()
	stdin := cmd.InOrStdin()
	ctx := cmd.Context()

	// CLI-07 synthetic gate (admin allowed in synthetic).
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateAdmin,
		APIKeyFlag:  f.APIKey,
		EnvKeyFlag:  f.EnvKey,
		ProfileFlag: f.Profile,
	}); err != nil {
		return err
	}

	email = strings.TrimSpace(email)
	if email == "" || !strings.Contains(email, "@") {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("invalid email %q", email),
		}
	}

	if !f.Yes {
		if err := adminConfirm(stdin, stderr,
			fmt.Sprintf("Revoke ALL keys owned by %s ? (y/N): ", email)); err != nil {
			return err
		}
	}

	baseURL, bearer, err := resolveAdminBearer(f.Profile, f.APIKey, f.EnvKey)
	if err != nil {
		return err
	}

	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		HTTPClient: adminHTTPClient,
		Verbose:    f.Verbose,
		Stderr:     stderr,
	}

	// URL-escape the email so `+` / `@` / `.` survive the wire path
	// (T-06-08-07 path-injection mitigation). The server-side handler
	// decodes via url.PathUnescape (see internal/platformapi/admin/
	// handler.go RevokeUserKeysHandler).
	escaped := url.PathEscape(email)
	path := "/platform/admin/users/" + escaped + "/revoke-keys"
	// Body is empty {} per the spec — the email lives in the URL path.
	var resp adminUserRevokeResponse
	if doErr := hc.Do(ctx, http.MethodPost, path, struct{}{}, &resp); doErr != nil {
		return doErr
	}
	_, _ = fmt.Fprintf(stdout, "Revoked %d keys owned by %s\n", resp.RevokedCount, email)
	for _, e := range resp.Errors {
		_, _ = fmt.Fprintf(stdout, "  - %s\n", e)
	}
	return nil
}

// ---------------------------------------------------------------------
// refresh
// ---------------------------------------------------------------------

func newAdminRefreshCmd() *cobra.Command {
	f := &adminCredFlags{}
	cmd := &cobra.Command{
		Use: "refresh",
		Short: "Force-refresh an external content resource. " +
			"Usage: ach admin refresh <kind> <name>",
		Long: "kind must be one of {plugin, prompt, artifact, marketplace}. " +
			"No interactive confirmation (idempotent operation).",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminRefresh(cmd, args[0], args[1], f)
		},
	}
	// withYes=false — refresh is idempotent / non-destructive, no prompt.
	registerAdminCredFlags(cmd, f, false)
	return cmd
}

func runAdminRefresh(cmd *cobra.Command, kind, name string, f *adminCredFlags) error {
	stderr := cmd.ErrOrStderr()
	stdout := cmd.OutOrStdout()
	ctx := cmd.Context()

	// CLI-07 synthetic gate (admin allowed in synthetic).
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateAdmin,
		APIKeyFlag:  f.APIKey,
		EnvKeyFlag:  f.EnvKey,
		ProfileFlag: f.Profile,
	}); err != nil {
		return err
	}

	// D-CONTEXT W3b: closed-set client-side validation. Even though the
	// server-side ForceRefreshHandler supports additional kinds (e.g.
	// pluginmarketplace) that v1alpha1 doesn't expose on the CLI, the
	// user-facing surface is intentionally limited to four. Phase 7
	// can lift this gate if/when additional kinds are surfaced.
	if _, ok := allowedRefreshKinds[kind]; !ok {
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf(
				"kind must be one of: plugin, prompt, artifact, marketplace; got: %s",
				kind),
		}
	}

	if strings.TrimSpace(name) == "" {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "name is required",
		}
	}

	baseURL, bearer, err := resolveAdminBearer(f.Profile, f.APIKey, f.EnvKey)
	if err != nil {
		return err
	}

	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		HTTPClient: adminHTTPClient,
		Verbose:    f.Verbose,
		Stderr:     stderr,
	}

	body := struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}{Kind: kind, Name: name}
	var resp adminRefreshResponse
	if doErr := hc.Do(ctx, http.MethodPost, "/platform/admin/refresh", body, &resp); doErr != nil {
		return doErr
	}
	_, _ = fmt.Fprintf(stdout, "Refresh requested: %s/%s\n", kind, name)
	return nil
}

// ---------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------

// resolveAdminBearer is a thin alias for resolveEnvKeysBearer
// (06-05). The precedence + sentinel-error shape is identical
// because admin shares env-keys' credential surface verbatim:
// synthetic → --api-key → --env-key → ACH_API_KEY → ACH_ENV_KEY →
// disk dep.PK. Pattern S5: bearer flows ONLY into
// httpclient.Client.APIKey; never into a print/log call.
//
// We accept --env-key here for compositional parity even though
// admin endpoints reject ek- at the server side (AdminOnly
// middleware in internal/platformapi/admin/mount.go) — the server
// emits a clear `invalid_key_type` outcome and the CLI maps it to
// exit 3 via MapServerError, which is the correct user experience
// (the user is told they used the wrong key type).
//
// Aliasing rather than duplicating avoids drift and satisfies dupl
// without hoisting the whole resolver into a new `internal/cli/`
// package for two callers. When a third caller appears (Phase 7?)
// the natural next step is to lift this into `internal/cli/cred/`.
func resolveAdminBearer(flagProfile, flagAPIKey, flagEnvKey string) (string, string, error) {
	return resolveEnvKeysBearer(flagProfile, flagAPIKey, flagEnvKey)
}

// Register `ach admin` on the root command. Mirrors the env-keys /
// login / whoami pattern from 06-03 / 06-05 — each subcommand owns
// its own init() so cobra registration is local to the file.
func init() {
	rootCmd.AddCommand(newAdminCmd())
}
