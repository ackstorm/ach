// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ackstorm/ach/internal/cli/merge"
	"github.com/ackstorm/ach/internal/cli/state"
)

// NativeTarget is implemented by adapters whose tool loads a whole plugin
// from a folder through its own plugin system. The plugin tree is never
// transformed: the tool reads the manifest the plugin ships for it.
type NativeTarget interface {
	// NativeSupports reports whether the plugin tree at dir carries this
	// tool's own plugin manifest.
	NativeSupports(dir string) bool
	// NativeRegister makes the tool load the plugin and returns what the
	// user still has to do, if anything. It is idempotent and runs once the
	// tree is in place.
	NativeRegister(ctx context.Context, p NativePlugin) (note string, err error)
	// NativeUnregister undoes NativeRegister. It is idempotent and runs
	// before the tree is removed.
	NativeUnregister(ctx context.Context, p NativePlugin) error
}

// NativePlugin locates one natively delivered plugin. Root holds every native
// plugin of one Environment for one tool, as Root/plugins/<Name>.
type NativePlugin struct {
	Env, Name string
	Root      string // absolute
	ToolRoot  string // absolute: the workspace, or the home directory under --global
	Global    bool
	// Changed is false when the tool already holds this exact tree.
	Changed bool
}

// Dir is the plugin's own folder.
func (p NativePlugin) Dir() string { return filepath.Join(p.Root, "plugins", p.Name) }

var notMarketplaceChar = regexp.MustCompile(`[^a-z0-9-]+`)

// Marketplace names Root for the tools that load plugins through a
// marketplace. Claude Code and Codex key a marketplace by name for the whole
// user, so the suffix keeps two folders holding the same Environment apart.
func (p NativePlugin) Marketplace() string {
	sum := sha256.Sum256([]byte(p.Root))
	env := notMarketplaceChar.ReplaceAllString(strings.ToLower(p.Env), "-")
	return "ach-" + env + "-" + hex.EncodeToString(sum[:4])
}

// Names lists the plugins present under Root, sorted.
func (p NativePlugin) Names() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(p.Root, "plugins"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// EditJSON applies edit to the JSON object stored at path and removes the
// file when the object ends up empty. A missing file is an empty object.
func EditJSON(path string, edit func(doc map[string]any) (changed bool, err error)) error {
	empty := false
	err := state.RewriteFile(path, 0o600, func(cur []byte) ([]byte, bool, error) {
		doc, err := merge.ParseDoc(cur, false)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", path, err)
		}
		changed, err := edit(doc)
		if err != nil || !changed {
			return nil, false, err
		}
		empty = len(doc) == 0
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, false, err
		}
		out, err := merge.EncodeDoc(doc, false)
		return out, true, err
	})
	if err == nil && empty {
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return rerr
		}
	}
	return err
}

// JSONObject returns the object at doc[key], creating it when absent. A value
// of another type is an error, never replaced.
func JSONObject(doc map[string]any, key string) (map[string]any, error) {
	switch v := doc[key].(type) {
	case nil:
		obj := map[string]any{}
		doc[key] = obj
		return obj, nil
	case map[string]any:
		return v, nil
	default:
		return nil, fmt.Errorf("%q is not an object", key)
	}
}

// ErrToolMissing reports that a tool's own command is not on PATH.
var ErrToolMissing = errors.New("not found on PATH")

// RunTool runs a tool's own command and returns its last output line on
// failure. Tests replace it.
var RunTool = func(ctx context.Context, bin string, args ...string) error {
	path, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%s: %w", bin, ErrToolMissing)
	}
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput() // #nosec G204 — a fixed tool name and arguments ach-cli builds
	if err != nil {
		lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
		return fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, lines[len(lines)-1])
	}
	return nil
}

// CopyTree makes dst an exact copy of src: files, permission bits and
// symlinks, with nothing left from a previous copy.
func CopyTree(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		info, err := e.Info()
		if err != nil {
			return err
		}
		switch {
		case e.IsDir():
			return os.MkdirAll(out, 0o750)
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, out)
		case !info.Mode().IsRegular():
			return nil
		}
		in, err := os.Open(path) // #nosec G304 — a file of the plugin ach-cli extracted
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()) // #nosec G304 — inside a folder ach-cli owns
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, in); err != nil {
			_ = w.Close()
			return err
		}
		return w.Close()
	})
}
