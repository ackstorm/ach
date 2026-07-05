// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestACHAgentCRD_FieldShapesStable is a migration canary. It flattens the
// ACHAgent CRD schema to `path -> type(+enum)` and diffs against a committed
// golden. A field changing SHAPE (string↔object↔array) or SHRINKING an enum
// orphans data already stored under the old schema: the operator's typed
// informer can no longer decode those CRs, its cache never syncs, and the whole
// manager aborts on startup (the v0.6.3 `channels[].session` string→object
// regression). This test does not stop such a change — it forces you to SEE it:
// when it fails, either the change is unintended, or you must migrate live CRs
// (rewrite stored objects) / ship a decode shim, THEN `UPDATE=1 go test` to
// re-bless this golden. Bumping the golden is the "did you migrate?" checkpoint.
func TestACHAgentCRD_FieldShapesStable(t *testing.T) {
	const (
		crdPath    = "../../../config/crd/bases/ach.ackstorm.ai_achagents.yaml"
		goldenPath = "testdata/achagent_field_shapes.golden"
	)

	raw, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("read CRD: %v", err)
	}
	var crd map[string]any
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("unmarshal CRD: %v", err)
	}

	shapes := map[string]string{}
	for _, root := range openAPISchemas(crd) {
		walkShapes("", root, shapes)
	}
	if len(shapes) == 0 {
		t.Fatal("no schema fields found — CRD layout changed, fix the navigation")
	}

	paths := make([]string, 0, len(shapes))
	for p := range shapes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	for _, p := range paths {
		fmt.Fprintf(&b, "%s\t%s\n", p, shapes[p])
	}
	got := b.String()

	if os.Getenv("UPDATE") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote golden %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (generate with `UPDATE=1 go test`): %v", err)
	}
	if got != string(want) {
		t.Errorf("ACHAgent field shapes changed — a shape/enum change orphans stored CRs.\n"+
			"Migrate live objects (or ship a decode shim), then re-bless with `UPDATE=1 go test`.\n"+
			"diff:\n%s", shapeDiff(string(want), got))
	}
}

// openAPISchemas returns every version's openAPIV3Schema node in the CRD.
func openAPISchemas(crd map[string]any) []map[string]any {
	spec, _ := crd["spec"].(map[string]any)
	versions, _ := spec["versions"].([]any)
	var out []map[string]any
	for _, v := range versions {
		vm, _ := v.(map[string]any)
		schema, _ := vm["schema"].(map[string]any)
		if root, ok := schema["openAPIV3Schema"].(map[string]any); ok {
			out = append(out, root)
		}
	}
	return out
}

// walkShapes records `path -> type(+enum)` for the node and recurses into
// object properties and array items.
func walkShapes(path string, node map[string]any, out map[string]string) {
	if path != "" {
		shape, _ := node["type"].(string)
		if enum, ok := node["enum"].([]any); ok {
			vals := make([]string, 0, len(enum))
			for _, e := range enum {
				vals = append(vals, fmt.Sprint(e))
			}
			sort.Strings(vals)
			shape += " enum[" + strings.Join(vals, ",") + "]"
		}
		out[path] = shape
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for name, p := range props {
			if pm, ok := p.(map[string]any); ok {
				walkShapes(join(path, name), pm, out)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		walkShapes(path+"[]", items, out)
	}
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// shapeDiff lists only the lines that differ, so the failure message points at
// the exact fields whose shape moved.
func shapeDiff(want, got string) string {
	wm, gm := lineMap(want), lineMap(got)
	keys := map[string]struct{}{}
	for k := range wm {
		keys[k] = struct{}{}
	}
	for k := range gm {
		keys[k] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	var b strings.Builder
	for _, k := range ordered {
		if wm[k] != gm[k] {
			fmt.Fprintf(&b, "  %s: golden=%q current=%q\n", k, wm[k], gm[k])
		}
	}
	return b.String()
}

func lineMap(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			continue
		}
		if path, shape, ok := strings.Cut(line, "\t"); ok {
			m[path] = shape
		}
	}
	return m
}
