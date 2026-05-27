//go:build integration

// SPDX-License-Identifier: Apache-2.0

// handler_test.go — replaced wholesale by the Plan 05-05 Task 4
// pipeline_test.go integration suite. The original §8-stub-era tests
// (TestHandler_PromptBody, TestHandler_PluginGzip, ...) asserted a
// handler shape that no longer exists post-Plan-05-05 D-16 (no authn,
// no envcache, no projection-row lookups, no Cache-Control: no-store).
//
// The current integration-test surface lives in pipeline_test.go and
// covers every D-03 outcome plus §12.3 precedence, the SC#4 inode-pin
// invariant, audit-emission shape, and the no-store header (drift flag
// #3 lockdown).
//
// This file is intentionally left as an empty integration-tagged Go
// source so the package still compiles without breaking any
// out-of-tree references to its previous tests.

package contentservice
