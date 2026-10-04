package gohan

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

const treePricing = 0.04

func treeSpend(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "x", Usage: &types.Usage{InputTokens: 1}}, nil)
	}
}

type treeChild struct {
	st     *chains.LimitsState
	limits types.RunLimits
}

func (f treeChild) Invoke(ctx context.Context, _ struct{}) (string, error) {
	mw := chains.Limits(f.limits, types.Pricing{Input: treePricing}, f.st)
	for _, err := range mw(treeSpend)(ctx, types.ModelRequest{}) {
		if err != nil {
			return "", err
		}
	}
	return "ok", nil
}

func treeHubLimits() types.RunLimits {
	return types.RunLimits{
		MaxCost:      0.10,
		SoftRatio:    0.8,
		MaxTurns:     10,
		MaxToolCalls: 10,
		MaxWallClock: time.Minute,
	}
}

type doneRuntime struct{}

func (doneRuntime) Name() string                         { return "tree" }
func (doneRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }
func (doneRuntime) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}
func (doneRuntime) Step(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	return runtime.State{}, nil, runtime.DoneStatus, nil
}

func TestRunTree(t *testing.T) {
	t.Run("identity.tree-budget", func(t *testing.T) {
		hub := chains.NewLimitsState()
		limits := treeHubLimits()
		ctx := types.WithRunInfo(context.Background(), types.RunInfo{RunID: "hub", RootRunID: "hub", Flow: "hub"})
		invoke := func(id string, branch *chains.LimitsState) error {
			child := FlowTool(id, treeChild{st: branch, limits: limits})
			_, err := child.Invoke(ctx, struct{}{})
			return err
		}

		if err := invoke("child1", hub.Branch()); err != nil {
			t.Fatalf("first sub-flow: %v", err)
		}
		if err := invoke("child2", hub.Branch()); err != nil {
			t.Fatalf("second sub-flow: %v", err)
		}
		err := invoke("child3", hub.Branch())
		var over *types.LimitExceededError
		if !errors.As(err, &over) || over.Limit != "MaxCost" {
			t.Fatalf("third sub-flow err = %v, want *LimitExceededError{Limit: MaxCost}", err)
		}
		err = invoke("child4", hub.Branch())
		if !errors.As(err, &over) || over.Limit != "MaxCost" {
			t.Fatalf("hub err = %v, want the tree overrun to abort the hub", err)
		}
		if hub.TreeCost() != 0.12 {
			t.Fatalf("tree cost = %v, want 0.12", hub.TreeCost())
		}
	})

	t.Run("a child records the root, parent and depth", func(t *testing.T) {
		ctx := types.WithRunInfo(context.Background(), types.RunInfo{
			RunID:     "parent",
			RootRunID: "root",
			SessionID: "s1",
			Flow:      "parent",
		})
		info := ChildRunInfo(ctx, "child")
		if info.RootRunID != "root" || info.ParentRunID != "parent" || info.Depth != 1 || info.RunID != "child" {
			t.Fatalf("child info = %+v, want root, parent and depth 1", info)
		}
		row := RunRow(info)
		if row.RootRunID != "root" || row.ParentRunID != "parent" || row.Depth != 1 ||
			row.RunID != "child" || row.SessionID != "s1" || row.Flow != "parent" {
			t.Fatalf("run row = %+v, want the tree identity recorded", row)
		}
		meta := RunMeta(info, 3, time.Unix(1, 0))
		if meta.RootRunID != "root" || meta.ParentRunID != "parent" || meta.Depth != 1 || meta.Seq != 3 {
			t.Fatalf("event meta = %+v, want the tree identity on the meta", meta)
		}
		root := ChildRunInfo(context.Background(), "solo")
		if root.RootRunID != "solo" || root.ParentRunID != "" || root.Depth != 0 {
			t.Fatalf("root info = %+v, want the run to root its own tree", root)
		}
	})

	t.Run("the root Done cost equals the summed spend", func(t *testing.T) {
		hub := chains.NewLimitsState()
		limits := treeHubLimits()
		limits.MaxCost = 0
		ctx := types.WithRunInfo(context.Background(), types.RunInfo{RunID: "hub", RootRunID: "hub"})
		child := treeChild{st: hub.Branch(), limits: limits}
		if _, err := child.Invoke(ctx, struct{}{}); err != nil {
			t.Fatalf("first spend: %v", err)
		}
		if _, err := child.Invoke(ctx, struct{}{}); err != nil {
			t.Fatalf("second spend: %v", err)
		}
		var done types.Done
		for ev, err := range DriveTree(ctx, doneRuntime{}, runtime.AgentRun{}, hub) {
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			if d, ok := ev.(types.Done); ok {
				done = d
			}
		}
		if done.Cost != 0.08 {
			t.Fatalf("Done.Cost = %v, want the summed spend 0.08", done.Cost)
		}
	})
}
