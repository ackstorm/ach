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

// detectArchiveRoot returns the single common leading segment shared by ALL
// entries (the REST wrapper dir), or "" when entries are already root-relative
// (git transport, or a true multi-top-level repo).
func detectArchiveRoot(names []string) string {
	root := ""
	for _, n := range names {
		clean := path.Clean(strings.TrimPrefix(n, "./"))
		first := clean
		if i := strings.IndexByte(clean, '/'); i >= 0 {
			first = clean[:i]
		} else {
			return "" // a file sits at top level → no single wrapper root
		}
		if root == "" {
			root = first
		} else if root != first {
			return "" // entries don't share one root → not wrapped
		}
	}
	return root
}

// discoverSkillsInTree takes the staged marketplace tar.gz BYTES (the reconciler
// already holds them, size-capped by the fetcher) and returns every TOP-LEVEL
// (post-root-strip) directory D with a valid D/SKILL.md whose frontmatter name
// passes validateSkillName AND equals basename(D), with a non-empty description.
// Convention-based — agentskills.io has no index file. The returned archiveRoot
// must be passed to sliceSkillSubtree so it strips the same wrapper.
//
// Deliberate scope: a SKILL.md at the archive ROOT (depth 0) and skills nested
// deeper than one level are IGNORED — a marketplace lists each skill as a
// single top-level directory.
func discoverSkillsInTree(tarball []byte) (archiveRoot string, skills []discoveredSkill, err error) {
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
		rel := normRel(hdr.Name, root)
		if path.Base(rel) != "SKILL.md" || strings.Count(rel, "/") != 1 {
			continue // only top-level <dir>/SKILL.md after root-strip
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

// sliceSkillSubtree re-packs entries under "<dir>/" (post-root-strip) into a
// fresh tar.gz rooted at "<dir>/" so the result passes verifySkillContents.
// Stage-2 stores it at skill-marketplace/<marketplace>/<name>.tar.gz. archiveRoot
// is the value discoverSkillsInTree returned.
func sliceSkillSubtree(tarball []byte, archiveRoot, dir string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("skillmkt: gzip open: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	var buf bytes.Buffer
	outGz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(outGz)
	prefix := dir + "/"
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
		if rel != dir && !strings.HasPrefix(rel, prefix) {
			continue
		}
		nh := &tar.Header{Name: rel, Mode: hdr.Mode, Size: hdr.Size, Typeflag: hdr.Typeflag, ModTime: hdr.ModTime}
		if err := tw.WriteHeader(nh); err != nil {
			return nil, fmt.Errorf("skillmkt: write header %s: %w", rel, err)
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
		return nil, fmt.Errorf("skillmkt: subtree %q empty", dir)
	}
	return buf.Bytes(), nil
}
