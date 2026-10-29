// SPDX-License-Identifier: Apache-2.0

package teams_test

import (
	"testing"

	"github.com/ackstorm/ach/internal/platformapi/teams"
)

func TestHasIntersect(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{nil, nil, false},
		{[]string{"x"}, nil, false},
		{nil, []string{"x"}, false},
		{[]string{"x"}, []string{"y"}, false},
		{[]string{"x", "y"}, []string{"y", "z"}, true},
		{[]string{"a"}, []string{"a"}, true},
	}
	for i, c := range cases {
		if got := teams.HasIntersect(c.a, c.b); got != c.want {
			t.Errorf("case %d: HasIntersect(%v,%v)=%v want %v", i, c.a, c.b, got, c.want)
		}
	}
}
