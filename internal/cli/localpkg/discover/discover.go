// SPDX-License-Identifier: Apache-2.0

// Package discover classifies a cloned repo tarball into the capability lenses it provides.
package discover

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"path"

	"github.com/ackstorm/ach/internal/cli/localpkg/store"
	"github.com/ackstorm/ach/internal/contentkit"
)

// Lens string values.
const (
	LensPluginMarketplace = "plugin-marketplace"
	LensSkillMarketplace  = "skill-marketplace"
	LensPlugin            = "plugin"
	LensSkill             = "skill"
)

// Detect inspects a gzipped-tar repo snapshot and returns the lenses it provides
// (possibly several). skillsRootHint is the optional --path narrow ("" → autodetect).
// Returns a non-nil (possibly empty) slice.
func Detect(tarball []byte, skillsRootHint string) ([]store.Capability, error) {
	caps := []store.Capability{}

	// Step 1: plugin-marketplace — scan for marketplace.json.
	mktBytes, found := extractFile(tarball, func(name string) bool {
		clean := path.Clean(name)
		return clean == ".claude-plugin/marketplace.json" || clean == "marketplace.json"
	})

	hasMarketplace := false
	if found {
		mkt, err := contentkit.ParseClaudeCodeMarketplace(mktBytes)
		if err == nil {
			caps = append(caps, store.Capability{Lens: LensPluginMarketplace, Count: len(mkt.Plugins)})
			hasMarketplace = true
		}
	}

	// Step 2: skill-marketplace — try roots.
	var skillRoots []string
	if skillsRootHint != "" {
		skillRoots = []string{skillsRootHint}
	} else {
		skillRoots = []string{"", "skills"}
	}
	for _, root := range skillRoots {
		_, skills, err := contentkit.DiscoverSkillsInTree(tarball, root)
		if err == nil && len(skills) >= 1 {
			caps = append(caps, store.Capability{Lens: LensSkillMarketplace, Count: len(skills)})
			break
		}
	}

	// Step 3: plugin (direct) — only if no marketplace.json was found.
	if !hasMarketplace {
		if err := contentkit.VerifyPluginContents(bytes.NewReader(tarball)); err == nil {
			caps = append(caps, store.Capability{Lens: LensPlugin, Count: 1})
		}
	}

	// Step 4: skill (direct).
	if err := contentkit.VerifySkillContents(bytes.NewReader(tarball)); err == nil {
		caps = append(caps, store.Capability{Lens: LensSkill, Count: 1})
	}

	return caps, nil
}

// extractFile walks the gzipped-tar once and returns the bytes of the first
// entry whose cleaned name satisfies match. found is false when no entry matches.
func extractFile(tarball []byte, match func(name string) bool) ([]byte, bool) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, false
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, false
		}
		if err != nil {
			return nil, false
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if match(hdr.Name) {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, false
			}
			return data, true
		}
	}
}
