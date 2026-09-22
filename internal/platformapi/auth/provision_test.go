// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keycrypt"
	"github.com/ackstorm/ach/internal/litellm"
)

// testDEK is a deterministic 32-byte data-encryption key for the callback
// seal path (G3). The callback now encrypts the LiteLLM virtual-key material
// at rest, so every Deps that drives a successful KeyGenerate+insert must
// carry a valid DEK.
func testDEK() []byte {
	k := make([]byte, keycrypt.KeySize)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

// callRecord captures the LiteLLM operations invoked during a single
// CallbackHandler test invocation. The fake LiteLLM client mutates this
// struct.
type callRecord struct {
	userInfoByEmailCalls  int
	userNewCalls          int
	teamMemberAddCalls    int
	keyGenerateCalls      int
	revokeKeyCalls        int
	revokeKeyTokensSeen   []string
	lastUserNewEmail      string
	lastUserNewReq        *litellm.UserNewRequest
	lastTeamMemberAddTeam string
	lastTeamMemberAddUser string
	lastTeamMemberAddRole string
	lastKeyGenerateKey    string
	lastKeyGenerateUser   string
	lastKeyGenerateBudget *float64
	lastKeyGenerateReq    *litellm.KeyGenerateRequest
	listTeamsCalls        int
	lastListTeamsAlias    string
	lastCreateTeamReq     *litellm.NewTeamRequest
}

// fakeLiteLLM is the test client implementing litellm.Client. Per-method
// behaviour is configured by setter funcs the tests assign before
// invoking CallbackHandler. Default behaviour is "first-time user happy
// path": UserInfoByEmail returns ErrNotFound, UserNew succeeds, etc.
type fakeLiteLLM struct {
	rec *callRecord

	// Behaviour switches (defaults match Test 1: first-time SSO happy).
	userInfoBehaviour    func(email string) (*litellm.UserInfo, error)
	userNewBehaviour     func(req *litellm.UserNewRequest) (*litellm.UserInfo, error)
	teamMemberAddError   func(teamID, userID, role string) error
	keyGenerateBehaviour func(req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error)
	revokeKeyError       func(keyID string) error
	listTeamsBehaviour   func(alias string) ([]litellm.TeamListEntry, error)
	listUserKeys         func(userID string) ([]litellm.UserKeyInfo, error)
	createTeamBehaviour  func(req *litellm.NewTeamRequest) (*litellm.TeamListEntry, error)

	// upsertedTags records every tag budget written through
	// UpsertTagBudget; upsertTagErr makes that write fail.
	upsertedTags map[string]litellm.TagBudget
	upsertTagErr error
}

func newFakeLiteLLM() *fakeLiteLLM {
	return &fakeLiteLLM{
		rec:          &callRecord{},
		upsertedTags: map[string]litellm.TagBudget{},
		userInfoBehaviour: func(string) (*litellm.UserInfo, error) {
			return nil, litellm.ErrNotFound
		},
		userNewBehaviour: func(req *litellm.UserNewRequest) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "litellm-user-" + req.UserEmail, UserEmail: req.UserEmail}, nil
		},
		teamMemberAddError: func(string, string, string) error { return nil },
		keyGenerateBehaviour: func(req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
			return &litellm.KeyGenerateResponse{
				// TESTING-PHASE (reverts FIX01 §A.6): LiteLLM mints its own
				// virtual-key plaintext (sk-…) in the response; ACH now
				// persists it. Use a fixed sentinel so the mint test can
				// assert the inserted row carries it.
				Key:    "sk-test-pk-material",
				Token:  "litellm-token-" + req.UserID,
				UserID: req.UserID,
			}, nil
		},
		revokeKeyError: func(string) error { return nil },
		listTeamsBehaviour: func(string) ([]litellm.TeamListEntry, error) {
			// Match production LiteLLM: alias "default" resolves to a UUID
			// team_id. Tests asserting on team_id should override this.
			return []litellm.TeamListEntry{
				{TeamID: "team-uuid-default", TeamAlias: "default"},
			}, nil
		},
	}
}

