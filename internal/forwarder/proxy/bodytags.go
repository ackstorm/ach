// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// maxTagScanBody caps the request body stripBodyTags will buffer.
const maxTagScanBody = 32 << 20

var (
	errBodyTooLarge = errors.New("request body exceeds the tag-scan limit")
	// errInvalidBody: a non-empty body that is not valid JSON (or a form that
	// does not parse). Fail closed: LiteLLM's Python parser is more lenient
	// than Go's (NaN, Infinity), so "Go cannot parse it" proves nothing.
	errInvalidBody = errors.New("request body is not valid")
	// errClientTags: a form body carrying a tags/metadata field.
	errClientTags = errors.New("client budget tags are not allowed in form bodies")
)

// tagBearing are the body keys LiteLLM reads budget tags from (as "tags", or
// inside the "metadata" / "litellm_metadata" objects); as form fields they
// are refused outright.
var tagBearing = []string{"tags", "metadata", "litellm_metadata"}

// stripBodyTags removes client-supplied budget tags from a request body.
// LiteLLM merges body "tags", "metadata.tags" and "litellm_metadata.tags"
// with ACH's x-litellm-tags header and charges every tag's budget, so a
// caller could otherwise bill "user:<someone else>" (review #5). Fail closed:
//   - every body except multipart/form-data and urlencoded forms is scanned as
//     JSON, whatever its Content-Type (LiteLLM parses them all as JSON); a
//     metadata value sent as a JSON string is parsed and re-encoded as an
//     object; a non-empty body that is not valid JSON is errInvalidBody;
//   - forms carrying a tags/metadata/litellm_metadata field are errClientTags;
//   - anything else is forwarded byte-identical (as are empty bodies).
//
// Never logs the body.
func stripBodyTags(r *http.Request) error {
	if r.Body == nil || r.Body == http.NoBody {
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
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}

	// Classify exactly as LiteLLM does (http_parsing_utils.py): text before the
	// first ";", trimmed and lower-cased. mime.ParseMediaType is stricter
	// (duplicate params -> "") and would disagree on what is a form.
	ct := r.Header.Get("Content-Type")
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])) {
	case "multipart/form-data":
		return checkMultipart(raw, boundaryOf(ct))
	case "application/x-www-form-urlencoded":
		return checkURLEncoded(raw)
	}

	out, changed, err := stripJSONTags(raw)
	if err != nil || !changed {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(out))
	r.ContentLength = int64(len(out))
	r.Header.Set("Content-Length", strconv.Itoa(len(out)))
	r.Header.Del("Transfer-Encoding")
	r.TransferEncoding = nil
	return nil
}

func hasTagField(name string) bool {
	for _, k := range tagBearing {
		if strings.EqualFold(name, k) {
			return true
		}
	}
	return false
}

func checkURLEncoded(raw []byte) error {
	q, err := url.ParseQuery(string(raw))
	if err != nil {
		return errInvalidBody
	}
	for k := range q {
		if hasTagField(k) {
			return errClientTags
		}
	}
	return nil
}

// boundaryOf returns the multipart boundary, or "" when none can be found.
func boundaryOf(ct string) string {
	if _, params, err := mime.ParseMediaType(ct); err == nil {
		return params["boundary"]
	}
	// Strict parse failed (e.g. duplicate params): take the first boundary=.
	for _, p := range strings.Split(ct, ";")[1:] {
		if k, v, ok := strings.Cut(p, "="); ok && strings.EqualFold(strings.TrimSpace(k), "boundary") {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

// checkMultipart refuses any part that names a tag-bearing field. Starlette
// ignores the disposition type, keeps the LAST Content-Disposition header and
// takes the last name param, while Go's FormName is stricter on all three, so
// every ambiguity (several headers, unparseable header, duplicate param, zero
// parts, a broken tail) is a 400 rather than a guess.
func checkMultipart(raw []byte, boundary string) error {
	if boundary == "" {
		return errInvalidBody
	}
	mr := multipart.NewReader(bytes.NewReader(raw), boundary)
	parts := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF && parts > 0 { //nolint:errorlint // a clean end is the bare io.EOF; Go wraps a truncated tail
			return nil
		}
		if err != nil {
			return errInvalidBody
		}
		parts++
		cd := p.Header["Content-Disposition"]
		_ = p.Close()
		if len(cd) != 1 {
			return errInvalidBody
		}
		_, params, err := mime.ParseMediaType(cd[0])
		if err != nil {
			return errInvalidBody
		}
		if hasTagField(strings.TrimSpace(params["name"])) {
			return errClientTags
		}
	}
}

// stripJSONTags drops the budget-tag keys from a JSON object body, touching
// only the objects that carried one (every other byte span is kept as-is).
func stripJSONTags(raw []byte) (out []byte, changed bool, err error) {
	if !json.Valid(raw) {
		return nil, false, errInvalidBody
	}
	// No legitimate /v1 or /gemini body is an array, string or number, and
	// Starlette may read a JSON string as a form: only an object passes.
	var top map[string]json.RawMessage
	if bytes.TrimSpace(raw)[0] != '{' || json.Unmarshal(raw, &top) != nil {
		return nil, false, errInvalidBody
	}
	if _, ok := top["tags"]; ok {
		delete(top, "tags")
		changed = true
	}
	for _, k := range []string{"metadata", "litellm_metadata"} {
		v, ok := top[k]
		if !ok {
			continue
		}
		// LiteLLM json-parses a metadata value sent as a string.
		var str string
		isStr := json.Unmarshal(v, &str) == nil
		if isStr {
			v = json.RawMessage(str)
		}
		var sub map[string]json.RawMessage
		if json.Unmarshal(v, &sub) != nil {
			// A non-empty string LiteLLM could json-parse (Python accepts
			// NaN...) but Go cannot: refuse. Non-string values carry no tags.
			if isStr && strings.TrimSpace(str) != "" {
				return nil, false, errInvalidBody
			}
			continue
		}
		if _, ok := sub["tags"]; !ok {
			continue
		}
		delete(sub, "tags")
		nb, err := json.Marshal(sub)
		if err != nil {
			return nil, false, err
		}
		top[k] = nb
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	out, err = json.Marshal(top)
	return out, true, err
}
