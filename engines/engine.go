package engines

//go:generate mockgen -source=engine.go -destination=mocks/engine.go -package=mocks
type LLM interface {
	Chat(prompt *ChatPrompt) (*ChatMessage, error)
}

type LLMWithFunctionCalls interface {
	LLM
	ChatWithFunctions(prompt *ChatPrompt, functions []FunctionSpecs) (*ChatMessage, error)
}

type ParameterSpecs struct {
	Type        string                     `json:"type"`
	Description string                     `json:"description,omitempty"`
	Properties  map[string]*ParameterSpecs `json:"properties,omitempty"`
	Required    []string                   `json:"required,omitempty"`
	Items       *ParameterSpecs            `json:"items,omitempty"`
	Enum        []any                      `json:"enum,omitempty"`
}

type FunctionSpecs struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  *ParameterSpecs `json:"parameters"`
}
