// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
// id→alias resolution in LookupCallerTeams has data), and KeyGenerate (which
// captures the incoming request so the test can assert on KeyAlias).
type captureLiteLLM struct {
	*litellm.NoopClient
	lastKeyGenerateReq *litellm.KeyGenerateRequest
}

func (c *captureLiteLLM) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	return &litellm.UserInfo{
		UserID:    "llu-" + email,
		UserEmail: email,
		Teams:     []string{"team-uuid-default"},
	}, nil
}

func (c *captureLiteLLM) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	return []litellm.TeamListEntry{{TeamID: "team-uuid-default", TeamAlias: "default"}}, nil
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
type fakeEkDB struct {
	inserted *db.EkInsertRow
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
func (d *fakeEkDB) ListEnvironmentKeysByOwner(context.Context, string, int, string) ([]db.EkKeyInfo, string, error) {
	return nil, "", nil
}
func (d *fakeEkDB) ListEnvironmentKeysByOwnerWithFilter(context.Context, *string, int, string) ([]db.EkKeyInfo, string, error) {
	return nil, "", nil
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
	}

	body := strings.NewReader(`{"environment":"prod","name":"my-key"}`)
	req := httptest.NewRequest(http.MethodPost, "/platform/env-keys", body)
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
	return []litellm.TeamListEntry{{TeamID: "team-uuid-default", TeamAlias: "default"}}, nil
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
	req := httptest.NewRequest(http.MethodPost, "/platform/env-keys", body)
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
	req := httptest.NewRequest(http.MethodPost, "/platform/env-keys", body)
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
