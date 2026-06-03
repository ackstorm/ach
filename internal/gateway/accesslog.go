// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AccessLogFormat selects the access-log line format. The gateway defaults
// to "combined" (the Apache/nginx combined log format) so operators get
// familiar, grep-able edge logs out of the box.
type AccessLogFormat string

const (
	// AccessLogCombined is the Apache/nginx combined log format:
	//   %h %l %u %t "%r" %>s %b "%{Referer}i" "%{User-Agent}i"
	AccessLogCombined AccessLogFormat = "combined"
	// AccessLogCommon is the Apache/nginx common log format (combined
	// without the Referer + User-Agent fields).
	AccessLogCommon AccessLogFormat = "common"
	// AccessLogOff disables access logging entirely.
	AccessLogOff AccessLogFormat = "off"
)

// ParseAccessLogFormat maps an env value to a format, defaulting to
// combined for empty/unknown input (fail-open to logging, never silently
// off). The second return reports whether the value was recognized.
func ParseAccessLogFormat(s string) (AccessLogFormat, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "combined":
		return AccessLogCombined, true
	case "common":
		return AccessLogCommon, true
	case "off", "none", "false", "0":
		return AccessLogOff, true
	default:
		return AccessLogCombined, false
	}
}

// accessLogWriter wraps http.ResponseWriter to capture the status code and
// the number of body bytes written — the %>s and %b combined-format fields.
// It exposes no body or header content (FWD-11 analog: never log payloads).
type accessLogWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (a *accessLogWriter) WriteHeader(code int) {
	if a.status == 0 {
		a.status = code
	}
	a.ResponseWriter.WriteHeader(code)
}

func (a *accessLogWriter) Write(b []byte) (int, error) {
	if a.status == 0 {
		a.status = http.StatusOK
	}
	n, err := a.ResponseWriter.Write(b)
	a.bytes += n
	return n, err
}

// Flush forwards to the inner writer so the FlushInterval=-1 streaming
// (SSE on /v1, streamable-http on /mcp) keeps working through the wrapper.
func (a *accessLogWriter) Flush() {
	if f, ok := a.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// AccessLog returns middleware that emits one Apache/nginx-style line per
// request to out. A mutex serializes writes so concurrent requests never
// interleave partial lines. Format AccessLogOff returns the handler
// unwrapped (zero overhead). The clock is injectable for tests via now;
// pass nil for time.Now.
func AccessLog(out io.Writer, format AccessLogFormat, now func() time.Time) func(http.Handler) http.Handler {
	if now == nil {
		now = time.Now
	}
	var mu sync.Mutex
	return func(next http.Handler) http.Handler {
		if format == AccessLogOff || out == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip the local readiness/liveness endpoint: k8s probes hit it
			// every few seconds and would drown real edge traffic.
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			start := now()
			alw := &accessLogWriter{ResponseWriter: w}
			next.ServeHTTP(alw, r)

			status := alw.status
			if status == 0 {
				status = http.StatusOK
			}
			line := formatAccessLine(format, r, status, alw.bytes, start)
			mu.Lock()
			_, _ = io.WriteString(out, line)
			mu.Unlock()
		})
	}
}

// formatAccessLine renders one combined/common log line (newline-terminated).
func formatAccessLine(format AccessLogFormat, r *http.Request, status, bytes int, t time.Time) string {
	host := clientIP(r)
	// Apache/nginx CLF timestamp: [02/Jan/2006:15:04:05 -0700]
	ts := t.Format("02/Jan/2006:15:04:05 -0700")
	// "%r" = method SP request-uri SP protocol
	reqLine := fmt.Sprintf("%s %s %s", r.Method, r.RequestURI, r.Proto)
	// %b: "-" when no body bytes (CLF convention), else the byte count.
	nb := "-"
	if bytes > 0 {
		nb = fmt.Sprintf("%d", bytes)
	}
	base := fmt.Sprintf("%s - - [%s] %q %d %s", host, ts, reqLine, status, nb)
	if format == AccessLogCommon {
		return base + "\n"
	}
	referer := r.Header.Get("Referer")
	if referer == "" {
		referer = "-"
	}
	ua := r.Header.Get("User-Agent")
	if ua == "" {
		ua = "-"
	}
	return fmt.Sprintf("%s %q %q\n", base, referer, ua)
}

// clientIP returns the originating client address. Behind the ACH Ingress
// the gateway sees the proxy as the peer, so prefer the left-most
// X-Forwarded-For hop (the real client) when present; otherwise fall back
// to the TCP peer address (host part of RemoteAddr).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if first != "" {
			return first
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
