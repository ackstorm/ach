// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/ackstorm/ach/internal/litellm"
)

func TestMintPK_OAuthPurposeRowAndDiscardablePlaintext(t *testing.T) {
	flm := newFakeLiteLLM()
	rec := newDBInsertRecord()
	deps := Deps{LiteLLM: flm, Pepper: []byte("test-pepper-32-bytes-long-aaaaaa"),
		KeyEncryptionKey: testDEK(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		InsertPKFn: rec.insertFn, Issuer: "https://ach.test"}

	plaintext, row, err := deps.MintPK(context.Background(), "u@x.com", "litellm-user-1", "oauth")
	if err != nil {
		t.Fatal(err)
	}
	if md := flm.rec.lastKeyGenerateReq.Metadata; md["ach_issuer"] != "https://ach.test" || md["ach_key_id"] != row.KeyID {
		t.Fatalf("key metadata: %v", md)
	}
	if plaintext == "" || row.Purpose != "oauth" || row.OwnerEmail != "u@x.com" || row.LiteLLMToken == nil {
		t.Fatalf("plaintext=%q row=%+v", plaintext, row)
	}
	if rec.calls != 1 || rec.lastRow.KeyID != row.KeyID || rec.lastRow.Purpose != "oauth" {
		t.Fatalf("insert not recorded: %+v", rec.lastRow)
	}
}

func TestMintPK_InsertFailureCompensatesAndClassifies(t *testing.T) {
	flm := newFakeLiteLLM()
	revoked := 0
	flm.revokeKeyError = func(string) error { revoked++; return nil }
	rec := newDBInsertRecord()
	rec.failWith = errors.New("db down")
	deps := Deps{LiteLLM: flm, Pepper: []byte("test-pepper-32-bytes-long-aaaaaa"),
		KeyEncryptionKey: testDEK(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		InsertPKFn: rec.insertFn}
	_, _, err := deps.MintPK(context.Background(), "u@x.com", "uid", "cli")
	var me *MintError
	if !errors.As(err, &me) || me.Status != 500 || me.KeyID == "" || revoked != 1 {
		t.Fatalf("err=%v revoked=%d", err, revoked)
	}
}

// mintDepsForRepair builds MintPK deps whose CreateTeam always answers
// LiteLLM's "already exists" — the steady state for every login after the
// first, and the only path that reaches repairUserShell.
func mintDepsForRepair(flm *fakeLiteLLM) Deps {
	flm.createTeamBehaviour = func(*litellm.NewTeamRequest) (*litellm.TeamListEntry, error) {
		return nil, errors.New("POST /team/new: team already exists")
	}
	return Deps{LiteLLM: flm, Pepper: []byte("test-pepper-32-bytes-long-aaaaaa"),
		KeyEncryptionKey: testDEK(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		InsertPKFn: newDBInsertRecord().insertFn, Issuer: "https://ach.test"}
}

// userShell returns a GET /team/info read-back of an ach-user shell carrying
// the given models, with the ownership marker and the rest of the deny-all
// block intact.
func userShell(models []string) *litellm.TeamListEntry {
	return &litellm.TeamListEntry{
		TeamID: "ach-user-u@x.com", TeamAlias: "ach-user-u@x.com",
		Models:           models,
		ObjectPermission: litellm.ShellTeamPermissions(),
		Metadata:         json.RawMessage(`{"ach_managed":"user-shell","ach_user":"u@x.com"}`),
	}
}

// TestMintPK_RepairsDriftedUserShell — a shell written by an older ACH still
// carries the legacy sentinel; the login that finds it migrates it, which is
// the only thing that takes the phantom model out of that person's catalog.
func TestMintPK_RepairsDriftedUserShell(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.getTeamInfoBehaviour = func(string) (*litellm.TeamListEntry, error) {
		return userShell([]string{litellm.ShellTeamDenyAllModelLegacy}), nil
	}
	deps := mintDepsForRepair(flm)

	if _, _, err := deps.MintPK(context.Background(), "u@x.com", "uid", "oauth"); err != nil {
		t.Fatalf("MintPK: %v", err)
	}
	if len(flm.updateTeamReqs) != 1 {
		t.Fatalf("want exactly 1 UpdateTeam, got %d", len(flm.updateTeamReqs))
	}
	req := flm.updateTeamReqs[0]
	if req.TeamID != "ach-user-u@x.com" || len(req.Models) != 1 || req.Models[0] != litellm.ShellTeamDenyAllModel {
		t.Fatalf("repair body = %+v", req)
	}
	if req.Metadata["ach_managed"] != litellm.UserShellManagedMetadataValue || req.Guardrails == nil {
		t.Fatalf("repair must re-stamp ownership and send an explicit []: %+v", req)
	}
}

// TestMintPK_LeavesACurrentUserShellAlone — no drift, no write: the repair
// must not turn every login into a team update.
func TestMintPK_LeavesACurrentUserShellAlone(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.getTeamInfoBehaviour = func(string) (*litellm.TeamListEntry, error) {
		return userShell([]string{litellm.ShellTeamDenyAllModel}), nil
	}
	deps := mintDepsForRepair(flm)

	if _, _, err := deps.MintPK(context.Background(), "u@x.com", "uid", "oauth"); err != nil {
		t.Fatalf("MintPK: %v", err)
	}
	if len(flm.updateTeamReqs) != 0 {
		t.Fatalf("an undrifted shell must not be rewritten: %+v", flm.updateTeamReqs)
	}
}

// TestMintPK_NeverUpdatesATeamACHDoesNotOwn — same ownership rule as
// ensureShellTeam: a same-alias team with neither the marker nor the deny-all
// shape could be anyone's, and an UpdateTeam would overwrite its permissions.
func TestMintPK_NeverUpdatesATeamACHDoesNotOwn(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.getTeamInfoBehaviour = func(string) (*litellm.TeamListEntry, error) {
		return &litellm.TeamListEntry{
			TeamID: "ach-user-u@x.com", TeamAlias: "ach-user-u@x.com",
			Models: []string{"gpt-4"}, // real grants, no ACH metadata
		}, nil
	}
	deps := mintDepsForRepair(flm)

	if _, _, err := deps.MintPK(context.Background(), "u@x.com", "uid", "oauth"); err != nil {
		t.Fatalf("MintPK: %v", err)
	}
	if len(flm.updateTeamReqs) != 0 {
		t.Fatalf("ACH must not touch a team it does not own: %+v", flm.updateTeamReqs)
	}
}

// TestMintPK_RepairFailuresStillMintAKey — repair is opportunistic. The shell
// already exists and already denies, so neither a failed read nor a failed
// write may cost the person a usable pk_.
func TestMintPK_RepairFailuresStillMintAKey(t *testing.T) {
	t.Run("read fails", func(t *testing.T) {
		flm := newFakeLiteLLM()
		flm.getTeamInfoBehaviour = func(string) (*litellm.TeamListEntry, error) {
			return nil, errors.New("litellm down")
		}
		plaintext, _, err := mintDepsForRepair(flm).MintPK(context.Background(), "u@x.com", "uid", "oauth")
		if err != nil || plaintext == "" {
			t.Fatalf("plaintext=%q err=%v", plaintext, err)
		}
		if len(flm.updateTeamReqs) != 0 {
			t.Fatalf("a failed read must write nothing: %+v", flm.updateTeamReqs)
		}
	})
	t.Run("write fails", func(t *testing.T) {
		flm := newFakeLiteLLM()
		flm.getTeamInfoBehaviour = func(string) (*litellm.TeamListEntry, error) {
			return userShell([]string{litellm.ShellTeamDenyAllModelLegacy}), nil
		}
		flm.updateTeamErr = errors.New("litellm down")
		plaintext, _, err := mintDepsForRepair(flm).MintPK(context.Background(), "u@x.com", "uid", "oauth")
		if err != nil || plaintext == "" {
			t.Fatalf("plaintext=%q err=%v", plaintext, err)
		}
	})
}
