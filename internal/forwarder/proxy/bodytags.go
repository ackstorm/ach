// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
)

// maxTagScanBody caps the JSON request body stripBodyTags will buffer.
const maxTagScanBody = 32 << 20

var errBodyTooLarge = errors.New("request body exceeds the tag-scan limit")

// stripBodyTags removes client-supplied budget tags from a JSON request body:
// top-level "tags", and "tags" inside "metadata" / "litellm_metadata".
// LiteLLM merges those with ACH's x-litellm-tags header and charges every
// tag's budget, so a caller could otherwise bill "user:<someone else>"
// (review #5). Unchanged, non-JSON and unparseable bodies are forwarded
// byte-identical (LiteLLM answers an invalid body itself). Never logs the body.
func stripBodyTags(r *http.Request) error {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxTagScanBody+1))
	if err != nil {
		return err
	}
	if len(raw) > maxTagScanBody {
		return errBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))

	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil
	}
	changed := false
	if _, ok := top["tags"]; ok {
		delete(top, "tags")
		changed = true
	}
	for _, k := range []string{"metadata", "litellm_metadata"} {
		v, ok := top[k]
		if !ok || len(bytes.TrimSpace(v)) == 0 || bytes.TrimSpace(v)[0] != '{' {
			continue
		}
		var sub map[string]json.RawMessage
		if json.Unmarshal(v, &sub) != nil {
			continue
		}
		if _, ok := sub["tags"]; !ok {
			continue
		}
		delete(sub, "tags")
		nb, err := json.Marshal(sub)
		if err != nil {
			return err
		}
		top[k] = nb
		changed = true
	}
	if !changed {
		return nil
	}
	out, err := json.Marshal(top)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(out))
	r.ContentLength = int64(len(out))
	r.Header.Set("Content-Length", strconv.Itoa(len(out)))
	r.Header.Del("Transfer-Encoding")
	r.TransferEncoding = nil
	return nil
}
