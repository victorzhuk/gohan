package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

// drainSafePoint is the safe-point drain a turn loop calls once a tool
// batch's results are appended and before it decides to finish: it drains
// the run's mailbox and lets applySignals append every steer after those
// results, so a steer never lands between an assistant message and its
// tool results. A cancel ends the run with Done{cancelled}; a drained
// steer makes the loop take one more turn, counted against MaxTurns the
// way applySignals counts it, or ends the run with Done{StopLimit} when
// the bound is spent.
func (lc *Lifecycle) drainSafePoint(ctx context.Context, st runtime.State) (terminal, again bool, evs []types.Event, err error) {
	if lc.runs == nil {
		return false, false, nil, nil
	}
	sigs, err := lc.runs.Drain(ctx, lc.lease)
	if err != nil {
		return false, false, nil, err
	}
	return lc.applySignals(ctx, st, sigs)
}
