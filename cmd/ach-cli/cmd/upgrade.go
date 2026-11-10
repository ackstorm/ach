// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// Release locations and the running binary; tests point them elsewhere.
var (
	releaseLatestURL  = "https://api.github.com/repos/ackstorm/ach/releases/latest"
	releaseDownload   = "https://github.com/ackstorm/ach/releases/download"
	upgradeHTTPClient = &http.Client{Timeout: 5 * time.Minute}
	executable        = os.Executable
)

const onWindows = runtime.GOOS == "windows"

func newUpgradeCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Replace this ach-cli with the latest release",
		Long: `Download ach-cli from the GitHub releases page, check its sha256 against the
release's checksums.txt, and replace the running binary in place. Profiles in
~/.config/ach/config.yaml are untouched.

Without --version it installs the latest release. You must be able to write to
the binary's directory; for one under /usr/local/bin, run it with sudo.

Exit codes: 0 success, 1 failure (nothing replaced), 6 network error.`,
		Example: `  ach-cli upgrade
  ach-cli upgrade --version 1.1.8`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpgrade(cmd.Context(), newUI(cmd.OutOrStdout(), false), strings.TrimPrefix(version, "v"))
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "install this release (e.g. 1.1.8) instead of the latest")
	return cmd
}

func init() {
	rootCmd.AddCommand(newUpgradeCmd())
}

func runUpgrade(ctx context.Context, u *ui, target string) error {
	start := time.Now()
	if target == "" {
		u.verb("Checking", "latest release")
		latest, err := latestRelease(ctx)
		if err != nil {
			return err
		}
		target = latest
	}
	current := strings.TrimPrefix(Version, "v")
	if target == current {
		u.verb("Finished", "ach-cli %s is up to date", current)
		return nil
	}

	exe, err := executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return fmt.Errorf("locate the running ach-cli: %w", err)
	}

	goos, goarch, ext := runtime.GOOS, runtime.GOARCH, ".tar.gz"
	if onWindows {
		goarch, ext = "amd64", ".zip" // the only Windows build; it also runs on ARM
	}
	file := fmt.Sprintf("ach-cli_%s_%s_%s%s", target, goos, goarch, ext)
	base := releaseDownload + "/v" + target + "/"

	resp, err := get(ctx, base+file)
	if err != nil {
		return err
	}
	u.verb("Downloading", "ach-cli %s (%s/%s%s)", target, goos, goarch, sizeNote(resp.ContentLength))
	archive, err := readAll(resp)
	if err != nil {
		return err
	}

	u.verb("Verifying", "sha256 against checksums.txt")
	resp, err = get(ctx, base+"checksums.txt")
	if err != nil {
		return err
	}
	sums, err := readAll(resp)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archive)
	if want := checksumFor(sums, file); want == "" || want != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("checksum mismatch for %s; nothing was installed", file)
	}
	bin, err := extractBinary(archive, ext == ".zip")
	if err != nil {
		return fmt.Errorf("%s: %w; nothing was installed", file, err)
	}

	u.verb("Replacing", "%s", exe)
	if err := replaceBinary(exe, bin); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("cannot replace %s: permission denied\n"+
				"hint: rerun as `sudo ach-cli upgrade`, or move ach-cli to ~/.local/bin", exe)
		}
		return fmt.Errorf("cannot replace %s: %w", exe, err)
	}
	u.verb("Finished", "ach-cli %s → %s in %s", current, target, elapsed(time.Since(start)))
	return nil
}

// latestRelease returns the newest release's version, without its leading v.
func latestRelease(ctx context.Context) (string, error) {
	resp, err := get(ctx, releaseLatestURL)
	if err != nil {
		return "", err
	}
	body, err := readAll(resp)
	if err != nil {
		return "", err
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil || rel.TagName == "" {
		return "", fmt.Errorf("read the latest release from %s: no tag_name", releaseLatestURL)
	}
	return strings.TrimPrefix(rel.TagName, "v"), nil
}

// get returns a 200 response; a transport failure is a network error (exit 6).
func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := upgradeHTTPClient.Do(req)
	if err != nil {
		return nil, &exit.CodedError{Code: exit.Network, Msg: err.Error(), Wrapped: err}
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

func readAll(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &exit.CodedError{Code: exit.Network, Msg: err.Error(), Wrapped: err}
	}
	return b, nil
}

func sizeNote(n int64) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf(", %.1f MB", float64(n)/1e6)
}

// checksumFor finds file's hash in goreleaser's checksums.txt ("<sha256>  <file>").
func checksumFor(sums []byte, file string) string {
	for line := range strings.SplitSeq(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == file {
			return f[0]
		}
	}
	return ""
}

// extractBinary returns the ach-cli executable from a release archive.
func extractBinary(archive []byte, zipped bool) ([]byte, error) {
	if zipped {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == "ach-cli.exe" {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer func() { _ = rc.Close() }()
				return io.ReadAll(rc)
			}
		}
		return nil, errors.New("no ach-cli.exe in the archive")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("no ach-cli in the archive")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == "ach-cli" {
			return io.ReadAll(tr)
		}
	}
}

// replaceBinary writes bin next to exe and renames it over exe, so a failure
// leaves the old binary in place.
func replaceBinary(exe string, bin []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".ach-cli-upgrade-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone once renamed
	if _, err := tmp.Write(bin); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// #nosec G302 -- an executable needs its exec bit
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	if !onWindows {
		return os.Rename(tmp.Name(), exe)
	}
	// Windows cannot overwrite a running .exe, but it can rename it aside.
	old := exe + ".old"
	_ = os.Remove(old) // the previous upgrade's leftover
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		_ = os.Rename(old, exe)
		return err
	}
	return nil
}
