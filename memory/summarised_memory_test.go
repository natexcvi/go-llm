package memory

import (
	"fmt"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/natexcvi/go-llm/engines"
	enginemocks "github.com/natexcvi/go-llm/engines/mocks"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarisedMemory(t *testing.T) {
	testCases := []struct {
		name               string
		recentMessageLimit int
		existingMessages   []*engines.ChatMessage
		newMessages        []*engines.ChatMessage
		currentMemoryState string
		expected           string
	}{
		{
			name:               "update memory state",
			recentMessageLimit: 1,
			existingMessages: []*engines.ChatMessage{
				{
					Role: engines.ConvRoleUser,
					Text: "hello",
				},
			},
			newMessages: []*engines.ChatMessage{
				{
					Role: engines.ConvRoleUser,
					Text: "world",
				},
				{
					Role: engines.ConvRoleSystem,
					Text: "the result of an action",
				},
			},
			currentMemoryState: "<memory state is empty>",
			expected:           "hello",
		},
		{
			name:               "memory state is empty",
			recentMessageLimit: 3,
			existingMessages:   []*engines.ChatMessage{},
			newMessages: []*engines.ChatMessage{
				{
					Role: engines.ConvRoleUser,
					Text: "Message 1",
				},
				{
					Role: engines.ConvRoleUser,
					Text: "Message 2",
				},
				{
					Role: engines.ConvRoleSystem,
					Text: "the result of an action",
				},
			},
			currentMemoryState: "<memory state is empty>",
			expected:           "",
		},
		{
			name:               "update memory state with capacity",
			recentMessageLimit: 1,
			existingMessages: []*engines.ChatMessage{
				{
					Role: engines.ConvRoleUser,
					Text: "hello",
				},
				{
					Role: engines.ConvRoleUser,
					Text: "world",
				},
			},
			newMessages: []*engines.ChatMessage{
				{
					Role: engines.ConvRoleSystem,
					Text: "OBS: the result of an action",
				},
			},
			currentMemoryState: "<memory state is empty>",
			expected:           "world",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			engineMock := enginemocks.NewMockLLM(ctrl)
			memState := tc.currentMemoryState
			engineMock.EXPECT().Predict(gomock.Any()).AnyTimes().DoAndReturn(func(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
				newMessages := prompt.History[2 : len(prompt.History)-1]
				newMessagesEnc := strings.Join(lo.Map(newMessages, func(msg *engines.ChatMessage, _ int) string {
					return msg.Text
				}), "\n")
				memState = memState + "\n" + newMessagesEnc
				return &engines.ChatMessage{
					Role: engines.ConvRoleAssistant,
					Text: memState,
				}, nil
			})
			memory := NewSummarisedMemory(tc.recentMessageLimit, engineMock)
			for _, msg := range tc.existingMessages {
				memory.Add(msg)
			}

			prompt, err := memory.PromptWithContext(tc.newMessages...)
			require.NoError(t, err)
			numExpectedPromptMsgs := lo.Min([]int{tc.recentMessageLimit, len(tc.existingMessages)}) + len(tc.newMessages) + 1
			assert.Len(t, prompt.History, numExpectedPromptMsgs, "history length")
			memoryStateMsg, ok := lo.Find(prompt.History, func(msg *engines.ChatMessage) bool {
				return msg.Role == engines.ConvRoleSystem && strings.HasPrefix(msg.Text, "Memory state:")
			})
			require.True(t, ok, "memory state message not found")
			assert.True(t, strings.HasSuffix(memoryStateMsg.Text, tc.expected), fmt.Sprintf("expected ends with: %s, got: %s", tc.expected, memoryStateMsg.Text))
		})
	}
}
