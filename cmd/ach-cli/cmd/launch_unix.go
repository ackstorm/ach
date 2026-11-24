//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// runJob runs the tool as a shell runs a foreground job: in its own process
// group, which holds the terminal until the tool exits, so the tool (not
// ach-cli) leads the terminal's foreground group. ok is false, with nothing
// started, unless stdin is a terminal that ach-cli holds in the foreground.
func runJob(c *exec.Cmd) (ok bool, err error) {
	tty, isFile := c.Stdin.(*os.File)
	if !isFile {
		return false, nil
	}
	fd, self := int(tty.Fd()), syscall.Getpgrp()
	if fg, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP); err != nil || fg != self {
		return false, nil
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: fd}
	if err := c.Start(); err != nil {
		return true, &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}
	pid := c.Process.Pid
	var ws syscall.WaitStatus
	for {
		if _, err := syscall.Wait4(pid, &ws, syscall.WUNTRACED, nil); err == syscall.EINTR {
			continue
		} else if err != nil {
			return true, &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
		}
		if !ws.Stopped() {
			break
		}
		// Ctrl-Z stopped the tool's group only. Stop ach-cli's group so the
		// shell above sees its job stop, then ask for the terminal: from the
		// background the kernel holds that call, stopping the group again if
		// it has to, until the shell puts ach-cli in the foreground, and it
		// refuses the call at once when no shell is above (an orphaned
		// group, which SIGTSTP does not stop either). A SIGTSTP alone is not
		// enough: off the main thread, kill returns before the stop lands.
		_ = syscall.Kill(0, syscall.SIGTSTP)
		_ = setForeground(fd, self)
		_ = setForeground(fd, pid)
		_ = syscall.Kill(-pid, syscall.SIGCONT)
	}
	// From the background, taking the terminal back raises SIGTTOU unless it
	// is ignored. Ignored only here: the tool must not inherit it, and the
	// stop above depends on its default action.
	signal.Ignore(syscall.SIGTTOU)
	_ = setForeground(fd, self)
	signal.Reset(syscall.SIGTTOU)
	_ = c.Wait() // the tool is already reaped: this only releases the Cmd
	return true, toolExit(ws)
}

// setForeground makes pgid the foreground process group of the terminal fd.
func setForeground(fd, pgid int) error {
	for {
		if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, pgid); err != unix.EINTR {
			return err
		}
	}
}
