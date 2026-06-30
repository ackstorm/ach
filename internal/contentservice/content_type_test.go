// SPDX-License-Identifier: Apache-2.0

package contentservice

import "testing"

// contentTypeFor now serves every known context kind as application/gzip
// (uniform context format) — prompt's spec.contentType override is no
// longer reflected on the wire.
func TestContentTypeFor_AllKindsGzip(t *testing.T) {
	md := "text/markdown"
	cases := []struct {
		kind string
		row  *contentRow
	}{
		{kindPrompt, &contentRow{ContentType: &md}}, // override now ignored for the wire
		{kindPrompt, &contentRow{}},
		{kindPlugin, &contentRow{}},
		{kindSkill, &contentRow{}},
		{kindArtifact, &contentRow{Scope: "object"}},
		{kindArtifact, &contentRow{Scope: "directory"}},
	}
	for _, tc := range cases {
		if got := contentTypeFor(tc.kind, tc.row); got != contentTypeGzip {
			t.Errorf("contentTypeFor(%q, scope=%q)=%q, want %q", tc.kind, tc.row.Scope, got, contentTypeGzip)
		}
	}
}
