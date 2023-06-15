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
	GitStatus   string
}

func (req GitAssistantRequest) Encode() string {
	marshaled, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return string(marshaled)
}

func (req GitAssistantRequest) Schema() string {
	return `{"instruction": "a description of what the user wants to do", "git_status": "output of git status"}`
}

type GitOperation struct {
	Operation string `json:"operation"`
	Reasoning string `json:"reasoning"`
}

type GitAssistantResponse struct {
	Operations []GitOperation `json:"operations"`
}

func (resp GitAssistantResponse) Encode() string {
	marshalled, err := json.Marshal(resp.Operations)
	if err != nil {
		panic(err)
	}
	return string(marshalled)
}

func (resp GitAssistantResponse) Schema() string {
	return `{"operations": [{"operation": "git command", "reasoning": "reason for command"}]}`
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
					Operations: []GitOperation{
						{
							Operation: "git add main.py",
							Reasoning: "add relevant files to staging area",
						},
						{
							Operation: "git commit -m \"added try/except block\"",
							Reasoning: "commit changes",
						},
						{
							Operation: "git push",
							Reasoning: "push changes to remote (GitHub)",
						},
					},
				},
				IntermediarySteps: []*engines.ChatMessage{
					// (&agents.ChainAgentAction{
					// 	Tool: tools.NewAskUser(),
					// 	Args: []byte(`{"question": "Should I commit only the changes to main.py? (yes/no)"}`),
					// }).Encode(engine),
					// (&agents.ChainAgentObservation{
					// 	Content:  "yes",
					// 	ToolName: tools.NewAskUser().Name(),
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
	agent := agents.NewChainAgent(engine, task, memory.NewBufferedMemory(10)).WithMaxSolutionAttempts(12).WithTools(
	// tools.NewAskUser(),
	)
	return agent
}
