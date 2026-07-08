// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// TestIsHTTPNotFound pins the classifier used at the ek_ revoke security
// boundary: ONLY a genuine *APIError with HTTP 404 counts as "confirmed gone".
// Every other error (other 4xx/5xx, 401, the empty-list ErrNotFound sentinel,
// a plain error, nil) must return false so it cannot bypass the LiteLLM-first
// revoke barrier (KEY-08). A wrapped 404 still matches (errors.As unwraps).
func TestIsHTTPNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"404 APIError", &APIError{StatusCode: http.StatusNotFound}, true},
		{"wrapped 404 APIError", fmt.Errorf("revoke: %w", &APIError{StatusCode: http.StatusNotFound}), true},
		{"400 APIError", &APIError{StatusCode: http.StatusBadRequest}, false},
		{"401 APIError", &APIError{StatusCode: http.StatusUnauthorized}, false},
		{"500 APIError", &APIError{StatusCode: http.StatusInternalServerError}, false},
		{"Auth401Error", &Auth401Error{Path: "/key/delete"}, false},
		{"ErrNotFound sentinel (empty list, not HTTP 404)", ErrNotFound, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsHTTPNotFound(tc.err); got != tc.want {
				t.Errorf("IsHTTPNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
