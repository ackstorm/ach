// SPDX-License-Identifier: Apache-2.0

// `ach-cli admin` is the operator-facing surface:
//
//   - admin list <kind|all>                     — ACH objects + LiteLLM runtime catalog
//   - admin keys list / revoke <id> / revoke --owner <email>
//   - admin users budget|limits <email>
//   - admin refresh <kind> <name>               — force-refresh CR
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
// Credentials: every child resolves through resolveCred (--profile /
// --key / synthetic mode).
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
	"slices"
	"strings"
	"text/tabwriter"

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
// to a constant so the confirmation prompt sites (`keys revoke`,
// `admin keys revoke`, `admin keys revoke --owner`) share a single
// source of truth and goconst stays happy.
const adminConfirmYes = "yes"

// statusAll is the sentinel value for "--status all" / kind "all" that
// disables the filter (passes no value to the server). Hoisted to avoid
// repeated "all" literals across runAdminList + buildAdminKeysListPath.
const statusAll = "all"

// outputJSON is the string value for the -o/--output flag that selects
// JSON output. Hoisted to a constant so goconst stays happy across the
// admin + runtime subcommands that share this flag.
const outputJSON = "json"

// adminCredFlags bundles the standard credential-set flags every
// admin subcommand exposes. Hoisted into one type + one
// registration helper so the per-subcommand cobra.Command struct
// stays small (cobra defaults + RunE only) and dupl doesn't trip
// on the otherwise-identical flag declaration blocks across the
// admin subcommands.
type adminCredFlags struct {
	credFlags
	Yes bool
}

