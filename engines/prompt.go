package engines

type ConvRole string

const (
	ConvRoleUser      ConvRole = "user"
	ConvRoleSystem    ConvRole = "system"
	ConvRoleAssistant ConvRole = "assistant"
)

type ChatMessage struct {
	Role ConvRole `json:"role"`
	Text string   `json:"content"`
}

type ChatPrompt struct {
	History []*ChatMessage
}
