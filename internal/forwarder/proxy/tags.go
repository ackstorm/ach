// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	// tagPrefix is the FWD-06 tag namespace. All Environment attribution
	// tags ship as "environment:<name>". Greppable, parseable upstream by
	// LiteLLM's tag aggregation per Hub §6.3.
	tagPrefix = "environment:"

	// headerTags mirrors the injected tag into an out-of-band x- header so
	// the SC2 e2e can observe at the LLM backend that ek_ traffic was tagged.
	// LiteLLM strips metadata.tags from the request body before forwarding to
	// the backend (it consumes them for tag-spend tracking), so the body tag
	// is NOT backend-observable; this header is. It rides ONLY the
	// body-injection success path below, so its presence is a faithful proxy
	// for "the Environment tag was injected" (ek_ → present, pk_ → absent).
	//
	// The name deliberately does NOT use the "x-ach-" prefix: the forwarder's
	// own headers.StripAndRewrite (the D-06 strip) drops every "x-ach-*" and
	// "x-litellm-*" header from the upstream request, so an "x-ach-tags"
	// header would be removed by the Director before it ever reached LiteLLM.
	// "x-achtest-" sits outside that prefix yet stays "x-*", which is what
	// LiteLLM's forward_client_headers_to_llm_api forwards to the backend.
	// In production LiteLLM does not enable that setting, so the header is
	// dropped before any real backend; only the test cluster forwards it,
	// scoped to the demo-model group.
	headerTags = "X-Achtest-Tags"

	// maxBodyForTagInjection caps request body size for tag injection.
	// Mirrors http.Server.MaxHeaderBytes (1 MiB) per D-04 spirit.
	maxBodyForTagInjection = 1 << 20 // 1 MiB
)

var (
	errBodyTooLarge     = errors.New("request body exceeds tag-injection cap")
	errNotJSON          = errors.New("request Content-Type is not application/json")
	errEmptyEnvironment = errors.New("environmentName is empty")
)

// InjectEnvironmentTag mutates req.Body in place to add
// "environment:<environmentName>" to metadata.tags[] in the JSON request
// body, per FWD-06 (Hub §5.1, §6.3). On success it also sets the headerTags
// (X-Ach-Tags) request header to the same value (see headerTags doc — it is
// a backend-observable mirror of the body tag for the SC2 e2e). Fail-open:
// returns a non-nil error on schema/transport problems but the caller is
// EXPECTED to ignore it; on those paths neither the body nor the header is
// mutated.
//
// Safe to call alongside SSE: SSE is RESPONSE-side; request body for
// LiteLLM chat completions is always buffered JSON.
//
// Scope (v1alpha1): /v1 + /gemini per-route handlers only.
// MCP/A2A tag injection deferred to v1beta1.
func InjectEnvironmentTag(req *http.Request, environmentName string) error {
	if environmentName == "" {
		return errEmptyEnvironment
	}
	if req == nil || req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	ct := req.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "application/json") {
		return errNotJSON
	}
	if req.ContentLength > maxBodyForTagInjection {
		return errBodyTooLarge
	}
	limited := io.LimitReader(req.Body, maxBodyForTagInjection+1)
	raw, readErr := io.ReadAll(limited)
	_ = req.Body.Close()
	if readErr != nil {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return readErr
	}
	if len(raw) > maxBodyForTagInjection {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return errBodyTooLarge
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return err
	}

	metaRaw, metaPresent := doc["metadata"]
	var meta map[string]any
	if metaPresent {
		var ok bool
		meta, ok = metaRaw.(map[string]any)
		if !ok {
			req.Body = io.NopCloser(bytes.NewReader(raw))
			return errors.New("metadata is not a JSON object")
		}
	} else {
		meta = map[string]any{}
	}

	tagsRaw, tagsPresent := meta["tags"]
	var tags []any
	if tagsPresent {
		var ok bool
		tags, ok = tagsRaw.([]any)
		if !ok {
			req.Body = io.NopCloser(bytes.NewReader(raw))
			return errors.New("metadata.tags is not a JSON array")
		}
	} else {
		tags = []any{}
	}

	tags = append(tags, tagPrefix+environmentName)
	meta["tags"] = tags
	doc["metadata"] = meta

	out, err := json.Marshal(doc)
	if err != nil {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return err
	}

	req.Body = io.NopCloser(bytes.NewReader(out))
	req.ContentLength = int64(len(out))
	req.Header.Set("Content-Length", strconv.Itoa(len(out)))
	// Mirror the injected tag into an x- header on the success path only, so
	// header presence ⟺ body tag injected (see headerTags doc).
	req.Header.Set(headerTags, tagPrefix+environmentName)
	return nil
}
