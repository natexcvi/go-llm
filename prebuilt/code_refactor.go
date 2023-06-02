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

func (req CodeBaseRefactorRequest) Encode() string {
	return fmt.Sprintf(`{"dir": "%s", "goal": "%s"}`, req.Dir, req.Goal)
}

func (req CodeBaseRefactorRequest) Schema() string {
	return `{"dir": "path to code base", "goal": "refactoring goal"}`
}

type CodeBaseRefactorResponse struct {
	RefactoredFiles map[string]string `json:"refactored_files"`
}

func (resp CodeBaseRefactorResponse) Encode() string {
	marshalled, err := json.Marshal(resp.RefactoredFiles)
	if err != nil {
		panic(err)
	}
	return string(marshalled)
}

func (resp CodeBaseRefactorResponse) Schema() string {
	return `{"refactored_files": {"path": "description of changes"}}`
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
							Tool: agents.NewGenericAgentTool(nil, nil),
							Args: json.RawMessage(`{"task": "scan code base for functions that might error", "input": "/Users/nate/code/base"}`),
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
							Content: "Now I should handle each function that might error.",
						}).Encode(),
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: (&agents.ChainAgentAction{
							Tool: agents.NewGenericAgentTool(nil, nil),
							Args: json.RawMessage(`{"task": "fix any function that has unhandled exceptions in the file you will be given.", "input": "/Users/nate/code/base/main.py"}`),
						}).Encode(),
					},
					{
						Role: engines.ConvRoleSystem,
						Text: (&agents.ChainAgentObservation{
							Content: "Okay, I've fixed the errors in main.py by wrapping a block with try/except.",
						}).Encode(),
					},
				},
			},
		},
		AnswerParser: func(msg string) (CodeBaseRefactorResponse, error) {
			var res CodeBaseRefactorResponse
			if err := json.Unmarshal([]byte(msg), &res); err == nil && res.RefactoredFiles != nil {
				return res, nil
			}
			var rawRes map[string]string
			if err := json.Unmarshal([]byte(msg), &rawRes); err != nil {
				return CodeBaseRefactorResponse{}, fmt.Errorf("invalid response: %s", err.Error())
			}
			return CodeBaseRefactorResponse{
				RefactoredFiles: rawRes,
			}, nil
		},
	}
	agent := agents.NewChainAgent(engine, task, memory.NewBufferedMemory(10)).WithMaxSolutionAttempts(12).WithTools(
		tools.NewPythonREPL(),
		tools.NewBashTerminal(),
		tools.NewAskUser(),
		agents.NewGenericAgentTool(engine, []tools.Tool{tools.NewBashTerminal(), tools.NewPythonREPL()}),
	)
	return agent
}
