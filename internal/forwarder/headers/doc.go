// SPDX-License-Identifier: Apache-2.0

// Package headers is the outbound header transform the forwarder applies on
// every route before the request reaches LiteLLM: drop x-ach-*, set
// x-litellm-api-key. Pure, stdlib only.
package headers