// registerAdminCredFlags wires the standard credential-set flags on
// the given cobra.Command. `withYes=false` for `refresh` (idempotent
// operation — no confirmation prompt). All other admin subcommands
// pass `withYes=true`.
func registerAdminCredFlags(cmd *cobra.Command, f *adminCredFlags, withYes bool) {
	if withYes {
		cmd.Flags().BoolVar(&f.Yes, "yes", false, "Bypass interactive confirmation")
	}
	registerCredFlags(cmd, &f.credFlags)
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

// allowedRefreshKinds is the closed-set client-side allow-list per
// D-CONTEXT W3b. The user-facing names `marketplace` / `skill-marketplace`
// map to the server kinds `pluginmarketplace` / `skillmarketplace` in
// runAdminRefresh via refreshKindToServer; `plugin`/`prompt`/`artifact`/
// `skill` pass through unchanged.
var allowedRefreshKinds = map[string]struct{}{
	"plugin":            {},
	"prompt":            {},
	"artifact":          {},
	"skill":             {},
	"marketplace":       {},
	"skill-marketplace": {},
}

// refreshKindToServer maps a user-facing refresh kind to the canonical
// server kind db.SetForceRefresh expects. Entries absent from the map pass
// through verbatim (plugin/prompt/artifact/skill). The two marketplace
// aliases MUST be mapped — sending `marketplace`/`skill-marketplace`
// verbatim would 400 server-side (G8 fix).
var refreshKindToServer = map[string]string{
	"marketplace":       "pluginmarketplace",
	"skill-marketplace": "skillmarketplace",
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

// newAdminCmd returns a fresh `ach-cli admin` parent with its three
// children registered. Factory shape (mirrors 06-03/06-04/06-05
// newXCmd factories) so tests construct a hermetic cobra subtree per
// t.Run without cross-test global cobra state leaks.
func newAdminCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "admin",
		Short: "Admin operations (inventory, keys, user budgets and limits, refresh)",
		Long: `Operator-facing admin surface. Every subcommand requires a signed-in
session (` + "`" + `ach-cli login` + "`" + `) whose owner email is in the Platform API
allowlist (` + "`" + `ACH_ADMIN_ALLOWLIST` + "`" + ` or the equivalent Helm value);
environment keys (ek-) are refused. Non-allowlisted callers receive
` + "`403 not_admin`" + ` and the CLI exits 3.
`,
		RunE: helpOrUnknownSubcommand,
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
// for `ach-cli admin list all`. Order here is the order `all` renders sections.
//
// litellm-connections and external-refs are deliberately excluded: both are
// operator-internal bookkeeping (the LiteLLM connection config singleton and
// the per-CR upstream-refresh cache ledger), not user-declared objects, so
// they only added noise to the inventory. The server-side routes were
// removed 2026-07-15 (audit).
var adminListKinds = []string{
	"environments", "plugins", "prompts", "artifacts", "skills",
	"marketplaces", "skill-marketplaces", "bips",
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

// adminRuntimeKinds are the LiteLLM runtime-catalog kinds `admin list`
// reads from /platform/admin/runtime/<route>, in `all` render order.
var adminRuntimeKinds = []string{"models", "mcp", "a2a", "teams", "guardrails"}

// adminRuntimeRoutes maps a runtime kind to its route segment.
var adminRuntimeRoutes = map[string]string{
	"models":     "models",
	"mcp":        "mcp-servers",
	"a2a":        "a2a-agents",
	"teams":      "teams",
	"guardrails": "guardrails",
}

// runtimeItem is one row of a /platform/admin/runtime/* list.
type runtimeItem struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	// Attributes is kind-specific JSON, present for guardrails only today
	// (mode, defaultOn).
	Attributes json.RawMessage `json:"attributes,omitempty"`
}

func newAdminListCmd() *cobra.Command {
	f := &adminCredFlags{}
	var out outputFlag
	cmd := &cobra.Command{
		Use:   "list <kind|all>",
		Short: "List ACH objects and the LiteLLM runtime catalog (read-only)",
		Long: `Read-only inventory. Admin-only.

ACH objects (from the Postgres projections, version + sync status):
  environments, plugins, prompts, artifacts, skills, marketplaces,
  skill-marketplaces, bips
LiteLLM runtime catalog (KIND NAME STATUS):
  models, mcp, a2a, teams, guardrails
'all' fans out across every kind.

PLUGINS merges standalone Plugin CRs (SOURCE=plugin) with plugins discovered
inside marketplaces (SOURCE=marketplace, shown as <name>@<marketplace>).
SKILLS likewise merges standalone Skill CRs (SOURCE=skill) with skills
discovered inside skill-marketplaces (SOURCE=marketplace, <name>@<marketplace>).
MARKETPLACES / SKILL-MARKETPLACES show the marketplace objects themselves
(Synced status + contained count), not their contained plugins/skills.

SYNC column:
  Synced / Degraded(<reason>) / Pending      marketplaces, skill-marketplaces, environments
  fresh / STALE(<age> over) / never          content kinds (refresh staleness)
  projected                                  bips

Note: prompts/artifacts show 'fresh*' — their refresh tracks name resolution,
not content presence. plugins and skills are truly content-gated (bare 'fresh').
Guardrails add MODE and DEFAULT-ON columns.`,
		Example: `  ach-cli admin list plugins
  ach-cli admin list models -o json
  ach-cli admin list all -o yaml`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminList(cmd, args[0], out.v, f)
		},
	}
	// withYes=false — read-only, no confirmation prompt.
	registerAdminCredFlags(cmd, f, false)
	registerOutputFlag(cmd, &out, "table", outputJSON, "yaml")
	return cmd
}

func runAdminList(cmd *cobra.Command, kind, output string, f *adminCredFlags) error {
	ctx := cmd.Context()

	kind = strings.TrimSpace(kind)
	var objKinds, rtKinds []string
	switch {
	case kind == statusAll:
		objKinds, rtKinds = adminListKinds, adminRuntimeKinds
	case slices.Contains(adminListKinds, kind):
		objKinds = []string{kind}
	case slices.Contains(adminRuntimeKinds, kind):
		rtKinds = []string{kind}
	default:
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf("invalid kind %q; expected one of %s, %s, or 'all'",
				kind, strings.Join(adminListKinds, ", "), strings.Join(adminRuntimeKinds, ", ")),
		}
	}

	c, err := resolveCred(ctx, f.credFlags, synthetic.GateAPI)
	if err != nil {
		return err
	}
	hc := newAPIClient(c.BaseURL, c.Bearer, adminHTTPClient, f.Verbose, cmd.ErrOrStderr())

	objs := make([][]render.AdminObjectView, len(objKinds))
	rts := make([][]runtimeItem, len(rtKinds))
	g, gctx := errgroup.WithContext(ctx)
	for i, k := range objKinds {
		g.Go(func() (e error) {
			objs[i], e = fetchAdminKind(gctx, hc, k)
			return e
		})
	}
	for i, k := range rtKinds {
		g.Go(func() (e error) {
			rts[i], e = fetchAll[runtimeItem](gctx, hc, "", func(cur string) string {
				return withCursor("/platform/admin/runtime/"+adminRuntimeRoutes[k], cur)
			})
			return e
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	stdout := cmd.OutOrStdout()
	if output != "table" {
		grouped := map[string]any{}
		for i, k := range objKinds {
			grouped[k] = objs[i]
		}
		for i, k := range rtKinds {
			grouped[k] = rts[i]
		}
		return renderAdminList(stdout, grouped, output)
	}
	if len(objKinds) > 0 {
		grouped := map[string][]render.AdminObjectView{}
		for i, k := range objKinds {
			grouped[k] = objs[i]
		}
		_, _ = io.WriteString(stdout, render.FormatAdminInventory(grouped))
	}
	if len(rtKinds) > 0 {
		if len(objKinds) > 0 {
			_, _ = io.WriteString(stdout, "\n")
		}
		return writeRuntimeTable(stdout, slices.Concat(rts...))
	}
	return nil
}

// fetchAdminKind pages through one kind's endpoint and returns the
// accumulated AdminObjectViews. environments is special-cased onto
// GET /platform/environments + the EnvironmentView map.
func fetchAdminKind(ctx context.Context, hc *httpclient.Client, kind string) ([]render.AdminObjectView, error) {
	base := "/platform/admin/" + kind
	if kind == "environments" {
		base = pathEnvironments
	}
	pathFor := func(c string) string { return withCursor(base, c) }
	if kind == "environments" {
		items, err := fetchAll[adminEnvItem](ctx, hc, "", pathFor)
		if err != nil {
			return nil, err
		}
		out := make([]render.AdminObjectView, 0, len(items))
		for _, it := range items {
			out = append(out, it.toView())
		}
		return out, nil
	}
	return fetchAll[render.AdminObjectView](ctx, hc, "", pathFor)
}

// withCursor appends an optional cursor query to base.
func withCursor(base, cursor string) string {
	if cursor == "" {
		return base
	}
	return base + "?" + url.Values{"cursor": {cursor}}.Encode()
}

// guardrailAttrs is the attribute JSON the catalog stores for guardrail rows.
type guardrailAttrs struct {
	Mode      []string `json:"mode"`
	DefaultOn bool     `json:"defaultOn"`
}

// writeRuntimeTable renders items as KIND / NAME / STATUS, plus MODE and
// DEFAULT-ON when any row carries guardrail attributes. DEFAULT-ON is the
// decision-relevant column: a default_on guardrail already runs on every
// request, so naming it in an Environment changes nothing.
func writeRuntimeTable(w io.Writer, items []runtimeItem) error {
	showAttrs := slices.ContainsFunc(items, func(it runtimeItem) bool { return len(it.Attributes) > 0 })
	tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
	if showAttrs {
		_, _ = fmt.Fprintln(tw, "KIND\tNAME\tSTATUS\tMODE\tDEFAULT-ON")
	} else {
		_, _ = fmt.Fprintln(tw, "KIND\tNAME\tSTATUS")
	}
	for _, it := range items {
		if !showAttrs {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", it.Kind, it.Name, it.Status)
			continue
		}
		mode, dflt := "-", "-"
		if len(it.Attributes) > 0 {
			var a guardrailAttrs
			if err := json.Unmarshal(it.Attributes, &a); err == nil {
				if len(a.Mode) > 0 {
					mode = strings.Join(a.Mode, ",")
				}
				dflt = "no"
				if a.DefaultOn {
					dflt = adminConfirmYes
				}
			}
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", it.Kind, it.Name, it.Status, mode, dflt)
	}
	return tw.Flush()
}

// renderAdminList writes the grouped inventory as json or yaml, marshalling
// the map directly so machine consumers get the full DTO.
func renderAdminList(stdout io.Writer, grouped map[string]any, output string) error {
	if output == outputJSON {
		return writeJSON(stdout, grouped)
	}
	b, err := yaml.Marshal(grouped)
	if err != nil {
		return &exit.CodedError{Code: exit.General, Msg: "marshal yaml: " + err.Error()}
	}
	_, _ = stdout.Write(b)
	return nil
}

// ---------------------------------------------------------------------
// keys → revoke
// ---------------------------------------------------------------------

// newAdminKeysCmd returns the intermediate `ach-cli admin keys` parent
// with its children `revoke` and `list`. Two-level nesting per Pattern P3
// because the spec surface is `ach-cli admin keys revoke <key-id>` /
// `ach-cli admin keys list` — keys is a noun-grouping under admin.
func newAdminKeysCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "keys",
		Short: "Admin key operations",
		RunE:  helpOrUnknownSubcommand,
	}
	parent.AddCommand(newAdminKeysRevokeCmd())
	parent.AddCommand(newAdminKeysListCmd())
	return parent
}

// newAdminKeysListCmd returns `admin keys list` — every pk- and ek- across
// owners, optionally filtered. Calls GET /platform/admin/keys.
func newAdminKeysListCmd() *cobra.Command {
	f := &adminCredFlags{}
	var out outputFlag
	var owner, keyType, status, environment, cursor string
	var limit int
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List every key (pk- and ek-) across owners",
		Example:       "  ach-cli admin keys list --owner alice@example.com --status suspended",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAdminKeysList(cmd, f, out.v, owner, keyType, status, environment, cursor, limit)
		},
	}
	cmd.Flags().StringVar(&owner, "owner", "", "Filter by owner email")
	cmd.Flags().StringVar(&keyType, "type", "", "Filter by type: pk|ek")
	cmd.Flags().StringVar(&status, "status", "active", "Filter by status: active|suspended|revoked|expired|all")
	cmd.Flags().StringVar(&environment, "env", "", "Filter by environment (ek- only)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor")
	cmd.Flags().IntVar(&limit, "limit", 0, "Max rows per page")
	// withYes=false — read-only, no confirmation prompt.
	registerAdminCredFlags(cmd, f, false)
	registerOutputFlag(cmd, &out, "table", outputJSON)
	return cmd
}

