// SPDX-License-Identifier: Apache-2.0

// Package a2amcp serves the MCP face of an A2A agent: one tool,
// send_message, relayed to the agent with A2A SendStreamingMessage. Working
// updates become MCP progress notifications; the call returns when the task
// completes, fails or asks for input. A stream that ends before that (the
// agent restarted) is followed by GetTask polls on the same task.
package a2amcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sendIn struct {
	Message   string `json:"message" jsonschema:"the message for the agent"`
	ContextID string `json:"context_id,omitempty" jsonschema:"context_id from an earlier reply, to continue that conversation"`
}

// Handler returns the stateless streamable-HTTP MCP handler for one agent.
// target maps the request to the agent's name and the A2A JSON-RPC URL the
// tool calls; cred returns the headers sent with the card fetch and every A2A
// call.
func Handler(target func(*http.Request) (name, agentURL string), cred func(context.Context) map[string]string) http.Handler {
	hc := &http.Client{} // no Timeout: it would cut the SSE body mid-stream
	// The SDK calls getServer more than once per request, so the server is
	// built once here and handed over through the request context.
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return r.Context().Value(serverKey{}).(*mcp.Server)
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, agentURL := target(r)
		// ponytail: the card is fetched on every POST (stateless: one server
		// per request); cache it per agent if card latency shows up.
		card := fetchCard(r.Context(), hc, agentURL, cred(r.Context()))

		s := mcp.NewServer(&mcp.Implementation{Name: "a2a-" + name, Version: "1.0.0"}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: "send_message", Description: describe(name, card)},
			func(ctx context.Context, req *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, any, error) {
				return send(ctx, hc, agentURL, cred(ctx), req, in)
			})
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serverKey{}, s)))
	})
}

type serverKey struct{}

// GetTask polling after a stream that ended early: the delay doubles from
// pollMin to pollMax, and the whole wait is capped at maxWait.
var (
	pollMin = 2 * time.Second
	pollMax = 10 * time.Second
	maxWait = 30 * time.Minute
)

const pollTimeout = 30 * time.Second

func send(ctx context.Context, hc *http.Client, agentURL string, cred map[string]string,
	req *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, any, error) {
	cl, err := a2aclient.NewFromEndpoints(ctx,
		[]*a2a.AgentInterface{a2a.NewAgentInterface(agentURL, a2a.TransportProtocolJSONRPC)},
		a2aclient.WithJSONRPCTransport(hc))
	if err != nil {
		return nil, nil, err
	}
	sp := a2aclient.ServiceParams{}
	for k, v := range cred {
		sp.Append(k, v)
	}
	ctx = a2aclient.AttachServiceParams(ctx, sp)

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(in.Message))
	msg.ContextID = in.ContextID
	token := req.Params.GetProgressToken()
	var n float64
	progress := func(text string) {
		if token != nil {
			n++
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: n, Message: text})
		}
	}
	var taskID a2a.TaskID
	for ev, err := range cl.SendStreamingMessage(ctx, &a2a.SendMessageRequest{Message: msg}) {
		if err != nil {
			if taskID == "" {
				return nil, nil, err
			}
			break // the task exists: its record has the outcome
		}
		var status a2a.TaskStatus
		var contextID string
		switch e := ev.(type) {
		case *a2a.Message:
			return result(e.ContextID, e.Parts, false), nil, nil
		case *a2a.Task:
			taskID, status, contextID = e.ID, e.Status, e.ContextID
		case *a2a.TaskStatusUpdateEvent:
			taskID, status, contextID = e.TaskID, e.Status, e.ContextID
		default:
			continue
		}
		if res := settled(status, contextID); res != nil {
			return res, nil, nil
		}
		if text := statusText(status); status.State == a2a.TaskStateWorking && text != "" {
			progress(text)
		}
	}
	if taskID == "" {
		return nil, nil, errors.New("the agent ended the stream without a final reply")
	}
	return await(ctx, cl, taskID, progress)
}

// await polls the task until it settles. The agent's endpoint is down for
// some seconds while it restarts, so a failed poll is retried; each poll
// sends progress, which keeps the caller's stream from idling out.
func await(ctx context.Context, cl *a2aclient.Client, id a2a.TaskID, progress func(string)) (*mcp.CallToolResult, any, error) {
	ctx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	for delay := pollMin; ; delay = min(2*delay, pollMax) {
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("the agent did not finish task %s in time; its result stays available through GetTask on that task", id)
		case <-time.After(delay):
		}
		text := "Waiting for the agent to finish."
		// ponytail: every GetTask error is retried until maxWait; stop early on
		// the permanent ones if a dead agent is seen to hold callers that long.
		pctx, pcancel := context.WithTimeout(ctx, pollTimeout)
		task, err := cl.GetTask(pctx, &a2a.GetTaskRequest{ID: id})
		pcancel()
		if err == nil {
			if res := settled(task.Status, task.ContextID); res != nil {
				return res, nil, nil
			}
			if t := statusText(task.Status); t != "" {
				text = t
			}
		}
		progress(text)
	}
}

// settled returns the tool result for a status that ends the call, or nil
// while the task is still running.
func settled(status a2a.TaskStatus, contextID string) *mcp.CallToolResult {
	var parts a2a.ContentParts
	if status.Message != nil {
		parts = status.Message.Parts
	}
	switch status.State {
	case a2a.TaskStateCompleted, a2a.TaskStateInputRequired:
		return result(contextID, parts, false)
	case a2a.TaskStateFailed, a2a.TaskStateCanceled, a2a.TaskStateRejected, a2a.TaskStateAuthRequired:
		return result(contextID, parts, true)
	}
	return nil
}

func statusText(status a2a.TaskStatus) string {
	if status.Message == nil {
		return ""
	}
	return joinText(status.Message.Parts)
}

// agentCard is the part of the agent card the tool description uses. It is
// decoded on its own because LiteLLM serves securitySchemes in the A2A 0.3
// shape, which a2a-go's AgentCard rejects as a whole.
type agentCard struct {
	Description string `json:"description"`
	Skills      []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"skills"`
}

// fetchCard returns the agent's card, or nil when it cannot be read.
func fetchCard(ctx context.Context, hc *http.Client, agentURL string, cred map[string]string) *agentCard {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL+a2asrv.WellKnownAgentCardPath, nil)
	if err != nil {
		return nil
	}
	for k, v := range cred {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	var card agentCard
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&card) != nil {
		return nil
	}
	return &card
}

// describe builds the tool description from the agent card; without one
// (fetch failed) it names the agent only.
func describe(name string, card *agentCard) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Send a message to the agent %s and wait for its reply.", name)
	if card != nil {
		if card.Description != "" {
			b.WriteString("\n\n" + card.Description)
		}
		for _, s := range card.Skills {
			fmt.Fprintf(&b, "\n- %s: %s", s.Name, s.Description)
		}
	}
	b.WriteString("\n\nThe reply ends with a context_id line: pass that value as context_id to continue the same conversation.")
	return b.String()
}

func result(contextID string, parts a2a.ContentParts, isErr bool) *mcp.CallToolResult {
	text := joinText(parts)
	if contextID != "" {
		text += "\n\ncontext_id: " + contextID
	}
	return &mcp.CallToolResult{IsError: isErr, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func joinText(parts a2a.ContentParts) string {
	var out []string
	for _, p := range parts {
		if t := p.Text(); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, "\n")
}
