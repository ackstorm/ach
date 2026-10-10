// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ackstorm/ach/internal/keycrypt"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/auth"
)

const (
	goodTok   = "aaa.bbb.ccc"
	userKey   = "sk-user-plaintext"
	userEmail = "u@x.com"
)

var kek = []byte("0123456789abcdef0123456789abcdef")

type fakeResolver struct {
	info *keystore.KeyInfo
	err  error
}

func (f *fakeResolver) Resolve(context.Context, string) (*keystore.KeyInfo, error) {
	return f.info, f.err
}

type fakeUser struct {
	groups []litellm.ModelGroupInfo
	err    error
}

func (f *fakeUser) ListModelGroups(context.Context) ([]litellm.ModelGroupInfo, error) {
	return f.groups, f.err
}

type fakeAdmin struct {
	deps  map[string]litellm.ModelCaps
	alias map[string]string
	err   error
	delay time.Duration
	calls atomic.Int32
}

func (f *fakeAdmin) ListDeploymentCapabilities(context.Context) (map[string]litellm.ModelCaps, error) {
	f.calls.Add(1)
	time.Sleep(f.delay)
	return f.deps, f.err
}
func (f *fakeAdmin) ModelGroupAliases(context.Context) (map[string]string, error) {
	return f.alias, f.err
}

func ptr[T any](v T) *T { return &v }

type fixture struct {
	h     http.HandlerFunc
	caps  *capsCache
	res   *fakeResolver
	user  *fakeUser
	admin *fakeAdmin
	seen  string // key the handler asked AsUser for
}

func newFixture(t *testing.T) *fixture { return newFixtureWith(t, "", "", "") }

func newFixtureWith(t *testing.T, defModel, defSmall, defEnv string) *fixture {
	t.Helper()
	sealed, err := keycrypt.Seal(kek, []byte(userKey))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		res: &fakeResolver{info: &keystore.KeyInfo{OwnerEmail: userEmail, LiteLLMKeyMaterial: &sealed}},
		user: &fakeUser{
			groups: []litellm.ModelGroupInfo{
				{Name: "ackstorm.smart", Mode: ptr("chat"), SupportsVision: true},
				{Name: "gemini.flash", Mode: ptr("chat")},
				{Name: "text-embedding", Mode: ptr("embedding")},
				{Name: "a2a/finops-advisor", Mode: ptr("chat")},
				{Name: "ackstorm.router", Mode: ptr("chat"), SupportsFunctionCalling: true},
			},
		},
		admin: &fakeAdmin{
			deps: map[string]litellm.ModelCaps{
				"gemini.flash": {Mode: ptr("chat"), MaxInputTokens: ptr(1048576.0), SupportsPDFInput: ptr(true),
					CacheReadInputTokenCost: ptr(7.5e-08), InputCostPerToken: ptr(7.5e-07)},
				"secret.model": {Mode: ptr("chat")},
			},
			alias: map[string]string{"ackstorm.smart": "gemini.flash"},
		},
	}
	mr := miniredis.RunT(t)
	f.h, f.caps = newConfigHandler(ConfigDeps{
		BaseURL: "https://ach.test/", Provider: "acme", DefaultModel: defModel, DefaultSmall: defSmall,
		Verify: func(tok string) (string, error) {
			if tok == goodTok {
				return userEmail, nil
			}
			return "", errors.New("bad signature")
		},
		Resolver: f.res, KeyEncryptionKey: kek,
		AsUser:     func(k string) UserCatalog { f.seen = k; return f.user },
		Admin:      f.admin,
		Store:      &auth.OAuthStore{RDB: redis.NewClient(&redis.Options{Addr: mr.Addr()})},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		DefaultEnv: defEnv,
	})
	return f
}

func (f *fixture) get(t *testing.T, authz string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/clients/opencode/config", nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	w := httptest.NewRecorder()
	f.h(w, req)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w, m
}

func models(m map[string]any) map[string]any {
	return m["config"].(map[string]any)["provider"].(map[string]any)["acme"].(map[string]any)["models"].(map[string]any)
}

