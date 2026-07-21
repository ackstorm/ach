// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"testing"

	"github.com/go-logr/logr"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
)

// TestEnsureShellTeamCreatesWithSentinels: absent shell → one POST /team/new
// carrying both sentinels, and the returned id is the created team's.
func TestEnsureShellTeamCreatesWithSentinels(t *testing.T) {
	fake := newAccessGroupFake()
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	id, err := r.ensureShellTeam(context.Background(), env, "", logr.Discard())
	if err != nil {
		t.Fatalf("ensureShellTeam: %v", err)
	}
	if id != "id-ach-env-demo" {
		t.Fatalf("team id = %q, want id-ach-env-demo", id)
	}
	if fake.teamCreateCalls["ach-env-demo"] != 1 {
		t.Fatalf("CreateTeam calls = %d, want 1", fake.teamCreateCalls["ach-env-demo"])
	}
	req := fake.lastTeamCreate["ach-env-demo"]
	if len(req.Models) != 1 || req.Models[0] != litellm.ShellTeamDenyAllModel {
		t.Fatalf("Models = %v, want the deny-all sentinel", req.Models)
	}
	if req.ObjectPermission == nil || len(req.ObjectPermission.Agents) != 1 {
		t.Fatalf("ObjectPermission = %+v, want the nil-UUID agent sentinel", req.ObjectPermission)
	}
}

// TestEnsureShellTeamIsIdempotent: a healthy existing shell triggers no write.
func TestEnsureShellTeamIsIdempotent(t *testing.T) {
	fake := newAccessGroupFake()
	fake.teamsByID["t-existing"] = litellm.TeamListEntry{
		TeamID:           "t-existing",
		TeamAlias:        "ach-env-demo",
		Models:           []string{litellm.ShellTeamDenyAllModel},
		ObjectPermission: litellm.ShellTeamPermissions(),
	}
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	id, err := r.ensureShellTeam(context.Background(), env, "t-existing", logr.Discard())
	if err != nil {
		t.Fatalf("ensureShellTeam: %v", err)
	}
	if id != "t-existing" {
		t.Fatalf("team id = %q, want t-existing", id)
	}
	if fake.teamCreateCalls["ach-env-demo"] != 0 || fake.teamUpdateCalls["t-existing"] != 0 {
		t.Fatalf("healthy shell was rewritten: create=%d update=%d",
			fake.teamCreateCalls["ach-env-demo"], fake.teamUpdateCalls["t-existing"])
	}
}

// TestEnsureShellTeamRepairsDrift: a shell whose sentinels were cleared is
// fail-open, so it must be repaired with one POST /team/update.
func TestEnsureShellTeamRepairsDrift(t *testing.T) {
	fake := newAccessGroupFake()
	fake.teamsByID["t-drifted"] = litellm.TeamListEntry{
		TeamID:           "t-drifted",
		TeamAlias:        "ach-env-demo",
		Models:           []string{},
		ObjectPermission: &litellm.TeamObjectPermission{Agents: []string{}},
	}
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	if _, err := r.ensureShellTeam(context.Background(), env, "t-drifted", logr.Discard()); err != nil {
		t.Fatalf("ensureShellTeam: %v", err)
	}
	if fake.teamUpdateCalls["t-drifted"] != 1 {
		t.Fatalf("UpdateTeam calls = %d, want 1", fake.teamUpdateCalls["t-drifted"])
	}
	got := fake.teamsByID["t-drifted"]
	if len(got.Models) != 1 || got.Models[0] != litellm.ShellTeamDenyAllModel {
		t.Fatalf("repaired Models = %v", got.Models)
	}
	if got.ObjectPermission == nil || len(got.ObjectPermission.Agents) != 1 {
		t.Fatalf("repaired ObjectPermission = %+v", got.ObjectPermission)
	}
}
