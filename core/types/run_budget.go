package types

import (
	"context"
	"time"
)

// RunBudget is the effective budget one sub-flow call derived for its child.
type RunBudget struct {
	WallClock time.Duration
	Cost      float64
	Depth     int
}

// ctxRunBudget continues the ctxKey numbering identity.go starts.
const ctxRunBudget ctxKey = 5

// WithRunBudget attaches the budget a parent derived for its child. Only
// harness code on the sub-flow call sets it.
func WithRunBudget(ctx context.Context, b RunBudget) context.Context {
	return context.WithValue(ctx, ctxRunBudget, b)
}

// RunBudgetFrom reports the budget in ctx, or ok == false when none was
// attached.
func RunBudgetFrom(ctx context.Context) (RunBudget, bool) {
	if ctx == nil {
		return RunBudget{}, false
	}
	b, ok := ctx.Value(ctxRunBudget).(RunBudget)
	return b, ok
}
