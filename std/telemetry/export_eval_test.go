package telemetry

import (
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func evalRecordFixture(id types.RecordID, signal types.ExportSignalKind) types.ExportRecord {
	return types.ExportRecord{
		SchemaVersion: types.CurrentExportSchemaVersion,
		ID:            id,
		Signal:        signal,
		Eval: &types.EvalRecord{
			EvaluationID: "eval-1",
			RunID:        "run-1",
			Results:      []types.EvalDimensionResult{},
			StartedAt:    time.Now().UTC(),
			CompletedAt:  time.Now().UTC(),
		},
	}
}

func TestValidateEvalRecord(t *testing.T) {
	t.Run("eval export record validation", func(t *testing.T) {
		t.Run("accepts eval signal with eval payload", func(t *testing.T) {
			rec := evalRecordFixture("rec-eval-ok", types.SignalEval)
			if err := validateRecord(&rec); err != nil {
				t.Fatalf("validateRecord err = %v, want nil", err)
			}
		})

		t.Run("rejects eval signal with span payload", func(t *testing.T) {
			rec := evalRecordFixture("rec-eval-span", types.SignalEval)
			rec.Span = &types.SpanRecord{}
			err := validateRecord(&rec)
			if err == nil {
				t.Fatal("validateRecord err = nil, want eval-signal-without-eval-payload error")
			}
			if !strings.Contains(err.Error(), "eval signal without eval payload") {
				t.Fatalf("err = %v, want eval payload mismatch message", err)
			}
		})

		t.Run("rejects eval signal with metric payload", func(t *testing.T) {
			rec := evalRecordFixture("rec-eval-metric", types.SignalEval)
			rec.Metric = &types.MetricRecord{}
			err := validateRecord(&rec)
			if err == nil {
				t.Fatal("validateRecord err = nil, want eval-signal-without-eval-payload error")
			}
			if !strings.Contains(err.Error(), "eval signal without eval payload") {
				t.Fatalf("err = %v, want eval payload mismatch message", err)
			}
		})

		t.Run("rejects span signal with eval payload", func(t *testing.T) {
			rec := evalRecordFixture("rec-span-eval", types.SignalSpan)
			rec.Eval = &types.EvalRecord{}
			err := validateRecord(&rec)
			if err == nil {
				t.Fatal("validateRecord err = nil, want span-signal-without-span-payload error")
			}
			if !strings.Contains(err.Error(), "span signal without span payload") {
				t.Fatalf("err = %v, want span payload mismatch message", err)
			}
		})

		t.Run("rejects metric signal with eval payload", func(t *testing.T) {
			rec := evalRecordFixture("rec-metric-eval", types.SignalMetric)
			rec.Eval = &types.EvalRecord{}
			err := validateRecord(&rec)
			if err == nil {
				t.Fatal("validateRecord err = nil, want metric-signal-without-metric-payload error")
			}
			if !strings.Contains(err.Error(), "metric signal without metric payload") {
				t.Fatalf("err = %v, want metric payload mismatch message", err)
			}
		})

		t.Run("hasPayload reflects payload presence", func(t *testing.T) {
			evalOnly := evalRecordFixture("rec-has-eval", types.SignalEval)
			if !hasPayload(evalOnly) {
				t.Fatal("hasPayload(eval payload) = false, want true")
			}

			empty := types.ExportRecord{
				SchemaVersion: types.CurrentExportSchemaVersion,
				ID:            "rec-empty",
				Signal:        types.SignalEval,
			}
			if hasPayload(empty) {
				t.Fatal("hasPayload(no payload) = true, want false")
			}
		})
	})
}
