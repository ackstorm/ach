// SPDX-License-Identifier: Apache-2.0

package route

import (
	"encoding/json"
	"fmt"
	"sort"
)

// MCPDeepKeys is the shared MergeDeep Transform for .mcp.json-shaped sources:
// it enumerates "<section>.<id>" for every id under each named top-level
// section (e.g. mcpServers, a2aAgents) and returns the input bytes UNCHANGED
// (D-03: no re-encode, no reorder). Malformed JSON is an error so Project
// aborts the file (first-error discipline). A missing section contributes
// nothing. Keys are sorted for stable enumeration.
func MCPDeepKeys(adapter, srcRel string, in []byte, sections ...string) (out []byte, keys []string, err error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(in, &top); err != nil {
		return nil, nil, fmt.Errorf("%s: mcpDeepKeys parse %q: %w", adapter, srcRel, err)
	}
	keys = make([]string, 0)
	for _, section := range sections {
		raw, ok := top[section]
		if !ok {
			continue
		}
		var ids map[string]json.RawMessage
		if err := json.Unmarshal(raw, &ids); err != nil {
			return nil, nil, fmt.Errorf("%s: mcpDeepKeys parse %q %s: %w", adapter, srcRel, section, err)
		}
		for id := range ids {
			keys = append(keys, section+"."+id)
		}
	}
	sort.Strings(keys)
	return in, keys, nil
}
