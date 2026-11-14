package flow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type costRecorder struct {
	*stores.MemoryRuns

	mu      sync.Mutex
	entries []costEntry
}

type costEntry struct {
	runID string
	cost  float64
}

var _ stores.RunCostRecorder = (*costRecorder)(nil)

func (c *costRecorder) RecordCost(ctx context.Context, l stores.Lease, cost float64) error {
	if err := c.MemoryRuns.RecordCost(ctx, l, cost); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, costEntry{runID: l.RunID, cost: cost})
	return nil
}

func hasCost(costs map[float64]bool, want float64) bool {
	for c := range costs {
		if c > want-1e-9 && c < want+1e-9 {
			return true
		}
	}
	return false
}

func TestSubflowsFanoutCost(t *testing.T) {
	t.Run("subflows.fanout-cost-attribution", func(t *testing.T) {
		memRuns := stores.NewMemoryRuns()
		rec := &costRecorder{MemoryRuns: memRuns}

		limits := types.AgenticLimits
		limits.MaxCost = 1.0
		limits.MaxDepth = 3

		stack, err := gohan.Build(
			gohan.WithStores(stores.Stores{Runs: rec}),
			gohan.WithLimits("parent", limits),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}

		type childIn struct {
			Value string `json:"value"`
		}
		child := gohan.FlowFunc[childIn, string]("child", func(ctx context.Context, in childIn) (string, error) {
			st, ok := chains.LimitsStateFrom(ctx)
			if !ok || st == nil {
				return "", errors.New("no ledger in child context")
			}
			tokens := map[string]int{"a": 2, "b": 5}[in.Value]
			st.Charge(types.Usage{InputTokens: tokens}, types.Pricing{Input: 0.01})
			return in.Value, nil
		})

		tool, err := stack.FlowAsTool("child_tool", child,
			gohan.WithDescription("runs the child sub-flow"),
			gohan.WithChildLimits(types.ChildLimits{}),
		)
		if err != nil {
			t.Fatalf("flow as tool: %v", err)
		}

		waveChild := gohan.FlowFunc[string, string]("wave", func(ctx context.Context, in string) (string, error) {
			args, err := json.Marshal(childIn{Value: in})
			if err != nil {
				return "", err
			}
			res, err := tool.Call(ctx, args)
			if err != nil {
				return "", err
			}
			var out strings.Builder
			for _, b := range res.Content {
				if txt, ok := b.(types.Text); ok {
					out.WriteString(txt.Text)
				}
			}
			return out.String(), nil
		})

		wave := MapReduce(waveChild, func(mids []string) (string, error) {
			return strings.Join(mids, ""), nil
		}, WithParallelism(3))

		ctx := types.WithRunInfo(context.Background(), types.RunInfo{
			RunID:     "run-parent",
			RootRunID: "run-parent",
			SessionID: "s1",
			Flow:      "parent",
		})
		st := chains.NewLimitsState()
		ctx = chains.WithLimitsState(ctx, st)
		ctx = types.WithPrincipal(ctx, types.Principal{Subject: "u1"})

		got, err := wave.Invoke(ctx, []string{"a", "b", "c"})
		if err != nil {
			t.Fatalf("wave invoke: %v", err)
		}
		if got != "abc" {
			t.Fatalf("wave output = %q, want %q", got, "abc")
		}

		if tc := st.TreeCost(); tc != 0.07 {
			t.Fatalf("tree cost = %v, want 0.07", tc)
		}

		rec.mu.Lock()
		defer rec.mu.Unlock()
		if len(rec.entries) != 2 {
			t.Fatalf("recorded %d cost entries, want 2: %v", len(rec.entries), rec.entries)
		}
		costs := map[float64]bool{}
		ids := map[string]bool{}
		for _, e := range rec.entries {
			costs[e.cost] = true
			if e.runID == "" {
				t.Fatalf("recorded empty run id for cost %v", e.cost)
			}
			if ids[e.runID] {
				t.Fatalf("duplicate run id %q", e.runID)
			}
			ids[e.runID] = true
		}
		if !hasCost(costs, 0.02) || !hasCost(costs, 0.05) {
			t.Fatalf("recorded costs %v, want 0.02 and 0.05", rec.entries)
		}
	})
}