func TestConfig_MissingOrMalformedHeaderIs401(t *testing.T) {
	f := newFixture(t)
	for _, h := range []string{"", "Basic abc", "Bearer ", "Bearer"} {
		if w, _ := f.get(t, h); w.Code != 401 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%q: %d %v", h, w.Code, w.Header())
		}
	}
}

func TestConfig_InvalidTokenIsBaseline(t *testing.T) {
	for name, tc := range map[string]struct {
		tok     string
		revoked bool
	}{"not-a-jws": {"sk-notajwt", false}, "bad-signature": {"bad.bad.bad", false}, "revoked": {goodTok, true}} {
		f := newFixture(t)
		if tc.revoked {
			f.res.info = nil
		}
		w, m := f.get(t, "Bearer "+tc.tok)
		if w.Code != 200 || m["auth"] != "invalid" || m["user"] != nil || len(m["config"].(map[string]any)) != 0 ||
			len(m["skills"].([]any)) != 0 || strings.Contains(w.Body.String(), "gemini") || strings.Contains(w.Body.String(), "github") {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestConfig_OK(t *testing.T) {
	f := newFixture(t)
	w, m := f.get(t, "Bearer "+goodTok)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || m["auth"] != "ok" || m["stale"] != false ||
		m["user"] != userEmail || m["environment"] != nil || m["schema"] != "ackstorm.opencode-config/1" || f.seen != userKey {
		t.Fatalf("%d %s (AsUser key %q)", w.Code, w.Body, f.seen)
	}
	cfg := m["config"].(map[string]any)
	for k := range cfg {
		if k != "provider" && k != "model" && k != "small_model" {
			t.Fatalf("unexpected config key %q", k)
		}
	}
	p := cfg["provider"].(map[string]any)["acme"].(map[string]any)
	if _, has := p["env"]; has || p["options"].(map[string]any)["baseURL"] != "https://ach.test/v1" || p["name"] != "acme" {
		t.Fatalf("provider %v", p)
	}
	if _, has := cfg["mcp"]; has {
		t.Fatalf("mcp %v", cfg["mcp"])
	}
	if sk := m["skills"].([]any); len(sk) != 1 || sk[0].(map[string]any)["name"] != "acme-ach" {
		t.Fatalf("skills %v", sk)
	}
	if strings.Contains(w.Body.String(), "secret.model") {
		t.Fatal("the admin view must never add a model")
	}
}

func TestConfig_ModelMapping(t *testing.T) {
	f := newFixture(t)
	_, m := f.get(t, "Bearer "+goodTok)
	ms := models(m)
	if len(ms) != 3 || ms["text-embedding"] != nil || ms["a2a/finops-advisor"] != nil {
		t.Fatalf("models %v", ms)
	}
	smart := ms["ackstorm.smart"].(map[string]any)
	if in := smart["modalities"].(map[string]any)["input"].([]any); len(in) != 3 || in[1] != "image" || in[2] != "pdf" ||
		smart["cost"].(map[string]any)["cache_read"] != 0.075 || smart["cost"].(map[string]any)["input"] != 0.75 ||
		smart["limit"].(map[string]any)["context"] != 1048576.0 || smart["attachment"] != true {
		t.Fatalf("alias caps %v", smart)
	}
	router := ms["ackstorm.router"].(map[string]any)
	if router["limit"].(map[string]any)["context"] != 128000.0 || router["limit"].(map[string]any)["output"] != 8192.0 ||
		router["tool_call"] != true || router["attachment"] != false {
		t.Fatalf("router %v", router)
	}
}

func TestConfig_UpstreamDownServesCacheThenBareFallback(t *testing.T) {
	f := newFixture(t)
	_, first := f.get(t, "Bearer "+goodTok)
	f.user.err = errors.New("litellm down")
	w, m := f.get(t, "Bearer "+goodTok)
	if w.Code != 200 || m["stale"] != true || m["version"] != first["version"] || len(models(m)) != 3 {
		t.Fatalf("cached: %d %s", w.Code, w.Body)
	}
	for name, mut := range map[string]func(*fixture){
		"litellm":  func(f *fixture) { f.user.err = errors.New("down") },
		"resolver": func(f *fixture) { f.res.err = errors.New("db down") },
	} {
		f := newFixture(t) // empty store
		mut(f)
		w, m := f.get(t, "Bearer "+goodTok)
		if w.Code != 200 || m["auth"] != "ok" || m["user"] != userEmail || m["stale"] != true ||
			len(m["config"].(map[string]any)) != 0 || m["skills"].([]any)[0].(map[string]any)["name"] != "acme-ach" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestConfig_AdminDownStillServesGroupValues(t *testing.T) {
	f := newFixture(t)
	f.admin.err = errors.New("down")
	w, m := f.get(t, "Bearer "+goodTok)
	smart := models(m)["ackstorm.smart"].(map[string]any)
	if w.Code != 200 || m["stale"] != false || len(smart["modalities"].(map[string]any)["input"].([]any)) != 2 ||
		smart["cost"].(map[string]any)["cache_read"] != 0.0 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestConfig_CapabilitiesCachedProcessWide(t *testing.T) {
	f := newFixture(t)
	f.get(t, "Bearer "+goodTok)
	f.get(t, "Bearer "+goodTok)
	if n := f.admin.calls.Load(); n != 1 {
		t.Fatalf("admin read %d times", n)
	}
}

func TestConfig_StaleCapabilitiesServedWithoutWaiting(t *testing.T) {
	f := newFixture(t)
	f.get(t, "Bearer "+goodTok) // warm
	c := f.caps
	c.mu.Lock()
	c.at = time.Now().Add(-2 * capsTTL)
	c.mu.Unlock()
	f.admin.delay = 3 * time.Second
	start := time.Now()
	_, m := f.get(t, "Bearer "+goodTok)
	if time.Since(start) > time.Second || len(models(m)["ackstorm.smart"].(map[string]any)["modalities"].(map[string]any)["input"].([]any)) != 3 {
		t.Fatalf("waited %v or lost stale caps: %v", time.Since(start), m)
	}
}

func TestConfig_VersionStable(t *testing.T) {
	f := newFixture(t)
	_, a := f.get(t, "Bearer "+goodTok)
	_, b := f.get(t, "Bearer "+goodTok)
	f.user.groups = f.user.groups[:1]
	_, c := f.get(t, "Bearer "+goodTok)
	if a["version"] != b["version"] || a["version"] == c["version"] {
		t.Fatalf("%v %v %v", a["version"], b["version"], c["version"])
	}
}

func TestConfig_NoForbiddenKeys(t *testing.T) {
	f := newFixture(t)
	w, _ := f.get(t, "Bearer "+goodTok)
	s := w.Body.String()
	for _, bad := range []string{`"plugin"`, `"permission"`, `"agent"`, `"command"`, `"env"`, "{env:", "{file:", userKey,
		*f.res.info.LiteLLMKeyMaterial} {
		if strings.Contains(s, bad) {
			t.Fatalf("body carries %q", bad)
		}
	}
}

func TestConfig_DefaultModels(t *testing.T) {
	for _, c := range []struct {
		name, model, small string
		wantModel, wantSm  any
	}{
		{"unset", "", "", nil, nil},
		{"both visible", "ackstorm.smart", "gemini.flash", "acme/ackstorm.smart", "acme/gemini.flash"},
		{"hidden", "nope", "ackstorm.smart", nil, "acme/ackstorm.smart"},
		{"non-chat", "text-embedding", "a2a/finops-advisor", nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, m := newFixtureWith(t, c.model, c.small, "").get(t, "Bearer "+goodTok)
			cfg := m["config"].(map[string]any)
			if cfg["model"] != c.wantModel || cfg["small_model"] != c.wantSm {
				t.Fatalf("model=%v small_model=%v", cfg["model"], cfg["small_model"])
			}
		})
	}
}

func TestConfig_SkillDefaultEnvironment(t *testing.T) {
	for env, want := range map[string]bool{"ackstorm": true, "": false} {
		_, m := newFixtureWith(t, "", "", env).get(t, "Bearer "+goodTok)
		sk := m["skills"].([]any)[0].(map[string]any)
		md := sk["files"].(map[string]any)["SKILL.md"].(string)
		if sk["name"] != "acme-ach" || strings.Contains(md, "ach-cli env hydrate ackstorm -g") != want || strings.Contains(md, "{{") {
			t.Fatalf("%q: skill %v", env, sk)
		}
	}
}
