package main

import (
	"encoding/json"
	"fmt"
	"os"

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

type UnitConversionRequest struct {
	From  string
	To    string
	Value float32
}

func (req UnitConversionRequest) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`{"from": "%s", "to": "%s", "value": %f}`, req.From, req.To, req.Value)), nil
}

type UnitConversionResponse struct {
	Value float32
}

func main() {
	task := &agents.Task[CodeBaseRefactorRequest, CodeBaseRefactorResponse]{
		Description: "You will be given access to a code base, and instructions for refactoring." +
			"your task is to refactor the code base to meet the given goal.",
		Examples: []agents.Example[CodeBaseRefactorRequest, CodeBaseRefactorResponse]{
			{
				Input: CodeBaseRefactorRequest{
					Dir:  "/Users/nate/code/base",
					Goal: "Handle errors gracefully",
				},
				Answer: CodeBaseRefactorResponse{
					RefactoredFiles: map[string]string{
						"/Users/nate/code/base/main.py": `def main():
							try:
								func_that_might_error()
							except Exception as e:
								print("Error: %s", e)`,
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
						Text: "OBS: main.py",
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: "THT: Now I should read the code file.",
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: `ACT: bash({"command": "cat /Users/nate/code/base/main.py"})`,
					},
					{
						Role: engines.ConvRoleSystem,
						Text: "OBS: def main():\n\tfunc_that_might_error()",
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: "THT: I should refactor the code to handle errors gracefully.",
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
	agent := agents.NewChainAgent(engines.NewGPTEngine(os.Getenv("OPENAI_TOKEN"), "gpt-3.5-turbo"), task, memory.NewBufferedMemory(0)).WithMaxSolutionAttempts(12).WithTools(tools.NewPythonREPL(), tools.NewBashTerminal(), tools.NewWolframAlpha(os.Getenv("WOLFRAM_APPID")))
	res, err := agent.Run(CodeBaseRefactorRequest{
		Dir:  "/Users/nate/Git/go-llm/memory",
		Goal: "Implement the Memory interface defined in memory.go with a new type called SummarisedMemory. It should summarise the history of the conversation as compactly as possible by using an LLM.",
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Result: %v\n", res)
	f, err := os.Create("output.json")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(res); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
}
