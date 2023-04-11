package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyValueStore(t *testing.T) {
	testCases := []struct {
		name       string
		storeState map[string]string
		input      json.RawMessage
		output     json.RawMessage
		expErr     error
	}{
		{
			name:       "setting value",
			storeState: map[string]string{},
			input:      json.RawMessage(`{"command": "set", "key": "hello", "value": "world"}`),
			output:     json.RawMessage(`"stored successfully"`),
		},
		{
			name:       "getting value",
			storeState: map[string]string{"hello": "world"},
			input:      json.RawMessage(`{"command": "get", "key": "hello"}`),
			output:     json.RawMessage(`"world"`),
		},
		{
			name:       "list keys",
			storeState: map[string]string{"hello": "world"},
			input:      json.RawMessage(`{"command": "list"}`),
			output:     json.RawMessage(`["hello"]`),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			store := NewKeyValueStore()
			store.store = testCase.storeState
			output, err := store.Execute(testCase.input)
			if testCase.expErr != nil {
				require.EqualError(t, err, testCase.expErr.Error())
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, string(testCase.output), string(output))
		})
	}
}
