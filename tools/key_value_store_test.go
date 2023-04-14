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

func TestKeyValueStorePreprocessing(t *testing.T) {
	testCases := []struct {
		name   string
		input  string
		output string
	}{
		{
			name:   "no store",
			input:  "hello world",
			output: "hello world",
		},
		{
			name:   "single store",
			input:  "hello {{ store \"key\" }}",
			output: "hello world",
		},
		{
			name:   "multiple stores",
			input:  "hello {{ store \"key1\" }} {{ store \"key2\" }}",
			output: "hello world world",
		},
		{
			name:   "multiple stores with other text",
			input:  "hello {{ store \"key1\" }} world {{ store \"key2\" }}",
			output: "hello world world world",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			store := NewKeyValueStore()
			store.store = map[string]string{
				"key":  "world",
				"key1": "world",
				"key2": "world",
			}
			marshaledInput, err := json.Marshal(testCase.input)
			require.NoError(t, err)
			output, err := store.Process(marshaledInput)
			require.NoError(t, err)
			var unmarshaledOutput string
			err = json.Unmarshal(output, &unmarshaledOutput)
			require.NoError(t, err)
			assert.Equal(t, testCase.output, unmarshaledOutput)
		})
	}
}
