// SPDX-License-Identifier: Apache-2.0

package cachefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageAtomic(t *testing.T) {
	root := t.TempDir()

	// Happy path: bytes land, file is closed, lives under .tmp.
	p, n, err := StageAtomic(root, strings.NewReader("hello"), 0)
	if err != nil {
		t.Fatalf("StageAtomic: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if filepath.Dir(p) != filepath.Join(root, ".tmp") {
		t.Errorf("staging path %q not under .tmp", p)
	}
	got, rerr := os.ReadFile(p)
	if rerr != nil || string(got) != "hello" {
		t.Errorf("staged content = %q, %v", got, rerr)
	}

	// Oversize: ErrOversize, temp file removed.
	p2, _, err := StageAtomic(root, strings.NewReader("0123456789"), 4)
	if !errors.Is(err, ErrOversize) {
		t.Fatalf("err = %v, want ErrOversize", err)
	}
	if p2 != "" {
		if _, serr := os.Stat(p2); !errors.Is(serr, os.ErrNotExist) {
			t.Errorf("oversize staging file not removed: %v", serr)
		}
	}
}
