// Package gohantest holds the hand-written doubles and harnesses the
// conformance suites and adapter tests build on: a scripted model, cassette
// record/replay, fakes, fault injection and the leak check.
package gohantest

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// ScriptedModel replays canned turns in call order. It never blocks: each
// turn's chunks are yielded back to back, and a delay is recorded as a
// duration value rather than waited out.
type ScriptedModel struct {
	profile types.ModelProfile
	turns   []Turn

	mu       sync.Mutex
	calls    int
	requests []types.ModelRequest
	timings  [][]time.Duration
}

// Turn is one canned model call: the chunks it streams, the error it fails
// with, the request assertion it applies before streaming, and the delay
// recorded on its chunks.
type Turn struct {
	chunks []types.ModelChunk
	err    error
	assert func(types.ModelRequest) error
	usage  *types.Usage
	delay  time.Duration
}

// WithUsage pins the usage reported on the turn's final chunk.
func (t Turn) WithUsage(u types.Usage) Turn {
	u.KeyID = ""
	u.Estimated = false
	t.usage = &u
	return t
}

// WithDelay records d as the timing of the turn's chunks.
func (t Turn) WithDelay(d time.Duration) Turn { t.delay = d; return t }

// WithAssert pins the assembled request the turn must receive.
func (t Turn) WithAssert(check func(types.ModelRequest) error) Turn {
	t.assert = check
	return t
}

// Text streams s as a single text delta followed by a stop finish.
func Text(s string) Turn {
	return Turn{chunks: []types.ModelChunk{
		{Kind: types.DeltaText, Delta: s},
		{Finish: types.FinishStop},
	}}
}

// ToolCall streams one complete ToolUse block and finishes with tool_use.
func ToolCall(name string, args any) Turn {
	encoded, err := json.Marshal(args)
	if err != nil {
		panic(fmt.Sprintf("gohantest: ToolCall(%q): %v", name, err))
	}
	return Turn{chunks: []types.ModelChunk{
		{ToolUse: &types.ToolUse{ID: "call_" + name, Name: name, Args: jsontext.Value(encoded)}},
		{Finish: types.FinishToolUse},
	}}
}

// Fail ends the call with a *ModelError of the given class.
func Fail(class types.ErrorClass) Turn {
	return Turn{err: &types.ModelError{Class: class}}
}

// Refuse ends the call with a refusal finish.
func Refuse() Turn {
	return Turn{chunks: []types.ModelChunk{{Finish: types.FinishRefusal}}}
}

// NewScriptedModel builds a model replaying turns in order. A call beyond
// the last turn fails with ClassPermanent.
func NewScriptedModel(profile types.ModelProfile, turns ...Turn) *ScriptedModel {
	return &ScriptedModel{profile: profile, turns: turns}
}

// Profile returns the profile the model was built with.
func (m *ScriptedModel) Profile() types.ModelProfile { return m.profile }

// Generate replays the next canned turn. It records the request, runs the
// turn's assertion when one is set, then yields the turn's chunks; the
// final chunk carries the turn's usage, or a profile-derived estimate when
// the turn set none. It stops as soon as the consumer stops reading.
func (m *ScriptedModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.mu.Lock()
		turn := Turn{err: &types.ModelError{Class: types.ClassPermanent, Err: errExhausted}}
		if m.calls < len(m.turns) {
			turn = m.turns[m.calls]
		}
		m.calls++
		m.requests = append(m.requests, req)
		m.mu.Unlock()

		if turn.assert != nil {
			if err := turn.assert(req); err != nil {
				yield(types.ModelChunk{}, &types.ModelError{Class: types.ClassPermanent, Err: err})
				return
			}
		}
		if turn.err != nil {
			yield(types.ModelChunk{}, turn.err)
			return
		}

		chunks := turn.chunks
		var timing []time.Duration
		if turn.delay > 0 {
			timing = make([]time.Duration, len(chunks))
			for i := range timing {
				timing[i] = turn.delay
			}
		}
		u := &types.Usage{
			InputTokens:  10 * (len(req.Messages) + 1),
			OutputTokens: 5 * len(chunks),
			ModelVersion: m.profile.Version,
			Estimated:    true,
		}
		if turn.usage != nil {
			u = turn.usage
		}
		if len(chunks) == 0 {
			chunks = []types.ModelChunk{{Usage: u}}
		} else {
			chunks = append([]types.ModelChunk(nil), turn.chunks...)
			chunks[len(chunks)-1].Usage = u
		}

		m.mu.Lock()
		m.timings = append(m.timings, timing)
		m.mu.Unlock()
		for _, c := range chunks {
			if !yield(c, nil) {
				return
			}
		}
	}
}

// Requests returns the assembled request each call received, in call order.
func (m *ScriptedModel) Requests() []types.ModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]types.ModelRequest(nil), m.requests...)
}

// Timings returns, per call, the delay recorded on each chunk of that call.
func (m *ScriptedModel) Timings() [][]time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]time.Duration, len(m.timings))
	copy(out, m.timings)
	return out
}

var errExhausted = errors.New("gohantest: no scripted turns left")
