package std

import (
	"context"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// Shield returns the cancel-shield middleware. A SideEffect call runs under
// context.WithoutCancel, so a client disconnect cannot interrupt it: the
// result is produced and recorded by the inner steps first, and only then
// does the call report context.Canceled back to the run. ReadOnly calls are
// not shielded and cost nothing here.
func Shield(lookup SpecLookup) chains.ToolMiddleware {
	return func(next chains.ToolFunc) chains.ToolFunc {
		return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			effect := types.SideEffect
			if lookup != nil {
				if spec, ok := lookup(call.Name); ok {
					effect = spec.Effect
				}
			}
			if effect != types.SideEffect {
				return next(ctx, call)
			}
			shielded := context.WithoutCancel(ctx)
			res, err := next(shielded, call)
			if ctx.Err() != nil {
				return res, context.Canceled
			}
			return res, err
		}
	}
}
