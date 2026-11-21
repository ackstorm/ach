// SPDX-License-Identifier: Apache-2.0

package adapter

import "testing"

func TestWithFrontmatter(t *testing.T) {
	got := string(WithFrontmatter("alwaysApply: true", []byte("# Rules\n")))
	want := "---\nalwaysApply: true\n---\n\n# Rules\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
