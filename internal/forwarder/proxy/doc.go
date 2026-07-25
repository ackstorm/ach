// SPDX-License-Identifier: Apache-2.0

// Package proxy implements the Forwarder's reverse-proxy plane: one
// shared *httputil.ReverseProxy (proxy.New) with Director that strips
// the client header surface and rewrites scheme/host per FWD-01 + D-05,
// plus five per-route http.HandlerFunc factories (HandlerV1, HandlerV2,
// HandlerGemini, HandlerMCP, HandlerA2A) that glue Plans 04-01 (headers),
// 04-02 (jwt), 04-04 (bip indexer), 04-06 (precheck) into the §5.1
// step-4 request flow.
//
// FWD-06 (Environment attribution tag injection) is implemented in
// tags.go and called by HandlerV1 + HandlerV2 + HandlerGemini for ek_ traffic only;
// MCP/A2A request-body tagging is deferred to v1beta1 per CONTEXT.md
// <deferred>.
package proxy
