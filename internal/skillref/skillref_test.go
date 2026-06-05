// SPDX-License-Identifier: Apache-2.0

package skillref

import "testing"

func TestParseAndValid(t *testing.T) {
	cases := []struct {
		in     string
		name   string
		mkt    string
		scoped bool
		valid  bool
	}{
		{"pdf-processing", "pdf-processing", "", false, true},
		{"branding@ackstorm", "branding", "ackstorm", true, true},
		{"a@b@c", "a@b", "c", true, true}, // split on FINAL '@' (mirrors pluginref)
		{"@ackstorm", "", "ackstorm", true, false},
		{"branding@", "branding", "", true, false},
		{"", "", "", false, false},
	}
	for _, c := range cases {
		name, mkt, scoped := Parse(c.in)
		if name != c.name || mkt != c.mkt || scoped != c.scoped {
			t.Errorf("Parse(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, name, mkt, scoped, c.name, c.mkt, c.scoped)
		}
		if got := Valid(c.in); got != c.valid {
			t.Errorf("Valid(%q) = %v, want %v", c.in, got, c.valid)
		}
	}
}
