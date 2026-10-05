// Package gohantest holds the hand-written doubles and harnesses the
// conformance suites and adapter tests build on: a scripted model, cassette
// record/replay, fakes, fault injection and the leak check.
package gohantest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/victorzhuk/gohan/core/types"
)

// ErrFault is wrapped by every injected fault, so a test can tell an
// injected failure from one the code under test raised itself.
var ErrFault = errors.New("gohantest: injected fault")

// FaultPlan selects the model fault a suite wants: every Nth call fails
// with Class after AfterChunks chunks of the real stream have passed
// through. A zero Every fails the first call; a zero Class is
// ClassTransient; a zero AfterChunks fails before any chunk.
type FaultPlan struct {
	Every       int
	Class       types.ErrorClass
	AfterChunks int
}

// Flaky wraps m with the fault plan plan. The seam is the model: a test
// names the failure it wants, not how to fake the provider.
func Flaky(m types.Model, plan FaultPlan) types.Model {
	if plan.Every <= 0 {
		plan.Every = 1
	}
	if plan.Class == 0 {
		plan.Class = types.ClassTransient
	}
	return &flakyModel{inner: m, plan: plan}
}

type flakyModel struct {
	inner types.Model
	plan  FaultPlan

	mu    sync.Mutex
	calls int
}

func (f *flakyModel) Profile() types.ModelProfile { return f.inner.Profile() }

func (f *flakyModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		f.mu.Lock()
		f.calls++
		nth := f.calls%f.plan.Every == 0
		f.mu.Unlock()

		if !nth {
			f.inner.Generate(ctx, req)(yield)
			return
		}

		passed := 0
		for chunk, err := range f.inner.Generate(ctx, req) {
			if err != nil {
				yield(chunk, err)
				return
			}
			if passed >= f.plan.AfterChunks {
				break
			}
			passed++
			if !yield(chunk, nil) {
				return
			}
		}
		yield(types.ModelChunk{}, &types.ModelError{
			Class:    f.plan.Class,
			Provider: f.inner.Profile().Name,
			Err:      fmt.Errorf("%w: model fault after %d chunks", ErrFault, passed),
		})
	}
}

// FaultyTool wraps t so every call fails with err. The seam is the tool:
// the caller sees err, wrapped with ErrFault, and a failed outcome.
func FaultyTool(t types.Tool, err error) types.Tool {
	return &faultyTool{inner: t, err: err}
}

type faultyTool struct {
	inner types.Tool
	err   error
}

func (f *faultyTool) Spec() types.ToolSpec { return f.inner.Spec() }

func (f *faultyTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	spec := f.inner.Spec()
	return types.ToolResult{ID: spec.Name, Outcome: types.Failed,
		Error: &types.ToolError{Kind: types.Permanent, Message: f.err.Error()},
	}, fmt.Errorf("%w: tool %s: %w", ErrFault, spec.Name, f.err)
}

// FaultyStore injects err as the store's fault: every operation fails with
// err, wrapped with ErrFault, until ClearFault.
func FaultyStore(s *FakeStore, err error) *FakeStore {
	s.mu.Lock()
	s.fault = fmt.Errorf("%w: %w", ErrFault, err)
	s.mu.Unlock()
	return s
}

// CancelledContext returns an already-cancelled context. The caller must
// invoke the returned CancelFunc.
func CancelledContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx, cancel
}

// leakReporter is the part of *testing.T the leak check needs; a test may
// substitute its own reporter to observe a failure.
type leakReporter interface {
	Helper()
	Errorf(format string, args ...any)
	Failed() bool
}

// leakSpins bounds how long LeakCheck waits for goroutines to drain. The
// wait is a Gosched spin rather than a timer, so the profile is safe under
// testing/synctest.
const (
	leakSpins          = 200000
	maxGoroutineStacks = 16 << 20
)

type goroutineStack struct {
	id   string
	head string
}

func goroutineSnapshot() (map[string]goroutineStack, error) {
	size := 64 << 10
	for {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			stacks := make(map[string]goroutineStack)
			var id, head string
			for _, line := range strings.Split(string(buf[:n]), "\n") {
				if strings.HasPrefix(line, "goroutine ") {
					if id != "" {
						stacks[id] = goroutineStack{id: id, head: head}
					}
					fields := strings.Fields(line)
					id, head = "", ""
					if len(fields) > 1 {
						id = fields[1]
					}
					continue
				}
				if id != "" && head == "" && strings.HasPrefix(line, "\t") {
					head = strings.TrimSpace(line)
				}
			}
			if id != "" {
				stacks[id] = goroutineStack{id: id, head: head}
			}
			return stacks, nil
		}
		if size >= maxGoroutineStacks {
			return nil, fmt.Errorf("goroutine snapshot exceeds %d bytes", maxGoroutineStacks)
		}
		size *= 2
		if size > maxGoroutineStacks {
			size = maxGoroutineStacks
		}
	}
}

// LeakCheck runs fn and fails the test if a new goroutine remains when fn
// returns. It runs on the caller's goroutine and spawns none of its own.
func LeakCheck(tb leakReporter, fn func()) {
	tb.Helper()
	before, err := goroutineSnapshot()
	if err != nil {
		tb.Errorf("gohantest: cannot capture goroutines before checked function: %v", err)
		return
	}
	fn()
	var after map[string]goroutineStack
	for range leakSpins {
		after, err = goroutineSnapshot()
		if err != nil {
			tb.Errorf("gohantest: cannot capture goroutines after checked function: %v", err)
			return
		}
		hasNew := false
		for id := range after {
			if _, existed := before[id]; !existed {
				hasNew = true
				break
			}
		}
		if !hasNew {
			return
		}
		runtime.Gosched()
	}
	residual := make([]string, 0)
	for id, stack := range after {
		if _, existed := before[id]; !existed {
			head := stack.head
			if parsed, parseErr := strconv.ParseUint(id, 10, 64); parseErr == nil {
				residual = append(residual, fmt.Sprintf("goroutine %d: %s", parsed, head))
			}
		}
	}
	tb.Errorf("gohantest: goroutine(s) outlived the checked function: %s", strings.Join(residual, "; "))
}
