package agents

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/memory"
	"github.com/natexcvi/go-llm/tools"
	"github.com/samber/lo"
)

type genericRequest struct {
	TaskDescription string `json:"task"`
	Input           string `json:"input"`
}

func (r genericRequest) Encode() string {
	return r.Input
}

func (r genericRequest) Schema() string {
	return `{"task": "a description of the task you want to give the agent, including helpful examples.", "input": "the specific input on which the agent should act."}`
}

type genericResponse struct {
	output string
}

func (r genericResponse) Encode() string {
	return r.output
}

func (r genericResponse) Schema() string {
	return ""
}

type GenericAgentTool struct {
	engine engines.LLM
	tools  []tools.Tool
}

func (ga *GenericAgentTool) Name() string {
	return "smart_agent"
}

func (ga *GenericAgentTool) Description() string {
	return "A smart agent you can delegate tasks to. Use for relatively larger tasks." +
		lo.If(
			len(ga.tools) > 0,
			" The agent will have access to the following tools: "+
				strings.Join(lo.Map(ga.tools, func(tool tools.Tool, _ int) string {
					return tool.Name()
				}), ", ")+".",
		).Else("")
}

func (ga *GenericAgentTool) Execute(args json.RawMessage) (json.RawMessage, error) {
	var request genericRequest
	err := json.Unmarshal(args, &request)
	if err != nil {
		return nil, fmt.Errorf("invalid arguments: %s", err.Error())
	}
	task := &Task[genericRequest, genericResponse]{
		Description: request.TaskDescription,
		Examples:    []Example[genericRequest, genericResponse]{},
		AnswerParser: func(res string) (genericResponse, error) {
			return genericResponse{res}, nil
		},
	}
	agent := NewChainAgent(ga.engine, task, memory.NewBufferedMemory(10)).WithTools(ga.tools...)
	response, err := agent.Run(request)
	if err != nil {
		return nil, fmt.Errorf("error running agent: %s", err.Error())
	}
	return json.Marshal(response.output)
}

func (ga *GenericAgentTool) ArgsSchema() json.RawMessage {
	return []byte(`{"task": "a description of the task you want to give the agent, including helpful examples.", "input": "the specific input on which the agent should act."}`)
}

func (ga *GenericAgentTool) CompactArgs(args json.RawMessage) json.RawMessage {
	return args
}

func NewGenericAgentTool(engine engines.LLM, tools []tools.Tool) *GenericAgentTool {
	return &GenericAgentTool{engine: engine, tools: tools}
}
