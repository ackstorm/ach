// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	_ "embed"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// workspaceV1SchemaJSON embeds the SAME vendored schema file schema_test.go's
// TestWorkspaceV1Schema_NoDrift keeps byte-identical to the coordinating runtime-schema-v1
// worker's generated copy (testdata/ach-workspace-config-v1.schema.json) — a production
// dependency on the vendored file, not a second copy, so a re-vendor can never leave the
// runtime gate validating against a stale schema.
//
//go:embed testdata/ach-workspace-config-v1.schema.json
var workspaceV1SchemaJSON []byte

var (
	workspaceV1SchemaOnce    sync.Once
	workspaceV1SchemaCompile *jsonschema.Schema
	workspaceV1SchemaErr     error
)

func compiledWorkspaceV1Schema() (*jsonschema.Schema, error) {
	workspaceV1SchemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(workspaceV1SchemaJSON))
		if err != nil {
			workspaceV1SchemaErr = fmt.Errorf("parse embedded workspace-v1 schema: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		const resourceName = "ach-workspace-config-v1.schema.json"
		if err := c.AddResource(resourceName, doc); err != nil {
			workspaceV1SchemaErr = fmt.Errorf("add embedded workspace-v1 schema resource: %w", err)
			return
		}
		s, err := c.Compile(resourceName)
		if err != nil {
			workspaceV1SchemaErr = fmt.Errorf("compile embedded workspace-v1 schema: %w", err)
			return
		}
		workspaceV1SchemaCompile = s
	})
	return workspaceV1SchemaCompile, workspaceV1SchemaErr
}

// validateWorkspaceV1Output is the Render2 output-validation gate (Important review
// finding: "Render2 does not validate its full output before applying it"). It marshals cfg
// exactly as the controller will (encoding/json) and validates the result against the same
// vendored schema the coordinating runtime-schema-v1 worker's own fixtures are checked
// against — defense in depth behind every individual Render*V1 field check above, catching
// any wire-shape defect (null vs array, an unsupported union variant, a missing required
// key) that an individual mapper missed.
func validateWorkspaceV1Output(cfg WSConfig) error {
	schema, err := compiledWorkspaceV1Schema()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal rendered config for schema validation: %w", err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("unmarshal rendered config for schema validation: %w", err)
	}
	if err := schema.Validate(v); err != nil {
		return fmt.Errorf("rendered config violates ach-workspace-config-v1: %w", err)
	}
	return nil
}
