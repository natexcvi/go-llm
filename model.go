// Package harness runs typed, tool-using agents. An Agent can serve concurrent
// conversations. Capabilities install their tools and hooks separately for each run.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type Role string

const (
	System     Role = "system"
	User       Role = "user"
	Assistant  Role = "assistant"
	ToolResult Role = "tool"
)

// Arguments retains the model's original text, including malformed JSON that
// a tool may ask the model to correct.
type Call struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// NativeContext is a provider-issued compaction item. Replay requires the
// issuing transport and endpoint. Keep its contents intact across checkpoints.
type NativeContext struct {
	// Boundary marks server-side compaction of everything before this response.
	Boundary  bool   `json:"boundary,omitempty"`
	Transport string `json:"transport"`
	Endpoint  string `json:"endpoint"`
	Content   string `json:"content,omitempty"`
	ID        string `json:"id,omitempty"`
	Signature string `json:"signature,omitempty"`
	Encrypted string `json:"encrypted,omitempty"`
	Tokens    int    `json:"tokens,omitempty"`
}

// ReasoningItem stores stateless Responses reasoning needed by later tool turns.
type ReasoningItem struct {
	BeforeCall int             `json:"before_call,omitempty"`
	Endpoint   string          `json:"endpoint"`
	ID         string          `json:"id,omitempty"`
	Encrypted  string          `json:"encrypted_content"`
	Summary    json.RawMessage `json:"summary,omitempty"`
}

type Message struct {
	Reasoning []ReasoningItem `json:"reasoning,omitempty"`

	Native *NativeContext `json:"native_context,omitempty"`

	// Pinned messages (and their tool exchange) survive local history compaction.
	Pinned bool `json:"pinned,omitempty"`
	// ContextTokens is the provider's input plus output usage through this
	// message, or an adjusted estimate after a history rewrite. Zero is unknown.
	ContextTokens int `json:"context_tokens,omitempty"`
	// ContextKind identifies framework-generated user-role context in snapshots.
	ContextKind string `json:"context_kind,omitempty"`

	Images []Image `json:"images,omitempty"`
	Model  string  `json:"model,omitempty"`
	Role   Role    `json:"role"`
	Text   string  `json:"text,omitempty"`
	Calls  []Call  `json:"calls,omitempty"`
	CallID string  `json:"call_id,omitempty"`
}
type Definition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"parameters_json_schema"`
}
type Request struct {
	Messages     []Message
	Tools        []Definition
	OutputSchema json.RawMessage
}
type Usage struct {
	Requests     int `json:"requests"`
	ToolCalls    int `json:"tool_calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
type Response struct {
	Native                            *NativeContext
	ContextTokens                     int
	Reasoning                         []ReasoningItem
	Model                             string
	CacheReadTokens, CacheWriteTokens int
	Text                              string
	Calls                             []Call
	InputTokens, OutputTokens         int
}

// NamedModel identifies the model used for cross-model history and cache telemetry.
type NamedModel interface{ ModelName() string }

type Model interface {
	Generate(context.Context, Request) (Response, error)
}
type ModelFunc func(context.Context, Request) (Response, error)

func (f ModelFunc) Generate(ctx context.Context, q Request) (Response, error) { return f(ctx, q) }

// StreamingModel emits incremental text and returns the assembled response.
// A callback error terminates the stream and must be propagated.
type StreamingModel interface {
	Model
	Stream(context.Context, Request, func(string) error) (Response, error)
}

var (
	ErrLimit   = errors.New("run limit reached")
	ErrRetries = errors.New("model correction limit reached")
)

type retryError struct{ error }

func (e retryError) Unwrap() error { return e.error }

// Retry sends a recoverable tool or guardrail error to the model. Other
// handler errors stop the run and remain available to errors.Is and errors.As.
func Retry(err error) error {
	if err == nil {
		return nil
	}
	return retryError{err}
}

func contextTokens(resp Response) int {
	if resp.ContextTokens > 0 {
		return resp.ContextTokens
	}
	if resp.InputTokens <= 0 {
		return 0
	}
	return resp.InputTokens + resp.OutputTokens
}

func cloneMessages(messages []Message) []Message {
	result := append([]Message(nil), messages...)
	for i := range result {
		result[i].Reasoning = cloneReasoning(result[i].Reasoning)
		if result[i].Native != nil {
			v := *result[i].Native
			result[i].Native = &v
		}
		result[i].Images = cloneImages(result[i].Images)
		result[i].Calls = append([]Call(nil), result[i].Calls...)
	}
	return result
}

func validateCalls(calls []Call) error {
	seen := make(map[string]bool, len(calls))
	for _, call := range calls {
		if call.ID == "" || call.Name == "" || seen[call.ID] {
			return fmt.Errorf("invalid or duplicate tool call id %q (name %q)", call.ID, call.Name)
		}
		seen[call.ID] = true
	}
	return nil
}

func cloneReasoning(items []ReasoningItem) []ReasoningItem {
	out := append([]ReasoningItem(nil), items...)
	for i := range out {
		out[i].Summary = append(json.RawMessage(nil), out[i].Summary...)
	}
	return out
}
