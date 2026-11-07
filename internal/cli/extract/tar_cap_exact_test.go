// SPDX-License-Identifier: Apache-2.0

package extract_test

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ackstorm/ach/internal/cli/extract"
)

func regEntry(name string, body []byte) tarEntry {
	return tarEntry{hdr: &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: int64(len(body))}, body: body}
}

// TestExtract_CapFilledExactly asserts an archive that fills the cap exactly
// still refuses the next non-empty entry, and accepts an empty one.
func TestExtract_CapFilledExactly(t *testing.T) {
	limits := generousLimits()
	limits.MaxExtractedPluginBytes = 100
	first := regEntry("a.bin", bytes.Repeat([]byte("A"), 100))

	_, err := extract.Extract(context.Background(),
		bytes.NewReader(makeTarGz(t, []tarEntry{first, regEntry("b.bin", []byte("x"))})),
		t.TempDir(), extract.KindPlugin, limits, false)
	if !errors.Is(err, extract.ErrBombCapExceeded) {
		t.Fatalf("entry after a full budget: want ErrBombCapExceeded, got %v", err)
	}

	if _, err := extract.Extract(context.Background(),
		bytes.NewReader(makeTarGz(t, []tarEntry{first, regEntry("empty.bin", nil)})),
		t.TempDir(), extract.KindPlugin, limits, false); err != nil {
		t.Fatalf("empty entry at the cap: %v", err)
	}
}