func runAdminKeysList(cmd *cobra.Command, f *adminCredFlags, output,
	owner, keyType, status, environment, cursor string, limit int) error {
	ctx := cmd.Context()

	// Validate --status before any network call (mirrors runKeysList).
	switch status {
	case "active", "suspended", "revoked", "expired", statusAll, "":
	default:
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("invalid --status %q: must be active, suspended, revoked, expired, or all", status),
		}
	}
	// Validate --type before any network call, same as --status.
	switch keyType {
	case "pk", "ek", "":
	default:
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("invalid --type %q: must be pk or ek", keyType),
		}
	}

	c, err := resolveCred(ctx, f.credFlags, synthetic.GateAPI)
	if err != nil {
		return err
	}
	hc := newAPIClient(c.BaseURL, c.Bearer, adminHTTPClient, f.Verbose, cmd.ErrOrStderr())

	all, err := fetchAll[render.KeyRowView](ctx, hc, cursor, func(c string) string {
		return buildAdminKeysListPath(owner, keyType, status, environment, c, limit)
	})
	if err != nil {
		return err
	}
	if output == outputJSON {
		return writeJSON(cmd.OutOrStdout(), all)
	}
	_, _ = io.WriteString(cmd.OutOrStdout(), render.FormatAdminKeyList(all))
	return nil
}

