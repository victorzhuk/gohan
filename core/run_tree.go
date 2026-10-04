package gohan

import (
	"context"
	"iter"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// ChildRunInfo derives the tree identity a sub-flow run records: the root
// run id is inherited, the parent run id names the run in ctx and the
// depth is one past its parent's. A run outside any parent roots its own
// tree at depth zero.
func ChildRunInfo(ctx context.Context, runID string) types.RunInfo {
	info, ok := types.RunInfoFrom(ctx)
	child := info
	child.RunID = runID
	if !ok || info.RunID == "" {
		child.RootRunID = runID
		child.ParentRunID = ""
		child.Depth = 0
		return child
	}
	child.RootRunID = info.RootRunID
	if child.RootRunID == "" {
		child.RootRunID = info.RunID
	}
	child.ParentRunID = info.RunID
	child.Depth = info.Depth + 1
	return child
}

// FlowTool exposes a sub-flow as its parent's tool: the typed In is the
// only value that crosses into the child, the child runs with the tree
// identity derived from ctx, and its spend charges the tree budget the
// caller wired into its chains. A child error surfaces as the parent's,
// so a tree budget overrun aborts the hub.
func FlowTool[In, Out any](runID string, f types.FlowAsTool[In, Out]) types.FlowAsTool[In, Out] {
	return flowTool[In, Out]{runID: runID, f: f}
}

type flowTool[In, Out any] struct {
	runID string
	f     types.FlowAsTool[In, Out]
}

func (t flowTool[In, Out]) Invoke(ctx context.Context, in In) (Out, error) {
	return t.f.Invoke(types.WithRunInfo(ctx, ChildRunInfo(ctx, t.runID)), in)
}

// RunMeta pairs an event of the run info carries with its meta, so the
// tree identity rides every event the harness appends or delivers.
func RunMeta(info types.RunInfo, seq int64, at time.Time) types.EventMeta {
	return types.EventMeta{
		SessionID:   info.SessionID,
		RunID:       info.RunID,
		RootRunID:   info.RootRunID,
		ParentRunID: info.ParentRunID,
		Depth:       info.Depth,
		Flow:        info.Flow,
		Seq:         seq,
		Time:        at,
	}
}

// RunRow seeds a stored run with the identity info carries; the tree
// fields are the grouping key recovery replays the tree by.
func RunRow(info types.RunInfo) stores.Run {
	return stores.Run{
		SessionID:   info.SessionID,
		RunID:       info.RunID,
		RootRunID:   info.RootRunID,
		ParentRunID: info.ParentRunID,
		Depth:       info.Depth,
		Flow:        info.Flow,
		Mode:        info.Mode,
	}
}

// DriveTree drives rt and projects the tree budget st carries onto the
// root's Done.Cost.
func DriveTree(ctx context.Context, rt runtime.Runtime, r runtime.AgentRun, st *chains.LimitsState) iter.Seq2[types.Event, error] {
	return func(yield func(types.Event, error) bool) {
		for ev, err := range Drive(ctx, rt, r) {
			if err != nil {
				yield(nil, err)
				return
			}
			if d, ok := ev.(types.Done); ok {
				d.Cost = st.TreeCost()
				ev = d
			}
			if !yield(ev, nil) {
				return
			}
		}
	}
}
