package embedding

// Embedder is the interface that all embedding providers must implement.
type Embedder interface {
	Embed(texts []string) ([]EmbeddingResult, error)
	EmbedSingle(text string) ([]float32, error)
	Model() string
	Dimensions() int
	SetModel(model string)
	SetDimensions(d int)
	SetBaseURL(url string)
}
