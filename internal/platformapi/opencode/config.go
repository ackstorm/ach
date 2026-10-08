// SPDX-License-Identifier: Apache-2.0

// Package opencode serves ACH's OpenCode client surface: the well-known
// manifest that installs the SSO plugin (wellknown.go) and the per-user
// config the plugin pulls at every start (config.go).
package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ackstorm/ach/internal/keycrypt"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/auth"
)

// GET /clients/opencode/config — the per-user OpenCode config the plugin's
// config hook pulls at every start (schema ackstorm.opencode-config/1, shared
// with alitellm-auth). Outside platform-api's Authn on purpose: OpenCode's
// start must never depend on this route, so it answers 200 to ANY Bearer —
// an unverifiable token or a revoked session gets the empty baseline
// (auth "invalid": the plugin drops its cache), an upstream failure gets the
// user's last good body (stale) — and 401 only when there is no Bearer.

const (
	schemaID     = "ackstorm.opencode-config/1"
	cacheKind    = "opencode_config"
	cacheTTL     = 30 * 24 * time.Hour
	capsTTL      = time.Hour
	capsTimeout  = 15 * time.Second
	upstreamWait = 1500 * time.Millisecond
	// Same fallbacks as alitellm-operator and alitellm-auth.
	defaultContext, defaultOutput = 128000, 8192
)

// UserCatalog is what the handler reads as the user (litellm.UserView).
type UserCatalog interface {
	ListModelGroups(ctx context.Context) ([]litellm.ModelGroupInfo, error)
}

// AdminCatalog is what it reads with the master key (*litellm.RESTClient).
// It only DESCRIBES models the user's own view returned; it never adds one.
type AdminCatalog interface {
	ListDeploymentCapabilities(ctx context.Context) (map[string]litellm.ModelCaps, error)
	ModelGroupAliases(ctx context.Context) (map[string]string, error)
}

// ConfigDeps wires ConfigHandler.
type ConfigDeps struct {
	BaseURL          string
	Provider         string                                       // provider id + display name (genai.providerName)
	DefaultModel     string                                       // bare model name → config.model when the user sees it (empty = off)
	DefaultSmall     string                                       // same, for config.small_model
	Verify           func(token string) (email string, err error) // local JWT check, no I/O
	Resolver         keystore.Resolver
	KeyEncryptionKey []byte
	AsUser           func(litellmKey string) UserCatalog
	Admin            AdminCatalog
	Store            *auth.OAuthStore
	Logger           *slog.Logger

	// DefaultEnv (genai.defaultEnvironment) is the Environment the skill tells
	// users to hydrate globally; empty cuts that step.
	DefaultEnv string
}

// ConfigHandler serves GET /clients/opencode/config.
func ConfigHandler(d ConfigDeps) http.HandlerFunc {
	h, _ := newConfigHandler(d)
	return h
}

func newConfigHandler(d ConfigDeps) (http.HandlerFunc, *capsCache) {
	base := strings.TrimRight(d.BaseURL, "/")
	skill := apiSkill(base, d.Provider, d.DefaultEnv)
	caps := &capsCache{admin: d.Admin, log: d.Logger}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		tok, ok := bearer(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized", "message": "Bearer token required."})
			return
		}
		ctx := r.Context()
		answer := func(b map[string]any) {
			d.Logger.Info("opencode config", "user", b["user"], "version", b["version"], "stale", b["stale"])
			_ = json.NewEncoder(w).Encode(b)
		}
		email, err := "", error(nil)
		if keys.LooksLikeJWS(tok) {
			email, err = d.Verify(tok)
		}
		if email == "" || err != nil {
			answer(body(nil, map[string]any{}, []any{}, "invalid", false)) // nothing about the org leaks
			return
		}
		fallback := func() {
			var cached map[string]any
			if ok, err := d.Store.Get(ctx, cacheKind, email, &cached); err == nil && ok {
				cached["stale"] = true
				answer(cached)
				return
			}
			answer(body(&email, map[string]any{}, []any{skill}, "ok", true))
		}
		info, err := d.Resolver.Resolve(ctx, tok)
		if err != nil {
			fallback()
			return
		}
		if info == nil || info.LiteLLMKeyMaterial == nil {
			answer(body(nil, map[string]any{}, []any{}, "invalid", false)) // session revoked
			return
		}
		sk, err := keycrypt.Open(d.KeyEncryptionKey, *info.LiteLLMKeyMaterial)
		if err != nil {
			fallback()
			return
		}
		uctx, cancel := context.WithTimeout(ctx, upstreamWait)
		defer cancel()
		user := d.AsUser(string(sk))
		var (
			wg      sync.WaitGroup
			groups  []litellm.ModelGroupInfo
			gErr    error
			deps    map[string]litellm.ModelCaps
			aliases map[string]string
		)
		wg.Add(2)
		go func() { defer wg.Done(); groups, gErr = user.ListModelGroups(uctx) }()
		go func() { defer wg.Done(); deps, aliases = caps.get(uctx) }()
		wg.Wait()
		if gErr != nil {
			fallback()
			return
		}
		b := body(&email, buildConfig(base, d.Provider, d.DefaultModel, d.DefaultSmall, groups, deps, aliases), []any{skill}, "ok", false)
		if err := d.Store.Put(ctx, cacheKind, email, b, cacheTTL); err != nil {
			d.Logger.Warn("opencode config: cache write failed", "user", email, "err", err)
		}
		answer(b)
	}, caps
}

