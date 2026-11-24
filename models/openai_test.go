package models_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/models"
)

func TestStreamToolRoundTrip(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream   bool
			Messages []map[string]any
			Tools    []map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !body.Stream || len(body.Tools) != 1 || r.Header.Get("Authorization") != "Bearer token" || r.URL.Path != "/v1/chat/completions" {
			t.Error(body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 0 {
			fmt.Fprintln(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"say","arguments":"{"}}]}}]}`)
			fmt.Fprintln(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			last := body.Messages[len(body.Messages)-1]
			if last["tool_call_id"] != "c1" || last["content"] != "hello" {
				t.Error(last)
			}
			fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"do"}}]}`)
			fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"ne"},"finish_reason":"stop"}]}`)
		}
		fmt.Fprintln(w, `data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
		fmt.Fprintln(w, "data: [DONE]")
		requests++
	}))
	defer server.Close()
	a := h.Agent[string]{Model: models.OpenAI{Model: "demo", APIKey: "token", BaseURL: server.URL + "/v1"}, Output: h.Text(), Capabilities: []h.Capability{h.Tools(h.NewTool("say", "", func(context.Context, struct{}) (string, error) { return "hello", nil }))}}
	var deltas strings.Builder
	out, err := a.Run(t.Context(), "hello", h.Options{Observe: func(_ context.Context, e h.Event) error {
		if e.Kind == "delta" {
			deltas.WriteString(e.Text)
		}
		return nil
	}})
	if err != nil || out.Output != "done" || deltas.String() != "done" || out.Usage.InputTokens != 4 {
		t.Fatal(out, deltas.String(), err)
	}
}
