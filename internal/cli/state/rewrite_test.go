// SPDX-License-Identifier: Apache-2.0

package state_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

// appendX is the rewrite under test: it returns the current bytes plus "X".
func appendX(cur []byte) ([]byte, bool, error) {
	return append(append([]byte(nil), cur...), 'X'), true, nil
}

// An editor save between the read and the write is kept: the rewrite runs
// again on the saved bytes instead of overwriting them.
func TestRewriteFile_RerunsOnEditDuringRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := state.RewriteFile(path, 0o600, func(cur []byte) ([]byte, bool, error) {
		calls++
		if calls == 1 {
			if err := os.WriteFile(path, []byte("user-edit"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return appendX(cur)
	})
	if err != nil {
		t.Fatalf("RewriteFile: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "user-editX" || calls != 2 {
		t.Fatalf("got %q after %d calls, want %q after 2", got, calls, "user-editX")
	}
}

// A file that changes on every attempt is left as the user wrote it.
func TestRewriteFile_GivesUpWhenFileKeepsChanging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	n := 0
	err := state.RewriteFile(path, 0o644, func(cur []byte) ([]byte, bool, error) {
		n++
		if err := os.WriteFile(path, []byte{byte('0' + n)}, 0o644); err != nil {
			t.Fatal(err)
		}
		return appendX(cur)
	})
	if !errors.Is(err, state.ErrConcurrentEdit) {
		t.Fatalf("err = %v, want ErrConcurrentEdit", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "3" {
		t.Fatalf("file = %q, want the user's last write %q", got, "3")
	}
}

// An absent file reaches fn as nil and is created; an existing empty file
// reaches fn as a non-nil empty slice.
func TestRewriteFile_AbsentVersusEmpty(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "absent.json")
	var sawNil bool
	if err := state.RewriteFile(absent, 0o600, func(cur []byte) ([]byte, bool, error) {
		sawNil = cur == nil
		return appendX(cur)
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(absent); !sawNil || string(got) != "X" {
		t.Fatalf("absent: sawNil=%v file=%q", sawNil, got)
	}

	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.RewriteFile(empty, 0o600, func(cur []byte) ([]byte, bool, error) {
		sawNil = cur == nil
		return appendX(cur)
	}); err != nil {
		t.Fatal(err)
	}
	if sawNil {
		t.Fatal("empty existing file reached fn as nil")
	}
}

// write=false leaves the file untouched.
func TestRewriteFile_NoWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	if err := state.RewriteFile(path, 0o644, func([]byte) ([]byte, bool, error) {
		return []byte("ignored"), false, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file written despite write=false: %v", err)
	}
}
