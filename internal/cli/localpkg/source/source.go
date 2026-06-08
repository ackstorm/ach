// SPDX-License-Identifier: Apache-2.0

// Package source parses ach-cli local-package source references.
package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Kind identifies the category of a source reference.
type Kind int

const (
	KindGitHub Kind = iota + 1 // github:owner/repo[#ref]
	KindGit                    // git:https://host/path[.git][#ref]
	KindLocal                  // /abs, ./rel, ../rel, ~/home
)

const (
	AuthBearer      = "bearer"
	AuthBasicOAuth2 = "basic-oauth2"
)

// SourceURI is the parsed representation of a source reference.
type SourceURI struct {
	Kind       Kind
	CloneURL   string // https URL for github/git; empty for local
	GitRef     string // text after '#'; empty → default branch
	LocalPath  string // absolute path for local
	AuthScheme string // "bearer" (default) or "basic-oauth2"
}

// Parse classifies ref. authOverride (""|"bearer"|"basic-oauth2") forces the
// scheme when non-empty; otherwise it is inferred (gitlab host → basic-oauth2).
func Parse(ref, authOverride string) (SourceURI, error) {
	if ref == "" {
		return SourceURI{}, fmt.Errorf("source: empty reference")
	}

	// Validate authOverride if provided.
	if authOverride != "" && authOverride != AuthBearer && authOverride != AuthBasicOAuth2 {
		return SourceURI{}, fmt.Errorf("source: unknown auth scheme %q (want %q or %q)", authOverride, AuthBearer, AuthBasicOAuth2)
	}

	// Local paths: start with /, ./, ../, or ~
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "./") ||
		strings.HasPrefix(ref, "../") || strings.HasPrefix(ref, "~") {
		return parseLocal(ref)
	}

	// github: scheme
	if strings.HasPrefix(ref, "github:") {
		return parseGitHub(ref, authOverride)
	}

	// git: scheme
	if strings.HasPrefix(ref, "git:") {
		return parseGit(ref, authOverride)
	}

	return SourceURI{}, fmt.Errorf("source: unrecognized reference %q (expected github:, git:, or a local path)", ref)
}

// parseGitHub handles "github:owner/repo[.git][#ref]".
func parseGitHub(ref, authOverride string) (SourceURI, error) {
	body := strings.TrimPrefix(ref, "github:")

	// Split off optional fragment (#ref).
	gitRef := ""
	if idx := strings.IndexByte(body, '#'); idx >= 0 {
		gitRef = body[idx+1:]
		body = body[:idx]
	}

	// Strip optional trailing .git before rebuilding.
	body = strings.TrimSuffix(body, ".git")

	cloneURL := "https://github.com/" + body + ".git"

	auth := AuthBearer
	if authOverride != "" {
		auth = authOverride
	}

	return SourceURI{
		Kind:       KindGitHub,
		CloneURL:   cloneURL,
		GitRef:     gitRef,
		AuthScheme: auth,
	}, nil
}

// parseGit handles "git:<url>[#ref]".
func parseGit(ref, authOverride string) (SourceURI, error) {
	// Strip the "git:" prefix — the rest is the full URL (including scheme).
	rawURL := strings.TrimPrefix(ref, "git:")

	// Split off optional fragment (#ref) — net/url would percent-encode #, so
	// we split manually before any URL parsing.
	gitRef := ""
	if idx := strings.IndexByte(rawURL, '#'); idx >= 0 {
		gitRef = rawURL[idx+1:]
		rawURL = rawURL[:idx]
	}

	// Infer auth scheme from the URL host.
	auth := inferGitAuth(rawURL)
	if authOverride != "" {
		auth = authOverride
	}

	return SourceURI{
		Kind:       KindGit,
		CloneURL:   rawURL,
		GitRef:     gitRef,
		AuthScheme: auth,
	}, nil
}

// inferGitAuth returns AuthBasicOAuth2 for known GitLab host patterns,
// AuthBearer otherwise.
func inferGitAuth(rawURL string) string {
	// Extract host from URL by finding the authority component.
	// rawURL looks like "https://host/path" or "http://host/path".
	host := rawURL
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	// Strip path.
	if idx := strings.IndexByte(host, '/'); idx >= 0 {
		host = host[:idx]
	}
	// Strip port if present.
	if idx := strings.LastIndexByte(host, ':'); idx >= 0 {
		host = host[:idx]
	}

	if strings.Contains(host, "gitlab") || strings.HasPrefix(host, "git.") {
		return AuthBasicOAuth2
	}
	return AuthBearer
}

// parseLocal handles /abs, ./rel, ../rel, ~/home paths.
func parseLocal(ref string) (SourceURI, error) {
	// Expand ~ to home directory.
	if strings.HasPrefix(ref, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return SourceURI{}, fmt.Errorf("source: cannot expand ~: %w", err)
		}
		ref = filepath.Join(home, ref[1:])
	}

	// Resolve to absolute path (handles ./ and ../ naturally).
	abs, err := filepath.Abs(ref)
	if err != nil {
		return SourceURI{}, fmt.Errorf("source: cannot resolve path %q: %w", ref, err)
	}

	return SourceURI{
		Kind:      KindLocal,
		LocalPath: abs,
	}, nil
}
