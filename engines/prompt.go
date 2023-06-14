package engines

type ConvRole string

const (
	ConvRoleUser      ConvRole = "user"
	ConvRoleSystem    ConvRole = "system"
	ConvRoleAssistant ConvRole = "assistant"
	ConvRoleFunction  ConvRole = "function"
)

type ChatMessage struct {
	Role         ConvRole      `json:"role"`
	Text         string        `json:"content"`
	FunctionCall *FunctionCall `json:"function_call,omitempty"`
	Name         string        `json:"name,omitempty"`
}

type FunctionCall struct {
	Name string `json:"name"`
	Args string `json:"arguments"`
}

type ChatPrompt struct {
	History []*ChatMessage
}
