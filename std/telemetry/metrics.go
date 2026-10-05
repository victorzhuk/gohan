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

// Metrics forwards counter and histogram emissions to a sink, carrying the
// release and variant stamps every gohan.* metric carries. Registrations
// are validated at Build, so a metric whose labels leave the allow-list is
// refused before anything is emitted.
type Metrics struct {
	tel        types.Telemetry
	release    string
	variant    string
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
	m := &Metrics{tel: tel, release: st.release, variant: st.variant, registered: make(map[string]Metric, len(metrics))}
	for _, metric := range metrics {
		if err := validateLabels(metric, st.tenant); err != nil {
			return nil, err
		}
		m.registered[metric.Name] = metric
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
		case !labelAllowlist[label]:
			return fmt.Errorf("gohan/telemetry: metric %s: label %s is not in the allow-list", metric.Name, label)
		}
	}
	return nil
}

// Count forwards a counter emission with the release and variant stamps.
func (m *Metrics) Count(ctx context.Context, name string, n int64, attrs ...types.Attr) {
	m.tel.Count(ctx, name, n, m.stamps(attrs)...)
}

// Record forwards a histogram or gauge observation with the stamps.
func (m *Metrics) Record(ctx context.Context, name string, v float64, attrs ...types.Attr) {
	m.tel.Record(ctx, name, v, m.stamps(attrs)...)
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
