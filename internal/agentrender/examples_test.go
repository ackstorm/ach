// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"encoding/json"
	"os"
	"testing"

	"sigs.k8s.io/yaml"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// Shipped control/execution images are unpublished (D3): the YAML carries a conspicuous
// marker instead of a real tag. This test substitutes an explicit, version-tagged
// test-only image IN MEMORY ONLY — it never edits the committed file — so Render2 can run
// to completion without ever claiming the marker is a real, bootable image.
const (
	publishedControlImageMarker   = "REPLACE_WITH_PUBLISHED_CONTROL_IMAGE"
	publishedExecutionImageMarker = "REPLACE_WITH_PUBLISHED_EXECUTION_IMAGE"
	testOnlyControlImage          = "test-only/ach-control:workspace-v1-authoring-test"
	testOnlyExecutionImage        = "test-only/ach-execution:workspace-v1-authoring-test"
)

func substituteImageMarker(image string) string {
	switch image {
	case publishedControlImageMarker:
		return testOnlyControlImage
	case publishedExecutionImageMarker:
		return testOnlyExecutionImage
	default:
		return image
	}
}

// loadProfile/loadAgent decode a committed example/fixture YAML file through the SAME
// typed, json-tagged CRD structs the API server admits (sigs.k8s.io/yaml: YAML -> JSON ->
// struct) — never a hand-rolled map. An authoring mistake that moves a field to the wrong
// parent (e.g. identity left off `ach`) is silently dropped by a map-based decode; decoding
// into the real type instead surfaces it as a nil field and a Render2 failure below, the
// same way the admission webhook's CRD schema or a failed merge would in the cluster.
func loadProfile(t *testing.T, path string) achv1alpha1.AgentProfile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var p achv1alpha1.AgentProfile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return p
}

func loadAgent(t *testing.T, path string) achv1alpha1.ACHAgent {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var a achv1alpha1.ACHAgent
	if err := yaml.Unmarshal(raw, &a); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return a
}

