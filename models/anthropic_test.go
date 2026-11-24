package models_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/models"
)

func TestAnthropicMessages(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "key" {
			t.Error(r)
		}
		var req struct {
			System   string
			Messages []struct {
				Role    string
				Content []map[string]any
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.System != "guide" || len(req.Messages) != 3 || len(req.Messages[2].Content) != 2 || req.Messages[2].Content[1]["tool_use_id"] != "b" {
			t.Error(req)
		}
		w.Write([]byte(`{"stop_reason":"tool_use","usage":{"input_tokens":4,"cache_read_input_tokens":2,"output_tokens":2},"content":[{"type":"text","text":"Next"},{"type":"tool_use","id":"c","name":"double","input":{"n":2}}]}`))
	}))
	defer s.Close()
	m := models.Anthropic{BaseURL: s.URL, APIKey: "key", Model: "test"}
	resp, err := m.Generate(t.Context(), h.Request{Messages: []h.Message{{Role: h.System, Text: "guide"}, {Role: h.User, Text: "go"}, {Role: h.Assistant, Calls: []h.Call{{ID: "a", Name: "one", Arguments: "{}"}, {ID: "b", Name: "two", Arguments: "{}"}}}, {Role: h.ToolResult, CallID: "a", Text: "first"}, {Role: h.ToolResult, CallID: "b", Text: "second"}}})
	if err != nil || resp.InputTokens != 6 || len(resp.Calls) != 1 || resp.Calls[0].Arguments != `{"n":2}` {
		t.Fatal(resp, err)
	}
}

func TestAnthropicStreaming(t *testing.T) {
	wire := `data: {"type":"message_start","message":{"usage":{"input_tokens":4}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}
data: {"type":"content_block_stop","index":0}
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"c1","name":"find","input":{}}}
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"n\":"}}
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"2}"}}
data: {"type":"content_block_stop","index":1}
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}
data: {"type":"message_stop"}

`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Stream bool }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Stream {
			t.Error(req, err)
		}
		_, _ = io.WriteString(w, wire)
	}))
	defer server.Close()
	m := models.Anthropic{Model: "test", BaseURL: server.URL}
	var received string
	out, err := m.Stream(t.Context(), h.Request{}, func(s string) error {
		received += s
		return nil
	})
	if err != nil || out.Text != "hello" || out.Text != received || len(out.Calls) != 1 || out.Calls[0].Arguments != `{"n":2}` || out.OutputTokens != 7 || out.InputTokens != 4 {
		t.Fatal(out, err)
	}
	stop := errors.New("stop streaming")
	_, err = m.Stream(t.Context(), h.Request{}, func(string) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
}
