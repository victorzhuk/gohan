package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

const providerSearchName = "web_search"

func TestProviderToolRegistered(t *testing.T) {
	t.Run("tools.provider-tool-registered", func(t *testing.T) {
		var calls int
		search := &countingProviderTool{spec: types.ToolSpec{
			Name:         providerSearchName,
			Effect:       types.ReadOnly,
			Trust:        types.Untrusted,
			Executor:     types.ByProvider,
			Capabilities: types.Capabilities{Exfil: true},
		}, calls: &calls}
		p := profile("small", true)
		p.Caps.ProviderTools = map[string]types.ProviderToolCap{
			providerSearchName: {Approval: true},
		}
		spec := nativeSpec("browse", "small")
		spec.Tools = []types.Tool{search}
		spec.Assemble = func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Tools: []types.ToolSpec{search.spec}}, nil
		}
		s, err := Build(WithModels(fakeModel{profile: p}), WithNativeAgent(spec))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		cfg, ok := s.resolvedNative("browse")
		if !ok {
			t.Fatal("flow not resolved")
		}
		got := cfg.specs[0]
		if got.Executor != types.ByProvider {
			t.Errorf("Executor = %v, want ByProvider", got.Executor)
		}
		if got.Effect != types.ReadOnly || got.Trust != types.Untrusted || !got.Capabilities.Exfil {
			t.Fatalf("spec = %+v, want ReadOnly Untrusted Exfil from the spec, not a provider block", got)
		}
		if calls != 0 {
			t.Fatalf("Tool.Call invoked %d times at registration", calls)
		}
		ex := s.Explain("browse")
		var listed bool
		for _, ti := range ex.Tools {
			if ti.Name != providerSearchName {
				continue
			}
			listed = true
			if ti.Executor != types.ByProvider {
				t.Errorf("Explain Executor = %v, want ByProvider", ti.Executor)
			}
		}
		if !listed {
			t.Fatalf("Explain tools = %v, missing %q", ex.Tools, providerSearchName)
		}
		if calls != 0 {
			t.Fatalf("Tool.Call invoked %d times by Explain", calls)
		}
	})
}

func TestProviderToolUnsupported(t *testing.T) {
	t.Run("providerToolUnsupportedIncrements", func(t *testing.T) {
		tel := &recTelemetry{}
		spec := nativeSpec("run", "small")
		spec.Tools = []types.Tool{&countingProviderTool{spec: types.ToolSpec{
			Name:     "code_exec",
			Effect:   types.SideEffect,
			Trust:    types.Untrusted,
			Executor: types.ByProvider,
		}}}
		_, err := Build(
			WithModels(fakeModel{profile: profile("small", true)}),
			WithTelemetry(tel),
			WithNativeAgent(spec),
		)
		if !errors.Is(err, ErrProviderToolUnsupported) {
			t.Fatalf("err = %v, want ErrProviderToolUnsupported", err)
		}
		if len(tel.counts) != 1 {
			t.Fatalf("counts = %v, want one gohan.tool.provider_unsupported", tel.counts)
		}
		c := tel.counts[0]
		if c.name != "gohan.tool.provider_unsupported" || c.n != 1 {
			t.Fatalf("count = %s %d, want gohan.tool.provider_unsupported at 1", c.name, c.n)
		}
		if !c.has("tool", "code_exec") {
			t.Fatalf("attrs = %v, want tool=code_exec", c.attrs)
		}
	})
}

type countingProviderTool struct {
	spec  types.ToolSpec
	calls *int
}

func (t *countingProviderTool) Spec() types.ToolSpec { return t.spec }

func (t *countingProviderTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	*t.calls++
	return types.ToolResult{}, nil
}

type recCount struct {
	name  string
	n     int64
	attrs []types.Attr
}

func (c recCount) has(key, value string) bool {
	for _, a := range c.attrs {
		if a.Key == key && a.Value == value {
			return true
		}
	}
	return false
}

type recTelemetry struct {
	counts []recCount
}

func (t *recTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *recTelemetry) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	t.counts = append(t.counts, recCount{name: name, n: n, attrs: attrs})
}

func (t *recTelemetry) Record(_ context.Context, _ string, _ float64, _ ...types.Attr) {}

var _ types.Telemetry = (*recTelemetry)(nil)
