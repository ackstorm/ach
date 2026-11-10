// SPDX-License-Identifier: Apache-2.0

// `ach-cli keys` manages the caller's environment keys (ek-…). A key is a
// long-lived secret scoped to one Environment. The plaintext is printed
// exactly once, by create, and stored nowhere; every other verb takes the
// key's name (looked up on the server) or its ekid_… id.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/render"
	"github.com/ackstorm/ach/internal/keys"
)

// keysHTTPClient is the test seam for the keys HTTP transport.
var keysHTTPClient *http.Client

// keyStatuses are the effective states GET /platform/keys filters on.
var keyStatuses = []string{"active", "suspended", "expired", "invalid", statusRevoked}

// statusRevoked is the one key status that never counts as a live key.
const statusRevoked = "revoked"

// envKeysCreateResponse mirrors the POST /platform/keys response.
type envKeysCreateResponse struct {
	KeyID     string `json:"key_id"`
	Plaintext string `json:"plaintext"`
}

// createdKeyView is the `keys create -o json` document.
type createdKeyView struct {
	Name        string  `json:"name"`
	ID          string  `json:"id"`
	Environment string  `json:"environment"`
	Key         string  `json:"key"`
	ExpiresAt   *string `json:"expires_at"`
}

func newKeysCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "keys",
		Short: "Manage your environment keys (ek-…)",
		Long: `Manage your environment keys. A key (ek-…) is a long-lived secret scoped to
one Environment — for an agent runtime or a CI job that cannot sign in with a
browser. keys create prints it once; store it in the secret store of whatever
uses it. Every other verb takes the key's name or its ekid_… id.`,
		RunE: helpOrUnknownSubcommand,
	}
	parent.AddCommand(newKeysCreateCmd(), newKeysListCmd(), newKeysRevokeCmd(),
		newKeysStateCmd("suspend", "Suspend a key (it stops working until resumed)", "Suspended"),
		newKeysStateCmd("resume", "Resume a suspended key", "Resumed"),
		newKeysBudgetCmd())
	return parent
}

// keysClient resolves the credential and builds the API client.
func keysClient(cmd *cobra.Command, f credFlags) (*httpclient.Client, error) {
	c, err := resolveCred(cmd.Context(), f)
	if err != nil {
		return nil, err
	}
	return newAPIClient(c.BaseURL, c.Bearer, keysHTTPClient, f.Verbose, cmd.ErrOrStderr()), nil
}

// ---------------------------------------------------------------------
// create
// ---------------------------------------------------------------------

func newKeysCreateCmd() *cobra.Command {
	var (
		f              credFlags
		name, expires  string
		budgetDuration string
		maxBudget      float64
		out            outputFlag
	)
	cmd := &cobra.Command{
		Use:   "create <environment>",
		Args:  cobra.MaximumNArgs(1),
		Short: "Create a key for an Environment",
		Long: `Create an environment key (ek-…) named --name (required: what the key is
for, so you and the console can tell keys apart). The key is printed to
stdout exactly once and stored nowhere else: put it in the secret store of
whatever uses it.

-o json prints {name, id, environment, key, expires_at} instead of the bare key.

--expires takes a number of days (90d), a duration (720h) or a date
(2026-12-31T00:00:00Z). --max-budget caps what this key can spend (USD),
optionally per --budget-duration (30d); your own total budget still applies.`,
		Example: `  ach-cli keys create frontend-dev --name laptop --expires 90d
  ach-cli keys create staging --name ci-deploy --max-budget 20 --budget-duration 30d`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("budget-duration") && !cmd.Flags().Changed("max-budget") {
				return &exit.CodedError{Code: exit.General, Msg: "--budget-duration needs --max-budget"}
			}
			name = strings.TrimSpace(name)
			if name == "" {
				return &exit.CodedError{
					Code: exit.General, Msg: "--name is required: say what the key is for, e.g. --name ci-deploy",
				}
			}
			var expiresAt string
			if expires != "" {
				t, err := parseExpires(expires, time.Now())
				if err != nil {
					return err
				}
				expiresAt = t.UTC().Format(time.RFC3339)
			}
			hc, err := keysClient(cmd, f)
			if err != nil {
				return err
			}
			env := ""
			if len(args) == 1 {
				env = strings.TrimSpace(args[0])
			}
			if err := checkEnvironment(cmd.Context(), hc, env); err != nil {
				return err
			}
			body := keysCreateBody{Environment: env, Name: name, ExpiresAt: expiresAt}
			if cmd.Flags().Changed("max-budget") {
				body.Budget = &keyBudget{MaxBudget: maxBudget, BudgetDuration: budgetDuration}
			}
			return runKeysCreate(cmd, hc, body, out.v == outputJSON)
		},
	}
	registerCredFlags(cmd, &f)
	registerOutputFlag(cmd, &out, "table", outputJSON)
	cmd.Flags().StringVar(&name, "name", "", "What the key is for (required)")
	cmd.Flags().StringVar(&expires, "expires", "", "Expiry: 90d, a duration (720h) or an RFC3339 date")
	cmd.Flags().Float64Var(&maxBudget, "max-budget", 0, "Spend cap for this key, in USD")
	cmd.Flags().StringVar(&budgetDuration, "budget-duration", "", "Budget reset period, e.g. 30d (needs --max-budget)")
	return cmd
}

