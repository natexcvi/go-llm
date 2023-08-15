package evaluation

type GoodnessFunction[Input, Output any] func(input Input, output Output) float64

type Options[Input, Output any] struct {
	Provider         string
	Model            string
	GoodnessFunction GoodnessFunction[Input, Output]
	Repetitions      int
	MaximumTokens    int
}

type Evaluator[Input, Output any] interface {
	Evaluate(TestPack []Input) ([]float64, error)
}
