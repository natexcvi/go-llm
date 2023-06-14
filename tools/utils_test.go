package tools

import (
	"encoding/json"
	"testing"

	"github.com/natexcvi/go-llm/engines"
	"github.com/stretchr/testify/assert"
)

type mockTool struct {
	name        string
	description string
	argsSchema  string
}

func (t *mockTool) Execute(args json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

func (t *mockTool) Name() string {
	return t.name
}

func (t *mockTool) Description() string {
	return t.description
}

func (t *mockTool) ArgsSchema() json.RawMessage {
	return []byte(t.argsSchema)
}
func (t *mockTool) CompactArgs(args json.RawMessage) json.RawMessage {
	return args
}

func TestConvertToLLMFunctionSpecs(t *testing.T) {
	testCases := []struct {
		name           string
		tool           Tool
		expectedOutput engines.FunctionSpecs
		expectedErr    error
	}{
		{
			name: "Simple tool args",
			tool: &mockTool{
				name:        "test",
				description: "This is a test.",
				argsSchema:  `{"text": "some text", "num": 0, "booly": true}`,
			},
			expectedOutput: engines.FunctionSpecs{
				Name:        "test",
				Description: "This is a test.",
				Parameters: &engines.ParameterSpecs{
					Type: "object",
					Properties: map[string]*engines.ParameterSpecs{
						"text": {
							Type:        "string",
							Description: "some text",
						},
						"num": {
							Type:        "number",
							Description: "a number",
						},
						"booly": {
							Type:        "boolean",
							Description: "a boolean value",
						},
					},
					Required: []string{},
				},
			},
			expectedErr: nil,
		},
		{
			name: "Tool args with array",
			tool: &mockTool{
				name:        "array_tool",
				description: "This is a test.",
				argsSchema:  `{"text": "some text", "num": 0, "arr": ["this", "is", "an", "array"]}`,
			},
			expectedOutput: engines.FunctionSpecs{
				Name:        "array_tool",
				Description: "This is a test.",
				Parameters: &engines.ParameterSpecs{
					Type: "object",
					Properties: map[string]*engines.ParameterSpecs{
						"text": {
							Type:        "string",
							Description: "some text",
						},
						"num": {
							Type:        "number",
							Description: "a number",
						},
						"arr": {
							Type:  "array",
							Items: &engines.ParameterSpecs{Type: "string", Description: "this is an array"},
						},
					},
					Required: []string{},
				},
			},
			expectedErr: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := ConvertToNativeFunctionSpecs(tc.tool)
			assert.Equal(t, tc.expectedOutput, output)
			assert.Equal(t, tc.expectedErr, err)
		})
	}
}
