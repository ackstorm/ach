// SPDX-License-Identifier: Apache-2.0

package hash

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/zeebo/xxh3"
)

// prefix is the literal string every output of this package carries. The
// state engine matches on this exact byte sequence; changing it would
// invalidate every on-disk state.json (which is fine across a clean-break
// schemaVersion bump per D-13, but would otherwise be a contract break).
const prefix = "xxh3:"

// Hash streams r through xxh3.New() via io.Copy and returns the canonical
// "xxh3:<32-char-lowercase-hex>" digest of the consumed bytes.
//
// The 128-bit output is encoded as a 32-character lowercase hex string
// (16 bytes × 2 hex chars each), big-endian, via xxh3.Uint128.Bytes().
//
// If the reader returns an error, Hash returns an empty string and the
// error wrapped with context — the caller (typically the state engine
// or extract pipeline) MUST treat this as a fatal failure for that
// resource. Silently returning a digest computed from partial input
// would lie about content.
func Hash(r io.Reader) (string, error) {
	h := New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("hash: read input: %w", err)
	}
	return h.Sum(), nil
}

// Writer is a streaming xxh3 hasher for callers that are ALREADY moving the
// bytes for another reason — typically an io.MultiWriter that spills a body to
// disk. Hashing in that same pass avoids re-opening and re-reading the file
// just to digest it.
//
// It exists so the canonical "xxh3:<32hex>" format stays owned by this package:
// prefix is unexported precisely because the state engine matches on it
// byte-for-byte, so callers must not assemble the digest string themselves.
//
// Sum may be called once the writes are done; it does not close anything.
type Writer struct{ h *xxh3.Hasher }

// New returns a Writer ready to accept bytes.
func New() *Writer { return &Writer{h: xxh3.New()} }

// Write implements io.Writer. The underlying xxh3 hasher never errors.
func (w *Writer) Write(p []byte) (int, error) { return w.h.Write(p) }

// Sum returns the canonical "xxh3:<32-char-lowercase-hex>" digest of every byte
// written so far — identical to what Hash/HashBytes/HashFile produce for the
// same content.
func (w *Writer) Sum() string {
	sum := w.h.Sum128().Bytes()
	return prefix + hex.EncodeToString(sum[:])
}

// HashBytes is the in-memory convenience form of Hash. It produces the
// same canonical "xxh3:<32-char-lowercase-hex>" digest for the given
// byte slice — `HashBytes(b) == Hash(bytes.NewReader(b))` for every b.
//
// There is no error path: no I/O is performed, and xxh3.Hash128 is a
// pure function over the input slice.
func HashBytes(b []byte) string {
	sum := xxh3.Hash128(b).Bytes()
	return prefix + hex.EncodeToString(sum[:])
}

// HashFile returns the canonical "xxh3:<32hex>" digest of the file at
// path. Wraps Hash with file-open/close discipline. The caller passes
// paths it controls (staging dir / prior state ledger), so the file
// open is trusted.
func HashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is caller-controlled (staging dir / prior state ledger)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return Hash(f)
}
