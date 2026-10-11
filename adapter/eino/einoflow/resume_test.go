package einoflow

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	einoagent "github.com/victorzhuk/gohan/adapter/eino"
	gohanruntime "github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// compileGraphText compiles a model-only graph: string in, string out.
func compileGraphText(t *testing.T, ctx context.Context, m types.Model) compose.Runnable[string, string] {
	t.Helper()
	g := compose.NewGraph[string, string]()
	in := compose.InvokableLambda(func(_ context.Context, s string) ([]*schema.Message, error) {
		return []*schema.Message{{Role: schema.User, Content: s}}, nil
	})
	if err := g.AddLambdaNode("in", in); err != nil {
		t.Fatalf("AddLambdaNode: %v", err)
	}
	if err := g.AddChatModelNode("model", ModelNode(m)); err != nil {
		t.Fatalf("AddChatModelNode: %v", err)
	}
	out := compose.InvokableLambda(func(_ context.Context, m *schema.Message) (string, error) {
		return m.Content, nil
	})
	if err := g.AddLambdaNode("out", out); err != nil {
		t.Fatalf("AddLambdaNode: %v", err)
	}
	if err := g.AddEdge(compose.START, "in"); err != nil {
		t.Fatalf("AddEdge start: %v", err)
	}
	if err := g.AddEdge("in", "model"); err != nil {
		t.Fatalf("AddEdge in: %v", err)
	}
	if err := g.AddEdge("model", "out"); err != nil {
		t.Fatalf("AddEdge model: %v", err)
	}
	if err := g.AddEdge("out", compose.END); err != nil {
		t.Fatalf("AddEdge end: %v", err)
	}
	r, err := g.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return r
}

// TestNativeResume binds the stores resume scenarios on the eino flows at
// the decision level: the runtime declares the flow kind and the backend
// version, and PlanResume decides before any token is spent. The
// conversation-level leg drives through gohan Resume and lands with the
// core resume wiring (task 2.1).
func TestNativeResume(t *testing.T) {
	ctx := context.Background()

	t.Run("stores.native-checkpoint-after-adapter-upgrade", func(t *testing.T) {
		rt, err := einoagent.New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		cp := stores.Checkpoint{SchemaVersion: stores.CurrentSchemaVersion, BackendVersion: rt.BackendVersion()}
		plan, err := stores.PlanResume(cp, rt.FlowKind(), "eino-upgraded")
		if err != nil {
			t.Fatalf("PlanResume across adapter upgrade: %v", err)
		}
		if plan != stores.ResumeReplay {
			t.Fatalf("plan = %d, want ResumeReplay", plan)
		}
	})

	t.Run("stores.graph-checkpoint-incompatible", func(t *testing.T) {
		rt := graphRuntime[string, string]{}
		cp := stores.Checkpoint{SchemaVersion: stores.CurrentSchemaVersion, BackendVersion: "eino-graph-v0"}
		plan, err := stores.PlanResume(cp, rt.FlowKind(), rt.BackendVersion())
		if !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("PlanResume across adapter upgrade: got %v, want ErrCheckpointIncompatible", err)
		}
		if plan != stores.ResumeNative {
			t.Errorf("plan = %d, want the zero plan with the refusal error", plan)
		}
	})

	t.Run("same_version_resumes_natively", func(t *testing.T) {
		m := &scriptGraphModel{chunks: []types.ModelChunk{{Kind: types.DeltaText, Delta: "ok"}}}
		r := compileGraphText(t, ctx, m)
		fl := FromRunnable[string, string](nil, r)
		flw, ok := fl.(*graphFlow[string, string])
		if !ok {
			t.Fatalf("FromRunnable returned %T", fl)
		}
		run := &graphRun[string, string]{f: flw, in: "hi"}
		rt := graphRuntime[string, string]{run: run}

		cp := stores.Checkpoint{SchemaVersion: stores.CurrentSchemaVersion, BackendVersion: rt.BackendVersion()}
		plan, err := stores.PlanResume(cp, rt.FlowKind(), rt.BackendVersion())
		if err != nil {
			t.Fatalf("PlanResume at the same version: %v", err)
		}
		if plan != stores.ResumeNative {
			t.Fatalf("plan = %d, want ResumeNative", plan)
		}

		st, err := rt.Start(ctx, gohanruntime.AgentRun{})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, status, err := rt.Step(ctx, st); err != nil || status != gohanruntime.DoneStatus {
			t.Fatalf("first run: status %d err %v, want Done", status, err)
		}
		// The re-entry Step after a native resume re-drives the saved
		// state without a refusal and without re-executing the run.
		if _, _, status, err := rt.Step(ctx, st); err != nil || status != gohanruntime.DoneStatus {
			t.Fatalf("resume step: status %d err %v, want Done", status, err)
		}
	})
}
