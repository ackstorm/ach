// SPDX-License-Identifier: Apache-2.0

package route_test

import (
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter/claudecode"
	"github.com/ackstorm/ach/internal/cli/adapter/codex"
	"github.com/ackstorm/ach/internal/cli/adapter/gemini"
	"github.com/ackstorm/ach/internal/cli/adapter/opencode"
	"github.com/ackstorm/ach/internal/cli/adapter/pimono"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
)

// TestKnownComponentKinds_CoversAllAdapterRules asserts every source kind any
// adapter routes is declared Known — otherwise a real dropped kind would be
// silently swallowed.
func TestKnownComponentKinds_CoversAllAdapterRules(t *testing.T) {
	providers := []route.RuleProvider{
		&claudecode.Adapter{}, &codex.Adapter{}, &gemini.Adapter{},
		&opencode.Adapter{}, &pimono.Adapter{},
	}
	for _, p := range providers {
		for _, r := range p.ProjectionRules() {
			anchor := strings.SplitN(strings.TrimPrefix(r.FromGlob, "./"), "/", 2)[0]
			if !route.KnownComponentKinds[anchor] {
				t.Errorf("rule FromGlob %q -> kind %q is not in KnownComponentKinds", r.FromGlob, anchor)
			}
		}
	}
}
