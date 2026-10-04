package types

// DeltaKind identifies what a streaming ModelChunk carries.
type DeltaKind int

const (
	DeltaText DeltaKind = iota
	DeltaReasoning
	DeltaToolArgs
)

// FinishReason explains why a model stream ended.
type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishToolUse   FinishReason = "tool_use"
	FinishMaxTokens FinishReason = "max_tokens"
	FinishRefusal   FinishReason = "refusal"
	FinishError     FinishReason = "error"
)

// ModelChunk is one increment of a model stream.
type ModelChunk struct {
	Kind    DeltaKind
	Delta   string
	ToolUse *ToolUse
	Usage   *Usage
	Finish  FinishReason
}

// Usage accumulates token and cost accounting for a model call.
type Usage struct {
	InputTokens       int
	CachedInputTokens int
	OutputTokens      int
	CacheWriteTokens  int
	SandboxSeconds    float64
	ProviderToolCalls map[string]int
	KeyID             string
	ModelVersion      string
	Estimated         bool
}
