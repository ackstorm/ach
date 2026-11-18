// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

type toolSession struct {
	*mcp.ClientSession
	wire *toolWire
}

type runtimeToolResult struct {
	*mcp.CallToolResult
	Raw json.RawMessage
}

func (r runtimeToolResult) MarshalJSON() ([]byte, error) { return r.Raw.MarshalJSON() }

func openToolSession(ctx context.Context, c cred, verbose bool, kind, name string) (*toolSession, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\%?#") {
		return nil, fmt.Errorf("invalid %s name %q", kind, name)
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/" + kind + "/" + url.PathEscape(name)
	switch kind {
	case "mcp":
	case a2aKind:
		endpoint += "/mcp"
	default:
		return nil, fmt.Errorf("unknown tool service %q", kind)
	}
	wire := &toolWire{bearer: c.Bearer, verbose: verbose}
	wire.Transport = &mcp.StreamableClientTransport{
		Endpoint: endpoint, DisableStandaloneSSE: true,
		HTTPClient: &http.Client{
			Transport:     wire,
			CheckRedirect: toolRedirect,
		},
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "ach-cli", Version: Version}, nil)
	cs, err := client.Connect(ctx, wire, nil)
	if err != nil {
		return nil, wire.redactError(err)
	}
	return &toolSession{ClientSession: cs, wire: wire}, nil
}

func toolRedirect(next *http.Request, via []*http.Request) error {
	first := via[0]
	status := next.Response.StatusCode
	if len(via) == 1 && next.Method == first.Method &&
		next.URL.Scheme == first.URL.Scheme && next.URL.Host == first.URL.Host &&
		next.URL.User == nil && next.URL.RawQuery == "" && next.URL.EscapedPath() == first.URL.EscapedPath()+"/" &&
		(status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect) {
		return nil
	}
	return http.ErrUseLastResponse
}

func listRuntimeTools(ctx context.Context, cs *toolSession, server string) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	params := &mcp.ListToolsParams{}
	for {
		page, err := cs.ListTools(ctx, params)
		if err != nil {
			return tools, cs.wire.redactError(err)
		}
		if outcomes, ok := page.Meta["litellm.ai/server_outcomes"].(map[string]any); ok {
			outcome, _ := outcomes[server].(map[string]any)
			switch status, _ := outcome["status"].(string); status {
			case "ok":
			case "auth_required":
				return tools, fmt.Errorf("provider sign-in required: run ach-cli mcp login %s", server)
			default:
				return tools, cs.wire.redactError(fmt.Errorf("could not query %s (gateway status %q)", server, status))
			}
		}
		// Keep numeric constraints and defaults exact in the schema printed by describe.
		var schemas struct {
			Tools []struct {
				Name         string
				InputSchema  json.RawMessage
				OutputSchema json.RawMessage
			}
		}
		if err := json.Unmarshal(cs.wire.response.Load().Result, &schemas); err != nil {
			return tools, err
		}
		// The SDK filters invalid tools and preserves the order of the rest.
		i := 0
		for _, raw := range schemas.Tools {
			if i < len(page.Tools) && page.Tools[i].Name == raw.Name {
				page.Tools[i].InputSchema = raw.InputSchema
				if raw.OutputSchema != nil {
					page.Tools[i].OutputSchema = raw.OutputSchema
				}
				i++
			}
		}
		tools = append(tools, page.Tools...)
		if page.NextCursor == "" {
			return tools, nil
		}
		params.Cursor = page.NextCursor
	}
}

func callRuntimeTool(
	ctx context.Context, cs *toolSession, name string, args json.RawMessage,
) (*runtimeToolResult, error) {
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, cs.wire.redactError(err)
	}
	return &runtimeToolResult{CallToolResult: result, Raw: cs.wire.response.Load().Result}, nil
}

// Each CLI session issues one request at a time. Keep the wire result for JSON
// output: the SDK's typed result rounds large numbers and drops extension fields.
type toolWire struct {
	mcp.Transport
	conn     mcp.Connection
	bearer   string
	verbose  bool
	response atomic.Pointer[jsonrpc.Response]
	protocol atomic.Pointer[string]
}

func (w *toolWire) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := w.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	w.conn = conn
	return &toolConnection{Connection: conn, wire: w}, nil
}

type toolConnection struct {
	mcp.Connection
	wire *toolWire
}

func (c *toolConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	msg, err := c.Connection.Read(ctx)
	if resp, ok := msg.(*jsonrpc.Response); ok && resp.Error == nil {
		c.wire.response.Store(resp)
		// The SDK's private session-update hook cannot reach a wrapped connection.
		if c.wire.protocol.Load() == nil {
			var init struct{ ProtocolVersion string }
			if json.Unmarshal(resp.Result, &init) == nil && init.ProtocolVersion != "" {
				c.wire.protocol.Store(&init.ProtocolVersion)
			}
		}
	}
	return msg, err
}

func (w *toolWire) RoundTrip(r *http.Request) (*http.Response, error) {
	req := r.Clone(r.Context())
	req.Header.Set("x-ach-key", w.bearer)
	if version := w.protocol.Load(); version != nil && req.Header.Get("Mcp-Protocol-Version") == "" {
		req.Header.Set("Mcp-Protocol-Version", *version)
	}
	if w.verbose {
		headers := req.Header.Clone()
		for k := range headers {
			if strings.EqualFold(k, "Mcp-Session-Id") || strings.EqualFold(k, "Authorization") ||
				strings.HasPrefix(strings.ToLower(k), "mcp-param-") {
				headers.Set(k, "redacted")
			}
		}
		_, _ = fmt.Fprintf(os.Stderr, "%s %s %s\n%s",
			req.Method, req.URL.EscapedPath(), req.Proto, httpclient.HeaderDump(headers))
	}
	return http.DefaultTransport.RoundTrip(req)
}

func (w *toolWire) redactError(err error) error {
	message := err.Error()
	if strings.Contains(message, "mismatching session IDs") {
		return errors.New("gateway returned conflicting MCP session IDs")
	}
	secrets := []string{w.bearer}
	if w.conn != nil {
		secrets = append(secrets, w.conn.SessionID())
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "redacted")
		}
	}
	if message != err.Error() {
		return errors.New(message)
	}
	return err
}
