// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"
)

func TestUserView_UsesTheUserKeyNotTheMaster(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/model_group/info":
			_, _ = w.Write([]byte(`{"data":[
			  {"model_group":"__deny_all__","providers":[]},
			  {"model_group":"demo-model","providers":["openai"],"mode":"chat","max_input_tokens":128000.0,
			   "input_cost_per_token":1e-06,"supports_vision":true,"litellm_params":{"api_key":"SECRET"}}]}`))
		case "/v1/mcp/server":
			_, _ = w.Write([]byte(`[{"server_id":"s1","server_name":"demo-mcp","alias":"demo","transport":"http","allowed_tools":["a","b"],"credentials":{"x":"SECRET"}}]`))
		case "/v1/agents":
			_, _ = w.Write([]byte(`{"data":[{"agent_id":"a1","agent_name":"demo-agent","agent_card_params":{"url":"http://x","skills":[{"name":"s"}],"capabilities":{"streaming":true}}}]}`))
		case "/v1/model/info":
			_, _ = w.Write([]byte(`{"data":[{"model_name":"m"}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := NewRESTClient(srv.URL, "sk-master", logr.Discard())
	u := c.AsUser("sk-user")

	groups, err := u.ListModelGroups(context.Background())
	if err != nil || len(groups) != 2 || groups[1].Name != "demo-model" || !groups[1].SupportsVision || groups[1].Mode == nil || *groups[1].Mode != "chat" {
		t.Fatalf("%+v %v", groups, err)
	}
	if groups[0].Providers == nil {
		t.Fatal("providers must be [] not null")
	}
	mcp, err := u.ListMCPServers(context.Background())
	if err != nil || len(mcp) != 1 || mcp[0].Alias != "demo" || len(mcp[0].AllowedTools) != 2 {
		t.Fatalf("%+v %v", mcp, err)
	}
	a2a, err := u.ListA2AAgents(context.Background())
	if err != nil || len(a2a) != 1 || a2a[0].AgentName != "demo-agent" {
		t.Fatalf("%+v %v", a2a, err)
	}
	for _, h := range seen {
		if h != "Bearer sk-user" {
			t.Fatalf("user read sent %q", h)
		}
	}
	// The shared client still speaks as the master.
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen[len(seen)-1] != "Bearer sk-master" {
		t.Fatalf("master call sent %q", seen[len(seen)-1])
	}
}

func TestUserView_EmptyCatalogsAreEmptyNotErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/mcp/server":
			_, _ = w.Write([]byte(`[]`))
		case "/v1/agents":
			_, _ = w.Write([]byte(`{"agents":[]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer srv.Close()
	u := NewRESTClient(srv.URL, "sk-master", logr.Discard()).AsUser("sk-user")
	if g, err := u.ListModelGroups(context.Background()); err != nil || len(g) != 0 {
		t.Fatalf("%v %v", g, err)
	}
	if m, err := u.ListMCPServers(context.Background()); err != nil || len(m) != 0 {
		t.Fatalf("%v %v", m, err)
	}
	if a, err := u.ListA2AAgents(context.Background()); err != nil || len(a) != 0 {
		t.Fatalf("%v %v", a, err)
	}
}

func TestUserView_RefusalIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid proxy server token passed"}}`))
	}))
	defer srv.Close()
	u := NewRESTClient(srv.URL, "sk-master", logr.Discard()).AsUser("sk-user")
	_, err := u.ListModelGroups(context.Background())
	var a *Auth401Error
	if err == nil || !errors.As(err, &a) {
		t.Fatalf("want *Auth401Error, got %v", err)
	}
}
