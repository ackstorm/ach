// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// textModes are the LiteLLM model modes GET /v1/models lists by default —
// what the console labels "chat". A chat client (LibreChat at
// chat.<domain>) would otherwise offer embedding, transcription, image…
// models it cannot use. ?types=<mode>[,<mode>…] adds LiteLLM modes on top;
// ?types=all turns the filter off. ACH's own views (console, env describe)
// read LiteLLM / the projection directly and are never filtered; any future
// ACH-internal call through here MUST pass ?types=all
// (TestNoInternalModelListWithoutTypesAll is a heuristic guard for it).
var textModes = []string{"chat", "completion"}

type modelModesKey struct{}

// wantsAllModels reports whether a ?types value (first param only, as
// prepareModelList reads it) names "all" anywhere in its comma list.
func wantsAllModels(types string) bool {
	for _, t := range strings.Split(types, ",") {
		if strings.ToLower(strings.TrimSpace(t)) == "all" {
			return true
		}
	}
	return false
}

// prepareModelList marks a GET /v1/models request for the response filter:
// it consumes ?types (LiteLLM never sees it) and drops Accept-Encoding so
// the Transport decompresses the upstream answer for us. Any other request
// is returned untouched.
func prepareModelList(r *http.Request) *http.Request {
	if r.Method != http.MethodGet || strings.TrimSuffix(r.URL.Path, "/") != "/v1/models" {
		return r
	}
	q := r.URL.Query()
	types := q.Get("types")
	q.Del("types")
	r.URL.RawQuery = q.Encode()
	allow := map[string]bool{}
	for _, m := range textModes {
		allow[m] = true
	}
	if wantsAllModels(types) {
		return r
	}
	for _, t := range strings.Split(types, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			allow[t] = true
		}
	}
	r.Header.Del("Accept-Encoding")
	return r.WithContext(context.WithValue(r.Context(), modelModesKey{}, allow))
}

// filterModelList drops the /v1/models items whose `mode` is not allowed.
// An item without a mode stays: LiteLLM omits it when neither the config nor
// its cost map knows the model, which is common for a custom chat model. A
// body that is not an OpenAI model list (Anthropic format, an error) passes
// through as it came.
func filterModelList(resp *http.Response) error {
	allow, ok := resp.Request.Context().Value(modelModesKey{}).(map[string]bool)
	if !ok || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxTagScanBody+1))
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if len(raw) > maxTagScanBody {
		return errBodyTooLarge // never pass a truncated list off as whole (502)
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	var list map[string]json.RawMessage
	var items []json.RawMessage
	if json.Unmarshal(raw, &list) != nil || json.Unmarshal(list["data"], &items) != nil {
		return nil
	}
	kept := make([]json.RawMessage, 0, len(items))
	for _, it := range items {
		var m struct {
			Mode *string `json:"mode"`
		}
		if json.Unmarshal(it, &m) != nil || m.Mode == nil || allow[*m.Mode] {
			kept = append(kept, it)
		}
	}
	if list["data"], err = json.Marshal(kept); err != nil {
		return err
	}
	out, err := json.Marshal(list)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
}
