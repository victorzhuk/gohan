package types

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fixtureTool is a tool value with no per-request state: the principal comes
// from ctx and everything else from the call arguments.
type fixtureTool struct {
	spec ToolSpec
}

func (t fixtureTool) Spec() ToolSpec { return t.spec }

type fixtureKey struct{}

func (t fixtureTool) Call(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	tenant, _ := ctx.Value(fixtureKey{}).(string)
	return ToolResult{
		ID:      "call-1",
		Content: []Block{Text{Text: fmt.Sprintf("%s:%s", tenant, args)}},
		Outcome: Succeeded,
	}, nil
}

func TestToolConcurrentValue(t *testing.T) {
	t.Run("tools.tool-value-shared-and-concurrent", func(t *testing.T) {
		spec := ToolSpec{
			Name:    "lookup",
			Effect:  ReadOnly,
			Timeout: 10 * time.Second,
			Risk:    RiskLow,
		}
		tool := fixtureTool{spec: spec}

		const runs = 64
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, runs)
		for i := range runs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tenant := fmt.Sprintf("tenant-%d", i)
				ctx := context.WithValue(context.Background(), fixtureKey{}, tenant)
				args := json.RawMessage(`{"tenant":"` + tenant + `"}`)
				<-start

				res, err := tool.Call(ctx, args)
				if err != nil {
					errs <- err
					return
				}
				if res.Outcome != Succeeded {
					errs <- fmt.Errorf("tenant %s: outcome %d", tenant, res.Outcome)
					return
				}
				want := Text{Text: tenant + ":" + string(args)}
				if len(res.Content) != 1 || res.Content[0] != Block(want) {
					errs <- fmt.Errorf("tenant %s: observed foreign request or result: %v", tenant, res.Content)
				}
				if tool.Spec().Name != "lookup" {
					errs <- fmt.Errorf("spec name changed to %q", tool.Spec().Name)
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	})
}
