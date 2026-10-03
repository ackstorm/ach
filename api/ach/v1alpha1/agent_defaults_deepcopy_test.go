// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import "testing"

// TestAgentDefaultsDeepCopy_PointerIsolation guards against a generated
// DeepCopyInto that does `*out = *in` and stops: that aliases pointer fields
// instead of allocating fresh storage, so mutating a copy mutates the
// original through the shared pointer.
func TestAgentDefaultsDeepCopy_PointerIsolation(t *testing.T) {
	t.Run("LimitsSpec.MaxSteps", func(t *testing.T) {
		steps := int64(30)
		original := &LimitsSpec{MaxSteps: &steps}
		copied := original.DeepCopy()
		*copied.MaxSteps = 12
		if *original.MaxSteps != 30 {
			t.Fatal("DeepCopy aliases maxSteps")
		}
	})

	t.Run("AchSpec.Environment", func(t *testing.T) {
		env := "prod"
		original := &AchSpec{Environment: &env}
		copied := original.DeepCopy()
		*copied.Environment = "staging"
		if *original.Environment != "prod" {
			t.Fatal("DeepCopy aliases ach.environment")
		}
	})

	t.Run("WorkspacePersistenceSpec.Enabled", func(t *testing.T) {
		enabled := true
		original := &WorkspacePersistenceSpec{Enabled: &enabled}
		copied := original.DeepCopy()
		*copied.Enabled = false
		if *original.Enabled != true {
			t.Fatal("DeepCopy aliases workspace persistence.enabled")
		}
	})

	t.Run("ArtifactsSpec.Enabled", func(t *testing.T) {
		enabled := true
		original := &ArtifactsSpec{Enabled: &enabled}
		copied := original.DeepCopy()
		*copied.Enabled = false
		if *original.Enabled != true {
			t.Fatal("DeepCopy aliases artifacts.enabled")
		}
	})

	t.Run("AgentDefaults full chain", func(t *testing.T) {
		env := "prod"
		steps := int64(30)
		persistenceEnabled := true
		artifactsEnabled := true
		original := &AgentDefaults{
			Ach:    &AchSpec{Environment: &env},
			Limits: &LimitsSpec{MaxSteps: &steps},
			Workspace: &WorkspaceSpec{
				Persistence: &WorkspacePersistenceSpec{Enabled: &persistenceEnabled},
			},
			Artifacts: &ArtifactsSpec{Enabled: &artifactsEnabled},
		}
		copied := original.DeepCopy()

		*copied.Ach.Environment = "staging"
		*copied.Limits.MaxSteps = 12
		*copied.Workspace.Persistence.Enabled = false
		*copied.Artifacts.Enabled = false

		if *original.Ach.Environment != "prod" {
			t.Error("DeepCopy aliases achagent.ach.environment")
		}
		if *original.Limits.MaxSteps != 30 {
			t.Error("DeepCopy aliases achagent.limits.maxSteps")
		}
		if *original.Workspace.Persistence.Enabled != true {
			t.Error("DeepCopy aliases achagent.workspace.persistence.enabled")
		}
		if *original.Artifacts.Enabled != true {
			t.Error("DeepCopy aliases achagent.artifacts.enabled")
		}
	})
}
