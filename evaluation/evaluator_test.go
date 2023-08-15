package evaluation

import (
	"github.com/golang/mock/gomock"
	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/engines/mocks"
	"github.com/stretchr/testify/assert"
	"math"
	"strings"
	"testing"
)

func createMockEchoLLM(t *testing.T) engines.LLM {
	t.Helper()
	ctrl := gomock.NewController(t)
	mock := mocks.NewMockLLM(ctrl)
	mock.EXPECT().Chat(gomock.Any()).DoAndReturn(func(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
		return &engines.ChatMessage{
			Text: prompt.History[0].Text,
		}, nil
	}).AnyTimes()
	return mock
}

func createMockIncrementalLLM(t *testing.T) engines.LLM {
	t.Helper()
	ctrl := gomock.NewController(t)
	mock := mocks.NewMockLLM(ctrl)
	counters := make(map[string]int)
	mock.EXPECT().Chat(gomock.Any()).DoAndReturn(func(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
		counters[prompt.History[0].Text]++
		return &engines.ChatMessage{
			Text: strings.Repeat(prompt.History[0].Text, counters[prompt.History[0].Text]),
		}, nil
	}).AnyTimes()
	return mock
}

func createMockExponentialLLM(t *testing.T) engines.LLM {
	t.Helper()
	ctrl := gomock.NewController(t)
	mock := mocks.NewMockLLM(ctrl)
	counters := make(map[string]int)
	mock.EXPECT().Chat(gomock.Any()).DoAndReturn(func(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
		counters[prompt.History[0].Text]++
		return &engines.ChatMessage{
			Text: strings.Repeat(prompt.History[0].Text, int(math.Pow(float64(len(prompt.History[0].Text)), float64(counters[prompt.History[0].Text]+1)))),
		}, nil
	}).AnyTimes()
	return mock
}

func TestLLMEvaluator(t *testing.T) {
	tests := []struct {
		name     string
		options  *Options[*engines.ChatPrompt, *engines.ChatMessage]
		engine   engines.LLM
		testPack []*engines.ChatPrompt
		want     []float64
	}{
		{
			name: "Test echo engine with response length goodness and 1 repetition",
			options: &Options[*engines.ChatPrompt, *engines.ChatMessage]{
				GoodnessFunction: func(_ *engines.ChatPrompt, response *engines.ChatMessage) float64 {
					return float64(len(response.Text))
				},
				Repetitions: 1,
			},
			engine: createMockEchoLLM(t),
			testPack: []*engines.ChatPrompt{
				{
					History: []*engines.ChatMessage{
						{
							Text: "Hello",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "Hello Hello",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "Hello Hello Hello Hello",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "Hello Hello Hello Hello Hello Hello",
						},
					},
				},
			},
			want: []float64{5, 11, 23, 35},
		},
		{
			name: "Test echo engine with response length goodness and 5 repetitions",
			options: &Options[*engines.ChatPrompt, *engines.ChatMessage]{
				GoodnessFunction: func(_ *engines.ChatPrompt, response *engines.ChatMessage) float64 {
					return float64(len(response.Text))
				},
				Repetitions: 5,
			},
			engine: createMockEchoLLM(t),
			testPack: []*engines.ChatPrompt{
				{
					History: []*engines.ChatMessage{
						{
							Text: "Hello",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "Hello Hello",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "Hello Hello Hello Hello",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "Hello Hello Hello Hello Hello Hello",
						},
					},
				},
			},
			want: []float64{5, 11, 23, 35},
		},
		{
			name: "Test incremental engine with response length goodness and 5 repetitions",
			options: &Options[*engines.ChatPrompt, *engines.ChatMessage]{
				GoodnessFunction: func(_ *engines.ChatPrompt, response *engines.ChatMessage) float64 {
					return float64(len(response.Text))
				},
				Repetitions: 5,
			},
			engine: createMockIncrementalLLM(t),
			testPack: []*engines.ChatPrompt{
				{
					History: []*engines.ChatMessage{
						{
							Text: "a",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "aa",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "aaa",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "aaaa",
						},
					},
				},
			},
			want: []float64{3, 6, 9, 12},
		},
		{
			name: "Test exponential engine with response length goodness and 4 repetitions",
			options: &Options[*engines.ChatPrompt, *engines.ChatMessage]{
				GoodnessFunction: func(_ *engines.ChatPrompt, response *engines.ChatMessage) float64 {
					return float64(len(response.Text))
				},
				Repetitions: 4,
			},
			engine: createMockExponentialLLM(t),
			testPack: []*engines.ChatPrompt{
				{
					History: []*engines.ChatMessage{
						{
							Text: "a",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "aa",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "aaa",
						},
					},
				},
				{
					History: []*engines.ChatMessage{
						{
							Text: "aaaa",
						},
					},
				},
			},
			want: []float64{1, 30, 270, 1360},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tester := NewLLMTester(tt.engine)
			evaluator := NewEvaluator(tester, tt.options)

			got, err := evaluator.Evaluate(tt.testPack)
			if err != nil {
				t.Fatal(err)
			}

			assert.Equal(t, tt.want, got)
		})
	}
}