// SPDX-License-Identifier: Apache-2.0

package sources

import (
	"fmt"
	"io"
	nethttp "net/http"
)

// DrainAndClose is the REL-04 helper: drain then close a response body so
// HTTP keepalive can reuse the connection and FDs/goroutines do not leak.
// Both errors are intentionally ignored (best-effort drain; double-close
// is a net/http no-op). nil-safe.
func DrainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

// ClassifyHTTPStatus maps an HTTP status code to a sources sentinel error,
// wrapping it with a "<provider>: <op> <status>" prefix. Returns nil for
// any 2xx. provider/op name the source and operation for the error string.
//
//	401,403 → ErrUnauthorized   404 → ErrNotFound
//	>=500   → ErrUnreachable    other >=400 → ErrUpstreamInvalid
func ClassifyHTTPStatus(provider, op string, status int) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == nethttp.StatusUnauthorized, status == nethttp.StatusForbidden:
		return fmt.Errorf("%s: %s %d: %w", provider, op, status, ErrUnauthorized)
	case status == nethttp.StatusNotFound:
		return fmt.Errorf("%s: %s %d: %w", provider, op, status, ErrNotFound)
	case status >= 500:
		return fmt.Errorf("%s: %s %d: %w", provider, op, status, ErrUnreachable)
	default:
		return fmt.Errorf("%s: %s %d: %w", provider, op, status, ErrUpstreamInvalid)
	}
}
