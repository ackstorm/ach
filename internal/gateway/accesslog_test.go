// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	t := time.Date(2026, time.June, 3, 12, 30, 45, 0, time.FixedZone("UTC", 0))
	return func() time.Time { return t }
}

func TestParseAccessLogFormat(t *testing.T) {
	cases := map[string]struct {
		want  AccessLogFormat
		known bool
	}{
		"":         {AccessLogCombined, true},
		"combined": {AccessLogCombined, true},
		"COMMON":   {AccessLogCommon, true},
		" off ":    {AccessLogOff, true},
		"none":     {AccessLogOff, true},
		"bogus":    {AccessLogCombined, false},
	}
	for in, exp := range cases {
		got, known := ParseAccessLogFormat(in)
		if got != exp.want || known != exp.known {
			t.Errorf("ParseAccessLogFormat(%q) = %q,%v want %q,%v", in, got, known, exp.want, exp.known)
		}
	}
}

func TestAccessLogCombined(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(&buf, AccessLogCombined, fixedClock())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat?x=1", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	req.Header.Set("User-Agent", "curl/8")
	req.Header.Set("Referer", "http://ref/")
	h.ServeHTTP(httptest.NewRecorder(), req)

	got := buf.String()
	want := `10.0.0.5 - - [03/Jun/2026:12:30:45 +0000] "GET /v1/chat?x=1 HTTP/1.1" 418 5 "http://ref/" "curl/8"` + "\n"
	if got != want {
		t.Errorf("line mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestAccessLogCommonOmitsRefererUA(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(&buf, AccessLogCommon, fixedClock())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/healthz-not", nil)
	req.RemoteAddr = "10.0.0.5:1"
	h.ServeHTTP(httptest.NewRecorder(), req)
	if strings.Contains(buf.String(), `"`) && strings.Count(buf.String(), `"`) != 2 {
		t.Errorf("common format should have exactly the request-line quotes: %q", buf.String())
	}
	// No body written => %b is "-", status defaults 200.
	if !strings.Contains(buf.String(), `" 200 -`+"\n") {
		t.Errorf("want status 200 and %%b=-, got %q", buf.String())
	}
}

func TestAccessLogXForwardedFor(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(&buf, AccessLogCombined, fixedClock())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:1"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.5")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.HasPrefix(buf.String(), "203.0.113.7 ") {
		t.Errorf("want left-most XFF as client IP, got %q", buf.String())
	}
}

func TestAccessLogHealthzSkipped(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(&buf, AccessLogCombined, fixedClock())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if buf.Len() != 0 {
		t.Errorf("/healthz should not be logged, got %q", buf.String())
	}
}

func TestAccessLogOffNoWrap(t *testing.T) {
	var buf bytes.Buffer
	called := false
	h := AccessLog(&buf, AccessLogOff, fixedClock())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called || buf.Len() != 0 {
		t.Errorf("off mode should pass through with no log; called=%v buf=%q", called, buf.String())
	}
}
