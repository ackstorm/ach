//go:build windows

// SPDX-License-Identifier: Apache-2.0

package cmd

import "os/exec"

// runJob never runs the tool: Windows has no process groups on a terminal.
func runJob(*exec.Cmd) (bool, error) { return false, nil }
