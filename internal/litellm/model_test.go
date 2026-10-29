// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"io"
	"net/http"
	"sync"
	"testing"
)

// capturedRequest records the wire-level shape of a request the mock
// server saw. Used by path-string-verification tests across the package.
type capturedRequest struct {
	Method string
	Path   string
	Body   []byte
}

// captureMock returns an http.HandlerFunc that records every request
// into the provided slice (mutex-protected) and responds with the given
// status + body. Caller passes a function that produces the response
// for the i-th request, enabling status-sequence per call.
func captureMock(t *testing.T, captured *[]capturedRequest, respond func(i int, w http.ResponseWriter)) http.HandlerFunc {
	t.Helper()
	var mu sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		*captured = append(*captured, capturedRequest{
			Method: r.Method,
			Path:   r.URL.RequestURI(),
			Body:   body,
		})
		idx := len(*captured) - 1
		mu.Unlock()
		respond(idx, w)
	}
}
