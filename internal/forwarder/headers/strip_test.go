// SPDX-License-Identifier: Apache-2.0

package headers_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/ackstorm/ach/internal/forwarder/headers"
)

// TestStripAndRewrite drives the D-06 (strip list) + D-07 (write list) contract
// for the forwarder's pure-function header transform. Every case constructs an
// input http.Header, calls StripAndRewrite, and compares the result with
// reflect.DeepEqual against the expected canonical-case http.Header.
//
// Per CONTEXT D-23, the test budget is ~30 cases. This file ships 30+ cases
// covering: case-insensitive prefix strips (x-litellm-* / x-ach-*), full
// RFC 7230 §6.1 hop-by-hop list, Connection-token-named strip with degenerate
// edge cases (empty, whitespace-only, comma-only), write pass after strip,
// pass-through preservation, idempotency, multi-value headers, and prior-value
// override (T-04-01-02 mitigation).
func TestStripAndRewrite(t *testing.T) {
	const (
		masterKey    = "sk-litellm-test-shared-key"
		litellmToken = "litellm-token-xyz"
	)

	// twoWritten is the canonical D-07 output that every case (except those
	// that explicitly override the masterKey/litellmToken) ends up with as
	// the two written headers — written via h.Set, which canonicalizes the
	// key to X-Litellm-Api-Key / X-Litellm-Key-Id.
	// We add "Bearer " prefix to satisfy LiteLLM's internal MCP key parser.
	twoWritten := func() http.Header {
		h := http.Header{}
		h.Set("x-litellm-api-key", "Bearer "+masterKey)
		h.Set("x-litellm-key-id", litellmToken)
		return h
	}

	cases := []struct {
		name         string
		in           http.Header
		masterKey    string
		litellmToken string
		want         http.Header
	}{
		// ---- Test 1: strip Authorization (every scheme) ----
		{
			name: "01_strip_Authorization_Bearer",
			in: http.Header{
				"Authorization": {"Bearer xyz"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},
		{
			name: "01b_strip_Authorization_Basic",
			in: http.Header{
				"Authorization": {"Basic dXNlcjpwYXNz"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 2: case-insensitive x-litellm-* strip ----
		{
			name: "02_strip_x-litellm_mixed_case",
			in: http.Header{
				"X-Litellm-Foo": {"v"},
				"X-LITELLM-BAR": {"v2"},
				"x-litellm-baz": {"v3"},
				"X-LiTeLlM-Qux": {"v4"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 3: case-insensitive x-ach-* strip ----
		{
			name: "03_strip_x-ach_mixed_case",
			in: http.Header{
				"X-Ach-Key":         {"pk_abc"},
				"X-ACH-Environment": {"prod"},
				"x-ach-something":   {"v"},
				"X-aCh-Other":       {"v2"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 4: hop-by-hop strip per RFC 7230 §6.1 ----
		{
			name: "04_strip_full_hop_by_hop",
			in: http.Header{
				"Connection":          {"close"},
				"Keep-Alive":          {"timeout=5"},
				"Proxy-Authenticate":  {"Basic"},
				"Proxy-Authorization": {"Bearer x"},
				"Te":                  {"trailers"},
				"Trailer":             {"X-Stream-Done"},
				"Transfer-Encoding":   {"chunked"},
				"Upgrade":             {"websocket"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 5: Connection-named tokens strip ----
		{
			name: "05_connection_token_strip",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Connection", "X-Custom-Header, Foo")
				h.Set("X-Custom-Header", "v")
				h.Set("Foo", "w")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 6: Connection with whitespace + empty tokens ----
		{
			name: "06_connection_whitespace_and_empty_tokens",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Connection", " , X-A , , X-B ,")
				h.Set("X-A", "va")
				h.Set("X-B", "vb")
				h.Set("Stay", "kept")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("Stay", "kept")
				return h
			}(),
		},

		// ---- Test 7: Connection comma/whitespace-only — no panic ----
		{
			name: "07_connection_comma_only",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Connection", ", ,")
				h.Set("Stay", "kept")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("Stay", "kept")
				return h
			}(),
		},
		{
			name: "07b_connection_only_whitespace",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Connection", "   ")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 8: write pass — two headers ----
		{
			name:         "08_write_pass_two_headers",
			in:           http.Header{},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 9: pass-through preservation ----
		{
			name: "09_pass_through_preservation",
			in: func() http.Header {
				h := http.Header{}
				h.Set("User-Agent", "ach-cli/1.0")
				h.Set("Accept", "application/json")
				h.Set("Content-Type", "application/json")
				h.Set("Content-Length", "42")
				h.Set("Accept-Encoding", "gzip")
				h.Set("X-Forwarded-For", "10.0.0.1")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("User-Agent", "ach-cli/1.0")
				h.Set("Accept", "application/json")
				h.Set("Content-Type", "application/json")
				h.Set("Content-Length", "42")
				h.Set("Accept-Encoding", "gzip")
				h.Set("X-Forwarded-For", "10.0.0.1")
				return h
			}(),
		},

		// ---- Test 11: empty/nil input ----
		{
			name:         "11_empty_input",
			in:           http.Header{},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 12: prior x-litellm-api-key value gets overwritten ----
		{
			name: "12_prior_value_override",
			in: func() http.Header {
				h := http.Header{}
				h.Set("X-Litellm-Api-Key", "ATTACKER-SUPPLIED-KEY")
				h.Set("X-Litellm-Key-Id", "ATTACKER-KEY-ID")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 13: multiple x-ach-* simultaneously ----
		{
			name: "13_multiple_x_ach_simultaneously",
			in: http.Header{
				"X-Ach-Key":         {"pk_one"},
				"X-Ach-Environment": {"env1"},
				"X-Ach-Foo":         {"v1"},
				"X-Ach-Bar":         {"v2"},
				"X-Ach-Baz":         {"v3"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 14: multiple x-litellm-* simultaneously ----
		{
			name: "14_multiple_x_litellm_simultaneously",
			in: http.Header{
				"X-Litellm-A": {"v1"},
				"X-Litellm-B": {"v2"},
				"X-Litellm-C": {"v3"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 15: hop-by-hop + Connection-named together ----
		{
			name: "15_hop_by_hop_plus_connection_named",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Connection", "X-Smuggle-Me")
				h.Set("X-Smuggle-Me", "boom")
				h.Set("Keep-Alive", "timeout=5")
				h.Set("Transfer-Encoding", "chunked")
				h.Set("Stay", "yes")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("Stay", "yes")
				return h
			}(),
		},

		// ---- Test 16: combinations of x-ach-* and x-litellm-* with mixed case ----
		{
			name: "16_combined_x_ach_and_x_litellm_mixed_case",
			in: http.Header{
				"x-ach-key":     {"pk_v"},
				"X-LITELLM-foo": {"v"},
				"X-aCh-Other":   {"v2"},
				"X-LiTeLlM-Bar": {"v3"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 17: multi-value Connection across separate header entries ----
		{
			name: "17_multi_value_connection_header",
			in: func() http.Header {
				h := http.Header{}
				h.Add("Connection", "X-Custom-A")
				h.Add("Connection", "X-Custom-B, X-Custom-C")
				h.Set("X-Custom-A", "va")
				h.Set("X-Custom-B", "vb")
				h.Set("X-Custom-C", "vc")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 18: TE in Connection tokens (already in static list) — no double-action ----
		{
			name: "18_TE_strip",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Te", "trailers")
				h.Set("Connection", "Te")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 19: pass-through with X-Forwarded-Proto / X-Forwarded-Host ----
		{
			name: "19_x_forwarded_pass_through",
			in: func() http.Header {
				h := http.Header{}
				h.Set("X-Forwarded-For", "10.0.0.1")
				h.Set("X-Forwarded-Proto", "https")
				h.Set("X-Forwarded-Host", "ach.example.com")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("X-Forwarded-For", "10.0.0.1")
				h.Set("X-Forwarded-Proto", "https")
				h.Set("X-Forwarded-Host", "ach.example.com")
				return h
			}(),
		},

		// ---- Test 20: Cookie + Cache-Control pass through ----
		{
			name: "20_cookie_cache_control_pass_through",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Cookie", "session=abc")
				h.Set("Cache-Control", "no-cache")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("Cookie", "session=abc")
				h.Set("Cache-Control", "no-cache")
				return h
			}(),
		},

		// ---- Test 21: empty values for shared key + litellm token ----
		{
			name:         "21_empty_shared_key_and_token",
			in:           http.Header{"Authorization": {"Bearer x"}},
			masterKey:    "",
			litellmToken: "",
			want: func() http.Header {
				h := http.Header{}
				h.Set("x-litellm-api-key", "Bearer ")
				h.Set("x-litellm-key-id", "")
				return h
			}(),
		},

		// ---- Test 22: multi-value Accept header preserved ----
		{
			name: "22_multi_value_accept_preserved",
			in: func() http.Header {
				h := http.Header{}
				h.Add("Accept", "application/json")
				h.Add("Accept", "text/event-stream")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Add("Accept", "application/json")
				h.Add("Accept", "text/event-stream")
				return h
			}(),
		},

		// ---- Test 23: Authorization plus pass-through plus strip ----
		{
			name: "23_full_mix",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Authorization", "Bearer x")
				h.Set("X-Ach-Key", "pk_a")
				h.Set("X-Litellm-Foo", "v")
				h.Set("User-Agent", "curl/8")
				h.Set("Content-Type", "application/json")
				h.Set("Connection", "X-Smuggle")
				h.Set("X-Smuggle", "boom")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("User-Agent", "curl/8")
				h.Set("Content-Type", "application/json")
				return h
			}(),
		},

		// ---- Test 24: only-Connection-no-named-targets — Connection stripped, no panic ----
		{
			name: "24_connection_with_no_named_targets",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Connection", "X-Does-Not-Exist, X-Also-Missing")
				h.Set("Real", "value")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("Real", "value")
				return h
			}(),
		},

		// ---- Test 25: Authorization mixed case canonical form is "Authorization" ----
		{
			name: "25_authorization_lowercase_canonical_still_stripped",
			in: http.Header{
				// http.Header is canonical-case; the key here is already canonical.
				"Authorization": {"Bearer xyz"},
				"X-Other":       {"keep"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("X-Other", "keep")
				return h
			}(),
		},

		// ---- Test 26: Empty Connection header value ----
		{
			name: "26_empty_connection_value",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Connection", "")
				h.Set("Keep", "kept")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("Keep", "kept")
				return h
			}(),
		},

		// ---- Test 27: case-insensitivity for hop-by-hop ----
		{
			name: "27_hop_by_hop_canonical_only",
			in: http.Header{
				// http.Header always canonicalizes keys; we exercise the canonical-form
				// path explicitly.
				"Keep-Alive":        {"timeout=5"},
				"Transfer-Encoding": {"chunked"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 28: Connection token referencing a header that does not exist ----
		{
			name: "28_connection_names_nonexistent_header",
			in: func() http.Header {
				h := http.Header{}
				h.Set("Connection", "X-Missing")
				h.Set("X-Present", "yes")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want: func() http.Header {
				h := twoWritten()
				h.Set("X-Present", "yes")
				return h
			}(),
		},

		// ---- Test 29: pure x-ach-key without other headers ----
		{
			name: "29_pure_x_ach_key_only",
			in: http.Header{
				"X-Ach-Key": {"pk_alone"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 30: multi-value x-litellm-* header — all entries stripped ----
		{
			name: "30_multi_value_x_litellm_strip",
			in: func() http.Header {
				h := http.Header{}
				h.Add("X-Litellm-Foo", "v1")
				h.Add("X-Litellm-Foo", "v2")
				return h
			}(),
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},

		// ---- Test 10 (idempotency, placed last so the underlying StripAndRewrite call appears twice — see subtest below) ----
		{
			name: "10_idempotency_marker", // exercised by the dedicated subtest below
			in: http.Header{
				"Authorization": {"Bearer x"},
				"X-Ach-Key":     {"pk_idem"},
			},
			masterKey:    masterKey,
			litellmToken: litellmToken,
			want:         twoWritten(),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := cloneHeader(tc.in)
			headers.StripAndRewrite(got, tc.masterKey, tc.litellmToken)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("StripAndRewrite mismatch\n got=%#v\nwant=%#v", got, tc.want)
			}
		})
	}

	// Dedicated idempotency subtest — calls StripAndRewrite twice on the same
	// header and asserts the result is byte-identical to a single call.
	t.Run("idempotent_double_invocation", func(t *testing.T) {
		in := http.Header{
			"Authorization": {"Bearer x"},
			"X-Ach-Key":     {"pk_idem"},
			"User-Agent":    {"curl/8"},
		}
		// Single call reference.
		single := cloneHeader(in)
		headers.StripAndRewrite(single, masterKey, litellmToken)
		// Double call.
		dbl := cloneHeader(in)
		headers.StripAndRewrite(dbl, masterKey, litellmToken)
		headers.StripAndRewrite(dbl, masterKey, litellmToken)
		if !reflect.DeepEqual(single, dbl) {
			t.Fatalf("StripAndRewrite not idempotent\nsingle=%#v\ndouble=%#v", single, dbl)
		}
	})

	// No-panic guarantee on adversarial input shapes per threat model T-04-01-05.
	t.Run("no_panic_on_degenerate_connection", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("StripAndRewrite panicked on adversarial Connection shape: %v", r)
			}
		}()
		shapes := []string{
			"",
			" ",
			",",
			" , , ",
			",,,,,",
			"\t,\t",
		}
		for _, shape := range shapes {
			h := http.Header{}
			h.Set("Connection", shape)
			headers.StripAndRewrite(h, masterKey, litellmToken)
		}
	})
}

// cloneHeader returns a deep copy of h so each subtest mutates its own
// instance — http.Header is a map[string][]string and shares the underlying
// slice references when copied shallow.
func cloneHeader(h http.Header) http.Header {
	out := http.Header{}
	for k, vs := range h {
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}
