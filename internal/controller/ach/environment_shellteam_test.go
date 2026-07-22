// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
)

// shellTeamMetadataJSON marshals the same ownership metadata
// litellm.ShellTeamMetadata produces, for seeding a fake TeamListEntry as
// already ACH-managed.
func shellTeamMetadataJSON(t *testing.T, env string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(litellm.ShellTeamMetadata(env))
	if err != nil {
		t.Fatalf("marshal shell team metadata: %v", err)
	}
	return raw
}

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
		Metadata:         shellTeamMetadataJSON(t, "demo"),
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
		Metadata:         shellTeamMetadataJSON(t, "demo"),
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

// TestEnsureShellTeamRepairVerifiesResponse is Fix 1's load-bearing test: a
// POST /team/update that 200s WITHOUT actually applying the sentinels must
// not be reported as a successful repair. UpdateTeam's response carries
// object_permission inline (unlike the list endpoints), so ensureShellTeam
// must re-check it instead of trusting the status code.
func TestEnsureShellTeamRepairVerifiesResponse(t *testing.T) {
	fake := newAccessGroupFake()
	fake.teamsByID["t-drifted"] = litellm.TeamListEntry{
		TeamID:           "t-drifted",
		TeamAlias:        "ach-env-demo",
		Models:           []string{},
		ObjectPermission: &litellm.TeamObjectPermission{Agents: []string{}},
		Metadata:         shellTeamMetadataJSON(t, "demo"),
	}
	// The response LiteLLM hands back is STILL drifted despite the request —
	// models a write that 200s but never actually applied.
	fake.teamUpdateResult = &litellm.TeamListEntry{
		TeamID:           "t-drifted",
		TeamAlias:        "ach-env-demo",
		Models:           []string{},
		ObjectPermission: &litellm.TeamObjectPermission{Agents: []string{}},
	}
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	if _, err := r.ensureShellTeam(context.Background(), env, "t-drifted", logr.Discard()); err == nil {
		t.Fatal("ensureShellTeam: want error when the repair response is still drifted, got nil")
	}
	if fake.teamUpdateCalls["t-drifted"] != 1 {
		t.Fatalf("UpdateTeam calls = %d, want 1", fake.teamUpdateCalls["t-drifted"])
	}
}

// TestEnsureShellTeamAdoptsUnmarkedShellShaped is Fix 2's migration-path
// test: a shell created before ownership metadata existed (every shell in
// the running kind cluster today) carries none, but its alias and deny-all
// Models sentinel are unmistakably shell-shaped. It must be adopted — a
// repair write that ALSO stamps the metadata — rather than refused forever.
func TestEnsureShellTeamAdoptsUnmarkedShellShaped(t *testing.T) {
	fake := newAccessGroupFake()
	fake.teamsByID["t-premigration"] = litellm.TeamListEntry{
		TeamID:           "t-premigration",
		TeamAlias:        "ach-env-demo",
		Models:           []string{litellm.ShellTeamDenyAllModel},
		ObjectPermission: litellm.ShellTeamPermissions(),
		// No Metadata — the pre-Fix-2 state.
	}
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	id, err := r.ensureShellTeam(context.Background(), env, "t-premigration", logr.Discard())
	if err != nil {
		t.Fatalf("ensureShellTeam: want adoption to succeed, got error: %v", err)
	}
	if id != "t-premigration" {
		t.Fatalf("team id = %q, want t-premigration", id)
	}
	if fake.teamUpdateCalls["t-premigration"] != 1 {
		t.Fatalf("UpdateTeam calls = %d, want 1 (the adoption stamp)", fake.teamUpdateCalls["t-premigration"])
	}
	got := fake.teamsByID["t-premigration"]
	if !litellm.IsShellTeamManaged(got, "demo") {
		t.Fatalf("team not marked managed after adoption: metadata = %s", got.Metadata)
	}
}

// TestEnsureShellTeamRefusesUnmanagedNonShellShaped is Fix 2's refusal test:
// a same-alias team that is neither marked ACH-managed NOR shell-shaped
// could be anything an admin created by hand. ensureShellTeam must refuse to
// touch it (no UpdateTeam call) rather than silently overwriting its
// models/object_permission.
func TestEnsureShellTeamRefusesUnmanagedNonShellShaped(t *testing.T) {
	fake := newAccessGroupFake()
	fake.teamsByID["t-foreign"] = litellm.TeamListEntry{
		TeamID:    "t-foreign",
		TeamAlias: "ach-env-demo",
		Models:    []string{"gpt-4"},
	}
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	if _, err := r.ensureShellTeam(context.Background(), env, "t-foreign", logr.Discard()); err == nil {
		t.Fatal("ensureShellTeam: want error for an unmanaged, non-shell-shaped team, got nil")
	}
	if fake.teamUpdateCalls["t-foreign"] != 0 {
		t.Fatalf("UpdateTeam calls = %d, want 0 — must never touch an unrecognized team", fake.teamUpdateCalls["t-foreign"])
	}
}

