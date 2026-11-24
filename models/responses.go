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

// OpenAIResponses is a stateless Responses API transport with native /compact
// support. BaseURL uses the same v1 convention as OpenAI.
type OpenAIResponses struct {
	// CompactAtTokens enables server-managed compaction for supporting models.
	CompactAtTokens        int
	Model, APIKey, BaseURL string
	Client                 *http.Client
}

func (m OpenAIResponses) ModelName() string { return "openai:" + m.Model }
func (m OpenAIResponses) endpoint() string {
	if m.BaseURL == "" {
		return "https://api.openai.com/v1"
	}
	return strings.TrimRight(m.BaseURL, "/")
}

type responseItem struct {
	Summary   json.RawMessage                        `json:"summary"`
	Type      string                                 `json:"type"`
	ID        string                                 `json:"id"`
	Encrypted string                                 `json:"encrypted_content"`
	CallID    string                                 `json:"call_id"`
	Name      string                                 `json:"name"`
	Arguments string                                 `json:"arguments"`
	Content   []struct{ Type, Text, Refusal string } `json:"content"`
}
type responsesReply struct {
	Status string
	Output []responseItem
	Error  *struct{ Message string }
	Usage  struct {
		Input        int `json:"input_tokens"`
		Output       int `json:"output_tokens"`
		InputDetails struct {
			Cached int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	}
}

func (m OpenAIResponses) input(messages []h.Message) ([]any, error) {
	items := make([]any, 0, len(messages))
	for _, msg := range effectiveHistory(messages, "openai-responses", m.endpoint()) {
		if n := msg.Native; n != nil {
			if msg.Role != h.Assistant || n.Transport != "openai-responses" || n.Endpoint != m.endpoint() || n.Encrypted == "" {
				return nil, errors.New("invalid or incompatible OpenAI compaction item")
			}
			item := map[string]string{"type": "compaction", "encrypted_content": n.Encrypted}
			if n.ID != "" {
				item["id"] = n.ID
			}
			items = append(items, item)
		}
		if msg.Role == h.ToolResult {
			items = append(items, map[string]string{"type": "function_call_output", "call_id": msg.CallID, "output": msg.Text})
			continue
		}
		if len(msg.Images) > 0 {
			if msg.Role != h.User {
				return nil, errors.New("images require a user message")
			}
			content := []map[string]string{}
			if msg.Text != "" {
				content = append(content, map[string]string{"type": "input_text", "text": msg.Text})
			}
			for _, im := range msg.Images {
				if err := im.Validate(); err != nil {
					return nil, err
				}
				url := im.URL
				if len(im.Data) > 0 {
					url = "data:" + im.MediaType + ";base64," + base64.StdEncoding.EncodeToString(im.Data)
				}
				content = append(content, map[string]string{"type": "input_image", "image_url": url})
			}
			items = append(items, map[string]any{"role": msg.Role, "content": content})
		} else if msg.Text != "" || (msg.Native == nil && len(msg.Calls) == 0) {
			items = append(items, map[string]any{"role": msg.Role, "content": msg.Text})
		}
		reason := 0
		for i := 0; i <= len(msg.Calls); i++ {
			for reason < len(msg.Reasoning) && msg.Reasoning[reason].BeforeCall == i {
				v := msg.Reasoning[reason]
				if v.Endpoint != m.endpoint() || v.Encrypted == "" || msg.Role != h.Assistant {
					return nil, errors.New("incompatible reasoning item")
				}
				item := map[string]any{"type": "reasoning", "encrypted_content": v.Encrypted, "summary": []string{}}
				if len(v.Summary) > 0 {
					item["summary"] = v.Summary
				}
				if v.ID != "" {
					item["id"] = v.ID
				}
				items = append(items, item)
				reason++
			}
			if i == len(msg.Calls) {
				break
			}
			c := msg.Calls[i]
			items = append(items, map[string]string{"type": "function_call", "call_id": c.ID, "name": c.Name, "arguments": c.Arguments})
		}
		if reason != len(msg.Reasoning) {
			return nil, errors.New("invalid reasoning item order")
		}
	}
	return items, nil
}

func (m OpenAIResponses) post(ctx context.Context, path string, body map[string]any) (io.ReadCloser, error) {
	if m.Model == "" {
		return nil, errors.New("model name required")
	}
	body["model"] = m.Model
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint()+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.APIKey)
	}
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("responses HTTP %d: %s", resp.StatusCode, detail)
	}
	return resp.Body, nil
}

