// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpgrade(t *testing.T) {
	if onWindows {
		t.Skip("the fake release is a tar.gz")
	}
	file := fmt.Sprintf("ach-cli_9.9.9_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archive := tarGz(t, map[string]string{"README.md": "readme", "ach-cli": "new-binary"})
	sum := sha256.Sum256(archive)

	for _, tc := range []struct {
		name, current, sums string
		wantOut, wantErr    string
		replaced            bool
	}{
		{name: "installs the latest", current: "1.0.0", sums: hex.EncodeToString(sum[:]) + "  " + file + "\n",
			wantOut: "Finished ach-cli 1.0.0 → 9.9.9", replaced: true},
		{name: "checksum mismatch", current: "1.0.0", sums: strings.Repeat("0", 64) + "  " + file + "\n",
			wantErr: "checksum mismatch for " + file + "; nothing was installed"},
		{name: "up to date", current: "9.9.9", wantOut: "Finished ach-cli 9.9.9 is up to date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/latest":
					_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
				case "/download/v9.9.9/" + file:
					_, _ = w.Write(archive)
				case "/download/v9.9.9/checksums.txt":
					_, _ = w.Write([]byte(tc.sums))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			exe := filepath.Join(t.TempDir(), "ach-cli")
			if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			oldLatest, oldDownload, oldClient := releaseLatestURL, releaseDownload, upgradeHTTPClient
			oldExe, oldVersion := executable, Version
			t.Cleanup(func() {
				releaseLatestURL, releaseDownload, upgradeHTTPClient = oldLatest, oldDownload, oldClient
				executable, Version = oldExe, oldVersion
			})
			releaseLatestURL, releaseDownload = srv.URL+"/latest", srv.URL+"/download"
			upgradeHTTPClient, Version = srv.Client(), tc.current
			executable = func() (string, error) { return exe, nil }

			out, _, _, err := executeCommand(t, newUpgradeCmd())

			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("upgrade: %v", err)
			}
			if !strings.Contains(out, tc.wantOut) {
				t.Fatalf("stdout %q lacks %q", out, tc.wantOut)
			}
			got, _ := os.ReadFile(exe)
			if want := map[bool]string{true: "new-binary", false: "old-binary"}[tc.replaced]; string(got) != want {
				t.Fatalf("binary = %q, want %q", got, want)
			}
			if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".ach-cli-upgrade-*")); len(leftovers) > 0 {
				t.Fatalf("temp files left behind: %v", leftovers)
			}
		})
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		h := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
