package gohan_test

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	stdcontext "github.com/victorzhuk/gohan/std/context"
)

// projScenarioModel scripts the native turn: while it still owes tool
// calls it emits the next scripted use, then answers with text and stops.
// Every request is recorded like recordingModel does.
type projScenarioModel struct {
	mu       sync.Mutex
	requests []types.ModelRequest
	steps    []types.ToolUse
	emitted  int
}

func (m *projScenarioModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *projScenarioModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	var step *types.ToolUse
	if m.emitted < len(m.steps) {
		s := m.steps[m.emitted]
		step = &s
		m.emitted++
	}
	m.mu.Unlock()
	return func(yield func(types.ModelChunk, error) bool) {
		if step != nil {
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: step}, nil)
			yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
			return
		}
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

func (m *projScenarioModel) recordedRequests() []types.ModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]types.ModelRequest(nil), m.requests...)
}

// projScenarioTool is a SideEffect tool, so the native gate asks for it
// and the run suspends before executing.
type projScenarioTool struct{}

func (projScenarioTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "gate_tool", Effect: types.SideEffect}
}

func (projScenarioTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{Content: []types.Block{types.Text{Text: "gate ran"}}, Outcome: types.Succeeded}, nil
}

type projScenarioApprovalPolicy struct{}

func (projScenarioApprovalPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

// projScenarioStack builds the native stack the public way with the given
// session log, model, context policy and tools.
func projScenarioStack(t *testing.T, model *projScenarioModel, log stores.SessionLog, policy stores.ContextPolicy, tools ...types.Tool) (*gohan.Stack, gohan.Conversation) {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{
			SessionLog:  log,
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:  gohan.FlowRequest{Name: "chat"},
			Profile:  "native",
			Tools:    tools,
			Assemble: passthroughAssemble,
		}),
		gohan.WithContextPolicy(policy),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "chat",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		gohan.WithConversationApprovalPolicy(projScenarioApprovalPolicy{}),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return stack, conv
}

func projScenarioCtx() context.Context {
	return types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
}

// projScenarioSend drives one public Send turn and reports the suspension
// token the run left behind, if any.
func projScenarioSend(t *testing.T, conv gohan.Conversation, text string) types.ResumeToken {
	t.Helper()
	var token types.ResumeToken
	for ev, err := range conv.Send(projScenarioCtx(), "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: text}}}) {
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	return token
}

// projScenarioResume drives the public Resume over an approval decision.
func projScenarioResume(t *testing.T, conv gohan.Conversation, token types.ResumeToken) {
	t.Helper()
	for ev, err := range conv.Resume(projScenarioCtx(), token, gohan.Approve()) {
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		_ = ev
	}
}

// projResultEstimator counts message volume plus the Text blocks inside
// ToolResult content, so cleared results carry measurable cost.
type projResultEstimator struct{}

func (projResultEstimator) Estimate(req types.ModelRequest, _ types.Caps) int {
	n := 10 * len(req.Messages)
	var walk func(blocks []types.Block)
	walk = func(blocks []types.Block) {
		for _, b := range blocks {
			switch v := b.(type) {
			case types.Text:
				n += len(v.Text)
			case types.ToolResult:
				walk(v.Content)
			}
		}
	}
	for _, m := range req.Messages {
		walk(m.Blocks)
	}
	return n
}

// projClearProjection builds the std clearing projection with a fixed
// retention window, threshold and exclude list.
func projClearProjection(keep, clearAtLeast int, exclude ...string) stdcontext.ClearToolResults {
	c := stdcontext.NewClearToolResults(stdcontext.WithClearEstimator(projResultEstimator{}))
	c.Keep = keep
	c.ClearAtLeast = clearAtLeast
	c.Exclude = exclude
	return c
}

// projLoadedHistory loads s1 through the store under the test principal.
func projLoadedHistory(t *testing.T, log stores.SessionLog) stores.History {
	t.Helper()
	h, err := log.Load(projScenarioCtx(), "s1")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	return h
}

// projStoredResultText collects the stored result text per call ID.
func projStoredResultText(h stores.History) map[string]string {
	out := map[string]string{}
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if r, ok := b.(types.ToolResult); ok && len(r.Content) > 0 {
				if txt, ok := r.Content[0].(types.Text); ok {
					out[r.ID] = txt.Text
				}
			}
		}
	}
	return out
}

