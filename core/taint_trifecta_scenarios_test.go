package gohan_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type taintTrifectaMetric struct {
	name  string
	value int64
	attrs []types.Attr
}

type taintTrifectaTelemetry struct {
	metrics []taintTrifectaMetric
}

func (*taintTrifectaTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (m *taintTrifectaTelemetry) Count(_ context.Context, name string, value int64, attrs ...types.Attr) {
	m.metrics = append(m.metrics, taintTrifectaMetric{name: name, value: value, attrs: append([]types.Attr(nil), attrs...)})
}

func (*taintTrifectaTelemetry) Record(context.Context, string, float64, ...types.Attr) {}

func TestTaintTrifectaScenarios(t *testing.T) {
	model := func() *gohantest.ScriptedModel {
		return gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}})
	}
	tools := func(t *testing.T) []types.Tool {
		t.Helper()
		private, err := gohan.NewTool[struct{}, struct{}]("private_read", "", func(context.Context, struct{}) (struct{}, error) {
			return struct{}{}, nil
		}, gohan.WithPrivateRead())
		if err != nil {
			t.Fatal(err)
		}
		untrusted := gohantest.NewFakeTool("untrusted", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Trust = types.Untrusted
		}))
		exfil := gohantest.NewFakeTool("send_email", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Capabilities.Exfil = true
		}))
		return []types.Tool{private, untrusted, exfil}
	}
	flow := func(ts []types.Tool, hook types.TaintHook) gohan.Option {
		return gohan.WithNativeAgent(gohan.NativeSpec{
			Request:     gohan.FlowRequest{Name: "refunds"},
			Profile:     "native",
			Tools:       ts,
			TaintPolicy: hook,
		})
	}

	t.Run("taint.trifecta-fails-build", func(t *testing.T) {
		m := model()
		_, err := gohan.Build(gohan.WithModels(m), flow(tools(t), nil))
		var trifecta types.ErrTaintTrifecta
		if !errors.As(err, &trifecta) || trifecta.Flow != "refunds" || !trifecta.Exfil || !trifecta.PrivateRead || !trifecta.Untrusted {
			t.Fatalf("Build error = %v, want ErrTaintTrifecta for refunds with all legs true", err)
		}
		if calls := m.Requests(); len(calls) != 0 {
			t.Fatalf("provider calls = %d, want 0", len(calls))
		}
	})

	t.Run("taint.trifecta-with-policy", func(t *testing.T) {
		m := model()
		hook := types.TaintHook(func(context.Context, types.ToolSpec, jsontext.Value) (types.TaintAction, []types.ArgTaint) {
			return types.TaintAllow, nil
		})
		telemetry := &taintTrifectaTelemetry{}
		if _, err := gohan.Build(gohan.WithModels(m), gohan.WithTelemetry(telemetry), flow(tools(t), hook)); err != nil {
			t.Fatalf("Build with policy: %v", err)
		}
		for _, metric := range telemetry.metrics {
			if metric.name != "gohan.build.trifecta" {
				continue
			}
			for _, attr := range metric.attrs {
				if attr.Key == "flow" && attr.Value == "refunds" {
					if metric.value != 1 {
						t.Fatalf("trifecta count = %d, want 1", metric.value)
					}
					return
				}
			}
		}
		t.Fatal("trifecta counter has no measurement for flow refunds")
	})

	t.Run("taint.no-exfil", func(t *testing.T) {
		m := model()
		ts := tools(t)[:2]
		if _, err := gohan.Build(gohan.WithModels(m), flow(ts, nil)); err != nil {
			t.Fatalf("Build without exfil: %v", err)
		}
	})
}
