package flow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type fanCounter struct {
	mu  sync.Mutex
	cur int
	max int
}

func (c *fanCounter) enter() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur++
	if c.cur > c.max {
		c.max = c.cur
	}
}

func (c *fanCounter) exit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur--
}

func (c *fanCounter) peak() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max
}

func (c *fanCounter) running() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

// invokeAsync runs one fan-out wave on another goroutine and returns the
// result channel.
func invokeAsync[In, Out any](t *testing.T, f gohan.Flow[In, Out], ctx context.Context, in In) <-chan fanResult[Out] {
	t.Helper()
	ch := make(chan fanResult[Out], 1)
	go func() {
		out, err := f.Invoke(ctx, in)
		ch <- fanResult[Out]{out: out, err: err}
	}()
	return ch
}

type fanResult[Out any] struct {
	out Out
	err error
}

// waitStarted reads n child start reports or fails the test on timeout.
func waitStarted(t *testing.T, started <-chan string, n int) []string {
	t.Helper()
	var got []string
	deadline := time.After(100 * time.Millisecond)
	for len(got) < n {
		select {
		case v := <-started:
			got = append(got, v)
		case <-deadline:
			t.Fatalf("only %d of %d children started", len(got), n)
		}
	}
	return got
}

func TestSubflowsFanout(t *testing.T) {
	ctx := context.Background()

	t.Run("subflows.fanout-results-keyed", func(t *testing.T) {
		gates := map[string]chan struct{}{
			"a": make(chan struct{}),
			"b": make(chan struct{}),
			"c": make(chan struct{}),
		}
		var gotOrder []string
		child := fanFlow[string, *string]{fn: func(_ context.Context, in string) (*string, error) {
			<-gates[in]
			return new(strings.ToUpper(in)), nil
		}}
		mr := MapReduce(child, func(mids []*string) (string, error) {
			for _, m := range mids {
				gotOrder = append(gotOrder, *m)
			}
			return strings.Join(gotOrder, "|"), nil
		})
		res := invokeAsync(t, mr, ctx, []string{"a", "b", "c"})
		go func() {
			close(gates["c"])
			close(gates["a"])
			close(gates["b"])
		}()
		r := <-res
		if r.err != nil {
			t.Fatalf("invoke: %v", r.err)
		}
		if r.out != "A|B|C" {
			t.Fatalf("reduced output = %q, want %q", r.out, "A|B|C")
		}
		if len(gotOrder) != 3 || gotOrder[0] != "A" || gotOrder[1] != "B" || gotOrder[2] != "C" {
			t.Fatalf("reduce saw %v, want input order [A B C]", gotOrder)
		}

		// Fork leg: same completion order, results keyed by input position.
		fgates := map[string]chan struct{}{
			"a": make(chan struct{}),
			"b": make(chan struct{}),
			"c": make(chan struct{}),
		}
		childF := fanFlow[string, string]{fn: func(_ context.Context, in string) (string, error) {
			<-fgates[in]
			return strings.ToUpper(in), nil
		}}
		f := Fork(childF)
		resF := invokeAsync(t, f, ctx, []string{"a", "b", "c"})
		go func() {
			close(fgates["c"])
			close(fgates["a"])
			close(fgates["b"])
		}()
		rF := <-resF
		if rF.err != nil {
			t.Fatalf("fork invoke: %v", rF.err)
		}
		want := []string{"A", "B", "C"}
		if len(rF.out) != 3 || rF.out[0] != want[0] || rF.out[1] != want[1] || rF.out[2] != want[2] {
			t.Fatalf("fork results = %v, want %v", rF.out, want)
		}
	})

	t.Run("subflows.fanout-partial-positions", func(t *testing.T) {
		child := fanFlow[string, *string]{fn: func(_ context.Context, in string) (*string, error) {
			if in == "v1" || in == "v3" {
				return nil, fmt.Errorf("boom %s", in)
			}
			return new(strings.ToUpper(in)), nil
		}}
		mr := MapReduce(child, func([]*string) (string, error) { return "", nil }, WithOnError(types.Collect))
		_, err := mr.Invoke(ctx, []string{"v0", "v1", "v2", "v3", "v4"})
		var partial *types.PartialError
		if !errors.As(err, &partial) {
			t.Fatalf("err = %v, want *types.PartialError", err)
		}
		if len(partial.Errors) != 2 {
			t.Fatalf("errors = %d, want 2", len(partial.Errors))
		}
		wantSteps := []string{"1", "3"}
		for i, e := range partial.Errors {
			step, ok := e.(*types.StepError)
			if !ok {
				t.Fatalf("error %d is %T, want *types.StepError", i, e)
			}
			if step.Step != wantSteps[i] {
				t.Fatalf("error %d step = %q, want %q", i, step.Step, wantSteps[i])
			}
		}
		if len(partial.Results) != 5 {
			t.Fatalf("results len = %d, want 5", len(partial.Results))
		}
		for _, pos := range []int{1, 3} {
			if partial.Results[pos] != nil {
				t.Fatalf("result at %d = %v, want nil", pos, partial.Results[pos])
			}
		}
		for pos, want := range map[int]string{0: "V0", 2: "V2", 4: "V4"} {
			got := *(partial.Results[pos].(*string))
			if got != want {
				t.Fatalf("result at %d = %q, want %q", pos, got, want)
			}
		}

		// Fork leg: same failures under Collect on the []Out surface.
		childF := fanFlow[string, string]{fn: func(_ context.Context, in string) (string, error) {
			if in == "v1" || in == "v3" {
				return "", fmt.Errorf("boom %s", in)
			}
			return strings.ToUpper(in), nil
		}}
		f := Fork(childF, WithOnError(types.Collect))
		_, errF := f.Invoke(ctx, []string{"v0", "v1", "v2", "v3", "v4"})
		var partialF *types.PartialError
		if !errors.As(errF, &partialF) {
			t.Fatalf("fork err = %v, want *types.PartialError", errF)
		}
		if len(partialF.Errors) != 2 {
			t.Fatalf("fork errors = %d, want 2", len(partialF.Errors))
		}
		for i, wantStep := range []string{"1", "3"} {
			step := partialF.Errors[i].(*types.StepError)
			if step.Step != wantStep {
				t.Fatalf("fork error %d step = %q, want %q", i, step.Step, wantStep)
			}
		}
		if len(partialF.Results) != 5 {
			t.Fatalf("fork results len = %d, want 5", len(partialF.Results))
		}
		for _, pos := range []int{1, 3} {
			if partialF.Results[pos] != nil {
				t.Fatalf("fork result at %d = %v, want nil", pos, partialF.Results[pos])
			}
		}
		for pos, want := range map[int]string{0: "V0", 2: "V2", 4: "V4"} {
			if got := partialF.Results[pos].(string); got != want {
				t.Fatalf("fork result at %d = %q, want %q", pos, got, want)
			}
		}
	})

	t.Run("subflows.fanout-per-wave-cap", func(t *testing.T) {
		const cap = 8
		var counter fanCounter
		var mu sync.Mutex
		var startOrder []string
		wave1 := []string{"w1-1", "w1-2", "w1-3", "w1-4", "w1-5", "w1-6", "w1-7", "w1-8"}
		wave2 := []string{"w2-1", "w2-2", "w2-3", "w2-4", "w2-5", "w2-6"}
		gates := make(map[string]chan struct{}, cap+len(wave2))
		for _, v := range wave1 {
			gates[v] = make(chan struct{})
		}
		for _, v := range wave2 {
			gates[v] = make(chan struct{})
		}
		started := make(chan string, cap+len(wave2))
		child := fanFlow[string, *string]{fn: func(_ context.Context, in string) (*string, error) {
			counter.enter()
			defer counter.exit()
			mu.Lock()
			startOrder = append(startOrder, in)
			mu.Unlock()
			started <- in
			<-gates[in]
			return new(in), nil
		}}
		f := Fork(child, WithParallelism(cap))
		res1 := invokeAsync(t, f, ctx, wave1)
		// Fill all 8 slots from wave 1 before arming wave 2 so the
		// goroutine race between the two waves cannot reorder the cap.
		waitStarted(t, started, cap)
		res2 := invokeAsync(t, f, ctx, wave2)

		// Demand is 14 children against a cap of 8: no ninth child may
		// start while wave 1 still holds every slot.
		select {
		case v := <-started:
			t.Fatalf("ninth child %q started before any wave-1 child finished", v)
		case <-time.After(100 * time.Millisecond):
		}
		if running := counter.running(); running != cap {
			t.Fatalf("running children = %d, want %d", running, cap)
		}

		// Free two slots; exactly two wave-2 children may start.
		close(gates["w1-1"])
		close(gates["w1-2"])
		waitStarted(t, started, 2)
		select {
		case v := <-started:
			t.Fatalf("11th child %q started after releasing only 2 slots", v)
		case <-time.After(100 * time.Millisecond):
		}
		if running := counter.running(); running != cap {
			t.Fatalf("running children after first release = %d, want %d", running, cap)
		}

		// Free the rest; the remaining 4 wave-2 children fill the freed slots.
		for i := 3; i <= cap; i++ {
			close(gates[fmt.Sprintf("w1-%d", i)])
		}
		waitStarted(t, started, len(wave2)-2)
		for _, v := range wave2 {
			close(gates[v])
		}

		for _, res := range []<-chan fanResult[[]*string]{res1, res2} {
			select {
			case r := <-res:
				if r.err != nil {
					t.Fatalf("wave invoke: %v", r.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("wave did not finish; a slot leaked")
			}
		}
		if peak := counter.peak(); peak != cap {
			t.Fatalf("peak concurrent children = %d, want exactly %d", peak, cap)
		}
		mu.Lock()
		defer mu.Unlock()
		for i, v := range startOrder {
			want := "w1-"
			if i >= cap {
				want = "w2-"
			}
			if !strings.HasPrefix(v, want) {
				t.Fatalf("start order[%d] = %q, want a %q* child", i, v, want)
			}
		}
	})

	t.Run("subflows.max-parallel-children", func(t *testing.T) {
		var counter fanCounter
		barrier := make(chan struct{})
		started := make(chan string, 20)
		child := fanFlow[string, *string]{fn: func(_ context.Context, in string) (*string, error) {
			started <- in
			counter.enter()
			defer counter.exit()
			<-barrier
			return new(in), nil
		}}
		inputs := make([]string, 20)
		for i := range inputs {
			inputs[i] = fmt.Sprintf("v%d", i)
		}
		mr := MapReduce(child, func(mids []*string) (int, error) {
			return len(mids), nil
		}, WithParallelism(8))
		res := invokeAsync(t, mr, ctx, inputs)
		waitStarted(t, started, 8)
		close(barrier)
		r := <-res
		if r.err != nil {
			t.Fatalf("invoke: %v", r.err)
		}
		if r.out != 20 {
			t.Fatalf("reduce count = %d, want 20", r.out)
		}
		if peak := counter.peak(); peak > 8 {
			t.Fatalf("peak concurrent children = %d, want <= 8", peak)
		}
	})

	t.Run("subflows.fail-fast", func(t *testing.T) {
		errBoom := errors.New("boom")
		gate := make(chan struct{})
		var cancelled int32
		child := fanFlow[string, *string]{fn: func(cctx context.Context, in string) (*string, error) {
			if in == "v2" {
				return nil, errBoom
			}
			select {
			case <-gate:
			case <-cctx.Done():
			}
			if cctx.Err() != nil {
				atomic.AddInt32(&cancelled, 1)
			}
			return new(in), nil
		}}
		f := Fork(child)
		res := invokeAsync(t, f, ctx, []string{"v0", "v1", "v2", "v3", "v4"})
		r := <-res
		close(gate)
		if !errors.Is(r.err, errBoom) {
			t.Fatalf("err = %v, want the failing child's error", r.err)
		}
		if got := atomic.LoadInt32(&cancelled); got != 4 {
			t.Fatalf("cancelled children = %d, want 4", got)
		}
	})

	t.Run("subflows.fork-not-suspendable", func(t *testing.T) {
		var ran int32
		child := fanFlow[string, *string]{fn: func(_ context.Context, in string) (*string, error) {
			atomic.AddInt32(&ran, 1)
			return new(in), nil
		}}
		f := Fork(child)
		_, err := f.Resume(ctx, "tok", stores.ResumeInput{})
		if !errors.Is(err, types.ErrNotSuspendable) {
			t.Fatalf("err = %v, want types.ErrNotSuspendable", err)
		}
		if atomic.LoadInt32(&ran) != 0 {
			t.Fatalf("child ran %d times on resume, want 0", ran)
		}
	})

	t.Run("subflows.collect-partials", func(t *testing.T) {
		child := fanFlow[string, *string]{fn: func(_ context.Context, in string) (*string, error) {
			if in == "v3" {
				return nil, errors.New("boom")
			}
			return new(in), nil
		}}
		mr := MapReduce(child, func([]*string) (int, error) { return 0, nil }, WithOnError(types.Collect))
		_, err := mr.Invoke(ctx, []string{"v0", "v1", "v2", "v3", "v4"})
		var partial *types.PartialError
		if !errors.As(err, &partial) {
			t.Fatalf("err = %v, want *types.PartialError", err)
		}
		if len(partial.Errors) != 1 {
			t.Fatalf("errors = %d, want 1", len(partial.Errors))
		}
		if len(partial.Results) != 5 {
			t.Fatalf("results len = %d, want 5", len(partial.Results))
		}
		succeeded := 0
		for i, r := range partial.Results {
			if i == 3 {
				if r != nil {
					t.Fatalf("result at 3 = %v, want nil", r)
				}
				continue
			}
			if r.(*string) == nil {
				t.Fatalf("result at %d is nil pointer, want a value", i)
			}
			succeeded++
		}
		if succeeded != 4 {
			t.Fatalf("successful results = %d, want 4", succeeded)
		}
	})
}
