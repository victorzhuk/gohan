package route

import (
	"context"
	"errors"
	"iter"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type fakeModel struct {
	profile types.ModelProfile
	calls   int
	streams [][]streamItem
}

type streamItem struct {
	chunk types.ModelChunk
	err   error
}

func (f *fakeModel) Profile() types.ModelProfile { return f.profile }

func (f *fakeModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		stream := f.streams[f.calls]
		f.calls++
		for _, item := range stream {
			if item.err != nil {
				yield(types.ModelChunk{}, item.err)
				return
			}
			if !yield(item.chunk, nil) {
				return
			}
		}
	}
}

func newFakeModel(name string, class types.LatencyClass, streams ...[]streamItem) *fakeModel {
	return &fakeModel{
		profile: types.ModelProfile{Name: name, LatencyClass: class},
		streams: streams,
	}
}

func transientErr(provider string) error {
	return &types.ModelError{Class: types.ClassTransient, Provider: provider, Status: 500}
}

func rateLimitedErr(provider string) error {
	return &types.ModelError{Class: types.ClassRateLimited, Provider: provider, Status: 429}
}

func textChunk(text string) streamItem {
	return streamItem{chunk: types.ModelChunk{Kind: types.DeltaText, Delta: text}}
}

func okStream(text string) []streamItem { return []streamItem{textChunk(text)} }

// call runs one request through the fallback middleware over the router and
// drains the stream.
func call(t *testing.T, r *Router, opts ...FallbackOption) ([]types.ModelChunk, error) {
	t.Helper()
	stream := Fallback(r, func(m types.Model) types.ModelFunc { return m.Generate }, opts...)(context.Background(), types.ModelRequest{})
	var chunks []types.ModelChunk
	var err error
	for chunk, e := range stream {
		if e != nil {
			err = e
			break
		}
		chunks = append(chunks, chunk)
	}
	return chunks, err
}

