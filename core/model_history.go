package gohan

import "github.com/victorzhuk/gohan/core/types"

// metaFinish records the FinishReason a model stream ended with on the
// assistant Message appended to the history; row 23's emission sets it.
const metaFinish = "finish"

// ProviderHistory projects the messages a provider may see on replay or
// continuation. A partial assistant message that ended with FinishError is
// terminal: it is never re-sent to a provider (model.partial-terminal-on-replay).
// The input slice is never modified.
func ProviderHistory(msgs []types.Message) []types.Message {
	out := msgs[:0:0]
	for _, m := range msgs {
		if m.Role == types.RoleAssistant && m.Meta[metaFinish] == types.FinishError {
			continue
		}
		out = append(out, m)
	}
	return out
}
