package flow

import (
	"context"
	"strconv"
	"sync"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// defaultParallelism is the per-wave cap a fan-out applies when the caller
// sets none.
const defaultParallelism = 8

// FanOption configures a fan-out wave's failure policy and concurrency cap.
type FanOption func(*fanConfig)

// fanConfig holds the wave's failure policy and per-wave child cap.
type fanConfig struct {
	onError     types.OnError
	parallelism int
}

func newFanConfig(opts []FanOption) fanConfig {
	c := fanConfig{parallelism: defaultParallelism}
	for _, opt := range opts {
		opt(&c)
	}
	if c.parallelism <= 0 {
		c.parallelism = defaultParallelism
	}
	return c
}

// WithOnError selects the wave's failure policy; FailFast is the default.
func WithOnError(e types.OnError) FanOption {
	return func(c *fanConfig) { c.onError = e }
}

// WithParallelism caps the children running concurrently per wave; queued
// children start as running ones finish. Values n <= 0 are ignored and keep
// the default of 8.
func WithParallelism(n int) FanOption {
	return func(c *fanConfig) {
		if n > 0 {
			c.parallelism = n
		}
	}
}

// Fork returns a Flow over input slices that invokes child once per input.
// Results land at their input positions regardless of completion order; the
// wave shares one concurrency cap across every concurrent invocation of the
// returned flow.
func Fork[In, Out any](child gohan.Flow[In, Out], opts ...FanOption) gohan.Flow[[]In, []Out] {
	c := newFanConfig(opts)
	sem := make(chan struct{}, c.parallelism)
	return fanFlow[[]In, []Out]{
		fn: func(ctx context.Context, in []In) ([]Out, error) {
			return runFan(ctx, sem, c.onError, in, func(ctx context.Context, _ int, v In) (Out, error) {
				return child.Invoke(ctx, v)
			})
		},
	}
}

// runFan invokes child for every input under the wave's semaphore. Under
// FailFast the first error cancels the siblings and is returned; under
// Collect every child settles, failed positions hold zero values, and the
// failures come back as a *types.PartialError keyed by input position. A
// cancelled wave ctx cancels the children and returns the ctx error, and the
// caller's return waits for every started child.
func runFan[In, Mid any](ctx context.Context, sem chan struct{}, onError types.OnError, in []In, child func(context.Context, int, In) (Mid, error)) ([]Mid, error) {
	results := make([]Mid, len(in))
	errs := make([]error, len(in))
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
launch:
	for i := range in {
		select {
		case sem <- struct{}{}:
		case <-runCtx.Done():
			break launch
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i], errs[i] = child(runCtx, i, in[i])
			if errs[i] != nil && onError == types.FailFast {
				once.Do(func() { firstErr = errs[i]; cancel() })
			}
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if onError == types.Collect {
		var failed []error
		for i, err := range errs {
			if err != nil {
				failed = append(failed, &types.StepError{Step: strconv.Itoa(i), Err: err})
			}
		}
		if len(failed) > 0 {
			seen := make([]any, len(results))
			for i, r := range results {
				seen[i] = r
			}
			return nil, &types.PartialError{Results: seen, Errors: failed}
		}
	}
	return results, nil
}

// fanFlow adapts a plain function to the Flow contract without the runtime
// stepper; a plain function has no checkpoint to resume from, so Resume
// refuses rather than re-executing the wave.
type fanFlow[In, Out any] struct {
	fn func(context.Context, In) (Out, error)
}

func (f fanFlow[In, Out]) Invoke(ctx context.Context, in In) (Out, error) {
	return f.fn(ctx, in)
}

func (fanFlow[In, Out]) Resume(context.Context, types.ResumeToken, stores.ResumeInput) (Out, error) {
	var zero Out
	return zero, types.ErrNotSuspendable
}
