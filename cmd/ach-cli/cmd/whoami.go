// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/synthetic"
	"github.com/ackstorm/ach/internal/keys"
)

// unavailable is printed for a field the Hub could not report.
const unavailable = "unavailable"

// whoamiHTTPClient is the test seam for the whoami HTTP transport.
var whoamiHTTPClient *http.Client

// bootstrapView is the part of GET /platform/console/bootstrap whoami prints.
type bootstrapView struct {
	Email    string `json:"email"`
	KeysUsed *int   `json:"keys_used"`
	MaxKeys  *int   `json:"max_keys"`
	Budget   *struct {
		Spend          float64  `json:"spend"`
		MaxBudget      *float64 `json:"max_budget"`
		BudgetDuration *string  `json:"budget_duration"`
	} `json:"budget"`
}

func newWhoamiCmd() *cobra.Command {
	var f credFlags
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show who you are signed in as, your budget and your key allowance",
		Long: `Ask the Hub who the active credential belongs to and print:

  Profile  the profile used
  URL      the Hub
  User     your email
  Auth     session (OAuth login) or key ek-****abcd (--key / ACH_KEY / a key profile)
  Budget   spend / ceiling for your whole account
  Keys     keys held / keys allowed

Exit codes: 0 success, 3 not authorized, 6 network error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doWhoami(cmd, f)
		},
	}
	registerCredFlags(cmd, &f)
	return cmd
}

func doWhoami(cmd *cobra.Command, f credFlags) error {
	c, err := resolveCred(cmd.Context(), f, synthetic.GateAPI)
	if err != nil {
		return err
	}
	hc := newAPIClient(c.BaseURL, c.Bearer, whoamiHTTPClient, f.Verbose, cmd.ErrOrStderr())
	var b bootstrapView
	if err := hc.Do(cmd.Context(), http.MethodGet, "/platform/console/bootstrap", nil, &b); err != nil {
		return mapVerifyError(err)
	}
	writeWhoami(cmd.OutOrStdout(), c, b)
	return nil
}

func writeWhoami(w io.Writer, c cred, b bootstrapView) {
	auth := "session"
	if !keys.LooksLikeJWS(c.Bearer) {
		auth = "key " + config.Mask(c.Bearer)
	}
	budget := unavailable
	if b.Budget != nil {
		budget = fmt.Sprintf("%.2f USD (no ceiling)", b.Budget.Spend)
		if b.Budget.MaxBudget != nil {
			budget = fmt.Sprintf("%.2f / %.2f USD", b.Budget.Spend, *b.Budget.MaxBudget)
			if b.Budget.BudgetDuration != nil {
				budget += " (" + *b.Budget.BudgetDuration + ")"
			}
		}
	}
	keyCount := unavailable
	if b.KeysUsed != nil && b.MaxKeys != nil {
		keyCount = fmt.Sprintf("%d / %d", *b.KeysUsed, *b.MaxKeys)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range [][2]string{
		{"Profile", c.ProfileName}, {"URL", c.BaseURL}, {"User", b.Email},
		{"Auth", auth}, {"Budget", budget}, {"Keys", keyCount},
	} {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", row[0], row[1])
	}
	_ = tw.Flush()
}

// mapVerifyError passes a decoded server error through (main maps it to
// its exit code) and turns anything else — a transport failure — into
// exit 6.
func mapVerifyError(err error) error {
	var sErr *httpclient.ServerError
	if errors.As(err, &sErr) {
		return err
	}
	return &exit.CodedError{Code: exit.Network, Msg: err.Error(), Wrapped: err}
}

func init() {
	rootCmd.AddCommand(newWhoamiCmd())
}
