// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package github

import "io"

// drainAndClose is the REL-04 helper duplicated from
// internal/litellm/transport.go to avoid an internal/sources/github →
// internal/litellm dep edge. Every code path that holds a tarball
// response body MUST call this before returning so HTTP keepalive can
// reuse the connection and goroutines/FDs do not leak.
//
// Both Copy and Close errors are intentionally ignored: drain is
// best-effort (a slow server should never block the caller), and
// double-close on Close is a no-op on net/http's response body.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}
