// Package gohantest holds the hand-written doubles and harnesses the
// conformance suites and adapter tests build on: a scripted model, cassette
// record/replay, fakes, fault injection and the leak check.
package gohantest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// FakeTool is a tool double with a canned result or error. It records every
// call's arguments.
type FakeTool struct {
	spec   types.ToolSpec
	result types.ToolResult
	err    error

	mu    sync.Mutex
	calls []json.RawMessage
}

// ToolFakeOption adjusts a FakeTool at construction.
type ToolFakeOption func(*FakeTool)

// WithToolSpec mutates the spec fields the double carries.
func WithToolSpec(mutate func(*types.ToolSpec)) ToolFakeOption {
	return func(t *FakeTool) { mutate(&t.spec) }
}

// WithToolResult sets the result every call returns. The default is a
// succeeded result with no blocks.
func WithToolResult(r types.ToolResult) ToolFakeOption {
	return func(t *FakeTool) { t.result = r }
}

// WithToolError sets the error every call returns alongside a failed
// outcome.
func WithToolError(err error) ToolFakeOption {
	return func(t *FakeTool) { t.err = err }
}

// NewFakeTool builds a tool double named name. By default every call
// succeeds with an empty result.
func NewFakeTool(name string, opts ...ToolFakeOption) *FakeTool {
	t := &FakeTool{
		spec: types.ToolSpec{
			Name:        name,
			Description: "gohantest fake tool",
			Effect:      types.ReadOnly,
		},
		result: types.ToolResult{ID: name + "-1", Outcome: types.Succeeded},
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Spec implements types.Tool.
func (t *FakeTool) Spec() types.ToolSpec { return t.spec }

// Call implements types.Tool.
func (t *FakeTool) Call(_ context.Context, args json.RawMessage) (types.ToolResult, error) {
	t.mu.Lock()
	t.calls = append(t.calls, args)
	t.mu.Unlock()
	if t.err != nil {
		return types.ToolResult{ID: t.spec.Name + "-1", Outcome: types.Failed,
			Error: &types.ToolError{Kind: types.Permanent, Message: t.err.Error()}}, t.err
	}
	return t.result, nil
}

// Calls returns the raw arguments of every call, in call order.
func (t *FakeTool) Calls() []json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]json.RawMessage, len(t.calls))
	copy(out, t.calls)
	return out
}

// FakeClock is a wall-clock double. Time moves only through Advance, and
// After yields on a channel, so nothing here needs a real timer.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []clockWaiter
}

type clockWaiter struct {
	at time.Time
	ch chan time.Time
}

// ClockOption adjusts a FakeClock at construction.
type ClockOption func(*FakeClock)

// WithClockStart sets the starting time. The default is
// 2026-01-01T00:00:00Z.
func WithClockStart(t time.Time) ClockOption {
	return func(c *FakeClock) { c.now = t }
}

// NewFakeClock builds a clock double.
func NewFakeClock(opts ...ClockOption) *FakeClock {
	c := &FakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d and releases every waiter whose
// deadline has passed.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	remaining := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
			close(w.ch)
			continue
		}
		remaining = append(remaining, w)
	}
	c.waiters = remaining
	c.mu.Unlock()
}

// After returns a buffered channel that receives the current time once the
// clock has advanced d past the call. The channel is closed at that point,
// so a waiter abandoned by a test never leaks a goroutine.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	if d <= 0 {
		ch <- c.now
		close(ch)
	} else {
		c.waiters = append(c.waiters, clockWaiter{at: c.now.Add(d), ch: ch})
	}
	c.mu.Unlock()
	return ch
}

// ErrKeyMissing is returned by FakeStore.Get for a key that was never put.
var ErrKeyMissing = fmt.Errorf("gohantest: key missing")

// FakeStore is a small in-memory key/value store double with an injectable
// fault. Every operation checks the fault first.
type FakeStore struct {
	mu    sync.Mutex
	data  map[string][]byte
	fault error
	calls int
}

// StoreOption adjusts a FakeStore at construction.
type StoreOption func(*FakeStore)

// WithSeed preloads the store.
func WithSeed(data map[string][]byte) StoreOption {
	return func(s *FakeStore) {
		for k, v := range data {
			s.data[k] = v
		}
	}
}

// WithStoreFault makes every operation fail with err until ClearFault.
func WithStoreFault(err error) StoreOption {
	return func(s *FakeStore) { s.fault = err }
}

// NewFakeStore builds an empty store double.
func NewFakeStore(opts ...StoreOption) *FakeStore {
	s := &FakeStore{data: map[string][]byte{}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Put stores value under key.
func (s *FakeStore) Put(key string, value []byte) error {
	if err := s.begin(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

// Get returns the value under key, or ErrKeyMissing.
func (s *FakeStore) Get(key string) ([]byte, error) {
	if err := s.begin(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	if !ok {
		return nil, ErrKeyMissing
	}
	return v, nil
}

// Delete removes key.
func (s *FakeStore) Delete(key string) error {
	if err := s.begin(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// ClearFault removes an injected store fault.
func (s *FakeStore) ClearFault() {
	s.mu.Lock()
	s.fault = nil
	s.mu.Unlock()
}

// Calls returns how many operations the store has served.
func (s *FakeStore) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *FakeStore) begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.fault != nil {
		return fmt.Errorf("gohantest: store fault: %w", s.fault)
	}
	return nil
}
