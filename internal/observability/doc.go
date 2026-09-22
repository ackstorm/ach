// SPDX-License-Identifier: Apache-2.0

// Package observability holds the pure aggregation folds behind the unified
// console's stats/latency/budget surfaces — no HTTP, no LiteLLM client, fully
// unit-testable. Task 4 wires them behind GET /platform/console/{stats,latency}.
//
// This is a direct port of alitellm-auth's app/stats.py, app/latency.py, and
// the _budget_block helper in app/session.py: the route layer fetches raw
// LiteLLM dicts (daily-activity windows, spend-log rows, user/team-member
// budget blocks) and hands them to these functions, which fold them into the
// page-ready contracts the console renders verbatim (the server shapes the
// contract; the UI is a dumb renderer).
//
// D-08 null-vs-0: an UNAVAILABLE figure (a *_pct whose denominator is 0, a
// last_used with no source, ...) is a nil pointer, serialized as JSON null;
// a figure that is genuinely 0 in the window stays a real 0. Every contract
// struct therefore uses pointer fields for nullable figures and never a Go
// `,omitempty` on them — the JSON key is always present.
package observability