// buildAdminKeysListPath returns GET /platform/admin/keys with optional
// query parameters.
func buildAdminKeysListPath(ownerEmail, keyType, status, environment, cursor string, limit int) string {
	q := url.Values{}
	if ownerEmail != "" {
		q.Set("owner_email", ownerEmail)
	}
	if keyType != "" {
		q.Set("type", keyType)
	}
	// send status unless "" or "all" (server normalizes unknown to no filter)
	if status != "" && status != statusAll {
		q.Set("status", status)
	}
	if environment != "" {
		q.Set("environment", environment)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if len(q) == 0 {
		return "/platform/admin/keys"
	}
	return "/platform/admin/keys?" + q.Encode()
}

func newAdminKeysRevokeCmd() *cobra.Command {
	f := &adminCredFlags{}
	var owner string
	cmd := &cobra.Command{
		Use:   "revoke [<key-id> | --owner <email>]",
		Short: "Revoke one key by id (pkid_… or ekid_…), or every key an owner holds",
		Example: `  ach-cli admin keys revoke ekid_01H…
  ach-cli admin keys revoke --owner alice@example.com --yes`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 1 && owner != "":
				return &exit.CodedError{Code: exit.General, Msg: "pass a key id or --owner, not both"}
			case len(args) == 1:
				return runAdminKeysRevoke(cmd, args[0], f)
			case owner != "":
				return runAdminRevokeOwnerKeys(cmd, owner, f)
			default:
				return &exit.CodedError{Code: exit.General, Msg: "pass a key id or --owner <email>"}
			}
		},
	}
	cmd.Flags().StringVar(&owner, "owner", "", "Revoke every key (pk- and ek-) this email owns")
	registerAdminCredFlags(cmd, f, true)
	return cmd
}

