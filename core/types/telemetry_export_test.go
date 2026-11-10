package types

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAttrWireRoundTrip(t *testing.T) {
	t.Run("constructor kinds round-trip", func(t *testing.T) {
		cases := []Attr{
			String("gohan.release", "r7"),
			Int("gohan.turn", 2),
			Float("gohan.cost", 0.5),
			Bool("gohan.cached", true),
		}
		for _, in := range cases {
			data, err := in.MarshalJSON()
			if err != nil {
				t.Fatalf("marshal %v: %v", in, err)
			}
			var out Attr
			if err := out.UnmarshalJSON(data); err != nil {
				t.Fatalf("unmarshal %s: %v", data, err)
			}
			if out.Key != in.Key || out.Value != in.Value {
				t.Fatalf("round-trip %v: got %v", in, out)
			}
		}
	})

	t.Run("frozen kind literals", func(t *testing.T) {
		for kind, value := range map[string]any{
			"string":  "x",
			"int64":   int64(2),
			"float64": 1.5,
			"bool":    false,
		} {
			data, err := Attr{Key: "k", Value: value}.MarshalJSON()
			if err != nil {
				t.Fatalf("marshal %v: %v", value, err)
			}
			want := fmt.Sprintf(`{"key":"k","kind":%q,"value":`, kind)
			if !strings.HasPrefix(string(data), want) {
				t.Fatalf("kind literal for %v: got %s", value, data)
			}
		}
	})

	t.Run("non-constructor value falls back to string kind", func(t *testing.T) {
		data, err := Attr{Key: "k", Value: []int{1, 2}}.MarshalJSON()
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var out Attr
		if err := out.UnmarshalJSON(data); err != nil {
			t.Fatalf("unmarshal %s: %v", data, err)
		}
		if out.Key != "k" || out.Value != "[1 2]" {
			t.Fatalf("fallback: got %v", out)
		}
	})

	t.Run("decode mismatch names key and kind", func(t *testing.T) {
		cases := map[string]string{
			"string-null": `{"key":"gohan.turn","kind":"string","value":null}`,
			"string":      `{"key":"gohan.turn","kind":"string","value":2}`,
			"int64":       `{"key":"gohan.turn","kind":"int64","value":"two"}`,
			"float64":     `{"key":"gohan.cost","kind":"float64","value":true}`,
			"bool":        `{"key":"gohan.cached","kind":"bool","value":1}`,
		}
		for kind, data := range cases {
			var a Attr
			err := a.UnmarshalJSON([]byte(data))
			if err == nil {
				t.Fatalf("kind %s: expected mismatch error", kind)
			}
			wantKind := strings.TrimSuffix(kind, "-null")
			if !strings.Contains(err.Error(), "gohan") || !strings.Contains(err.Error(), wantKind) {
				t.Fatalf("kind %s: error %q must name key and kind", kind, err)
			}
		}
	})
}

