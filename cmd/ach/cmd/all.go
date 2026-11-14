// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/config"
)

var allModes = []string{"operator", "content-service", "platform-api", "forwarder", "gateway"}

var allCmd = &cobra.Command{
	Use:   "all",
	Short: "Run migrate, then every service mode, as child processes of this binary",
	Long: `Run 'ach migrate', then operator, content-service, platform-api, forwarder
and gateway, each as a child process of this binary sharing this process's
environment. For a single-container install outside Kubernetes.

The default bind addresses of the modes do not collide. The gateway needs
ACH_GATEWAY_PLATFORM_API_URL, ACH_GATEWAY_FORWARDER_URL and
ACH_GATEWAY_CONTENT_SERVICE_URL pointing at the local listeners.

SIGINT/SIGTERM is passed to every child. A child that exits on its own stops
the others and 'ach all' exits non-zero, so the container runtime restarts
the set.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		shutdownTimeout, err := config.MustEnvDurationAtLeast("ACH_SHUTDOWN_TIMEOUT", 10*time.Second, time.Second)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		// Forwarder, platform-api and gateway drain for ACH_SHUTDOWN_TIMEOUT;
		// the margin keeps the kill behind that drain, as the chart's pod
		// grace period does.
		return runAll(ctx, self, allModes, shutdownTimeout+5*time.Second)
	},
}

func init() {
	rootCmd.AddCommand(allCmd)
}

// runAll runs `self migrate` to completion, then `self <mode>` for every
// mode until ctx is cancelled or one of them exits. Children still running
// get SIGTERM, then SIGKILL after grace. It returns nil when ctx is
// cancelled after every child started and before any child exit is seen.
func runAll(ctx context.Context, self string, modes []string, grace time.Duration) error {
	migrate := exec.CommandContext(ctx, self, "migrate")
	migrate.Stdout, migrate.Stderr = os.Stdout, os.Stderr
	if err := migrate.Run(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	exits := make(chan error, len(modes))
	started := 0
	var first error
	for _, mode := range modes {
		child := exec.CommandContext(ctx, self, mode)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
		child.WaitDelay = grace
		if err := child.Start(); err != nil {
			first = fmt.Errorf("start %s: %w", mode, err)
			cancel()
			break
		}
		started++
		go func() {
			err := child.Wait()
			if err == nil {
				err = errors.New("exit status 0")
			}
			exits <- fmt.Errorf("%s exited: %w", mode, err)
		}()
	}
	for range started {
		err := <-exits
		if first == nil && ctx.Err() == nil {
			first = err
		}
		cancel()
	}
	return first
}
