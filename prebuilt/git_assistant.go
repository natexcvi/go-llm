package prebuilt

import (
	"encoding/json"
	"fmt"

	"github.com/natexcvi/go-llm/agents"
	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/memory"
)

type GitAssistantRequest struct {
	Instruction string
}

func (req GitAssistantRequest) Encode() string {
	return fmt.Sprintf(`{"instruction": "%s"}`, req.Instruction)
}

func (req GitAssistantRequest) Schema() string {
	return `{"instruction": "a description of what the user wants to do"}`
}

type GitAssistantResponse struct {
	Operations map[string]string `json:"operations"`
}

func (resp GitAssistantResponse) Encode() string {
	marshalled, err := json.Marshal(resp.Operations)
	if err != nil {
		panic(err)
	}
	return string(marshalled)
}

func (resp GitAssistantResponse) Schema() string {
	return `{"operations": {"git command": "why it was used"}}`
}

func NewGitAssistantAgent(engine engines.LLM) agents.Agent[GitAssistantRequest, GitAssistantResponse] {
	task := &agents.Task[GitAssistantRequest, GitAssistantResponse]{
		Description: "You will be given an instruction for some operation " +
			"to be performed with git. Your task is to perform the operation, " +
			"and explain why it was performed. Sometimes more than one operation " +
			"will be required to complete the task, but make sure to use as few command " +
			"as possible.",
		Examples: []agents.Example[GitAssistantRequest, GitAssistantResponse]{
			{
				Input: GitAssistantRequest{
					Instruction: "I added a try/except block to main.py, and now I want to push the changes to GitHub.",
				},
				Answer: GitAssistantResponse{
					Operations: map[string]string{
						"git add main.py":                          "add relevant files to staging area",
						"git commit -m \"added try/except block\"": "commit changes",
						"git push": "push changes to remote (GitHub)",
					},
				},
				IntermediarySteps: []*engines.ChatMessage{
					// (&agents.ChainAgentThought{
					// 	Content: "I should find which files are related to the try/except block.",
					// }).Encode(engine),
					// (&agents.ChainAgentAction{
					// 	Tool: tools.NewBashTerminal(),
					// 	Args: json.RawMessage(`{"command": "git status -s"}`),
					// }).Encode(engine),
					// (&agents.ChainAgentObservation{
					// 	Content:  "?? main.py\n?? README.md\n",
					// 	ToolName: tools.NewBashTerminal().Name(),
					// }).Encode(engine),
					// (&agents.ChainAgentThought{
					// 	Content: "main.py is probably the only file I need to add.",
					// }).Encode(engine),
				},
			},
		},
		AnswerParser: func(msg string) (GitAssistantResponse, error) {
			var resp GitAssistantResponse
			if err := json.Unmarshal([]byte(msg), &resp); err != nil {
				return GitAssistantResponse{}, fmt.Errorf("failed to parse response: %w", err)
			}
			return resp, nil
		},
	}
	agent := agents.NewChainAgent(engine, task, memory.NewBufferedMemory(10)).WithMaxSolutionAttempts(12).WithTools()
	return agent
}
