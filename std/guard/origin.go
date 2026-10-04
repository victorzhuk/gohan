package guard

import (
	"context"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/types"
)

// Fence wraps every span whose origin is neither system nor user in the
// provider-appropriate delimiters from prompts, preceded once by the
// standing data-not-instructions instruction. Fenced content is data,
// never instructions.
func Fence(spans []types.Block, prompts chains.PromptSet) []types.Block {
	out := make([]types.Block, 0, len(spans)+3)
	fenced := false
	for _, b := range spans {
		if trustedOrigin(b.BlockOrigin().Kind) {
			out = append(out, b)
			continue
		}
		if !fenced {
			fenced = true
			out = append(out, types.Text{Text: prompts.DataNotInstructions})
		}
		out = append(out,
			types.Text{Text: prompts.FenceOpen},
			b,
			types.Text{Text: prompts.FenceClose},
		)
	}
	return out
}

func trustedOrigin(k types.OriginKind) bool {
	return k == types.OriginSystem || k == types.OriginUser
}

// OriginGuard refuses flow input whose blocks already claim a
// chain-assigned origin kind. The chain assigns OriginUser to flow input
// regardless of what the caller set; a claim of any other kind means the
// caller is trying to smuggle content in as if the chain had produced it.
// The chain-side overwrite itself is enforced by the runtime.
type OriginGuard struct{}

// NewOriginGuard builds the input guard that rejects caller-set origins.
func NewOriginGuard() *OriginGuard { return &OriginGuard{} }

// Name reports the decider name recorded by decision observability.
func (g *OriginGuard) Name() string { return "origin" }

// Decide implements guards.Guard.
func (g *OriginGuard) Decide(_ context.Context, in guards.GuardInput) (types.Decision[guards.GuardVerdict], error) {
	for _, b := range in.Blocks {
		switch kind := b.BlockOrigin().Kind; kind {
		case types.OriginModel, types.OriginTool, types.OriginProvider, types.OriginOperator:
			return types.Decision[guards.GuardVerdict]{
				Value: guards.GuardVerdict{
					Action: guards.Block,
					Reason: "caller-set chain origin",
				},
				Confidence: 1,
			}, nil
		}
	}
	return types.Decision[guards.GuardVerdict]{
		Value:      guards.GuardVerdict{Action: guards.Pass, Reason: "no caller-set origin"},
		Confidence: 1,
	}, nil
}
