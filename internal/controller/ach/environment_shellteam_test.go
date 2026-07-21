// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

// TestEnsureShellTeamCreateFailureAborts: when CreateTeam fails (no shell
// exists yet), ensureShellTeam must return the wrapped sentinel and an empty
// id — this is the signal reconcileAccessGroup relies on to abort the pass via
// shellTeamFailed instead of falling through to CreateAccessGroup/UpdateAccessGroup.
func TestEnsureShellTeamCreateFailureAborts(t *testing.T) {
	fake := newAccessGroupFake()
	sentinel := errors.New("litellm unavailable")
	fake.teamCreateErr = sentinel
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	id, err := r.ensureShellTeam(context.Background(), env, "", logr.Discard())
	if err == nil {
		t.Fatal("ensureShellTeam: want error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("ensureShellTeam error = %v, want it to wrap %v", err, sentinel)
	}
	if id != "" {
		t.Fatalf("team id = %q, want empty on failure", id)
	}
}

// TestShellTeamFailedCondition: shellTeamFailed must produce the closed-set
// AccessGroupSynced=False/ShellTeamFailed condition that reconcileAccessGroup
// returns to abort the pass on a shell-team failure (see the fake-driven abort
// test above for the error side of that path).
func TestShellTeamFailedCondition(t *testing.T) {
	env := &achv1alpha1.Environment{}
	env.Name = "demo"
	sentinel := errors.New("boom")

	cond := shellTeamFailed(env, sentinel)

	if cond.Type != "AccessGroupSynced" {
		t.Fatalf("Type = %q, want AccessGroupSynced", cond.Type)
	}
	if cond.Status != metav1.ConditionFalse {
		t.Fatalf("Status = %q, want %q", cond.Status, metav1.ConditionFalse)
	}
	if cond.Reason != "ShellTeamFailed" {
		t.Fatalf("Reason = %q, want ShellTeamFailed", cond.Reason)
	}
}

// TestReconcileDeletionOrder asserts the load-bearing LiteLLM call order on
// Environment deletion: keys → access group → shell team. Deleting the team
// first would leave its keys answering 200 for ~60s with no route to revoke
// them (references/litellm-permission-model.md §8).
func TestReconcileDeletionOrder(t *testing.T) {
	fake := newAccessGroupFake()
	// Seed a shell team so deleteShellTeam has something to delete.
	if _, err := fake.CreateTeam(context.Background(), litellm.NewShellTeamRequest("demo")); err != nil {
		t.Fatalf("seed CreateTeam: %v", err)
	}
	fake.order = nil

	r := &EnvironmentReconciler{LiteLLM: fake} // DB nil ⇒ no ek_ rows to revoke
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	if err := r.revokeEnvironmentKeys(context.Background(), env, logr.Discard()); err != nil {
		t.Fatalf("revokeEnvironmentKeys: %v", err)
	}
	if err := r.LiteLLM.DeleteAccessGroup(context.Background(), env.Name); err != nil {
		t.Fatalf("DeleteAccessGroup: %v", err)
	}
	if err := r.deleteShellTeam(context.Background(), env, logr.Discard()); err != nil {
		t.Fatalf("deleteShellTeam: %v", err)
	}

	if len(fake.order) == 0 || fake.order[len(fake.order)-1] != "DeleteTeam" {
		t.Fatalf("call order = %v, want DeleteTeam last", fake.order)
	}
	for i, call := range fake.order {
		if call == "DeleteTeam" {
			for _, later := range fake.order[i+1:] {
				if later == "RevokeKey" || later == "DeleteAccessGroup" {
					t.Fatalf("call order = %v, want every revoke/group delete BEFORE DeleteTeam", fake.order)
				}
			}
		}
	}
}
