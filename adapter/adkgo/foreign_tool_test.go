package adkgo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/conformance"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// adkForeign is an imported adk tool double with the runnable shape adk
// function tools implement: it counts Run calls and returns a scripted
// reply map.
type adkForeign struct {
	reply    map[string]any
	runCalls int
	lastCtx  context.Context
}

func (*adkForeign) Name() string        { return "echo" }
func (*adkForeign) Description() string { return "echoes its input" }
func (*adkForeign) IsLongRunning() bool { return false }

func (t *adkForeign) Run(ctx agent.Context, _ any) (map[string]any, error) {
	t.runCalls++
	t.lastCtx = ctx
	if t.reply != nil {
		return t.reply, nil
	}
	return map[string]any{"text": "1"}, nil
}

func foreignSpec() types.ToolSpec {
	return types.ToolSpec{
		Name:           "echo",
		Description:    "declared echo",
		Schema:         json.RawMessage(`{"type":"object"}`),
		Effect:         types.Idempotent,
		RequiredScopes: []string{"tool:echo"},
		Timeout:        3 * time.Second,
		Risk:           types.RiskMedium,
	}
}

func TestForeignToolUnderAdkgo(t *testing.T) {
	t.Run("engines.adkgo-foreign-tool", func(t *testing.T) {
		spec := foreignSpec()

		t.Run("identity_and_declaration", func(t *testing.T) {
			foreign := &adkForeign{}
			bridged, err := NewForeignTool(context.Background(), foreign, spec)
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			if got := bridged.Spec().Name; got != spec.Name || got != foreign.Name() {
				t.Fatalf("bridged name %q, want the declared and upstream %q", got, spec.Name)
			}
			s := bridged.Spec()
			if s.Effect != spec.Effect || s.Risk != spec.Risk || s.Timeout != spec.Timeout {
				t.Fatalf("governance fields drifted: effect %v risk %v timeout %v", s.Effect, s.Risk, s.Timeout)
			}
			if len(s.RequiredScopes) != 1 || s.RequiredScopes[0] != "tool:echo" {
				t.Fatalf("required scopes = %v, want the declared [tool:echo]", s.RequiredScopes)
			}

			// The same declared name is the name the governed run calls.
			foreign.reply = map[string]any{"text": "upstream-1"}
			h := newForeignHarness(t, bridged, nil)
			evs, err := h.Run(context.Background(), harnessInput())
			if err != nil {
				t.Fatalf("governed run: %v", err)
			}
			assertDone(t, evs)
			obs := h.Observed()
			if len(obs.ToolCalls) != 1 || obs.ToolCalls[0] != "echo" {
				t.Fatalf("tool calls %v, want exactly one call under the declared name echo", obs.ToolCalls)
			}
			if foreign.runCalls != 1 {
				t.Fatalf("imported tool ran %d times, want exactly once", foreign.runCalls)
			}
			if foreign.lastCtx == nil {
				t.Fatal("the imported tool never saw the runner-supplied invocation context")
			}
		})

		// The shipped bridge demotes every declared Trust to Untrusted:
		// an imported tool is never trusted by construction, so an
		// explicit Trusted resolves the same as the zero value.
		t.Run("trust_marking", func(t *testing.T) {
			foreign := &adkForeign{}
			bridged, err := NewForeignTool(context.Background(), foreign, spec)
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			if bridged.Spec().Trust != types.Untrusted {
				t.Fatalf("zero trust resolved to %v, want Untrusted", bridged.Spec().Trust)
			}
			trusted, err := NewForeignTool(context.Background(), foreign, types.ToolSpec{Name: "echo", Trust: types.Trusted})
			if err != nil {
				t.Fatalf("construct trusted: %v", err)
			}
			if trusted.Spec().Trust != types.Untrusted {
				t.Fatalf("explicit Trusted resolved to %v, want Untrusted like the zero value", trusted.Spec().Trust)
			}
			untrusted, err := NewForeignTool(context.Background(), foreign, types.ToolSpec{Name: "echo", Trust: types.Untrusted})
			if err != nil {
				t.Fatalf("construct untrusted: %v", err)
			}
			if untrusted.Spec().Trust != types.Untrusted {
				t.Fatalf("explicit Untrusted resolved to %v, want Untrusted", untrusted.Spec().Trust)
			}
		})

		t.Run("result_mapping_text", func(t *testing.T) {
			foreign := &adkForeign{reply: map[string]any{"text": "upstream-1"}}
			bridged, err := NewForeignTool(context.Background(), foreign, spec)
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			res, err := bridged.Call(runCtx(), json.RawMessage(`{"in":"1"}`))
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(res.Content) != 1 {
				t.Fatalf("text mode produced %d blocks, want one", len(res.Content))
			}
			txt, ok := res.Content[0].(types.Text)
			if !ok {
				t.Fatalf("text mode produced %T, want types.Text", res.Content[0])
			}
			want, err := json.Marshal(foreign.reply)
			if err != nil {
				t.Fatalf("marshal reply: %v", err)
			}
			if txt.Text != string(want) {
				t.Fatalf("text = %q, want the verbatim upstream reply %q", txt.Text, want)
			}

			// The same verbatim text reaches the next model turn under the
			// declared call identity on the governed run.
			h := newForeignHarness(t, bridged, func(req types.ModelRequest) error {
				for _, msg := range req.Messages {
					for _, block := range msg.Blocks {
						tr, ok := block.(types.ToolResult)
						if !ok || tr.ID != callUse("echo").ID {
							continue
						}
						if len(tr.Content) != 1 {
							return errors.New("carried tool result has no single block")
						}
						carried, ok := tr.Content[0].(types.Text)
						if !ok {
							return errors.New("carried tool result block is not text")
						}
						if carried.Text != string(want) {
							return errors.New("carried tool result text is not the verbatim upstream reply")
						}
						return nil
					}
				}
				return errors.New("next model turn carries no echo tool result")
			})
			evs, err := h.Run(context.Background(), harnessInput())
			if err != nil {
				t.Fatalf("governed run: %v", err)
			}
			assertDone(t, evs)
		})

		t.Run("result_mapping_envelope", func(t *testing.T) {
			foreign := &adkForeign{reply: map[string]any{
				"gohan_tool_result": map[string]any{
					"ID":      "call-1",
					"Content": []any{map[string]any{"Kind": "text", "Block": map[string]any{"Text": "structured"}}},
					"Outcome": float64(1),
				},
			}}
			envTool, err := NewForeignToolWithEnvelope(context.Background(), foreign, types.ToolSpec{Name: "echo"})
			if err != nil {
				t.Fatalf("construct envelope: %v", err)
			}
			res, err := envTool.Call(runCtx(), json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("envelope call: %v", err)
			}
			if res.ID != "call-1" || res.Outcome != types.Failed || len(res.Content) != 1 {
				t.Fatalf("envelope parsed to id %q outcome %v with %d blocks, want call-1 Failed with one block", res.ID, res.Outcome, len(res.Content))
			}
			blk, ok := res.Content[0].(types.Text)
			if !ok || blk.Text != "structured" {
				t.Fatalf("envelope block %T %+v, want the parsed text block", res.Content[0], res.Content[0])
			}
		})

		t.Run("no_synthesized_context", func(t *testing.T) {
			foreign := &adkForeign{}
			bridged, err := NewForeignTool(context.Background(), foreign, spec)
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			if _, err := bridged.Call(context.Background(), json.RawMessage(`{"in":"1"}`)); !errors.Is(err, ErrAdkContextRequired) {
				t.Fatalf("plain-context call err = %v, want ErrAdkContextRequired", err)
			}
			if foreign.runCalls != 0 {
				t.Fatalf("imported tool ran behind a refused ctx: %d calls", foreign.runCalls)
			}
		})

		t.Run("gate_ran_in_governed_run", func(t *testing.T) {
			foreign := &adkForeign{reply: map[string]any{"text": "1"}}
			bridged, err := NewForeignTool(context.Background(), foreign, spec)
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			h := newForeignHarness(t, bridged, nil)
			evs, err := h.Run(context.Background(), harnessInput())
			if err != nil {
				t.Fatalf("governed run: %v", err)
			}
			assertDone(t, evs)
			sawGate := false
			for _, step := range h.Observed().Steps {
				if step == conformance.StepGate {
					sawGate = true
				}
			}
			if !sawGate {
				t.Fatalf("steps %v: the gate never ran", h.Observed().Steps)
			}
		})
	})
}