type keyBudget struct {
	MaxBudget      float64 `json:"max_budget"`
	BudgetDuration string  `json:"budget_duration,omitempty"`
}

type keysCreateBody struct {
	Environment string     `json:"environment"`
	Name        string     `json:"name"`
	ExpiresAt   string     `json:"expires_at,omitempty"`
	Budget      *keyBudget `json:"budget,omitempty"`
}

// parseExpires accepts <n>d, a Go duration, or an RFC3339 timestamp.
func parseExpires(s string, now time.Time) (time.Time, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return now.AddDate(0, 0, n), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return now.Add(d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, &exit.CodedError{Code: exit.General,
		Msg: fmt.Sprintf("--expires %q: want a number of days (90d), a duration (720h) or an RFC3339 date", s)}
}

// checkEnvironment requires an environment and, when the caller's
// environment list is readable, that it is on it — so a typo fails before
// any key is minted, with the list to pick from.
func checkEnvironment(ctx context.Context, hc *httpclient.Client, env string) error {
	names := fetchEnvNamesBestEffort(ctx, hc)
	yours := ""
	if len(names) > 0 {
		yours = "\n  Your environments:\n    " + strings.Join(names, ", ")
	}
	if env == "" {
		return &exit.CodedError{Code: exit.General, Msg: "missing environment.\n" +
			"  Usage: ach-cli keys create <environment> --name <name>\n" +
			"  Example: ach-cli keys create frontend-dev --name laptop" + yours}
	}
	if len(names) > 0 && !slices.Contains(names, env) {
		return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("environment %q not found.%s", env, yours)}
	}
	return nil
}

