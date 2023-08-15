package evaluation

import (
	"fmt"
	"github.com/natexcvi/go-llm/agents"
)

type agentEvaluator[Input, Output any] struct {
	options *Options[Input, Output]
	agent   agents.Agent[Input, Output]
}

func NewAgentEvaluator[Input, Output any](agent agents.Agent[Input, Output], options *Options[Input, Output]) Evaluator[Input, Output] {
	return &agentEvaluator[Input, Output]{
		options: options,
		agent:   agent,
	}
}

func (a agentEvaluator[Input, Output]) Evaluate(TestPack []Input) ([]float64, error) {
	channels := make([]chan []float64, a.options.Repetitions)

	for i := 0; i < a.options.Repetitions; i++ {
		channels[i] = make(chan []float64)
		go func(i int) {
			report, err := a.evaluate(TestPack)
			if err != nil {
				channels[i] <- nil
				return
			}
			channels[i] <- report
		}(i)
	}

	responses := make([][]float64, a.options.Repetitions)
	for i := 0; i < a.options.Repetitions; i++ {
		responses[i] = <-channels[i]
	}

	report := make([]float64, len(TestPack))
	for i := 0; i < len(TestPack); i++ {
		sum := 0.0
		for j := 0; j < a.options.Repetitions; j++ {
			sum += responses[j][i]
		}
		report[i] = sum / float64(a.options.Repetitions)
	}

	return report, nil
}

func (a agentEvaluator[Input, Output]) evaluate(TestPack []Input) ([]float64, error) {
	responses, err := a.test(TestPack)
	if err != nil {
		return nil, fmt.Errorf("failed to test: %w", err)
	}

	report := make([]float64, len(TestPack))
	for i, response := range responses {
		report[i] = a.options.GoodnessFunction(TestPack[i], response)
	}

	return report, nil
}

func (a agentEvaluator[Input, Output]) test(TestPack []Input) ([]Output, error) {
	responses := make([]Output, len(TestPack))

	for i, test := range TestPack {
		response, err := a.agent.Run(test)
		if err != nil {
			return nil, fmt.Errorf("failed to chat: %w", err)
		}

		responses[i] = response
	}

	return responses, nil
}
