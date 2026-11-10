package types

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ExportSignalKind names the signal an export record carries.
type ExportSignalKind string

const (
	SignalSpan   ExportSignalKind = "span"
	SignalMetric ExportSignalKind = "metric"
)

// RecordID is one record's stable identity. Producers mint it once with the
// stdlib uuid package (uuid.NewV7().String(), ADR-0058) and keep it across
// retries. IDs permit deduplication; they never promise exactly-once remote
// aggregation.
type RecordID string

// CurrentExportSchemaVersion is the export record shape version this
// release writes (ADR-0088 vocabulary; the wire field is schema_version).
const CurrentExportSchemaVersion = 1

// ResourceIdentity names the entity that produced a record.
type ResourceIdentity struct {
	Name  string `json:"name"`
	Attrs []Attr `json:"attrs,omitempty"`
}

// Source identifies the instrumentation scope and the convention schema a
// signal was produced under (schema identity).
type Source struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	SchemaURL string `json:"schema_url,omitempty"`
}

// SpanOutcome is a completed span's outcome.
type SpanOutcome string

const (
	SpanUnset SpanOutcome = "unset"
	SpanOK    SpanOutcome = "ok"
	SpanError SpanOutcome = "error"
)

// SpanRecord is one completed span. Completed spans cross the SDK export
// boundary in adapter/otel. They are never replayed through StartSpan.
type SpanRecord struct {
	TraceID  string      `json:"trace_id"`
	SpanID   string      `json:"span_id"`
	ParentID string      `json:"parent_id,omitempty"`
	Name     string      `json:"name"`
	Start    time.Time   `json:"start"`
	End      time.Time   `json:"end"`
	Attrs    []Attr      `json:"attrs,omitempty"`
	Outcome  SpanOutcome `json:"outcome"`
	Source   Source      `json:"source"`
}

// InstrumentKind classifies the instrument a metric record came from.
type InstrumentKind string

const (
	InstrumentCounter   InstrumentKind = "counter"
	InstrumentGauge     InstrumentKind = "gauge"
	InstrumentHistogram InstrumentKind = "histogram"
)

// Temporality declares the aggregation window of a metric record.
type Temporality string

const (
	TemporalityCumulative Temporality = "cumulative"
	TemporalityDelta      Temporality = "delta"
)

// Instrument is the descriptor of one metric instrument.
type Instrument struct {
	Name        string         `json:"name"`
	Kind        InstrumentKind `json:"kind"`
	Unit        string         `json:"unit,omitempty"`
	Description string         `json:"description,omitempty"`
	// Boundaries are histogram bucket upper bounds: increasing, +inf
	// implied, len(BucketCounts)==len(Boundaries)+1. nil means SDK default
	// aggregation.
	Boundaries []float64 `json:"boundaries,omitempty"`
}

// HistogramValue is one aggregated histogram data point. Bounds are bucket
// upper bounds; the +inf bucket is implied. len(BucketCounts) == len(Bounds)+1.
type HistogramValue struct {
	Count        uint64    `json:"count"`
	Bounds       []float64 `json:"bounds"`
	BucketCounts []uint64  `json:"bucket_counts"`
	Sum          float64   `json:"sum"`
	Min          *float64  `json:"min,omitempty"`
	Max          *float64  `json:"max,omitempty"`
}

// MetricValue is the typed payload of one metric data point. Exactly one
// field is set. Int carries int64 instrument values, Float float64 values.
type MetricValue struct {
	Int       *int64          `json:"int,omitempty"`
	Float     *float64        `json:"float,omitempty"`
	Histogram *HistogramValue `json:"histogram,omitempty"`
}

// MetricRecord is one aggregated metric data point delivered to one
// destination. Aggregation happens once per destination with the
// destination's declared temporality; the record preserves instrument kind,
// unit, observation time, dimensions and histogram boundaries.
type MetricRecord struct {
	Instrument  Instrument  `json:"instrument"`
	Source      Source      `json:"source"`
	Time        time.Time   `json:"time"`
	Start       time.Time   `json:"start,omitempty"`
	Temporality Temporality `json:"temporality"`
	Dimensions  []Attr      `json:"dimensions,omitempty"`
	Value       MetricValue `json:"value"`
}

// ExportRecord is one completed signal record crossing the export boundary.
// Exactly one of Span or Metric is non-nil and matches Signal.
type ExportRecord struct {
	SchemaVersion int              `json:"schema_version"`
	ID            RecordID         `json:"id"`
	Resource      ResourceIdentity `json:"resource"`
	Signal        ExportSignalKind `json:"signal"`
	Span          *SpanRecord      `json:"span,omitempty"`
	Metric        *MetricRecord    `json:"metric,omitempty"`
}

