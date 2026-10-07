package eino

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func waitFor(d time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
}

type waitTool struct {
	wait  func(context.Context) error
	done  atomic.Int64
	calls atomic.Int64
}

func (t *waitTool) Spec() types.ToolSpec { return types.ToolSpec{Name: "echo"} }

func (t *waitTool) Call(ctx context.Context, _ json.RawMessage) (types.ToolResult, error) {
	t.calls.Add(1)
	if err := t.wait(ctx); err != nil {
		return types.ToolResult{}, err
	}
	t.done.Add(1)
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

// mwModel applies one model middleware around the scripted model, so the
// governed limit step runs inside the eino turn exactly as it does on the
// native flow.
type mwModel struct {
	inner types.Model
	mw    types.ModelMiddleware
	calls int
}

func (m *mwModel) Profile() types.ModelProfile { return m.inner.Profile() }

func (m *mwModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.calls++
	return m.mw(m.inner.Generate)(ctx, req)
}

// mwTool applies one tool middleware around the double, so the governed
// tool limit step runs inside the eino tool batch.
type mwTool struct {
	inner types.Tool
	mw    types.ToolMiddleware
}

func (t *mwTool) Spec() types.ToolSpec { return t.inner.Spec() }

func (t *mwTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	return t.mw(func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
		return t.inner.Call(ctx, call.Args)
	})(ctx, types.ToolUse{Args: args})
}

// limitsRun drives the shipped eino runtime over the given governed
// components, mirroring the harness loop, and reports the events.
func limitsRun(t *testing.T, model types.Model, tools []types.Tool) ([]types.Event, error) {
	t.Helper()
	rt, err := New(context.Background(), &adk.ChatModelAgent{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ag := runtime.AgentRun{
		Model: model,
		Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Messages: in.History}, nil
		},
		Tools: tools,
	}
	if _, err := rt.Start(context.Background(), ag); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var evs []types.Event
	var st runtime.State
	for {
		var status runtime.Status
		st, evs, status, err = rt.Step(context.Background(), st)
		if err != nil {
			return evs, err
		}
		if status == runtime.DoneStatus {
			return evs, nil
		}
	}
}

func doneOf(t *testing.T, evs []types.Event) types.Done {
	t.Helper()
	var done types.Done
	for _, ev := range evs {
		if d, ok := ev.(types.Done); ok {
			done = d
		}
	}
	return done
}

func TestLimits(t *testing.T) {
	t.Run("limits.limits-under-foreign-backend", func(t *testing.T) {
		profile := types.ModelProfile{Name: "conformance", Version: "v1"}

		t.Run("hard cost abort", func(t *testing.T) {
			st := chains.NewLimitsState()
			spend := gohantest.NewScriptedModel(profile,
				gohantest.ToolCall("echo", map[string]string{"in": "1"}),
				gohantest.Text("expensive").WithUsage(types.Usage{InputTokens: 2}),
			)
			m := &mwModel{inner: spend, mw: chains.Limits(
				types.RunLimits{MaxTurns: 10, MaxToolCalls: 10, MaxCost: 0.05, MaxWallClock: time.Minute, SoftRatio: 0.8},
				types.Pricing{Input: 0.04}, st,
			)}
			evs, err := limitsRun(t, m, []types.Tool{gohantest.NewFakeTool("echo")})
			var over *types.LimitExceededError
			if !errors.As(err, &over) || over.Limit != "MaxCost" {
				t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxCost}, events %v", err, evs)
			}
			if c := st.Cost(); c <= 0.05 {
				t.Fatalf("cost = %v, want the overrun charged before the stop", c)
			}
		})

		t.Run("wall clock", func(t *testing.T) {
			st := chains.NewLimitsState()
			tool := &mwTool{inner: &waitTool{wait: waitFor(50 * time.Millisecond)}, mw: chains.ToolLimits(
				types.RunLimits{MaxTurns: 10, MaxToolCalls: 10, MaxWallClock: 10 * time.Millisecond},
				st,
			)}
			model := gohantest.NewScriptedModel(profile, gohantest.ToolCall("echo", map[string]string{"in": "1"}))
			evs, err := limitsRun(t, model, []types.Tool{tool})
			var over *types.LimitExceededError
			if !errors.As(err, &over) || over.Limit != "MaxWallClock" {
				t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxWallClock}, events %v", err, evs)
			}
		})

		t.Run("provider call count", func(t *testing.T) {
			st := chains.NewLimitsState()
			turns := make([]gohantest.Turn, 0, 5)
			for range 5 {
				turns = append(turns, gohantest.ToolCall("echo", map[string]string{"in": "1"}))
			}
			m := &mwModel{
				inner: gohantest.NewScriptedModel(profile, turns...),
				mw: chains.Limits(
					types.RunLimits{MaxTurns: 3, MaxToolCalls: 10, MaxWallClock: time.Minute},
					types.Pricing{}, st,
				),
			}
			evs, _ := limitsRun(t, m, []types.Tool{gohantest.NewFakeTool("echo")})
			if done := doneOf(t, evs); done.Reason != types.StopLimit {
				t.Fatalf("done = %+v, want Done{StopLimit}", done)
			}
			if m.calls != 4 {
				// The ledger refuses the fourth call before it streams,
				// so the middleware sees one attempt past the bound.
				t.Fatalf("provider calls = %d, want 3 executed plus the refused one", m.calls)
			}
		})

		t.Run("wall clock monotonic", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				st := chains.NewLimitsState()
				tool := &mwTool{inner: &waitTool{wait: waitFor(time.Hour)}, mw: chains.ToolLimits(
					types.RunLimits{MaxTurns: 10, MaxToolCalls: 10, MaxWallClock: time.Second},
					st,
				)}
				model := gohantest.NewScriptedModel(profile, gohantest.ToolCall("echo", map[string]string{"in": "1"}))
				evs, err := limitsRun(t, model, []types.Tool{tool})
				var over *types.LimitExceededError
				if !errors.As(err, &over) || over.Limit != "MaxWallClock" {
					t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxWallClock}, events %v", err, evs)
				}
			})
		})
	})
}
