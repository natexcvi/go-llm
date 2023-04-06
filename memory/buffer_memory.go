package memory

import "github.com/natexcvi/go-llm/engines"

type BufferMemory struct {
	MaxHistory int
	Buffer     []*engines.ChatMessage
}

func (memory *BufferMemory) reduceBuffer() {
	if memory.MaxHistory > 0 && len(memory.Buffer) > memory.MaxHistory {
		memory.Buffer = memory.Buffer[1:]
	}
}

func (memory *BufferMemory) Add(msg *engines.ChatMessage) error {
	memory.Buffer = append(memory.Buffer, msg)
	memory.reduceBuffer()
	return nil
}

func (memory *BufferMemory) AddPrompt(prompt *engines.ChatPrompt) error {
	memory.Buffer = append(memory.Buffer, prompt.History...)
	memory.reduceBuffer()
	return nil
}

func (memory *BufferMemory) PromptWithContext(nextMessages ...*engines.ChatMessage) (*engines.ChatPrompt, error) {
	memory.Buffer = append(memory.Buffer, nextMessages...)
	memory.reduceBuffer()
	return &engines.ChatPrompt{
		History: memory.Buffer,
	}, nil
}

func NewBufferedMemory(maxHistory int) *BufferMemory {
	return &BufferMemory{
		MaxHistory: maxHistory,
	}
}
