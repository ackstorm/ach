//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type a2aMCPHeaders http.Header

func (h a2aMCPHeaders) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h {
		r.Header[k] = v
	}
	return http.DefaultTransport.RoundTrip(r)
}

// testPhase4SC2A2AMCP — /a2a/<name>/mcp: an agent the Environment does not
// grant is 403 unauthorized_resource (as on /a2a/<name>); for demo-agent,
// tools/list carries the card from LiteLLM and send_message streams the
// mock's working update as progress, then returns its answer.
func testPhase4SC2A2AMCP(t *testing.T) {
	forwarderURL := os.Getenv("ACH_FORWARDER_URL")
	if forwarderURL == "" {
		t.Fatalf("ACH_FORWARDER_URL not set — required for a phase4 run (set ACH_SKIP_PHASE4=1 to opt out).")
	}
	ek := mustAcquireEkBoundToEnv(t, "demo")
	waitForwarderPrecheckAuthorized(t, forwarderURL+"/a2a/demo-agent/", ek)

	req, _ := http.NewRequest(http.MethodPost, forwarderURL+"/a2a/not-granted/mcp", strings.NewReader(`{}`))
	req.Header.Set("x-ach-key", ek)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("not-granted mcp request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	if resp.StatusCode != http.StatusForbidden || env.Error.Code != "unauthorized_resource" {
		t.Fatalf("not-granted: status=%d code=%q body=%s; want 403 unauthorized_resource", resp.StatusCode, env.Error.Code, body)
	}

	progress := make(chan string, 8)
	c := mcp.NewClient(&mcp.Implementation{Name: "ach-e2e", Version: "0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			progress <- r.Params.Message
		},
	})
	cs, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   forwarderURL + "/a2a/demo-agent/mcp",
		HTTPClient: &http.Client{Transport: a2aMCPHeaders{"X-Ach-Key": {ek}}},
	}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "send_message" {
		t.Fatalf("tools = %+v; want exactly send_message", tools.Tools)
	}
	if !strings.Contains(tools.Tools[0].Description, "Echo agent for ACH e2e") {
		t.Errorf("description %q lacks the seeded card description", tools.Tools[0].Description)
	}

	p := &mcp.CallToolParams{Name: "send_message", Arguments: map[string]any{"message": "ping"}}
	p.SetProgressToken("e2e")
	res, err := cs.CallTool(t.Context(), p)
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError || !strings.Contains(text, "echo: ping") || !strings.Contains(text, "context_id: ") {
		t.Fatalf("send_message: isError=%v text=%q", res.IsError, text)
	}
	// The client delivers notifications on its own goroutine, so one may land
	// after CallTool returns.
	select {
	case m := <-progress:
		if m != "thinking" {
			t.Errorf("progress = %q; want the mock's working update, thinking", m)
		}
	case <-time.After(10 * time.Second):
		t.Error("no progress notification from send_message")
	}
}
