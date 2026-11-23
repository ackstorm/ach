// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// MergeNative marks the single state row of a plugin delivered whole: its
// Target is the plugin's folder and its Hash covers the tree.
const MergeNative = "native"

// nativePlugins returns the stage directory names of the plugins the manifest
// delivers through the tool's own plugin system.
func nativePlugins(m *manifest.Manifest) map[string]bool {
	if m == nil || m.Context == nil {
		return nil
	}
	out := map[string]bool{}
	for _, ref := range m.Context.Plugins {
		if ref.Delivery == manifest.DeliveryNative {
			_, name := classifyDownloadURL(ref.DownloadURL, ref.Name)
			out[name] = true
		}
	}
	return out
}

// installNativePlugins installs the entries delivered natively and returns the
// rest, which are projected as files.
func (d *adapterDispatcherImpl) installNativePlugins(ctx context.Context, ad adapter.Adapter, s *state.File,
	entries []os.DirEntry, pluginRoot, envDir, toolRoot string, result *RenderResult) ([]os.DirEntry, error) {
	split := entries[:0:0]
	for _, ent := range entries {
		if !ent.IsDir() || !d.native[ent.Name()] {
			split = append(split, ent)
			continue
		}
		if verr := validatePluginName(ent.Name()); verr != nil {
			return nil, fmt.Errorf("adapter %s plugin directory %q: %w", d.platformID, ent.Name(), verr)
		}
		if err := d.installNative(ctx, ad, s, ent.Name(), filepath.Join(pluginRoot, ent.Name()), envDir, toolRoot, result); err != nil {
			return nil, err
		}
	}
	return split, nil
}

// installNative puts the staged plugin, unchanged, where the tool loads it
// from and registers it there. A tool the plugin has no manifest for gets
// nothing: the plugin is never split into files instead.
func (d *adapterDispatcherImpl) installNative(ctx context.Context, ad adapter.Adapter, s *state.File,
	plugin, src, envDir, toolRoot string, result *RenderResult) error {
	nt, ok := ad.(adapter.NativeTarget)
	if !ok {
		result.NativeWarnings = append(result.NativeWarnings,
			fmt.Sprintf("plugin %s uses native delivery, which ach-cli does not offer for %s: not installed", plugin, d.platformID))
		return nil
	}
	if !nt.NativeSupports(src) {
		result.NativeWarnings = append(result.NativeWarnings,
			fmt.Sprintf("plugin %s uses native delivery and carries no %s plugin manifest: not installed", plugin, d.platformID))
		return nil
	}
	// removeNative reads the same Environment name back from the path.
	p := adapter.NativePlugin{
		Env: filepath.Base(envDir), Name: plugin, ToolRoot: toolRoot, Global: d.global,
		Root: filepath.Join(envDir, "native", d.platformID),
	}
	target, err := filepath.Rel(toolRoot, p.Dir())
	if err != nil || !filepath.IsLocal(target) {
		return fmt.Errorf("plugin %s: native folder %s is outside %s", plugin, p.Dir(), toolRoot)
	}
	if !d.global {
		if err := state.CheckContained(toolRoot, p.Dir()); err != nil {
			return err
		}
	}
	sum, err := treeHash(src)
	if err != nil {
		return fmt.Errorf("plugin %s: %w", plugin, err)
	}
	entry := FileWrite{Target: filepath.ToSlash(target), Hash: sum, Merge: MergeNative, Source: plugin}
	var prior *state.FileEntry
	if s != nil {
		prior = findEntry(s.Plugins, entry.Target)
	}
	_, statErr := os.Stat(p.Dir())
	p.Changed = prior == nil || prior.Hash != sum || statErr != nil
	if d.dryRun {
		result.ProjectedFiles = append(result.ProjectedFiles, entry)
		return nil
	}
	if p.Changed {
		if err := adapter.CopyTree(src, p.Dir()); err != nil {
			return fmt.Errorf("plugin %s: %w", plugin, err)
		}
	}
	note, err := nt.NativeRegister(ctx, p)
	if errors.Is(err, adapter.ErrToolMissing) {
		// No row is recorded, so the next hydrate tries again.
		_ = os.RemoveAll(p.Dir())
		result.NativeWarnings = append(result.NativeWarnings, fmt.Sprintf("plugin %s not installed for %s: %v", plugin, d.platformID, err))
		return nil
	}
	if err != nil {
		return fmt.Errorf("plugin %s: register with %s: %w", plugin, d.platformID, err)
	}
	if note != "" && p.Changed {
		result.NativeNotes = append(result.NativeNotes, note)
	}
	result.ProjectedFiles = append(result.ProjectedFiles, entry)
	return nil
}

// removeNative unregisters a natively delivered plugin and deletes its tree.
// abs is <state dir>/native/<tool>/plugins/<plugin>.
func removeNative(abs string, opts SyncOptions) error {
	root := filepath.Dir(filepath.Dir(abs))
	if filepath.Base(filepath.Dir(abs)) != "plugins" || filepath.Base(filepath.Dir(root)) != "native" {
		return fmt.Errorf("native row %q is not <state dir>/native/<tool>/plugins/<plugin>: nothing removed", abs)
	}
	if ad, ok := adapter.Lookup(filepath.Base(root)); ok {
		if nt, ok := ad.(adapter.NativeTarget); ok {
			err := nt.NativeUnregister(context.Background(), adapter.NativePlugin{
				Env:  filepath.Base(filepath.Dir(filepath.Dir(root))),
				Name: filepath.Base(abs), Root: root, ToolRoot: opts.toolRoot, Global: opts.Global,
			})
			if errors.Is(err, adapter.ErrToolMissing) {
				_, _ = fmt.Fprintf(opts.Stderr, "warning: %s still holds %s: %v\n", filepath.Base(root), filepath.Base(abs), err)
			} else if err != nil {
				return err
			}
		}
	}
	if err := os.RemoveAll(abs); err != nil {
		return err
	}
	// Drop the folders this left empty, up to <state dir>/native. os.Remove
	// refuses a folder that still holds another plugin.
	for _, dir := range []string{filepath.Dir(abs), root, filepath.Dir(root)} {
		_ = os.Remove(dir)
	}
	return nil
}

// treeHash is a digest of every file of dir: its path, permission bits and
// bytes, and for a symlink its target.
func treeHash(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%o\x00", filepath.ToSlash(rel), info.Mode().Perm())
		if info.Mode()&fs.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			_, _ = io.WriteString(h, link)
			return err
		}
		f, err := os.Open(path) // #nosec G304 — a file of the plugin ach-cli extracted
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(h, f)
		return err
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