// renderExample assigns the canonical fixture UID (tests only — the API server assigns the
// real one; shipped YAML carries none), substitutes the committed image marker, resolves
// profile+agent through the real Render2 entrypoint, and schema-validates the full output —
// the end-to-end authoring check this file exists for (effective merge + live renderer +
// output schema/configVersion, not prose). configVersion is checked against an independent
// recomputation of the canonical digest (configversion.go's ComputeConfigVersion), not just
// non-emptiness/format: a correctly formatted but wrong hash fails here.
func renderExample(t *testing.T, p achv1alpha1.AgentProfile, a achv1alpha1.ACHAgent, defaultBaseURL string) WSConfig {
	t.Helper()
	p.Spec.Achagent.Image = substituteImageMarker(p.Spec.Achagent.Image)
	p.Spec.Execution.Image = substituteImageMarker(p.Spec.Execution.Image)
	a.UID = fixtureAgentUID

	cfg, err := Render2(p, a, defaultBaseURL)
	if err != nil {
		t.Fatalf("Render2(%s/%s): %v", a.Namespace, a.Name, err)
	}
	if cfg.ConfigVersion == "" {
		t.Fatalf("Render2(%s/%s) did not populate configVersion", a.Namespace, a.Name)
	}
	if recomputed, err := ComputeConfigVersion(cfg); err != nil {
		t.Fatalf("recompute configVersion for %s/%s: %v", a.Namespace, a.Name, err)
	} else if recomputed != cfg.ConfigVersion {
		t.Fatalf("%s/%s configVersion %q does not match the recomputed canonical digest %q of the complete rendered object", a.Namespace, a.Name, cfg.ConfigVersion, recomputed)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal %s/%s WSConfig: %v", a.Namespace, a.Name, err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	schema := compileSchema(t, vendoredWorkspaceV1Schema)
	if err := schema.Validate(v); err != nil {
		t.Fatalf("%s/%s violates ach-workspace-config-v1:\n%v", a.Namespace, a.Name, err)
	}
	return cfg
}

// expectedExecutionInfra pins the two infrastructure.execution fields that legitimately
// differ between the curated example (2Gi) and the stage-06 fixtures (1Gi) — every other
// policy value shared by assertEffectivePolicy below is identical across all four pairs
// (the shared fixture values named in ach-task6-brief.md Step 2).
type expectedExecutionInfra struct {
	ephemeralStorage string
	grace            int64
}

// assertEffectivePolicy asserts the complete resolved compaction/limits/workspace/artifacts
// policy blocks and the execution storage/grace quantities against their exact required
// values (ach-task6-review.md I1): a schema-valid drift such as maxActiveWorkspaces 4->5,
// retentionDays 90->30, or compaction buffer 20000->10000 fails one of these checks, not
// just schema validation (which a drifted-but-still-valid value would still pass).
func assertEffectivePolicy(t *testing.T, cfg WSConfig, want expectedExecutionInfra) {
	t.Helper()

	if !cfg.Engine.Compaction.Auto {
		t.Errorf("engine.compaction.auto = %v, want true", cfg.Engine.Compaction.Auto)
	}
	if cfg.Engine.Compaction.Keep.Tokens != 8000 {
		t.Errorf("engine.compaction.keep.tokens = %d, want 8000", cfg.Engine.Compaction.Keep.Tokens)
	}
	if cfg.Engine.Compaction.Buffer != 20000 {
		t.Errorf("engine.compaction.buffer = %d, want 20000", cfg.Engine.Compaction.Buffer)
	}

	l := cfg.Limits
	if l.MaxActiveWorkspaces != 4 || l.MaxConcurrentInvocations != 4 ||
		l.MaxConcurrentScripts == nil || *l.MaxConcurrentScripts != 2 ||
		l.MaxInvocationSeconds != 900 || l.MaxQueuedTotal != 100 ||
		l.IdempotencyWindowSeconds != 3600 || l.MaxSteps != 30 {
		t.Errorf("limits = %+v, want {MaxActiveWorkspaces:4 MaxConcurrentInvocations:4 MaxConcurrentScripts:2 MaxInvocationSeconds:900 MaxQueuedTotal:100 IdempotencyWindowSeconds:3600 MaxSteps:30}", l)
	}

	wantPersist := WSPersistenceBlock{Enabled: false, RetentionDays: 90}
	wantWorkspace := WSWorkspaceBlock{
		IdleTimeoutSeconds: 600, ShutdownTimeoutSeconds: 300, MaxConcurrentSessions: 1,
		Persistence: wantPersist,
		Session:     WSSessionPolicyBlock{IdleTimeoutSeconds: 300, Persistence: wantPersist},
	}
	if cfg.Workspace != wantWorkspace {
		t.Errorf("workspace = %+v, want %+v", cfg.Workspace, wantWorkspace)
	}

	wantArtifacts := WSArtifactsBlock{Enabled: false, MaxArtifactBytes: 104857600, RetentionDays: 90}
	if cfg.Artifacts != wantArtifacts {
		t.Errorf("artifacts = %+v, want %+v", cfg.Artifacts, wantArtifacts)
	}

	if cfg.Infrastructure.Execution.EphemeralStorage != want.ephemeralStorage {
		t.Errorf("infrastructure.execution.ephemeralStorage = %q, want %q", cfg.Infrastructure.Execution.EphemeralStorage, want.ephemeralStorage)
	}
	if cfg.Infrastructure.Execution.TerminationGracePeriodSeconds != want.grace {
		t.Errorf("infrastructure.execution.terminationGracePeriodSeconds = %d, want %d", cfg.Infrastructure.Execution.TerminationGracePeriodSeconds, want.grace)
	}
}

// TestRuntimeExamples_RenderWorkspaceV1 exercises the curated examples/agent-runtime and
// the synced test/e2e/cluster/06-agent fixtures through actual YAML authoring: typed
// decode, the real profile+agent effective merge, and the live Render2 renderer — not a
// hand-built Go struct standing in for what the files say. A profile/agent pair that no
// longer matches the current authoring contract fails here before it ever reaches a
// cluster.
func TestRuntimeExamples_RenderWorkspaceV1(t *testing.T) {
	const (
		curatedDir = "../../examples/agent-runtime"
		stage06Dir = "../../test/e2e/cluster/06-agent"
		// The curated profile intentionally omits ach.baseUrl (comment: inherit the
		// operator's own ACH_BASE_URL) — this stands in for that operator default.
		curatedDefaultBaseURL = "https://ach.example.internal"
	)
	curatedExecInfra := expectedExecutionInfra{ephemeralStorage: "2Gi", grace: 330}
	stage06ExecInfra := expectedExecutionInfra{ephemeralStorage: "1Gi", grace: 330}

	t.Run("curated/gitlab-reviewer", func(t *testing.T) {
		profile := loadProfile(t, curatedDir+"/profile.yaml")
		agent := loadAgent(t, curatedDir+"/agent.yaml")
		cfg := renderExample(t, profile, agent, curatedDefaultBaseURL)
		if cfg.Ach.Identity.Env != AchIdentityAliasEnv {
			t.Errorf("ach.identity.env = %q, want %q", cfg.Ach.Identity.Env, AchIdentityAliasEnv)
		}
		// engine.forwardEnv selects OPENCODE_ENABLE_EXA (profile default) — the literal
		// must actually be declared in the merged spec.env or Render2 would have failed
		// closed above; this pins the value survives into engine.env too.
		if got := cfg.Engine.Env["OPENCODE_ENABLE_EXA"]; got != "true" {
			t.Errorf("engine.env[OPENCODE_ENABLE_EXA] = %q, want \"true\"", got)
		}
		assertEffectivePolicy(t, cfg, curatedExecInfra)
	})

	t.Run("curated/memory-reviewer", func(t *testing.T) {
		profile := loadProfile(t, curatedDir+"/profile.yaml")
		agent := loadAgent(t, curatedDir+"/agent-memory.yaml")
		cfg := renderExample(t, profile, agent, curatedDefaultBaseURL)
		if cfg.Memory == nil || cfg.Memory.AchMemory == nil || cfg.Memory.AchMemory.McpServerID != "ach-memory" {
			t.Errorf("memory.achMemory = %+v, want mcpServerId \"ach-memory\"", cfg.Memory)
		}
		assertEffectivePolicy(t, cfg, curatedExecInfra)
	})

	t.Run("stage06/e2e-agent", func(t *testing.T) {
		profile := loadProfile(t, stage06Dir+"/profile.yaml")
		agent := loadAgent(t, stage06Dir+"/agent.yaml")
		cfg := renderExample(t, profile, agent, "")
		if cfg.Ach.Identity.Env != AchIdentityAliasEnv {
			t.Errorf("ach.identity.env = %q, want %q", cfg.Ach.Identity.Env, AchIdentityAliasEnv)
		}
		if cfg.Ach.Environment != "demo" {
			t.Errorf("ach.environment = %q, want %q", cfg.Ach.Environment, "demo")
		}
		assertEffectivePolicy(t, cfg, stage06ExecInfra)
	})

	t.Run("stage06/e2e-agent-pvc", func(t *testing.T) {
		profile := loadProfile(t, stage06Dir+"/profile-pvc.yaml")
		agent := loadAgent(t, stage06Dir+"/agent-pvc.yaml")
		cfg := renderExample(t, profile, agent, "")
		assertEffectivePolicy(t, cfg, stage06ExecInfra)
	})

	// A leaf override (agent sets ONLY workspace.session.idleTimeoutSeconds) must leave
	// every sibling field — workspace.{idleTimeoutSeconds,shutdownTimeoutSeconds,
	// maxConcurrentSessions,persistence} and workspace.session.persistence — inherited
	// verbatim from the profile (ResolveWorkspace/ResolveSession per-field merge, not a
	// wholesale sub-block swap). ach-task6-review.md I1.
	t.Run("leaf override preserves inherited workspace siblings", func(t *testing.T) {
		profile := loadProfile(t, curatedDir+"/profile.yaml")
		agent := loadAgent(t, curatedDir+"/agent.yaml")
		agent.Spec.Workspace = &achv1alpha1.WorkspaceSpec{
			Session: &achv1alpha1.WorkspaceSessionSpec{IdleTimeoutSeconds: ptr(int64(900))},
		}
		cfg := renderExample(t, profile, agent, curatedDefaultBaseURL)

		if cfg.Workspace.Session.IdleTimeoutSeconds != 900 {
			t.Errorf("workspace.session.idleTimeoutSeconds = %d, want 900 (leaf override)", cfg.Workspace.Session.IdleTimeoutSeconds)
		}
		if cfg.Workspace.IdleTimeoutSeconds != 600 {
			t.Errorf("workspace.idleTimeoutSeconds = %d, want 600 (inherited sibling)", cfg.Workspace.IdleTimeoutSeconds)
		}
		if cfg.Workspace.ShutdownTimeoutSeconds != 300 {
			t.Errorf("workspace.shutdownTimeoutSeconds = %d, want 300 (inherited sibling)", cfg.Workspace.ShutdownTimeoutSeconds)
		}
		if cfg.Workspace.MaxConcurrentSessions != 1 {
			t.Errorf("workspace.maxConcurrentSessions = %d, want 1 (inherited sibling)", cfg.Workspace.MaxConcurrentSessions)
		}
		wantPersist := WSPersistenceBlock{Enabled: false, RetentionDays: 90}
		if cfg.Workspace.Persistence != wantPersist {
			t.Errorf("workspace.persistence = %+v, want %+v (inherited sibling)", cfg.Workspace.Persistence, wantPersist)
		}
		if cfg.Workspace.Session.Persistence != wantPersist {
			t.Errorf("workspace.session.persistence = %+v, want %+v (inherited sibling)", cfg.Workspace.Session.Persistence, wantPersist)
		}
	})

	// channels[].routing's two fields are independent (RoutingSpec doc comment): setting
	// one must not touch the other, and omitting the block entirely must render routing
	// as null, not a zero-value object. ach-task6-review.md I1 ("no workspace-only or
	// session-only override cases").
	t.Run("channel routing: omitted, workspace-only and session-only stay independent", func(t *testing.T) {
		loadReviewer := func(t *testing.T) (achv1alpha1.AgentProfile, achv1alpha1.ACHAgent) {
			t.Helper()
			return loadProfile(t, curatedDir+"/profile.yaml"), loadAgent(t, curatedDir+"/agent.yaml")
		}

		t.Run("omitted", func(t *testing.T) {
			profile, agent := loadReviewer(t)
			cfg := renderExample(t, profile, agent, curatedDefaultBaseURL)
			if cfg.Channels[0].Routing != nil {
				t.Errorf("channels[0].routing = %+v, want nil (omitted)", cfg.Channels[0].Routing)
			}
		})

		t.Run("workspace-only", func(t *testing.T) {
			profile, agent := loadReviewer(t)
			agent.Spec.Channels[0].Routing = &achv1alpha1.RoutingSpec{WorkspaceKey: ptr("override-workspace-key")}
			cfg := renderExample(t, profile, agent, curatedDefaultBaseURL)
			got := cfg.Channels[0].Routing
			if got == nil || got.WorkspaceKey == nil || *got.WorkspaceKey != "override-workspace-key" {
				t.Errorf("channels[0].routing.workspaceKey = %+v, want \"override-workspace-key\"", got)
			}
			if got != nil && got.SessionKey != nil {
				t.Errorf("channels[0].routing.sessionKey = %q, want nil (independent of workspaceKey)", *got.SessionKey)
			}
		})

		t.Run("session-only", func(t *testing.T) {
			profile, agent := loadReviewer(t)
			agent.Spec.Channels[0].Routing = &achv1alpha1.RoutingSpec{SessionKey: ptr("override-session-key")}
			cfg := renderExample(t, profile, agent, curatedDefaultBaseURL)
			got := cfg.Channels[0].Routing
			if got == nil || got.SessionKey == nil || *got.SessionKey != "override-session-key" {
				t.Errorf("channels[0].routing.sessionKey = %+v, want \"override-session-key\"", got)
			}
			if got != nil && got.WorkspaceKey != nil {
				t.Errorf("channels[0].routing.workspaceKey = %q, want nil (independent of sessionKey)", *got.WorkspaceKey)
			}
		})
	})
}
