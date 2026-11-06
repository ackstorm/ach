// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// modelsSegment is the fixed path element that precedes the model name in
// every Google AI Studio / Gemini native route ACH forwards:
//
//	/gemini/v1beta/models/<model>:generateContent
//	/gemini/v1beta/models/<model>:streamGenerateContent
//	/gemini/v1beta/models/<model>            (model metadata)
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

// geminiMethods are the POST actions ACH forwards on a model: the chat, token
// count and embedding calls Gemini clients make. Every other action stays
// closed until it is reviewed. The two embedding actions record no spend in
// LiteLLM (references/litellm-permission-model.md).
var geminiMethods = map[string]bool{
	"generateContent":       true,
	"streamGenerateContent": true,
	"countTokens":           true,
	"embedContent":          true,
	"batchEmbedContents":    true,
}

// geminiPathModel applies the /gemini allowlist to r: the list and get
// routes on GET, and a model action from geminiMethods on POST, all under
// /v1beta/models, exact and case-sensitive. The model is one decoded path
// segment once a configured vendor prefix is stripped, and it is taken
// literally: an encoded slash, a dot segment, a colon, %, ? or # never names
// a model.
// model is "" for the list route.
func geminiPathModel(r *http.Request, prefixes []string) (model string, ok bool) {
	rest, found := strings.CutPrefix(r.URL.Path, "/gemini/v1beta/models")
	if !found || strings.Contains(strings.ToLower(r.URL.RawPath), "%2f") {
		return "", false
	}
	if rest == "" {
		return "", r.Method == http.MethodGet
	}
	tail, found := strings.CutPrefix(rest, "/")
	if !found {
		return "", false
	}
	model = tail
	if r.Method == http.MethodPost {
		var method string
		model, method, found = strings.Cut(tail, ":")
		if !found || !geminiMethods[method] {
			return "", false
		}
	} else if r.Method != http.MethodGet {
		return "", false
	}
	// A configured prefix may itself hold a slash ("gemini/"), so it is
	// stripped before the one-segment test; any other slash stays refused.
	seg := strings.TrimPrefix(stripGeminiModelPrefix(modelsSegment+model, prefixes), modelsSegment)
	if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "/:%?#") {
		return "", false
	}
	return model, true
}

// normGeminiModel reduces a model name to the form LiteLLM resolves: without
// a leading "models/" (SDKs send it in bodies) and without a configured
// vendor prefix.
func normGeminiModel(name string, prefixes []string) string {
	name = strings.TrimPrefix(name, "models/")
	return strings.TrimPrefix(stripGeminiModelPrefix(modelsSegment+name, prefixes), modelsSegment)
}

// bodyModelMatches reports whether a request body, when there is one, names
// no model other than the path's: one request addresses one model, and the
// body must agree with the path on which. Only a JSON object can be compared,
// so any other declared Content-Type or body shape is refused. The entries of
// a batchEmbedContents body each name a model too, and must agree the same way.
func bodyModelMatches(r *http.Request, body []byte, pathModel string, prefixes []string) bool {
	if len(strings.TrimSpace(string(body))) == 0 {
		return true
	}
	if mt := mediaType(r.Header.Get("Content-Type")); mt != "" && mt != "application/json" {
		return false
	}
	var b map[string]json.RawMessage
	if json.Unmarshal(body, &b) != nil {
		return false
	}
	// A second model field has no place on these routes.
	if _, ok := b["target_model_names"]; ok {
		return false
	}
	namesPath := func(raw json.RawMessage) bool {
		var name string
		return json.Unmarshal(raw, &name) == nil &&
			normGeminiModel(name, prefixes) == normGeminiModel(pathModel, prefixes)
	}
	if raw, ok := b["model"]; ok && !namesPath(raw) {
		return false
	}
	if !strings.HasSuffix(r.URL.Path, ":batchEmbedContents") {
		return true
	}
	var entries []map[string]json.RawMessage
	if raw, ok := b["requests"]; ok && json.Unmarshal(raw, &entries) != nil {
		return false
	}
	for _, e := range entries {
		if e == nil { // a JSON null entry
			return false
		}
		if raw, ok := e["model"]; ok && !namesPath(raw) {
			return false
		}
	}
	return true
}
