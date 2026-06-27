// SPDX-License-Identifier: Apache-2.0

// Package achfile parses and serializes ach.yaml, the committed, secret-free
// project manifest that declares which ACH Environments (and optional adapter
// targets) a project hydrates. It is a pure file-format leaf: no network, no
// credential, no hub binding. Distinct from internal/cli/manifest, which is
// the server hydrate-response manifest.
package achfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the fixed manifest filename at the workspace root.
const FileName = "ach.yaml"

// ErrParse wraps any malformed-or-invalid ach.yaml. Callers test with
// errors.Is(err, ErrParse). An ABSENT file is reported as os.ErrNotExist
// instead (errors.Is(err, os.ErrNotExist)), so "no manifest" is
// distinguishable from "broken manifest".
var ErrParse = errors.New("ach.yaml parse error")

// Manifest is the decoded ach.yaml.
type Manifest struct {
	Version      int     `yaml:"version"`
	Environments []Entry `yaml:"environments"`
}

// Entry is one Environment the project hydrates. Targets are adapter ids
// (canonical or alias); empty means "autodetect at hydrate time".
type Entry struct {
	Name    string   `yaml:"name"`
	Targets []string `yaml:"targets,omitempty"`
}

// MarshalYAML renders an Entry with its targets in flow style
// (targets: [claude-code, codex]) for a compact, readable manifest that
// mirrors the accepted input form.
func (e Entry) MarshalYAML() (interface{}, error) {
	n := &yaml.Node{Kind: yaml.MappingNode}
	n.Content = append(n.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: e.Name},
	)
	if len(e.Targets) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, t := range e.Targets {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: t})
		}
		n.Content = append(n.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "targets"},
			seq,
		)
	}
	return n, nil
}

// Path returns <dir>/ach.yaml.
func Path(dir string) string { return filepath.Join(dir, FileName) }

// Load reads and validates <dir>/ach.yaml.
func Load(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		return nil, err // includes os.ErrNotExist for an absent file
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	if m.Version != 1 {
		return fmt.Errorf("unsupported version %d (only version 1 is supported)", m.Version)
	}
	if len(m.Environments) == 0 {
		return errors.New("environments must list at least one entry")
	}
	seen := map[string]bool{}
	for i, e := range m.Environments {
		if strings.TrimSpace(e.Name) == "" {
			return fmt.Errorf("environments[%d]: name is required", i)
		}
		if seen[e.Name] {
			return fmt.Errorf("duplicate environment name %q", e.Name)
		}
		seen[e.Name] = true
	}
	return nil
}

// WriteTo serializes the manifest to <dir>/ach.yaml deterministically:
// environments sorted by name, targets sorted within each entry. A header
// comment documents the no-secrets contract.
func (m *Manifest) WriteTo(dir string) error {
	out := &Manifest{Version: 1, Environments: make([]Entry, len(m.Environments))}
	copy(out.Environments, m.Environments)
	sort.Slice(out.Environments, func(i, j int) bool {
		return out.Environments[i].Name < out.Environments[j].Name
	})
	for i := range out.Environments {
		t := append([]string(nil), out.Environments[i].Targets...)
		sort.Strings(t)
		out.Environments[i].Targets = t
	}
	body, err := yaml.Marshal(out)
	if err != nil {
		return err
	}
	header := "# ach.yaml — committed. Declares which ACH Environments this project hydrates.\n" +
		"# Contains NO secrets. Each developer hydrates with their own credential and\n" +
		"# must have access to each Environment (server-side authz is unchanged).\n"
	return os.WriteFile(Path(dir), append([]byte(header), body...), 0o644)
}
