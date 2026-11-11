// SPDX-License-Identifier: Apache-2.0

// Package a2amcp serves the MCP face of an A2A agent: one tool,
// send_message, relayed to the agent with A2A SendStreamingMessage. Working
// updates become MCP progress notifications; the call returns when the task
// completes, fails or asks for input.
package a2amcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