// projRequestResultText collects the result text per call ID inside one
// recorded model request.
func projRequestResultText(req types.ModelRequest) map[string]string {
	out := map[string]string{}
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if r, ok := b.(types.ToolResult); ok && len(r.Content) > 0 {
				if txt, ok := r.Content[0].(types.Text); ok {
					out[r.ID] = txt.Text
				}
			}
		}
	}
	return out
}

// projSeededCalls seeds one user turn plus n completed tool calls whose
// results carry a 100-byte payload, and returns the loaded history.
func projSeededCalls(t *testing.T, log stores.SessionLog, n int) stores.History {
	t.Helper()
	msgs := []types.Message{
		{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "seed"}}},
	}
	for i := range n {
		id := "call-" + string(rune('a'+i))
		msgs = append(msgs,
			types.Message{Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: id, Name: "worker_" + string(rune('a'+i))}}},
			types.Message{Role: types.RoleUser, Blocks: []types.Block{
				types.ToolResult{ID: id, Content: []types.Block{types.Text{Text: strings.Repeat("r", 100)}}, Outcome: types.Succeeded},
			}},
		)
	}
	var version int64
	if h, err := log.Load(projScenarioCtx(), "s1"); err == nil {
		version = h.Version
	}
	if _, err := log.Append(projScenarioCtx(), "s1", version, msgs...); err != nil {
		t.Fatalf("seed calls: %v", err)
	}
	return projLoadedHistory(t, log)
}

// projCountBlocks counts ToolUse and ToolResult blocks across a request.
func projCountBlocks(req types.ModelRequest) (uses, results int) {
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			switch b.(type) {
			case types.ToolUse:
				uses++
			case types.ToolResult:
				results++
			}
		}
	}
	return uses, results
}

