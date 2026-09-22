// SPDX-License-Identifier: Apache-2.0

package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keycrypt"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/observability"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

var testDEK = []byte("0123456789abcdef0123456789abcdef")

type fakeCatalog struct {
	models []litellm.ModelGroupInfo
	mcp    []litellm.MCPServerEntry
	a2a    []litellm.AgentEntry
	err    error

	// Task 4 fields — zero-valued for every pre-existing capabilities test.
	// DailyActivity is called twice per stats request (current window,
	// then prior window); dailyCalls picks which of daily/prior to answer.
	daily          observability.DailyActivity
	dailyErr       error
	prior          observability.DailyActivity
	priorErr       error
	dailyCalls     int
	spendRows      []observability.SpendLogRow
	spendTruncated bool
	spendErr       error
	lastSpendStart string
	lastSpendEnd   string
	lastSpendMax   int
	userInfo       observability.UserInfo
	userInfoErr    error
	memberBudget   *observability.MemberBudget
	memberErr      error
}

func (f *fakeCatalog) ListModelGroups(context.Context) ([]litellm.ModelGroupInfo, error) {
	return f.models, f.err
}
func (f *fakeCatalog) ListMCPServers(context.Context) ([]litellm.MCPServerEntry, error) {
	return f.mcp, f.err
}
func (f *fakeCatalog) ListA2AAgents(context.Context) ([]litellm.AgentEntry, error) {
	return f.a2a, f.err
}
func (f *fakeCatalog) DailyActivity(context.Context, string, string) (observability.DailyActivity, error) {
	f.dailyCalls++
	if f.dailyCalls == 1 {
		return f.daily, f.dailyErr
	}
	return f.prior, f.priorErr
}
func (f *fakeCatalog) SpendLogsV2(_ context.Context, start, end string, maxPages int) ([]observability.SpendLogRow, bool, error) {
	f.lastSpendStart, f.lastSpendEnd, f.lastSpendMax = start, end, maxPages
	return f.spendRows, f.spendTruncated, f.spendErr
}
func (f *fakeCatalog) UserInfo(context.Context) (observability.UserInfo, error) {
	return f.userInfo, f.userInfoErr
}
func (f *fakeCatalog) TeamMemberBudget(context.Context, string, string) (*observability.MemberBudget, error) {
	return f.memberBudget, f.memberErr
}

type fakeStore map[string]*db.EnvironmentRow

func (s fakeStore) GetEnvironment(_ context.Context, name string) (*db.EnvironmentRow, error) {
	return s[name], nil
}

// fakeLL: only the two methods LookupCallerTeams calls; the embedded nil
// interface panics on anything else, which is the point.
type fakeLL struct {
	litellm.Client
	teams map[string][]string
	err   error
}

func (f *fakeLL) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &litellm.UserInfo{UserID: email, Teams: f.teams[email]}, nil
}
func (f *fakeLL) ListAllTeams(context.Context) ([]litellm.TeamListEntry, error) {
	return []litellm.TeamListEntry{{TeamID: "team-a", TeamAlias: "team-a"}}, nil
}

