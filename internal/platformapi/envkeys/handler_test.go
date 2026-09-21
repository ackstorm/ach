// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keycrypt"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// keyEncTestDEK is a deterministic 32-byte data-encryption key for the
// CreateHandler seal path (G3). Every Deps that drives a successful
// KeyGenerate+insert must carry a valid DEK or the seal fails with 500.
func keyEncTestDEK() []byte {
	k := make([]byte, keycrypt.KeySize)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

// TestIsEnterpriseTagsRejection covers the detector that drives the
// drop-tags-and-retry degradation: only a 403 *litellm.APIError whose body
// names "LiteLLM Enterprise" qualifies. Any other status, error type, or
// body must NOT trigger the retry (it would mask a real failure).
func TestIsEnterpriseTagsRejection(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "403 enterprise tags body",
			err: &litellm.APIError{
				Method: http.MethodPost, Path: "/key/generate", StatusCode: 403, Code: "403",
				Body: []byte(`{"error":{"message":"This feature is only available for LiteLLM Enterprise users: tags","code":"403"}}`),
			},
			want: true,
		},
		{
			name: "403 but unrelated body",
			err: &litellm.APIError{
				StatusCode: 403, Code: "403",
				Body: []byte(`{"error":{"message":"forbidden: not an admin"}}`),
			},
			want: false,
		},
		{
			name: "enterprise wording but wrong status (500)",
			err: &litellm.APIError{
				StatusCode: 500, Code: "500",
				Body: []byte(`LiteLLM Enterprise`),
			},
			want: false,
		},
		{name: "nil error", err: nil, want: false},
		{name: "non-APIError", err: errors.New("dial tcp: connection refused"), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEnterpriseTagsRejection(tc.err); got != tc.want {
				t.Errorf("isEnterpriseTagsRejection() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClassifyLitellmErr asserts an upstream 4xx (APIError or Auth401Error)
// maps to 502 + litellm_rejected, while connectivity / 5xx keeps the 503
// litellm_unreachable mapping.
func TestClassifyLitellmErr(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantOutcome string
	}{
		{
			name:        "upstream 403",
			err:         &litellm.APIError{StatusCode: 403, Code: "403"},
			wantStatus:  http.StatusBadGateway,
			wantOutcome: audit.OutcomeLitellmRejected,
		},
		{
			name:        "upstream 422 validation",
			err:         &litellm.APIError{StatusCode: 422, Code: "422"},
			wantStatus:  http.StatusBadGateway,
			wantOutcome: audit.OutcomeLitellmRejected,
		},
		{
			name:        "auth 401",
			err:         &litellm.Auth401Error{Path: "/key/generate"},
			wantStatus:  http.StatusBadGateway,
			wantOutcome: audit.OutcomeLitellmRejected,
		},
		{
			name:        "transient 503",
			err:         &litellm.APIError{StatusCode: 503, Code: "503", Transient: true},
			wantStatus:  http.StatusServiceUnavailable,
			wantOutcome: audit.OutcomeLitellmUnreachable,
		},
		{
			name:        "connectivity error",
			err:         errors.New("dial tcp: connection refused"),
			wantStatus:  http.StatusServiceUnavailable,
			wantOutcome: audit.OutcomeLitellmUnreachable,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, oc, _ := classifyLitellmErr(tc.err)
			if st != tc.wantStatus || oc != tc.wantOutcome {
				t.Errorf("classifyLitellmErr() = (%d, %q), want (%d, %q)",
					st, oc, tc.wantStatus, tc.wantOutcome)
			}
		})
	}
}

// --- happy-path ek_ mint test (KeyAlias attribution) ---------------------

// captureLiteLLM is a fake litellm.Client for the ek_ create happy path. It
// embeds *litellm.NoopClient (which satisfies the full Client interface) and
// overrides only the three methods CreateHandler exercises: UserInfoByEmail
// (so the caller is a member of an authorized team), ListAllTeams (so the
// id→alias resolution in LookupCallerTeams AND the mintAndInsert shell-team
// lookup have data), and KeyGenerate (which captures the incoming request so
// the test can assert on KeyAlias/TeamID).
type captureLiteLLM struct {
	*litellm.NoopClient
	lastKeyGenerateReq *litellm.KeyGenerateRequest
	// teams overrides the default ListAllTeams fixture below when non-nil —
	// used by TestCreateEkRejectsWhenShellTeamMissing to omit the shell team.
	teams []litellm.TeamListEntry
}

func (c *captureLiteLLM) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	return &litellm.UserInfo{
		UserID:    "llu-" + email,
		UserEmail: email,
		Teams:     []string{"team-uuid-default"},
	}, nil
}

func (c *captureLiteLLM) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	if c.teams != nil {
		return c.teams, nil
	}
	// Default fixture: the caller's authorized "default" team plus the
	// "prod" environment's shell team (alias ach-env-prod per
	// litellm.ShellTeamAlias) — every existing test in this file uses
	// environment "prod", so mintAndInsert's shell-team lookup resolves.
	return []litellm.TeamListEntry{
		{TeamID: "team-uuid-default", TeamAlias: "default"},
		{TeamID: "t-shell", TeamAlias: "ach-env-prod"},
	}, nil
}

