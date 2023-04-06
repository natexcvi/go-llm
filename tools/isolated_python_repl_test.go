package tools

import (
	"encoding/json"
	"testing"
)

func TestPythonRepl(t *testing.T) {
	testCases := []struct {
		name      string
		code      string
		expError  error
		expOutput string
	}{
		{
			name:      "simple",
			code:      "print('hello world')",
			expOutput: `"hello world\n"`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repl := NewIsolatedPythonREPL()
			output, err := repl.Execute(json.RawMessage(
				`{"code": "` + tc.code + `"}`,
			))
			if err != tc.expError {
				t.Errorf("Unexpected error: %v", err)
			}
			if string(output) != tc.expOutput {
				t.Errorf("Unexpected output: %v", string(output))
			}
		})
	}
}
