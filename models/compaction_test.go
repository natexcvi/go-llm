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
	c "github.com/natexcvi/go-llm/capabilities"
	"github.com/natexcvi/go-llm/models"
)

func TestAnthropicCompactAndReplay(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("anthropic-beta") != "compact-2026-09-04" {
			t.Error(r.URL, r.Header)
		}
		var body struct {
			Compaction struct{ Type, Instructions string }
			Messages   []struct {
				Content []struct {
					Type, Content, Signature string
					Encrypted                string `json:"encrypted_content"`
				}
			}
			OutputConfig any `json:"output_config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		count++
		if count == 1 {
			if body.Compaction.Type != "summarize" || body.OutputConfig != nil || !strings.Contains(body.Compaction.Instructions, "focus") {
				t.Error(body)
			}
			fmt.Fprint(w, `{"stop_reason":"compaction","content":[{"type":"compaction","content":"notes","signature":"signed","encrypted_content":"opaque"}],"usage":{"output_tokens":3}}`)
		} else {
			if body.Compaction.Type != "" || len(body.Messages) != 2 || body.Messages[0].Content[0].Encrypted != "opaque" || body.Messages[0].Content[0].Signature != "signed" || body.Messages[0].Content[0].Content != "notes" {
				t.Error(body)
			}
			fmt.Fprint(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":"continued"}]}`)
		}
	}))
	defer srv.Close()
	m := models.Anthropic{Model: "fixture", BaseURL: srv.URL}
	q := h.Request{Messages: []h.Message{{Role: h.User, Text: "prior"}}, OutputSchema: json.RawMessage(`{"type":"object"}`)}
	msg, err := m.Compact(t.Context(), q, "keep focus")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(msg)
	msg = h.Message{}
	if err := json.Unmarshal(b, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Native.Encrypted != "opaque" {
		t.Fatal(msg)
	}
	q = h.Request{Messages: []h.Message{msg, {Role: h.User, Text: "continue"}}}
	reply, err := m.Generate(t.Context(), q)
	if err != nil || reply.Text != "continued" {
		t.Fatal(reply, err)
	}
	for _, other := range []h.Model{models.OpenAI{Model: "bad", BaseURL: srv.URL}, models.OpenAIResponses{Model: "bad", BaseURL: srv.URL}, models.Anthropic{Model: "bad", BaseURL: srv.URL + "/other"}} {
		if _, err := other.Generate(t.Context(), q); err == nil {
			t.Fatal("accepted incompatible native context")
		}
	}
	if count != 2 {
		t.Fatal(count)
	}
}

