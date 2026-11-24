package integrations_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/integrations"
)

func TestMCPDiscoveryCallAndReadOnlyFilter(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, &mcp.ServerOptions{Instructions: "Use the lookup tool."})
	type args struct {
		Key string `json:"key"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "lookup", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(_ context.Context, _ *mcp.CallToolRequest, in args) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "value " + in.Key}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "write"}, func(context.Context, *mcp.CallToolRequest, args) (*mcp.CallToolResult, any, error) {
		return nil, nil, errors.New("should be filtered")
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	conn := integrations.MCP{Prefix: "demo", ReadOnly: true, IncludeInstructions: true, Transport: func(context.Context) (mcp.Transport, error) {
		return &mcp.StreamableClientTransport{Endpoint: httpServer.URL, HTTPClient: httpServer.Client()}, nil
	}}
	model := h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		if len(q.Tools) != 1 || q.Tools[0].Name != "demo_lookup" || !strings.Contains(q.Messages[0].Text, "lookup") {
			t.Fatal(q)
		}
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{ID: "c", Name: "demo_lookup", Arguments: `{"key":"seven"}`}}}, nil
	})
	a := h.Agent[string]{Model: model, Output: h.Text(), Capabilities: []h.Capability{conn.Capability()}}
	out, err := a.Run(t.Context(), "lookup", h.Options{})
	if err != nil || out.Output != "value seven" {
		t.Fatal(out, err)
	}
}
func TestKeenableWireContracts(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/search/public":
			if r.Method != "POST" {
				t.Error(r.Method)
			}
			w.Write([]byte(`{"results":[{"title":"Docs","url":"https://example.org/docs","snippet":"example"}]}`))
		case "/v1/fetch":
			if r.Header.Get("X-API-Key") != "key" || r.URL.Query().Get("url") != "https://example.org/docs" {
				t.Error(r)
			}
			w.Write([]byte(`{"title":"Docs","content":"Example document."}`))
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	k := integrations.Keenable{BaseURL: s.URL, Client: s.Client()}
	hits, err := k.Search(t.Context(), "docs")
	if err != nil || len(hits) != 1 || hits[0].Text != "example" {
		t.Fatal(hits, err)
	}
	k.APIKey = "key"
	page, err := k.Fetch(t.Context(), hits[0].URL)
	if err != nil || page.Text != "Example document." {
		t.Fatal(page, err)
	}
}

func TestStackOneAccountAndActionFilter(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "linked", Version: "1"}, nil)
	for _, name := range []string{"crm_list_people", "crm_delete_person"} {
		mcp.AddTool(server, &mcp.Tool{Name: name}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "listed"}}}, nil, nil
		})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, pass, ok := r.BasicAuth()
		if !ok || key != "fixture" || pass != "" || r.Header.Get("x-account-id") != "account" || r.URL.Path != "/mcp" || r.URL.RawQuery != "" {
			t.Error("incorrect StackOne authentication or route")
		}
		handler.ServeHTTP(w, r)
	}))
	defer s.Close()
	conn := integrations.StackOne{BaseURL: s.URL, Client: s.Client(), Actions: []string{"CRM_LIST_*"}, Credentials: func(context.Context) (string, string, error) { return "account", "fixture", nil }}
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{conn.Capability()}, Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		if len(q.Tools) != 1 || q.Tools[0].Name != "crm_list_people" {
			t.Fatal(q.Tools)
		}
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{ID: "crm", Name: "crm_list_people", Arguments: "{}"}}}, nil
	})}
	out, err := a.Run(t.Context(), "list", h.Options{})
	if err != nil || out.Output != "listed" {
		t.Fatal(out, err)
	}
}