func (c *captureLiteLLM) KeyGenerate(ctx context.Context, req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	c.lastKeyGenerateReq = req
	resp, err := c.NoopClient.KeyGenerate(ctx, req)
	if resp != nil {
		// TESTING-PHASE (reverts FIX01 §A.6): production LiteLLM returns its
		// own minted virtual-key plaintext (sk-…) in the response; ACH now
		// persists it. The NoopClient echoes the (empty) req.Key, so override
		// with a sentinel the mint test can assert on.
		resp.Key = "sk-ek-xyz"
	}
	return resp, err
}

// fakeEnvStore returns a single ready environment whose authorizedTeams the
// captureLiteLLM caller is a member of.
type fakeEnvStore struct {
	env *db.EnvironmentRow
}

func (s *fakeEnvStore) GetEnvironment(_ context.Context, _ string) (*db.EnvironmentRow, error) {
	return s.env, nil
}

func (s *fakeEnvStore) AccessGroupSyncedFromRow(_ *db.EnvironmentRow) bool { return true }

// fakeEkDB records the inserted ek_ row and returns no error on insert.
// items overrides the default two-row ListKeys fixture when non-nil (the
// zero value keeps every pre-existing test's fixture unchanged).
type fakeEkDB struct {
	inserted     *db.EkInsertRow
	lastFilter   db.KeyListFilter
	items        []db.KeyListItem
	suspendCalls []string
	resumeCalls  []string
}

func (d *fakeEkDB) InsertEnvironmentKey(_ context.Context, row db.EkInsertRow) error {
	d.inserted = &row
	return nil
}
func (d *fakeEkDB) GetEnvironmentKey(context.Context, string) (*db.EkKeyInfo, error) {
	return nil, nil
}
func (d *fakeEkDB) RevokeEnvironmentKey(context.Context, string) (*db.EkKeyInfo, error) {
	return nil, nil
}
func (d *fakeEkDB) SuspendEnvironmentKey(_ context.Context, keyID string) (*db.EkKeyInfo, error) {
	d.suspendCalls = append(d.suspendCalls, keyID)
	return nil, nil
}
func (d *fakeEkDB) ResumeEnvironmentKey(_ context.Context, keyID string) (*db.EkKeyInfo, error) {
	d.resumeCalls = append(d.resumeCalls, keyID)
	return nil, nil
}
func (d *fakeEkDB) ListKeys(_ context.Context, f db.KeyListFilter, _ int, _ string) ([]db.KeyListItem, string, error) {
	d.lastFilter = f
	if d.items != nil {
		return d.items, "", nil
	}
	env := "demo"
	name := "laptop"
	return []db.KeyListItem{
		{KeyID: "pkid_x", Type: "pk", OwnerEmail: "user@example.com", Status: "active"},
		{KeyID: "ekid_y", Type: "ek", OwnerEmail: "user@example.com", Environment: &env, Name: &name, Status: "active"},
	}, "", nil
}

func (d *fakeEkDB) RevokePersonalKeyByOwner(_ context.Context, _ string, _ string) (*string, error) {
	return nil, nil
}

