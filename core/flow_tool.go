package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	jsonv2 "encoding/json/v2"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// subflowConfig carries the options FlowAsTool applies to one sub-flow
// tool: the description, the parent limits override, the child call
// bounds and the derived and declared effects.
type subflowConfig struct {
	name        string
	desc        string
	parent      *types.RunLimits
	child       types.ChildLimits
	childEffect types.Effect
}

// SubflowOption adjusts one FlowAsTool construction.
type SubflowOption func(*subflowConfig) error

// WithChildLimits bounds one sub-flow call: the declared wall clock, the
// cost share and the fixed share the child applies for.
func WithChildLimits(l types.ChildLimits) SubflowOption {
	return func(c *subflowConfig) error {
		c.child = l
		return nil
	}
}

// WithParentLimits overrides the parent run limits the call boundary
// would otherwise resolve from the calling flow's registered limits.
func WithParentLimits(l types.RunLimits) SubflowOption {
	return func(c *subflowConfig) error {
		c.parent = &l
		return nil
	}
}

// WithChildEffect sets the effect the child flow derives. It defaults to
// ReadOnly.
func WithChildEffect(e types.Effect) SubflowOption {
	return func(c *subflowConfig) error {
		c.childEffect = e
		return nil
	}
}

// WithDescription sets the model-facing description. An empty one is a
// construction error.
func WithDescription(desc string) SubflowOption {
	return func(c *subflowConfig) error {
		c.desc = desc
		return nil
	}
}

// FlowAsTool exposes a child flow as its parent's tool. The typed input
// is the only value that crosses into the child; the call boundary
// derives the child's budget from the parent's remaining budget, runs
// the child under its own tree identity and ledger branch, and charges
// the spend to the parent's tree.
func (s *Stack) FlowAsTool[In, Out any](name string, f Flow[In, Out], opts ...SubflowOption) (types.Tool, error) {
	cfg := subflowConfig{name: name, childEffect: types.ReadOnly}
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, fmt.Errorf("gohan: subflow tool %s: %w", name, err)
		}
	}
	if cfg.desc == "" {
		return nil, fmt.Errorf("gohan: subflow tool %s requires a description", name)
	}
	inner, err := NewTool[In, Out](name, cfg.desc, func(ctx context.Context, in In) (Out, error) {
		return runSubflow(ctx, s, f, cfg, in)
	}, WithEffect(cfg.childEffect))
	if err != nil {
		return nil, err
	}
	return flowSubtool[In, Out]{tool: inner, f: f, cfg: cfg, stack: s}, nil
}

// flowSubtool delegates Spec and Call to the inner tool and keeps the
// concrete child flow, the config and the stack, so a later slice can
// add a resume path.
type flowSubtool[In, Out any] struct {
	tool  *tool[In, Out]
	f     Flow[In, Out]
	cfg   subflowConfig
	stack *Stack
}

func (t flowSubtool[In, Out]) Spec() types.ToolSpec { return t.tool.Spec() }

func (t flowSubtool[In, Out]) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	return t.tool.Call(ctx, args)
}

// ResumeChild resumes the wrapped flow at a suspension token the child
// minted and returns the typed result as bytes. An *types.SuspendError and
// types.ErrNotSuspendable cross unchanged: the caller decides between
// re-suspending and refusing.
func (t flowSubtool[In, Out]) ResumeChild(ctx context.Context, token types.ResumeToken, r stores.ResumeInput) (json.RawMessage, error) {
	out, err := t.f.Resume(ctx, token, r)
	if err != nil {
		return nil, err
	}
	b, err := jsonv2.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("gohan: subflow tool %s: encode resume result: %w", t.cfg.name, err)
	}
	return b, nil
}

// subflowParentLimits resolves the limits the call boundary derives the
// child budget from: the explicit override wins, then the calling flow's
// registered limits. A call with neither refuses before the child starts.
func subflowParentLimits(s *Stack, ctx context.Context, cfg subflowConfig) (types.RunLimits, types.RunInfo, error) {
	info, _ := types.RunInfoFrom(ctx)
	if cfg.parent != nil {
		return *cfg.parent, info, nil
	}
	if info.Flow == "" {
		return types.RunLimits{}, info, errors.New(
			"gohan: subflow call outside a run: no limits are configured for the calling flow",
		)
	}
	parent, ok := s.limits[info.Flow]
	if !ok {
		return types.RunLimits{}, info, fmt.Errorf(
			"gohan: flow %s has no limits configured for a subflow call", info.Flow,
		)
	}
	return parent, info, nil
}