// MarshalJSON encodes Attr as {"key":...,"kind":...,"value":...}. kind is
// "string", "int64", "float64" or "bool" per Value's Go type. A Value
// outside the constructor kinds marshals as kind "string" with its fmt.Sprint
// form (mapAttrs fallback, adapter/otel/otel.go:100-117).
func (a Attr) MarshalJSON() ([]byte, error) {
	wire := marshalAttr{Key: a.Key}
	switch v := a.Value.(type) {
	case string:
		wire.Kind = "string"
		wire.Value = v
	case int64:
		wire.Kind = "int64"
		wire.Value = v
	case float64:
		wire.Kind = "float64"
		wire.Value = v
	case bool:
		wire.Kind = "bool"
		wire.Value = v
	default:
		wire.Kind = "string"
		wire.Value = fmt.Sprint(a.Value)
	}
	return json.Marshal(wire)
}

// UnmarshalJSON decodes the wire form and validates that kind agrees with
// value's JSON type. A mismatch returns an error naming the key and kind.
func (a *Attr) UnmarshalJSON(data []byte) error {
	var wire wireAttr
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	value, err := decodeAttrValue(wire.Key, wire.Kind, wire.Value)
	if err != nil {
		return err
	}
	a.Key = wire.Key
	a.Value = value
	return nil
}

type marshalAttr struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Value any    `json:"value"`
}

type wireAttr struct {
	Key   string         `json:"key"`
	Kind  string         `json:"kind"`
	Value jsontext.Value `json:"value"`
}

func decodeAttrValue(key, kind string, raw jsontext.Value) (any, error) {
	switch kind {
	case "string":
		if string(raw) == "null" {
			return nil, fmt.Errorf("attr %q kind %s: value is not a string", key, kind)
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("attr %q kind %s: value is not a string: %w", key, kind, err)
		}
		return s, nil
	case "int64":
		n, err := strconv.ParseInt(raw.String(), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("attr %q kind %s: value is not an int64", key, kind)
		}
		return n, nil
	case "float64":
		f, err := strconv.ParseFloat(raw.String(), 64)
		if err != nil {
			return nil, fmt.Errorf("attr %q kind %s: value is not a float64", key, kind)
		}
		return f, nil
	case "bool":
		switch string(raw) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("attr %q kind %s: value is not a bool", key, kind)
	default:
		return nil, fmt.Errorf("attr %q: unknown kind %q", key, kind)
	}
}

// OutcomeStatus is one record's acknowledged fate in one export attempt.
type OutcomeStatus string

const (
	OutcomeAccepted  OutcomeStatus = "accepted"
	OutcomeRetryable OutcomeStatus = "retryable"
	OutcomePermanent OutcomeStatus = "permanent"
	OutcomeAmbiguous OutcomeStatus = "ambiguous"
)

// RecordOutcome is one record's acknowledged fate in one batch. A record
// with no outcome row when Export returns is unresolved, not ambiguous.
type RecordOutcome struct {
	ID      RecordID      `json:"id"`
	Status  OutcomeStatus `json:"status"`
	Message string        `json:"message,omitempty"`
}

// ReplaySemantics declares how a destination treats record identity.
type ReplaySemantics string

const (
	ReplayNone      ReplaySemantics = "none"
	ReplayDedupByID ReplaySemantics = "dedup_by_id"
)

// DestinationCapabilities declares what a destination supports. Pipelines
// reject incompatible configurations before admission.
type DestinationCapabilities struct {
	Signals     []ExportSignalKind `json:"signals"`
	Replay      ReplaySemantics    `json:"replay"`
	Temporality Temporality        `json:"temporality"`
}

// ExportDestination receives export records. It is implemented by adapters
// and users, never by gohan core. Export is called synchronously per
// delivery attempt and must honor ctx. It MAY return fewer outcomes than
// records: every record without an outcome row is unresolved and is retried
// with its ID unchanged. On cancellation or deadline Export returns the
// outcomes established so far plus an error wrapping ctx.Err().
type ExportDestination interface {
	Name() string
	Capabilities() DestinationCapabilities
	Export(ctx context.Context, recs []ExportRecord) ([]RecordOutcome, error)
}

// RecordSink admits translated records into a delivery pipeline. The std
// pipeline implements it; adapter/otel pushes translated records through it.
type RecordSink interface {
	Ingest(ctx context.Context, dest string, recs []ExportRecord) error
	Flush(ctx context.Context) (UnresolvedWork, error)
	Shutdown(ctx context.Context) (UnresolvedWork, error)
}

// TelemetryLifecycle is the optional interface an owned telemetry pipeline
// implements, discovered by type assertion on a Telemetry value. The port
// method set never changes (docs/design/compatibility.md). Absent-assertion
// behaviour: the Telemetry is borrowed. Callers do not flush or shut it
// down; its providers stay with their owner.
type TelemetryLifecycle interface {
	Flush(ctx context.Context) (UnresolvedWork, error)
	Shutdown(ctx context.Context) (UnresolvedWork, error)
}

