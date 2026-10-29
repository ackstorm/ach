// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// modelsSegment is the fixed path element that precedes the model name in
// every Google AI Studio / Gemini native route ACH forwards:
//
//	/gemini/v1beta/models/<model>:generateContent
//	/gemini/v1beta/models/<model>:streamGenerateContent
//	/gemini/v1beta/models/<model>            (model metadata)
//
// A /gemini path without it (the OpenAI-compat sub-surface, where the model
// travels in the JSON body) carries no rewritable model name.
const modelsSegment = "/models/"

// stripGeminiModelPrefix removes the first matching prefix in prefixes from
// the model name of a Gemini native path, and returns the path unchanged
// when nothing matches.
//
// WHY this operates on the tail after modelsSegment and never on the raw
// path: a configured prefix may legitimately be "gemini/", which also
// matches ACH's own route prefix "/gemini/". Rewriting the raw path would
// silently eat the route. Anchoring at modelsSegment makes the route
// prefix unreachable by configuration.
//
// The tail deliberately runs to the END of the path rather than to the next
// "/": a prefix like "gemini/" puts a slash INSIDE the model name
// ("/models/gemini/gemini-3.7-flash:generateContent"), so cutting at the
// next separator would hide exactly the case the prefix exists to strip.
// The ":action" suffix rides along untouched — prefixes anchor at the start.
//
// Prefixes are tried in declared order, first match wins, and exactly one is
// removed: stripping repeatedly would turn a model that merely repeats the
// vendor token into a different model.
func stripGeminiModelPrefix(path string, prefixes []string) string {
	if len(prefixes) == 0 {
		return path
	}
	idx := strings.Index(path, modelsSegment)
	if idx < 0 {
		return path
	}
	head := path[:idx+len(modelsSegment)]
	tail := path[idx+len(modelsSegment):]
	for _, p := range prefixes {
		if p == "" || !strings.HasPrefix(tail, p) {
			continue
		}
		stripped := tail[len(p):]
		// A tail that is nothing but the prefix would forward a model-less
		// path upstream. Leave it alone and let LiteLLM answer for it.
		if stripped == "" || strings.HasPrefix(stripped, ":") {
			return path
		}
		return head + stripped
	}
	return path
}

// ParseGeminiStripModelPrefixes decodes the JSON string list the chart
// renders into ACH_GEMINI_STRIP_MODEL_PREFIXES (forwarder.gemini.
// stripModelPrefixes). Absent or empty disables the rewrite, which is the
// pre-feature behaviour.
//
// Order is preserved and load-bearing: the first matching prefix is the one
// removed, so a deployment listing both "gemini." and "gemini" decides for
// itself which wins. Duplicates and empty entries are configuration
// mistakes, not something to silently tolerate.
func ParseGeminiStripModelPrefixes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("gemini strip model prefixes: %w", err)
	}
	seen := make(map[string]bool, len(out))
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
		switch {
		case out[i] == "":
			return nil, errors.New("gemini strip model prefixes: empty entry")
		case seen[out[i]]:
			return nil, fmt.Errorf("gemini strip model prefixes: %q listed twice", out[i])
		}
		seen[out[i]] = true
	}
	return out, nil
}
