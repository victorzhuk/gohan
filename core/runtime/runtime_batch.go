package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

const NotExecutedPrefix = "not_executed: "

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
	Calls     []types.ToolUse
	Limits    types.RunLimits
	Used      int
	Gate      BatchGate
	Exec      BatchExec
	Scheduler SchedulerConfig
}

// BatchResult is the settled result of one call, in call order.
type BatchResult struct {
	Call   types.ToolUse
	Result types.ToolResult
}

// BatchSuspend reports the ask that suspended the batch. Pending lists every
// ask in call order, starting with Call; they settle on resume, one at a time.
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
	if b.Used > b.Limits.MaxToolCalls || len(b.Calls) > b.Limits.MaxToolCalls-b.Used {
		return BatchReport{}, fmt.Errorf("%w: need %d, %d of %d remain",
			ErrBatchOverrun, len(b.Calls), b.Limits.MaxToolCalls-b.Used, b.Limits.MaxToolCalls)
	}
	decisions := make([]BatchDecision, len(b.Calls))
	for i, call := range b.Calls {
		decisions[i] = b.Gate(ctx, call)
	}
	report := BatchReport{Results: make([]BatchResult, 0, len(b.Calls)), Spent: len(b.Calls)}
	allowed := make([]types.ToolUse, 0, len(b.Calls))
	askCalls := make([]types.ToolUse, 0, len(b.Calls))
	allowedSlots := make([]int, 0, len(b.Calls))
	for i, call := range b.Calls {
		switch decisions[i].Outcome {
		case BatchDeny, BatchTaintDenied:
			report.Results = append(report.Results, BatchResult{Call: call, Result: types.ToolResult{
				ID: call.ID, Outcome: types.Failed,
				Error: &types.ToolError{Kind: types.Permanent, Message: NotExecutedPrefix + decisions[i].Reason},
			}})
		case BatchAsk:
			askCalls = append(askCalls, call)
		default:
			allowedSlots = append(allowedSlots, len(report.Results))
			report.Results = append(report.Results, BatchResult{Call: call})
			allowed = append(allowed, call)
		}
	}
	if len(askCalls) > 0 {
		report.Suspend = &BatchSuspend{Call: askCalls[0], Pending: askCalls}
	}
	results, err := Schedule(ctx, allowed, ToolFunc(b.Exec), b.Scheduler)
	for j, res := range results {
		report.Results[allowedSlots[j]].Result = res
	}
	if err != nil {
		return report, err
	}
	return report, nil
}
