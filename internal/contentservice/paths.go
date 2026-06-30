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

// ErrInvalidScope is returned for an artifact request whose resolved
// scope (from the projection row) is neither "object" nor "directory".
// The SQL CHECK constraint should prevent this at write time; the gate
// here is defensive.
var ErrInvalidScope = errors.New("contentservice: invalid scope")

// PluginStoragePathWithinRoot validates an operator-written plugin
// storage_location before it is opened. Plugins are served directly from
// the projection row's StorageLocation (not recomputed via ResolvePath),
// so the path is NOT derived from a validated {name}. storage_location is
// operator-written today, but the schema permits future origin='ui' rows,
// so the value is treated as untrusted: it must be absolute and contained
// within cacheRoot. Returns the cleaned path and ok=false (caller → 404)
// when the location is relative or escapes the cache root (e.g.
// "/etc/passwd" or "<root>/../x"). Defense-in-depth against a row that
// would otherwise serve a file outside the cache.
func PluginStoragePathWithinRoot(cacheRoot, storageLocation string) (string, bool) {
	clean := filepath.Clean(storageLocation)
	if !filepath.IsAbs(clean) {
		return "", false
	}
	root := filepath.Clean(cacheRoot)
	if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return "", false
	}
	return clean, true
}

// ResolvePath returns the single on-disk filename for a (kind, name,
// scope) tuple under cacheRoot. Every context kind now resolves to
// name + ".tar.gz" (uniform context format): prompt, plugin, and skill
// always use .tar.gz; for kind=artifact both scope="object" (1-entry
// tar) and scope="directory" (subtree tar) resolve to name + ".tar.gz".
// Scope is still consumed for artifact only — an unknown scope is
// rejected with ErrInvalidScope.
//
// Returns ErrInvalidKind for unknown kinds, ErrInvalidName for any
// name that would traverse outside the kind subdirectory, and
// ErrInvalidScope for artifact requests with scope ∉ {object,directory}.
//
// Replaces the prior two-candidate signature (which existed because
// pre-Plan-05-05 the handler did not know the resolved scope — it
// stat-walked both candidates). After Plan 05-02 + 05-05, the
// resolved row carries the scope explicitly, so this function returns
// a single deterministic path.
func ResolvePath(cacheRoot, kind, name, scope string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	switch kind {
	case kindPrompt:
		return filepath.Join(cacheRoot, kindPrompt, name+gzipSuffix), nil
	case kindPlugin:
		return filepath.Join(cacheRoot, kindPlugin, name+gzipSuffix), nil
	case kindSkill:
		return filepath.Join(cacheRoot, kindSkill, name+gzipSuffix), nil
	case kindArtifact:
		switch scope {
		case "object", "directory":
			return filepath.Join(cacheRoot, kindArtifact, name+gzipSuffix), nil
		default:
			return "", ErrInvalidScope
		}
	}
	return "", ErrInvalidKind
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
