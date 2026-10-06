package types

import (
	"context"
	"encoding/json"
)

// Verifier settles one journal entry the session still holds as Unknown.
// It runs the tool's read-only Verify, records the reconciled outcome and
// its audit record, and reports whether the entry was settled. A key it
// could not settle stays in the session's uncertainty set.
type Verifier interface {
	Reconcile(ctx context.Context, tool string, args json.RawMessage, key CallKey, res ToolResult) (ToolResult, bool, error)
}
