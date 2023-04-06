package memory

import "github.com/natexcvi/go-llm/engines"

//go:generate mockgen -source=memory.go -destination=mocks/memory.go -package=mocks
type Memory interface {
	Add(msg *engines.ChatMessage) error
	AddPrompt(prompt *engines.ChatPrompt) error
	PromptWithContext(nextMessages ...*engines.ChatMessage) (*engines.ChatPrompt, error)
}
