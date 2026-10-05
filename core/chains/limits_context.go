package chains

import "context"

type limitsStateKey struct{}

// WithLimitsState returns a context that carries one run's ledger. The
// invocation factory sets it once per run; a preset never captures one.
func WithLimitsState(ctx context.Context, st *LimitsState) context.Context {
	return context.WithValue(ctx, limitsStateKey{}, st)
}

// LimitsStateFrom reports the ledger the context carries, if any.
func LimitsStateFrom(ctx context.Context) (*LimitsState, bool) {
	st, ok := ctx.Value(limitsStateKey{}).(*LimitsState)
	if !ok || st == nil {
		return nil, false
	}
	return st, true
}
