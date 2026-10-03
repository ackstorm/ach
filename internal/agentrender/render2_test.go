// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

const fixtureAgentUID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"

func fixtureExecutionSpec() *achv1alpha1.ExecutionInfraSpec {
	return &achv1alpha1.ExecutionInfraSpec{
		Image: "registry.ackstorm.ai/ach-runtime/execution:0.1.0",
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
		},
		EphemeralStorage:              "2Gi",
		TerminationGracePeriodSeconds: ptr(int64(330)),
	}
}

func TestWorkspaceV1_InfrastructureMatchesFixture(t *testing.T) {
	got, err := RenderInfrastructureV1(fixtureAgentUID, "ach", fixtureExecutionSpec())
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, got, "infrastructure")
}

func TestWorkspaceV1_Infrastructure_FailsClosedWithoutExecutionSpec(t *testing.T) {
	_, err := RenderInfrastructureV1(fixtureAgentUID, "ach", nil)
	if err == nil {
		t.Fatal("expected failure: spec.execution is required")
	}
}

func TestWorkspaceV1_Infrastructure_GraceFallsBackWhenUnset(t *testing.T) {
	exec := fixtureExecutionSpec()
	exec.TerminationGracePeriodSeconds = nil
	got, err := RenderInfrastructureV1(fixtureAgentUID, "ach", exec)
	if err != nil {
		t.Fatal(err)
	}
	if got.Execution.TerminationGracePeriodSeconds != defaultGraceSecondsConst {
		t.Errorf("grace = %d, want default %d", got.Execution.TerminationGracePeriodSeconds, defaultGraceSecondsConst)
	}
}

// minimalRender2Fixture builds the smallest profile+agent pair Render2 accepts (every
// wire-required block fully resolved), for tests that only care about one dimension.
func minimalRender2Fixture() (achv1alpha1.AgentProfile, achv1alpha1.ACHAgent) {
	profile := achv1alpha1.AgentProfile{
		Spec: achv1alpha1.AgentProfileSpec{
			Achagent: achv1alpha1.AgentDefaults{
				Image: "img", Ach: &achv1alpha1.AchSpec{BaseURL: "https://ach"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
				Engine: &achv1alpha1.EngineSpec{Compaction: &achv1alpha1.CompactionSpec{Auto: ptr(true), Keep: &achv1alpha1.CompactionKeepSpec{Tokens: ptr(int64(8000))}, Buffer: ptr(int64(20000))}},
				Limits: &achv1alpha1.LimitsSpec{
					MaxActiveWorkspaces: ptr(int64(4)), MaxConcurrentInvocations: ptr(int64(4)), MaxInvocationSeconds: ptr(int64(900)),
					MaxQueuedTotal: ptr(int64(100)), IdempotencyWindowSeconds: ptr(int64(3600)), MaxSteps: ptr(int64(30)),
				},
				Workspace: &achv1alpha1.WorkspaceSpec{
					IdleTimeoutSeconds: ptr(int64(600)), ShutdownTimeoutSeconds: ptr(int64(300)), MaxConcurrentSessions: ptr(int64(1)),
					Persistence: &achv1alpha1.WorkspacePersistenceSpec{Enabled: ptr(false), RetentionDays: ptr(int64(90))},
					Session: &achv1alpha1.WorkspaceSessionSpec{
						IdleTimeoutSeconds: ptr(int64(300)),
						Persistence:        &achv1alpha1.WorkspacePersistenceSpec{Enabled: ptr(false), RetentionDays: ptr(int64(90))},
					},
				},
				Artifacts: &achv1alpha1.ArtifactsSpec{Enabled: ptr(false), MaxArtifactBytes: ptr(int64(1000)), RetentionDays: ptr(int64(90))},
			},
			Execution: *fixtureExecutionSpec(),
		},
	}
	agent := achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ach"},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "p"},
			AgentDefaults: achv1alpha1.AgentDefaults{
				Ach: &achv1alpha1.AchSpec{Identity: &achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}}},
			},
			Channels: []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	}
	agent.UID = fixtureAgentUID
	return profile, agent
}

// TestRender2_InheritsProfileAchEnvironmentAndCapability is the Important review finding 2
// end-to-end regression: an agent that omits ach.environment/capability must inherit the
// profile's, through the real Render2 entrypoint (not just ResolveAch in isolation).
func TestRender2_InheritsProfileAchEnvironmentAndCapability(t *testing.T) {
	profile, agent := minimalRender2Fixture()
	profile.Spec.Achagent.Ach.Environment = ptr("engineering-prod")
	profile.Spec.Achagent.Ach.Capability = &achv1alpha1.CapabilitySpec{Filter: &achv1alpha1.FilterSpec{Exclude: &achv1alpha1.ExcludeSpec{Skills: []string{"send-email"}}}}

	cfg, err := Render2(profile, agent, "")
	if err != nil {
		t.Fatalf("Render2: %v", err)
	}
	if cfg.Ach.Environment != "engineering-prod" {
		t.Errorf("ach.environment = %q, want inherited profile default", cfg.Ach.Environment)
	}
	if cfg.Ach.Capability == nil || cfg.Ach.Capability.Filter == nil || len(cfg.Ach.Capability.Filter.Exclude.Skills) != 1 {
		t.Errorf("ach.capability = %+v, want inherited profile default", cfg.Ach.Capability)
	}
}

