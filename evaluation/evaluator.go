package evaluation

import (
	"fmt"
	"github.com/samber/mo"
)

// GoodnessFunction is a function that takes an input, an output and an error (if one occurred) and returns a float64
// which represents the goodness score of the output.
type GoodnessFunction[Input, Output any] func(input Input, output Output, err error) float64

// Options is a struct that contains the options for the evaluator.
type Options[Input, Output any] struct {
	// The goodness function that will be used to evaluate the output.
	GoodnessFunction GoodnessFunction[Input, Output]
	// The number of times the test will be repeated. The goodness level of each output will be
	// averaged.
	Repetitions int
}

// Runner is an interface that represents a test runner that will be used to evaluate the output.
// It takes an input and returns an output and an error.
type Runner[Input, Output any] interface {
	Run(input Input) (Output, error)
}

// Evaluator is a struct that runs the tests and evaluates the outputs.
type Evaluator[Input, Output any] struct {
	options *Options[Input, Output]
	runner  Runner[Input, Output]
}

// Creates a new `Evaluator` with the provided configuration.
func NewEvaluator[Input, Output any](runner Runner[Input, Output], options *Options[Input, Output]) *Evaluator[Input, Output] {
	return &Evaluator[Input, Output]{
		options: options,
		runner:  runner,
	}
}

// Runs the tests and evaluates the outputs. The function receives a test pack
// which is a slice of inputs and returns a slice of float64 which represents the goodness level
// of each respective output.
func (e *Evaluator[Input, Output]) Evaluate(testPack []Input) []float64 {
	repetitionChannels := make([]chan []float64, e.options.Repetitions)

	for i := 0; i < e.options.Repetitions; i++ {
		repetitionChannels[i] = make(chan []float64)
		go func(i int) {
			report, err := e.evaluate(testPack)
			if err != nil {
				repetitionChannels[i] <- nil
				return
			}
			repetitionChannels[i] <- report
		}(i)
	}

	responses := make([][]float64, e.options.Repetitions)
	for i := 0; i < e.options.Repetitions; i++ {
		responses[i] = <-repetitionChannels[i]
	}

	report := make([]float64, len(testPack))
	for i := 0; i < len(testPack); i++ {
		sum := 0.0
		for j := 0; j < e.options.Repetitions; j++ {
			sum += responses[j][i]
		}
		report[i] = sum / float64(e.options.Repetitions)
	}

	return report
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
		response, err := e.runner.Run(test)
		if err != nil {
			responses[i] = mo.Err[Output](err)
		} else {
			responses[i] = mo.Ok(response)
		}
	}

	return responses, nil
}
