// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"io"
	"net/http"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// newAPIClient builds the platform-api client every subcommand uses.
// transport is the per-subsystem test seam (adminHTTPClient, keysHTTPClient, …);
// nil means the httpclient default.
func newAPIClient(baseURL, key string, transport *http.Client, verbose bool, stderr io.Writer) *httpclient.Client {
	return &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     key,
		HTTPClient: transport,
		Verbose:    verbose,
		Stderr:     stderr,
	}
}