func (f *fakeLiteLLM) ListTeamsByAlias(_ context.Context, alias string) ([]litellm.TeamListEntry, error) {
	f.rec.listTeamsCalls++
	f.rec.lastListTeamsAlias = alias
	return f.listTeamsBehaviour(alias)
}

// ListAllTeams is a no-op shim — interface compliance only; the SSO
// handler does not call it.
func (f *fakeLiteLLM) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	return nil, nil
}

func (f *fakeLiteLLM) EnsureDefaultTeam(_ context.Context) error { return nil }

// UpdateTeam/DeleteTeam/GetTeamInfo are no-op shims — Client interface
// compliance. CreateTeam is recorded + configurable: mintAndPersistPK calls
// it to ensure the caller's per-user shell before KeyGenerate.
func (f *fakeLiteLLM) CreateTeam(_ context.Context, req *litellm.NewTeamRequest) (*litellm.TeamListEntry, error) {
	f.rec.lastCreateTeamReq = req
	if f.createTeamBehaviour != nil {
		return f.createTeamBehaviour(req)
	}
	return &litellm.TeamListEntry{TeamID: req.TeamID, TeamAlias: req.TeamAlias}, nil
}
func (f *fakeLiteLLM) UpdateTeam(_ context.Context, _ *litellm.TeamUpdateRequest) (*litellm.TeamListEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) DeleteTeam(_ context.Context, _ string) error { return nil }
func (f *fakeLiteLLM) GetTeamInfo(_ context.Context, _ string) (*litellm.TeamListEntry, error) {
	return nil, nil
}

