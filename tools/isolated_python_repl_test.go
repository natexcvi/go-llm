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
			code: "import requests\nrequests.get('https://httpbin.org/get').json()\nprint('hello world')",
			modules: []string{
				"requests",
			},
			expOutput: `"hello world\n"`,
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
				require.EqualError(t, err, tc.expError.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expOutput, string(output))
		})
	}
}
