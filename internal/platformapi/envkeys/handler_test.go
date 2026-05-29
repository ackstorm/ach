// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"errors"
	"net/http"
	"testing"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/litellm"
)

// TestIsEnterpriseTagsRejection covers the detector that drives the
// drop-tags-and-retry degradation: only a 403 *litellm.APIError whose body
// names "LiteLLM Enterprise" qualifies. Any other status, error type, or
// body must NOT trigger the retry (it would mask a real failure).
func TestIsEnterpriseTagsRejection(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "403 enterprise tags body",
			err: &litellm.APIError{
				Method: http.MethodPost, Path: "/key/generate", StatusCode: 403, Code: "403",
				Body: []byte(`{"error":{"message":"This feature is only available for LiteLLM Enterprise users: tags","code":"403"}}`),
			},
			want: true,
		},
		{
			name: "403 but unrelated body",
			err: &litellm.APIError{
				StatusCode: 403, Code: "403",
				Body: []byte(`{"error":{"message":"forbidden: not an admin"}}`),
			},
			want: false,
		},
		{
			name: "enterprise wording but wrong status (500)",
			err: &litellm.APIError{
				StatusCode: 500, Code: "500",
				Body: []byte(`LiteLLM Enterprise`),
			},
			want: false,
		},
		{name: "nil error", err: nil, want: false},
		{name: "non-APIError", err: errors.New("dial tcp: connection refused"), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEnterpriseTagsRejection(tc.err); got != tc.want {
				t.Errorf("isEnterpriseTagsRejection() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClassifyLitellmErr asserts an upstream 4xx (APIError or Auth401Error)
// maps to 502 + litellm_rejected, while connectivity / 5xx keeps the 503
// litellm_unreachable mapping.
func TestClassifyLitellmErr(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantOutcome string
	}{
		{
			name:        "upstream 403",
			err:         &litellm.APIError{StatusCode: 403, Code: "403"},
			wantStatus:  http.StatusBadGateway,
			wantOutcome: audit.OutcomeLitellmRejected,
		},
		{
			name:        "upstream 422 validation",
			err:         &litellm.APIError{StatusCode: 422, Code: "422"},
			wantStatus:  http.StatusBadGateway,
			wantOutcome: audit.OutcomeLitellmRejected,
		},
		{
			name:        "auth 401",
			err:         &litellm.Auth401Error{Path: "/key/generate"},
			wantStatus:  http.StatusBadGateway,
			wantOutcome: audit.OutcomeLitellmRejected,
		},
		{
			name:        "transient 503",
			err:         &litellm.APIError{StatusCode: 503, Code: "503", Transient: true},
			wantStatus:  http.StatusServiceUnavailable,
			wantOutcome: audit.OutcomeLitellmUnreachable,
		},
		{
			name:        "connectivity error",
			err:         errors.New("dial tcp: connection refused"),
			wantStatus:  http.StatusServiceUnavailable,
			wantOutcome: audit.OutcomeLitellmUnreachable,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, oc, _ := classifyLitellmErr(tc.err)
			if st != tc.wantStatus || oc != tc.wantOutcome {
				t.Errorf("classifyLitellmErr() = (%d, %q), want (%d, %q)",
					st, oc, tc.wantStatus, tc.wantOutcome)
			}
		})
	}
}
