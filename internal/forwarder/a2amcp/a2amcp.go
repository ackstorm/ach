// SPDX-License-Identifier: Apache-2.0

// Package a2amcp serves the MCP face of an A2A agent: one tool,
// send_message, relayed to the agent with A2A SendStreamingMessage. Working
// updates become MCP progress notifications; the call returns when the task
// completes, fails or asks for input.
package a2amcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sendIn struct {
	Message   string `json:"message" jsonschema:"the message for the agent"`
	ContextID string `json:"context_id,omitempty" jsonschema:"context_id from an earlier reply, to continue that conversation"`
}

// Handler returns the stateless streamable-HTTP MCP handler for one agent.
// target maps the request to the agent's name and the A2A JSON-RPC URL the
// tool calls; credHeaders are copied from the MCP request onto the card fetch
// and every A2A call.
func Handler(target func(*http.Request) (name, agentURL string), credHeaders []string) http.Handler {
	hc := &http.Client{} // no Timeout: it would cut the SSE body mid-stream
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		name, agentURL := target(r)
		// ponytail: the card is fetched on every POST (stateless: one server
		// per request); cache it per agent if card latency shows up.
		opts := []agentcard.ResolveOption{agentcard.WithPath(a2asrv.WellKnownAgentCardPath)}
		for k, v := range creds(r.Header, credHeaders) {
			opts = append(opts, agentcard.WithRequestHeader(k, v))
		}
		card, _ := agentcard.NewResolver(hc).Resolve(r.Context(), agentURL, opts...)

		s := mcp.NewServer(&mcp.Implementation{Name: "a2a-" + name, Version: "1.0.0"}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: "send_message", Description: describe(name, card)},
			func(ctx context.Context, req *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, any, error) {
				return send(ctx, hc, agentURL, creds(req.Extra.Header, credHeaders), req, in)
			})
		return s
	}, &mcp.StreamableHTTPOptions{Stateless: true})
}

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
	for ev, err := range cl.SendStreamingMessage(ctx, &a2a.SendMessageRequest{Message: msg}) {
		if err != nil {
			return nil, nil, err
		}
		var status a2a.TaskStatus
		var contextID string
		switch e := ev.(type) {
		case *a2a.Message:
			return result(e.ContextID, e.Parts, false), nil, nil
		case *a2a.Task:
			status, contextID = e.Status, e.ContextID
		case *a2a.TaskStatusUpdateEvent:
			status, contextID = e.Status, e.ContextID
		default:
			continue
		}
		var parts a2a.ContentParts
		if status.Message != nil {
			parts = status.Message.Parts
		}
		switch status.State {
		case a2a.TaskStateWorking:
			if text := joinText(parts); token != nil && text != "" {
				n++
				_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: n, Message: text})
			}
		case a2a.TaskStateCompleted, a2a.TaskStateInputRequired:
			return result(contextID, parts, false), nil, nil
		case a2a.TaskStateFailed, a2a.TaskStateCanceled, a2a.TaskStateRejected, a2a.TaskStateAuthRequired:
			return result(contextID, parts, true), nil, nil
		}
	}
	return nil, nil, errors.New("the agent ended the stream without a final reply")
}

// describe builds the tool description from the agent card; without one
// (fetch failed) it names the agent only.
func describe(name string, card *a2a.AgentCard) string {
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

// creds returns the named headers present in h.
func creds(h http.Header, names []string) map[string]string {
	out := map[string]string{}
	for _, n := range names {
		if v := h.Get(n); v != "" {
			out[n] = v
		}
	}
	return out
}
