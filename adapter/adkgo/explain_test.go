package adkgo

import (
	"context"
	"iter"
	"slices"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

type explainProfileModel struct{}

func (*explainProfileModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "primary", Version: "v1"}
}

func (*explainProfileModel) Generate(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(func(types.ModelChunk, error) bool) {}
}

func TestExplain(t *testing.T) {
	t.Run("new_succeeds_and_names_adkgo", func(t *testing.T) {
		rt, err := New(context.Background())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if rt.Name() != "adkgo" {
			t.Errorf("name = %q, want adkgo", rt.Name())
		}
	})

	t.Run("adkgo_reports_turn_granularity", func(t *testing.T) {
		s, err := gohan.Build(
			gohan.WithModels(&explainProfileModel{}),
			gohan.WithLimits("agent", types.InteractiveLimits),
			gohan.WithForeignAgent(gohan.ForeignSpec{
				Request: gohan.FlowRequest{Name: "agent"},
				Profile: "primary",
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{Messages: slices.Clone(in.History)}, nil
				},
				NewRuntime: func() runtime.Runtime {
					rt, err := New(context.Background())
					if err != nil {
						return nil
					}
					return rt
				},
			}),
		)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		ex := s.Explain("agent")
		if ex.Flow != "agent" {
			t.Fatalf("flow = %q, want agent", ex.Flow)
		}
		if ex.Granularity != "turn" {
			t.Errorf("granularity = %q, want turn", ex.Granularity)
		}
	})
}