// TestDeleteShellTeamSkipsUnmanaged: deleteShellTeam must never DeleteTeam
// (which CASCADES to that team's keys) a same-alias team it cannot prove it
// created.
func TestDeleteShellTeamSkipsUnmanaged(t *testing.T) {
	fake := newAccessGroupFake()
	foreign := litellm.TeamListEntry{
		TeamID:    "t-foreign",
		TeamAlias: "ach-env-demo",
		Models:    []string{"gpt-4"},
	}
	fake.teamsByAlias["ach-env-demo"] = []litellm.TeamListEntry{foreign}
	fake.teamsByID["t-foreign"] = foreign
	r := &EnvironmentReconciler{LiteLLM: fake}
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	if err := r.deleteShellTeam(context.Background(), env, logr.Discard()); err != nil {
		t.Fatalf("deleteShellTeam: %v", err)
	}
	if fake.teamDeleteCalls["t-foreign"] != 0 {
		t.Fatalf("DeleteTeam calls = %d, want 0 for an unmanaged team", fake.teamDeleteCalls["t-foreign"])
	}
	if _, ok := fake.teamsByID["t-foreign"]; !ok {
		t.Fatal("team was removed from fake state — DeleteTeam must have been skipped, not called")
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

// TestReconcileDeletionOrder drives the REAL reconcileDeletion (the §6.5
// finalizer drain in environment_controller.go) end-to-end against a fake
// client, and asserts the load-bearing LiteLLM call order it PRODUCES:
// keys → access group → shell team. Deleting the team first would leave its
// keys answering 200 for ~60s with no route to revoke them
// (references/litellm-permission-model.md §8).
//
// A prior version of this test called revokeEnvironmentKeys /
// DeleteAccessGroup / deleteShellTeam directly, in the order written in the
// test body, then asserted the recording matched that order — circular: it
// would still pass if the statements inside reconcileDeletion were reordered,
// which is the exact regression this test exists to catch.
//
// r.DB is left nil — the injected ListActiveEKsForRevoke seam is consulted
// BEFORE revokeEnvironmentKeys's `if r.DB == nil` guard, so it runs
// regardless, without also unlocking the OTHER real-Postgres codepaths later
// in reconcileDeletion (drainEkRows, softDeleteEnvironmentProjection) that
// have no seam of their own and would panic against a non-functional pool.
// The seam returns two fake key rows so RevokeKey does real work and the
// "keys revoked BEFORE the access group / shell team" leg is actually
// exercised, not vacuous.
func TestReconcileDeletionOrder(t *testing.T) {
	fake := newAccessGroupFake()
	// Seed a shell team so deleteShellTeam has something to delete.
	if _, err := fake.CreateTeam(context.Background(), litellm.NewShellTeamRequest("demo")); err != nil {
		t.Fatalf("seed CreateTeam: %v", err)
	}
	fake.order = nil

	scheme := runtime.NewScheme()
	if err := achv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	env := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "demo",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
	}
	c := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(env).Build()

	// Deleting an object that still carries a finalizer makes the fake
	// client (like a real apiserver) set DeletionTimestamp instead of
	// removing it — exactly the state reconcileDeletion's ContainsFinalizer
	// guard needs to see in order to proceed instead of no-opping.
	if err := c.Delete(context.Background(), env); err != nil {
		t.Fatalf("seed delete: %v", err)
	}
	var draining achv1alpha1.Environment
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(env), &draining); err != nil {
		t.Fatalf("get after seed delete: %v", err)
	}
	if draining.DeletionTimestamp.IsZero() {
		t.Fatal("seed: DeletionTimestamp not set after Delete — reconcileDeletion would no-op")
	}

	r := &EnvironmentReconciler{
		Client:  c,
		LiteLLM: fake,
		// DB stays nil (see the doc comment above); the seam is consulted
		// first and never touches it.
		ListActiveEKsForRevoke: func(_ context.Context, _ *pgxpool.Pool, environment string) ([]achdb.EkRevokeRow, error) {
			return []achdb.EkRevokeRow{
				{KeyID: "ekid_demo_1", LiteLLMToken: "tok-demo-1"},
				{KeyID: "ekid_demo_2", LiteLLMToken: "tok-demo-2"},
			}, nil
		},
	}

	if _, err := r.reconcileDeletion(context.Background(), &draining, logr.Discard()); err != nil {
		t.Fatalf("reconcileDeletion: %v", err)
	}

	// Exact order: both RevokeKey calls (one per seeded key row), then
	// DeleteAccessGroup, then DeleteTeam. deleteShellTeam's ListTeamsByAlias
	// lookup and DeleteTag are not recorded into fake.order, so this is the
	// full recording for the run.
	wantOrder := []string{"RevokeKey", "RevokeKey", "DeleteAccessGroup", "DeleteTeam"}
	if !slices.Equal(fake.order, wantOrder) {
		t.Fatalf("call order = %v, want %v", fake.order, wantOrder)
	}
}
