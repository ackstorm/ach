// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"errors"
	"net/http"
	"testing"
)

func TestIsDuplicateTeamErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed 400 team/new body", &APIError{StatusCode: http.StatusBadRequest, Path: "/team/new",
			Body: []byte(`{"error":{"message":"Team id = ach-user-a@b.com already exists"}}`)}, true},
		{"typed 409", &APIError{StatusCode: http.StatusConflict, Path: "/team/new",
			Body: []byte("already exists")}, true},
		{"wrapped string", errors.New(`litellm: POST /team/new: 400 Team id = x already exists`), true},
		{"unrelated", &APIError{StatusCode: http.StatusInternalServerError, Path: "/team/new",
			Body: []byte("boom")}, false},
	}
	for _, c := range cases {
		if got := IsDuplicateTeamErr(c.err); got != c.want {
			t.Errorf("%s: IsDuplicateTeamErr = %v, want %v", c.name, got, c.want)
		}
	}
}
