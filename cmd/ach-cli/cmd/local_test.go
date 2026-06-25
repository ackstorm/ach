// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/featuregate"
)

func hasChild(parent *cobra.Command, name string) bool {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

// TestLocalCmd_ParentsRepoPluginSkill — newLocalCmd() always owns repo/skill,
// and owns plugin IFF featuregate.PluginsEnabled. Its help carries the
// "ungoverned" framing (G9).
func TestLocalCmd_ParentsRepoPluginSkill(t *testing.T) {
	local := newLocalCmd()
	// repo + skill are always present (skill is a supported content kind).
	for _, child := range []string{"repo", "skill"} {
		if !hasChild(local, child) {
			t.Errorf("local is missing child %q", child)
		}
	}
	// plugin is registered IFF plugins are enabled via the feature gate.
	if got := hasChild(local, "plugin"); got != featuregate.PluginsEnabled {
		t.Errorf("local has plugin child = %v; want %v (featuregate.PluginsEnabled)", got, featuregate.PluginsEnabled)
	}
	if !strings.Contains(local.Long, "ungoverned") {
		t.Errorf("local Long should explain the ungoverned tradeoff; got:\n%s", local.Long)
	}
}

// TestLocalCmd_ReparentedOffRoot — repo/plugin/skill are no longer top-level;
// `local` is the only entry point for them (G9 hard cut).
func TestLocalCmd_ReparentedOffRoot(t *testing.T) {
	if !hasChild(rootCmd, "local") {
		t.Fatal("rootCmd is missing the 'local' parent")
	}
	for _, moved := range []string{"repo", "plugin", "skill"} {
		if hasChild(rootCmd, moved) {
			t.Errorf("%q is still a top-level command; it must live under 'local' (G9)", moved)
		}
	}
}
