package telemetry

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestFeedbackMetricLabels(t *testing.T) {
	t.Run("telemetry.feedback-metric-labels", func(t *testing.T) {
		sink := &recordingSink{}
		m, err := Build(sink, DefaultMetrics, WithRelease("r7"), WithVariant("b"))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		m.Record(context.Background(), MetricFeedback, 4,
			types.String("name", "csat"),
			types.String(types.KeyFlow, "support"),
		)
		obs := findMetric(t, sink.records, MetricFeedback)
		if obs.v != 4 {
			t.Fatalf("value = %v, want 4", obs.v)
		}
		got := map[string]string{}
		for _, a := range obs.attrs {
			got[a.Key] = a.Value.(string)
		}
		want := map[string]string{"name": "csat", "flow": "support", types.KeyRelease: "r7", types.KeyVariant: "b"}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("label %s = %q, want %q", k, got[k], v)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("labels %v, want exactly %v", got, want)
		}
		for _, banned := range []string{"subject", "session_id", "comment", "run_id"} {
			if _, ok := got[banned]; ok {
				t.Fatalf("label %s must not be carried", banned)
			}
		}
	})
}

func TestFeedbackMetricDropsIdentityLabels(t *testing.T) {
	t.Run("telemetry.feedback-metric-labels", func(t *testing.T) {
		sink := &recordingSink{}
		m, err := Build(sink, DefaultMetrics, WithRelease("r7"), WithVariant("b"))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		m.Record(context.Background(), MetricFeedback, 4,
			types.String("name", "csat"),
			types.String(types.KeyFlow, "support"),
			types.String("subject", "u1"),
			types.String("session_id", "s1"),
			types.String("run_id", "r1"),
			types.String("target", "m3"),
			types.String("comment", "the reply was wrong"),
			types.String("correction", "use the other tool"),
			types.String(types.KeySubject, "u1"),
			types.String(types.KeySessionID, "s1"),
			types.String(types.KeyRunID, "r1"),
		)
		obs := findMetric(t, sink.records, MetricFeedback)
		got := map[string]string{}
		for _, a := range obs.attrs {
			got[a.Key] = a.Value.(string)
		}
		want := map[string]string{"name": "csat", "flow": "support", types.KeyRelease: "r7", types.KeyVariant: "b"}
		if len(got) != len(want) {
			t.Fatalf("labels %v, want exactly %v", got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("label %s = %q, want %q", k, got[k], v)
			}
		}
		for _, banned := range []string{"subject", "session_id", "run_id", "target", "comment", "correction"} {
			if _, ok := got[banned]; ok {
				t.Fatalf("label %s carries subject data into the metric", banned)
			}
		}
	})
}
