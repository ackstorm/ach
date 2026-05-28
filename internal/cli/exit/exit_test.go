// SPDX-License-Identifier: Apache-2.0

package exit_test

import (
	"errors"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// TestMapServerError_Nil asserts Test 11: nil → OK.
func TestMapServerError_Nil(t *testing.T) {
	if got := exit.MapServerError(nil); got != exit.OK {
		t.Errorf("MapServerError(nil) = %d, want OK (0)", got)
	}
}

// TestMapServerError_401_AuthN asserts Test 12.
func TestMapServerError_401_AuthN(t *testing.T) {
	got := exit.MapServerError(&httpclient.ServerError{Status: 401})
	if got != exit.AuthN {
		t.Errorf("MapServerError(401) = %d, want AuthN (3)", got)
	}
}

// TestMapServerError_403_NotAdmin asserts Test 13.
func TestMapServerError_403_NotAdmin(t *testing.T) {
	got := exit.MapServerError(&httpclient.ServerError{Status: 403, Code: "not_admin"})
	if got != exit.AuthN {
		t.Errorf("MapServerError(403/not_admin) = %d, want AuthN (3)", got)
	}
}

// TestMapServerError_403_UnauthorizedTeam asserts Test 14.
func TestMapServerError_403_UnauthorizedTeam(t *testing.T) {
	got := exit.MapServerError(&httpclient.ServerError{Status: 403, Code: "unauthorized_team"})
	if got != exit.AuthN {
		t.Errorf("MapServerError(403/unauthorized_team) = %d, want AuthN (3)", got)
	}
}

// TestMapServerError_403_OtherCode asserts Test 15.
func TestMapServerError_403_OtherCode(t *testing.T) {
	got := exit.MapServerError(&httpclient.ServerError{Status: 403, Code: "missing_environment"})
	if got != exit.General {
		t.Errorf("MapServerError(403/missing_environment) = %d, want General (1)", got)
	}
}

// TestMapServerError_503_Network asserts Test 16.
func TestMapServerError_503_Network(t *testing.T) {
	got := exit.MapServerError(&httpclient.ServerError{Status: 503})
	if got != exit.Network {
		t.Errorf("MapServerError(503) = %d, want Network (6)", got)
	}
}

// TestMapServerError_500_General asserts Test 17.
func TestMapServerError_500_General(t *testing.T) {
	got := exit.MapServerError(&httpclient.ServerError{Status: 500})
	if got != exit.General {
		t.Errorf("MapServerError(500) = %d, want General (1)", got)
	}
}

// TestCodedError_Error asserts Test 18: CodedError.Error() returns Msg
// verbatim; Unwrap returns Wrapped.
func TestCodedError_Error(t *testing.T) {
	ce := &exit.CodedError{Code: exit.Network, Msg: "x"}
	if ce.Error() != "x" {
		t.Errorf("CodedError.Error() = %q, want %q", ce.Error(), "x")
	}
	inner := errors.New("inner")
	wrap := &exit.CodedError{Code: exit.ConfigFile, Msg: "wrap", Wrapped: inner}
	if !errors.Is(wrap, inner) {
		t.Errorf("errors.Is failed on wrapped CodedError")
	}
}

// TestExitCodeConstants asserts the value of every exit-code constant
// is the §9.3 row number — defensive against typo regressions.
func TestExitCodeConstants(t *testing.T) {
	cases := []struct {
		name string
		got  exit.Code
		want int
	}{
		{"OK", exit.OK, 0},
		{"General", exit.General, 1},
		{"AuthN", exit.AuthN, 3},
		{"Network", exit.Network, 6},
		{"ConfigFile", exit.ConfigFile, 8},
	}
	for _, tc := range cases {
		if int(tc.got) != tc.want {
			t.Errorf("exit.%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// TestMapServerError_504_Network asserts the 504 → Network arm too
// (the action block lists 503 + 504 as Network).
func TestMapServerError_504_Network(t *testing.T) {
	got := exit.MapServerError(&httpclient.ServerError{Status: 504})
	if got != exit.Network {
		t.Errorf("MapServerError(504) = %d, want Network (6)", got)
	}
}