func bearer(r *http.Request) (string, bool) {
	scheme, tok, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	tok = strings.TrimSpace(tok)
	return tok, strings.EqualFold(scheme, "bearer") && tok != ""
}

func buildConfig(base, provider, defModel, defSmall string, groups []litellm.ModelGroupInfo,
	deps map[string]litellm.ModelCaps, aliases map[string]string) map[string]any {
	models := map[string]any{}
	for _, g := range groups {
		if g.Name == "" || g.Mode == nil || *g.Mode != "chat" {
			continue // OpenCode cannot use embeddings, TTS, image, transcription
		}
		if strings.HasPrefix(g.Name, "a2a/") {
			// An A2A agent, not a model: LiteLLM lists it once the agent is granted, but
			// routes "a2a/" names (its own _is_a2a_agent_model) through the team's MODEL
			// list, which never holds agents, so every call is refused.
			continue
		}
		target := g.Name
		if a, ok := aliases[g.Name]; ok {
			target = a
		}
		models[g.Name] = model(g.Name, overlay(capsFromGroup(g), deps[target]))
	}
	config := map[string]any{}
	if len(models) > 0 {
		// No "env": auth comes from the plugin; an env entry would let an
		// exported variable override OAuth.
		config["provider"] = map[string]any{provider: map[string]any{
			"name": provider, "npm": "@ai-sdk/openai-compatible",
			"options": map[string]any{"baseURL": base + "/v1"}, "models": models,
		}}
	}
	// Defaults only when the user's own list holds the model; otherwise OpenCode
	// picks the first one. The user's opencode.json overrides both.
	for key, name := range map[string]string{"model": defModel, "small_model": defSmall} {
		if _, ok := models[name]; ok && name != "" {
			config[key] = provider + "/" + name
		}
	}
	return config
}

func capsFromGroup(g litellm.ModelGroupInfo) litellm.ModelCaps {
	return litellm.ModelCaps{Mode: g.Mode, MaxInputTokens: g.MaxInputTokens, MaxOutputTokens: g.MaxOutputTokens,
		InputCostPerToken: g.InputCostPerToken, OutputCostPerToken: g.OutputCostPerToken,
		SupportsVision: &g.SupportsVision, SupportsFunctionCalling: &g.SupportsFunctionCalling,
		SupportsReasoning: &g.SupportsReasoning}
}

// overlay returns base with every field dep knows replacing base's.
func overlay(base, dep litellm.ModelCaps) litellm.ModelCaps {
	pick := func(b, d *float64) *float64 {
		if d != nil {
			return d
		}
		return b
	}
	pickB := func(b, d *bool) *bool {
		if d != nil {
			return d
		}
		return b
	}
	if dep.Mode != nil {
		base.Mode = dep.Mode
	}
	base.MaxInputTokens = pick(base.MaxInputTokens, dep.MaxInputTokens)
	base.MaxOutputTokens = pick(base.MaxOutputTokens, dep.MaxOutputTokens)
	base.InputCostPerToken = pick(base.InputCostPerToken, dep.InputCostPerToken)
	base.OutputCostPerToken = pick(base.OutputCostPerToken, dep.OutputCostPerToken)
	base.CacheReadInputTokenCost = pick(base.CacheReadInputTokenCost, dep.CacheReadInputTokenCost)
	base.SupportsVision = pickB(base.SupportsVision, dep.SupportsVision)
	base.SupportsPDFInput = pickB(base.SupportsPDFInput, dep.SupportsPDFInput)
	base.SupportsAudioInput = pickB(base.SupportsAudioInput, dep.SupportsAudioInput)
	base.SupportsVideoInput = pickB(base.SupportsVideoInput, dep.SupportsVideoInput)
	base.SupportsFunctionCalling = pickB(base.SupportsFunctionCalling, dep.SupportsFunctionCalling)
	base.SupportsReasoning = pickB(base.SupportsReasoning, dep.SupportsReasoning)
	return base
}

