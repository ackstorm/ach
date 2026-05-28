// SPDX-License-Identifier: Apache-2.0

// Unit tests for the Claude Code real-schema parser + UnmarshalJSON
// union. Pure-Go (no envtest); runs with `go test ./internal/controller/ach/...`.

package ach

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

// ─── Union UnmarshalJSON tests ────────────────────────────────────────

func TestClaudeCodeMarketplaceSource_UnmarshalString(t *testing.T) {
	var s ClaudeCodeMarketplaceSource
	if err := json.Unmarshal([]byte(`"./plugins/agent-sdk-dev"`), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Kind != "local-path" {
		t.Errorf("Kind = %q; want local-path", s.Kind)
	}
	if s.Path != "./plugins/agent-sdk-dev" {
		t.Errorf("Path = %q; want ./plugins/agent-sdk-dev", s.Path)
	}
}

func TestClaudeCodeMarketplaceSource_UnmarshalGitSubdir(t *testing.T) {
	body := []byte(`{"source":"git-subdir","url":"https://github.com/42Crunch-AI/claude-plugins.git","path":"plugins/api-security-testing","ref":"v1.5.5","sha":"a175b24f7b34852b70c78c21545cce8037eb3112"}`)
	var s ClaudeCodeMarketplaceSource
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Kind != "git-subdir" {
		t.Errorf("Kind = %q; want git-subdir", s.Kind)
	}
	if s.URL != "https://github.com/42Crunch-AI/claude-plugins.git" {
		t.Errorf("URL = %q", s.URL)
	}
	if s.Path != "plugins/api-security-testing" {
		t.Errorf("Path = %q", s.Path)
	}
	if s.SHA != "a175b24f7b34852b70c78c21545cce8037eb3112" {
		t.Errorf("SHA = %q", s.SHA)
	}
}

func TestClaudeCodeMarketplaceSource_UnmarshalURL(t *testing.T) {
	body := []byte(`{"source":"url","url":"https://github.com/AikidoSec/aikido-claude-plugin.git","sha":"79ac524f87c9faa9a356ff3d495b8a5b77e01bbd"}`)
	var s ClaudeCodeMarketplaceSource
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Kind != "url" || s.URL == "" || s.SHA == "" || s.Path != "" {
		t.Errorf("got %+v", s)
	}
}

func TestClaudeCodeMarketplaceSource_UnmarshalUnknownDiscriminator(t *testing.T) {
	body := []byte(`{"source":"npm","package":"left-pad"}`)
	var s ClaudeCodeMarketplaceSource
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Kind != "" {
		t.Errorf("Kind = %q; want \"\" for unknown discriminator", s.Kind)
	}
}

func TestClaudeCodeMarketplaceSource_UnmarshalMalformed(t *testing.T) {
	body := []byte(`[1,2,3]`) // neither string nor object
	var s ClaudeCodeMarketplaceSource
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("unmarshal should not error on malformed; got %v", err)
	}
	if s.Kind != "" {
		t.Errorf("Kind = %q; want \"\"", s.Kind)
	}
}

// ─── Parser tests ─────────────────────────────────────────────────────

const validRealSchemaMarketplace = `{
  "$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
  "name": "claude-plugins-official",
  "owner": {"name": "Anthropic", "email": "support@anthropic.com"},
  "plugins": [
    {
      "name": "agent-sdk-dev",
      "source": "./plugins/agent-sdk-dev"
    },
    {
      "name": "42crunch-api-security-testing",
      "source": {
        "source": "git-subdir",
        "url": "https://github.com/42Crunch-AI/claude-plugins.git",
        "path": "plugins/api-security-testing",
        "ref": "v1.5.5",
        "sha": "a175b24f7b34852b70c78c21545cce8037eb3112"
      }
    },
    {
      "name": "aikido",
      "source": {
        "source": "url",
        "url": "https://github.com/AikidoSec/aikido-claude-plugin.git",
        "sha": "79ac524f87c9faa9a356ff3d495b8a5b77e01bbd"
      }
    }
  ]
}`

func TestParseClaudeCodeMarketplace_RealSchemaValid(t *testing.T) {
	mkt, err := parseClaudeCodeMarketplace([]byte(validRealSchemaMarketplace))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(mkt.Plugins) != 3 {
		t.Fatalf("want 3 plugins; got %d", len(mkt.Plugins))
	}
	if mkt.Plugins[0].Source.Kind != "local-path" {
		t.Errorf("plugin[0] Kind = %q; want local-path", mkt.Plugins[0].Source.Kind)
	}
	if mkt.Plugins[1].Source.Kind != "git-subdir" {
		t.Errorf("plugin[1] Kind = %q; want git-subdir", mkt.Plugins[1].Source.Kind)
	}
	if mkt.Plugins[2].Source.Kind != "url" {
		t.Errorf("plugin[2] Kind = %q; want url", mkt.Plugins[2].Source.Kind)
	}
	if mkt.Owner.Email != "support@anthropic.com" {
		t.Errorf("owner.email = %q", mkt.Owner.Email)
	}
}

