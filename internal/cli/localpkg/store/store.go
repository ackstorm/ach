// SPDX-License-Identifier: Apache-2.0

// Package store persists ach-cli local package-manager state under ~/.config/ach/local/.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/state"
)

// Capability describes a single lens a repo provides and how many
// objects of that kind were discovered in the last scan.
type Capability struct {
	Lens  string `json:"lens"` // "plugin-marketplace"|"skill-marketplace"|"plugin"|"skill"
	Count int    `json:"count"`
}

// RepoEntry is one entry in repos.json describing a registered repo.
type RepoEntry struct {
	Name        string       `json:"name"`
	Source      string       `json:"source"`
	Kind        string       `json:"kind"` // "github"|"git"|"local"
	CloneURL    string       `json:"cloneURL,omitempty"`
	GitRef      string       `json:"gitRef,omitempty"`
	LocalPath   string       `json:"localPath,omitempty"`
	AuthScheme  string       `json:"authScheme,omitempty"`
	HasToken    bool         `json:"hasToken"`
	Provides    []Capability `json:"provides"`
	DetectedSHA string       `json:"detectedSHA,omitempty"`
	AddedAt     string       `json:"addedAt"`
}

// ReposFile is the top-level schema for repos.json.
type ReposFile struct {
	Version int         `json:"version"`
	Repos   []RepoEntry `json:"repos"`
}

// FileRec records one file that was installed as part of a package.
type FileRec struct {
	RelPath string `json:"relPath"`
	Hash    string `json:"hash"`
}

// InstalledEntry is one entry in installed.json describing a currently
// installed plugin or skill.
type InstalledEntry struct {
	Ref         string    `json:"ref"` // "<name>@<repo>"
	Repo        string    `json:"repo"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`   // "plugin"|"skill"
	Target      string    `json:"target"` // adapter id
	ResolvedSHA string    `json:"resolvedSHA,omitempty"`
	Files       []FileRec `json:"files"`
	InstalledAt string    `json:"installedAt"`
}

// InstalledFile is the top-level schema for installed.json.
type InstalledFile struct {
	Version   int              `json:"version"`
	Installed []InstalledEntry `json:"installed"`
}

// Dir returns ~/.config/ach/local, creating it with mode 0700.
func Dir() (string, error) {
	cfg, err := config.Path()
	if err != nil {
		return "", fmt.Errorf("store: resolve config path: %w", err)
	}
	d := filepath.Join(filepath.Dir(cfg), "local")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", fmt.Errorf("store: mkdir local dir: %w", err)
	}
	return d, nil
}

// LoadRepos returns the parsed repos.json, or &ReposFile{Version:1}
// when the file is absent.
func LoadRepos() (*ReposFile, error) {
	path, err := reposPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ReposFile{Version: 1}, nil
		}
		return nil, fmt.Errorf("store: read repos.json: %w", err)
	}
	var f ReposFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("store: parse repos.json: %w", err)
	}
	return &f, nil
}

// SaveRepos writes f to repos.json atomically at mode 0600.
func SaveRepos(f *ReposFile) error {
	path, err := reposPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal repos.json: %w", err)
	}
	if err := state.WriteAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("store: write repos.json: %w", err)
	}
	return nil
}

// LoadInstalled returns the parsed installed.json, or
// &InstalledFile{Version:1} when the file is absent.
func LoadInstalled() (*InstalledFile, error) {
	path, err := installedPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &InstalledFile{Version: 1}, nil
		}
		return nil, fmt.Errorf("store: read installed.json: %w", err)
	}
	var f InstalledFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("store: parse installed.json: %w", err)
	}
	return &f, nil
}

// SaveInstalled writes f to installed.json atomically at mode 0600.
func SaveInstalled(f *InstalledFile) error {
	path, err := installedPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal installed.json: %w", err)
	}
	if err := state.WriteAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("store: write installed.json: %w", err)
	}
	return nil
}

// LoadToken returns the token stored for repo, or "" when absent.
// Credentials live in a separate credentials.json file (map[string]string).
func LoadToken(repo string) (string, error) {
	m, err := loadCredentials()
	if err != nil {
		return "", err
	}
	return m[repo], nil
}

// SaveToken persists token for repo in credentials.json (0600).
func SaveToken(repo, token string) error {
	m, err := loadCredentials()
	if err != nil {
		return err
	}
	m[repo] = token
	return saveCredentials(m)
}

// DeleteToken removes the entry for repo from credentials.json. It is
// a no-op (no error) when the key is absent.
func DeleteToken(repo string) error {
	m, err := loadCredentials()
	if err != nil {
		return err
	}
	if _, ok := m[repo]; !ok {
		return nil
	}
	delete(m, repo)
	return saveCredentials(m)
}

// ---- internal helpers -------------------------------------------------------

func reposPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "repos.json"), nil
}

func installedPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "installed.json"), nil
}

func credentialsPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "credentials.json"), nil
}

// loadCredentials reads credentials.json as map[string]string.
// Returns an empty map when the file is absent.
func loadCredentials() (map[string]string, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("store: read credentials.json: %w", err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("store: parse credentials.json: %w", err)
	}
	return m, nil
}

// saveCredentials writes the credentials map atomically at mode 0600.
func saveCredentials(m map[string]string) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal credentials.json: %w", err)
	}
	if err := state.WriteAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("store: write credentials.json: %w", err)
	}
	return nil
}
