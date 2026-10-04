package gohan

import "github.com/victorzhuk/gohan/core/types"

// metaReasoning records on an assistant message the opaque reasoning the
// provider streamed for it. Gating Caps.ReasoningVisible controls only the
// ReasoningDelta the consumer sees; the retained text rides on the message
// in history, so a provider that requires its reasoning returned on the
// next request still gets it.
const metaReasoning = "reasoning"

// retainReasoning records the accumulated reasoning text on an assistant
// message at completion.
func retainReasoning(m *types.Message, reasoning string) {
	if reasoning == "" {
		return
	}
	if m.Meta == nil {
		m.Meta = make(map[string]any, 1)
	}
	m.Meta[metaReasoning] = reasoning
}
