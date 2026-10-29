// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"testing"
)

// TestClassifyDownloadURL_EscapedSlashDecodesAndIsCaught pins the S2
// path-traversal defense: the server emits a PathEscaped name in the
// DownloadURL (e.g. "../admin" percent-encoded as "..%2Fadmin"); url.Parse's
// .Path field DECODES %2F back to "/"; strings.Split on "/" then isolates ".."
// as the name segment; and validateContentName rejects ".." as a traversal
// segment. A future refactor that switches classifyDownloadURL to u.RawPath
// (which preserves %2F undecoded, causing Split to yield "..%2Fadmin" instead
// of "..") would produce a name that passes validateContentName — silently
// regressing the defense. This test keeps that silent regression detectable.
func TestClassifyDownloadURL_EscapedSlashDecodesAndIsCaught(t *testing.T) {
	t.Parallel()

	_, name := classifyDownloadURL("https://host/content/artifact/..%2Fadmin", "fallback")
	// url.Parse decodes %2F → "/", Split yields ".." as the name segment.
	if name != ".." {
		t.Fatalf("expected url.Parse to decode %%2F and Split to yield \"..\", got %q", name)
	}
	if err := validateContentName(name); err == nil {
		t.Fatal("validateContentName must reject the decoded traversal segment")
	}
}
