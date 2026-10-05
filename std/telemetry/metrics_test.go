package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type observation struct {
	name  string
	n     int64
	v     float64
	attrs []types.Attr
}

type recordingSink struct {
	counts   []observation
	records  []observation
	spans    []spanObservation
	spanCtxs []context.Context
}

func (s *recordingSink) StartSpan(ctx context.Context, _ string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	s.spans = append(s.spans, spanObservation{attrs: attrs})
	s.spanCtxs = append(s.spanCtxs, ctx)
	i := len(s.spans) - 1
	return ctx, func(final ...types.Attr) { s.spans[i].final = final }
}

func (s *recordingSink) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	s.counts = append(s.counts, observation{name: name, n: n, attrs: attrs})
}

func (s *recordingSink) Record(_ context.Context, name string, v float64, attrs ...types.Attr) {
	s.records = append(s.records, observation{name: name, v: v, attrs: attrs})
}

func findMetric(t *testing.T, obs []observation, name string) observation {
	t.Helper()
	for _, o := range obs {
		if o.name == name {
			return o
		}
	}
	t.Fatalf("no observation for %s", name)
	return observation{}
}

func attrValue(t *testing.T, attrs []types.Attr, key string) string {
	t.Helper()
	for _, a := range attrs {
		if a.Key == key {
			return a.Value.(string)
		}
	}
	t.Fatalf("no attribute %s", key)
	return ""
}

// streamTimes derives token arrival times from the scripted model's own
// recorded chunk timings, moving a fake clock instead of sleeping.
func streamTimes(t *testing.T, m *gohantest.ScriptedModel) (time.Time, []time.Time) {
	t.Helper()
	clock := gohantest.NewFakeClock()
	start := clock.Now()
	var tokens []time.Time
	for _, call := range m.Timings() {
		for _, d := range call {
			clock.Advance(d)
			tokens = append(tokens, clock.Now())
		}
	}
	if len(tokens) == 0 {
		t.Fatal("scripted model recorded no chunk timings")
	}
	return start, tokens
}

func TestTelemetryMetrics(t *testing.T) {
	ctx := context.Background()
	profile := types.ModelProfile{Name: "echo", Version: "v1"}

	t.Run("telemetry.ttft-and-tpot", func(t *testing.T) {
		turns := make([]gohantest.Turn, 5)
		for i := range turns {
			turns[i] = gohantest.Text("chunk").WithDelay(20 * time.Millisecond)
		}
		m := gohantest.NewScriptedModel(profile, turns...)
		req := types.ModelRequest{}
		for range 5 {
			for chunk, err := range m.Generate(ctx, req) {
				if err != nil {
					t.Fatalf("generate: %v", err)
				}
				_ = chunk
			}
		}
		start, tokens := streamTimes(t, m)
		if len(tokens) != 10 {
			t.Fatalf("got %d tokens, want 10", len(tokens))
		}

		sink := &recordingSink{}
		metrics, err := Build(sink, []Metric{
			{Name: MetricTTFT, Labels: []string{"release", "variant"}},
			{Name: MetricTPOT, Labels: []string{"release", "variant"}},
		}, WithRelease("r7"), WithVariant("b"))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		metrics.ObserveStream(ctx, start, tokens...)

		ttft := findMetric(t, sink.records, MetricTTFT)
		if diff := ttft.v - 20.0; diff < -0.001 || diff > 0.001 {
			t.Errorf("ttft = %v ms, want 20", ttft.v)
		}
		tpot := findMetric(t, sink.records, MetricTPOT)
		if diff := tpot.v - 20.0; diff < -0.001 || diff > 0.001 {
			t.Errorf("tpot = %v ms, want 20", tpot.v)
		}
		if got := attrValue(t, ttft.attrs, types.KeyRelease); got != "r7" {
			t.Errorf("ttft release = %q, want r7", got)
		}
		if got := attrValue(t, tpot.attrs, types.KeyVariant); got != "b" {
			t.Errorf("tpot variant = %q, want b", got)
		}
	})

	t.Run("telemetry.loop-detection", func(t *testing.T) {
		sink := &recordingSink{}
		metrics := mustBuild(t, sink)
		detector := NewLoopDetector(metrics, 3)
		args := `{"q":"gohan"}`
		for range 4 {
			detector.Observe(ctx, "search", args)
		}
		if len(sink.counts) != 1 {
			t.Fatalf("got %d counts, want 1", len(sink.counts))
		}
		c := sink.counts[0]
		if c.name != MetricLoopDetected || c.n != 1 {
			t.Errorf("count = %s %d, want %s 1", c.name, c.n, MetricLoopDetected)
		}
		if got := attrValue(t, c.attrs, "tool"); got != "search" {
			t.Errorf("tool label = %q, want search", got)
		}
	})

	t.Run("telemetry.forbidden-label", func(t *testing.T) {
		_, err := Build(&recordingSink{}, []Metric{
			{Name: "gohan.feedback", Labels: []string{"name", "session_id"}},
		})
		if err == nil {
			t.Fatal("Build accepted a session_id label")
		}
		for _, want := range []string{"gohan.feedback", "session_id"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %s", err, want)
			}
		}
	})

	t.Run("repeat below threshold not counted", func(t *testing.T) {
		sink := &recordingSink{}
		metrics := mustBuild(t, sink)
		detector := NewLoopDetector(metrics, 3)
		args := `{"q":"gohan"}`
		for range 3 {
			detector.Observe(ctx, "search", args)
		}
		if len(sink.counts) != 0 {
			t.Errorf("got %d counts, want 0 below threshold", len(sink.counts))
		}
	})

	t.Run("allowlist admits permitted label", func(t *testing.T) {
		_, err := Build(&recordingSink{}, []Metric{
			{Name: "gohan.tool.calls", Labels: []string{"name", "flow", "release", "variant"}},
		}, WithRelease("r7"))
		if err != nil {
			t.Errorf("Build refused permitted labels: %v", err)
		}
	})
}

func mustBuild(t *testing.T, sink *recordingSink) *Metrics {
	t.Helper()
	metrics, err := Build(sink, []Metric{
		{Name: MetricLoopDetected, Labels: []string{"tool", "release", "variant"}},
	}, WithRelease("r7"), WithVariant("b"))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return metrics
}
