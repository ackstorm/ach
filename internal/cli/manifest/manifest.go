// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// schemaV1Alpha1 is the only schemaVersion this decoder accepts per
// Hub spec §15.2 + CLI spec §6.2. Drift from this literal flips
// ErrSchemaMismatch which callers map to exit.SchemaMismatch (5).
const schemaV1Alpha1 = "v1alpha1"

// ContentRef is the shared shape for every runtime AND context entry
// in the hydrate response. Runtime entries (models, mcpServers,
// a2aAgents) populate Endpoint; context entries (prompts, plugins,
// artifacts) populate DownloadURL. The `omitempty` tags keep encoded
// output minimal — a context entry round-trips with no Endpoint field
// and vice-versa.
type ContentRef struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	DownloadURL string `json:"downloadUrl,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
}

// RuntimeBlock carries the runtime arms of the manifest. All three
// slices are always present per Hub spec §15.2 (empty `[]` when the
// Environment has no entries); the orchestrator iterates them.
type RuntimeBlock struct {
	Models     []ContentRef `json:"models"`
	MCPServers []ContentRef `json:"mcpServers"`
	A2AAgents  []ContentRef `json:"a2aAgents"`
}

// ContextBlock carries the context arms of the manifest.
type ContextBlock struct {
	Prompts   []ContentRef `json:"prompts"`
	Plugins   []ContentRef `json:"plugins"`
	Artifacts []ContentRef `json:"artifacts"`
	Skills    []ContentRef `json:"skills"`
}

// Manifest is the decoded POST /platform/hydrate response.
type Manifest struct {
	SchemaVersion string        `json:"schemaVersion"`
	Environment   string        `json:"environment"`
	Runtime       *RuntimeBlock `json:"runtime"`
	Context       *ContextBlock `json:"context"`
	Notice        string        `json:"notice,omitempty"`
}

// ErrSchemaMismatch is returned (wrapped with %w via fmt.Errorf) when
// the decoded manifest violates the §15.2 contract: schemaVersion is
// not "v1alpha1", or the runtime / context block is missing. Callers
// (07-W1-06 hydrate orchestrator) map this to exit.SchemaMismatch
// (code 5).
var ErrSchemaMismatch = errors.New("manifest: schemaVersion != \"v1alpha1\" or runtime/context block missing (STATE-09 / spec §6.2)")

// Decode reads a hydrate response from r, parses it strictly
// (json.Decoder.DisallowUnknownFields), and asserts §15.2 invariants.
// Returns ErrSchemaMismatch (via %w) on contract violation. Decode-
// level errors (malformed JSON, unknown fields, type mismatches) are
// wrapped via fmt.Errorf but are NOT ErrSchemaMismatch — they surface
// a JSON-path-bearing message to the caller (consistent with the
// W1-02 state.Decode strict-shape posture).
func Decode(r io.Reader) (*Manifest, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: decode: %w", err)
	}
	if m.SchemaVersion != schemaV1Alpha1 {
		return nil, fmt.Errorf("manifest: schemaVersion=%q: %w", m.SchemaVersion, ErrSchemaMismatch)
	}
	if m.Runtime == nil {
		return nil, fmt.Errorf("manifest: runtime block missing: %w", ErrSchemaMismatch)
	}
	if m.Context == nil {
		return nil, fmt.Errorf("manifest: context block missing: %w", ErrSchemaMismatch)
	}
	return &m, nil
}

// Fetch issues POST /platform/hydrate against the configured
// httpclient.Client, buffers the response body, and runs Decode on it.
// `environment` is sent as `{"environment": "<env>"}` in the request
// body; when empty (ek_ + no --environment), an empty object `{}` is
// sent. Non-2xx responses surface as *httpclient.ServerError via
// DoRaw — the caller layer (07-W1-06) maps that envelope to the
// appropriate Phase 6 exit code per D-13.
//
// We use DoRaw + Decode rather than client.Do(out=&Manifest{}) for
// two reasons: (1) the httpclient.Client.Do path does NOT enable
// DisallowUnknownFields (see client.go lines 127-134 — that posture
// is deliberate for additive server fields on other endpoints, but
// the manifest contract is strict per §15.2); (2) the buffered-body
// path keeps Decode unit-testable directly against examples/hydrate.json
// without an httptest.Server in the loop.
func Fetch(ctx context.Context, client *httpclient.Client, environment string) (*Manifest, error) {
	var body any
	if environment != "" {
		body = map[string]string{"environment": environment}
	} else {
		body = struct{}{}
	}
	resp, err := client.DoRaw(ctx, http.MethodPost, "/platform/hydrate", body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		return nil, fmt.Errorf("manifest: read response body: %w", err)
	}
	return Decode(&buf)
}
