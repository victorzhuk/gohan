package telemetry

import (
	"testing"
	"time"
)

func TestLatencyWindow(t *testing.T) {
	t.Run("rolling-p90", func(t *testing.T) {
		w := NewLatencyWindow(100)
		for i := range 10 {
			w.Observe("a", time.Duration(i+1)*10*time.Millisecond)
		}
		got, ok := w.P90("a")
		if !ok {
			t.Fatal("P90 reported no samples")
		}
		if want := 90 * time.Millisecond; got != want {
			t.Fatalf("P90 = %v, want %v", got, want)
		}
	})
	t.Run("ring-bounded", func(t *testing.T) {
		w := NewLatencyWindow(4)
		for i := range 10 {
			w.Observe("a", time.Duration(i)*time.Millisecond)
		}
		got, _ := w.P90("a")
		if want := 9 * time.Millisecond; got != want {
			t.Fatalf("P90 = %v, want %v (oldest samples evicted)", got, want)
		}
	})
	t.Run("per-endpoint", func(t *testing.T) {
		w := NewLatencyWindow(0)
		w.Observe("a", time.Second)
		w.Observe("b", time.Millisecond)
		if got, _ := w.P90("b"); got != time.Millisecond {
			t.Fatalf("P90(b) = %v, want 1ms", got)
		}
	})
	t.Run("empty-reports-false", func(t *testing.T) {
		w := NewLatencyWindow(0)
		if _, ok := w.P90("missing"); ok {
			t.Fatal("P90 on empty window must report false")
		}
	})
}
