package evaluation

import "github.com/natexcvi/go-llm/engines"

type llmRunner struct {
	llm engines.LLM
}

// NewLLMRunner returns a new llm runner that can be used to evaluate the output.
func NewLLMRunner(llm engines.LLM) Runner[*engines.ChatPrompt, *engines.ChatMessage] {
	return &llmRunner{
		llm: llm,
	}
}

func (t *llmRunner) Run(input *engines.ChatPrompt) (*engines.ChatMessage, error) {
	return t.llm.Chat(input)
}
