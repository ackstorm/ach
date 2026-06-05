// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// discoveredSkill is one skill folder found inside a SkillMarketplace tree.
type discoveredSkill struct {
	Name        string // SKILL.md frontmatter name (== top-level dir basename)
	Dir         string // top-level directory within the fetched tree
	Description string
}

// Caps (defensive — explicit bounds, not just a nolint comment).
const (
	skillMarketplaceMaxSkills    = 2000
	skillMarketplaceMaxFileBytes = 64 << 20 // 64 MiB per file inside a sliced subtree
)

// normRel strips the optional single archive-root wrapper that REST archive
// APIs add ("<repo>-<sha>/<rest>"). Git-protocol fetches already strip it, but
// GitHub/GitLab/Bitbucket REST archives wrap everything in one root dir. We
// detect it once and strip it from every entry so depth math is consistent
// across transports.
func normRel(name, root string) string {
	clean := path.Clean(strings.TrimPrefix(name, "./"))
	if root != "" {
		clean = strings.TrimPrefix(clean, root+"/")
	}
	return clean
}

// normSubPath cleans a spec.<git>.path into a slash-trimmed relative path
// ("" when empty/root). The skill dirs are discovered directly under
// archiveRoot + this subPath.
func normSubPath(p string) string {
	p = path.Clean(strings.Trim(strings.TrimSpace(p), "/"))
	if p == "." || p == "/" {
		return ""
	}
	return p
}

// relUnderSubPath strips the skills-root subPath prefix from an entry path that
// has already had the archive-root wrapper removed. ok=false when the entry is
// not under subPath. subPath == "" passes every entry through unchanged.
func relUnderSubPath(rel, subPath string) (string, bool) {
	if subPath == "" {
		return rel, true
	}
	if rel == subPath || !strings.HasPrefix(rel, subPath+"/") {
		return "", false
	}
	return strings.TrimPrefix(rel, subPath+"/"), true
}

// detectArchiveRoot returns the single common leading segment shared by ALL
// entries (the REST wrapper dir), or "" when entries are already root-relative
// (git transport, or a true multi-top-level repo).
func detectArchiveRoot(names []string) string {
	root := ""
	for _, n := range names {
		clean := path.Clean(strings.TrimPrefix(n, "./"))
		i := strings.IndexByte(clean, '/')
		if i < 0 {
			return "" // a file sits at top level → no single wrapper root
		}
		first := clean[:i]
		if root == "" {
			root = first
		} else if root != first {
			return "" // entries don't share one root → not wrapped
		}
	}
	return root
}