func testDeps(t *testing.T) Deps {
	t.Helper()
	return Deps{
		Store:            fakeStore{"demo": {Name: "demo", AuthorizedTeams: []string{"team-a"}, RuntimeModels: []string{"demo-model"}}},
		LiteLLM:          &fakeLL{teams: map[string][]string{"u@x.com": {"team-a"}, "v@x.com": {"team-z"}}},
		AsUser:           func(string) UserReads { return &fakeCatalog{} },
		KeyEncryptionKey: testDEK,
		Audit:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func pkCtx(t *testing.T, email string, admin bool) context.Context {
	t.Helper()
	sealed, err := keycrypt.Seal(testDEK, []byte("sk-user"))
	if err != nil {
		t.Fatal(err)
	}
	return middleware.WithKeyContext(context.Background(),
		&keystore.KeyInfo{KeyID: "pkid_1", KeyType: keys.PrefixPk, OwnerEmail: email, LiteLLMKeyMaterial: &sealed}, admin)
}

func ekCtx() context.Context {
	return middleware.WithKeyContext(context.Background(),
		&keystore.KeyInfo{KeyID: "ekid_1", KeyType: keys.PrefixEk, OwnerEmail: "u@x.com", Environment: "demo"}, false)
}

func do(t *testing.T, d Deps, path string, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	Mount(r, d)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	return rec
}

func TestBootstrap(t *testing.T) {
	d := testDeps(t)
	d.OpenWorkEnabled = true
	rec := do(t, d, "/platform/console/bootstrap", pkCtx(t, "u@x.com", true))
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got["email"] != "u@x.com" || got["is_admin"] != true || got["openwork_enabled"] != true || got["suspend_propagation_seconds"] != float64(60) {
		t.Fatalf("%d %v", rec.Code, got)
	}
}

func TestCapabilities_PersonalUsesTheUsersOwnKey(t *testing.T) {
	d := testDeps(t)
	mode := "chat"
	cat := &fakeCatalog{
		models: []litellm.ModelGroupInfo{{Name: "__deny_all__", Providers: []string{}}, {Name: "demo-model", Providers: []string{"openai"}, Mode: &mode}},
		mcp:    []litellm.MCPServerEntry{{ServerID: "s1", ServerName: "demo-mcp", Transport: "http", AllowedTools: []string{"a"}}},
		a2a:    []litellm.AgentEntry{{AgentID: "a1", AgentName: "demo-agent", AgentCardParams: map[string]any{"url": "http://x", "skills": []any{map[string]any{"name": "s"}}, "capabilities": map[string]any{"streaming": true}}}},
	}
	var usedKey string
	d.AsUser = func(key string) UserReads { usedKey = key; return cat }
	rec := do(t, d, "/platform/console/capabilities?scope=personal", pkCtx(t, "u@x.com", false))
	if rec.Code != 200 || usedKey != "sk-user" {
		t.Fatalf("%d key=%q %s", rec.Code, usedKey, rec.Body)
	}
	var got struct {
		Scope        string           `json:"scope"`
		Models       []map[string]any `json:"models"`
		MCP          []map[string]any `json:"mcp_servers"`
		A2A          []map[string]any `json:"a2a_agents"`
		Provisioning bool             `json:"provisioning"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Scope != scopePersonal || len(got.Models) != 1 || got.Models[0]["name"] != "demo-model" || got.Provisioning {
		t.Fatalf("%+v (deny-all sentinel must be stripped)", got)
	}
	if len(got.MCP) != 1 || got.MCP[0]["name"] != "demo-mcp" || got.MCP[0]["tool_count"] != float64(1) {
		t.Fatalf("mcp: %+v", got.MCP)
	}
	if len(got.A2A) != 1 || got.A2A[0]["name"] != "demo-agent" || got.A2A[0]["streaming"] != true || got.A2A[0]["skill_count"] != float64(1) {
		t.Fatalf("a2a: %+v", got.A2A)
	}
	// Allow-list: nothing but the projected fields.
	for _, forbidden := range []string{"litellm_params", "credentials", "static_headers", "api_key", "server_id"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("leaked %q: %s", forbidden, rec.Body)
		}
	}
}

func TestCapabilities_PersonalProvisioningState(t *testing.T) {
	d := testDeps(t)
	d.AsUser = func(string) UserReads {
		return &fakeCatalog{models: []litellm.ModelGroupInfo{{Name: "__deny_all__", Providers: []string{}}}}
	}
	rec := do(t, d, "/platform/console/capabilities?scope=personal", pkCtx(t, "u@x.com", false))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"provisioning":true`) || !strings.Contains(rec.Body.String(), `"models":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestCapabilities_PersonalNeverRetriesAsMaster(t *testing.T) {
	d := testDeps(t)
	d.AsUser = func(string) UserReads { return &fakeCatalog{err: &litellm.Auth401Error{}} }
	rec := do(t, d, "/platform/console/capabilities?scope=personal", pkCtx(t, "u@x.com", false))
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "litellm_rejected") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	d.AsUser = func(string) UserReads { return &fakeCatalog{err: errors.New("dial tcp: refused")} }
	if rec := do(t, d, "/platform/console/capabilities?scope=personal", pkCtx(t, "u@x.com", false)); rec.Code != 503 {
		t.Fatalf("transport: %d %s", rec.Code, rec.Body)
	}
}

func TestCapabilities_Environment(t *testing.T) {
	d := testDeps(t)
	if rec := do(t, d, "/platform/console/capabilities?scope=environment&name=demo", pkCtx(t, "u@x.com", false)); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"scope":"environment"`) || !strings.Contains(rec.Body.String(), "demo-model") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(t, d, "/platform/console/capabilities?scope=environment&name=demo", pkCtx(t, "v@x.com", false)); rec.Code != 403 {
		t.Fatalf("no access: %d", rec.Code)
	}
	if rec := do(t, d, "/platform/console/capabilities?scope=environment&name=demo", pkCtx(t, "v@x.com", true)); rec.Code != 200 {
		t.Fatalf("admin: %d", rec.Code)
	}
	if rec := do(t, d, "/platform/console/capabilities?scope=environment&name=nope", pkCtx(t, "u@x.com", false)); rec.Code != 404 {
		t.Fatalf("absent: %d", rec.Code)
	}
	if rec := do(t, d, "/platform/console/capabilities?scope=environment", pkCtx(t, "u@x.com", false)); rec.Code != 400 {
		t.Fatalf("no name: %d", rec.Code)
	}
	if rec := do(t, d, "/platform/console/capabilities?scope=bogus", pkCtx(t, "u@x.com", false)); rec.Code != 400 {
		t.Fatalf("bad scope: %d", rec.Code)
	}
	d.LiteLLM = &fakeLL{err: errors.New("down")}
	if rec := do(t, d, "/platform/console/capabilities?scope=environment&name=demo", pkCtx(t, "u@x.com", false)); rec.Code != 503 {
		t.Fatalf("litellm down: %d", rec.Code)
	}
}

func TestCapabilities_EkCallerRefused(t *testing.T) {
	if rec := do(t, testDeps(t), "/platform/console/capabilities?scope=personal", ekCtx()); rec.Code != 401 {
		t.Fatalf("ek_: %d", rec.Code)
	}
}
