// SPDX-License-Identifier: Apache-2.0

// Package contentservice implements the ACH Content Service HTTP surface
// (Hub §15.2, TODO §8). It serves three routes:
//
//	GET /content/prompt/{name}    -> raw bytes, Content-Type from
//	                                 Prompt.spec.contentType (default
//	                                 text/markdown)
//	GET /content/plugin/{name}    -> .tar.gz, Content-Type: application/gzip
//	GET /content/artifact/{name}  -> raw file (scope=object) or .tar.gz
//	                                 (scope=directory); Content-Type is
//	                                 application/gzip for .tar.gz,
//	                                 application/octet-stream otherwise
//
// Files live under ACH_CACHE_ROOT (default /var/cache/ach) per the
// §10.3 layout that the Operator's reconcilers + cachefs.EnsureLayout
// publish to:
//
//	prompt/<name>
//	plugin/<name>.tar.gz
//	artifact/<name>          (scope=object)
//	artifact/<name>.tar.gz   (scope=directory)
//
// v1alpha1 contract (TODO §8):
//   - Auth: anonymous (Phase 5 will add pk_/ek_ + environment scoping)
//   - Range: SHOULD support (delegated to http.ServeContent stdlib)
//   - Cache-Control: public, max-age=300
//   - 404 only when the cache file is absent — not when the route is
//     unrouted (route absence yields chi's default 404, but this plan
//     ensures every {prompt,plugin,artifact} kind IS routed).
//
// Streaming uses http.ServeContent, which on Linux drives io.Copy from
// *os.File into the response's TCP socket — net/http's ReadFrom path
// engages internal/poll.SendFile, giving zero-copy without us reaching
// for syscall.Sendfile directly.
package contentservice
