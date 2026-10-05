// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

func TestWorkspaceOperatorRBAC(t *testing.T) {
	generated := loadWorkspaceClusterRole(t, "../../../config/rbac/role.yaml")
	helm := loadWorkspaceHelmRules(t, "../../../deploy/helm/ach/templates/operator-rbac.yaml")

	for _, surface := range []struct {
		name string
		role *rbacv1.ClusterRole
	}{{"generated", generated}, {"Helm", helm}} {
		t.Run(surface.name, func(t *testing.T) {
			assertOperatorWorkspaceGrants(t, surface.role)
		})
	}
}

func loadWorkspaceClusterRole(t *testing.T, path string) *rbacv1.ClusterRole {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var role rbacv1.ClusterRole
	if err := sigsyaml.Unmarshal(b, &role); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return &role
}

func loadWorkspaceHelmRules(t *testing.T, path string) *rbacv1.ClusterRole {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	roleStart := strings.Index(text, "kind: ClusterRole\n")
	if roleStart < 0 {
		t.Fatalf("%s has no literal ClusterRole", path)
	}
	rulesStart := strings.Index(text[roleStart:], "rules:")
	if rulesStart < 0 {
		t.Fatalf("%s ClusterRole has no rules", path)
	}
	rulesStart += roleStart
	roleEnd := strings.Index(text[rulesStart:], "\n---\n")
	if roleEnd < 0 {
		t.Fatalf("%s ClusterRole rules have no YAML document boundary", path)
	}
	var parsed struct {
		Rules []rbacv1.PolicyRule `yaml:"rules"`
	}
	if err := sigsyaml.Unmarshal([]byte(text[rulesStart:rulesStart+roleEnd]), &parsed); err != nil {
		t.Fatalf("parse literal Helm ClusterRole rules in %s: %v", path, err)
	}
	return &rbacv1.ClusterRole{Rules: parsed.Rules}
}

func assertOperatorWorkspaceGrants(t *testing.T, role *rbacv1.ClusterRole) {
	t.Helper()
	for _, verb := range []string{"get", "list", "watch", "create", "update", "patch", "delete"} {
		assertClusterRoleGrant(t, role, "ach.ackstorm.ai", "workspaces", verb)
	}
	for _, verb := range []string{"get", "update", "patch"} {
		assertClusterRoleGrant(t, role, "ach.ackstorm.ai", "workspaces/status", verb)
	}
	for _, rule := range role.Rules {
		for _, resource := range rule.Resources {
			if resource == "ach.ackstorm.ai/workspaces/finalizers" || resource == "workspaces/finalizers" {
				t.Errorf("operator must not receive a Workspace finalizer grant: %+v", rule)
			}
			if resource == "statefulsets/scale" {
				t.Errorf("operator must not receive statefulsets/scale: %+v", rule)
			}
		}
	}
	for _, verb := range []string{"create", "update", "patch", "delete"} {
		assertClusterRoleGrant(t, role, "apps", "statefulsets", verb)
		assertClusterRoleGrant(t, role, "", "services", verb)
	}
	for _, verb := range []string{"get", "list", "watch", "delete"} {
		assertClusterRoleGrant(t, role, "", "pods", verb)
	}

	harnessRules := []struct {
		group, resource string
		verbs           []string
	}{
		{"ach.ackstorm.ai", "workspaces", []string{"create", "delete", "get", "list", "patch", "update", "watch"}},
		{"apps", "statefulsets", []string{"get", "list", "watch"}},
		{"", "services", []string{"get", "list", "watch"}},
		{"", "pods", []string{"delete", "get", "list", "watch"}},
	}
	for _, want := range harnessRules {
		for _, verb := range want.verbs {
			assertClusterRoleGrant(t, role, want.group, want.resource, verb)
		}
	}
}

func assertClusterRoleGrant(t *testing.T, role *rbacv1.ClusterRole, group, resource, verb string) {
	t.Helper()
	for _, rule := range role.Rules {
		if containsExactString(rule.APIGroups, group) && containsExactString(rule.Resources, resource) && containsExactString(rule.Verbs, verb) {
			if len(rule.ResourceNames) != 0 {
				t.Errorf("ClusterRole grant %s on %s/%s is restricted to resourceNames %v", verb, group, resource, rule.ResourceNames)
			}
			return
		}
	}
	t.Errorf("ClusterRole is missing %s on %s/%s", verb, group, resource)
}

func containsExactString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
