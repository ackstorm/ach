// SPDX-License-Identifier: Apache-2.0

package evidence

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func chServer(t *testing.T, status int, body string) (*ClickHouse, *http.Request, *[]byte) {
	t.Helper()
	var got http.Request
	var sent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		sent, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewClickHouse(Config{URL: srv.URL, User: "ro", Password: "s3cret"}, srv.Client()), &got, &sent
}

func TestQuerySendsReadonlyTypedParamsAndDecodesRows(t *testing.T) {
	c, got, sent := chServer(t, 200, `{"data":[{"a":"1"}],"rows":1}`)
	var rows []map[string]any
	if err := c.Query(context.Background(), "SELECT {x:String} AS a FORMAT JSON", map[string]string{"x": "1"}, 51, &rows); err != nil {
		t.Fatal(err)
	}
	q := got.URL.Query()
	for k, want := range map[string]string{
		"param_x": "1", "readonly": "2", "max_result_rows": "51", "max_memory_usage": "900000000",
		"result_overflow_mode":                    "throw",
		"output_format_json_quote_64bit_integers": "0",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if u, p, _ := got.BasicAuth(); u != "ro" || p != "s3cret" {
		t.Error("basic auth missing")
	}
	if string(*sent) != "SELECT {x:String} AS a FORMAT JSON" {
		t.Errorf("sql must travel in the body, got %q", *sent)
	}
	if len(rows) != 1 || rows[0]["a"] != "1" {
		t.Errorf("rows = %v", rows)
	}
}

func TestQueryFailuresNeverLeakCredentialsOrBody(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"non-200":            {403, "Code: 497 denied for ro s3cret"},
		"200 with exception": {200, `{"data":[{"a":1}],"exception":"Code: 241 s3cret"}`},
		"null data":          {200, `{"data":null}`},
		"missing data":       {200, `{"rows":0}`},
		"cut mid-json":       {200, `{"data":[{"a":`},
	} {
		t.Run(name, func(t *testing.T) {
			c, _, _ := chServer(t, tc.status, tc.body)
			err := c.Query(context.Background(), "SELECT 1 FORMAT JSON", nil, 1, new([]map[string]any))
			if err == nil {
				t.Fatal("want error")
			}
			if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "Code:") {
				t.Fatalf("error leaks upstream text: %v", err)
			}
		})
	}
}