func runAdminKeysRevoke(cmd *cobra.Command, keyID string, f *adminCredFlags) error {
	ctx := cmd.Context()

	// Client-side key-id classification BEFORE any HTTP call.
	if err := validateAdminKeyID(keyID); err != nil {
		return err
	}
	if !f.Yes {
		if err := adminConfirm(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Revoke key %s ? (y/N): ", keyID)); err != nil {
			return err
		}
	}

	c, err := resolveCred(ctx, f.credFlags, synthetic.GateAPI)
	if err != nil {
		return err
	}
	hc := newAPIClient(c.BaseURL, c.Bearer, adminHTTPClient, f.Verbose, cmd.ErrOrStderr())

	body := struct {
		KeyID string `json:"key_id"`
	}{KeyID: keyID}
	var resp adminRevokeKeyResponse
	if doErr := hc.Do(ctx, http.MethodPost, "/platform/admin/keys/revoke", body, &resp); doErr != nil {
		return doErr
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s (status: %s)\n", resp.KeyID, resp.Status)
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
// users → budget / limits
// ---------------------------------------------------------------------

func newAdminUsersCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "users",
		Short: "Set a user's budget or limits",
		RunE:  helpOrUnknownSubcommand,
	}
	parent.AddCommand(newAdminUsersBudgetCmd(), newAdminUsersLimitsCmd())
	return parent
}

// validateEmail rejects an argument that cannot be an owner email.
func validateEmail(email string) error {
	if email == "" || !strings.Contains(email, "@") {
		return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("invalid email %q", email)}
	}
	return nil
}

// adminUserPath is /platform/admin/users/{email}/{leaf}. The email is
// path-escaped; the server decodes it with url.PathUnescape.
func adminUserPath(email, leaf string) string {
	return "/platform/admin/users/" + url.PathEscape(email) + "/" + leaf
}

// runAdminRevokeOwnerKeys bulk-revokes every key an owner holds.
func runAdminRevokeOwnerKeys(cmd *cobra.Command, email string, f *adminCredFlags) error {
	ctx := cmd.Context()
	email = strings.TrimSpace(email)
	if err := validateEmail(email); err != nil {
		return err
	}
	if !f.Yes {
		if err := adminConfirm(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Revoke ALL keys owned by %s ? (y/N): ", email)); err != nil {
			return err
		}
	}

	c, err := resolveCred(ctx, f.credFlags, synthetic.GateAPI)
	if err != nil {
		return err
	}
	hc := newAPIClient(c.BaseURL, c.Bearer, adminHTTPClient, f.Verbose, cmd.ErrOrStderr())

	var resp adminUserRevokeResponse
	if doErr := hc.Do(ctx, http.MethodPost, adminUserPath(email, "revoke-keys"), struct{}{}, &resp); doErr != nil {
		return doErr
	}
	stdout := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(stdout, "Revoked %d keys owned by %s\n", resp.RevokedCount, email)
	for _, e := range resp.Errors {
		_, _ = fmt.Fprintf(stdout, "  - %s\n", e)
	}
	return nil
}

