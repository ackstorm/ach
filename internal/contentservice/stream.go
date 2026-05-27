// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"io"
	"net/http"
	"os"
	"strconv"
)

// stream writes the response for a successful Content Service GET. It
// performs the §15.6 D-01 custom serve path:
//
//   - sets Content-Type, Content-Length (exact, from size), Cache-Control:
//     no-store (CS-06 + D-01),
//   - writes 200 OK (CS-08 — Range / If-None-Match / If-Modified-Since /
//     If-Match / If-Unmodified-Since headers on r are NEVER inspected,
//     full body always),
//   - copies the file body via io.Copy. On Linux with a *net.TCPConn
//     underlying writer this engages sendfile(2) through
//     *os.File.WriteTo (D-01 — http.ServeContent is deliberately NOT
//     used).
//
// The caller (pipeline.go) is responsible for opening the *os.File
// EARLY (D-02 — before the staleness gate) and holding it until this
// function returns. That early-open discipline pins the inode against a
// concurrent Operator atomic rename(2) (SC#4); this function does NOT
// open the file.
//
// Returns the number of bytes successfully copied (which equals size on
// success) and io.Copy's error (typically a write error on premature
// client disconnect).
func stream(w http.ResponseWriter, _ *http.Request, f *os.File, contentType string, size int64) (int64, error) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	return io.Copy(w, f)
}
