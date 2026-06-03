// SPDX-License-Identifier: Apache-2.0

package pluginref

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in         string
		wantName   string
		wantMkt    string
		wantScoped bool
	}{
		{"code-review", "code-review", "", false},
		{"code-review@anthropics-official", "code-review", "anthropics-official", true},
		{"a@b@c", "a@b", "c", true}, // marketplace is the final @-segment
	}
	for _, tc := range cases {
		name, mkt, scoped := Parse(tc.in)
		if name != tc.wantName || mkt != tc.wantMkt || scoped != tc.wantScoped {
			t.Errorf("Parse(%q) = (%q,%q,%v); want (%q,%q,%v)",
				tc.in, name, mkt, scoped, tc.wantName, tc.wantMkt, tc.wantScoped)
		}
	}
}

func TestValid(t *testing.T) {
	good := []string{"code-review", "code-review@anthropics-official"}
	bad := []string{"", "@mkt", "name@", "@", "name@@"}
	for _, s := range good {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false; want true", s)
		}
	}
	for _, s := range bad {
		if Valid(s) {
			t.Errorf("Valid(%q) = true; want false", s)
		}
	}
}
