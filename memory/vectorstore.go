package memory

type TextEmbedder interface {
	Embed(text string) []float64
}

type Vectorstore interface {
	Store(key []float64, value string) error
	FindNearest(key []float64, k int) ([]string, error)
}

type VectorstoreMemory struct {
	embedder TextEmbedder
	store    Vectorstore
}

// TODO: implement Memory for VectorstoreMemory