// adminUserPatch validates email, then PATCHes body onto the user's leaf
// route and prints done on success (204).
func adminUserPatch(cmd *cobra.Command, f *adminCredFlags, email, leaf string, body any, done string) error {
	email = strings.TrimSpace(email)
	if err := validateEmail(email); err != nil {
		return err
	}
	c, err := resolveCred(cmd.Context(), f.credFlags, synthetic.GateAPI)
	if err != nil {
		return err
	}
	hc := newAPIClient(c.BaseURL, c.Bearer, adminHTTPClient, f.Verbose, cmd.ErrOrStderr())
	if err := hc.Do(cmd.Context(), http.MethodPatch, adminUserPath(email, leaf), body, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s for %s\n", done, email)
	return nil
}

func newAdminUsersBudgetCmd() *cobra.Command {
	f := &adminCredFlags{}
	var maxBudget float64
	var budgetDuration string
	cmd := &cobra.Command{
		Use:   "budget <email>",
		Short: "Set a user's total spend ceiling (all their keys and sessions)",
		Long: `Set the spend ceiling (USD) that covers everything one user spends: their
personal session and every key they own. A user who has never signed in is
created, not refused. A later sign-in never overwrites it.`,
		Example:       "  ach-cli admin users budget alice@example.com --max-budget 200 --budget-duration 30d",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return adminUserPatch(cmd, f, args[0], "budget",
				keyBudget{MaxBudget: maxBudget, BudgetDuration: budgetDuration}, "budget set")
		},
	}
	cmd.Flags().Float64Var(&maxBudget, "max-budget", 0, "Spend ceiling, in USD")
	cmd.Flags().StringVar(&budgetDuration, "budget-duration", "", "Budget reset period, e.g. 30d")
	_ = cmd.MarkFlagRequired("max-budget")
	registerAdminCredFlags(cmd, f, false)
	return cmd
}

func newAdminUsersLimitsCmd() *cobra.Command {
	f := &adminCredFlags{}
	var maxKeys int
	cmd := &cobra.Command{
		Use:   "limits <email>",
		Short: "Set how many keys a user may hold",
		Long: `Set how many non-revoked keys one user may hold (0 = none). Takes effect on
their next keys create.`,
		Example:       "  ach-cli admin users limits alice@example.com --max-keys 10",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxKeys < 0 {
				return &exit.CodedError{Code: exit.General, Msg: "--max-keys must be >= 0"}
			}
			body := struct {
				MaxKeys int `json:"max_keys"`
			}{maxKeys}
			return adminUserPatch(cmd, f, args[0], "limits", body, "limits set")
		},
	}
	cmd.Flags().IntVar(&maxKeys, "max-keys", 0, "Maximum non-revoked keys the user may hold")
	_ = cmd.MarkFlagRequired("max-keys")
	registerAdminCredFlags(cmd, f, false)
	return cmd
}

// ---------------------------------------------------------------------
// refresh
// ---------------------------------------------------------------------

func newAdminRefreshCmd() *cobra.Command {
	f := &adminCredFlags{}
	cmd := &cobra.Command{
		Use:   "refresh <kind> <name>",
		Short: "Force-refresh an external content resource",
		Long: "kind must be one of {plugin, prompt, artifact, skill, marketplace, " +
			"skill-marketplace}. No interactive confirmation (idempotent operation).",
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

	// D-CONTEXT W3b: closed-set client-side validation. Even though the
	// server-side ForceRefreshHandler supports additional kinds (e.g.
	// pluginmarketplace) that v1alpha1 doesn't expose on the CLI, the
	// user-facing surface is intentionally limited to four. Phase 7
	// can lift this gate if/when additional kinds are surfaced.
	if _, ok := allowedRefreshKinds[kind]; !ok {
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf(
				"kind must be one of: plugin, prompt, artifact, skill, marketplace, skill-marketplace; got: %s",
				kind),
		}
	}

	// G8: map the user-facing kind to the canonical server kind. The two
	// marketplace aliases differ from their server names; the rest pass
	// through. Sending an alias verbatim would 400 server-side.
	serverKind := kind
	if mapped, ok := refreshKindToServer[kind]; ok {
		serverKind = mapped
	}

	if strings.TrimSpace(name) == "" {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "name is required",
		}
	}

	c, err := resolveCred(ctx, f.credFlags, synthetic.GateAPI)
	if err != nil {
		return err
	}
	baseURL, bearer := c.BaseURL, c.Bearer

	hc := newAPIClient(baseURL, bearer, adminHTTPClient, f.Verbose, stderr)

	body := struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}{Kind: serverKind, Name: name}
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

// Register `ach-cli admin` on the root command. Mirrors the env-keys /
// login / whoami pattern from 06-03 / 06-05 — each subcommand owns
// its own init() so cobra registration is local to the file.
func init() {
	rootCmd.AddCommand(newAdminCmd())
}
