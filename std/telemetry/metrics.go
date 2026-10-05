package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// Metric names this package records on behalf of a run.
const (
	MetricTTFT         = "gohan.model.ttft"
	MetricTPOT         = "gohan.model.tpot"
	MetricLoopDetected = "gohan.loop.detected"
)

// Metric is one metric registration: its name and the label names it
// carries. Labels are the bare names of the allow-list below; the
// Convention layer maps them to a backend's vocabulary.
type Metric struct {
	Name   string
	Labels []string
}

// labelAllowlist is the metric-label allow-list. tenant is gated behind
// WithTenantLabel; the identity labels are admitted never, with or
// without the option.
var labelAllowlist = map[string]bool{
	"flow": true, "tool": true, "profile": true, "class": true,
	"release": true, "variant": true, "mode": true, "stage": true,
	"reason": true, "kind": true, "limit": true, "lifecycle": true,
	"source": true, "judge": true, "name": true, "target": true,
	"provider": true, "notes": true, "from": true, "to": true,
}

var forbiddenLabels = map[string]bool{
	"session_id": true, "run_id": true, "subject": true, "approver": true,
}

// labelCanonical maps the canonical gohan.* spellings a caller may hand in
// to the bare registration vocabulary. A canonical identity key never maps:
// the forbidden check runs on both spellings before this table is read.
var labelCanonical = map[string]string{
	types.KeyFlow:          "flow",
	types.KeyModeAttr:      "mode",
	types.KeyGuardStage:    "stage",
	types.KeySuspendReason: "reason",
	types.KeyRelease:       "release",
	types.KeyVariant:       "variant",
	types.KeyToolName:      "tool",
	types.KeyModelProfile:  "profile",
	types.KeyTenant:        "tenant",
	types.KeySessionID:     "session_id",
	types.KeyRunID:         "run_id",
	types.KeySubject:       "subject",
	types.KeyApprover:      "approver",
}

// Metrics forwards counter and histogram emissions to a sink, carrying the
// release and variant stamps every gohan.* metric carries. Registrations
// are validated at Build, so a metric whose labels leave the allow-list is
// refused before anything is emitted.
type Metrics struct {
	tel        types.Telemetry
	release    string
	variant    string
	tenant     bool
	registered map[string]Metric
}

// Option adjusts Build.
type Option func(*settings)

type settings struct {
	release string
	variant string
	tenant  bool
}

// WithRelease stamps every emission with the given release.
func WithRelease(r string) Option { return func(s *settings) { s.release = r } }

// WithVariant stamps every emission with the given variant.
func WithVariant(v string) Option { return func(s *settings) { s.variant = v } }

// WithTenantLabel admits the tenant label on registered metrics. Without
// it a metric carrying tenant fails Build.
func WithTenantLabel() Option { return func(s *settings) { s.tenant = true } }

// Build validates the metric registrations against the label allow-list
// and returns the forwarder. The error names the metric and the offending
// label, so a rejected registration is readable at the call site.
func Build(tel types.Telemetry, metrics []Metric, opts ...Option) (*Metrics, error) {
	if tel == nil {
		return nil, fmt.Errorf("gohan/telemetry: Build needs a telemetry sink")
	}
	var st settings
	for _, opt := range opts {
		opt(&st)
	}
	m := &Metrics{tel: tel, release: st.release, variant: st.variant, tenant: st.tenant, registered: make(map[string]Metric, len(metrics))}
	for _, metric := range metrics {
		if err := validateLabels(metric, st.tenant); err != nil {
			return nil, err
		}
		copied := Metric{Name: metric.Name, Labels: append([]string(nil), metric.Labels...)}
		m.registered[metric.Name] = copied
	}
	return m, nil
}

func validateLabels(metric Metric, tenant bool) error {
	for _, label := range metric.Labels {
		switch {
		case forbiddenLabels[label]:
			return fmt.Errorf("gohan/telemetry: metric %s: label %s is never admitted", metric.Name, label)
		case label == "tenant" && !tenant:
			return fmt.Errorf("gohan/telemetry: metric %s: label tenant needs WithTenantLabel", metric.Name)
		case label != "tenant" && !labelAllowlist[label]:
			return fmt.Errorf("gohan/telemetry: metric %s: label %s is not in the allow-list", metric.Name, label)
		}
	}
	return nil
}

// StartSpan forwards to the sink untouched: metric label registrations
// govern Count and Record, not spans.
func (m *Metrics) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	return m.tel.StartSpan(ctx, name, attrs...)
}

