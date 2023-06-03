package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPythonREPL(t *testing.T) {
	testCases := []struct {
		name   string
		repl   *PythonREPL
		input  json.RawMessage
		output json.RawMessage
		expErr error
	}{
		{
			name:   "simple",
			repl:   NewPythonREPL(),
			input:  json.RawMessage(`{"code": "print(1 + 1)"}`),
			output: json.RawMessage(`2`),
		},
		{
			name:   "error",
			repl:   NewPythonREPL(),
			input:  json.RawMessage(`{"code": "print(1 + 1"}`),
			expErr: fmt.Errorf("python exited with code 1:   File \"<string>\", line 1\n    print(1 + 1\n         ^\nSyntaxError: '(' was never closed\n"),
		},
		{
			name: "multiline code",
			repl: NewPythonREPL(),
			input: json.RawMessage(`{
				"code": "print('[')\nfor i in range(3):\n    print(i)\n    print(',')\nprint('9]')"
			}`),
			output: json.RawMessage(`[0,1,2,9]`),
		},
		{
			name: "with modules",
			repl: NewPythonREPL(),
			input: json.RawMessage(`{
				"code": "import dotenv\ndotenv.load_dotenv()\nprint([1,2,3])",
				"modules": ["python-dotenv"]
			}`),
			output: json.RawMessage(`[1,2,3]`),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Cleanup(func() {
				require.NoError(t, os.RemoveAll(".venv"))
			})
			output, err := testCase.repl.Execute(testCase.input)
			if testCase.expErr != nil {
				require.EqualError(t, err, testCase.expErr.Error())
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, string(testCase.output), string(output))
		})
	}
}
