package runtime

import (
	"context"
	"sync"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// ToolFunc executes one governed tool call. The cancel shield and the
// journal reach the scheduler as values of this shape; a leaf cannot import
// the packages that build them.
type ToolFunc func(ctx context.Context, call types.ToolUse) (types.ToolResult, error)

// ToolMiddleware wraps a ToolFunc, the way the shield middleware does.
type ToolMiddleware func(next ToolFunc) ToolFunc

// SchedulerConfig carries the batch's wiring: the per-call effect lookup,
// the journal and its key and fingerprint derivations, and the resolved
// strategy - Parallel is the profile's ParallelTools capability combined
// with the sequential hint, MaxParallel the resolved cap.
type SchedulerConfig struct {
	EffectOf    func(types.ToolUse) types.Effect
	Shield      ToolMiddleware
	Journal     stores.Journal
	Key         func(types.ToolUse) types.CallKey
	Fingerprint func(types.ToolUse) stores.Fingerprint
	Parallel    bool
	MaxParallel int
}

// Schedule executes one batch of tool calls. ReadOnly calls run
// concurrently, at most MaxParallel at a time, when the resolved strategy
// allows parallel tools; every other effect runs sequentially in call
// order after the read-only group, each journaled reserve → call →
// complete. One call's failure never cancels another: every call produces
// a result, and the first execution error is returned after the batch.
// Results come back in call order, never completion order.
func Schedule(ctx context.Context, calls []types.ToolUse, exec ToolFunc, cfg SchedulerConfig) ([]types.ToolResult, error) {
	if cfg.Shield != nil {
		exec = cfg.Shield(exec)
	}
	results := make([]types.ToolResult, len(calls))
	if !cfg.Parallel {
		var err error
		for i, call := range calls {
			results[i], err = execOne(ctx, call, exec, cfg)
			if err != nil {
				return results, err
			}
		}
		return results, nil
	}
	parallel := make([]int, 0, len(calls))
	sequential := make([]int, 0, len(calls))
	for i, call := range calls {
		if cfg.EffectOf(call) == types.ReadOnly {
			parallel = append(parallel, i)
			continue
		}
		sequential = append(sequential, i)
	}
	max := cfg.MaxParallel
	if max < 1 {
		max = 1
	}
	sem := make(chan struct{}, max)
	var wg sync.WaitGroup
	var once sync.Once
	var batchErr error
	for _, i := range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := execOne(ctx, calls[i], exec, cfg)
			results[i] = res
			if err != nil {
				once.Do(func() { batchErr = err })
			}
		}()
	}
	wg.Wait()
	for _, i := range sequential {
		res, err := execOne(ctx, calls[i], exec, cfg)
		results[i] = res
		if err != nil && batchErr == nil {
			batchErr = err
		}
	}
	return results, batchErr
}

// execOne runs one call, replaying a recorded journal entry instead of
// executing when the journal already holds one for the call's key.
func execOne(ctx context.Context, call types.ToolUse, exec ToolFunc, cfg SchedulerConfig) (types.ToolResult, error) {
	effect := types.ReadOnly
	if cfg.EffectOf != nil {
		effect = cfg.EffectOf(call)
	}
	if effect == types.ReadOnly || cfg.Journal == nil || cfg.Key == nil || cfg.Fingerprint == nil {
		return exec(ctx, call)
	}
	key := cfg.Key(call)
	entry, replayed, err := cfg.Journal.Reserve(ctx, key, cfg.Fingerprint(call))
	if err != nil {
		return types.ToolResult{}, err
	}
	if replayed {
		return entry.Result, nil
	}
	res, err := exec(ctx, call)
	if cerr := cfg.Journal.Complete(ctx, key, res); err == nil {
		err = cerr
	}
	return res, err
}
