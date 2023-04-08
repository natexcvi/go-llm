package prebuilt

import (
	"encoding/json"
	"fmt"

	"github.com/natexcvi/go-llm/agents"
	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/memory"
	"github.com/natexcvi/go-llm/tools"
)

type CodeBaseRefactorRequest struct {
	Dir  string
	Goal string
}

func (req CodeBaseRefactorRequest) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`{"dir": "%s", "goal": "%s"}`, req.Dir, req.Goal)), nil
}

type CodeBaseRefactorResponse struct {
	RefactoredFiles map[string]string
}

func NewCodeRefactorAgent(engine engines.LLM) agents.Agent[CodeBaseRefactorRequest, CodeBaseRefactorResponse] {
	task := &agents.Task[CodeBaseRefactorRequest, CodeBaseRefactorResponse]{
		Description: "You will be given access to a code base, and instructions for refactoring. " +
			"Your task is to refactor the code base to meet the given goal.",
		Examples: []agents.Example[CodeBaseRefactorRequest, CodeBaseRefactorResponse]{
			{
				Input: CodeBaseRefactorRequest{
					Dir:  "/Users/nate/code/base",
					Goal: "Handle errors gracefully",
				},
				Answer: CodeBaseRefactorResponse{
					RefactoredFiles: map[string]string{
						"/Users/nate/code/base/main.py": "added try/except block",
					},
				},
				IntermediarySteps: []*engines.ChatMessage{
					{
						Role: engines.ConvRoleAssistant,
						Text: (&agents.ChainAgentThought{
							Content: "I should scan the code base for functions that might error.",
						}).Encode(),
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: (&agents.ChainAgentAction{
							Tool: tools.NewBashTerminal(),
							Args: json.RawMessage(`{"command": "ls /Users/nate/code/base"}`),
						}).Encode(),
					},
					{
						Role: engines.ConvRoleSystem,
						Text: (&agents.ChainAgentObservation{
							Content: "main.py",
						}).Encode(),
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: (&agents.ChainAgentThought{
							Content: "Now I should read the code file.",
						}).Encode(),
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: (&agents.ChainAgentAction{
							Tool: tools.NewBashTerminal(),
							Args: json.RawMessage(`{"command": "cat /Users/nate/code/base/main.py"}`),
						}).Encode(),
					},
					{
						Role: engines.ConvRoleSystem,
						Text: (&agents.ChainAgentObservation{
							Content: "def main():\n\tfunc_that_might_error()",
						}).Encode(),
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: (&agents.ChainAgentThought{
							Content: "I should refactor the code to handle errors gracefully.",
						}).Encode(),
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: (&agents.ChainAgentAction{
							Tool: tools.NewBashTerminal(),
							Args: json.RawMessage(`{"command": "echo 'def main():\n\ttry:\n\t\tfunc_that_might_error()\n\texcept Exception as e:\n\t\tprint(\"Error: %s\", e)' > /Users/nate/code/base/main.py"}`),
						}).Encode(),
					},
				},
			},
		},
		AnswerParser: func(msg string) (CodeBaseRefactorResponse, error) {
			var res CodeBaseRefactorResponse
			if err := json.Unmarshal([]byte(msg), &res); err != nil {
				return CodeBaseRefactorResponse{}, err
			}
			return res, nil
		},
	}
	agent := agents.NewChainAgent(engine, task, memory.NewSummarisedMemory(3, engine)).WithMaxSolutionAttempts(12).WithTools(tools.NewBashTerminal())
	return agent
}