func TestExportRecordWireShape(t *testing.T) {
	start := time.Date(2026, 11, 10, 12, 0, 0, 1, time.UTC)
	end := start.Add(250000001 * time.Nanosecond)
	rec := ExportRecord{
		SchemaVersion: CurrentExportSchemaVersion,
		ID:            RecordID("0194f0c2-7a3b-7c1d-9e2f-0a1b2c3d4e5f"),
		Resource:      ResourceIdentity{Name: "gohan", Attrs: []Attr{String("gohan.release", "r7")}},
		Signal:        SignalSpan,
		Span: &SpanRecord{
			TraceID:  "0af7651916cd43dd8448eb211c80319c",
			SpanID:   "b7ad6b7169203331",
			ParentID: "b9c7c989f97918e1",
			Name:     "chat",
			Start:    start,
			End:      end,
			Attrs:    []Attr{Int("gohan.turn", 2)},
			Outcome:  SpanOK,
			Source: Source{
				Name:      "github.com/victorzhuk/gohan",
				Version:   "v0.2.0",
				SchemaURL: "https://opentelemetry.io/schemas/1.27.0",
			},
		},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"schema_version":1`,
		`"id":"0194f0c2-7a3b-7c1d-9e2f-0a1b2c3d4e5f"`,
		`"signal":"span"`,
		`"trace_id":"0af7651916cd43dd8448eb211c80319c"`,
		`"span_id":"b7ad6b7169203331"`,
		`"parent_id":"b9c7c989f97918e1"`,
		`"outcome":"ok"`,
		`"schema_url":"https://opentelemetry.io/schemas/1.27.0"`,
		`{"key":"gohan.release","kind":"string","value":"r7"}`,
		`{"key":"gohan.turn","kind":"int64","value":2}`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("wire shape: %s not in %s", want, data)
		}
	}
	if strings.Contains(string(data), `"metric":`) {
		t.Fatalf("span record must omit metric: %s", data)
	}

	t.Run("metric variant snake_case and enum literals", func(t *testing.T) {
		min, max := 4.5, 12.0
		rec := ExportRecord{
			SchemaVersion: CurrentExportSchemaVersion,
			ID:            RecordID("0194f0c3-7a3b-7c1d-9e2f-0a1b2c3d4e5f"),
			Resource:      ResourceIdentity{Name: "gohan"},
			Signal:        SignalMetric,
			Metric: &MetricRecord{
				Instrument:  Instrument{Name: "gohan.model.ttft", Kind: InstrumentHistogram, Unit: "ms"},
				Source:      Source{Name: "github.com/victorzhuk/gohan"},
				Time:        start,
				Temporality: TemporalityCumulative,
				Value: MetricValue{Histogram: &HistogramValue{
					Count: 3, Bounds: []float64{5, 10, 25}, BucketCounts: []uint64{1, 1, 0, 1},
					Sum: 21.5, Min: &min, Max: &max,
				}},
			},
		}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, want := range []string{
			`"signal":"metric"`,
			`"kind":"histogram"`,
			`"unit":"ms"`,
			`"temporality":"cumulative"`,
			`"bucket_counts":[1,1,0,1]`,
			`"bounds":[5,10,25]`,
			`"min":4.5`,
		} {
			if !strings.Contains(string(data), want) {
				t.Fatalf("metric wire shape: %s not in %s", want, data)
			}
		}
	})

	t.Run("scalar arms", func(t *testing.T) {
		n := int64(42)
		f := 1.5
		intRec := ExportRecord{SchemaVersion: 1, Signal: SignalMetric, Metric: &MetricRecord{Value: MetricValue{Int: &n}}}
		floatRec := ExportRecord{SchemaVersion: 1, Signal: SignalMetric, Metric: &MetricRecord{Value: MetricValue{Float: &f}}}
		intData, err := json.Marshal(intRec)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(intData), `"int":42`) || strings.Contains(string(intData), `"float":`) {
			t.Fatalf("int arm: %s", intData)
		}
		floatData, err := json.Marshal(floatRec)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(floatData), `"float":1.5`) || strings.Contains(string(floatData), `"int":`) {
			t.Fatalf("float arm: %s", floatData)
		}
	})
}

func TestExportErrorUnwrap(t *testing.T) {
	cause := errors.New("boom")
	err := &ExportError{Destination: "otlp", Err: cause}
	if err.Error() == "" || err.Unwrap() != cause {
		t.Fatalf("ExportError unwrap: %v", err)
	}
}

func TestBoundedDiagnosticsBudget(t *testing.T) {
	var got []ExportDiagnostic
	sink := &captureDiagnostics{reports: func(ctx context.Context, d ExportDiagnostic) { got = append(got, d) }}
	bounded := BoundedDiagnostics(sink)
	ctx := context.Background()
	for range 100 {
		bounded.Report(ctx, ExportDiagnostic{Kind: DiagAdmissionRejected, Name: "session_id"})
	}
	bounded.Report(ctx, ExportDiagnostic{Kind: DiagDeliveryDropped, Name: "otlp"})
	if len(got) != 2 {
		t.Fatalf("cycle: want 2 first-rejection reports, got %d", len(got))
	}
	got = nil
	if _, ok := bounded.(interface {
		Flush(context.Context)
	}); !ok {
		t.Fatalf("bounded diagnostics must expose cycle end")
	}
	bounded.(interface{ Flush(context.Context) }).Flush(ctx)
	if len(got) != 2 {
		t.Fatalf("cycle end: want final reports for both keys, got %d", len(got))
	}
	for _, d := range got {
		if d.Dropped != 100 || d.Kind != DiagAdmissionRejected {
			if d.Dropped != 1 || d.Kind != DiagDeliveryDropped {
				t.Fatalf("final report: unexpected %+v", d)
			}
		}
	}
	got = nil
	bounded.(interface{ Shutdown(context.Context) }).Shutdown(ctx)
	if len(got) != 0 {
		t.Fatalf("second cycle end without growth must report nothing, got %d", len(got))
	}
}

type captureDiagnostics struct {
	reports func(ctx context.Context, d ExportDiagnostic)
}

func (c *captureDiagnostics) Report(ctx context.Context, d ExportDiagnostic) {
	c.reports(ctx, d)
}
