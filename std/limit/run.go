// Package limit ships the run limit policy and the local MaxInFlight
// bulkhead. The policy reads the run's ledger from the invocation context
// and enforces the run's limits against it; the per-endpoint ceiling arrives
// as a constructor argument.
package limit

import (
	"context"
	"iter"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// Limits returns the model-chain limit step over the run's ledger. It
// charges the message's usage at message end through the pricing, warns
// once at the soft ratio and aborts on a hard cost overrun or an expired
// wall clock. Passing MaxTurns only records the counter: the driver ends
// the run at its effect boundary. Fallback and retry run inside the step,
// so a chunk is charged once no matter how many endpoints it took to serve.
// Without a ledger in the context the step forwards unchanged.
func Limits(l types.RunLimits, p types.Pricing) types.ModelMiddleware {
	return func(next types.ModelFunc) types.ModelFunc {
		return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				st, ok := chains.LimitsStateFrom(ctx)
				if !ok {
					for chunk, err := range next(ctx, req) {
						if !yield(chunk, err) {
							return
						}
					}
					return
				}
				remaining, err := st.ChargeModelCall(l, time.Now())
				if err != nil {
					yield(types.ModelChunk{}, err)
					return
				}
				callCtx, cancel := context.WithTimeout(ctx, remaining)
				defer cancel()
				var usage types.Usage
				for chunk, cerr := range next(callCtx, req) {
					if cerr != nil {
						yield(chunk, cerr)
						return
					}
					if chunk.Usage != nil {
						usage = *chunk.Usage
					}
					if !yield(chunk, nil) {
						return
					}
					if callCtx.Err() != nil {
						yield(types.ModelChunk{}, &types.LimitExceededError{Limit: "MaxWallClock", Value: l.MaxWallClock.Seconds()})
						return
					}
				}
				if callCtx.Err() != nil {
					yield(types.ModelChunk{}, &types.LimitExceededError{Limit: "MaxWallClock", Value: l.MaxWallClock.Seconds()})
					return
				}
				st.Charge(usage, p)
				if err := st.AfterCharge(l); err != nil {
					yield(types.ModelChunk{}, err)
				}
			}
		}
	}
}

// ToolLimits returns the tool-chain limit step over the run's ledger. It
// charges each call against the tool total — consuming a reserved batch
// slot when the call runs inside one — and bounds the call by the wall
// clock the run has left, so a tool's context is cancelled when the run
// overruns. It never refuses a call for the counter: ending the run on
// MaxToolCalls belongs to the driver. Without a ledger in the context the
// step forwards unchanged.
func ToolLimits(l types.RunLimits) types.ToolMiddleware {
	return func(next types.ToolFunc) types.ToolFunc {
		return func(ctx context.Context, call types.ToolUse) (res types.ToolResult, err error) {
			st, ok := chains.LimitsStateFrom(ctx)
			if !ok {
				return next(ctx, call)
			}
			if !st.ConsumeReservation(ctx) {
				if _, err := st.ChargeToolCall(l, time.Now()); err != nil {
					return types.ToolResult{}, err
				}
			}
			remaining, err := st.WallClockLeft(l, time.Now())
			if err != nil {
				return types.ToolResult{}, err
			}
			callCtx, cancel := context.WithTimeout(ctx, remaining)
			defer cancel()
			res, err = next(callCtx, call)
			if err != nil && callCtx.Err() != nil && ctx.Err() == nil {
				return types.ToolResult{}, &types.LimitExceededError{Limit: "MaxWallClock", Value: l.MaxWallClock.Seconds()}
			}
			return res, err
		}
	}
}
