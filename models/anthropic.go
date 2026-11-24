package models

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	h "github.com/natexcvi/go-llm"
)

// Anthropic implements the Messages API with ordered tool-use and tool-result blocks.
type Anthropic struct {
	// CompactAtTokens enables server-side threshold compaction (minimum 50,000).
	CompactAtTokens        int
	Model, APIKey, BaseURL string
	MaxTokens              int
	Client                 *http.Client
}

func (m Anthropic) ModelName() string { return "anthropic:" + m.Model }

type block struct {
	Signature string            `json:"signature,omitempty"`
	Encrypted string            `json:"encrypted_content,omitempty"`
	Source    map[string]string `json:"source,omitempty"`
	Type      string            `json:"type"`
	Text      string            `json:"text,omitempty"`
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Input     json.RawMessage   `json:"input,omitempty"`
	ToolUseID string            `json:"tool_use_id,omitempty"`
	Content   *string           `json:"content,omitempty"`
}
type anthropicMessage struct {
	Role    h.Role  `json:"role"`
	Content []block `json:"content"`
}

func (m Anthropic) Generate(ctx context.Context, q h.Request) (h.Response, error) {
	return m.generate(ctx, q, nil)
}

func (m Anthropic) Stream(ctx context.Context, q h.Request, emit func(string) error) (h.Response, error) {
	if emit == nil {
		return h.Response{}, errors.New("stream observer required")
	}
	return m.generate(ctx, q, emit)
}

type anthropicUsage struct {
	Type       string           `json:"type"`
	Iterations []anthropicUsage `json:"iterations"`
	Input      int              `json:"input_tokens"`
	Output     int              `json:"output_tokens"`
	CacheRead  int              `json:"cache_read_input_tokens"`
	CacheWrite int              `json:"cache_creation_input_tokens"`
}
type anthropicReply struct {
	Content []block
	Reason  string `json:"stop_reason"`
	Usage   anthropicUsage
}

func (m Anthropic) endpoint() string {
	if m.BaseURL == "" {
		return "https://api.anthropic.com"
	}
	return strings.TrimRight(m.BaseURL, "/")
}

// Compact requests a signed on-demand summary from Anthropic.
func (m Anthropic) Compact(ctx context.Context, q h.Request, focus string) (h.Message, error) {
	q.OutputSchema = nil
	var reply anthropicReply
	err := h.AccountModelRequest(ctx, func(ctx context.Context) (h.Usage, error) {
		var err error
		reply, err = m.send(ctx, q, nil, &focus)
		u := reply.Usage
		return h.Usage{InputTokens: u.Input + u.CacheRead + u.CacheWrite, OutputTokens: u.Output}, err
	})
	if err != nil {
		return h.Message{}, err
	}
	if len(reply.Content) != 1 {
		return h.Message{}, errors.New("expected a single Anthropic compaction block")
	}
	b := reply.Content[0]
	if b.Type != "compaction" || b.Content == nil || *b.Content == "" || b.Signature == "" || reply.Reason == "max_tokens" {
		return h.Message{}, errors.New("incomplete signed Anthropic compaction block")
	}
	return h.Message{Role: h.Assistant, Model: m.ModelName(), Native: &h.NativeContext{Transport: "anthropic", Endpoint: m.endpoint(), Content: *b.Content, Signature: b.Signature, Encrypted: b.Encrypted, Tokens: reply.Usage.Output}}, nil
}