func TestParseClaudeCodeMarketplace_MalformedJSON(t *testing.T) {
	_, err := parseClaudeCodeMarketplace([]byte("{not json"))
	if err == nil {
		t.Fatal("expected err on malformed JSON; got nil")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
	}
}

func TestParseClaudeCodeMarketplace_ZeroPlugins(t *testing.T) {
	body := `{"name":"m","owner":{"name":"o","url":""},"plugins":[]}`
	_, err := parseClaudeCodeMarketplace([]byte(body))
	if err == nil {
		t.Fatal("expected err on zero plugins; got nil")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "zero plugins") {
		t.Errorf("err message should mention 'zero plugins'; got %q", err.Error())
	}
}

func TestParseClaudeCodeMarketplace_PluginNameTraversalRejected(t *testing.T) {
	// T-02-06-01 adversarial-name mitigation: '../etc/passwd' MUST be
	// rejected by the DNS-1123-subdomain check.
	body := `{
      "name": "m",
      "owner": {"name": "o", "url": ""},
      "plugins": [{
        "name": "../etc/passwd",
        "source": "./safe"
      }]
    }`
	_, err := parseClaudeCodeMarketplace([]byte(body))
	if err == nil {
		t.Fatal("expected DNS-1123 rejection for path-traversal plugin name")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
	}
}

func TestParseClaudeCodeMarketplace_PluginNameUppercaseRejected(t *testing.T) {
	// DNS-1123 subdomains are lowercase-only; an uppercase plugin name
	// SHOULD be rejected.
	body := `{
      "name": "m",
      "owner": {"name": "o", "url": ""},
      "plugins": [{
        "name": "UpperCase",
        "source": "./safe"
      }]
    }`
	_, err := parseClaudeCodeMarketplace([]byte(body))
	if err == nil {
		t.Fatal("expected DNS-1123 rejection for uppercase plugin name")
	}
}

// ─── Per-entry demote tests (issue #15 / Phase 1) ─────────────────────

func TestParseClaudeCodeMarketplace_UrlMissingShaDemotedPerEntry(t *testing.T) {
	// A url-Kind entry missing `sha` MUST NOT abort the catalog. The
	// invalid entry resolves to Kind="" so Stage-2 demotes it via
	// ReasonUnsupportedPluginSource. The sibling valid git-subdir entry
	// must round-trip intact.
	body := `{
	  "name": "mkt",
	  "owner": {"name": "o"},
	  "plugins": [
	    {
	      "name": "missing-sha",
	      "source": {"source": "url", "url": "https://example.com/p.git"}
	    },
	    {
	      "name": "valid-git-subdir",
	      "source": {
	        "source": "git-subdir",
	        "url": "https://github.com/o/r.git",
	        "path": "plugins/x",
	        "ref": "v1",
	        "sha": "0123456789abcdef0123456789abcdef01234567"
	      }
	    }
	  ]
	}`
	mkt, err := parseClaudeCodeMarketplace([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v (whole-catalog abort is the bug)", err)
	}
	if len(mkt.Plugins) != 2 {
		t.Fatalf("want 2 plugins; got %d", len(mkt.Plugins))
	}
	if mkt.Plugins[0].Source.Kind != "" {
		t.Errorf("plugin[0].Kind = %q; want \"\" (demoted)", mkt.Plugins[0].Source.Kind)
	}
	if mkt.Plugins[1].Source.Kind != "git-subdir" {
		t.Errorf("plugin[1].Kind = %q; want git-subdir", mkt.Plugins[1].Source.Kind)
	}
}

func TestParseClaudeCodeMarketplace_GitSubdirMissingUrlDemoted(t *testing.T) {
	body := `{
	  "name": "mkt", "owner": {"name": "o"},
	  "plugins": [{
	    "name": "bad",
	    "source": {"source": "git-subdir", "path": "p", "sha": "0123456789abcdef0123456789abcdef01234567"}
	  }]
	}`
	mkt, err := parseClaudeCodeMarketplace([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mkt.Plugins[0].Source.Kind != "" {
		t.Errorf("Kind = %q; want demoted to \"\"", mkt.Plugins[0].Source.Kind)
	}
}

func TestParseClaudeCodeMarketplace_LocalPathTraversalDemotedPerEntry(t *testing.T) {
	// local-path with `..` segment must NOT abort the catalog (#4 (b)
	// decision: demote per-entry, T-02-06-01 mitigation still applies
	// because the traversal path never reaches the filesystem — the
	// dispatcher short-circuits on Kind="").
	body := `{
	  "name": "mkt", "owner": {"name": "o"},
	  "plugins": [{
	    "name": "evil",
	    "source": "../etc/passwd"
	  }]
	}`
	mkt, err := parseClaudeCodeMarketplace([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mkt.Plugins[0].Source.Kind != "" {
		t.Errorf("Kind = %q; want \"\" (demoted)", mkt.Plugins[0].Source.Kind)
	}
}
