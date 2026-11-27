// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ackstorm/ach/internal/cli/oauthlogin"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

func newMCPLoginCmd() *cobra.Command {
	var flags credFlags
	var noBrowser, device bool
	cmd := &cobra.Command{
		Use:     "login <server>",
		Aliases: []string{"auth"},
		Args:    cobra.ExactArgs(1),
		Short:   "Approve provider access for an MCP server in your browser",
		Long: `Open the authorization URL and sign in with the account for your active
ACH profile. A local browser returns to the CLI automatically. Over SSH,
use --device to confirm a code in any browser. With --no-browser, open
the authorization URL on your own machine and paste the final loopback
URL into this terminal, even if the browser says it could not connect.
Use the exact server name shown by ach-cli mcp list for your active credential.`,
		Example: "  ach-cli mcp login mcp-zoho-crm-ro",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]
			c, err := resolveCred(ctx, flags)
			if err != nil {
				return err
			}
			if synthetic.IsRawKey(c.Bearer) {
				return fmt.Errorf("ask the key owner to run ach-cli mcp login %s with their personal profile", name)
			}
			targets, err := runtimeCatalog(ctx, c, flags.Verbose, "mcp")
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(targets, func(target runtimeTarget) bool { return target.Name == name }) {
				return fmt.Errorf("MCP server %q is unavailable to the active credential", name)
			}
			resource := strings.TrimRight(c.BaseURL, "/") + "/mcp/" + url.PathEscape(name)
			client := oauthlogin.Client{BaseURL: c.BaseURL, LoginHint: jwtSub(c.Bearer)}
			if device {
				_, err = client.DeviceLogin(ctx, c.OAuthClientID, resource, deviceLoginPrompt(cmd.ErrOrStderr()))
			} else {
				prompt := mcpLoginPrompt(noBrowser, cmd.InOrStdin(), cmd.ErrOrStderr())
				_, err = client.LoginForResource(ctx, c.OAuthClientID, resource, prompt)
			}
			if err != nil {
				return err
			}
			tools, err := cachedMCPTools(ctx, c, flags.Verbose, name, true)
			if err != nil {
				return fmt.Errorf("check access to %s with the active ACH credential: %w", name, err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Authenticated MCP session for %s (%d tools).\n", name, len(tools))
			return err
		},
	}
	registerCredFlags(cmd, &flags)
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false,
		"Print the authorization URL and accept the final browser URL in this terminal")
	cmd.Flags().BoolVar(&device, "device", false, "Show a code to approve provider access from any browser")
	cmd.MarkFlagsMutuallyExclusive("device", "no-browser")
	return cmd
}

func mcpLoginPrompt(noBrowser bool, in io.Reader, out io.Writer) func(context.Context, string) (string, error) {
	return func(ctx context.Context, authURL string) (string, error) {
		_, _ = fmt.Fprintf(out, "Open this authorization URL:\n%s\n", authURL)
		if !noBrowser && oauthlogin.Opener(authURL) == nil {
			_, _ = fmt.Fprintln(out, "Waiting for the local browser. Over SSH, rerun with --no-browser.")
			return "", nil
		}
		return readMCPCallback(ctx, in, out)
	}
}

func readMCPCallback(ctx context.Context, in io.Reader, out io.Writer) (string, error) {
	const prompt = "Paste the final loopback URL here and press Enter (input hidden): "
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var value string
	var err error
	if file, ok := in.(*os.File); ok && isTerminal(file) && term.IsTerminal(int(file.Fd())) {
		fd := int(file.Fd())
		state, err := term.MakeRaw(fd)
		if err != nil {
			return "", fmt.Errorf("hide callback input: %w", err)
		}
		defer func() { _ = term.Restore(fd, state) }()
		line, readErr := term.NewTerminal(rwPair{Reader: file, Writer: out}, "").ReadPassword(prompt)
		if readErr != nil {
			return "", errors.New("callback input canceled")
		}
		value = line
	} else {
		if file, ok := in.(*os.File); ok {
			deadline, bounded := ctx.Deadline()
			if !bounded || file.SetReadDeadline(deadline) != nil {
				return "", errors.New("use an interactive terminal to paste the final loopback URL")
			}
			defer func() { _ = file.SetReadDeadline(time.Time{}) }()
		}
		_, _ = fmt.Fprint(out, prompt)
		scanner := bufio.NewScanner(in)
		if scanner.Scan() {
			value = scanner.Text()
		}
		err = scanner.Err()
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return "", context.DeadlineExceeded
	}
	if err != nil {
		return "", errors.New("could not read the callback URL")
	}
	if value == "" {
		return "", errors.New("no callback URL entered")
	}
	return value, nil
}