// TestCreateHandler_KeyAliasIsAchKeyID drives the §8.2 ek_ create happy path
// end-to-end and asserts the LiteLLM KeyGenerate request carried KeyAlias set
// to the minted ekid_ — i.e. KeyAlias != "" AND KeyAlias == ach_key_id
// metadata (debug attribution only, never used for lookup/routing).
func TestCreateHandler_KeyAliasIsAchKeyID(t *testing.T) {
	flm := &captureLiteLLM{NoopClient: &litellm.NoopClient{}}
	store := &fakeEnvStore{env: &db.EnvironmentRow{
		Namespace:       "ach",
		Name:            "prod",
		AuthorizedTeams: []string{"default"},
	}}
	deps := Deps{
		LiteLLM:          flm,
		DB:               &fakeEkDB{},
		Store:            store,
		Pepper:           []byte("test-pepper"),
		KeyEncryptionKey: keyEncTestDEK(),
		Audit:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace:        "ach",
		Issuer:           "https://ach.test",
	}

	body := strings.NewReader(`{"environment":"prod","name":"my-key"}`)
	req := httptest.NewRequest(http.MethodPost, "/platform/keys", body)
	// Authenticate as a pk_ caller (only pk_ may create ek_).
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_00000000000000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: "user@example.com",
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	CreateHandler(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("CreateHandler status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := flm.lastKeyGenerateReq
	if got == nil {
		t.Fatalf("KeyGenerate was never called")
	}
	// KeyAlias must equal the minted ekid_ carried in metadata.ach_key_id.
	if got.KeyAlias == "" || got.KeyAlias != got.Metadata["ach_key_id"] {
		t.Fatalf("KeyGenerate KeyAlias = %q, want it to equal metadata ach_key_id %q",
			got.KeyAlias, got.Metadata["ach_key_id"])
	}
	if got.Metadata["ach_issuer"] != "https://ach.test" {
		t.Fatalf("KeyGenerate metadata lacks this release's ach_issuer: %v", got.Metadata)
	}
	if !strings.HasPrefix(got.KeyAlias, keys.EkidKeyIDPrefix) {
		t.Fatalf("KeyGenerate KeyAlias = %q, want %s prefix", got.KeyAlias, keys.EkidKeyIDPrefix)
	}
	// G3: the inserted ek_ row must carry the LiteLLM virtual-key material
	// ENCRYPTED at rest (keycrypt blob), never the sk-… plaintext.
	inserted := deps.DB.(*fakeEkDB).inserted
	if inserted == nil {
		t.Fatalf("InsertEnvironmentKey was never called")
	}
	if inserted.LiteLLMKeyMaterial == nil {
		t.Fatal("EkInsertRow.LiteLLMKeyMaterial is nil; want sealed key material")
	}
	if *inserted.LiteLLMKeyMaterial == "sk-ek-xyz" {
		t.Fatal("EkInsertRow.LiteLLMKeyMaterial stored in PLAINTEXT — must be encrypted (G3)")
	}
	pt, err := keycrypt.Open(keyEncTestDEK(), *inserted.LiteLLMKeyMaterial)
	if err != nil || string(pt) != "sk-ek-xyz" {
		t.Fatalf("sealed material did not open to sk-ek-xyz: %v / %q", err, pt)
	}
}

// TestCreateEkMintsIntoShellTeam is the whole point of the shell-team change:
// the ek_ must be capped by team_id and must NOT carry any access-group field
// (a key in both a team and a group triggers LiteLLM's agent-collapse bug).
func TestCreateEkMintsIntoShellTeam(t *testing.T) {
	flm := &captureLiteLLM{NoopClient: &litellm.NoopClient{}}
	store := &fakeEnvStore{env: &db.EnvironmentRow{
		Namespace:       "ach",
		Name:            "prod",
		AuthorizedTeams: []string{"default"},
	}}
	deps := Deps{
		LiteLLM:          flm,
		DB:               &fakeEkDB{},
		Store:            store,
		Pepper:           []byte("test-pepper"),
		KeyEncryptionKey: keyEncTestDEK(),
		Audit:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace:        "ach",
	}

	body := strings.NewReader(`{"environment":"prod","name":"my-key"}`)
	req := httptest.NewRequest(http.MethodPost, "/platform/keys", body)
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_00000000000000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: "user@example.com",
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	CreateHandler(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("CreateHandler status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := flm.lastKeyGenerateReq
	if got == nil {
		t.Fatal("KeyGenerate was never called")
	}
	if got.TeamID != "t-shell" {
		t.Fatalf("TeamID = %q, want t-shell (the environment's shell team)", got.TeamID)
	}
	if len(got.Models) != 0 {
		t.Fatalf("Models = %v, want none — the team is the ceiling", got.Models)
	}
}

// TestCreateEkRejectsWhenShellTeamMissing: no shell team ⇒ the environment is
// not fully provisioned, and minting would produce a fail-open key.
func TestCreateEkRejectsWhenShellTeamMissing(t *testing.T) {
	flm := &captureLiteLLM{
		NoopClient: &litellm.NoopClient{},
		// Only the caller's authorized team — no ach-env-prod entry.
		teams: []litellm.TeamListEntry{{TeamID: "team-uuid-default", TeamAlias: "default"}},
	}
	store := &fakeEnvStore{env: &db.EnvironmentRow{
		Namespace:       "ach",
		Name:            "prod",
		AuthorizedTeams: []string{"default"},
	}}
	deps := Deps{
		LiteLLM:          flm,
		DB:               &fakeEkDB{},
		Store:            store,
		Pepper:           []byte("test-pepper"),
		KeyEncryptionKey: keyEncTestDEK(),
		Audit:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace:        "ach",
	}

	body := strings.NewReader(`{"environment":"prod","name":"my-key"}`)
	req := httptest.NewRequest(http.MethodPost, "/platform/keys", body)
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_00000000000000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: "user@example.com",
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	CreateHandler(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("CreateHandler status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_ready"`) {
		t.Fatalf("body missing not_ready code: %s", rec.Body.String())
	}
	if flm.lastKeyGenerateReq != nil {
		t.Fatal("KeyGenerate must not be called when the shell team is missing")
	}
}

// --- first-time provision test (user_id=email + no auto key) -------------

// firstTimeLiteLLM drives the env-key create path through provisionUser's
// first-time (UserNew) branch. UserInfoByEmail is stateful: the first call
// (LookupCallerTeams, §8.2 step 5) returns an authorized user so the
// team-intersection passes; the second call (provisionUser, step 6)
// false-negatives (nil) — the LiteLLM v1.83 #36 broken email lookup — which
// forces the UserNew branch. UserNew records the request so the test can
// assert user_id=email + auto_create_key=false.
type firstTimeLiteLLM struct {
	*litellm.NoopClient
	userInfoCalls      int
	lastUserNewReq     *litellm.UserNewRequest
	userNewErr         error // if set, UserNew returns it (after recording the req)
	lastKeyGenerateReq *litellm.KeyGenerateRequest
}

func (c *firstTimeLiteLLM) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	c.userInfoCalls++
	if c.userInfoCalls == 1 {
		// Auth lookup: caller is a member of the default team.
		return &litellm.UserInfo{UserID: "llu-" + email, UserEmail: email, Teams: []string{"team-uuid-default"}}, nil
	}
	// provisionUser targeted lookup false-negatives → first-time branch.
	return nil, nil
}

func (c *firstTimeLiteLLM) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	// Includes the "prod" shell team (both tests using this fake create
	// against environment "prod") so mintAndInsert's shell-team lookup
	// resolves and the create still succeeds.
	return []litellm.TeamListEntry{
		{TeamID: "team-uuid-default", TeamAlias: "default"},
		{TeamID: "t-shell", TeamAlias: "ach-env-prod"},
	}, nil
}

func (c *firstTimeLiteLLM) UserNew(_ context.Context, req *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	c.lastUserNewReq = req
	if c.userNewErr != nil {
		return nil, c.userNewErr
	}
	return &litellm.UserInfo{UserID: req.UserID, UserEmail: req.UserEmail}, nil
}

func (c *firstTimeLiteLLM) KeyGenerate(ctx context.Context, req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	c.lastKeyGenerateReq = req
	return c.NoopClient.KeyGenerate(ctx, req)
}

// TestCreateHandler_FirstTimeUser_UserIDEmailAndNoAutoKey asserts the
// env-key create path provisions a brand-new LiteLLM user with a
// deterministic user_id=email and auto_create_key=false (regression guard
// for the 2026-06-04 prod finding: no untracked default key leaked).
func TestCreateHandler_FirstTimeUser_UserIDEmailAndNoAutoKey(t *testing.T) {
	flm := &firstTimeLiteLLM{NoopClient: &litellm.NoopClient{}}
	store := &fakeEnvStore{env: &db.EnvironmentRow{
		Namespace:       "ach",
		Name:            "prod",
		AuthorizedTeams: []string{"default"},
	}}
	deps := Deps{
		LiteLLM:          flm,
		DB:               &fakeEkDB{},
		Store:            store,
		Pepper:           []byte("test-pepper"),
		KeyEncryptionKey: keyEncTestDEK(),
		Audit:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace:        "ach",
	}

	body := strings.NewReader(`{"environment":"prod","name":"my-key"}`)
	req := httptest.NewRequest(http.MethodPost, "/platform/keys", body)
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_00000000000000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: "user@example.com",
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	CreateHandler(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("CreateHandler status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	r := flm.lastUserNewReq
	if r == nil {
		t.Fatal("UserNew was not called")
	}
	if r.UserID != "user@example.com" {
		t.Errorf("UserNew user_id: got %q, want %q (deterministic email id)", r.UserID, "user@example.com")
	}
	if r.AutoCreateKey == nil || *r.AutoCreateKey != false {
		t.Errorf("UserNew auto_create_key: got %v, want explicit false", r.AutoCreateKey)
	}
}

// TestCreateHandler_FirstTimeUser_DuplicateUserRecovers: with deterministic
// user_id=email, the provisionUser probe false-negatives (LiteLLM #36) → UserNew
// collides (409) for an already-existing user. Recovery: treat email as the id
// and continue. The create must SUCCEED (200 + ek_ minted with UserID=email),
// not 500.
func TestCreateHandler_FirstTimeUser_DuplicateUserRecovers(t *testing.T) {
	flm := &firstTimeLiteLLM{
		NoopClient: &litellm.NoopClient{},
		userNewErr: &litellm.APIError{ // captured prod signature (Task 4)
			Method:     "POST",
			Path:       "/user/new",
			StatusCode: 409,
			Code:       "409",
			Body:       []byte(`{"error":{"message":"User with id user@example.com already exists","code":"409"}}`),
		},
	}
	store := &fakeEnvStore{env: &db.EnvironmentRow{
		Namespace:       "ach",
		Name:            "prod",
		AuthorizedTeams: []string{"default"},
	}}
	deps := Deps{
		LiteLLM:          flm,
		DB:               &fakeEkDB{},
		Store:            store,
		Pepper:           []byte("test-pepper"),
		KeyEncryptionKey: keyEncTestDEK(),
		Audit:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace:        "ach",
	}

	body := strings.NewReader(`{"environment":"prod","name":"my-key"}`)
	req := httptest.NewRequest(http.MethodPost, "/platform/keys", body)
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_00000000000000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: "user@example.com",
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	CreateHandler(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("CreateHandler status = %d, want 200 (duplicate must recover); body=%s", rec.Code, rec.Body.String())
	}
	if flm.lastKeyGenerateReq == nil {
		t.Fatal("KeyGenerate was never called")
	}
	if flm.lastKeyGenerateReq.UserID != "user@example.com" {
		t.Errorf("KeyGenerate user_id: got %q, want %q", flm.lastKeyGenerateReq.UserID, "user@example.com")
	}
}

// TestListAllHandler_CallerScopedAndDefaults asserts ListAllHandler
// forces owner_email to the authenticated caller and honours the ?status filter.
func TestListAllHandler_CallerScopedAndDefaults(t *testing.T) {
	fdb := &fakeEkDB{}
	// The default fixture's ekid_y sits in environment "demo"; grant the
	// caller access so EffectiveState's derivation leaves it "active"
	// (this test predates D-30 access derivation and is not itself testing it).
	store := &fakeEnvStore{env: &db.EnvironmentRow{Name: "demo", AuthorizedTeams: []string{"team-a"}}}
	fll := &listTeamsLiteLLM{
		NoopClient: &litellm.NoopClient{},
		teams:      map[string][]string{"user@example.com": {"team-a"}},
	}
	deps := Deps{
		DB:      fdb,
		Store:   store,
		LiteLLM: fll,
		Audit:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest(http.MethodGet, "/platform/keys?status=active", nil)
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_00000000000000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: "user@example.com",
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	ListAllHandler(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fdb.lastFilter.OwnerEmail == nil || *fdb.lastFilter.OwnerEmail != "user@example.com" {
		t.Errorf("owner filter not forced to caller; got %+v", fdb.lastFilter.OwnerEmail)
	}
	if fdb.lastFilter.Status != "active" {
		t.Errorf("status filter=%q; want active", fdb.lastFilter.Status)
	}
	if !strings.Contains(rec.Body.String(), `"pkid_x"`) || !strings.Contains(rec.Body.String(), `"ekid_y"`) {
		t.Errorf("body missing merged items: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "credential_hash") || strings.Contains(rec.Body.String(), "litellm") {
		t.Errorf("response leaked secret material: %s", rec.Body.String())
	}
}

// listTeamsLiteLLM is a controllable fake litellm.Client for
// ListAllHandler's accessByEnvironment path (LookupCallerTeams). teams maps
// an owner email to their raw LiteLLM team ids, matched directly against a
// fakeEnvStore's AuthorizedTeams (no alias-resolution ListAllTeams data
// needed for these tests). callCount records how many times
// UserInfoByEmail ran — D-30 requires at most one lookup per list call. err,
// when set, is returned unconditionally (simulates a LiteLLM outage).
type listTeamsLiteLLM struct {
	*litellm.NoopClient
	teams     map[string][]string
	callCount int
	err       error
}

func (c *listTeamsLiteLLM) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	c.callCount++
	if c.err != nil {
		return nil, c.err
	}
	return &litellm.UserInfo{UserID: "llu-" + email, UserEmail: email, Teams: c.teams[email]}, nil
}

func (c *listTeamsLiteLLM) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	return nil, nil
}

// newCreateDeps builds a Deps wired for a successful CreateHandler call
// against environment "prod" (matches captureLiteLLM's default ListAllTeams
// fixture: caller team "default" + shell team "ach-env-prod").
func newCreateDeps(fdb dbOps) (Deps, *captureLiteLLM) {
	flm := &captureLiteLLM{NoopClient: &litellm.NoopClient{}}
	store := &fakeEnvStore{env: &db.EnvironmentRow{
		Namespace:       "ach",
		Name:            "prod",
		AuthorizedTeams: []string{"default"},
	}}
	return Deps{
		LiteLLM:          flm,
		DB:               fdb,
		Store:            store,
		Pepper:           []byte("test-pepper"),
		KeyEncryptionKey: keyEncTestDEK(),
		Audit:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace:        "ach",
	}, flm
}

func doCreate(deps Deps, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/platform/keys", strings.NewReader(body))
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_00000000000000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: "user@example.com",
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	CreateHandler(deps).ServeHTTP(rec, req)
	return rec
}

func TestCreate_ExpiresAtValidatedAndStored(t *testing.T) {
	t.Run("past expires_at -> 400 invalid_argument", func(t *testing.T) {
		fdb := &fakeEkDB{}
		deps, _ := newCreateDeps(fdb)

		rec := doCreate(deps, `{"environment":"prod","name":"n","expires_at":"2020-01-01T00:00:00Z"}`)

		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), codeInvalidArgument) {
			t.Fatalf("status/body = %d/%s, want 400 invalid_argument", rec.Code, rec.Body.String())
		}
		if fdb.inserted != nil {
			t.Error("InsertEnvironmentKey must not be called on a rejected create")
		}
	})

	t.Run("future expires_at -> 200, stored and echoed", func(t *testing.T) {
		fdb := &fakeEkDB{}
		deps, flm := newCreateDeps(fdb)
		future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

		rec := doCreate(deps, `{"environment":"prod","name":"n","expires_at":"`+future+`"}`)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if fdb.inserted == nil || fdb.inserted.ExpiresAt == nil {
			t.Fatal("InsertEnvironmentKey row missing ExpiresAt")
		}
		wantTs, _ := time.Parse(time.RFC3339, future)
		if !fdb.inserted.ExpiresAt.Equal(wantTs) {
			t.Errorf("inserted ExpiresAt = %v, want %v", fdb.inserted.ExpiresAt, wantTs)
		}
		if flm.lastKeyGenerateReq == nil || flm.lastKeyGenerateReq.Duration != "" {
			t.Errorf("KeyGenerateRequest.Duration = %q, want unset (D-24: ACH enforces expiry, never LiteLLM)",
				flm.lastKeyGenerateReq.Duration)
		}
		var resp CreateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.ExpiresAt == nil || *resp.ExpiresAt != future {
			t.Errorf("response expires_at = %v, want %q", resp.ExpiresAt, future)
		}
	})

	t.Run("no expires_at -> perpetual", func(t *testing.T) {
		fdb := &fakeEkDB{}
		deps, _ := newCreateDeps(fdb)

		rec := doCreate(deps, `{"environment":"prod","name":"n"}`)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if fdb.inserted == nil || fdb.inserted.ExpiresAt != nil {
			t.Errorf("inserted ExpiresAt = %v, want nil (perpetual)", fdb.inserted.ExpiresAt)
		}
		var resp CreateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.ExpiresAt != nil {
			t.Errorf("response expires_at = %v, want null", *resp.ExpiresAt)
		}
	})
}

func doList(deps Deps, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/platform/keys"+query, nil)
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_00000000000000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: "u@x.com",
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	ListAllHandler(deps).ServeHTTP(rec, req)
	return rec
}

// TestListAll_DerivesInvalidOncePerOwner: an active ek_ whose owner no
// longer intersects the Environment's authorizedTeams derives 'invalid';
// a suspended ek_ in the same environment stays 'suspended' with an added
// 'no_access' reason; a pk_ is never touched by the derivation. Exactly one
// UserInfoByEmail call covers every row (D-30 — one lookup per owner).
func TestListAll_DerivesInvalidOncePerOwner(t *testing.T) {
	env := "demo"
	nameEk1, nameEk2 := "laptop", "server"
	items := []db.KeyListItem{
		{KeyID: "ekid_1", Type: "ek", OwnerEmail: "u@x.com", Environment: &env, Name: &nameEk1, Status: "active"},
		{KeyID: "ekid_2", Type: "ek", OwnerEmail: "u@x.com", Environment: &env, Name: &nameEk2, Status: "suspended"},
		{KeyID: "pkid_3", Type: "pk", OwnerEmail: "u@x.com", Status: "active"},
	}
	fdb := &fakeEkDB{items: items}
	store := &fakeEnvStore{env: &db.EnvironmentRow{Name: "demo", AuthorizedTeams: []string{"team-a"}}}
	fll := &listTeamsLiteLLM{
		NoopClient: &litellm.NoopClient{},
		teams:      map[string][]string{"u@x.com": {"team-b"}}, // no intersection with team-a
	}
	deps := Deps{
		DB: fdb, Store: store, LiteLLM: fll,
		Audit: slog.New(slog.NewTextHandler(io.Discard, nil)), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	rec := doList(deps, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fll.callCount != 1 {
		t.Errorf("UserInfoByEmail calls = %d, want 1 (once per owner)", fll.callCount)
	}
	var resp struct {
		Items []struct {
			KeyID   string   `json:"key_id"`
			Status  string   `json:"status"`
			Reasons []string `json:"reasons"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	byID := map[string]struct {
		Status  string
		Reasons []string
	}{}
	for _, it := range resp.Items {
		byID[it.KeyID] = struct {
			Status  string
			Reasons []string
		}{it.Status, it.Reasons}
	}
	if got := byID["ekid_1"]; got.Status != "invalid" || !reflect.DeepEqual(got.Reasons, []string{"no_access"}) {
		t.Errorf("ekid_1 = %+v, want status=invalid reasons=[no_access]", got)
	}
	if got := byID["ekid_2"]; got.Status != "suspended" || !reflect.DeepEqual(got.Reasons, []string{"suspended", "no_access"}) {
		t.Errorf("ekid_2 = %+v, want status=suspended reasons=[suspended no_access]", got)
	}
	if got := byID["pkid_3"]; got.Status != "active" {
		t.Errorf("pkid_3 = %+v, want status=active", got)
	}
}

// TestListAll_TeamsLookupFailureIsUnverifiedNotInvalid (§7.2): a transient
// TeamsResolver failure must never write a derived state — the row stays
// its persisted status with an access_unverified reason, not 'invalid'.
func TestListAll_TeamsLookupFailureIsUnverifiedNotInvalid(t *testing.T) {
	env := "demo"
	name := "laptop"
	items := []db.KeyListItem{
		{KeyID: "ekid_1", Type: "ek", OwnerEmail: "u@x.com", Environment: &env, Name: &name, Status: "active"},
	}
	fdb := &fakeEkDB{items: items}
	store := &fakeEnvStore{env: &db.EnvironmentRow{Name: "demo", AuthorizedTeams: []string{"team-a"}}}
	fll := &listTeamsLiteLLM{NoopClient: &litellm.NoopClient{}, err: errors.New("dial tcp: connection refused")}
	deps := Deps{
		DB: fdb, Store: store, LiteLLM: fll,
		Audit: slog.New(slog.NewTextHandler(io.Discard, nil)), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	rec := doList(deps, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			Status  string   `json:"status"`
			Reasons []string `json:"reasons"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	if resp.Items[0].Status != "active" || !reflect.DeepEqual(resp.Items[0].Reasons, []string{"access_unverified"}) {
		t.Errorf("item = %+v, want status=active reasons=[access_unverified]", resp.Items[0])
	}
}

// TestListAll_StatusFilterInvalid: ?status=invalid returns only the
// derived-invalid rows — filtered client-side after EffectiveState runs,
// since 'invalid' is not a persisted SQL status.
func TestListAll_StatusFilterInvalid(t *testing.T) {
	env := "demo"
	name1, name2 := "laptop", "phone"
	items := []db.KeyListItem{
		{KeyID: "ekid_1", Type: "ek", OwnerEmail: "u@x.com", Environment: &env, Name: &name1, Status: "active"},
		{KeyID: "ekid_2", Type: "ek", OwnerEmail: "u@x.com", Environment: &env, Name: &name2, Status: "suspended"},
	}
	fdb := &fakeEkDB{items: items}
	store := &fakeEnvStore{env: &db.EnvironmentRow{Name: "demo", AuthorizedTeams: []string{"team-a"}}}
	fll := &listTeamsLiteLLM{
		NoopClient: &litellm.NoopClient{},
		teams:      map[string][]string{"u@x.com": {"team-z"}}, // no access at all
	}
	deps := Deps{
		DB: fdb, Store: store, LiteLLM: fll,
		Audit: slog.New(slog.NewTextHandler(io.Discard, nil)), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	rec := doList(deps, "?status=invalid")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// The SQL-level filter must NOT have narrowed the query (there is no
	// persisted 'invalid' status) — the derivation runs against everything.
	if fdb.lastFilter.Status != "" {
		t.Errorf("SQL status filter = %q, want empty (invalid is derived post-query)", fdb.lastFilter.Status)
	}
	var resp struct {
		Items []struct {
			KeyID  string `json:"key_id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].KeyID != "ekid_1" || resp.Items[0].Status != "invalid" {
		t.Errorf("items = %+v, want exactly [ekid_1 invalid]", resp.Items)
	}
}

// multiEnvStore resolves a distinct db.EnvironmentRow per name — unlike
// fakeEnvStore (one fixed env for every lookup), needed to exercise access
// derivation differing across environments within a single list call.
type multiEnvStore struct {
	envs map[string]*db.EnvironmentRow
}

func (s *multiEnvStore) GetEnvironment(_ context.Context, name string) (*db.EnvironmentRow, error) {
	return s.envs[name], nil
}

func (s *multiEnvStore) AccessGroupSyncedFromRow(_ *db.EnvironmentRow) bool { return true }

// TestListAll_StatusFilterIsOnEffectiveState: the wire contract is "?status=
// filters on the EFFECTIVE state" for all five values, not just 'invalid'.
// A persisted-active row whose owner lost Environment access derives
// 'invalid' — ?status=active must exclude it (the SQL-level pre-filter
// alone lets it through, since it is still persisted 'active'), and
// ?status=invalid must return only it.
func TestListAll_StatusFilterIsOnEffectiveState(t *testing.T) {
	nameOK, nameLost := "laptop", "phone"
	envOK, envLost := "demo", "lost"
	items := []db.KeyListItem{
		{KeyID: "ekid_ok", Type: "ek", OwnerEmail: "u@x.com", Environment: &envOK, Name: &nameOK, Status: "active"},
		{KeyID: "ekid_invalid", Type: "ek", OwnerEmail: "u@x.com", Environment: &envLost, Name: &nameLost, Status: "active"},
	}
	store := &multiEnvStore{envs: map[string]*db.EnvironmentRow{
		"demo": {Name: "demo", AuthorizedTeams: []string{"team-a"}},
		"lost": {Name: "lost", AuthorizedTeams: []string{"team-b"}},
	}}
	fll := &listTeamsLiteLLM{
		NoopClient: &litellm.NoopClient{},
		teams:      map[string][]string{"u@x.com": {"team-a"}}, // member of team-a only
	}
	newDeps := func() (Deps, *fakeEkDB) {
		fdb := &fakeEkDB{items: items}
		return Deps{
			DB: fdb, Store: store, LiteLLM: fll,
			Audit: slog.New(slog.NewTextHandler(io.Discard, nil)), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}, fdb
	}

	t.Run("?status=active excludes the derived-invalid row", func(t *testing.T) {
		deps, _ := newDeps()
		rec := doList(deps, "?status=active")

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []struct {
				KeyID  string `json:"key_id"`
				Status string `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Items) != 1 || resp.Items[0].KeyID != "ekid_ok" || resp.Items[0].Status != "active" {
			t.Errorf("items = %+v, want exactly [ekid_ok active]", resp.Items)
		}
	})

	t.Run("?status=invalid returns only it", func(t *testing.T) {
		deps, _ := newDeps()
		rec := doList(deps, "?status=invalid")

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []struct {
				KeyID  string `json:"key_id"`
				Status string `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Items) != 1 || resp.Items[0].KeyID != "ekid_invalid" || resp.Items[0].Status != "invalid" {
			t.Errorf("items = %+v, want exactly [ekid_invalid invalid]", resp.Items)
		}
	})
}
