package prebuilt

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/natexcvi/go-llm/agents"
	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/memory"
	"github.com/natexcvi/go-llm/tools"
)

var (
	gitTool = tools.NewGenericTool(
		"git",
		"A tool for executing git commands.",
		json.RawMessage(`{"command": "the git command to execute", "reason": "explain why you are executing this command, e.g. 'add a file to the staging area''"}`),
		func(args json.RawMessage) (json.RawMessage, error) {
			var command struct {
				Command string `json:"command"`
				Reason  string `json:"reason"`
			}
			err := json.Unmarshal(args, &command)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(command.Command, "git ") {
				command.Command = command.Command[4:]
			}
			out, err := tools.NewBashTerminal().Execute([]byte(fmt.Sprintf(`{"command": "git %s"}`, command.Command)))
			if err != nil {
				return nil, err
			}
			return json.Marshal(string(out))
		},
	)
)

type GitAssistantRequest struct {
	Instruction string
	GitStatus   string
	CurrentDate string
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

type GitAssistantResponse struct {
	Summary string `json:"summary"`
}

func (resp GitAssistantResponse) Encode() string {
	marshaled, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return string(marshaled)
}

func (resp GitAssistantResponse) Schema() string {
	return `{"summary": "a summary of the git operations performed"}`
}

func NewGitAssistantAgent(engine engines.LLM, actionConfirmationHook func(action *agents.ChainAgentAction) bool, additionalTools ...tools.Tool) agents.Agent[GitAssistantRequest, GitAssistantResponse] {
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
					Summary: "I pushed the changes to GitHub.",
				},
				IntermediarySteps: []*engines.ChatMessage{
					(&agents.ChainAgentAction{
						Tool: gitTool,
						Args: []byte(`{"command": "push", "reason": "push the changes to GitHub"}`),
					}).Encode(engine),
				},
			},
		},
		AnswerParser: func(msg string) (GitAssistantResponse, error) {
			var resp GitAssistantResponse
			if err := json.Unmarshal([]byte(msg), &resp); err != nil {
				resp.Summary = msg
			}
			return resp, nil
		},
	}
	additionalTools = append(additionalTools, gitTool)
	agent := agents.NewChainAgent(engine, task, memory.NewBufferedMemory(10)).WithMaxSolutionAttempts(15).WithTools(
		additionalTools...,
	).WithActionConfirmation(actionConfirmationHook)
	return agent
}