func TestResponsesCompactionThroughAgent(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		var body struct {
			Input  []map[string]any
			Store  bool
			Tools  []map[string]any
			Stream bool
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if r.Header.Get("Authorization") != "Bearer api" {
			t.Error("missing auth")
		}
		switch count {
		case 1:
			if r.URL.Path != "/responses/compact" || len(body.Input) != 2 {
				t.Error(r.URL, body)
			}
			fmt.Fprint(w, `{"output":[{"type":"compaction","id":"cmp","encrypted_content":"ciphertext"}],"usage":{"output_tokens":3}}`)
		case 2:
			if r.URL.Path != "/responses" || body.Input[0]["encrypted_content"] != "ciphertext" || body.Input[1]["content"] != "continue" || body.Tools[0]["name"] != "lookup" || !body.Stream {
				t.Error(body)
			}
			fmt.Fprintln(w, `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","id":"rs","encrypted_content":"thought","summary":[]},{"type":"function_call","call_id":"call","name":"lookup","arguments":"{}"}],"usage":{"input_tokens":20}}}`)
		case 3:
			if r.URL.Path != "/responses" || body.Input[2]["encrypted_content"] != "thought" || body.Input[4]["type"] != "function_call_output" || body.Input[4]["output"] != "ok" {
				t.Error(body)
			}
			fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"done"}`)
			fmt.Fprintln(w, `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}}`)
		default:
			t.Error("unexpected extra request", count)
		}
	}))
	defer srv.Close()
	m := models.OpenAIResponses{Model: "fixture", APIKey: "api", BaseURL: srv.URL}
	p := c.CompactionPolicy{TargetTokens: 200, Strategies: []c.CompactionStrategy{c.NativeSummary(m, 1)}}
	a := h.Agent[string]{Model: m, Output: h.Text(), Capabilities: []h.Capability{p.Capability(), h.Tools(h.NewTool("lookup", "", func(context.Context, struct{}) (string, error) { return "ok", nil }))}}
	var streamed string
	res, err := a.Run(t.Context(), "continue", h.Options{History: []h.Message{{Role: h.User, Text: strings.Repeat("word ", 500)}, {Role: h.Assistant, Text: "old answer"}}, Observe: func(_ context.Context, e h.Event) error {
		if e.Kind == "delta" {
			streamed += e.Text
		}
		return nil
	}})
	if err != nil || streamed != "done" || res.Output != "done" || res.History[0].Native.ID != "cmp" || count != 3 {
		t.Fatal(res, err, count, streamed)
	}
}

func TestServerThresholdContinuation(t *testing.T) {
	for _, provider := range []string{"anthropic", "responses"} {
		t.Run(provider, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if !strings.Contains(string(body["context_management"]), "50000") {
					t.Error(body)
				}
				if provider == "anthropic" {
					if r.Header.Get("anthropic-beta") != "compact-2026-01-12" {
						t.Error(r.Header)
					}
					if requests == 1 {
						fmt.Fprintln(w, `data: {"type":"message_start","message":{"usage":{"input_tokens":100}}}`)
						fmt.Fprintln(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"compaction"}}`)
						fmt.Fprintln(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"compaction_delta","content":"condensed","encrypted_content":"secure"}}`)
						fmt.Fprintln(w, `data: {"type":"content_block_stop","index":0}`)
						fmt.Fprintln(w, `data: {"type":"message_delta","delta":{"stop_reason":"compaction"},"usage":{"output_tokens":4,"iterations":[{"type":"compaction","input_tokens":90,"output_tokens":8},{"type":"message","input_tokens":10,"output_tokens":3}]}}`)
					} else {
						if string(body["system"]) != `"standing"` || strings.Contains(string(body["messages"]), "original") || !strings.Contains(string(body["messages"]), "secure") {
							t.Error(string(body["messages"]))
						}
						fmt.Fprintln(w, `data: {"type":"message_start","message":{"content":[{"type":"text","text":"continued"}]}}`)
						fmt.Fprintln(w, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
					}
					fmt.Fprintln(w, `data: {"type":"message_stop"}`)
				} else if requests == 1 {
					fmt.Fprintln(w, `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"compaction","id":"cmp","encrypted_content":"secure"}]}}`)
				} else {
					if !strings.Contains(string(body["input"]), "standing") || !strings.Contains(string(body["input"]), "secure") || strings.Contains(string(body["input"]), "original") {
						t.Error(string(body["input"]))
					}
					fmt.Fprintln(w, `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"continued"}]}]}}`)
				}
			}))
			defer srv.Close()
			var m h.Model = models.OpenAIResponses{Model: "fixture", BaseURL: srv.URL, CompactAtTokens: 50000}
			if provider == "anthropic" {
				m = models.Anthropic{Model: "fixture", BaseURL: srv.URL, CompactAtTokens: 50000}
			}
			a := h.Agent[string]{Instructions: "standing", Output: h.Text(), Model: m}
			r, err := a.Run(t.Context(), "original", h.Options{Observe: func(_ context.Context, e h.Event) error {
				if e.Kind == "delta" && e.Text == "condensed" {
					t.Fatal(e)
				}
				return nil
			}})
			if err != nil || r.Output != "continued" || requests != 2 || !r.History[1].Native.Boundary {
				t.Fatal(r, err)
			}
			if provider == "anthropic" && (r.Usage.InputTokens != 190 || r.Usage.OutputTokens != 12 || r.History[1].ContextTokens != 13) {
				t.Fatal(r)
			}
		})
	}
}
