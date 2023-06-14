package memory

import (
	"fmt"

	"github.com/natexcvi/go-llm/engines"
	log "github.com/sirupsen/logrus"
)

type SummarisedMemory struct {
	recentMessageLimit int
	recentMessages     []*engines.ChatMessage
	originalPrompt     *engines.ChatPrompt
	memoryState        string
	model              engines.LLM
}

func (memory *SummarisedMemory) reduceBuffer() {
	if memory.recentMessageLimit > 0 && len(memory.recentMessages) > memory.recentMessageLimit {
		memory.recentMessages = memory.recentMessages[1:]
	}
}

func (memory *SummarisedMemory) updateMemoryState(msg ...*engines.ChatMessage) error {
	if memory.memoryState == "" {
		memory.memoryState = "<memory state is empty>"
	}
	prompt := engines.ChatPrompt{
		History: []*engines.ChatMessage{
			{
				Role: engines.ConvRoleSystem,
				Text: "You are a smart memory manager. The user sends you two or more messages: " +
					"one with the current memory state, and the rest with new messages " +
					"sent to their conversation with a smart, LLM based assistant. You should " +
					"update the memory state to reflect the new messages' content. " +
					"Your goal is for the memory state to be as compact as possible, " +
					"while still providing the smart assistant with all the information " +
					"it needs for completing its task. Specifically, you should make sure " +
					"you specify actions the assistant has taken and their results, as well as " +
					"intentions of the assistant and its action plan. Do not include any other text " +
					"in your response. Remember, the smart assistant will read it and use it " +
					"as its context for further action, so try to be helpful, as if " +
					"the assistant has just asked you what has been happening in the conversation " +
					"so far.",
			},
			{
				Role: engines.ConvRoleUser,
				Text: "The current memory state is:\n\n" + memory.memoryState,
			},
		},
	}
	for _, m := range msg {
		prompt.History = append(prompt.History, &engines.ChatMessage{
			Role: engines.ConvRoleUser,
			Text: fmt.Sprintf("New message:\n\nRole: %s\nContent: %s", m.Role, m.Text),
		})
	}
	prompt.History = append(prompt.History, &engines.ChatMessage{
		Role: engines.ConvRoleSystem,
		Text: "Please update the memory state to reflect the new messages. " +
			"Do not forget to give proper weight to the current memory state.",
	})
	updatedMemState, err := memory.model.Predict(&prompt)
	if err != nil {
		return fmt.Errorf("failed to update memory state: %w", err)
	}
	memory.memoryState = updatedMemState.Text
	log.Debugf("Updated memory state: %s", memory.memoryState)
	return nil
}

func (memory *SummarisedMemory) Add(msg *engines.ChatMessage) error {
	memory.recentMessages = append(memory.recentMessages, msg)
	memory.reduceBuffer()
	if err := memory.updateMemoryState(msg); err != nil {
		return fmt.Errorf("failed to update memory state: %w", err)
	}
	return nil
}

func (memory *SummarisedMemory) AddPrompt(prompt *engines.ChatPrompt) error {
	memory.originalPrompt = prompt
	return nil
}

func (memory *SummarisedMemory) PromptWithContext(nextMessages ...*engines.ChatMessage) (*engines.ChatPrompt, error) {
	promptMessages := make([]*engines.ChatMessage, 0, len(memory.recentMessages)+len(nextMessages))
	if memory.originalPrompt != nil {
		promptMessages = append(promptMessages, memory.originalPrompt.History...)
	}
	promptMessages = append(promptMessages, &engines.ChatMessage{
		Role: engines.ConvRoleSystem,
		Text: fmt.Sprintf("Memory state:\n\n%s", memory.memoryState),
	})
	memory.recentMessages = append(memory.recentMessages, nextMessages...)
	promptMessages = append(promptMessages, memory.recentMessages...)
	return &engines.ChatPrompt{
		History: promptMessages,
	}, nil
}

func NewSummarisedMemory(recentMessageLimit int, model engines.LLM) *SummarisedMemory {
	return &SummarisedMemory{
		recentMessageLimit: recentMessageLimit,
		model:              model,
	}
}