// Count enforces the registration at emission: an unregistered metric is
// dropped, a label outside the metric's registration is omitted, identity
// attributes are dropped under either spelling, and the package stamps
// override any caller release or variant.
func (m *Metrics) Count(ctx context.Context, name string, n int64, attrs ...types.Attr) {
	out := m.emit(name, attrs)
	if out == nil {
		return
	}
	m.tel.Count(ctx, name, n, out...)
}

// Record enforces the registration the same way Count does.
func (m *Metrics) Record(ctx context.Context, name string, v float64, attrs ...types.Attr) {
	out := m.emit(name, attrs)
	if out == nil {
		return
	}
	m.tel.Record(ctx, name, v, out...)
}

// emit normalizes, filters and de-duplicates the caller attributes against
// the metric's registration, then appends the package stamps. A nil slice
// with a nil registration means the metric is unknown and nothing emits.
func (m *Metrics) emit(name string, attrs []types.Attr) []types.Attr {
	reg, ok := m.registered[name]
	if !ok {
		return nil
	}
	registered := make(map[string]bool, len(reg.Labels))
	for _, l := range reg.Labels {
		registered[l] = true
	}
	admitted := make([]types.Attr, 0, len(attrs))
	for _, a := range attrs {
		bare := normalizeLabel(a.Key)
		if forbiddenLabels[bare] {
			continue
		}
		if bare == "tenant" && !m.tenant {
			continue
		}
		if (bare == "release" && m.release != "") || (bare == "variant" && m.variant != "") {
			continue
		}
		if !registered[bare] {
			continue
		}
		// The last admissible caller value for one normalized label wins.
		admitted = append(admitted, types.Attr{Key: bare, Value: a.Value})
	}
	out := make([]types.Attr, 0, len(admitted)+2)
	idx := make(map[string]int, len(admitted))
	for _, a := range admitted {
		if i, ok := idx[a.Key]; ok {
			out[i] = a
			continue
		}
		idx[a.Key] = len(out)
		out = append(out, a)
	}
	return m.stamps(out)
}

// normalizeLabel reduces a canonical gohan.* spelling to the bare
// registration vocabulary; a bare name passes through unchanged.
func normalizeLabel(key string) string {
	if bare, ok := labelCanonical[key]; ok {
		return bare
	}
	return key
}

func (m *Metrics) stamps(attrs []types.Attr) []types.Attr {
	out := make([]types.Attr, 0, len(attrs)+2)
	if m.release != "" {
		out = append(out, types.String(types.KeyRelease, m.release))
	}
	if m.variant != "" {
		out = append(out, types.String(types.KeyVariant, m.variant))
	}
	return append(out, attrs...)
}

// ObserveStream records TTFT and TPOT in milliseconds from one model
// stream's own timing: start is when the call began, tokens are the
// arrival times of the streamed tokens in order. TTFT is the first
// token's latency; TPOT is the mean per-token pace after it. A stream
// with no tokens records nothing.
func (m *Metrics) ObserveStream(ctx context.Context, start time.Time, tokens ...time.Time) {
	if len(tokens) == 0 {
		return
	}
	m.Record(ctx, MetricTTFT, ms(tokens[0].Sub(start)))
	if len(tokens) == 1 {
		return
	}
	var sum time.Duration
	for i := 1; i < len(tokens); i++ {
		sum += tokens[i].Sub(tokens[i-1])
	}
	m.Record(ctx, MetricTPOT, ms(sum/time.Duration(len(tokens)-1)))
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// LoopDetector counts a repeated identical tool call past a threshold.
// A call is identical when the tool name and the serialized arguments
// match a previous call exactly; the detector counts gohan.loop.detected
// once per repeating call signature.
type LoopDetector struct {
	m         *Metrics
	threshold int

	mu    sync.Mutex
	seen  map[string]int
	fired map[string]bool
}

// NewLoopDetector returns a detector that fires when a signature has been
// observed more than threshold times; the threshold is the number of
// repeats admitted before detection. A threshold below one is clamped to
// one.
func NewLoopDetector(m *Metrics, threshold int) *LoopDetector {
	if threshold < 1 {
		threshold = 1
	}
	return &LoopDetector{m: m, threshold: threshold, seen: make(map[string]int), fired: make(map[string]bool)}
}

// Observe records one tool call with its serialized arguments.
func (d *LoopDetector) Observe(ctx context.Context, tool, args string) {
	key := tool + "\x00" + args
	d.mu.Lock()
	d.seen[key]++
	fire := d.seen[key] > d.threshold && !d.fired[key]
	if fire {
		d.fired[key] = true
	}
	d.mu.Unlock()
	if fire {
		d.m.Count(ctx, MetricLoopDetected, 1, types.String(types.KeyToolName, tool))
	}
}
