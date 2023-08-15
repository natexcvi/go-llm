package evaluation

import (
	"github.com/natexcvi/go-llm/agents"
)

type agentTester[Input, Output any] struct {
	agent agents.Agent[Input, Output]
}

func NewAgentTester[Input, Output any](agent agents.Agent[Input, Output]) Tester[Input, Output] {
	return &agentTester[Input, Output]{
		agent: agent,
	}
}

func (t *agentTester[Input, Output]) test(test Input) (Output, error) {
	return t.agent.Run(test)
}