func (m Anthropic) send(ctx context.Context, q h.Request, emit func(string) error, focus *string) (anthropicReply, error) {

	var reply anthropicReply
	signed := focus != nil
	hasThresholdBlock := false
	if m.CompactAtTokens != 0 && m.CompactAtTokens < 50000 {
		return reply, errors.New("Anthropic compaction threshold must be at least 50000 tokens")
	}
	if m.Model == "" {
		return reply, errors.New("model name required")
	}
	var system []string
	var messages []anthropicMessage
	for _, msg := range effectiveHistory(q.Messages, "anthropic", m.endpoint()) {
		if n := msg.Native; n != nil {
			if n.Transport != "anthropic" || n.Endpoint != m.endpoint() || (!n.Boundary && n.Signature == "") || n.Content == "" || msg.Role != h.Assistant {
				return reply, errors.New("invalid or incompatible native compaction block")
			}
			if n.Signature != "" {
				signed = true
			} else {
				hasThresholdBlock = true
			}
		}
		if len(msg.Images) > 0 && msg.Role != h.User {
			return reply, errors.New("images require a user message")
		}
		if msg.Role == h.System {
			system = append(system, msg.Text)
			continue
		}
		role := msg.Role
		var blocks []block
		if n := msg.Native; n != nil {
			blocks = append(blocks, block{Type: "compaction", Content: &n.Content, Signature: n.Signature, Encrypted: n.Encrypted})
		}
		if role == h.ToolResult {
			role = h.User
			blocks = append(blocks, block{Type: "tool_result", ToolUseID: msg.CallID, Content: &msg.Text})
		} else {
			if msg.Text != "" {
				blocks = append(blocks, block{Type: "text", Text: msg.Text})
			}
			for _, img := range msg.Images {
				if err := img.Validate(); err != nil {
					return reply, err
				}
				source := map[string]string{"type": "url", "url": img.URL}
				if len(img.Data) > 0 {
					source = map[string]string{"type": "base64", "media_type": img.MediaType, "data": base64.StdEncoding.EncodeToString(img.Data)}
				}
				blocks = append(blocks, block{Type: "image", Source: source})
			}
			for _, call := range msg.Calls {
				// An invalid input can only originate from a different provider.
				// Keep such history representable for the Messages API.
				input := json.RawMessage(call.Arguments)
				if !json.Valid(input) {
					input, _ = json.Marshal(map[string]string{"invalid_json": call.Arguments})
				}
				blocks = append(blocks, block{Type: "tool_use", ID: call.ID, Name: call.Name, Input: input})
			}
		}
		if n := len(messages); n > 0 && messages[n-1].Role == role {
			messages[n-1].Content = append(messages[n-1].Content, blocks...)
		} else {
			messages = append(messages, anthropicMessage{Role: role, Content: blocks})
		}
	}
	tokens := m.MaxTokens
	if tokens == 0 {
		tokens = 4096
	}
	body := map[string]any{"model": m.Model, "max_tokens": tokens, "messages": messages}
	threshold := hasThresholdBlock || m.CompactAtTokens > 0
	if threshold && !signed {
		edit := map[string]any{"type": "compact_20260112"}
		if m.CompactAtTokens > 0 {
			edit["trigger"] = map[string]any{"type": "input_tokens", "value": m.CompactAtTokens}
		}
		body["context_management"] = map[string]any{"edits": []map[string]any{edit}}
	}
	if focus != nil {
		params := map[string]string{"type": "summarize"}
		if strings.TrimSpace(*focus) != "" {
			params["instructions"] = "Summarize the goals, decisions, evidence and unfinished work. Preserve attribution. Focus: " + *focus
		}
		body["compaction"] = params
	}
	if emit != nil {
		body["stream"] = true
	}
	if len(system) > 0 {
		body["system"] = strings.Join(system, "\n\n")
	}
	if len(q.OutputSchema) > 0 {
		body["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": q.OutputSchema}}
	}
	if len(q.Tools) > 0 {
		var tools []map[string]any
		for _, d := range q.Tools {
			tools = append(tools, map[string]any{"name": d.Name, "description": d.Description, "input_schema": d.Schema})
		}
		body["tools"] = tools
	}
	b, err := json.Marshal(body)
	if err != nil {
		return reply, err
	}
	base := m.endpoint()
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/v1/messages", bytes.NewReader(b))
	if err != nil {
		return reply, err
	}
	var betas []string
	if signed {
		betas = append(betas, "compact-2026-09-04")
	}
	if hasThresholdBlock || threshold && !signed {
		betas = append(betas, "compact-2026-01-12")
	}
	if len(betas) > 0 {
		req.Header.Set("anthropic-beta", strings.Join(betas, ","))
	}
	req.Header.Set("x-api-key", m.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return reply, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return reply, fmt.Errorf("anthropic HTTP %d: %s", resp.StatusCode, b)
	}
	if emit != nil {
		reply, err = readAnthropicStream(resp.Body, emit)
	} else {
		err = json.NewDecoder(resp.Body).Decode(&reply)
	}
	if err != nil {
		return reply, err
	}
	return reply, nil
}

