// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/ackstorm/ach/internal/sources"
)

const skillMaxManifestBytes = 1 << 20 // 1 MiB

// skillRawIngressCap bounds the raw skill tarball read before validation — an
// operator-memory guard mirroring gitDefaultMaxCloneBytes (512 MiB), separate
// from the user-facing SkillMaxSizeMiB cap applied to the size-cap copy.
const skillRawIngressCap = 512 << 20

// stageSkillBody reads the fetched skill tar.gz into memory (bounded by
// skillRawIngressCap), validates the SKILL.md gate, and returns the staged
// bytes for materializeExternalRef's size-cap copy (no pluginpack.Filter is
// applied — the skill tree is served verbatim). Returns *OversizeError on
// ingress overflow (→ ReasonPluginTooLarge) and an ErrUpstreamInvalid-wrapping
// error on a malformed skill tree (→ ReasonUpstreamInvalid).
func stageSkillBody(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, skillRawIngressCap+1))
	if err != nil {
		return nil, fmt.Errorf("skill: read body: %w", err)
	}
	if int64(len(raw)) > skillRawIngressCap {
		return nil, &OversizeError{Bytes: int64(len(raw)), Cap: skillRawIngressCap}
	}
	if err := verifySkillContents(bytes.NewReader(raw)); err != nil {
		return nil, err // already wraps sources.ErrUpstreamInvalid
	}
	return raw, nil
}

// skillNameRe enforces the agentskills.io name rule: lowercase alnum + single
// hyphens, no leading/trailing/consecutive hyphen, 1-64 chars (length checked
// separately in validateSkillName).
var skillNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type skillFrontmatter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// validateSkillName implements the agentskills.io name constraints. Shared by
// the standalone Skill verify and (in the SkillMarketplace plan) discovery.
// Returns an error that wraps sources.ErrUpstreamInvalid on any violation.
func validateSkillName(name string) error {
	if name == "" || len(name) > 64 || !skillNameRe.MatchString(name) {
		return fmt.Errorf("skill: invalid name %q (must match %s, 1-64 chars): %w", name, skillNameRe.String(), sources.ErrUpstreamInvalid)
	}
	return nil
}

// verifySkillContents streams a fetched skill tar.gz and confirms it contains
// a valid SKILL.md at the tar root OR one directory deep (git-subdir fetches
// may retain the parent folder). Required: name (valid per validateSkillName)
// + non-empty description (<=1024). All failures wrap sources.ErrUpstreamInvalid
// so classifyFetchError → ReasonUpstreamInvalid.
func verifySkillContents(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("skill: gzip open: %w", errors.Join(err, sources.ErrUpstreamInvalid))
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("skill: tar read: %w", errors.Join(err, sources.ErrUpstreamInvalid))
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		clean := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if path.Base(clean) != "SKILL.md" || strings.Count(clean, "/") > 1 {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, skillMaxManifestBytes))
		if err != nil {
			return fmt.Errorf("skill: read SKILL.md: %w", errors.Join(err, sources.ErrUpstreamInvalid))
		}
		fm, err := parseSkillFrontmatter(body)
		if err != nil {
			return err // already wraps ErrUpstreamInvalid
		}
		if err := validateSkillName(fm.Name); err != nil {
			return err
		}
		if fm.Description == "" || len(fm.Description) > 1024 {
			return fmt.Errorf("skill: SKILL.md description must be 1-1024 chars: %w", sources.ErrUpstreamInvalid)
		}
		return nil
	}
	return fmt.Errorf("skill: no SKILL.md found in fetched tree: %w", sources.ErrUpstreamInvalid)
}

func parseSkillFrontmatter(body []byte) (skillFrontmatter, error) {
	var fm skillFrontmatter
	s := strings.TrimLeft(string(body), "\ufeff \t\r\n")
	if !strings.HasPrefix(s, "---") {
		return fm, fmt.Errorf("skill: SKILL.md missing frontmatter: %w", sources.ErrUpstreamInvalid)
	}
	rest := s[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fm, fmt.Errorf("skill: SKILL.md frontmatter not terminated: %w", sources.ErrUpstreamInvalid)
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return fm, fmt.Errorf("skill: SKILL.md frontmatter parse: %w", errors.Join(err, sources.ErrUpstreamInvalid))
	}
	return fm, nil
}
