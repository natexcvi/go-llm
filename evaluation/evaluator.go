package evaluation

import (
	"fmt"
	"github.com/samber/mo"
)

type GoodnessFunction[Input, Output any] func(input Input, output Output, err error) float64

type Options[Input, Output any] struct {
	GoodnessFunction GoodnessFunction[Input, Output]
	Repetitions      int
}

type Tester[Input, Output any] interface {
	Test(test Input) (Output, error)
}

type Evaluator[Input, Output any] struct {
	options *Options[Input, Output]
	tester  Tester[Input, Output]
}

func NewEvaluator[Input, Output any](tester Tester[Input, Output], options *Options[Input, Output]) *Evaluator[Input, Output] {
	return &Evaluator[Input, Output]{
		options: options,
		tester:  tester,
	}
}

func (e *Evaluator[Input, Output]) Evaluate(testPack []Input) ([]float64, error) {
	channels := make([]chan []float64, e.options.Repetitions)

	for i := 0; i < e.options.Repetitions; i++ {
		channels[i] = make(chan []float64)
		go func(i int) {
			report, err := e.evaluate(testPack)
			if err != nil {
				channels[i] <- nil
				return
			}
			channels[i] <- report
		}(i)
	}

	responses := make([][]float64, e.options.Repetitions)
	for i := 0; i < e.options.Repetitions; i++ {
		responses[i] = <-channels[i]
	}

	report := make([]float64, len(testPack))
	for i := 0; i < len(testPack); i++ {
		sum := 0.0
		for j := 0; j < e.options.Repetitions; j++ {
			sum += responses[j][i]
		}
		report[i] = sum / float64(e.options.Repetitions)
	}

	return report, nil
}

func (e *Evaluator[Input, Output]) evaluate(testPack []Input) ([]float64, error) {
	responses, err := e.test(testPack)
	if err != nil {
		return nil, fmt.Errorf("failed to test: %w", err)
	}

	report := make([]float64, len(testPack))
	for i, response := range responses {
		res, resErr := response.Get()
		report[i] = e.options.GoodnessFunction(testPack[i], res, resErr)
	}

	return report, nil
}

func (e *Evaluator[Input, Output]) test(testPack []Input) ([]mo.Result[Output], error) {
	responses := make([]mo.Result[Output], len(testPack))

	for i, test := range testPack {
		response, err := e.tester.Test(test)
		if err != nil {
			responses[i] = mo.Err[Output](err)
		} else {
			responses[i] = mo.Ok(response)
		}
	}

	return responses, nil
}