// Unused-method shims to satisfy the wider litellm.Client interface.
func (f *fakeLiteLLM) DeleteAccessGroup(context.Context, string) error { return nil }
func (f *fakeLiteLLM) DeleteTag(context.Context, string) error         { return nil }
func (f *fakeLiteLLM) UpsertTagBudget(_ context.Context, name string, b litellm.TagBudget) error {
	if f.upsertTagErr != nil {
		return f.upsertTagErr
	}
	f.upsertedTags[name] = b
	return nil
}
func (f *fakeLiteLLM) TagInfo(context.Context, string) (*litellm.TagInfoEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) DeleteTagByName(context.Context, string) error { return nil }
func (f *fakeLiteLLM) DeleteBudget(context.Context, string) error    { return nil }
func (f *fakeLiteLLM) ListModels(context.Context) ([]litellm.ModelInfoResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListMCPServers(context.Context) ([]litellm.MCPServerEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListA2AAgents(context.Context) ([]litellm.AgentEntry, error) { return nil, nil }
func (f *fakeLiteLLM) ListGuardrails(context.Context) ([]litellm.GuardrailEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListUserKeys(_ context.Context, userID string) ([]litellm.UserKeyInfo, error) {
	if f.listUserKeys != nil {
		return f.listUserKeys(userID)
	}
	return nil, nil
}

func (f *fakeLiteLLM) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	f.rec.userInfoByEmailCalls++
	return f.userInfoBehaviour(email)
}
func (f *fakeLiteLLM) UserNew(_ context.Context, req *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	f.rec.userNewCalls++
	f.rec.lastUserNewEmail = req.UserEmail
	f.rec.lastUserNewReq = req
	return f.userNewBehaviour(req)
}
func (f *fakeLiteLLM) TeamMemberAdd(_ context.Context, teamID, userID, role string) error {
	f.rec.teamMemberAddCalls++
	f.rec.lastTeamMemberAddTeam = teamID
	f.rec.lastTeamMemberAddUser = userID
	f.rec.lastTeamMemberAddRole = role
	return f.teamMemberAddError(teamID, userID, role)
}
func (f *fakeLiteLLM) KeyGenerate(_ context.Context, req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	f.rec.keyGenerateCalls++
	f.rec.lastKeyGenerateKey = req.Key
	f.rec.lastKeyGenerateUser = req.UserID
	f.rec.lastKeyGenerateBudget = req.MaxBudget
	f.rec.lastKeyGenerateReq = req
	return f.keyGenerateBehaviour(req)
}
func (f *fakeLiteLLM) RevokeKey(_ context.Context, keyID string) error {
	f.rec.revokeKeyCalls++
	f.rec.revokeKeyTokensSeen = append(f.rec.revokeKeyTokensSeen, keyID)
	return f.revokeKeyError(keyID)
}

// dbInsertRecord captures the (last) PkInsertRow seen by the seam-injected
// InsertPersonalKey, plus the count + an optional error.
type dbInsertRecord struct {
	calls       int
	lastRow     db.PkInsertRow
	failWith    error
	rowsByKeyID map[string]db.PkInsertRow
}

func newDBInsertRecord() *dbInsertRecord {
	return &dbInsertRecord{rowsByKeyID: map[string]db.PkInsertRow{}}
}

func (d *dbInsertRecord) insertFn(_ context.Context, row db.PkInsertRow) error {
	d.calls++
	d.lastRow = row
	d.rowsByKeyID[row.KeyID] = row
	return d.failWith
}

func (f *fakeLiteLLM) CreateAccessGroup(_ context.Context, _ litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) GetAccessGroupByName(_ context.Context, _ string) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) UpdateAccessGroup(_ context.Context, _ string, _ litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) DeleteAccessGroupByID(_ context.Context, _ string) error { return nil }

// --- Phase 6 D-20 — LoginHandler + CallbackHandler session_id threading ---
func provisionDeps(flm *fakeLiteLLM) Deps {
	return Deps{LiteLLM: flm, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// provisionUser is the LiteLLM side of every login (the AS calls it from
// as-callback): resolve the "default" team by alias, create the user with
// user_id=email and no auto key, enrol them — idempotently.
func TestProvisionUser(t *testing.T) {
	t.Run("first time: user_id=email, no auto key, enrolled in the resolved default team", func(t *testing.T) {
		flm := newFakeLiteLLM()
		uid, err := provisionUser(context.Background(), provisionDeps(flm), "alice@example.com")
		if err != nil || uid != "litellm-user-alice@example.com" {
			t.Fatalf("uid=%q err=%v", uid, err)
		}
		req := flm.rec.lastUserNewReq
		if req == nil || req.UserID != "alice@example.com" || req.AutoCreateKey == nil || *req.AutoCreateKey {
			t.Fatalf("UserNew request: %+v", req)
		}
		if flm.rec.lastListTeamsAlias != "default" || flm.rec.lastTeamMemberAddTeam != "team-uuid-default" || flm.rec.lastTeamMemberAddRole != "user" {
			t.Fatalf("team enrolment: %+v", flm.rec)
		}
	})

	t.Run("first time: 409 duplicate user recovers with user_id=email", func(t *testing.T) {
		flm := newFakeLiteLLM()
		flm.userNewBehaviour = func(*litellm.UserNewRequest) (*litellm.UserInfo, error) {
			return nil, &litellm.APIError{Method: "POST", Path: "/user/new", StatusCode: 409, Code: "409",
				Body: []byte(`{"error":{"message":"User with id alice@example.com already exists","code":"409"}}`)}
		}
		uid, err := provisionUser(context.Background(), provisionDeps(flm), "alice@example.com")
		if err != nil || uid != "alice@example.com" {
			t.Fatalf("uid=%q err=%v", uid, err)
		}
	})

	t.Run("existing user: no UserNew, TeamMemberAdd still called, duplicate-add swallowed", func(t *testing.T) {
		flm := newFakeLiteLLM()
		flm.userInfoBehaviour = func(email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "litellm-existing", UserEmail: email}, nil
		}
		// LiteLLM 1.83 returns 400 on duplicate-add; the 4xx wrapper drops the
		// body, so only path + status identify it (isDuplicateAddErr).
		flm.teamMemberAddError = func(string, string, string) error {
			return errors.New("litellm: 400 on POST /team/member_add (code=400)")
		}
		uid, err := provisionUser(context.Background(), provisionDeps(flm), "bob@example.com")
		if err != nil || uid != "litellm-existing" || flm.rec.userNewCalls != 0 || flm.rec.teamMemberAddCalls != 1 {
			t.Fatalf("uid=%q err=%v rec=%+v", uid, err, flm.rec)
		}
	})

	t.Run("default team alias missing in LiteLLM", func(t *testing.T) {
		flm := newFakeLiteLLM()
		flm.listTeamsBehaviour = func(string) ([]litellm.TeamListEntry, error) { return nil, nil }
		_, err := provisionUser(context.Background(), provisionDeps(flm), "x@example.com")
		var pe *provisionErr
		if !errors.As(err, &pe) || pe.kind != provisionKindDefaultTeamMissing || flm.rec.userNewCalls != 0 {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("TeamMemberAdd not-found on an existing user is default team missing", func(t *testing.T) {
		flm := newFakeLiteLLM()
		flm.userInfoBehaviour = func(email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "litellm-dave", UserEmail: email}, nil
		}
		flm.teamMemberAddError = func(string, string, string) error { return litellm.ErrNotFound }
		_, err := provisionUser(context.Background(), provisionDeps(flm), "dave@example.com")
		var pe *provisionErr
		if !errors.As(err, &pe) || pe.kind != provisionKindDefaultTeamMissing {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("LiteLLM unreachable", func(t *testing.T) {
		flm := newFakeLiteLLM()
		flm.userInfoBehaviour = func(string) (*litellm.UserInfo, error) { return nil, errors.New("dial tcp: refused") }
		_, err := provisionUser(context.Background(), provisionDeps(flm), "x@example.com")
		var pe *provisionErr
		if !errors.As(err, &pe) || pe.kind != provisionKindLitellm {
			t.Fatalf("err=%v", err)
		}
	})
}

func mintDeps(flm *fakeLiteLLM, rec *dbInsertRecord) Deps {
	d := provisionDeps(flm)
	d.Pepper, d.KeyEncryptionKey, d.InsertPKFn = []byte("test-pepper-32-bytes-long-aaaaaa"), testDEK(), rec.insertFn
	return d
}

func TestMintPK_PutsKeyInUserShellWithExpiry(t *testing.T) {
	flm := newFakeLiteLLM()
	if _, _, err := mintDeps(flm, newDBInsertRecord()).MintPK(context.Background(), "alice@example.com", "uid", "oauth"); err != nil {
		t.Fatal(err)
	}
	const wantShell = "ach-user-alice@example.com"
	if flm.rec.lastCreateTeamReq == nil || flm.rec.lastCreateTeamReq.TeamID != wantShell {
		t.Fatalf("user shell not ensured: %+v", flm.rec.lastCreateTeamReq)
	}
	if kg := flm.rec.lastKeyGenerateReq; kg == nil || kg.TeamID != wantShell || kg.Duration == "" || kg.MaxBudget != nil {
		t.Fatalf("KeyGenerate: %+v", kg)
	}
}

func TestMintPK_DuplicateShellIsSuccess(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.createTeamBehaviour = func(*litellm.NewTeamRequest) (*litellm.TeamListEntry, error) {
		return nil, &litellm.APIError{StatusCode: 400, Path: "/team/new", Body: []byte("Team id = x already exists")}
	}
	if _, _, err := mintDeps(flm, newDBInsertRecord()).MintPK(context.Background(), "alice@example.com", "uid", "oauth"); err != nil {
		t.Fatal(err)
	}
	if flm.rec.keyGenerateCalls != 1 {
		t.Fatal("KeyGenerate must run after a duplicate-shell 400")
	}
}

func TestMintPK_ShellEnsureFailure_503_NoMint(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.createTeamBehaviour = func(*litellm.NewTeamRequest) (*litellm.TeamListEntry, error) { return nil, errors.New("litellm down") }
	_, _, err := mintDeps(flm, newDBInsertRecord()).MintPK(context.Background(), "alice@example.com", "uid", "oauth")
	var me *MintError
	if !errors.As(err, &me) || me.Status != 503 || !errors.Is(err, ErrMintLiteLLM) || flm.rec.keyGenerateCalls != 0 {
		t.Fatalf("err=%v keyGenerateCalls=%d", err, flm.rec.keyGenerateCalls)
	}
}

// TestProvisionUserWritesUserBudgetTag — a configured default budget lands
// on the user's own tag at first login, so every later pk_ and ek_ request
// (which carries user:<email>) is capped.
func TestProvisionUserWritesUserBudgetTag(t *testing.T) {
	flm := newFakeLiteLLM()
	deps := provisionDeps(flm)
	deps.UserBudget = &litellm.TagBudget{MaxBudget: 100, BudgetDuration: "30d"}

	if _, err := provisionUser(context.Background(), deps, "Pepe@Example.com"); err != nil {
		t.Fatalf("provisionUser: %v", err)
	}
	got, ok := flm.upsertedTags["user:pepe@example.com"]
	if !ok {
		t.Fatalf("no budget tag written; got %v", flm.upsertedTags)
	}
	if got.MaxBudget != 100 || got.BudgetDuration != "30d" {
		t.Fatalf("tag budget = %+v, want {100 30d}", got)
	}
}

// TestProvisionUserWritesUserBudgetTagForExistingUser — the second login
// takes the existing-user branch; the ceiling must land there too.
func TestProvisionUserWritesUserBudgetTagForExistingUser(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.userInfoBehaviour = func(email string) (*litellm.UserInfo, error) {
		return &litellm.UserInfo{UserID: "litellm-existing", UserEmail: email}, nil
	}
	deps := provisionDeps(flm)
	deps.UserBudget = &litellm.TagBudget{MaxBudget: 100}

	if _, err := provisionUser(context.Background(), deps, "pepe@example.com"); err != nil {
		t.Fatalf("provisionUser: %v", err)
	}
	if _, ok := flm.upsertedTags["user:pepe@example.com"]; !ok {
		t.Fatalf("no budget tag written; got %v", flm.upsertedTags)
	}
}

// TestProvisionUserWithoutBudgetWritesNoTag — unset defaults stay unset.
func TestProvisionUserWithoutBudgetWritesNoTag(t *testing.T) {
	flm := newFakeLiteLLM()
	deps := provisionDeps(flm)
	deps.UserBudget = nil

	if _, err := provisionUser(context.Background(), deps, "pepe@example.com"); err != nil {
		t.Fatalf("provisionUser: %v", err)
	}
	if len(flm.upsertedTags) != 0 {
		t.Fatalf("want no tag writes, got %v", flm.upsertedTags)
	}
}

// TestProvisionUserTagFailureIsLoud — LiteLLM refusing the tag write must
// not be swallowed: an uncapped user is a governance hole, not a detail.
func TestProvisionUserTagFailureIsLoud(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.upsertTagErr = errors.New("boom")
	deps := provisionDeps(flm)
	deps.UserBudget = &litellm.TagBudget{MaxBudget: 100}

	_, err := provisionUser(context.Background(), deps, "pepe@example.com")
	var pe *provisionErr
	if !errors.As(err, &pe) || pe.kind != provisionKindLitellm {
		t.Fatalf("want a litellm provisionErr, got %v", err)
	}
}
