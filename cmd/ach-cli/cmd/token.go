// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/lock"
	"github.com/ackstorm/ach/internal/cli/synthetic"
	"github.com/ackstorm/ach/internal/keys"
)

// tokenLockTimeout bounds the wait for a concurrent helper. Claude Code and
// Codex both exec `ach-cli token` on their own timers, so two helpers hitting
// the refresh window together is the normal case, not contention.
const tokenLockTimeout = 30 * time.Second

// newTokenCmd prints a valid credential and NOTHING else — it is the
// credential helper the tools run: Claude Code `apiKeyHelper`, Codex
// `[model_providers.*].auth.command`, opencode `.well-known/opencode`.
// An OAuth profile prints its access token (refreshed under a file lock —
// the AS rotates refresh tokens, so two concurrent refreshes would spend
// one and strand the other); a key profile prints its Key. Every
// diagnostic goes to stderr.
func newTokenCmd() *cobra.Command {
	var (
		flagProfile string
		out         outputFlag
	)
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print the active profile's token, or its key (credential helper)",
		Long: `Print the credential and nothing else. -o json prints {token, type, expires_at}
(type jwt or ek; expires_at is null for a key).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateSession}); err != nil {
				return err
			}
			path, release, err := acquireTokenLock(cmd.Context())
			if err != nil {
				return err
			}
			defer release()

			// Load INSIDE the lock: a helper that waited sees the pair the
			// first one just rotated and returns it without refreshing again.
			file, err := config.Load(path)
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			if file == nil {
				return &exit.CodedError{Code: exit.General, Msg: "no profile configured; run `ach-cli login`"}
			}
			name, prof, err := config.ResolveActive(file, flagProfile, os.Getenv("ACH_PROFILE"))
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("%v; run `ach-cli login`", err), Wrapped: err}
			}
			tok, err := profileBearer(cmd.Context(), file, path, prof)
			if err != nil {
				return err
			}
			if tok == "" {
				return &exit.CodedError{
					Code: exit.General, Msg: fmt.Sprintf("profile %q has no login; run `ach-cli login`", name),
				}
			}
			if out.v == outputJSON {
				return writeJSON(cmd.OutOrStdout(), newTokenView(tok))
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), tok)
			return err
		},
	}
	cmd.Flags().StringVar(&flagProfile, "profile", "", "Use this profile instead of the active one")
	registerOutputFlag(cmd, &out, "table", outputJSON)
	return cmd
}

// tokenView is the `token -o json` document.
type tokenView struct {
	Token     string  `json:"token"`
	Type      string  `json:"type"`
	ExpiresAt *string `json:"expires_at"`
}

// newTokenView describes tok. A key carries no expiry the profile records.
func newTokenView(tok string) tokenView {
	v := tokenView{Token: tok, Type: "ek"}
	if !strings.HasPrefix(tok, "ek-") {
		v.Type = "jwt"
	}
	if exp, ok := jwtExp(tok); ok {
		s := exp.UTC().Format(time.RFC3339)
		v.ExpiresAt = &s
	}
	return v
}

// jwtExp reads a JWT's exp claim without verifying the signature: the token
// is the caller's own and the value only says when to ask again.
func jwtExp(tok string) (time.Time, bool) {
	if !keys.LooksLikeJWS(tok) {
		return time.Time{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	if err != nil {
		return time.Time{}, false
	}
	var c struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(b, &c) != nil || c.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(c.Exp, 0), true
}

// acquireTokenLock takes the lock every token refresh runs under and returns
// the config path. The AS rotates refresh tokens, so two processes refreshing
// at once would spend one and strand the other.
func acquireTokenLock(ctx context.Context) (string, func(), error) {
	path, err := config.Path()
	if err != nil {
		return "", nil, &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", nil, &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	lease, err := lock.NewLocker(filepath.Join(filepath.Dir(path), "token.lock")).
		Acquire(ctx, lock.AcquireWithTimeout, tokenLockTimeout)
	if err != nil {
		return "", nil, &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("token: %v", err), Wrapped: err}
	}
	return path, func() { _ = lease.Release() }, nil
}

func init() { rootCmd.AddCommand(newTokenCmd()) }
