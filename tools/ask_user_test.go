package tools

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockStream(text string) io.Reader {
	return strings.NewReader(text)
}

func TestAskUser(t *testing.T) {
	testCases := []struct {
		name           string
		question       string
		mockResponse   string
		expectedOutput string
		expectedError  error
	}{
		{
			name:           "simple",
			question:       "What is your name?",
			mockResponse:   "John",
			expectedOutput: `{"answer": "John"}`,
		},
		{
			name:          "empty",
			question:      "What is your name?",
			mockResponse:  "",
			expectedError: fmt.Errorf("the user did not provide an answer"),
		},
		{
			name:           "reads only one line",
			question:       "What is your name?",
			mockResponse:   "John\nAlex",
			expectedOutput: `{"answer": "John"}`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			askUser := NewAskUserWithSource(mockStream(tc.mockResponse))
			output, err := askUser.Execute([]byte(`{"question": "` + tc.question + `"}`))
			if tc.expectedError != nil {
				require.EqualError(t, err, tc.expectedError.Error())
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tc.expectedOutput, string(output))
		})
	}

}