func (m OpenAIResponses) Compact(ctx context.Context, q h.Request, focus string) (h.Message, error) {
	input, err := m.input(q.Messages)
	if err != nil {
		return h.Message{}, err
	}
	body := map[string]any{"input": input}
	if focus != "" {
		body["instructions"] = focus
	}
	var reply responsesReply
	err = h.AccountModelRequest(ctx, func(ctx context.Context) (h.Usage, error) {
		res, err := m.post(ctx, "/responses/compact", body)
		if err != nil {
			return h.Usage{}, err
		}
		defer res.Close()
		err = json.NewDecoder(res).Decode(&reply)
		return h.Usage{InputTokens: reply.Usage.Input, OutputTokens: reply.Usage.Output}, err
	})
	if err != nil {
		return h.Message{}, err
	}
	if len(reply.Output) == 0 {
		return h.Message{}, errors.New("empty compact response")
	}
	item := reply.Output[len(reply.Output)-1]
	if item.Type != "compaction" || item.Encrypted == "" {
		return h.Message{}, errors.New("missing encrypted OpenAI compaction item")
	}
	return h.Message{Role: h.Assistant, Model: m.ModelName(), Native: &h.NativeContext{Transport: "openai-responses", Endpoint: m.endpoint(), ID: item.ID, Encrypted: item.Encrypted, Tokens: reply.Usage.Output}}, nil
}
func (m OpenAIResponses) Generate(ctx context.Context, q h.Request) (h.Response, error) {
	return m.generate(ctx, q, nil)
}
func (m OpenAIResponses) Stream(ctx context.Context, q h.Request, emit func(string) error) (h.Response, error) {
	if emit == nil {
		return h.Response{}, errors.New("stream observer required")
	}
	return m.generate(ctx, q, emit)
}
func (m OpenAIResponses) generate(ctx context.Context, q h.Request, emit func(string) error) (h.Response, error) {
	input, err := m.input(q.Messages)
	if err != nil {
		return h.Response{}, err
	}
	body := map[string]any{"input": input, "store": false, "include": []string{"reasoning.encrypted_content"}}
	if m.CompactAtTokens < 0 {
		return h.Response{}, errors.New("invalid native compaction threshold")
	}
	if m.CompactAtTokens > 0 {
		body["context_management"] = []map[string]any{{"type": "compaction", "compact_threshold": m.CompactAtTokens}}
	}
	if emit != nil {
		body["stream"] = true
	}
	if len(q.Tools) > 0 {
		var tools []map[string]any
		for _, d := range q.Tools {
			tools = append(tools, map[string]any{"type": "function", "name": d.Name, "description": d.Description, "parameters": d.Schema, "strict": false})
		}
		body["tools"] = tools
	}
	if len(q.OutputSchema) > 0 {
		body["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "result", "schema": q.OutputSchema}}
	}
	res, err := m.post(ctx, "/responses", body)
	if err != nil {
		return h.Response{}, err
	}
	defer res.Close()
	var reply responsesReply
	if emit != nil {
		reply, err = streamResponses(res, emit)
	} else {
		err = json.NewDecoder(res).Decode(&reply)
	}
	if err != nil {
		return h.Response{}, err
	}
	if reply.Error != nil {
		return h.Response{}, errors.New(reply.Error.Message)
	}
	if reply.Status != "completed" {
		return h.Response{}, fmt.Errorf("incomplete Responses request: %s", reply.Status)
	}
	out := h.Response{Model: m.ModelName(), InputTokens: reply.Usage.Input, OutputTokens: reply.Usage.Output, CacheReadTokens: reply.Usage.InputDetails.Cached}
	var text strings.Builder
	for _, item := range reply.Output {
		switch item.Type {
		case "compaction":
			if item.Encrypted == "" || len(out.Calls) > 0 {
				return h.Response{}, errors.New("invalid server compaction item")
			}
			out.Native = &h.NativeContext{Transport: "openai-responses", Endpoint: m.endpoint(), ID: item.ID, Encrypted: item.Encrypted, Boundary: true}
			out.Reasoning = nil
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					text.WriteString(part.Text)
				case "refusal":
					return h.Response{}, fmt.Errorf("model refusal: %s", part.Refusal)
				default:
					return h.Response{}, fmt.Errorf("unsupported Responses content %q", part.Type)
				}
			}
		case "function_call":
			out.Calls = append(out.Calls, h.Call{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
		case "reasoning":
			if item.Encrypted == "" {
				return h.Response{}, errors.New("missing stateless reasoning content")
			}
			out.Reasoning = append(out.Reasoning, h.ReasoningItem{BeforeCall: len(out.Calls), Endpoint: m.endpoint(), ID: item.ID, Encrypted: item.Encrypted, Summary: item.Summary})
		default:
			return h.Response{}, fmt.Errorf("unsupported Responses output %q", item.Type)
		}
	}
	out.Text = text.String()
	if len(reply.Output) == 0 || (out.Native == nil && out.Text == "" && len(out.Calls) == 0) {
		return h.Response{}, errors.New("response contains no text or tool call")
	}
	return out, nil
}
func streamResponses(r io.Reader, emit func(string) error) (responsesReply, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 8192), 8<<20)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		var event struct {
			Type, Delta, Message string
			Response             responsesReply
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return responsesReply{}, err
		}
		switch event.Type {
		case "response.output_text.delta":
			if err := emit(event.Delta); err != nil {
				return responsesReply{}, err
			}
		case "response.completed", "response.incomplete", "response.failed":
			return event.Response, nil
		case "error":
			return responsesReply{}, fmt.Errorf("Responses stream: %s", event.Message)
		}
	}
	if err := scanner.Err(); err != nil {
		return responsesReply{}, err
	}
	return responsesReply{}, io.ErrUnexpectedEOF
}
