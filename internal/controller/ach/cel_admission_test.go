// SPDX-License-Identifier: Apache-2.0

// CEL admission validation — Plan 01-11 Task 3. Asserts SC #1 from the
// ROADMAP Phase 1 Success Criteria: valid CRs are accepted, invalid CRs
// are rejected with a readable error.
//
// The test applies valid samples (config/samples/ach_v1alpha1_*.yaml
// from Plan 01-02 Task 3) plus seven invalid fixtures (test/fixtures/
// invalid/*.yaml from Plan 01-11 Task 1). Each invalid case asserts a
// substring of the CEL message text so a future edit to the CEL rule
// won't silently break test coverage.

package ach

import (
	"context"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// TestCELAdmission walks the valid + invalid fixture matrix and
// asserts the API server's admission behavior matches the CRD-NN
// contracts (CRD-02..CRD-08). Each subtest is independent: valid CRs are
// deleted after their assertion so the next invalid case starts with a
// clean namespace.
func TestCELAdmission(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name           string
		fixturePath    string
		shouldFail     bool
		errMustContain string
		// apiVersion+kind+resourceName let the cleanup path delete the
		// successfully-applied valid CR without parsing the YAML twice.
		apiVersion   string
		kind         string
		resourceName string
	}{
		// ─── Valid samples (Plan 01-02 Task 3 + LiteLLMConnection/default) ───
		{
			name:         "valid_environment",
			fixturePath:  "../../../config/samples/ach_v1alpha1_environment.yaml",
			shouldFail:   false,
			apiVersion:   "ach.ackstorm.ai/v1alpha1",
			kind:         "Environment",
			resourceName: "example",
		},
		{
			name:         "valid_plugin",
			fixturePath:  "../../../config/samples/ach_v1alpha1_plugin.yaml",
			shouldFail:   false,
			apiVersion:   "ach.ackstorm.ai/v1alpha1",
			kind:         "Plugin",
			resourceName: "example-plugin",
		},
		{
			name:         "valid_pluginmarketplace",
			fixturePath:  "../../../config/samples/ach_v1alpha1_pluginmarketplace.yaml",
			shouldFail:   false,
			apiVersion:   "ach.ackstorm.ai/v1alpha1",
			kind:         "PluginMarketplace",
			resourceName: "example-marketplace",
		},
		{
			name:         "valid_artifact",
			fixturePath:  "../../../config/samples/ach_v1alpha1_artifact.yaml",
			shouldFail:   false,
			apiVersion:   "ach.ackstorm.ai/v1alpha1",
			kind:         "Artifact",
			resourceName: "example-artifact",
		},
		{
			name:         "valid_prompt",
			fixturePath:  "../../../config/samples/ach_v1alpha1_prompt.yaml",
			shouldFail:   false,
			apiVersion:   "ach.ackstorm.ai/v1alpha1",
			kind:         "Prompt",
			resourceName: "example-prompt",
		},
		{
			name:         "valid_bip",
			fixturePath:  "../../../config/samples/ach_v1alpha1_backendidentitypolicy.yaml",
			shouldFail:   false,
			apiVersion:   "ach.ackstorm.ai/v1alpha1",
			kind:         "BackendIdentityPolicy",
			resourceName: "example-bip",
		},
		{
			name:         "valid_litellmconnection",
			fixturePath:  "../../../config/samples/ach_v1alpha1_litellmconnection.yaml",
			shouldFail:   false,
			apiVersion:   "ach.ackstorm.ai/v1alpha1",
			kind:         "LiteLLMConnection",
			resourceName: "default",
		},
		// S2 CRD validation — runtime element-level Pattern must NOT be too
		// strict: provider-prefixed ("openai/gpt-4") and tagged
		// ("gpt-4o:latest") LiteLLM model names are legitimate and must pass.
		{
			name:         "valid_env_runtime_provider_prefixed",
			fixturePath:  "../../../test/fixtures/valid/environment_runtime_provider_prefixed.yaml",
			shouldFail:   false,
			apiVersion:   "ach.ackstorm.ai/v1alpha1",
			kind:         "Environment",
			resourceName: "valid-runtime-provider-prefixed",
		},
		// Environment.spec.runtime is OPTIONAL (commit 30c76e0): a manifest
		// omitting spec.runtime is accepted — the API server defaults it to
		// {} and the list sub-fields backfill to []. Guards the relaxation
		// of the former CRD-02 "runtime required" admission rule.
		{
			name:         "valid_env_no_runtime",
			fixturePath:  "../../../test/fixtures/valid/environment_missing_runtime.yaml",
			shouldFail:   false,
			apiVersion:   "ach.ackstorm.ai/v1alpha1",
			kind:         "Environment",
			resourceName: "valid-omitted-runtime",
		},

		// ─── Invalid fixtures (Plan 01-11 Task 1 + S2 review additions) ───
		// errMustContain is matched case-insensitively against the API
		// server's error string. The substrings are fragments of the CEL
		// `message:` text declared in api/ach/v1alpha1/*_types.go (Plan
		// 01-02 Task 2) — coordinate edits with that file.
		{
			name:           "invalid_env_empty_teams",
			fixturePath:    "../../../test/fixtures/invalid/environment_empty_authorized_teams.yaml",
			shouldFail:     true,
			errMustContain: "authorizedteams",
		},
		{
			name:           "invalid_plugin_no_staleness",
			fixturePath:    "../../../test/fixtures/invalid/plugin_missing_maxstaleness.yaml",
			shouldFail:     true,
			errMustContain: "maxstaleness",
		},
		{
			name:           "invalid_plugin_interval_exceeds",
			fixturePath:    "../../../test/fixtures/invalid/plugin_interval_exceeds_maxstaleness.yaml",
			shouldFail:     true,
			errMustContain: "interval",
		},
		{
			name:           "invalid_plugin_type_mismatch",
			fixturePath:    "../../../test/fixtures/invalid/plugin_type_mismatch_subobject.yaml",
			shouldFail:     true,
			errMustContain: "subobject",
		},
		{
			name:           "invalid_artifact_http_dir",
			fixturePath:    "../../../test/fixtures/invalid/artifact_http_with_directory_scope.yaml",
			shouldFail:     true,
			errMustContain: "scope=object",
		},
		{
			name:           "invalid_bip_no_jwt",
			fixturePath:    "../../../test/fixtures/invalid/backendidentitypolicy_missing_forwardidentityjwt.yaml",
			shouldFail:     true,
			errMustContain: "forwardidentityjwt",
		},
		{
			name:           "invalid_litellmconnection_non_default",
			fixturePath:    "../../../test/fixtures/invalid/litellmconnection_non_default.yaml",
			shouldFail:     true,
			errMustContain: "default",
		},
		// S2 CRD validation — element-level Pattern on the content/runtime name
		// fields (api/ach/v1alpha1/environment_types.go). Three patterns apply:
		//   • Models (loose): allows "/" and ":" for provider-prefixed LiteLLM
		//     names; forbids ? # % whitespace control-chars DEL.
		//   • MCPServers, A2AAgents (strict): forbids "/" too — names are chi
		//     route params (/mcp/{name}, /a2a/{name}); slash always 403s.
		//   • Prompts, Plugins, Artifacts (strict): same, plus "\" (path-traversal).
		// Path-traversal prompts name → rejected; query-injection models name →
		// rejected; slash-containing mcpServers name → rejected (S2 review fix).
		{
			name:           "invalid_env_context_path_traversal",
			fixturePath:    "../../../test/fixtures/invalid/environment_context_path_traversal.yaml",
			shouldFail:     true,
			errMustContain: "prompts",
		},
		{
			name:           "invalid_env_runtime_query_injection",
			fixturePath:    "../../../test/fixtures/invalid/environment_runtime_query_injection.yaml",
			shouldFail:     true,
			errMustContain: "models",
		},
		// S2 review fix — mcpServers/a2aAgents now use the STRICT (no-slash)
		// pattern because names become chi route params (/mcp/{name}, /a2a/{name});
		// a slash would always 403 at the forwarder. Only models stays loose.
		{
			name:           "invalid_env_runtime_mcp_slash",
			fixturePath:    "../../../test/fixtures/invalid/environment_runtime_mcp_slash.yaml",
			shouldFail:     true,
			errMustContain: "mcpServers",
		},
		// CONTRACT_v3 mcpServers addendum — the discriminated union on
		// ACHAgent.spec.mcpServers[].type requires the matching sub-block.
		// type=repoCheckout with no repoCheckout: block → rejected.
		{
			name:           "invalid_achagent_mcpserver_missing_block",
			fixturePath:    "../../../test/fixtures/invalid/achagent_mcpserver_missing_block.yaml",
			shouldFail:     true,
			errMustContain: "mcpServers",
		},
	}

	for _, tc := range cases {
		tc := tc // capture for parallel safety even though we don't use t.Parallel
		t.Run(tc.name, func(t *testing.T) {
			err := ApplyFixture(ctx, tc.fixturePath)
			if tc.shouldFail {
				if err == nil {
					t.Fatalf("expected admission rejection for %s, got nil", tc.fixturePath)
				}
				if tc.errMustContain != "" {
					if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.errMustContain)) {
						t.Errorf("expected error containing %q (case-insensitive); got %q",
							tc.errMustContain, err.Error())
					}
				}
				return
			}
			// Valid case.
			if err != nil {
				t.Fatalf("expected admission accept for %s, got error: %v", tc.fixturePath, err)
			}
			// Cleanup so the next subtest sees an empty namespace for this
			// kind. Delete by GVK+name; finalizer-equipped CRs will drain
			// asynchronously (six reconcilers run on the test manager), so
			// we don't WaitForGone here — the cleanup is best-effort.
			t.Cleanup(func() {
				_ = DeleteByGVKName(context.Background(), tc.apiVersion, tc.kind,
					"ach-system", tc.resourceName)
			})
		})
	}
}

