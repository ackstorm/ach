//go:build linux

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// The job tests re-run this test binary on a pty, as one of two roles:
// "launch" calls runTool the way `ach-cli launch` does, and "shell" is a
// minimal job-control shell with "launch" as its one foreground job.
const (
	jobRoleEnv = "LAUNCH_JOB_ROLE"
	jobToolEnv = "LAUNCH_JOB_TOOL" // sh script the launched tool runs
	jobLogEnv  = "LAUNCH_JOB_LOG"
)

func jobLog(format string, a ...any) {
	f, err := os.OpenFile(os.Getenv(jobLogEnv), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		os.Exit(90)
	}
	fmt.Fprintf(f, format+"\n", a...)
	_ = f.Close()
}

func TestLaunchJobHelper(t *testing.T) {
	switch os.Getenv(jobRoleEnv) {
	case "launch":
		err := runTool(&cobra.Command{}, "/bin/sh", []string{"-c", os.Getenv(jobToolEnv)}, "", nil)
		fg, _ := unix.IoctlGetInt(0, unix.TIOCGPGRP)
		jobLog("terminal back: %v", fg == syscall.Getpgrp())
		var ce *exit.CodedError
		if errors.As(err, &ce) {
			os.Exit(int(ce.Code))
		}
		os.Exit(0)
	case "shell-fg", "shell-bg":
		jobShell(os.Getenv(jobRoleEnv) == "shell-bg")
	}
}

// jobShell runs the "launch" role as a foreground job. A stopped job is
// resumed in the foreground, except that with bgFirst its first stop is
// answered as `bg` would: continued without the terminal.
func jobShell(bgFirst bool) {
	c := exec.Command(os.Args[0], "-test.run=^TestLaunchJobHelper$")
	c.Env = append(os.Environ(), jobRoleEnv+"=launch")
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: 0}
	if err := c.Start(); err != nil {
		os.Exit(91)
	}
	signal.Ignore(syscall.SIGTTOU) // after Start: the job starts with the default action
	pid := c.Process.Pid
	for stops := 1; ; stops++ {
		var ws syscall.WaitStatus
		if _, err := syscall.Wait4(pid, &ws, syscall.WUNTRACED, nil); err != nil {
			os.Exit(92)
		}
		if !ws.Stopped() {
			jobLog("exit %d", ws.ExitStatus())
			os.Exit(0)
		}
		jobLog("stop %d", ws.StopSignal())
		_ = setForeground(0, syscall.Getpgrp())
		switch {
		case stops > 5:
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		case bgFirst && stops == 1:
			_ = syscall.Kill(-pid, syscall.SIGCONT)
		default:
			_ = setForeground(0, pid)
			_ = syscall.Kill(-pid, syscall.SIGCONT)
		}
	}
}

// jobRun is one helper process on its own pty.
type jobRun struct {
	t      *testing.T
	master *os.File
	out    chan []byte
	seen   []byte
	cmd    *exec.Cmd
	log    string
}

func startJob(t *testing.T, role, tool string) *jobRun {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	mfd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(mfd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(mfd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLaunchJobHelper$")
	cmd.Env = append(os.Environ(), jobRoleEnv+"="+role, jobToolEnv+"="+tool, jobLogEnv+"="+log)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	j := &jobRun{t: t, master: master, out: make(chan []byte, 64), cmd: cmd, log: log}
	go func() {
		defer close(j.out)
		for {
			b := make([]byte, 4096)
			n, err := master.Read(b)
			if n > 0 {
				j.out <- b[:n]
			}
			if err != nil {
				return
			}
		}
	}()
	return j
}

// expect reads the pty until needle shows up and returns the text after it.
func (j *jobRun) expect(needle string) string {
	j.t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		if _, after, ok := bytes.Cut(j.seen, []byte(needle)); ok {
			return string(after)
		}
		select {
		case b, ok := <-j.out:
			if !ok {
				j.t.Fatalf("pty closed before %q; saw %q", needle, j.seen)
			}
			j.seen = append(j.seen, b...)
		case <-deadline:
			j.t.Fatalf("no %q within 20s; saw %q", needle, j.seen)
		}
	}
}

func (j *jobRun) send(s string) {
	j.t.Helper()
	if _, err := j.master.WriteString(s); err != nil {
		j.t.Fatal(err)
	}
}

// exit waits for the helper and returns its exit code and its log lines.
func (j *jobRun) exit() (int, string) {
	j.t.Helper()
	err := j.cmd.Wait()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		j.t.Fatal(err)
	}
	if !j.cmd.ProcessState.Exited() {
		j.t.Fatalf("helper did not exit by itself: %v; saw %q", j.cmd.ProcessState, j.seen)
	}
	b, _ := os.ReadFile(j.log)
	return j.cmd.ProcessState.ExitCode(), strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", "; ")
}

// The launched tool leads the terminal's foreground process group (pane UIs
// find the agent session from that process), its exit code passes through,
// and ach-cli's group holds the terminal again afterwards.
func TestLaunchJob_ToolLeadsForegroundGroup(t *testing.T) {
	j := startJob(t, "launch", "echo READY $$; read x; exit 7")
	tool, _ := strconv.Atoi(strings.Fields(j.expect("READY "))[0])
	fg, err := unix.IoctlGetInt(int(j.master.Fd()), unix.TIOCGPGRP)
	if err != nil || fg != tool || fg == j.cmd.Process.Pid {
		t.Fatalf("foreground group = %d (%v), want the tool's %d, not ach-cli's %d", fg, err, tool, j.cmd.Process.Pid)
	}
	j.send("\n")
	if code, log := j.exit(); code != 7 || log != "terminal back: true" {
		t.Fatalf("exit code %d, log %q; want the tool's 7 and the terminal back", code, log)
	}
}

// Ctrl-C kills the tool only: ach-cli survives and exits 128+SIGINT itself.
func TestLaunchJob_CtrlC(t *testing.T) {
	j := startJob(t, "launch", "echo READY; exec sleep 60")
	j.expect("READY")
	j.send("\x03")
	if code, log := j.exit(); code != 130 || log != "terminal back: true" {
		t.Fatalf("exit code %d, log %q; want 130 from a surviving ach-cli", code, log)
	}
}

// Ctrl-Z stops the tool. With no shell above, ach-cli continues it; under a
// shell the job stops once and `fg` resumes it; after `bg` it stops again
// until it gets the terminal. The shell role exits 0 once its job is done.
func TestLaunchJob_CtrlZ(t *testing.T) {
	tstp, ttou := fmt.Sprintf("stop %d; ", syscall.SIGTSTP), fmt.Sprintf("stop %d; ", syscall.SIGTTOU)
	cases := []struct {
		name, role string
		wantCode   int
		wantLog    string
	}{
		{"no shell above", "launch", 7, "terminal back: true"},
		{"fg", "shell-fg", 0, tstp + "terminal back: true; exit 7"},
		{"bg then fg", "shell-bg", 0, tstp + ttou + "terminal back: true; exit 7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := startJob(t, tc.role, "echo READY; read x; echo GOT $x; exit 7")
			j.expect("READY")
			j.send("\x1a")
			j.send("hello\n")
			j.expect("GOT hello")
			if code, log := j.exit(); code != tc.wantCode || log != tc.wantLog {
				t.Fatalf("exit code %d, log %q; want %d, %q", code, log, tc.wantCode, tc.wantLog)
			}
		})
	}
}
