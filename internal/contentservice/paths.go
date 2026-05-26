// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrInvalidKind is returned by ResolvePath for unknown kinds. Callers
// surface this as 404 (the router only registers known kinds, so this
// is defensive — a misrouted request, not a missing file).
var ErrInvalidKind = errors.New("contentservice: invalid kind")

// ErrInvalidName is returned when the {name} URL parameter would
// escape the cache subdirectory (contains "/", "..", or starts with
// "."). The router rejects these with 400.
var ErrInvalidName = errors.New("contentservice: invalid name")

// ResolvePath returns the on-disk filename for a (kind, name) pair
// under cacheRoot. For artifact, both candidate paths are returned —
// the caller stats each in order (.tar.gz preferred, then bare name)
// to disambiguate scope=directory vs scope=object without needing the
// CR. The slice is ordered most-specific first.
//
// Returns ErrInvalidKind for unknown kinds and ErrInvalidName for any
// name that would traverse outside the kind subdirectory.
func ResolvePath(cacheRoot, kind, name string) ([]string, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	switch kind {
	case kindPrompt:
		return []string{filepath.Join(cacheRoot, kindPrompt, name)}, nil
	case kindPlugin:
		return []string{filepath.Join(cacheRoot, kindPlugin, name+gzipSuffix)}, nil
	case kindArtifact:
		// Try .tar.gz first (scope=directory is the more common
		// case for the v1alpha1 examples corpus); fall through to
		// bare name (scope=object) if the gzip variant is absent.
		return []string{
			filepath.Join(cacheRoot, kindArtifact, name+gzipSuffix),
			filepath.Join(cacheRoot, kindArtifact, name),
		}, nil
	}
	return nil, ErrInvalidKind
}

func validateName(name string) error {
	if name == "" {
		return ErrInvalidName
	}
	if strings.ContainsAny(name, "/\\") {
		return ErrInvalidName
	}
	if strings.HasPrefix(name, ".") {
		return ErrInvalidName
	}
	if name == "." || name == ".." {
		return ErrInvalidName
	}
	return nil
}
