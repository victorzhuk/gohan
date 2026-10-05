package telemetry

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestDecorate(t *testing.T) {
	newSink := func() (*recordingSink, types.Telemetry) {
		sink := &recordingSink{}
		tel, err := Decorate(sink, GenAI())
		if err != nil {
			t.Fatalf("decorate: %v", err)
		}
		return sink, tel
	}

	t.Run("decorate-nil-sink", func(t *testing.T) {
		if _, err := Decorate(nil, GenAI()); err == nil {
			t.Fatal("Decorate accepted a nil sink")
		}
	})

	t.Run("decorate-maps-initial-and-final-attrs", func(t *testing.T) {
		sink, tel := newSink()
		sctx, end := tel.StartSpan(context.Background(), SpanNameTest,
			types.String(types.KeyModelProfile, "echo"),
			types.String("gohan.content.input", "secret"),
		)
		_ = sctx
		end(types.String(types.KeyModelVersion, "v9"),
			types.String("gohan.content.output", "secret"),
		)
		if len(sink.spans) != 1 {
			t.Fatalf("spans %d, want 1", len(sink.spans))
		}
		attrs := sink.spans[0].attrs
		for _, a := range attrs {
			if isContentKey(a.Key) {
				t.Fatalf("content key %s reached the sink", a.Key)
			}
		}
		if attrValue(t, attrs, "gen_ai.request.model") != "echo" {
			t.Fatalf("initial profile not mapped: %v", attrs)
		}
		if attrValue(t, sink.spans[0].final, "gen_ai.response.model") != "v9" {
			t.Fatalf("final version not mapped: %v", sink.spans[0].final)
		}
	})

	t.Run("decorate-passes-sink-context-through", func(t *testing.T) {
		sink, tel := newSink()
		ctx := context.WithValue(context.Background(), decorateProbe{}, "x")
		sctx, end := tel.StartSpan(ctx, SpanNameTest)
		end()
		if sctx != ctx || sink.spanCtxs[0] != ctx {
			t.Fatal("Decorate replaced the sink's derived context")
		}
	})

	t.Run("unregistered-metric-emits-nothing", func(t *testing.T) {
		sink := &recordingSink{}
		tel, err := Decorate(mustBuild(t, sink), GenAI())
		if err != nil {
			t.Fatalf("decorate: %v", err)
		}
		tel.Count(context.Background(), "gohan.not.registered", 1, types.String("flow", "trip"))
		tel.Record(context.Background(), "gohan.not.registered", 2.0)
		if len(sink.counts) != 0 || len(sink.records) != 0 {
			t.Fatal("unregistered metric reached the sink")
		}
	})

	t.Run("forbidden-label-omitted-under-both-spellings", func(t *testing.T) {
		sink := &recordingSink{}
		tel, err := Decorate(mustBuild(t, sink), GenAI())
		if err != nil {
			t.Fatalf("decorate: %v", err)
		}
		tel.Count(context.Background(), MetricLoopDetected, 1,
			types.String(types.KeySessionID, "s1"),
			types.String("session_id", "s2"),
			types.String(types.KeyRunID, "r1"),
			types.String(types.KeySubject, "u"),
			types.String(types.KeyApprover, "a"),
		)
		c := sink.counts[0]
		if len(c.attrs) != 2 {
			t.Fatalf("attrs %v, want only the two stamps", c.attrs)
		}
		for _, a := range c.attrs {
			if isIdentityKey(a.Key) {
				t.Fatalf("identity attr %s reached the sink", a.Key)
			}
		}
	})

	t.Run("unregistered-label-omitted-and-duplicates-keep-last", func(t *testing.T) {
		sink := &recordingSink{}
		tel, err := Decorate(mustBuild(t, sink), Convention{})
		if err != nil {
			t.Fatalf("decorate: %v", err)
		}
		tel.Count(context.Background(), MetricLoopDetected, 1,
			types.String("flow", "trip"),
			types.String("tool", "first"),
			types.String(types.KeyToolName, "second"),
		)
		c := sink.counts[0]
		if len(c.attrs) != 3 {
			t.Fatalf("attrs %v, want tool twice plus two stamps", c.attrs)
		}
		if attrValue(t, c.attrs, "tool") != "second" {
			t.Fatalf("tool = %q, want the last admissible value", attrValue(t, c.attrs, "tool"))
		}
		for _, a := range c.attrs {
			if a.Key == "flow" {
				t.Fatal("unregistered label reached the sink")
			}
		}
	})

	t.Run("stamps-override-caller-spellings", func(t *testing.T) {
		sink := &recordingSink{}
		m, err := Build(sink, []Metric{{Name: MetricLoopDetected, Labels: []string{"tool", "release", "variant"}}},
			WithRelease("r7"), WithVariant("b"))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		m.Count(context.Background(), MetricLoopDetected, 1,
			types.String(types.KeyRelease, "caller"),
			types.String("variant", "caller"),
		)
		c := sink.counts[0]
		releases := 0
		for _, a := range c.attrs {
			if a.Key == types.KeyRelease {
				releases++
				if a.Value != "r7" {
					t.Fatalf("release = %v, want the package stamp", a.Value)
				}
			}
			if a.Key == types.KeyVariant && a.Value != "b" {
				t.Fatalf("variant = %v, want the package stamp", a.Value)
			}
		}
		if releases != 1 {
			t.Fatalf("release emitted %d times, want once", releases)
		}
	})

	t.Run("tenant-needs-the-option", func(t *testing.T) {
		sink := &recordingSink{}
		_, err := Build(sink, []Metric{{Name: "gohan.tenant.calls", Labels: []string{"tenant"}}})
		if err == nil {
			t.Fatal("Build admitted tenant without WithTenantLabel")
		}
		m, err := Build(sink, []Metric{{Name: "gohan.tenant.calls", Labels: []string{"tenant"}}}, WithTenantLabel())
		if err != nil {
			t.Fatalf("Build refused tenant with the option: %v", err)
		}
		m.Count(context.Background(), "gohan.tenant.calls", 1, types.String(types.KeyTenant, "t1"))
		if attrValue(t, sink.counts[0].attrs, "tenant") != "t1" {
			t.Fatal("tenant label missing at emission")
		}
	})

	t.Run("caller-mutation-cannot-change-enforcement", func(t *testing.T) {
		labels := []string{"tool", "release", "variant"}
		m, err := Build(&recordingSink{}, []Metric{{Name: MetricLoopDetected, Labels: labels}})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		labels[0] = "session_id"
		m.Count(context.Background(), MetricLoopDetected, 1, types.String("tool", "search"))
	})
}

type decorateProbe struct{}

type spanObservation struct {
	attrs []types.Attr
	final []types.Attr
}

func isIdentityKey(key string) bool {
	switch key {
	case "session_id", "run_id", "subject", "approver",
		"gen_ai.conversation.id", types.KeyRunID, types.KeySubject, types.KeyApprover:
		return true
	}
	return false
}

// SpanNameTest is a span name no convention maps.
const SpanNameTest = "test.span"