func model(name string, c litellm.ModelCaps) map[string]any {
	in := []string{"text"}
	for _, m := range []struct {
		f *bool
		v string
	}{{c.SupportsVision, "image"}, {c.SupportsAudioInput, "audio"}, {c.SupportsPDFInput, "pdf"}, {c.SupportsVideoInput, "video"}} {
		if isTrue(m.f) {
			in = append(in, m.v)
		}
	}
	return map[string]any{
		"name":       name,
		"attachment": len(in) > 1, // unlocks file/image attach in the TUI
		"reasoning":  isTrue(c.SupportsReasoning), "tool_call": isTrue(c.SupportsFunctionCalling), "temperature": true,
		"modalities": map[string]any{"input": in, "output": []string{"text"}},
		"limit":      map[string]any{"context": intOr(c.MaxInputTokens, defaultContext), "output": intOr(c.MaxOutputTokens, defaultOutput)},
		"cost": map[string]any{"input": perMillion(c.InputCostPerToken), "output": perMillion(c.OutputCostPerToken),
			"cache_read": perMillion(c.CacheReadInputTokenCost)},
	}
}

func isTrue(b *bool) bool { return b != nil && *b }

func intOr(v *float64, def int) int {
	if v == nil || *v <= 0 {
		return def
	}
	return int(*v)
}

func perMillion(v *float64) float64 {
	if v == nil {
		return 0
	}
	return math.Round(*v*1e6*1e6) / 1e6
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// body is the response envelope. version hashes config+skills (encoding/json
// sorts map keys, so the marshal is canonical); stable while inputs are.
func body(user *string, config map[string]any, skills []any, authState string, stale bool) map[string]any {
	raw, _ := json.Marshal(map[string]any{"config": config, "skills": skills})
	var u any
	if user != nil {
		u = *user
	}
	return map[string]any{
		"schema": schemaID, "version": sha(string(raw)),
		"user": u, "environment": nil, "auth": authState, "stale": stale,
		"generatedAt": time.Now().UTC().Format(time.RFC3339),
		"config":      config, "skills": skills,
	}
}

// capsCache is the process-wide admin view (deployment capabilities + alias
// map), refreshed every capsTTL, stale-while-revalidate: a stale copy is
// served at once while ONE background refresh runs; only a cold start waits,
// bounded by the request. A failed refresh keeps the old copy (possibly none
// ⇒ group-row values). ponytail: per replica; replicas refresh independently.
type capsCache struct {
	admin AdminCatalog
	log   *slog.Logger

	mu         sync.Mutex
	at         time.Time
	refreshing chan struct{}
	deps       map[string]litellm.ModelCaps
	aliases    map[string]string
}

func (c *capsCache) get(ctx context.Context) (map[string]litellm.ModelCaps, map[string]string) {
	c.mu.Lock()
	if time.Since(c.at) > capsTTL && c.refreshing == nil {
		done := make(chan struct{})
		c.refreshing = done
		go c.refresh(done)
	}
	loaded, wait := !c.at.IsZero(), c.refreshing
	c.mu.Unlock()
	if !loaded && wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deps, c.aliases
}

func (c *capsCache) refresh(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), capsTimeout)
	defer cancel()
	deps, err := c.admin.ListDeploymentCapabilities(ctx)
	var aliases map[string]string
	if err == nil {
		aliases, err = c.admin.ModelGroupAliases(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.log.Warn("opencode config: capability refresh failed", "err", err)
	} else {
		c.deps, c.aliases, c.at = deps, aliases, time.Now()
	}
	c.refreshing = nil
	close(done)
}
