// SPDX-License-Identifier: Apache-2.0

package contentservice

const (
	// kindPrompt / kindPlugin / kindArtifact are the routed kinds the
	// handler accepts (Hub §15.2). Centralised so the router, the
	// helpers, and the Content-Type policy stay in lockstep.
	kindPrompt   = "prompt"
	kindPlugin   = "plugin"
	kindArtifact = "artifact"
	kindSkill    = "skill"

	// contentTypeGzip / contentTypeOctet are the response Content-Type
	// values produced by the per-kind policy (contentTypeFor in
	// pipeline.go).
	contentTypeGzip  = "application/gzip"
	contentTypeOctet = "application/octet-stream"

	// gzipSuffix is the on-disk suffix the handler uses to disambiguate
	// artifact scope=directory (.tar.gz) vs scope=object (bare file).
	gzipSuffix = ".tar.gz"
)
