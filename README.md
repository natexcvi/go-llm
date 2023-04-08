# Go LLM
[![Go](https://github.com/natexcvi/go-llm/actions/workflows/go.yml/badge.svg)](https://github.com/natexcvi/go-llm/actions/workflows/go.yml)

Integrate the power of large language models (LLM) into your Go application.

## Usage Example
```go
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
					{
						Role: engines.ConvRoleAssistant,
						Text: `ACT: bash({"command": "echo 'def main():\n\ttry:\n\t\tfunc_that_might_error()\n\texcept Exception as e:\n\t\tprint(\"Error: %s\", e)' > /Users/nate/code/base/main.py"})`,
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
	agent := agents.NewChainAgent(engines.NewGPTEngine(os.Getenv("OPENAI_TOKEN"), "gpt-3.5-turbo"), task, memory.NewBufferedMemory(0)).WithMaxSolutionAttempts(12).WithTools(tools.NewPythonREPL(), tools.NewBashTerminal())
	res, err := agent.Run(CodeBaseRefactorRequest{
		Dir:  "/Users/nate/Git/go-llm/tools",
		Goal: "Write unit tests for the bash.go file, following the example of python_repl_test.go.",
	})
	...
}
```
> Fun fact: the `tools/bash_test.go` file was written by this very agent, and helped find a bug!

## Components
### Engines
Connectors to LLM engines. Currently only OpenAI's GPT chat completion API is supported.
### Tools
Tools that can provide agents with the ability to perform actions interacting with the outside world.
Currently available tools are:
- `PythonREPL` - a tool that allows agents to execute Python code in a REPL.
- `IsolatedPythonREPL` - a tool that allows agents to execute Python code in a REPL, but in a Docker container.
- `BashTerminal` - a tool that allows agents to execute bash commands in a terminal.
- `GoogleSearch` - a tool that allows agents to search Google.
- `WebpageSummary` - an LLM-based tool that allows agents to get a summary of a webpage.
- `WolframAlpha` - a tool that allows agents to query WolframAlpha's short answer API.

### Memory
A memory system that allows agents to store and retrieve information.
Currently available memory systems are:
- `BufferMemory` - which provides each step of the agent with a fixed buffer of recent messages from the conversation history.
- `SummarisedMemory` - which provides each step of the agent with a summary of the conversation history, powered by an LLM.

### Agents
Agents are the main component of the library. Agents can perform complex tasks that involve iterative interactions with the outside world.

### Prebuilt (WIP)
A collection of ready-made agents that can be easily integrated with your application.