// TestRender2_RejectsUnsupportedMemoryType is the Important review finding 4 regression:
// workspace-v1's memory union is ach-memory or absent — "codemem" (a retired
// agent-config-v1-only variant) must fail closed through Render2, not be silently delegated
// to the legacy renderMemory mapping.
func TestRender2_RejectsUnsupportedMemoryType(t *testing.T) {
	profile, agent := minimalRender2Fixture()
	agent.Spec.Memory = &achv1alpha1.MemorySpec{Type: "codemem"}
	if _, err := Render2(profile, agent, ""); err == nil {
		t.Fatal("expected rejection of memory.type=codemem under workspace-v1")
	}
}

// TestRender2_FullOutputConformsToSchemaAndJCSHash is the end-to-end gate the root asked
// for: the earlier workspacev1_test.go tests only ever feed individual blocks (ach, engine,
// limits, ...) through their own RenderXV1 function and compare that one subtree against the
// fixture — never Render2 itself, and never schema-validate the actual produced document.
// This test runs the REAL orchestrator end to end (full profile+agent -> Render2 -> WSConfig)
// and checks two things the subtree tests cannot: (1) the complete marshaled document
// satisfies the vendored ach-workspace-config-v1 JSON Schema, not just each block in
// isolation, and (2) ComputeConfigVersion recomputed over Render2's own output (with
// configVersion stripped, exactly as Render2 computed it) reproduces the SAME hash Render2
// already set — proving configVersion was computed over what actually got serialized, not
// over some earlier or different struct.
func TestRender2_FullOutputConformsToSchemaAndJCSHash(t *testing.T) {
	ach, engine, limits, workspace, artifacts, env, channel := gitlabPRReviewerAgent()
	env = append(env,
		corev1.EnvVar{Name: "GITLAB_REPO_BASEURL", Value: "https://git.ackstorm.com"},
		corev1.EnvVar{Name: "GITLAB_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "ach-agent-gitlab-ro"}, Key: "GITLAB_TOKEN"}}},
	)

	profile := achv1alpha1.AgentProfile{
		Spec: achv1alpha1.AgentProfileSpec{
			Achagent: achv1alpha1.AgentDefaults{
				Image: "ghcr.io/ackstorm/ach-agent:latest",
				Model: &achv1alpha1.ModelSpec{Name: "openai.gpt-5", Type: "openai"},
			},
			Execution: *fixtureExecutionSpec(),
		},
	}
	agent := achv1alpha1.ACHAgent{
		Spec: achv1alpha1.ACHAgentSpec{
			AgentDefaults: achv1alpha1.AgentDefaults{
				Ach: ach, Engine: engine, Limits: limits, Workspace: workspace, Artifacts: artifacts,
			},
			Env:      env,
			Channels: []achv1alpha1.ChannelSpec{channel},
		},
	}
	agent.Name, agent.Namespace, agent.UID = "gitlab-reviewer", "ach", fixtureAgentUID

	cfg, err := Render2(profile, agent, "")
	if err != nil {
		t.Fatalf("Render2: %v", err)
	}
	if cfg.ConfigVersion == "" {
		t.Fatal("Render2 did not populate configVersion")
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal WSConfig: %v", err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	schema := compileSchema(t, vendoredWorkspaceV1Schema)
	if err := schema.Validate(v); err != nil {
		t.Fatalf("Render2's full output violates ach-workspace-config-v1:\n%v", err)
	}

	recomputed, err := ComputeConfigVersion(cfg)
	if err != nil {
		t.Fatalf("recompute configVersion: %v", err)
	}
	if recomputed != cfg.ConfigVersion {
		t.Fatalf("configVersion self-consistency broken: Render2 set %q, recomputing over its own output gives %q", cfg.ConfigVersion, recomputed)
	}
}

// TestRender2_RequiresEffectiveModelName/Type: a profile/agent pair whose merged model is
// missing name or type must fail closed — ModelSpec.Name/Type have no CRD Required marker
// any more (either field may legitimately come from the sibling), so Render2 is the one
// place completeness of the EFFECTIVE value is still enforced.
func TestRender2_RequiresEffectiveModelName(t *testing.T) {
	profile, agent := minimalRender2Fixture()
	profile.Spec.Achagent.Model = &achv1alpha1.ModelSpec{Type: "openai"}
	_, err := Render2(profile, agent, "")
	if err == nil || !strings.Contains(err.Error(), "no effective model.name") {
		t.Fatalf("err = %v, want containing %q", err, "no effective model.name")
	}
}

func TestRender2_RequiresEffectiveModelType(t *testing.T) {
	profile, agent := minimalRender2Fixture()
	profile.Spec.Achagent.Model = &achv1alpha1.ModelSpec{Name: "m"}
	_, err := Render2(profile, agent, "")
	if err == nil || !strings.Contains(err.Error(), "no effective model.type") {
		t.Fatalf("err = %v, want containing %q", err, "no effective model.type")
	}
}
