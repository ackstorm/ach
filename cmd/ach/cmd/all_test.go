// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for `ach`: runAll re-executes it
// with a mode name as the only argument.
func TestMain(m *testing.M) {
	if os.Getenv("ACH_ALL_TEST_CHILD") == "1" && len(os.Args) == 2 {
		switch os.Args[1] {
		case "migrate":
			os.Exit(0)
		case "fail":
			os.Exit(3)
		case "serve":
			if dir := os.Getenv("ACH_ALL_TEST_READY_DIR"); dir != "" {
				_ = os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), nil, 0o600)
			}
			time.Sleep(time.Minute)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func TestRunAllStopsEveryChildWhenOneExits(t *testing.T) {
	t.Setenv("ACH_ALL_TEST_CHILD", "1")
	start := time.Now()
	err := runAll(context.Background(), os.Args[0], []string{"serve", "fail", "serve"}, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "fail exited: exit status 3") {
		t.Fatalf("got %v, want the failed child's exit", err)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("children were not stopped: runAll took %s", d)
	}
}

func TestRunAllReturnsNilOnCancel(t *testing.T) {
	t.Setenv("ACH_ALL_TEST_CHILD", "1")
	ready := t.TempDir()
	t.Setenv("ACH_ALL_TEST_READY_DIR", ready)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if up, _ := os.ReadDir(ready); len(up) == 2 {
				cancel()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	if err := runAll(ctx, os.Args[0], []string{"serve", "serve"}, 5*time.Second); err != nil {
		t.Fatalf("got %v, want nil on cancel", err)
	}
}

func TestRunAllStopsOnFailedMigrate(t *testing.T) {
	err := runAll(context.Background(), "/nonexistent/ach", []string{"serve"}, time.Second)
	if err == nil || !strings.HasPrefix(err.Error(), "migrate: ") {
		t.Fatalf("got %v, want a migrate error", err)
	}
}
