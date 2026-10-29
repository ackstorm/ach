// SPDX-License-Identifier: Apache-2.0

// Package evidence reads per-user observability evidence (Langfuse traces in
// ClickHouse, later Thanos series). Every read takes a Scope; there is no
// unscoped query path.
package evidence

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Config is the read-only ClickHouse HTTP endpoint. The limits default to the
// observability reads; the conversation classifier raises them because it
// reads one raw session input at a time, measured up to 22.5 MB.
type Config struct {
	URL, User, Password string
	MaxResultBytes      int64 // default 1 MiB
	MaxMemoryBytes      int64 // default 900 MB
}

func (c Config) resultBytes() int64 { return cmp.Or(c.MaxResultBytes, 1<<20) }
func (c Config) memoryBytes() int64 { return cmp.Or(c.MaxMemoryBytes, 900_000_000) }

// ClickHouse is a thin read-only client over the HTTP interface.
type ClickHouse struct {
	cfg Config
	hc  *http.Client
}

func NewClickHouse(cfg Config, hc *http.Client) *ClickHouse {
	if hc == nil {
		hc = &http.Client{Timeout: 35 * time.Second}
	}
	return &ClickHouse{cfg: cfg, hc: hc}
}

// Query runs sql (which must end in FORMAT JSON) with typed {name:Type}
// parameters and decodes the "data" array into out. maxRows is enforced
// server-side via max_result_rows. Errors never carry upstream text, which can
// echo the SQL, parameters or credentials.
func (c *ClickHouse) Query(ctx context.Context, sql string, params map[string]string, maxRows int, out any) error {
	q := url.Values{
		"readonly":           {"2"},
		"max_execution_time": {"30"},
		// Grouping a 31-day window reads metadata arrays for tens of thousands of
		// spans; 512 MiB failed live (MEMORY_LIMIT_EXCEEDED). The server profile caps at 1 GB.
		"max_memory_usage": {strconv.FormatInt(c.cfg.memoryBytes(), 10)},
		"max_threads":      {"2"},
		"max_result_rows":  {strconv.Itoa(maxRows)},
		"max_result_bytes": {strconv.FormatInt(c.cfg.resultBytes(), 10)},
		// Overflow must fail the read, never truncate it into apparent evidence.
		"result_overflow_mode": {"throw"},
		// Counts and durations arrive as JSON numbers, not quoted strings.
		"output_format_json_quote_64bit_integers": {"0"},
	}
	for k, v := range params {
		q.Set("param_"+k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL+"/?"+q.Encode(), bytes.NewReader([]byte(sql)))
	if err != nil {
		return errors.New("clickhouse: bad request")
	}
	req.SetBasicAuth(c.cfg.User, c.cfg.Password)
	resp, err := c.hc.Do(req)
	if err != nil {
		return errors.New("clickhouse: request failed") // *url.Error embeds the URL and params
	}
	defer func() { _ = resp.Body.Close() }()
	// The JSON envelope can be twice the result: escaping plus the column metadata.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*c.cfg.resultBytes()))
	if err != nil {
		return errors.New("clickhouse: read body")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clickhouse: status %d", resp.StatusCode)
	}
	var env struct {
		Data      json.RawMessage `json:"data"`
		Exception json.RawMessage `json:"exception"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return errors.New("clickhouse: decode")
	}
	// HTTP 200 can still carry an exception (and partial data) in valid JSON.
	if len(env.Exception) > 0 && string(env.Exception) != "null" {
		return errors.New("clickhouse: query exception")
	}
	if len(env.Data) == 0 || env.Data[0] != '[' {
		return errors.New("clickhouse: data is not an array")
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return errors.New("clickhouse: decode rows")
	}
	return nil
}