// GaugeTelemetry is the optional gauge surface. Absent-assertion behaviour:
// gauge observations are dropped and a wrapping layer holding Diagnostics
// reports DiagGaugeUnsupported once per instrument name per cycle.
type GaugeTelemetry interface {
	Gauge(ctx context.Context, name string, v float64, attrs ...Attr)
}

// UnresolvedWork counts records a Flush or Shutdown could not complete.
type UnresolvedWork struct {
	Records       int64            `json:"records"`
	ByDestination map[string]int64 `json:"by_destination,omitempty"`
}

// ExportLimits bounds admission. The zero value means the defaults below.
type ExportLimits struct {
	MaxQueueDepth      int
	MaxInstruments     int
	MaxDimensionValues int
	MaxSeries          int
}

const (
	DefaultMaxQueueDepth      = 2048
	DefaultMaxInstruments     = 256
	DefaultMaxDimensionValues = 1024
	DefaultMaxSeries          = 10000
)

// DiagnosticKind classifies an independent export diagnostic.
type DiagnosticKind string

const (
	DiagInstrumentRejected DiagnosticKind = "instrument_rejected"
	DiagGaugeUnsupported   DiagnosticKind = "gauge_unsupported"
	DiagAdmissionRejected  DiagnosticKind = "admission_rejected"
	DiagDeliveryDropped    DiagnosticKind = "delivery_dropped"
	DiagDeliveryAmbiguous  DiagnosticKind = "delivery_ambiguous"
)

// ExportDiagnostic is one diagnostic about the export path. Name is the
// instrument name, dimension key, content key or destination name. Dropped
// is the cumulative rejection count for (Kind, Name) in the current cycle.
type ExportDiagnostic struct {
	Kind    DiagnosticKind
	Name    string
	Err     error
	Dropped int64
}

// Diagnostics receives export diagnostics (slog or health reporting). A
// Report must not emit telemetry and must not enqueue export records.
type Diagnostics interface {
	Report(ctx context.Context, d ExportDiagnostic)
}

type boundedDiagnostics struct {
	inner  Diagnostics
	counts map[diagnosticKey]int64
	report map[diagnosticKey]bool
}

type diagnosticKey struct {
	kind DiagnosticKind
	name string
}

// BoundedDiagnostics wraps inner with the per-key report budget: per
// (Kind, Name) key at most one Report per cycle, emitted at the first
// rejection of the cycle; a cycle ends at the owning pipeline's Flush or
// Shutdown, which emits one final Report per key whose count grew.
func BoundedDiagnostics(inner Diagnostics) Diagnostics {
	return &boundedDiagnostics{
		inner:  inner,
		counts: make(map[diagnosticKey]int64),
		report: make(map[diagnosticKey]bool),
	}
}

func (b *boundedDiagnostics) Report(ctx context.Context, d ExportDiagnostic) {
	key := diagnosticKey{d.Kind, d.Name}
	b.counts[key]++
	if b.report[key] {
		return
	}
	b.report[key] = true
	b.inner.Report(ctx, d)
}

func (b *boundedDiagnostics) endCycle(ctx context.Context) {
	for key, count := range b.counts {
		if !b.report[key] || count == 0 {
			continue
		}
		b.inner.Report(ctx, ExportDiagnostic{Kind: key.kind, Name: key.name, Dropped: count})
	}
	clear(b.counts)
	clear(b.report)
}

// Flush ends the current cycle: one final Report per key whose count grew,
// then the counters reset for the next cycle. The owning pipeline calls it
// at its Flush.
func (b *boundedDiagnostics) Flush(ctx context.Context) {
	b.endCycle(ctx)
}

// Shutdown ends the current cycle like Flush. The owning pipeline calls it
// at its Shutdown.
func (b *boundedDiagnostics) Shutdown(ctx context.Context) {
	b.endCycle(ctx)
}

var (
	ErrExportUnsupportedSignal = errors.New("gohan: destination does not support this signal")
	ErrIncompatibleReplay      = errors.New("gohan: destination replay semantics below the requirement")
	ErrUnknownDestination      = errors.New("gohan: export destination not configured")
	ErrExportQueueFull         = errors.New("gohan: export queue is full")
	ErrInstrumentLimit         = errors.New("gohan: instrument limit reached")
	ErrInstrumentConflict      = errors.New("gohan: conflicting instrument declarations")
	ErrDimensionLimit          = errors.New("gohan: dimension value limit reached")
	ErrSeriesLimit             = errors.New("gohan: series limit reached")
	ErrExportShutdown          = errors.New("gohan: export pipeline is shut down")
)

// ExportError reports one destination's failure. Matched with
// errors.AsType[*ExportError]; Err unwraps to the cause.
type ExportError struct {
	Destination string
	Err         error
}

func (e *ExportError) Error() string {
	return "gohan: export to " + e.Destination + ": " + e.Err.Error()
}

func (e *ExportError) Unwrap() error {
	return e.Err
}
