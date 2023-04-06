package engines

//go:generate mockgen -source=engine.go -destination=mocks/engine.go -package=mocks
type LLM interface {
	Predict(prompt *ChatPrompt) (*ChatMessage, error)
}
