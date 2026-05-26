// SPDX-License-Identifier: Apache-2.0

package contentservice

import "strings"

const (
	// kindPrompt / kindPlugin / kindArtifact are the routed kinds the
	// handler accepts (Hub §15.2). Centralised so the router, the
	// helpers, and the Content-Type policy stay in lockstep.
	kindPrompt   = "prompt"
	kindPlugin   = "plugin"
	kindArtifact = "artifact"

	// contentTypeMarkdown is the §8 default Content-Type for prompts
	// that do not set Prompt.spec.contentType.
	contentTypeMarkdown = "text/markdown"

	// contentTypeGzip / contentTypeOctet are the response Content-Type
	// values produced by the per-kind policy below.
	contentTypeGzip  = "application/gzip"
	contentTypeOctet = "application/octet-stream"

	// gzipSuffix is the on-disk suffix the handler uses to disambiguate
	// artifact scope=directory (.tar.gz) vs scope=object (bare file).
	gzipSuffix = ".tar.gz"
)

// ContentTypeForFile returns the HTTP Content-Type to send for a file
// under the given kind. The override parameter is honored only for
// kind=prompt and corresponds to Prompt.spec.contentType (empty falls
// back to text/markdown, the §8 default).
//
// Policy:
//   - prompt:   override OR text/markdown
//   - plugin:   application/gzip       (always .tar.gz by layout)
//   - artifact: application/gzip when filename ends ".tar.gz" else
//     application/octet-stream
func ContentTypeForFile(kind, filename, override string) string {
	switch kind {
	case kindPrompt:
		if override != "" {
			return override
		}
		return contentTypeMarkdown
	case kindPlugin:
		return contentTypeGzip
	case kindArtifact:
		if strings.HasSuffix(filename, gzipSuffix) {
			return contentTypeGzip
		}
		return contentTypeOctet
	}
	return contentTypeOctet
}
