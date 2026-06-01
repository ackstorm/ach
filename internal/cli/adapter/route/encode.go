// SPDX-License-Identifier: Apache-2.0

package route

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/BurntSushi/toml"
)

// CanonicalJSON encodes m as deterministic, byte-stable JSON (FMT-05 /
// D-09): sorted keys (json.Marshal sorts map[string]any keys, the same
// approach as subtreeHash), HTML escaping disabled (SetEscapeHTML(false)
// so <, &, > appear literally), and a fixed 2-space indent. Repeated
// calls on the same input produce byte-identical output, which is the
// VER-03 idempotence precondition.
//
// The encoder block mirrors the ONLY current SetEscapeHTML(false) JSON
// call site (internal/cli/hydrate/wiring.go mergeForwardJSON). D-10
// scope guard: this helper feeds the new route projection path only; the
// existing per-adapter RenderRuntime encoders are left untouched.
func CanonicalJSON(m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("route: canonical json: %w", err)
	}
	return buf.Bytes(), nil
}

// CanonicalTOML encodes v as deterministic TOML via the already-vendored
// github.com/BurntSushi/toml v1.6.0 (FMT-05 / D-09; NO new dependency per
// OPENPACKAGE-MAPPING §"New Go dependency: NONE"). Indent is fixed at two
// spaces, matching the codex RenderRuntime encoder block. Repeated calls
// on the same input produce byte-identical output.
func CanonicalTOML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = "  "
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("route: canonical toml: %w", err)
	}
	return buf.Bytes(), nil
}
