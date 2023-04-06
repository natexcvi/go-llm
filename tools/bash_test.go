package tools

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBash(t *testing.T) {
	testCases := []struct {
		name   string
		bash   *BashTerminal
		input  json.RawMessage
		output json.RawMessage
		expErr error
	}{
		{
			name:   "simple",
			bash:   NewBashTerminal(),
			input:  json.RawMessage(`{"command": "echo hello, world"}`),
			output: json.RawMessage(`"hello, world\n"`),
		},
		{
			name:   "error",
			bash:   NewBashTerminal(),
			input:  json.RawMessage(`{"command": "cat no-such-file"}`),
			expErr: fmt.Errorf("bash exited with code 1: cat: no-such-file: No such file or directory\n"),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			output, err := testCase.bash.Execute(testCase.input)
			if testCase.expErr != nil {
				require.EqualError(t, err, testCase.expErr.Error())
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, string(testCase.output), string(output))
		})
	}
}