func TestProjectionPublicPath(t *testing.T) {
	t.Run("context.projection-does-not-write", func(t *testing.T) {
		const marker = "[scenario marker]"
		model := &projScenarioModel{steps: []types.ToolUse{{ID: "call-gate", Name: "gate_tool", Args: json.RawMessage(`{}`)}}}
		log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
		_, conv := projScenarioStack(t, model, log,
			stores.ContextPolicy{Projections: []stores.Projection{clearingProjection{marker: marker}}},
			projScenarioTool{})
		seededHistory(t, log, "s1")

		token := projScenarioSend(t, conv, "turn")
		if token == "" {
			t.Fatal("Send did not suspend on the side-effect gate; Resume cannot be driven")
		}
		projScenarioResume(t, conv, token)

		reqs := model.recordedRequests()
		if len(reqs) < 2 {
			t.Fatalf("model invocations = %d, want at least one per entry point", len(reqs))
		}
		for i, req := range reqs {
			results := projRequestResultText(req)
			if results["use-read"] != marker {
				t.Fatalf("request %d carried use-read = %q, want the projection marker", i, results["use-read"])
			}
		}

		h := projLoadedHistory(t, log)
		got := projStoredResultText(h)
		if got["use-read"] != "read result" || got["use-write"] != "write result" || got["call-gate"] != "gate ran" {
			t.Fatalf("stored results = %v, want the full recorded results", got)
		}
		for _, msgs := range log.recorded() {
			for _, msg := range msgs {
				if lastText(msg) == marker {
					t.Fatal("an Append carried a projected message")
				}
			}
		}
	})

	t.Run("context.projection-deterministic", func(t *testing.T) {
		build := func() string {
			model := &projScenarioModel{}
			log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
			_, conv := projScenarioStack(t, model, log,
				stores.ContextPolicy{Projections: []stores.Projection{clearingProjection{marker: "[tool result cleared]"}}})
			seededHistory(t, log, "s1")
			projScenarioSend(t, conv, "turn")
			reqs := model.recordedRequests()
			if len(reqs) == 0 {
				t.Fatal("no model request recorded")
			}
			raw, err := json.Marshal(reqs[0].Messages)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			return string(raw)
		}
		if first, second := build(), build(); first != second {
			t.Fatalf("two assemblies of the same version and profile diverged:\n%s\n%s", first, second)
		}
	})

	t.Run("context.keep-last-n", func(t *testing.T) {
		model := &projScenarioModel{}
		log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
		_, conv := projScenarioStack(t, model, log,
			stores.ContextPolicy{Projections: []stores.Projection{projClearProjection(3, 0)}})
		projSeededCalls(t, log, 6)
		projScenarioSend(t, conv, "turn")

		reqs := model.recordedRequests()
		if len(reqs) == 0 {
			t.Fatal("no model request recorded")
		}
		results := projRequestResultText(reqs[0])
		uses, resultsN := projCountBlocks(reqs[0])
		if uses != 6 || resultsN != 6 {
			t.Fatalf("request blocks: %d uses, %d results, want 6 and 6", uses, resultsN)
		}
		for _, id := range []string{"call-d", "call-e", "call-f"} {
			if got := results[id]; got != strings.Repeat("r", 100) {
				t.Fatalf("recent result %s = %q, want intact", id, got)
			}
		}
		for _, id := range []string{"call-a", "call-b", "call-c"} {
			if got := results[id]; got != "[tool result cleared]" {
				t.Fatalf("older result %s = %q, want the placeholder", id, got)
			}
		}
	})

	t.Run("context.notes-excluded", func(t *testing.T) {
		model := &projScenarioModel{}
		log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
		_, conv := projScenarioStack(t, model, log,
			stores.ContextPolicy{Projections: []stores.Projection{projClearProjection(1, 0, "worker_a")}})
		seededHistory(t, log, "s1")
		projSeededCalls(t, log, 3)
		projScenarioSend(t, conv, "turn")

		reqs := model.recordedRequests()
		if len(reqs) == 0 {
			t.Fatal("no model request recorded")
		}
		results := projRequestResultText(reqs[0])
		if results["use-read"] != "read result" {
			t.Fatalf("notes_read result = %q, want intact whatever the exclude list", results["use-read"])
		}
		if results["use-write"] != "write result" {
			t.Fatalf("notes_write result = %q, want intact whatever the exclude list", results["use-write"])
		}
		if results["call-a"] != strings.Repeat("r", 100) {
			t.Fatalf("excluded worker_a result = %q, want intact", results["call-a"])
		}
		if results["call-b"] != "[tool result cleared]" {
			t.Fatalf("non-excluded clearing did not fire: call-b = %q", results["call-b"])
		}
	})

	t.Run("context.clear-at-least", func(t *testing.T) {
		drive := func(clearAtLeast int) map[string]string {
			model := &projScenarioModel{}
			log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
			_, conv := projScenarioStack(t, model, log,
				stores.ContextPolicy{Projections: []stores.Projection{projClearProjection(3, clearAtLeast)}})
			projSeededCalls(t, log, 6)
			projScenarioSend(t, conv, "turn")
			reqs := model.recordedRequests()
			if len(reqs) == 0 {
				t.Fatal("no model request recorded")
			}
			return projRequestResultText(reqs[0])
		}
		// Three cleared 100-byte results leave 3 * (100 - 21) = 237
		// estimated tokens on the table under projResultEstimator.
		if results := drive(238); results["call-a"] != strings.Repeat("r", 100) {
			t.Fatalf("clearing fired below ClearAtLeast: call-a = %q", results["call-a"])
		}
		if results := drive(237); results["call-a"] != "[tool result cleared]" {
			t.Fatalf("clearing refused at ClearAtLeast: call-a = %q", results["call-a"])
		}
	})
}
