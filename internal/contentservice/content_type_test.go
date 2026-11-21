// SPDX-License-Identifier: Apache-2.0

package contentservice

import "testing"

// contentTypeFor serves every known context kind as application/gzip
// (uniform context format).
func TestContentTypeFor_AllKindsGzip(t *testing.T) {
	for _, kind := range []string{kindInstruction, kindPlugin, kindSkill, kindArtifact} {
		if got := contentTypeFor(kind); got != contentTypeGzip {
			t.Errorf("contentTypeFor(%q)=%q, want %q", kind, got, contentTypeGzip)
		}
	}
}
