// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"net/http"
	"testing"
)

// swapHTTPClientForTest points one of the per-subsystem *http.Client seams
// (adminHTTPClient, envHTTPClient, …) at c for the lifetime of t.
func swapHTTPClientForTest(t testing.TB, target **http.Client, c *http.Client) {
	t.Helper()
	previous := *target
	*target = c
	t.Cleanup(func() { *target = previous })
}