func (m Anthropic) generate(ctx context.Context, q h.Request, emit func(string) error) (h.Response, error) {
	var out h.Response
	reply, err := m.send(ctx, q, emit, nil)
	if err != nil {
		return out, err
	}
	switch reply.Reason {
	case "end_turn", "tool_use", "stop_sequence", "compaction":
	default:
		return out, fmt.Errorf("incomplete anthropic response: %s", reply.Reason)
	}
	var text strings.Builder
	for _, b := range reply.Content {
		switch b.Type {
		case "compaction":
			if b.Content == nil || *b.Content == "" || len(out.Calls) > 0 {
				return h.Response{}, errors.New("invalid Anthropic compaction block")
			}
			out.Native = &h.NativeContext{Transport: "anthropic", Endpoint: m.endpoint(), Content: *b.Content, Signature: b.Signature, Encrypted: b.Encrypted, Boundary: true}
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			out.Calls = append(out.Calls, h.Call{ID: b.ID, Name: b.Name, Arguments: string(b.Input)})
		}
	}
	out.Text = text.String()
	out.Model = m.ModelName()
	out.CacheReadTokens = reply.Usage.CacheRead
	out.CacheWriteTokens = reply.Usage.CacheWrite
	out.InputTokens = reply.Usage.Input + reply.Usage.CacheRead + reply.Usage.CacheWrite
	out.OutputTokens = reply.Usage.Output
	for _, it := range reply.Usage.Iterations {
		input := it.Input + it.CacheRead + it.CacheWrite
		switch it.Type {
		case "compaction":
			out.InputTokens += input
			out.OutputTokens += it.Output
			out.CacheReadTokens += it.CacheRead
			out.CacheWriteTokens += it.CacheWrite
		case "message", "fallback_message":
			out.ContextTokens = input + it.Output
		}
	}
	if reply.Reason == "compaction" && out.Native == nil {
		return h.Response{}, errors.New("missing Anthropic compaction block")
	}
	return out, nil
}

// Fallback tries the next model when Retryable returns true. Each model gets
// the same conversation. Context cancellation stops the entire chain.
type Fallback struct {
	Models    []h.Model
	Retryable func(error) bool
}

func (f Fallback) Generate(ctx context.Context, q h.Request) (h.Response, error) {
	var failures []error
	for _, m := range f.Models {
		if err := ctx.Err(); err != nil {
			return h.Response{}, err
		}
		if m == nil {
			return h.Response{}, errors.New("nil fallback model")
		}
		r, err := m.Generate(ctx, q)
		if err == nil {
			return r, nil
		}
		if ctx.Err() != nil {
			return h.Response{}, ctx.Err()
		}
		failures = append(failures, err)
		if f.Retryable != nil && !f.Retryable(err) {
			break
		}
	}
	if len(failures) == 0 {
		return h.Response{}, errors.New("no fallback models")
	}
	return h.Response{}, errors.Join(failures...)
}

// Anthropic sends complete blocks sequentially, with partial JSON for tool inputs.
// Only message_stop commits a response, so a truncated stream cannot execute tools.
func readAnthropicStream(body io.Reader, emit func(string) error) (anthropicReply, error) {
	var reply anthropicReply
	var active *block
	var fragment strings.Builder
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 8192), 8<<20)
	for scanner.Scan() {
		payload, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		var event struct {
			Type    string
			Index   int
			Message anthropicReply
			Block   block `json:"content_block"`
			Delta   struct {
				Type, Text, Content string
				Encrypted           string `json:"encrypted_content"`
				JSON                string `json:"partial_json"`
				Reason              string `json:"stop_reason"`
			}
			Usage anthropicUsage
			Error struct{ Message string }
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return reply, err
		}
		switch event.Type {
		case "error":
			return reply, errors.New(event.Error.Message)
		case "message_start":
			reply = event.Message
		case "content_block_start":
			if active != nil || event.Index != len(reply.Content) {
				return reply, errors.New("unexpected content block index")
			}
			active = &event.Block
			fragment.Reset()
			if active.Text != "" {
				if err := emit(active.Text); err != nil {
					return reply, err
				}
			}
		case "content_block_delta":
			if active == nil || event.Index != len(reply.Content) {
				return reply, errors.New("delta without matching content block")
			}
			switch event.Delta.Type {
			case "compaction_delta":
				if active.Type != "compaction" {
					return reply, errors.New("compaction delta outside compaction block")
				}
				if event.Delta.Content != "" {
					active.Content = &event.Delta.Content
				}
				if event.Delta.Encrypted != "" {
					active.Encrypted = event.Delta.Encrypted
				}
			case "text_delta":
				if err := emit(event.Delta.Text); err != nil {
					return reply, err
				}
				fragment.WriteString(event.Delta.Text)
			case "input_json_delta":
				fragment.WriteString(event.Delta.JSON)
			}
		case "content_block_stop":
			if active == nil || event.Index != len(reply.Content) {
				return reply, errors.New("unmatched content block stop")
			}
			switch active.Type {
			case "text":
				active.Text += fragment.String()
			case "tool_use":
				if fragment.Len() > 0 {
					active.Input = json.RawMessage(fragment.String())
				}
			}
			reply.Content = append(reply.Content, *active)
			active = nil
		case "message_delta":
			reply.Reason = event.Delta.Reason
			reply.Usage.Output = event.Usage.Output
			if event.Usage.Iterations != nil {
				reply.Usage.Iterations = event.Usage.Iterations
			}
		case "message_stop":
			if active != nil {
				return reply, io.ErrUnexpectedEOF
			}
			return reply, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return reply, err
	}
	return reply, io.ErrUnexpectedEOF
}
