package tools

import (
	"encoding/json"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/natexcvi/go-llm/engines"
	enginemocks "github.com/natexcvi/go-llm/engines/mocks"
	"github.com/stretchr/testify/assert"
)

func TestJSONAutoFixer_Process(t *testing.T) {
	testCases := []struct {
		name           string
		maxRetries     int
		raw            string
		expectedErr    error
		expectedOutput string
		modelResponses []string
	}{
		{
			name:           "Valid JSON",
			maxRetries:     1,
			raw:            `{"foo":"bar"}`,
			expectedErr:    nil,
			expectedOutput: `{"foo":"bar"}`,
			modelResponses: []string{},
		},
		{
			name:           "Error JSON",
			maxRetries:     1,
			raw:            `{"foo":"bar"`,
			expectedErr:    nil,
			expectedOutput: `{"foo": "bar"}`,
			modelResponses: []string{
				`{"foo": "bar"}`,
			},
		},
		{
			name:           "Error JSON with max retries",
			maxRetries:     2,
			raw:            `{"foo":"bar"`,
			expectedErr:    nil,
			expectedOutput: `{"foo": "bar"}`,
			modelResponses: []string{
				`{"foo": "bar"`,
				`{"foo": "bar"}`,
			},
		},
		{
			name:           "Error JSON - max retries exceeded",
			maxRetries:     2,
			raw:            `{"foo":"bar"`,
			expectedErr:    ErrMaxRetriesExceeded,
			expectedOutput: `{"foo": "bar"`,
			modelResponses: []string{
				`{"foo": "bar"`,
				`{"foo": "bar"`,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			engineMock := enginemocks.NewMockLLM(ctrl)
			i := -1
			engineMock.EXPECT().Predict(gomock.Any()).DoAndReturn(func(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
				i++
				return &engines.ChatMessage{
					Role: engines.ConvRoleAssistant,
					Text: tc.modelResponses[i],
				}, nil
			}).Times(len(tc.modelResponses))
			autoFixer := NewJSONAutoFixer(engineMock, tc.maxRetries)
			output, err := autoFixer.Process(json.RawMessage(tc.raw))
			if tc.expectedErr != nil {
				assert.ErrorIs(t, err, tc.expectedErr, "error")
				assert.Nil(t, output, "output")
			} else {
				assert.NoError(t, err, "error")
				assert.Equal(t, json.RawMessage(tc.expectedOutput), output, "output")
			}
		})
	}
}
