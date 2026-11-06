// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ackstorm/ach/internal/litellm"
)

// aliasRow is the /v2/model/info row of a model-group alias. model_info holds
// only the ModelCaps allow-list, so litellm_params never reaches a caller.
type aliasRow struct {
	ModelName string            `json:"model_name"`
	ModelInfo litellm.ModelCaps `json:"model_info"`
}

type aliasRowKey struct{}

// prepareModelInfo marks GET /v2/model/info?model=<alias> for addAliasInfo
// and drops Accept-Encoding so the Transport decompresses the answer. The
// request itself goes upstream unchanged: rewriting it to the target would
// 403, because a caller is granted the alias, not what it points at. Any
// other request, and an alias whose target LiteLLM does not describe, is
// returned untouched.
func prepareModelInfo(r *http.Request, caps *litellm.CapsCache) *http.Request {
	name := r.URL.Query().Get("model")
	if caps == nil || r.Method != http.MethodGet || name == "" {
		return r
	}
	deps, aliases := caps.Get(r.Context())
	if _, ok := aliases[name]; !ok {
		return r
	}
	row := aliasRow{ModelName: name, ModelInfo: litellm.DeploymentCaps(deps, aliases, name)}
	if row.ModelInfo == (litellm.ModelCaps{}) {
		return r
	}
	r.Header.Del("Accept-Encoding")
	return r.WithContext(context.WithValue(r.Context(), aliasRowKey{}, row))
}

// addAliasInfo answers a marked request with the alias's row. LiteLLM's own
// status is the entitlement check: ?model=<name> is 403 unless the caller is
// granted that name, so only a 200 is rewritten. That 200 carries no row for
// the alias, only rows unrelated to the filter (a2a agents), which go. A body
// that already has the row, or is not a model list, passes through as it came.
func addAliasInfo(resp *http.Response) error {
	row, ok := resp.Request.Context().Value(aliasRowKey{}).(aliasRow)
	if !ok || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" {
		return nil
	}
	raw, err := bufferBody(resp)
	if err != nil {
		return err
	}
	var list map[string]json.RawMessage
	var rows []struct {
		ModelName string `json:"model_name"`
	}
	if json.Unmarshal(raw, &list) != nil || json.Unmarshal(list["data"], &rows) != nil {
		return nil
	}
	for _, r := range rows {
		if r.ModelName == row.ModelName {
			return nil
		}
	}
	if list["data"], err = json.Marshal([]aliasRow{row}); err != nil {
		return err
	}
	list["total_count"] = json.RawMessage("1")
	return replaceBody(resp, list)
}
