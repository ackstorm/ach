// SPDX-License-Identifier: Apache-2.0

package contentkit

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

	"github.com/ackstorm/ach/internal/sourceserr"
)

const skillMaxManifestBytes = 1 << 20 // 1 MiB

// SkillRawIngressCap bounds the raw skill tarball read before validation — an
// operator-memory guard mirroring gitDefaultMaxCloneBytes (512 MiB). It is the
// CEILING; StageSkillBody reads no more than the effective cap (min of this and
// the user-facing SkillMaxSizeMiB) so a 511 MiB body destined to fail the 50 MiB
// user cap never buffers the full 512 MiB first (F4).
const SkillRawIngressCap = 512 << 20

// StageSkillBody reads the fetched skill tar.gz into memory (bounded by the
// effective cap = min(SkillRawIngressCap, sizeCap)), validates the SKILL.md
// gate, and returns the staged bytes for materializeExternalRef's size-cap copy
// (no pluginpack.Filter — the skill tree is served verbatim). sizeCap is
// deps.SizeCapBytes (0 = no user cap → ceiling applies).
//
// spec.<git>.path narrowing is now applied at FETCH time (the per-provider
// fetcher narrows the git worktree on-disk, and the REST archive via
// sources.NarrowArchiveSubtree) — so the body this receives is already rooted
// at the skill's contents and no post-fetch slicing happens here (F1). Returns
// *OversizeError on overflow (caller maps it to a PluginTooLarge status
// condition) and an ErrUpstreamInvalid-wrapping error on a malformed/absent
// skill tree (caller maps it to an upstream-invalid outcome).
func StageSkillBody(body io.Reader, sizeCap int64) ([]byte, error) {
	limit := int64(SkillRawIngressCap)
	if sizeCap > 0 && sizeCap < limit {
		limit = sizeCap
	}
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("skill: read body: %w", err)
	}
	if int64(len(raw)) > limit {
		return nil, &OversizeError{Bytes: int64(len(raw)), Cap: limit}
	}
	if err := VerifySkillContents(bytes.NewReader(raw)); err != nil {
		return nil, err // already wraps sourceserr.ErrUpstreamInvalid
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
// Returns an error that wraps sourceserr.ErrUpstreamInvalid on any violation.
func validateSkillName(name string) error {
	if name == "" || len(name) > 64 || !skillNameRe.MatchString(name) {
		return fmt.Errorf("skill: invalid name %q (must match %s, 1-64 chars): %w", name, skillNameRe.String(), sourceserr.ErrUpstreamInvalid)
	}
	return nil
}

// VerifySkillContents streams a fetched skill tar.gz and confirms it contains
// a valid SKILL.md at the tar root OR one directory deep (git-subdir fetches
// may retain the parent folder), Required: name (valid per validateSkillName)
// + non-empty description (<=1024). It ALSO runs tarEntrySafe over EVERY entry
// (the walk runs to EOF, no early return) so a tar carrying a valid SKILL.md
// alongside an unsafe entry the CLI extractor rejects fails here rather than
// reaching Available=True yet breaking hydrate (F3). All failures wrap
// All failures wrap sourceserr.ErrUpstreamInvalid.
func VerifySkillContents(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("skill: gzip open: %w", errors.Join(err, sourceserr.ErrUpstreamInvalid))
	}
	defer func() { _ = gz.Close() }()
	tr, cr := cappedTarReader(gz)
	found := false
	entries := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if cr.n > maxVerifyDecompressedBytes {
				return fmt.Errorf("skill: archive decompresses past %d-byte cap: %w", maxVerifyDecompressedBytes, sourceserr.ErrUpstreamInvalid)
			}
			return fmt.Errorf("skill: tar read: %w", errors.Join(err, sourceserr.ErrUpstreamInvalid))
		}
		entries++
		if entries > maxVerifyEntries {
			return fmt.Errorf("skill: more than %d entries: %w", maxVerifyEntries, sourceserr.ErrUpstreamInvalid)
		}
		if cr.n > maxVerifyDecompressedBytes {
			return fmt.Errorf("skill: archive decompresses past %d-byte cap: %w", maxVerifyDecompressedBytes, sourceserr.ErrUpstreamInvalid)
		}
		// Full-tar safety gate: any entry the CLI extractor rejects under every
		// policy fails the whole tar (F3).
		if err := tarEntrySafe(hdr); err != nil {
			return fmt.Errorf("skill: %w", err)
		}
		if found || hdr.Typeflag != tar.TypeReg {
			continue
		}
		clean := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if path.Base(clean) != "SKILL.md" || strings.Count(clean, "/") > 1 {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, skillMaxManifestBytes))
		if err != nil {
			return fmt.Errorf("skill: read SKILL.md: %w", errors.Join(err, sourceserr.ErrUpstreamInvalid))
		}
		fm, err := parseSkillFrontmatter(body)
		if err != nil {
			return err // already wraps ErrUpstreamInvalid
		}
		if err := validateSkillName(fm.Name); err != nil {
			return err
		}
		if fm.Description == "" || len(fm.Description) > 1024 {
			return fmt.Errorf("skill: SKILL.md description must be 1-1024 chars: %w", sourceserr.ErrUpstreamInvalid)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("skill: no SKILL.md found in fetched tree: %w", sourceserr.ErrUpstreamInvalid)
	}
	return nil
}

func parseSkillFrontmatter(body []byte) (skillFrontmatter, error) {
	var fm skillFrontmatter
	s := strings.TrimLeft(string(body), "\ufeff \t\r\n")
	if !strings.HasPrefix(s, "---") {
		return fm, fmt.Errorf("skill: SKILL.md missing frontmatter: %w", sourceserr.ErrUpstreamInvalid)
	}
	rest := s[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fm, fmt.Errorf("skill: SKILL.md frontmatter not terminated: %w", sourceserr.ErrUpstreamInvalid)
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return fm, fmt.Errorf("skill: SKILL.md frontmatter parse: %w", errors.Join(err, sourceserr.ErrUpstreamInvalid))
	}
	return fm, nil
}
