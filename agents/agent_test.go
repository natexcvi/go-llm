package agents

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/natexcvi/go-llm/engines"
	memorymocks "github.com/natexcvi/go-llm/memory/mocks"
	"github.com/natexcvi/go-llm/tools"
	toolmocks "github.com/natexcvi/go-llm/tools/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockEngine struct {
	Responses []*engines.ChatMessage
	Functions []engines.FunctionSpecs
}

func (engine *MockEngine) Predict(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
	if len(engine.Responses) == 0 {
		return nil, errors.New("no more responses")
	}
	response := engine.Responses[0]
	engine.Responses = engine.Responses[1:]
	return response, nil
}

func (engine *MockEngine) SetFunctions(funcs ...engines.FunctionSpecs) {
	engine.Functions = funcs
}

type Str string

func (s *Str) Encode() string {
	return string(*s)
}

func (s *Str) Schema() string {
	return "<some text>"
}

func (s *Str) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	*s = Str(str)
	return nil
}

func newStr(str string) *Str {
	s := Str(str)
	return &s
}

func newMockMemory(t *testing.T) *memorymocks.MockMemory {
	ctrl := gomock.NewController(t)
	buffer := make([]*engines.ChatMessage, 0)
	mock := memorymocks.NewMockMemory(ctrl)
	mock.EXPECT().Add(gomock.Any()).AnyTimes().DoAndReturn(func(msg *engines.ChatMessage) error {
		buffer = append(buffer, msg)
		return nil
	})
	mock.EXPECT().AddPrompt(gomock.Any()).AnyTimes().DoAndReturn(func(prompt *engines.ChatPrompt) error {
		buffer = append(buffer, prompt.History...)
		return nil
	})
	mock.EXPECT().PromptWithContext(gomock.Any()).AnyTimes().DoAndReturn(func(nextMessages ...*engines.ChatMessage) (*engines.ChatPrompt, error) {
		buffer = append(buffer, nextMessages...)
		return &engines.ChatPrompt{
			History: buffer,
		}, nil
	})
	return mock
}

func newMockTool(t *testing.T, name, description string, argSchema json.RawMessage, impl func(json.RawMessage) (json.RawMessage, error)) *toolmocks.MockTool {
	ctrl := gomock.NewController(t)
	mock := toolmocks.NewMockTool(ctrl)
	mock.EXPECT().Execute(gomock.Any()).AnyTimes().DoAndReturn(impl)
	mock.EXPECT().Name().AnyTimes().Return(name)
	mock.EXPECT().Description().AnyTimes().Return(description)
	mock.EXPECT().ArgsSchema().AnyTimes().Return(argSchema)
	return mock
}

func TestChainAgent(t *testing.T) {
	testCases := []struct {
		name   string
		agent  *ChainAgent[*Str, *Str]
		input  *Str
		output *Str
	}{
		{
			name: "simple",
			agent: &ChainAgent[*Str, *Str]{
				Engine: &MockEngine{
					Responses: []*engines.ChatMessage{
						{
							Role: engines.ConvRoleAssistant,
							Text: `ACT: echo("world")`,
						},
						{
							Role: engines.ConvRoleAssistant,
							Text: `ANS: "Hello world"`,
						},
					},
				},
				Task: &Task[*Str, *Str]{
					Description: "Say hello to an entity you find yourself",
					AnswerParser: func(text string) (*Str, error) {
						var output string
						err := json.Unmarshal([]byte(text), &output)
						require.NoError(t, err)
						return newStr(output), nil
					},
				},
				Memory: newMockMemory(t),
				Tools: map[string]tools.Tool{
					"echo": newMockTool(
						t,
						"echo",
						"echoes the input",
						json.RawMessage(`"the string to echo"`),
						func(args json.RawMessage) (json.RawMessage, error) {
							return args, nil
						},
					),
				},
				OutputValidators: []func(*Str) error{
					func(output *Str) error {
						if *output == "" {
							return errors.New("output is empty")
						}
						return nil
					},
				},
			},
			input:  newStr("hello"),
			output: newStr("Hello world"),
		},
		{
			name: "simple with native LLM functions",
			agent: &ChainAgent[*Str, *Str]{
				Engine: &MockEngine{
					Responses: []*engines.ChatMessage{
						{
							Role: engines.ConvRoleAssistant,
							Text: "",
							FunctionCall: &engines.FunctionCall{
								Name: "echo",
								Args: []byte(`{"msg": "world"}`),
							},
						},
						{
							Role: engines.ConvRoleAssistant,
							Text: `ANS: "Hello world"`,
						},
					},
				},
				Task: &Task[*Str, *Str]{
					Description: "Say hello to an entity you find yourself",
					AnswerParser: func(text string) (*Str, error) {
						var output string
						err := json.Unmarshal([]byte(text), &output)
						require.NoError(t, err)
						return newStr(output), nil
					},
				},
				Memory: newMockMemory(t),
				Tools: map[string]tools.Tool{
					"echo": newMockTool(
						t,
						"echo",
						"echoes the input",
						json.RawMessage(`{"msg": "the string to echo"}`),
						func(args json.RawMessage) (json.RawMessage, error) {
							return args, nil
						},
					),
				},
				OutputValidators: []func(*Str) error{
					func(output *Str) error {
						if *output == "" {
							return errors.New("output is empty")
						}
						return nil
					},
				},
			},
			input:  newStr("hello"),
			output: newStr("Hello world"),
		},
		{
			name: "empty output makes validator fail",
			agent: &ChainAgent[*Str, *Str]{
				Engine: &MockEngine{
					Responses: []*engines.ChatMessage{
						{
							Role: engines.ConvRoleAssistant,
							Text: `ACT: echo("world")`,
						},
						{
							Role: engines.ConvRoleAssistant,
							Text: `ANS: ""`,
						},
						{
							Role: engines.ConvRoleAssistant,
							Text: `THT: That's right, the output is empty. I'll fix it`,
						},
						{
							Role: engines.ConvRoleAssistant,
							Text: `ANS: "Hello world"`,
						},
					},
				},
				Task: &Task[*Str, *Str]{
					Description: "Say hello to an entity you find yourself",
					AnswerParser: func(text string) (*Str, error) {
						var output string
						err := json.Unmarshal([]byte(text), &output)
						require.NoError(t, err)
						return newStr(output), nil
					},
				},
				Memory: newMockMemory(t),
				Tools: map[string]tools.Tool{
					"echo": newMockTool(
						t,
						"echo",
						"echoes the input",
						json.RawMessage(`"the string to echo"`),
						func(args json.RawMessage) (json.RawMessage, error) {
							return args, nil
						},
					),
				},
				OutputValidators: []func(*Str) error{
					func(output *Str) error {
						if *output == "" {
							return errors.New("output is empty")
						}
						return nil
					},
				},
			},
			input:  newStr("hello"),
			output: newStr("Hello world"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			output, err := testCase.agent.Run(testCase.input)
			require.NoError(t, err)
			assert.Equal(t, *testCase.output, *output)
		})
	}
}
