// SPDX-License-Identifier: Apache-2.0

package conflict

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		want    Policy
		wantErr bool
	}{
		{"", Namespace, false},
		{"namespace", Namespace, false},
		{"NAMESPACE", Namespace, false},
		{"Skip", Skip, false},
		{"overwrite", Overwrite, false},
		{"refuse", Refuse, false},
		{"bogus", Namespace, true},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("Parse(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestString(t *testing.T) {
	for _, c := range []struct {
		p    Policy
		want string
	}{{Namespace, "namespace"}, {Skip, "skip"}, {Overwrite, "overwrite"}, {Refuse, "refuse"}} {
		if got := c.p.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.p, got, c.want)
		}
	}
}