// fetchEnvNamesBestEffort lists the caller's environment names; any
// failure yields nil so callers just omit the list.
func fetchEnvNamesBestEffort(ctx context.Context, hc *httpclient.Client) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var resp page[render.EnvView]
	if err := hc.Do(ctx, http.MethodGet, buildEnvListPath(defaultEnvListLimit, "", false), nil, &resp); err != nil {
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

func runKeysCreate(cmd *cobra.Command, hc *httpclient.Client, body keysCreateBody, asJSON bool) error {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	var resp envKeysCreateResponse
	if err := hc.Do(cmd.Context(), http.MethodPost, "/platform/keys", body, &resp); err != nil {
		// resp is zero-valued on a non-2xx: nothing of a key can leak.
		return err
	}
	if resp.KeyID == "" || resp.Plaintext == "" {
		return &exit.CodedError{Code: exit.General,
			Msg: "the server returned an incomplete response (no key id or key)"}
	}
	// The one and only time the plaintext is printed; stdout carries
	// nothing else so it is pipe-safe.
	if asJSON {
		var expires *string
		if body.ExpiresAt != "" {
			expires = &body.ExpiresAt
		}
		if err := writeJSON(stdout, createdKeyView{Name: body.Name, ID: resp.KeyID,
			Environment: body.Environment, Key: resp.Plaintext, ExpiresAt: expires}); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintln(stdout, resp.Plaintext)
	}
	_, _ = fmt.Fprintf(stderr, "Key ID: %s (revoke with: ach-cli keys revoke %s)\n", resp.KeyID, resp.KeyID)
	return nil
}

// ---------------------------------------------------------------------
// list
// ---------------------------------------------------------------------

func newKeysListCmd() *cobra.Command {
	var (
		f           credFlags
		out         outputFlag
		environment string
		status      string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your environment keys",
		Long: `List your environment keys. --status is one of active (default),
suspended, expired, invalid (you lost access to its Environment), revoked,
or all.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if status != statusAll && !slices.Contains(keyStatuses, status) {
				return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf(
					"invalid --status %q: must be one of %s, or all", status, strings.Join(keyStatuses, ", "))}
			}
			hc, err := keysClient(cmd, f)
			if err != nil {
				return err
			}
			rows, err := listKeys(cmd.Context(), hc, environment, status)
			if err != nil {
				return err
			}
			if out.v == outputJSON {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			if len(rows) == 0 {
				_, _ = io.WriteString(cmd.OutOrStdout(), emptyKeysMessage(status, environment))
				return nil
			}
			_, _ = io.WriteString(cmd.OutOrStdout(), render.FormatKeyList(rows))
			return nil
		},
	}
	registerCredFlags(cmd, &f)
	registerOutputFlag(cmd, &out, "table", outputJSON)
	cmd.Flags().StringVar(&environment, "environment", "", "Only keys of this Environment")
	cmd.Flags().StringVar(&status, "status", "active", "active|suspended|expired|invalid|revoked|all")
	return cmd
}

// emptyKeysMessage says which filters produced no rows and how to widen them.
func emptyKeysMessage(status, environment string) string {
	what := "No keys"
	if status != statusAll {
		what = "No " + status + " keys"
	}
	if environment != "" {
		what += fmt.Sprintf(" for environment %q", environment)
	}
	switch {
	case status != statusAll:
		return what + " (try --status all)\n"
	case environment != "":
		return what + " (check the name with `ach-cli environment list`)\n"
	default:
		return what + "; `ach-cli keys create <env>` makes one\n"
	}
}

// listKeys pages GET /platform/keys?type=ek. status "all" sends no filter.
func listKeys(ctx context.Context, hc *httpclient.Client, environment, status string) ([]render.KeyRowView, error) {
	return fetchAll[render.KeyRowView](ctx, hc, "", func(cursor string) string {
		q := url.Values{"type": {"ek"}}
		if status != statusAll {
			q.Set("status", status)
		}
		if environment != "" {
			q.Set("environment", environment)
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		return "/platform/keys?" + q.Encode()
	})
}

// resolveKeyID turns a revoke/suspend/resume/budget argument into a key id:
// an ekid_… is used as is; a name is looked up among your keys on the
// server (non-revoked, unique by name).
func resolveKeyID(ctx context.Context, hc *httpclient.Client, arg string) (string, error) {
	switch {
	case strings.HasPrefix(arg, keys.EkidKeyIDPrefix):
		return arg, nil
	case strings.HasPrefix(arg, keys.EkBearerPrefix), strings.HasPrefix(arg, keys.PkBearerPrefix):
		return "", &exit.CodedError{Code: exit.General,
			Msg: "pass the key's name or its ekid_… id, not the key itself"}
	}
	rows, err := listKeys(ctx, hc, "", statusAll)
	if err != nil {
		return "", err
	}
	var ids []string
	for _, r := range rows {
		if r.Name == arg && r.Status != statusRevoked {
			ids = append(ids, r.KeyID)
		}
	}
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		var names []string
		for _, r := range rows {
			if r.Status != statusRevoked && r.Name != "" {
				names = append(names, r.Name)
			}
		}
		hint := "you have no keys; `ach-cli keys create <env>` makes one"
		if len(names) > 0 {
			sort.Strings(names)
			hint = "your keys: " + strings.Join(slices.Compact(names), ", ")
		}
		return "", &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("no key named %q; %s", arg, hint)}
	default:
		return "", &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf(
			"%d keys are named %q; pass one of their ids: %s", len(ids), arg, strings.Join(ids, ", "))}
	}
}

// keyServerError gives the key-verb server errors a plain message.
func keyServerError(err error, arg string) error {
	var sErr *httpclient.ServerError
	if !errors.As(err, &sErr) {
		return err
	}
	switch {
	case sErr.Status == http.StatusNotFound:
		return &exit.CodedError{Code: exit.General,
			Msg: fmt.Sprintf("key %q not found, or not owned by you", arg), Wrapped: err}
	case sErr.Status == http.StatusConflict && sErr.Code == "key_revoked":
		return &exit.CodedError{Code: exit.General, Msg: "key is revoked", Wrapped: err}
	}
	return err
}

// keyVerb runs one key verb once the credential and key id are resolved.
type keyVerb func(cmd *cobra.Command, hc *httpclient.Client, arg, id string) error

// keyArgCmd builds a `keys <verb> <name|id>` command: it resolves the
// credential and the key id, then hands the client and the id to run.
func keyArgCmd(use, short string, run keyVerb) *cobra.Command {
	f := &credFlags{}
	cmd := &cobra.Command{
		Use:   use + " <name|id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hc, err := keysClient(cmd, *f)
			if err != nil {
				return err
			}
			id, err := resolveKeyID(cmd.Context(), hc, args[0])
			if err != nil {
				return err
			}
			return run(cmd, hc, args[0], id)
		},
	}
	registerCredFlags(cmd, f)
	return cmd
}

// ---------------------------------------------------------------------
// revoke / suspend / resume / budget
// ---------------------------------------------------------------------

func newKeysRevokeCmd() *cobra.Command {
	var yes bool
	cmd := keyArgCmd("revoke", "Revoke a key for good",
		func(cmd *cobra.Command, hc *httpclient.Client, arg, id string) error {
			if !yes {
				prompt := fmt.Sprintf("Revoke %s (%s)? [y/N]: ", arg, id)
				if err := adminConfirm(cmd.InOrStdin(), cmd.ErrOrStderr(), prompt); err != nil {
					return err
				}
			}
			if err := hc.Do(cmd.Context(), http.MethodDelete, "/platform/keys/"+id, nil, nil); err != nil {
				return keyServerError(err, arg)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s (%s)\n", arg, id)
			return nil
		})
	cmd.Example = `  ach-cli keys revoke laptop
  ach-cli keys revoke ekid_01j0zxyz --yes`
	cmd.Flags().BoolVar(&yes, "yes", false, "Do not ask for confirmation")
	return cmd
}

func newKeysStateCmd(verb, short, done string) *cobra.Command {
	cmd := keyArgCmd(verb, short, func(cmd *cobra.Command, hc *httpclient.Client, arg, id string) error {
		if err := hc.Do(cmd.Context(), http.MethodPost, "/platform/keys/"+id+"/"+verb, nil, nil); err != nil {
			return keyServerError(err, arg)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s)\n", done, arg, id)
		return nil
	})
	return cmd
}

func newKeysBudgetCmd() *cobra.Command {
	var (
		maxBudget      float64
		budgetDuration string
	)
	cmd := keyArgCmd("budget", "Set a key's spend cap",
		func(cmd *cobra.Command, hc *httpclient.Client, arg, id string) error {
			body := keyBudget{MaxBudget: maxBudget, BudgetDuration: budgetDuration}
			if err := hc.Do(cmd.Context(), http.MethodPatch, "/platform/keys/"+id+"/budget", body, nil); err != nil {
				return keyServerError(err, arg)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Budget set on %s (%s)\n", arg, id)
			return nil
		})
	cmd.Long = `Set the spend cap (USD) of one key, optionally per --budget-duration
(e.g. 30d). The cap applies to that key alone;
your own total budget still applies on top of it.
Takes about 10 seconds to take effect.`
	cmd.Example = `  ach-cli keys budget laptop --max-budget 20 --budget-duration 30d`
	cmd.Flags().Float64Var(&maxBudget, "max-budget", 0, "Spend cap for this key, in USD")
	cmd.Flags().StringVar(&budgetDuration, "budget-duration", "", "Budget reset period, e.g. 30d")
	_ = cmd.MarkFlagRequired("max-budget")
	return cmd
}

func init() {
	rootCmd.AddCommand(newKeysCmd())
}
