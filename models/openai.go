// Package models contains model transports for the harness.
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
	"sort"
	"strings"

	h "github.com/natexcvi/go-llm"
)

// OpenAI speaks the Chat Completions API. BaseURL defaults to the OpenAI v1 endpoint.
type OpenAI struct {
	Model, APIKey, BaseURL string
	Client                 *http.Client
	Temperature            *float64
}

func (m OpenAI) ModelName() string { return "openai:" + m.Model }

type function struct {
	Name        string          `json:"name,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}
type wireCall struct {
	Index    int      `json:"index,omitempty"`
	ID       string   `json:"id,omitempty"`
	Type     string   `json:"type,omitempty"`
	Function function `json:"function"`
}
type wireMessage struct {
	Role    h.Role     `json:"role,omitempty"`
	Content string     `json:"content"`
	Calls   []wireCall `json:"tool_calls,omitempty"`
	CallID  string     `json:"tool_call_id,omitempty"`
}
type completion struct {
	Choices []struct {
		Message wireMessage `json:"message"`
		Delta   wireMessage `json:"delta"`
		Reason  string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		Details struct {
			Cached int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		Input  int `json:"prompt_tokens"`
		Output int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (m OpenAI) request(ctx context.Context, q h.Request, stream bool) (io.ReadCloser, error) {
	if m.Model == "" {
		return nil, errors.New("model name required")
	}
	msgs := make([]any, 0, len(q.Messages))
	for _, msg := range q.Messages {
		if msg.Native != nil {
			return nil, errors.New("chat completions cannot replay native compaction")
		}
		w := wireMessage{Role: msg.Role, Content: msg.Text, CallID: msg.CallID}
		for _, c := range msg.Calls {
			w.Calls = append(w.Calls, wireCall{Type: "function", ID: c.ID, Function: function{Name: c.Name, Arguments: c.Arguments}})
		}
		if len(msg.Images) > 0 {
			if msg.Role != h.User {
				return nil, errors.New("images require a user message")
			}
			parts := []map[string]any{}
			if msg.Text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": msg.Text})
			}
			for _, img := range msg.Images {
				if err := img.Validate(); err != nil {
					return nil, err
				}
				u := img.URL
				if len(img.Data) > 0 {
					u = "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
				}
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": u}})
			}
			msgs = append(msgs, map[string]any{"role": w.Role, "content": parts})
		} else {
			msgs = append(msgs, w)
		}
	}
	body := map[string]any{"model": m.Model, "messages": msgs}
	if m.Temperature != nil {
		body["temperature"] = *m.Temperature
	}
	if len(q.Tools) > 0 {
		tools := make([]wireCall, 0, len(q.Tools))
		for _, d := range q.Tools {
			tools = append(tools, wireCall{Type: "function", Function: function{Name: d.Name, Description: d.Description, Parameters: d.Schema}})
		}
		body["tools"] = tools
	}
	if q.OutputSchema != nil {
		body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "result", "schema": q.OutputSchema}}
	}
	if stream {
		body["stream"] = true
		body["stream_options"] = map[string]bool{"include_usage": true}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	base := m.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(raw))
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
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode/100 != 2 {
		defer res.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("chat completions HTTP %d: %s", res.StatusCode, b)
	}
	return res.Body, nil
}
func (m OpenAI) Generate(ctx context.Context, q h.Request) (h.Response, error) {
	var out h.Response
	body, err := m.request(ctx, q, false)
	if err != nil {
		return out, err
	}
	defer body.Close()
	var c completion
	if err = json.NewDecoder(body).Decode(&c); err != nil {
		return out, err
	}
	if c.Error != nil {
		return out, errors.New(c.Error.Message)
	}
	if len(c.Choices) == 0 {
		return out, errors.New("empty completion")
	}
	choice := c.Choices[0]
	if err := checkReason(choice.Reason); err != nil {
		return out, err
	}
	out.Text = choice.Message.Content
	for _, call := range choice.Message.Calls {
		out.Calls = append(out.Calls, h.Call{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
	}
	out.CacheReadTokens = c.Usage.Details.Cached
	out.Model = m.ModelName()
	out.InputTokens = c.Usage.Input
	out.OutputTokens = c.Usage.Output
	return out, nil
}
func checkReason(reason string) error {
	switch reason {
	case "stop", "tool_calls":
		return nil
	default:
		return fmt.Errorf("incomplete completion: finish reason %q", reason)
	}
}
func (m OpenAI) Stream(ctx context.Context, q h.Request, emit func(string) error) (h.Response, error) {
	var out h.Response
	body, err := m.request(ctx, q, true)
	if err != nil {
		return out, err
	}
	defer body.Close()
	s := bufio.NewScanner(body)
	s.Buffer(make([]byte, 8192), 8<<20)
	calls := map[int]*h.Call{}
	var content strings.Builder
	finished := false
	for s.Scan() {
		line := s.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var c completion
		if err = json.Unmarshal([]byte(payload), &c); err != nil {
			return out, err
		}
		if c.Error != nil {
			return out, errors.New(c.Error.Message)
		}
		if c.Usage.Input != 0 || c.Usage.Output != 0 {
			out.CacheReadTokens = c.Usage.Details.Cached
			out.Model = m.ModelName()
			out.InputTokens = c.Usage.Input
			out.OutputTokens = c.Usage.Output
		}
		if len(c.Choices) == 0 {
			continue
		}
		ch := c.Choices[0]
		if ch.Reason != "" {
			if err := checkReason(ch.Reason); err != nil {
				return out, err
			}
			finished = true
		}
		if ch.Delta.Content != "" {
			if err := emit(ch.Delta.Content); err != nil {
				return out, err
			}
			content.WriteString(ch.Delta.Content)
		}
		for _, d := range ch.Delta.Calls {
			call := calls[d.Index]
			if call == nil {
				call = &h.Call{}
				calls[d.Index] = call
			}
			call.ID += d.ID
			call.Name += d.Function.Name
			call.Arguments += d.Function.Arguments
		}
	}
	if err := s.Err(); err != nil {
		return out, err
	}
	if !finished {
		return out, io.ErrUnexpectedEOF
	}
	indexes := make([]int, 0, len(calls))
	for i := range calls {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	for _, i := range indexes {
		out.Calls = append(out.Calls, *calls[i])
	}
	out.Text = content.String()
	return out, nil
}
