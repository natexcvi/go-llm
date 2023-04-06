package memory

import (
	"testing"

	"github.com/natexcvi/go-llm/engines"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferMemory(t *testing.T) {
	testCases := []struct {
		name             string
		max              int
		existingMessages []*engines.ChatMessage
		newMessages      []*engines.ChatMessage
		expected         []*engines.ChatMessage
	}{
		{
			name: "no max",
			max:  0,
			existingMessages: []*engines.ChatMessage{
				{
					Text: "hello",
				},
				{
					Text: "world",
				},
			},
			newMessages: []*engines.ChatMessage{
				{
					Text: "!",
				},
			},
			expected: []*engines.ChatMessage{
				{
					Text: "hello",
				},
				{
					Text: "world",
				},
				{
					Text: "!",
				},
			},
		},
		{
			name: "max 2",
			max:  2,
			existingMessages: []*engines.ChatMessage{
				{
					Text: "hello",
				},
				{
					Text: "world",
				},
			},
			newMessages: []*engines.ChatMessage{
				{
					Text: "!",
				},
			},
			expected: []*engines.ChatMessage{
				{
					Text: "world",
				},
				{
					Text: "!",
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			memory := NewBufferedMemory(tc.max)
			for _, msg := range tc.existingMessages {
				memory.Add(msg)
			}
			assert.Equal(t, len(tc.existingMessages), len(memory.Buffer), "expected %d messages in buffer, got %d", len(tc.expected), len(memory.Buffer))
			prompt, err := memory.PromptWithContext(tc.newMessages...)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, prompt.History)
		})
	}
}
