package tools

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/go-multierror"
	"github.com/natexcvi/go-llm/engines"
)

var ErrMaxRetriesExceeded = fmt.Errorf("max retries exceeded")

type JSONAutoFixer struct {
	engine     engines.LLM
	maxRetries int
}

func (t *JSONAutoFixer) prompt(args json.RawMessage) *engines.ChatPrompt {
	prompt := engines.ChatPrompt{
		History: []*engines.ChatMessage{
			{
				Role: engines.ConvRoleSystem,
				Text: "You are an automated JSON fixer. " +
					"You will receive a JSON payload that might contain " +
					"errors, and you must fix them and return a valid JSON payload.",
			},
			{
				Role: engines.ConvRoleUser,
				Text: `{"name": "John "Doe", "age": 30, "car": null}`,
			},
			{
				Role: engines.ConvRoleAssistant,
				Text: `{"name": "John \"Doe", "age": 30, "car": null}`,
			},
		},
	}
	prompt.History = append(prompt.History, &engines.ChatMessage{
		Role: engines.ConvRoleUser,
		Text: string(args),
	})
	return &prompt
}

func (t *JSONAutoFixer) validateJSON(raw string) error {
	var obj any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func (t *JSONAutoFixer) Process(args json.RawMessage) (json.RawMessage, error) {
	if err := t.validateJSON(string(args)); err == nil {
		return args, nil
	}
	prompt := t.prompt(args)
	var cumErr *multierror.Error
	for i := 0; i < t.maxRetries; i++ {
		resp, err := t.engine.Predict(prompt)
		if err != nil {
			return nil, fmt.Errorf("error running JSON auto fixer: %w", err)
		}
		if err := t.validateJSON(resp.Text); err != nil {
			cumErr = multierror.Append(cumErr, fmt.Errorf("invalid JSON returned by JSON auto fixer: %w", err))
			continue
		}
		return json.RawMessage(resp.Text), nil
	}
	return nil, multierror.Append(cumErr, ErrMaxRetriesExceeded)
}

func NewJSONAutoFixer(engine engines.LLM, maxRetries int) *JSONAutoFixer {
	return &JSONAutoFixer{
		engine:     engine,
		maxRetries: maxRetries,
	}
}
