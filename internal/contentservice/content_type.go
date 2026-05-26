// SPDX-License-Identifier: Apache-2.0

package contentservice

import "strings"

// contentTypeMarkdown is the §8 default Content-Type for prompts that
// do not set Prompt.spec.contentType.
const contentTypeMarkdown = "text/markdown"

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
	case "prompt":
		if override != "" {
			return override
		}
		return contentTypeMarkdown
	case "plugin":
		return "application/gzip"
	case "artifact":
		if strings.HasSuffix(filename, ".tar.gz") {
			return "application/gzip"
		}
		return "application/octet-stream"
	}
	return "application/octet-stream"
}
