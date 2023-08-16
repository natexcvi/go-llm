package evaluation

import (
	"github.com/natexcvi/go-llm/agents"
)

type agentRunner[Input, Output any] struct {
	agent agents.Agent[Input, Output]
}

// Returns a new agent runner that can be used to evaluate the output.
func NewAgentRunner[Input, Output any](agent agents.Agent[Input, Output]) Runner[Input, Output] {
	return &agentRunner[Input, Output]{
		agent: agent,
	}
}

func (t *agentRunner[Input, Output]) Run(input Input) (Output, error) {
	return t.agent.Run(input)
}
