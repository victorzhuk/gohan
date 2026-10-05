package std

import (
	"context"
	"iter"
	"strings"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// PromptMiddleware places the authored strings of a caller-supplied
// PromptSet ahead of every model call as one fenced system block. The
// strings are model-facing, so the model middleware, not a chain step, is
// their honest carrier. The set is a parameter, never a captured field:
// drivers that resolve the run's set through the stack register it with
// WithPrompts instead and read nothing from this middleware.
func PromptMiddleware(ps chains.PromptSet) types.ModelMiddleware {
	return func(next types.ModelFunc) types.ModelFunc {
		return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			req.System = append(preamble(ps), req.System...)
			return next(ctx, req)
		}
	}
}

// preamble renders every model-facing PromptSet string as one system
// block, each accounted for by its named field.
func preamble(ps chains.PromptSet) []types.Block {
	return []types.Block{types.Text{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginSystem}},
		Text: strings.Join([]string{
			ps.FenceOpen,
			ps.DataNotInstructions,
			ps.FenceClose,
			ps.OutcomeUnknown,
			ps.ReadBackHint,
			ps.OutputRefHint,
			ps.RepairInstruction,
			ps.NotesPreamble,
			ps.OperatorTurn,
		}, "\n"),
	}}
}
