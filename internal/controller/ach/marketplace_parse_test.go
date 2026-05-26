// SPDX-License-Identifier: Apache-2.0

// Plan 02-06 Task 1: unit tests for parseClaudeCodeMarketplace +
// marketplacePluginToSourceSpec. Pure-Go (no envtest); runs with
// `go test ./internal/controller/ach/...`.

package ach

import (
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

const validGithubMarketplace = `{
  "name": "example",
  "owner": {"name": "ackstorm", "url": "https://ackstorm.com"},
  "plugins": [
    {
      "name": "alpha",
      "description": "Alpha plugin",
      "source": {
        "type": "github",
        "github": {"repo": "ackstorm/alpha", "ref": "main", "authSecretRef": {"name": "gh-secret"}}
      }
    },
    {
      "name": "beta",
      "description": "Beta plugin",
      "source": {
        "type": "github",
        "github": {"repo": "ackstorm/beta", "ref": "main", "authSecretRef": {"name": "gh-secret"}}
      }
    }
  ]
}`

func TestParseClaudeCodeMarketplace_Valid(t *testing.T) {
	mkt, err := parseClaudeCodeMarketplace([]byte(validGithubMarketplace))
	if err != nil {
		t.Fatalf("expected nil err; got %v", err)
	}
	if len(mkt.Plugins) != 2 {
		t.Errorf("expected 2 plugins; got %d", len(mkt.Plugins))
	}
	if mkt.Plugins[0].Source.GitHub == nil {
		t.Errorf("plugin[0].Source.GitHub should be non-nil")
	}
	if mkt.Plugins[1].Source.GitHub == nil {
		t.Errorf("plugin[1].Source.GitHub should be non-nil")
	}
	if mkt.Name != "example" {
		t.Errorf("Name = %q; want %q", mkt.Name, "example")
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

func TestParseClaudeCodeMarketplace_UnknownType(t *testing.T) {
	body := `{
      "name": "m",
      "owner": {"name": "o", "url": ""},
      "plugins": [{
        "name": "ftp-thing",
        "source": {"type": "ftp"}
      }]
    }`
	_, err := parseClaudeCodeMarketplace([]byte(body))
	if err == nil {
		t.Fatal("expected err on unknown source.type; got nil")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "ftp-thing") {
		t.Errorf("err should reference plugin name; got %q", err.Error())
	}
}

func TestParseClaudeCodeMarketplace_NpmIsKept(t *testing.T) {
	body := `{
      "name": "m",
      "owner": {"name": "o", "url": ""},
      "plugins": [
        {
          "name": "evil",
          "source": {"type": "npm", "npm": {"package": "left-pad"}}
        },
        {
          "name": "good",
          "source": {"type": "github", "github": {"repo": "x/y", "ref": "main", "authSecretRef": {"name": "s"}}}
        }
      ]
    }`
	mkt, err := parseClaudeCodeMarketplace([]byte(body))
	if err != nil {
		t.Fatalf("expected parse to succeed (npm kept); got %v", err)
	}
	if len(mkt.Plugins) != 2 {
		t.Fatalf("expected 2 plugins; got %d", len(mkt.Plugins))
	}
	if mkt.Plugins[0].Source.Type != "npm" {
		t.Errorf("plugin[0].Source.Type = %q; want npm", mkt.Plugins[0].Source.Type)
	}
	if mkt.Plugins[0].Name != "evil" {
		t.Errorf("plugin[0].Name = %q; want evil", mkt.Plugins[0].Name)
	}
}

func TestParseClaudeCodeMarketplace_GitHubSubobjectMissing(t *testing.T) {
	body := `{
      "name": "m",
      "owner": {"name": "o", "url": ""},
      "plugins": [{
        "name": "noobject",
        "source": {"type": "github"}
      }]
    }`
	_, err := parseClaudeCodeMarketplace([]byte(body))
	if err == nil {
		t.Fatal("expected err on missing source.github")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
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
        "source": {"type": "github", "github": {"repo": "x/y", "ref": "main", "authSecretRef": {"name": "s"}}}
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
        "source": {"type": "github", "github": {"repo": "x/y", "ref": "main", "authSecretRef": {"name": "s"}}}
      }]
    }`
	_, err := parseClaudeCodeMarketplace([]byte(body))
	if err == nil {
		t.Fatal("expected DNS-1123 rejection for uppercase plugin name")
	}
}

func TestMarketplacePluginToSourceSpec_Npm(t *testing.T) {
	p := ClaudeCodeMarketplacePlugin{
		Name:   "evil",
		Source: ClaudeCodeMarketplaceSource{Type: "npm"},
	}
	_, err := marketplacePluginToSourceSpec(p)
	if err == nil {
		t.Fatal("expected err for npm source.type")
	}
	if !errors.Is(err, errUnsupportedPluginSource) {
		t.Errorf("err should wrap errUnsupportedPluginSource; got %v", err)
	}
}

func TestMarketplacePluginToSourceSpec_GitHub(t *testing.T) {
	mkt, err := parseClaudeCodeMarketplace([]byte(validGithubMarketplace))
	if err != nil {
		t.Fatalf("parse setup err: %v", err)
	}
	spec, err := marketplacePluginToSourceSpec(mkt.Plugins[0])
	if err != nil {
		t.Fatalf("marketplacePluginToSourceSpec err: %v", err)
	}
	if spec.Type != "github" {
		t.Errorf("SourceSpec.Type = %q; want github", spec.Type)
	}
	if spec.GitHub == nil {
		t.Errorf("SourceSpec.GitHub should be non-nil")
	}
	if spec.GitHub != mkt.Plugins[0].Source.GitHub {
		t.Errorf("SourceSpec.GitHub should alias the parsed pointer")
	}
}
