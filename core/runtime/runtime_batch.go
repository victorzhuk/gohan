package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// notExecutedPrefix begins the reason of every result for a call the batch
// did not execute; the frozen form is normative in runtime/spec.md.
const notExecutedPrefix = "not_executed: "

// BatchOutcome is a pre-execution decision for one call in a batch.
type BatchOutcome int

const (
	BatchAllow BatchOutcome = iota
	BatchDeny
	BatchTaintDenied
	BatchAsk
)

// BatchDecision carries the gate's verdict for one call before the batch
// executes. Reason states the denial; it renders after notExecutedPrefix.
type BatchDecision struct {
	Outcome BatchOutcome
	Reason  string
}

// BatchGate decides one call without running it. The driver wires the
// permission seam into it.
type BatchGate func(ctx context.Context, call types.ToolUse) BatchDecision

// BatchExec runs one gated call. The driver's governed call path backs it;
// errors other than tool failures abort the run.
type BatchExec func(ctx context.Context, call types.ToolUse) (types.ToolResult, error)

// ErrBatchOverrun reports a batch whose reservation exceeds MaxToolCalls.
// The run aborts with nothing executed.
var ErrBatchOverrun = errors.New("gohan: tool batch exceeds MaxToolCalls")

// Batch is one turn's tool calls under the batch protocol. Used is the
// tool-call count the run already spent.
type Batch struct {
	Calls  []types.ToolUse
	Limits types.RunLimits
	Used   int
	Gate   BatchGate
	Exec   BatchExec
}

// BatchResult is the settled result of one call, in call order.
type BatchResult struct {
	Call   types.ToolUse
	Result types.ToolResult
}

// BatchSuspend reports the ask that suspended the batch. Its call and every
// pending ask after it settle on resume, one at a time.
type BatchSuspend struct {
	Call    types.ToolUse
	Pending []types.ToolUse
}

// BatchReport is the outcome of one batch. Results holds one entry per
// settled call in call order; a call the batch did not execute carries a
// Failed(Permanent) result whose reason begins with notExecutedPrefix.
type BatchReport struct {
	Results []BatchResult
	Suspend *BatchSuspend
	// Spent is the batch's reservation against MaxToolCalls.
	Spent int
}

// Run decides the whole batch before the first call executes: every call is
// gated and reserved against MaxToolCalls up front, an overrun aborts with
// ErrBatchOverrun and nothing executed, denials render at once, and the
// allowed calls run through Exec before the first ask suspends the batch.
func (b Batch) Run(ctx context.Context) (BatchReport, error) {
	if b.Used+len(b.Calls) > b.Limits.MaxToolCalls {
		return BatchReport{}, fmt.Errorf("%w: need %d, %d of %d remain",
			ErrBatchOverrun, len(b.Calls), b.Limits.MaxToolCalls-b.Used, b.Limits.MaxToolCalls)
	}

	report := BatchReport{Spent: len(b.Calls)}
	var suspend *BatchSuspend
	for _, call := range b.Calls {
		if suspend != nil {
			// Asks settle one at a time across resumes; allowed calls are
			// never held back behind an ask.
			if b.Gate(ctx, call).Outcome == BatchAsk {
				suspend.Pending = append(suspend.Pending, call)
			}
			continue
		}
		dec := b.Gate(ctx, call)
		switch dec.Outcome {
		case BatchDeny, BatchTaintDenied:
			report.Results = append(report.Results, BatchResult{
				Call: call,
				Result: types.ToolResult{
					ID:      call.ID,
					Outcome: types.Failed,
					Error:   &types.ToolError{Kind: types.Permanent, Message: notExecutedPrefix + dec.Reason},
				},
			})
		case BatchAsk:
			suspend = &BatchSuspend{Call: call}
		default:
			res, err := b.Exec(ctx, call)
			if err != nil {
				return report, err
			}
			report.Results = append(report.Results, BatchResult{Call: call, Result: res})
		}
	}
	report.Suspend = suspend
	return report, nil
}
