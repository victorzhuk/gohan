package route

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func classEndpoints() []*fakeModel {
	return []*fakeModel{
		newFakeModel("batch-a", types.Batch, okStream("batch-a")),
		newFakeModel("fast-a", types.Interactive, okStream("fast-a")),
		newFakeModel("batch-b", types.Batch, okStream("batch-b")),
		newFakeModel("fast-b", types.Interactive, okStream("fast-b")),
		newFakeModel("agentic-a", types.Agentic, okStream("agentic-a")),
	}
}

func assertOrder(t *testing.T, got []types.Model, want []string) {
	t.Helper()
	names := names(got)
	if len(names) != len(want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}

func TestRoutingStaticOrder(t *testing.T) {
	t.Run("routing.static-order", func(t *testing.T) {
		endpoints := classEndpoints()
		models := []types.Model{endpoints[0], endpoints[1], endpoints[2], endpoints[3], endpoints[4]}
		r, err := NewRouter(models)
		if err != nil {
			t.Fatal(err)
		}
		assertOrder(t, r.Candidates(), []string{"batch-a", "fast-a", "batch-b", "fast-b", "agentic-a"})
		assertOrder(t, r.Eligible(), []string{"batch-a", "fast-a", "batch-b", "fast-b", "agentic-a"})
		assertOrder(t, models, []string{"batch-a", "fast-a", "batch-b", "fast-b", "agentic-a"})
	})
}

func TestRoutingLatencyClassOrder(t *testing.T) {
	t.Run("routing.latency-class-order", func(t *testing.T) {
		endpoints := classEndpoints()
		models := []types.Model{endpoints[0], endpoints[1], endpoints[2], endpoints[3], endpoints[4]}
		r, err := NewRouter(models, WithLatencyClass(types.Interactive))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"fast-a", "fast-b", "batch-a", "batch-b", "agentic-a"}
		assertOrder(t, r.Candidates(), want)
		assertOrder(t, r.Eligible(), want)
		assertOrder(t, models, []string{"batch-a", "fast-a", "batch-b", "fast-b", "agentic-a"})
	})
}

func TestRoutingLatencyClassNoMatch(t *testing.T) {
	t.Run("routing.latency-class-no-match", func(t *testing.T) {
		models := []types.Model{
			newFakeModel("m1", types.Agentic, okStream("m1")),
			newFakeModel("m2", types.Batch, okStream("m2")),
			newFakeModel("m3", types.Batch, okStream("m3")),
		}
		r, err := NewRouter(models, WithLatencyClass(types.Interactive))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"m1", "m2", "m3"}
		assertOrder(t, r.Candidates(), want)
		assertOrder(t, r.Eligible(), want)
		assertOrder(t, models, want)
	})
}

func TestRoutingLatencyClassBreakerOrder(t *testing.T) {
	t.Run("routing.latency-class-breaker-order", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			endpoints := classEndpoints()
			models := []types.Model{endpoints[0], endpoints[1], endpoints[2], endpoints[3], endpoints[4]}
			r, err := NewRouter(models, WithLatencyClass(types.Interactive))
			if err != nil {
				t.Fatal(err)
			}
			for range defaultOpenThreshold {
				r.RecordFailure("fast-a", transientErr("fast-a"))
			}
			assertOrder(t, r.Candidates(), []string{"fast-b", "batch-a", "batch-b", "agentic-a"})
			assertOrder(t, r.Eligible(), []string{"fast-b", "batch-a", "batch-b", "agentic-a"})

			br := r.Breaker("fast-a")
			br.window, br.halfOpenDelay = 0, 0
			time.Sleep(time.Millisecond)
			if br.State() != HalfOpen {
				t.Fatalf("state = %v, want half-open", br.State())
			}
			if got := r.Eligible(); len(got) != 4 {
				t.Fatalf("eligible = %d, want 4: half-open must not be free admission", len(got))
			}
			if br.probing {
				t.Fatal("Eligible consumed the half-open probe")
			}
			if got := r.Candidates(); len(got) != 5 {
				t.Fatalf("candidates = %d, want 5: Candidates consumes the probe", len(got))
			}
		})
	})
}

func TestRoutingLatencyClassFallback(t *testing.T) {
	t.Run("routing.latency-class-fallback", func(t *testing.T) {
		preferred1 := newFakeModel("fast-a", types.Interactive,
			[]streamItem{{err: rateLimitedErr("fast-a")}})
		preferred2 := newFakeModel("fast-b", types.Interactive,
			[]streamItem{{err: rateLimitedErr("fast-b")}})
		fallback := newFakeModel("batch-a", types.Batch, okStream("from-batch-a"))
		r, err := NewRouter([]types.Model{preferred1, preferred2, fallback}, WithLatencyClass(types.Interactive))
		if err != nil {
			t.Fatal(err)
		}
		var servedName string
		chunks, err := call(t, r, WithServed(func(m types.Model) { servedName = m.Profile().Name }))
		if err != nil {
			t.Fatal(err)
		}
		if servedName != "batch-a" {
			t.Fatalf("served = %q, want batch-a", servedName)
		}
		if len(chunks) != 1 || chunks[0].Delta != "from-batch-a" {
			t.Fatalf("chunks = %v, want the fallback endpoint's chunk", chunks)
		}
		if preferred1.calls != 1 || preferred2.calls != 1 || fallback.calls != 1 {
			t.Fatalf("calls = %d/%d/%d, want one per endpoint in order", preferred1.calls, preferred2.calls, fallback.calls)
		}
	})
}
