// SPDX-License-Identifier: Apache-2.0

package hydrate

import "testing"

func TestParseConflictPolicy(t *testing.T) {
	cases := []struct {
		in      string
		want    ConflictPolicy
		wantErr bool
	}{
		{"namespace", ConflictNamespace, false},
		{"skip", ConflictSkip, false},
		{"overwrite", ConflictOverwrite, false},
		{"refuse", ConflictRefuse, false},
		{"", ConflictNamespace, false},
		{"NAMESPACE", ConflictNamespace, false},
		{"bogus", ConflictNamespace, true},
	}
	for _, c := range cases {
		got, err := ParseConflictPolicy(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseConflictPolicy(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if err == nil && got != c.want {
			t.Errorf("ParseConflictPolicy(%q) = %v want %v", c.in, got, c.want)
		}
	}
}

func TestConflictPolicy_String(t *testing.T) {
	if ConflictNamespace.String() != "namespace" {
		t.Errorf("String() = %q want namespace", ConflictNamespace.String())
	}
}
