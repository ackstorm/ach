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

// ResolvePath returns the single on-disk filename for a (kind, name,
// scope) tuple under cacheRoot. Scope is consumed only for
// kind=artifact: scope="object" → bare name, scope="directory" →
// name + ".tar.gz". For prompt/plugin scope is ignored — plugin always
// uses .tar.gz, prompt always uses the bare name.
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
		return filepath.Join(cacheRoot, kindPrompt, name), nil
	case kindPlugin:
		return filepath.Join(cacheRoot, kindPlugin, name+gzipSuffix), nil
	case kindArtifact:
		switch scope {
		case "object":
			return filepath.Join(cacheRoot, kindArtifact, name), nil
		case "directory":
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