func TestEndpointRouting(t *testing.T) {
	t.Run("model.breaker-opens", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			primary := newFakeModel("primary", types.Interactive,
				[]streamItem{{err: transientErr("primary")}},
				[]streamItem{{err: transientErr("primary")}},
				okStream("recovered"),
			)
			secondary := newFakeModel("secondary", types.Interactive,
				okStream("from-secondary"),
				okStream("from-secondary"),
			)
			router, err := NewRouter([]types.Model{primary, secondary},
				WithBreaker("primary", NewBreaker(
					WithOpenThreshold(2),
					WithWindow(time.Second),
					WithHalfOpenDelay(time.Second),
				)),
			)
			if err != nil {
				t.Fatalf("NewRouter: %v", err)
			}

			// Two transient failures open the breaker; each call fails over
			// to the secondary.
			for range 2 {
				chunks, err := call(t, router)
				if err != nil {
					t.Fatalf("failover call: %v", err)
				}
				if len(chunks) == 0 || chunks[0].Delta != "from-secondary" {
					t.Fatalf("chunks = %v, want secondary's chunk", chunks)
				}
			}
			if got := router.Candidates(); got[0].Profile().Name != "secondary" {
				t.Fatalf("candidates = %v, want the open primary skipped", names(got))
			}

			// After the window plus the half-open delay one probe is
			// admitted; its success closes the breaker.
			time.Sleep(2 * time.Second)
			chunks, err := call(t, router)
			if err != nil {
				t.Fatalf("probe call: %v", err)
			}
			if len(chunks) == 0 || chunks[0].Delta != "recovered" {
				t.Fatalf("probe chunks = %v, want primary's recovered chunk", chunks)
			}
			if primary.calls != 3 {
				t.Fatalf("primary calls = %d, want the probe on the primary", primary.calls)
			}
			if got := router.Candidates(); got[0].Profile().Name != "primary" {
				t.Fatalf("candidates = %v, want the breaker closed again", names(got))
			}
		})
	})

	t.Run("model.429-fails-over-without-retry", func(t *testing.T) {
		primary := newFakeModel("primary", types.Interactive,
			[]streamItem{{err: rateLimitedErr("primary")}},
		)
		secondary := newFakeModel("secondary", types.Interactive, okStream("ok"))
		router, err := NewRouter([]types.Model{primary, secondary})
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}
		var served string
		chunks, err := call(t, router, WithServed(func(m types.Model) { served = m.Profile().Name }))
		if err != nil {
			t.Fatalf("failover call: %v", err)
		}
		if len(chunks) == 0 || chunks[0].Delta != "ok" {
			t.Fatalf("chunks = %v, want secondary's ok chunk", chunks)
		}
		if primary.calls != 1 {
			t.Fatalf("primary calls = %d, want exactly one: a 429 never retries the same endpoint", primary.calls)
		}
		if secondary.calls != 1 || served != "secondary" {
			t.Fatalf("secondary calls = %d, served = %q, want failover to and charge on secondary", secondary.calls, served)
		}
	})

	t.Run("chains.fallback-charged", func(t *testing.T) {
		primary := newFakeModel("primary", types.Interactive,
			[]streamItem{{err: transientErr("primary")}},
		)
		fallbackModel := newFakeModel("fallback", types.Interactive, okStream("fallback-answer"))
		router, err := NewRouter([]types.Model{primary, fallbackModel})
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}
		var served types.Model
		chunks, err := call(t, router, WithServed(func(m types.Model) { served = m }))
		if err != nil {
			t.Fatalf("fallback call: %v", err)
		}
		if len(chunks) == 0 || chunks[0].Delta != "fallback-answer" {
			t.Fatalf("chunks = %v, want the fallback's answer", chunks)
		}
		if served == nil || served.Profile().Name != "fallback" {
			t.Fatalf("served endpoint = %v, want fallback", served)
		}
		// The charging half (budget charged with the fallback's usage) needs
		// RunLimits, row 23.7; WithServed is the charging point that row
		// wires, and the failover metric is row 28.
	})

	t.Run("static-strategy", func(t *testing.T) {
		a := newFakeModel("a", types.Batch, okStream("a"))
		b := newFakeModel("b", types.Interactive, okStream("b"))
		router, err := NewRouter([]types.Model{a, b})
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}
		got := router.Candidates()
		if got[0].Profile().Name != "a" || got[1].Profile().Name != "b" {
			t.Fatalf("static order = %v, want declaration order a,b", names(got))
		}
	})

	t.Run("latency-class-strategy", func(t *testing.T) {
		batch := newFakeModel("batch", types.Batch, okStream("batch"))
		agent := newFakeModel("agentic", types.Agentic, okStream("agentic"))
		router, err := NewRouter([]types.Model{batch, agent}, WithLatencyClass(types.Agentic))
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}
		got := router.Candidates()
		if got[0].Profile().Name != "agentic" || got[1].Profile().Name != "batch" {
			t.Fatalf("class order = %v, want agentic first", names(got))
		}
	})

	t.Run("half-open-probe-single-slot", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			b := NewBreaker(WithOpenThreshold(1), WithWindow(time.Second), WithHalfOpenDelay(0))
			if !b.Allow() {
				t.Fatal("closed breaker denied a call")
			}
			b.Record(transientErr("p"))
			if b.Allow() {
				t.Fatal("open breaker admitted a call")
			}
			time.Sleep(time.Second)
			if !b.Allow() {
				t.Fatal("half-open probe denied after the window")
			}
			if b.Allow() {
				t.Fatal("second call admitted while the probe is in flight")
			}
			b.Record(transientErr("p"))
			if b.Allow() {
				t.Fatal("failed probe did not reopen the breaker")
			}
		})
	})

	t.Run("exhausted-fallback-list", func(t *testing.T) {
		primary := newFakeModel("primary", types.Interactive,
			[]streamItem{{err: transientErr("primary")}},
		)
		secondary := newFakeModel("secondary", types.Interactive,
			[]streamItem{{err: rateLimitedErr("secondary")}},
		)
		router, err := NewRouter([]types.Model{primary, secondary})
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}
		_, err = call(t, router)
		me, ok := errors.AsType[*types.ModelError](err)
		if !ok || me.Class != types.ClassRateLimited {
			t.Fatalf("err = %v, want the last endpoint's ClassRateLimited surfaced", err)
		}
		if primary.calls != 1 || secondary.calls != 1 {
			t.Fatalf("calls = %d/%d, want one attempt per endpoint", primary.calls, secondary.calls)
		}
	})
}

func names(models []types.Model) []string {
	var out []string
	for _, m := range models {
		out = append(out, m.Profile().Name)
	}
	return out
}
