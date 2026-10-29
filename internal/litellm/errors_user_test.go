// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsDuplicateUserErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			"typed 409 on /user/new (prod signature)",
			&APIError{Method: "POST", Path: "/user/new", StatusCode: 409, Code: "409",
				Body: []byte(`{"error":{"message":"User with id a@b.com already exists","code":"409"}}`)},
			true,
		},
		{
			"typed already-exists body on non-409 status",
			&APIError{Method: "POST", Path: "/user/new", StatusCode: 400, Code: "400",
				Body: []byte("User already exists")},
			true,
		},
		{
			"wrapped typed 409 still matches via errors.As",
			fmt.Errorf("provision: %w", &APIError{Method: "POST", Path: "/user/new", StatusCode: 409, Code: "409"}),
			true,
		},
		{
			"string fallback (body stripped, status+path in message)",
			errors.New("litellm: 409 on POST /user/new (code=409)"),
			true,
		},
		{
			"unrelated 409 on a different path does not match",
			&APIError{Method: "POST", Path: "/team/new", StatusCode: 409, Code: "409"},
			false,
		},
		{
			"unrelated error",
			errors.New("litellm: 500 on POST /user/new (code=500, transient)"),
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsDuplicateUserErr(tc.err); got != tc.want {
				t.Errorf("IsDuplicateUserErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
