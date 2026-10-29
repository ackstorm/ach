// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// executeCommand is the shared test driver every cmd/ach/cmd/*_test.go
// helper composes around. It captures stdout + stderr, invokes
// cmd.ExecuteContext with a background context, and unwraps the
// returned error to an exit.Code via the same dispatch logic
// cmd/ach/main.go uses (errors.As against *httpclient.ServerError
// and *exit.CodedError).
//
// Extracted here so per-subcommand helpers (executeLogin, executeEnv,
// executeWhoami, ...) stay small and the golangci-lint dupl detector
// doesn't trip on five structurally-identical 20-line bodies.
func executeCommand(t *testing.T, cmd *cobra.Command, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		return outBuf.String(), errBuf.String(), exit.OK, nil
	}
	var sErr *httpclient.ServerError
	if errors.As(err, &sErr) {
		return outBuf.String(), errBuf.String(), exit.MapServerError(sErr), err
	}
	var cErr *exit.CodedError
	if errors.As(err, &cErr) {
		return outBuf.String(), errBuf.String(), cErr.Code, err
	}
	return outBuf.String(), errBuf.String(), exit.General, err
}
