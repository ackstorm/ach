// SPDX-License-Identifier: Apache-2.0

package a2amcp

import (
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// echoAgent streams one working update, then completes with
// "echo: <text> key=<x-ach-key>"; the text "fail" fails the task instead.
type echoAgent struct{}

func (echoAgent) Execute(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if ec.StoredTask == nil && !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
			return
		}
		text := ec.Message.Parts[0].Text()
		if text == "fail" {
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateFailed,
				a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart("boom"))), nil)
			return
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking,
			a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart("thinking"))), nil) {
			return
		}
		key, _ := ec.ServiceParams.Get("x-ach-key")
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted,
			a2a.NewMessageForTask(a2a.MessageRoleAgent, ec,
				a2a.NewTextPart("echo: "+text+" key="+strings.Join(key, ",")))), nil)
	}
}

func (echoAgent) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

func newAgent(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	card := &a2a.AgentCard{
		Name: "echo", Description: "Echoes what it is sent.", Version: "1.0.0",
		Capabilities:        a2a.AgentCapabilities{Streaming: true},
		DefaultInputModes:   []string{"text/plain"},
		DefaultOutputModes:  []string{"text/plain"},
		Skills:              []a2a.AgentSkill{{ID: "echo", Name: "Echo", Description: "repeats the message"}},
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://card-url-must-not-be-used.invalid", a2a.TransportProtocolJSONRPC)},
	}
	mux.Handle("/a2a/echo"+a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/a2a/echo", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(echoAgent{})))
	return srv.URL + "/a2a/echo"
}

type headerRT http.Header

func (h headerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h {
		r.Header[k] = v
	}
	return http.DefaultTransport.RoundTrip(r)
}

func connect(t *testing.T, agentURL string, progress func(string)) *mcp.ClientSession {
	t.Helper()
	h := Handler(func(*http.Request) (string, string) { return "echo", agentURL }, []string{"x-ach-key", "Authorization"})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			progress(r.Params.Message)
		},
	})
	cs, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Transport: headerRT{"X-Ach-Key": {"ek_test"}}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestToolDescribesAgentFromCard(t *testing.T) {
	cs := connect(t, newAgent(t), func(string) {})
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "send_message" {
		t.Fatalf("tools = %+v; want exactly send_message", res.Tools)
	}
	d := res.Tools[0].Description
	for _, want := range []string{"Echoes what it is sent.", "Echo: repeats the message", "context_id"} {
		if !strings.Contains(d, want) {
			t.Errorf("description %q lacks %q", d, want)
		}
	}
}

func TestSendMessageStreamsProgressAndAnswers(t *testing.T) {
	progress := make(chan string, 8)
	cs := connect(t, newAgent(t), func(m string) { progress <- m })
	p := &mcp.CallToolParams{Name: "send_message", Arguments: map[string]any{"message": "hi"}}
	p.SetProgressToken("tok")
	res, err := cs.CallTool(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError || !strings.Contains(text, "echo: hi key=ek_test") || !strings.Contains(text, "context_id: ") {
		t.Fatalf("result isError=%v text=%q", res.IsError, text)
	}
	// The client delivers notifications on its own goroutine, so one may land
	// after CallTool returns.
	select {
	case m := <-progress:
		if m != "thinking" {
			t.Errorf("progress = %q; want thinking", m)
		}
	case <-time.After(5 * time.Second):
		t.Error("no progress notification")
	}
}

func TestSendMessageFailedTaskIsToolError(t *testing.T) {
	cs := connect(t, newAgent(t), func(string) {})
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "send_message", Arguments: map[string]any{"message": "fail"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "boom") {
		t.Fatalf("result isError=%v content=%+v; want tool error carrying boom", res.IsError, res.Content)
	}
}