// runSubflow is the call boundary: depth, wall clock and cost derive
// here, the child runs under its own identity and ledger branch, and its
// spend charges the parent's tree budget.
func runSubflow[In, Out any](ctx context.Context, s *Stack, f Flow[In, Out], cfg subflowConfig, in In) (Out, error) {
	var zero Out
	parent, info, err := subflowParentLimits(s, ctx, cfg)
	if err != nil {
		return zero, err
	}
	ledger, ok := chains.LimitsStateFrom(ctx)
	if !ok || ledger == nil {
		return zero, errors.New("gohan: subflow call outside a run: no ledger in context")
	}
	if info.Depth+1 > parent.MaxDepth {
		return zero, &types.LimitExceededError{Limit: "MaxDepth", Value: float64(info.Depth + 1)}
	}
	if cfg.child.MaxWallClock > parent.MaxWallClock {
		return zero, fmt.Errorf(
			"gohan: subflow tool %s declares wall clock %s above the calling flow %s limit %s",
			cfg.name, cfg.child.MaxWallClock, info.Flow, parent.MaxWallClock,
		)
	}
	remaining, err := ledger.WallClockLeft(parent, time.Now())
	if err != nil {
		return zero, err
	}
	wall := cfg.child.MaxWallClock
	if wall == 0 {
		wall = remaining
	}
	wall = min(wall, remaining)

	cost := subflowCost(parent, cfg.child, ledger.TreeCost())

	childID, err := newRunID()
	if err != nil {
		return zero, err
	}
	childInfo := ChildRunInfo(ctx, childID)
	if info.SessionID != "" {
		childInfo.SessionID = info.SessionID + "/" + childID
	}
	start := time.Now()
	childCtx := chains.WithLimitsState(
		types.WithRunInfo(types.WithoutRunIdentity(ctx), childInfo),
		ledger.BranchInherited(wall, start),
	)
	childCtx = types.WithRunBudget(childCtx, types.RunBudget{
		WallClock: wall,
		Cost:      cost,
		Depth:     childInfo.Depth,
	})

	var lease stores.Lease
	var stopHeartbeat func()
	if s.stores.Runs != nil {
		lease, err = s.stores.Runs.Start(ctx, RunRow(childInfo), stores.LeaseTTL)
		if err != nil {
			return zero, fmt.Errorf("start subflow run %s: %w", childID, err)
		}
		stopHeartbeat = startLeaseHeartbeat(ctx, s.stores.Runs, lease, &lease)
	}
	before := ledger.TreeCost()
	out, callErr := f.Invoke(childCtx, in)
	spent := ledger.TreeCost() - before
	var storeErr error
	if stopHeartbeat != nil {
		stopHeartbeat()
	}
	if s.stores.Runs != nil {
		if rec, ok := s.stores.Runs.(stores.RunCostRecorder); ok && spent > 0 {
			if recErr := rec.RecordCost(ctx, lease, spent); recErr != nil {
				storeErr = errors.Join(storeErr, fmt.Errorf("record subflow cost: %w", recErr))
			}
		}
		if susp, ok := errors.AsType[*types.SuspendError](callErr); ok && susp.Token != "" {
			if suspErr := s.stores.Runs.Suspend(ctx, lease, susp.Token); suspErr != nil {
				storeErr = errors.Join(storeErr, fmt.Errorf("suspend subflow run: %w", suspErr))
			}
		} else {
			state := stores.Finished
			if callErr != nil {
				state = stores.Failed
			}
			if finErr := s.stores.Runs.Finish(ctx, lease, state, nil, ""); finErr != nil {
				storeErr = errors.Join(storeErr, fmt.Errorf("finish subflow run: %w", finErr))
			}
		}
	}
	if _, isSusp := errors.AsType[*types.SuspendError](callErr); isSusp {
		return zero, callErr
	}
	if callErr != nil {
		return zero, errors.Join(callErr, storeErr)
	}
	if parent.MaxCost > 0 && spent > cost {
		return zero, &types.LimitExceededError{Limit: "MaxCost", Value: spent}
	}
	if elapsed := time.Since(start); wall > 0 && elapsed > wall {
		return zero, &types.LimitExceededError{Limit: "MaxWallClock", Value: elapsed.Seconds()}
	}
	if storeErr != nil {
		return zero, storeErr
	}
	return out, nil
}

// startLeaseHeartbeat keeps the child run's lease alive while the child
// call runs, refreshing the caller's lease with each renewal. The
// returned stop closes the loop and joins the goroutine.
func startLeaseHeartbeat(ctx context.Context, runs stores.Runs, l stores.Lease, cur *stores.Lease) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(stores.HeartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if next, hbErr := runs.Heartbeat(ctx, l); hbErr == nil {
					*cur = next
					l = next
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

// subflowCost derives the child's cost budget: the cost share, clamped
// to the tree budget's remainder when the tree MaxCost bounds it. A
// negative result clamps to zero.
func subflowCost(parent types.RunLimits, child types.ChildLimits, treeCost float64) float64 {
	if parent.MaxCost <= 0 {
		if child.CostShare == types.ShareFixed {
			return max(child.FixedCost, 0)
		}
		return 0
	}
	treeRemaining := max(parent.MaxCost-treeCost, 0)
	share := treeRemaining
	if child.CostShare == types.ShareFixed {
		share = child.FixedCost
	}
	return max(min(share, treeRemaining), 0)
}
