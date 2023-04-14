package prebuilt

import (
	"encoding/json"
	"fmt"

	"github.com/natexcvi/go-llm/agents"
	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/memory"
)

type UnitTestWriterRequest struct {
	SourceFile  string `json:"source_file"`
	ExampleFile string `json:"example_file"`
}

func (r UnitTestWriterRequest) Encode() string {
	return fmt.Sprintf(`Write unit tests for the following file based on the example: {"source_file": %q, "example_file": %q}`, r.SourceFile, r.ExampleFile)
}

func (r UnitTestWriterRequest) Schema() string {
	return `{"source_file": "source code file", "example_file": "example unit test file"}`
}

type UnitTestWriterResponse struct {
	UnitTestFile string `json:"unit_test_file"`
}

func NewUnitTestWriter(engine engines.LLM, codeValidator func(code string) error) (agents.Agent[UnitTestWriterRequest, UnitTestWriterResponse], error) {
	task := &agents.Task[UnitTestWriterRequest, UnitTestWriterResponse]{
		Description: "You are a coding assistant that specialises in writing " +
			"unit tests. You will be given a source code file and an example unit test file. " +
			"Your task is to write unit tests for the source code file, following " +
			"the patterns and conventions you see in the example unit test file. " +
			"Your final answer should be just the content of the unit test file, " +
			"and nothing else. " +
			"For this task, no intermediary steps are required and in most cases you can " +
			"Reply with your final answer immediately.",
		Examples: []agents.Example[UnitTestWriterRequest, UnitTestWriterResponse]{
			{
				Input: UnitTestWriterRequest{
					SourceFile: "def add(a, b):\n    return a + b\n",
					ExampleFile: "from example import multiply\ndef test_multiply():" +
						"\n    assert multiply(2, 3) == 6\n",
				},
				Answer: UnitTestWriterResponse{
					UnitTestFile: "from example import add\ndef test_add():" +
						"\n    assert add(4, -4) == 0\n",
				},
				IntermediarySteps: []*engines.ChatMessage{
					{
						Role: engines.ConvRoleAssistant,
						Text: (&agents.ChainAgentThought{
							Content: "I now know what tests to write.",
						}).Encode(),
					},
				},
			},
		},
		AnswerParser: func(answer string) (UnitTestWriterResponse, error) {
			var response UnitTestWriterResponse
			if err := json.Unmarshal([]byte(answer), &response); err == nil {
				return response, nil
			}
			return UnitTestWriterResponse{
				UnitTestFile: answer,
			}, nil
		},
	}
	agent := agents.NewChainAgent(engine, task, memory.NewBufferedMemory(0))
	if codeValidator != nil {
		agent = agent.WithOutputValidators(func(utwr UnitTestWriterResponse) error {
			err := codeValidator(utwr.UnitTestFile)
			if err != nil {
				return fmt.Errorf("Your unit test file is not valid: %s", err)
			}
			return nil
		})
	}
	return agent, nil
}
