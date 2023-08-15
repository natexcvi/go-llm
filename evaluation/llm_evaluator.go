package evaluation

import "github.com/natexcvi/go-llm/engines"

type llmTester struct {
	llm engines.LLM
}

func NewLLMTester(llm engines.LLM) Tester[*engines.ChatPrompt, *engines.ChatMessage] {
	return &llmTester{
		llm: llm,
	}
}

func (t *llmTester) Test(test *engines.ChatPrompt) (*engines.ChatMessage, error) {
	return t.llm.Chat(test)
}