// TestEnvironmentGuardrailsAdmission: the guardrails axis carries the strict
// route-segment deny-pattern, a 253-byte cap, and CEL uniqueness. Uniqueness
// matters because Task 5's drift comparator compares deduplicated sets — a
// duplicate in the spec would make "did LiteLLM change this?" undecidable.
func TestEnvironmentGuardrailsAdmission(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name       string
		guardrails []string
		wantErr    bool
	}{
		{"plain", []string{"pii-filter"}, false},
		{"dotted", []string{"pii-filter.v1"}, false},
		{"two distinct", []string{"a", "b"}, false},
		{"empty list", []string{}, false},
		{"duplicate", []string{"a", "a"}, true},
		{"slash", []string{"bad/name"}, true},
		{"backslash", []string{`bad\name`}, true},
		{"question", []string{"bad?name"}, true},
		{"hash", []string{"bad#name"}, true},
		{"percent", []string{"bad%name"}, true},
		{"space", []string{"bad name"}, true},
		{"tab", []string{"bad\tname"}, true},
		{"too long", []string{strings.Repeat("a", 254)}, true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cr := &achv1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("guardrail-admission-%d", i),
					Namespace: WatchNamespace,
				},
				Spec: achv1alpha1.EnvironmentSpec{
					AuthorizedTeams: []string{"default"},
					Runtime:         achv1alpha1.RuntimeBlock{Guardrails: tc.guardrails},
					Context:         achv1alpha1.ContextBlock{},
				},
			}
			err := k8sClient.Create(ctx, cr)
			if err == nil {
				t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })
			}
			if tc.wantErr && err == nil {
				t.Fatalf("expected admission rejection for %v", tc.guardrails)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected rejection for %v: %v", tc.guardrails, err)
			}
		})
	}
}