// discoverSkillsInTree takes the staged marketplace tar.gz BYTES (the reconciler
// already holds them, size-capped by the fetcher) and returns every directory D
// directly under the skills-root (archiveRoot + subPath) with a valid D/SKILL.md
// whose frontmatter name passes validateSkillName AND equals basename(D), with a
// non-empty description. Convention-based — agentskills.io has no index file.
// subPath is spec.<git>.path: the directory inside the repo that holds the skill
// dirs ("skills" for an anthropics/skills-style monorepo, "" when the skills sit
// at the repo root). The returned archiveRoot must be passed (with the same
// subPath) to sliceSkillSubtree so it strips the same wrapper.
//
// Deliberate scope: only directories EXACTLY one level under the skills-root are
// skills — a SKILL.md at the skills-root itself, or nested deeper than one
// level, is IGNORED.
func discoverSkillsInTree(tarball []byte, subPath string) (archiveRoot string, skills []discoveredSkill, err error) {
	subPath = normSubPath(subPath)
	names, err := tarRegularNames(tarball)
	if err != nil {
		return "", nil, err
	}
	root := detectArchiveRoot(names)

	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return "", nil, fmt.Errorf("skillmkt: gzip open: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	found := map[string]discoveredSkill{}
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", nil, fmt.Errorf("skillmkt: tar read: %w", e)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		rel, ok := relUnderSubPath(normRel(hdr.Name, root), subPath)
		if !ok {
			continue
		}
		if path.Base(rel) != "SKILL.md" || strings.Count(rel, "/") != 1 {
			continue // only <skills-root>/<dir>/SKILL.md
		}
		dir := path.Dir(rel)
		body, e := io.ReadAll(io.LimitReader(tr, skillMaxManifestBytes))
		if e != nil {
			return "", nil, fmt.Errorf("skillmkt: read %s: %w", rel, e)
		}
		fm, e := parseSkillFrontmatter(body)
		if e != nil {
			continue // malformed → not a valid skill
		}
		if validateSkillName(fm.Name) != nil || fm.Name != dir || fm.Description == "" || len(fm.Description) > 1024 {
			continue // name must be valid AND equal the dir basename
		}
		found[dir] = discoveredSkill{Name: fm.Name, Dir: dir, Description: fm.Description}
		if len(found) > skillMarketplaceMaxSkills {
			return "", nil, fmt.Errorf("skillmkt: more than %d skills in marketplace", skillMarketplaceMaxSkills)
		}
	}
	out := make([]discoveredSkill, 0, len(found))
	for _, s := range found {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return root, out, nil
}

// tarRegularNames returns the cleaned names of all regular-file entries (one
// streaming pass) so detectArchiveRoot can run before the body pass.
func tarRegularNames(tarball []byte) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("skillmkt: gzip open: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("skillmkt: tar read: %w", e)
		}
		if hdr.Typeflag == tar.TypeReg {
			names = append(names, hdr.Name)
		}
	}
	return names, nil
}

// sliceSkillSubtree re-packs entries under "<subtreePath>/" (post-archive-root
// strip) into a fresh tar.gz RE-ROOTED at the path's last segment ("<base>/"),
// so the result passes verifySkillContents (SKILL.md one dir deep) and hydrate's
// single-wrapper strip lands it at .claude/skills/<name>/SKILL.md. subtreePath
// is relative to archiveRoot — e.g. "skills/pdf" for an anthropics/skills-style
// repo (subPath "skills" + skill dir "pdf"), or just "pdf-processing" when the
// skill sits at the repo root. Stage-2 stores the result at
// skill-marketplace/<marketplace>/<name>.tar.gz.
func sliceSkillSubtree(tarball []byte, archiveRoot, subtreePath string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("skillmkt: gzip open: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	var buf bytes.Buffer
	outGz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(outGz)
	base := path.Base(subtreePath)
	prefix := subtreePath + "/"
	wrote := false
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("skillmkt: tar read: %w", e)
		}
		rel := normRel(hdr.Name, archiveRoot)
		if rel != subtreePath && !strings.HasPrefix(rel, prefix) {
			continue
		}
		// Re-root: replace the subtreePath prefix with its last segment.
		rerooted := base + strings.TrimPrefix(rel, subtreePath)
		nh := &tar.Header{Name: rerooted, Mode: hdr.Mode, Size: hdr.Size, Typeflag: hdr.Typeflag, ModTime: hdr.ModTime}
		if err := tw.WriteHeader(nh); err != nil {
			return nil, fmt.Errorf("skillmkt: write header %s: %w", rerooted, err)
		}
		if hdr.Typeflag == tar.TypeReg {
			// Explicit per-file byte cap.
			n, cErr := io.Copy(tw, io.LimitReader(tr, skillMarketplaceMaxFileBytes+1))
			if cErr != nil {
				return nil, fmt.Errorf("skillmkt: copy %s: %w", rel, cErr)
			}
			if n > skillMarketplaceMaxFileBytes {
				return nil, fmt.Errorf("skillmkt: %s exceeds %d bytes", rel, skillMarketplaceMaxFileBytes)
			}
		}
		wrote = true
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := outGz.Close(); err != nil {
		return nil, err
	}
	if !wrote {
		return nil, fmt.Errorf("skillmkt: subtree %q empty", subtreePath)
	}
	return buf.Bytes(), nil
}
