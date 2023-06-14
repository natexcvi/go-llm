package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPythonRepl(t *testing.T) {
	testCases := []struct {
		name      string
		code      string
		modules   []string
		expError  error
		expOutput string
	}{
		{
			name:      "simple",
			code:      "print('hello world')",
			expOutput: `"hello world\n"`,
		},
		{
			name:     "error",
			code:     "print('hello world')\nprint(1/0)",
			expError: fmt.Errorf("python exited with code 1: Traceback (most recent call last):\n  File \"//app/script.py\", line 2, in <module>\n    print(1/0)\n          ~^~\nZeroDivisionError: division by zero\n"),
		},
		{
			name: "with modules",
			code: "import requests\nprint('hello world')",
			modules: []string{
				"requests",
			},
			expOutput: `"hello world\n"`,
		},
		{
			name: "no existing module - error",
			code: "import requests\nprint('hello world')",
			modules: []string{
				"requests",
				"does-not-exist",
			},
			expError: fmt.Errorf("python exited with code 1: ERROR: Could not find a version that satisfies the requirement does-not-exist"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repl := NewIsolatedPythonREPL()
			output, err := repl.Execute(json.RawMessage(
				fmt.Sprintf(
					`{"code": %q, "modules": [%s]}`,
					tc.code,
					strings.Join(
						lo.Map(tc.modules, func(in string, _ int) string { return fmt.Sprintf("%q", in) }),
						",",
					),
				),
			))
			if tc.expError != nil {
				actualError := err.Error()
				require.True(t, strings.HasPrefix(actualError, tc.expError.Error()), "expected error to start with %q, got %q", tc.expError.Error(), actualError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expOutput, string(output))
		})
	}
}