// newForeignHarness drives the bridged foreign tool through the same
// harness the conformance suite uses: one scripted tool call, then a text
// answer, over rt.Start and the rt.Step loop to DoneStatus.
func newForeignHarness(t *testing.T, bridged types.Tool, assertFinal func(types.ModelRequest) error) *adkgoHarness {
	t.Helper()
	doneTurn := gohantest.Text("done")
	if assertFinal != nil {
		doneTurn = doneTurn.WithAssert(assertFinal)
	}
	h := &adkgoHarness{
		tel:   newRecordTel(),
		tools: map[string]types.Tool{},
		model: gohantest.NewScriptedModel(conformanceProfile,
			gohantest.ToolCall("echo", map[string]string{"in": "1"}),
			doneTurn,
		),
	}
	h.tools["echo"] = bridged
	return h
}

// assertDone requires a completed terminal Done event.
func assertDone(t *testing.T, evs []types.Event) {
	t.Helper()
	if len(evs) == 0 {
		t.Fatal("run produced no events")
	}
	done, ok := evs[len(evs)-1].(types.Done)
	if !ok {
		t.Fatalf("last event %T, want types.Done", evs[len(evs)-1])
	}
	if done.Reason != types.StopCompleted {
		t.Fatalf("done reason %q, want %q", done.Reason, types.StopCompleted)
	}
}
