// SPDX-License-Identifier: Apache-2.0

package a2amcp

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	agentURL, _ := newCountedAgent(t)
	return agentURL
}

// newCountedAgent also counts the JSON-RPC calls the agent receives.
func newCountedAgent(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			calls.Add(1)
		}
		mux.ServeHTTP(w, r)
	}))
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
	return srv.URL + "/a2a/echo", &calls
}

func connect(t *testing.T, agentURL string, progress func(string)) *mcp.ClientSession {
	t.Helper()
	cred := func(context.Context) map[string]string { return map[string]string{"x-ach-key": "ek_test"} }
	h := Handler(func(*http.Request) (string, string) { return "echo", agentURL }, cred)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			progress(r.Params.Message)
		},
	})
	cs, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
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

// LiteLLM serves the card with securitySchemes in the A2A 0.3 shape, which
// a2a-go's AgentCard rejects; the description must still come through.
func TestToolDescribesAgentFromLiteLLMCard(t *testing.T) {
	var fetches atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/a2a/echo/.well-known/agent-card.json", func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		_, _ = w.Write([]byte(`{"name":"echo","description":"Echoes what it is sent.",` +
			`"skills":[{"id":"echo","name":"Echo","description":"repeats the message"}],` +
			`"security":[{"LiteLLMKey":[]}],` +
			`"securitySchemes":{"LiteLLMKey":{"type":"http","scheme":"bearer","description":"LiteLLM virtual key"}},` +
			`"capabilities":{"streaming":true},"protocolVersion":"1.0"}`))
	})
	litellm := httptest.NewServer(mux)
	t.Cleanup(litellm.Close)
	cs := connect(t, litellm.URL+"/a2a/echo", func(string) {})
	before := fetches.Load()
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := res.Tools[0].Description; !strings.Contains(d, "Echoes what it is sent.") || !strings.Contains(d, "Echo: repeats the message") {
		t.Errorf("description %q lacks the card's description and skill", d)
	}
	if n := fetches.Load() - before; n != 1 {
		t.Errorf("tools/list fetched the card %d times, want 1", n)
	}
}

func TestSendMessageWithoutRestartNeverPolls(t *testing.T) {
	agentURL, calls := newCountedAgent(t)
	cs := connect(t, agentURL, func(string) {})
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "send_message", Arguments: map[string]any{"message": "hi"}})
	if err != nil || res.IsError {
		t.Fatalf("err=%v result=%+v", err, res)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("the agent received %d JSON-RPC calls, want only the stream", n)
	}
}

// restartingAgent answers in the wire shape LiteLLM serves. Its stream ends on
// a working update, cleanly or by dropping the connection. GetTask then
// answers 503 twice and returns the task in state final; with no final state
// it answers 503 every time. The counters are the GetTask and stream calls.
func restartingAgent(t *testing.T, drop bool, final string) (agentURL string, gets, sends *atomic.Int32) {
	t.Helper()
	gets, sends = new(atomic.Int32), new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage
			Method string
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "SendStreamingMessage":
			sends.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, `data: {"jsonrpc":"2.0","id":%s,"result":{"statusUpdate":{"taskId":"task-1","contextId":"ctx-1",`+
				`"status":{"state":"TASK_STATE_WORKING","message":{"role":"ROLE_AGENT","parts":[{"text":"The agent is restarting."}]}}}}}`+"\n\n", req.ID)
			w.(http.Flusher).Flush()
			if drop {
				panic(http.ErrAbortHandler)
			}
		case "GetTask":
			if gets.Add(1) <= 2 || final == "" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"id":"task-1","contextId":"ctx-1",`+
				`"status":{"state":%q,"message":{"role":"ROLE_AGENT","parts":[{"text":"pong"}]}},`+
				`"history":[{"role":"ROLE_AGENT","parts":[{"text":"Starting the agent"}]}]}}`, req.ID, final)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, gets, sends
}

func fastPoll(t *testing.T, wait time.Duration) {
	t.Helper()
	oldMin, oldMax, oldWait := pollMin, pollMax, maxWait
	pollMin, pollMax, maxWait = 5*time.Millisecond, 10*time.Millisecond, wait
	t.Cleanup(func() { pollMin, pollMax, maxWait = oldMin, oldMax, oldWait })
}

func TestSendMessageFetchesTheTaskWhenTheStreamEndsEarly(t *testing.T) {
	fastPoll(t, time.Minute)
	for _, tc := range []struct {
		name    string
		drop    bool
		final   string
		isError bool
	}{
		{"stream closed, task completed", false, "TASK_STATE_COMPLETED", false},
		{"connection dropped, task completed", true, "TASK_STATE_COMPLETED", false},
		{"stream closed, task failed", false, "TASK_STATE_FAILED", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentURL, gets, sends := restartingAgent(t, tc.drop, tc.final)
			progress := make(chan string, 16)
			cs := connect(t, agentURL, func(m string) { progress <- m })
			p := &mcp.CallToolParams{Name: "send_message", Arguments: map[string]any{"message": "hi"}}
			p.SetProgressToken("tok")
			res, err := cs.CallTool(t.Context(), p)
			if err != nil {
				t.Fatal(err)
			}
			text := res.Content[0].(*mcp.TextContent).Text
			if res.IsError != tc.isError || text != "pong\n\ncontext_id: ctx-1" {
				t.Fatalf("result isError=%v text=%q", res.IsError, text)
			}
			if n := gets.Load(); n != 3 {
				t.Errorf("GetTask calls = %d, want 3 (two 503s, then the task)", n)
			}
			if n := sends.Load(); n != 1 {
				t.Errorf("the message was sent %d times, want 1", n)
			}
			// The stream's text, then one notification per failed poll.
			for _, want := range []string{"The agent is restarting.", "Waiting for the agent to finish.", "Waiting for the agent to finish."} {
				select {
				case m := <-progress:
					if m != want {
						t.Errorf("progress = %q; want %q", m, want)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("no progress notification %q", want)
				}
			}
		})
	}
}

func TestSendMessageNamesTheTaskWhenTheWaitRunsOut(t *testing.T) {
	fastPoll(t, 100*time.Millisecond)
	agentURL, gets, _ := restartingAgent(t, false, "")
	cs := connect(t, agentURL, func(string) {})
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "send_message", Arguments: map[string]any{"message": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, "task-1") {
		t.Fatalf("result isError=%v text=%q; want a tool error naming task-1", res.IsError, text)
	}
	if gets.Load() < 3 {
		t.Errorf("GetTask calls = %d, want the poll to keep retrying", gets.Load())
	}
}
