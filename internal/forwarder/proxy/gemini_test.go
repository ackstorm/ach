// SPDX-License-Identifier: Apache-2.0

package proxy

import "testing"

// The configured set from the ackstorm proxy: a dotted vendor prefix, a
// slashed one, and a second vendor.
var geminiPrefixes = []string{"gemini.", "gemini/", "vertex_ai."}

func TestStripGeminiModelPrefix(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "dotted vendor prefix is removed",
			in:   "/gemini/v1beta/models/gemini.gemini-3.7-flash:generateContent",
			want: "/gemini/v1beta/models/gemini-3.7-flash:generateContent",
		},
		{
			name: "slashed vendor prefix is removed, not the route prefix",
			in:   "/gemini/v1beta/models/gemini/gemini-3.7-flash:generateContent",
			want: "/gemini/v1beta/models/gemini-3.7-flash:generateContent",
		},
		{
			name: "second vendor prefix is removed",
			in:   "/gemini/v1beta/models/vertex_ai.gemini-3.8-flash:streamGenerateContent",
			want: "/gemini/v1beta/models/gemini-3.8-flash:streamGenerateContent",
		},
		{
			name: "model metadata route has no action suffix",
			in:   "/gemini/v1beta/models/gemini.gemini-3.7-flash",
			want: "/gemini/v1beta/models/gemini-3.7-flash",
		},
		{
			name: "an unprefixed model is forwarded unchanged",
			in:   "/gemini/v1beta/models/gemini-3.7-flash:generateContent",
			want: "/gemini/v1beta/models/gemini-3.7-flash:generateContent",
		},
		{
			name: "an unconfigured vendor prefix is left alone",
			in:   "/gemini/v1beta/models/bedrock.claude-opus:generateContent",
			want: "/gemini/v1beta/models/bedrock.claude-opus:generateContent",
		},
		{
			name: "only the first match is removed",
			in:   "/gemini/v1beta/models/gemini.gemini.gemini-3.7-flash:generateContent",
			want: "/gemini/v1beta/models/gemini.gemini-3.7-flash:generateContent",
		},
		{
			name: "a path without the models segment is untouched",
			in:   "/gemini/v1beta/openai/chat/completions",
			want: "/gemini/v1beta/openai/chat/completions",
		},
		{
			name: "the route prefix is never eaten by the gemini/ entry",
			in:   "/gemini/v1beta/models/other-model:generateContent",
			want: "/gemini/v1beta/models/other-model:generateContent",
		},
		{
			name: "a tail that is only the prefix is left for LiteLLM to reject",
			in:   "/gemini/v1beta/models/gemini.",
			want: "/gemini/v1beta/models/gemini.",
		},
		{
			name: "a prefix followed straight by the action is left alone",
			in:   "/gemini/v1beta/models/gemini.:generateContent",
			want: "/gemini/v1beta/models/gemini.:generateContent",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripGeminiModelPrefix(tc.in, geminiPrefixes); got != tc.want {
				t.Fatalf("stripGeminiModelPrefix(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// An unconfigured deployment must behave exactly as it did before the
// feature existed: no rewrite on any path.
func TestStripGeminiModelPrefix_Unconfigured(t *testing.T) {
	const in = "/gemini/v1beta/models/gemini.gemini-3.7-flash:generateContent"
	for _, prefixes := range [][]string{nil, {}} {
		if got := stripGeminiModelPrefix(in, prefixes); got != in {
			t.Fatalf("with %v prefixes: got %q, want the path unchanged", prefixes, got)
		}
	}
}
