//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Shared test helpers for the admin package integration suite.
// Defines the infra plumbing (fakes, router builders, request helpers)
// consumed by handler_integration_test.go.

package admin

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/redis/go-redis/v9"
)

// testNs is the deployment namespace injected into admin Deps for tests.
const testNs = "ach-test"

// adminAllowlist returns a single-entry allowlist containing the email
// injected by newAdminRouter as the caller identity.
func adminAllowlist() map[string]struct{} {
	return map[string]struct{}{
		"admin@x.example": {},
	}
}

// newAdminRouter builds a chi.Router with admin.Mount(deps) wired under
// /platform/admin, injecting an admin pk_ KeyContext so AdminOnly passes.
// The caller identity is always "admin@x.example" (must be in deps.Allowlist).
func newAdminRouter(t *testing.T, deps Deps) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/platform/admin", func(r chi.Router) {
		// Inject admin identity upstream of AdminOnly middleware.
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := middleware.WithRequestID(r.Context(), "test-req-id")
				info := &keystore.KeyInfo{
					KeyID:      "pkid_admin_test",
					KeyType:    keys.PrefixPk,
					OwnerEmail: "admin@x.example",
				}
				ctx = middleware.WithKeyContext(ctx, info, true)
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		})
		Mount(deps)(r)
	})
	return r
}

// adminPostJSON sends a POST request with body (may be nil) to the given
// path on the provided router.
func adminPostJSON(t *testing.T, router http.Handler, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(http.MethodPost, path, bodyReader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// adminGetRequest sends a GET request to path on the provided router.
func adminGetRequest(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// discardLogger returns a slog.Logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// =========================== fakes ===========================

// recorderOrder records the sequence of side-effect steps so tests can
// assert ordering (e.g., LiteLLM-first on ek_ revoke).
type recorderOrder struct {
	steps []string
}

func (o *recorderOrder) record(step string) {
	if o != nil {
		o.steps = append(o.steps, step)
	}
}

// fakeLitellm implements litellm.Client, recording revoke calls and
// optionally returning a configured error.
type fakeLitellm struct {
	revokeCalled atomic.Int64
	revokedKeys  []string
	revokeErr    error
	order        *recorderOrder
}

func (f *fakeLitellm) RevokeKey(_ context.Context, keyID string) error {
	f.revokeCalled.Add(1)
	f.revokedKeys = append(f.revokedKeys, keyID)
	if f.order != nil {
		f.order.record("litellm")
	}
	return f.revokeErr
}

// Satisfy the full litellm.Client interface with no-ops.
func (f *fakeLitellm) DeleteAccessGroup(_ context.Context, _ string) error { return nil }
func (f *fakeLitellm) DeleteTag(_ context.Context, _ string) error         { return nil }
func (f *fakeLitellm) ListModels(_ context.Context) ([]litellm.ModelInfoResponse, error) {
	return nil, nil
}
func (f *fakeLitellm) ListMCPServers(_ context.Context) ([]litellm.MCPServerEntry, error) {
	return nil, nil
}
func (f *fakeLitellm) ListA2AAgents(_ context.Context) ([]litellm.AgentEntry, error) {
	return nil, nil
}
func (f *fakeLitellm) ListUserKeys(_ context.Context, _ string) ([]litellm.UserKeyInfo, error) {
	return nil, nil
}
func (f *fakeLitellm) UserNew(_ context.Context, _ *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	return nil, nil
}
func (f *fakeLitellm) UserInfoByEmail(_ context.Context, _ string) (*litellm.UserInfo, error) {
	return nil, nil
}
func (f *fakeLitellm) TeamMemberAdd(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeLitellm) ListTeamsByAlias(_ context.Context, _ string) ([]litellm.TeamListEntry, error) {
	return nil, nil
}
func (f *fakeLitellm) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	return nil, nil
}
func (f *fakeLitellm) KeyGenerate(_ context.Context, _ *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	return nil, nil
}
func (f *fakeLitellm) CreateAccessGroup(_ context.Context, _ litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLitellm) GetAccessGroupByName(_ context.Context, _ string) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLitellm) UpdateAccessGroup(_ context.Context, _ string, _ litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLitellm) DeleteAccessGroupByID(_ context.Context, _ string) error { return nil }
func (f *fakeLitellm) EnsureDefaultTeam(_ context.Context) error               { return nil }

// recordingRedis implements redisDeleter, recording Del calls.
type recordingRedis struct {
	delCalled []string
	order     *recorderOrder
}

func (r *recordingRedis) Del(_ context.Context, ks ...string) *redis.IntCmd {
	r.delCalled = append(r.delCalled, ks...)
	if r.order != nil {
		r.order.record("redis-del")
	}
	cmd := redis.NewIntCmd(context.Background())
	cmd.SetVal(int64(len(ks)))
	return cmd
}

