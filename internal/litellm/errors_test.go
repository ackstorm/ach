// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed sentinel", ErrNotFound, true},
		{"wrapped sentinel", fmt.Errorf("x: %w", ErrNotFound), true},
		{"legacy 404 string", errors.New("litellm: GET /user/info status: 404"), true},
		{"other error", errors.New("boom"), false},
	}
	for _, c := range cases {
		if got := IsNotFound(c.err); got != c.want {
			t.Errorf("%s: IsNotFound = %v, want %v", c.name, got, c.want)
		}
	}
}
