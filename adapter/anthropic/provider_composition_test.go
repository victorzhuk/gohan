package anthropic

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type compTurn struct {
	events  []string
	pauseAt int
	gate    <-chan struct{}
}

type compFixture struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
	turns  []compTurn
}

func newCompFixture(t *testing.T, turns ...compTurn) *compFixture {
	t.Helper()
	fx := &compFixture{turns: turns}
	fx.srv = httptest.NewServer(http.HandlerFunc(fx.serve))
	t.Cleanup(fx.srv.Close)
	return fx
}

func (fx *compFixture) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	fx.mu.Lock()
	idx := len(fx.bodies)
	fx.bodies = append(fx.bodies, body)
	fx.mu.Unlock()
	turn := compTurn{}
	if idx < len(fx.turns) {
		turn = fx.turns[idx]
	}
	events := turn.events
	if events == nil {
		events = compTextEvents("done")
	}
	cut := len(events)
	if turn.gate != nil {
		cut = turn.pauseAt
	}
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	compWriteEvents(w, flusher, events[:cut])
	if turn.gate != nil {
		select {
		case <-turn.gate:
		case <-r.Context().Done():
			return
		}
	}
	compWriteEvents(w, flusher, events[cut:])
}

func compWriteEvents(w io.Writer, flusher http.Flusher, events []string) {
	for _, ev := range events {
		_, _ = io.WriteString(w, ev)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (fx *compFixture) request(t *testing.T, i int) map[string]any {
	t.Helper()
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if i >= len(fx.bodies) {
		t.Fatalf("recorded %d requests, want request %d", len(fx.bodies), i+1)
	}
	return fx.bodies[i]
}

func compMsgStart() string {
	return event("message_start", `{"type":"message_start","message":{"model":"claude-composition-1","usage":{"input_tokens":10}}}`)
}

func compMsgDelta(stop string) string {
	return event("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":5}}`, stop))
}

func compStop() string {
	return event("message_stop", `{"type":"message_stop"}`)
}

func compToolStart(index int, id, name string) string {
	return event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":%q}}`, index, id, name))
}

func compBlockStop(index int) string {
	return event("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
}

func compArgsDelta(index int, frag string) string {
	return event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%q}}`, index, frag))
}

func compTextEvents(text string) []string {
	return []string{
		compMsgStart(),
		event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		textDelta(text),
		compMsgDelta("end_turn"),
		compStop(),
	}
}

type compRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *compRecorder) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "search", Description: "recorded search tool", Effect: types.ReadOnly}
}

func (r *compRecorder) Call(_ context.Context, args jsontext.Value) (types.ToolResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, string(args))
	return types.ToolResult{Content: []types.Block{types.Text{Text: "found"}}, Outcome: types.Succeeded}, nil
}

func (r *compRecorder) effects() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func compModel(t *testing.T, fx *compFixture) *Model {
	t.Helper()
	m, err := New(Config{
		Endpoint: fx.srv.URL,
		APIKey:   "test" + "-key",
		KeyID:    "test-key-1",
		Profile: types.ModelProfile{
			Name:    "claude-composition",
			Version: "claude-composition-1",
			Caps: types.Caps{
				Tools:            true,
				Constrained:      true,
				Streaming:        true,
				Cache:            types.CacheExplicit,
				CacheBreakpoints: 2,
			},
		},
		Client: fx.srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func compAssemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	msgs := append(append([]types.Message{}, in.History...), in.Input...)
	return types.ModelRequest{Messages: msgs, Tools: in.Tools}, nil
}

func compForcedAssemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	msgs := append(append([]types.Message{}, in.History...), in.Input...)
	return types.ModelRequest{
		Messages: msgs,
		Tools:    in.Tools,
		Options: types.ModelOptions{
			ResponseSchema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`),
		},
	}, nil
}

func compStack(t *testing.T, m *Model, recorder types.Tool, assemble func(context.Context, types.AssembleInput) (types.ModelRequest, error)) *gohan.Stack {
	t.Helper()
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}),
		gohan.WithModels(m),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:  gohan.FlowRequest{Name: "chat"},
			Profile:  m.Profile().Name,
			Tools:    []types.Tool{recorder},
			Assemble: assemble,
		}),
		gohan.WithLimits("chat", types.RunLimits{
			MaxTurns:            3,
			MaxToolCalls:        4,
			MaxWallClock:        time.Minute,
			SoftRatio:           0.8,
			MaxDepth:            2,
			MaxParallelChildren: 2,
			MaxParallelTools:    1,
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return stack
}

func compConversation(t *testing.T, stack *gohan.Stack) gohan.Conversation {
	t.Helper()
	conv, err := gohan.NewNativeConversation(stack, "chat",
		gohan.WithConversationRuns(stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return conv
}

func compCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type compRun struct {
	events []types.Event
	err    error
}

func compSend(ctx context.Context, conv gohan.Conversation, sessionID string, hook func(types.Event)) compRun {
	var res compRun
	send := conv.Send(gohan.WithPrincipal(ctx, types.Principal{Tenant: "t-a", Subject: "u1"}), sessionID, types.Message{
		Role: types.RoleUser,
		Blocks: []types.Block{types.Text{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}},
			Text:      "go",
		}},
	})
	for ev, err := range send {
		if err != nil {
			res.err = err
			return res
		}
		res.events = append(res.events, ev)
		if hook != nil {
			hook(ev)
		}
	}
	return res
}

// compSendAll drains the whole conversation stream: it counts every error
// yield and every event emitted after the first error, so a terminal error
// that leaks completion or usage stays visible.
func compSendAll(ctx context.Context, conv gohan.Conversation, sessionID string) (compRun, int, int) {
	var res compRun
	errs, afterErr := 0, 0
	send := conv.Send(gohan.WithPrincipal(ctx, types.Principal{Tenant: "t-a", Subject: "u1"}), sessionID, types.Message{
		Role: types.RoleUser,
		Blocks: []types.Block{types.Text{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}},
			Text:      "go",
		}},
	})
	for ev, err := range send {
		if err != nil {
			errs++
			if res.err == nil {
				res.err = err
			}
			continue
		}
		if res.err != nil {
			afterErr++
		}
		res.events = append(res.events, ev)
	}
	return res, errs, afterErr
}

func compPreviews(evs []types.Event) []types.ToolArgsDelta {
	var out []types.ToolArgsDelta
	for _, ev := range evs {
		if p, ok := ev.(types.ToolArgsDelta); ok {
			out = append(out, p)
		}
	}
	return out
}

func compStarted(evs []types.Event) []types.ToolUse {
	var out []types.ToolUse
	for _, ev := range evs {
		if s, ok := ev.(types.ToolStarted); ok {
			out = append(out, s.Call)
		}
	}
	return out
}

func compAssistantText(evs []types.Event) string {
	var out strings.Builder
	for _, ev := range evs {
		am, ok := ev.(types.AssistantMessage)
		if !ok {
			continue
		}
		for _, b := range am.Message.Blocks {
			if txt, ok := b.(types.Text); ok {
				out.WriteString(txt.Text)
			}
		}
	}
	return out.String()
}

func compDone(evs []types.Event) bool {
	for _, ev := range evs {
		if _, ok := ev.(types.Done); ok {
			return true
		}
	}
	return false
}

func compAssertAssembled(t *testing.T, body map[string]any, wantTool string) {
	t.Helper()
	if body["stream"] != true {
		t.Errorf("assembled request stream = %v, want true", body["stream"])
	}
	if body["model"] != "claude-composition-1" {
		t.Errorf("assembled request model = %v, want claude-composition-1", body["model"])
	}
	tools, _ := body["tools"].([]any)
	found := false
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] == wantTool {
			found = true
		}
	}
	if !found {
		t.Errorf("assembled request tools = %v, want %s declared", tools, wantTool)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("assembled request carries no messages")
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "user" {
		t.Errorf("first assembled message role = %v, want user", first["role"])
	}
}

func TestProviderComposition(t *testing.T) {
	t.Run("model.anthropic-complete-tool-blocks", func(t *testing.T) {
		release := make(chan struct{})
		var once sync.Once
		// The fixture parks after the first fragment, before the first
		// block stops, so no effect may run while the completion is open.
		// Block 1 opens and streams its first fragment before block 0
		// stops, so per-index ownership must survive interleaving.
		turn1 := compTurn{
			events: []string{
				compMsgStart(),
				compToolStart(0, "toolu_a", "search"),
				compArgsDelta(0, `{"q":`),
				compToolStart(1, "toolu_b", "search"),
				compArgsDelta(1, `{"lang":`),
				compArgsDelta(0, `"go"}`),
				compBlockStop(0),
				compArgsDelta(1, `"en"}`),
				compBlockStop(1),
				compMsgDelta("tool_use"),
				compStop(),
			},
			pauseAt: 3,
			gate:    release,
		}
		fx := newCompFixture(t, turn1, compTurn{events: compTextEvents("done")})
		recorder := &compRecorder{}
		stack := compStack(t, compModel(t, fx), recorder, compAssemble)
		conv := compConversation(t, stack)
		ctx := compCtx(t)

		res := compSend(ctx, conv, "s-complete-tool-blocks", func(ev types.Event) {
			p, ok := ev.(types.ToolArgsDelta)
			if !ok || p.CallID == "" {
				return
			}
			// Only the first preview is guaranteed to reach the consumer
			// while the fixture still pauses; later previews may arrive after
			// effects because core buffers events asynchronously.
			once.Do(func() {
				if n := len(recorder.effects()); n != 0 {
					t.Errorf("effect executed while the fixture paused before completion: %d effects", n)
				}
				close(release)
			})
		})

		compAssertAssembled(t, fx.request(t, 0), "search")

		effects := recorder.effects()
		want := map[string]bool{`{"q":"go"}`: false, `{"lang":"en"}`: false}
		for _, call := range effects {
			if _, ok := want[call]; ok {
				want[call] = true
			}
		}
		complete := 0
		for _, seen := range want {
			if seen {
				complete++
			}
		}
		if len(effects) != 2 || complete != 2 {
			t.Fatalf("recorded tool effects %v, want exactly two complete calls executed once each with independent arguments {\"q\":\"go\"} and {\"lang\":\"en\"} from both content_block_stop boundaries", effects)
		}

		started := compStarted(res.events)
		ids := map[string]bool{}
		for _, call := range started {
			ids[call.ID] = true
		}
		if len(started) != 2 || !ids["toolu_a"] || !ids["toolu_b"] {
			t.Fatalf("started tool calls %v, want distinct IDs toolu_a and toolu_b, one per stopped block", started)
		}

		if text := compAssistantText(res.events); !strings.Contains(text, "done") {
			t.Fatalf("terminal assistant text = %q, want the final text response to complete the conversation", text)
		}
		if !compDone(res.events) {
			t.Fatalf("conversation ended without Done after the terminal text response")
		}
	})

	t.Run("model.provider-argument-preview-identity", func(t *testing.T) {
		whole := `{"q":"gohan","limit":42}`
		fx := newCompFixture(t,
			compTurn{events: []string{
				compMsgStart(),
				compToolStart(0, "toolu_p", "search"),
				compArgsDelta(0, `{"q":`),
				compArgsDelta(0, `"gohan",`),
				compArgsDelta(0, `"limit":`),
				compArgsDelta(0, `42}`),
				compBlockStop(0),
				compMsgDelta("tool_use"),
				compStop(),
			}},
			compTurn{events: compTextEvents("done")},
			compTurn{events: []string{
				compMsgStart(),
				compToolStart(0, "toolu_w", "search"),
				compArgsDelta(0, whole),
				compBlockStop(0),
				compMsgDelta("tool_use"),
				compStop(),
			}},
			compTurn{events: compTextEvents("done")},
		)
		recorder := &compRecorder{}
		stack := compStack(t, compModel(t, fx), recorder, compAssemble)

		multi := compSend(compCtx(t), compConversation(t, stack), "s-preview-fragments", nil)
		single := compSend(compCtx(t), compConversation(t, stack), "s-preview-whole", nil)

		previews := compPreviews(multi.events)
		if len(previews) != 4 {
			t.Fatalf("streamed %d argument previews, want one identity-bearing preview per fragment (4 fragments sent)", len(previews))
		}
		var streamed strings.Builder
		for _, p := range previews {
			if p.CallID != "toolu_p" || p.Name != "search" || p.Delta == "" {
				t.Fatalf("preview carries CallID=%q Name=%q Delta=%q, want call identity and fragment text", p.CallID, p.Name, p.Delta)
			}
			streamed.WriteString(p.Delta)
		}
		if streamed.String() != whole {
			t.Fatalf("preview fragments concatenate to %q, want the complete argument string %q", streamed.String(), whole)
		}

		solo := compPreviews(single.events)
		if len(solo) != 1 {
			t.Fatalf("whole-argument completion streamed %d previews, want exactly one identity-bearing preview", len(solo))
		}
		if solo[0].CallID != "toolu_w" || solo[0].Name != "search" || solo[0].Delta != whole {
			t.Fatalf("whole-argument preview = CallID %q Name %q Delta %q, want toolu_w/search carrying the whole argument string", solo[0].CallID, solo[0].Name, solo[0].Delta)
		}

		effects := recorder.effects()
		if len(effects) != 2 || effects[0] != whole || effects[1] != whole {
			t.Fatalf("recorded tool effects %v, want each previewed call to execute once with its complete argument string", effects)
		}
		if !compDone(multi.events) || !compDone(single.events) {
			t.Fatalf("both preview runs must end with Done after their terminal text responses")
		}
	})

	t.Run("model.forced-output-and-tool-truncation", func(t *testing.T) {
		forcedArgs := `{"answer":"hi"}`

		// Control: a forced-schema response stays text and executes no local tool.
		forcedFx := newCompFixture(t,
			compTurn{events: []string{
				compMsgStart(),
				compToolStart(0, "toolu_f", forcedToolName),
				compArgsDelta(0, forcedArgs),
				compBlockStop(0),
				compMsgDelta("end_turn"),
				compStop(),
			}},
		)
		forcedRecorder := &compRecorder{}
		forcedStack := compStack(t, compModel(t, forcedFx), forcedRecorder, compForcedAssemble)
		forcedRun := compSend(compCtx(t), compConversation(t, forcedStack), "s-forced-output", nil)
		compAssertAssembled(t, forcedFx.request(t, 0), forcedToolName)
		choice, _ := forcedFx.request(t, 0)["tool_choice"].(map[string]any)
		if choice["type"] != "tool" || choice["name"] != forcedToolName {
			t.Errorf("assembled tool_choice = %v, want the forced gohan_respond tool", choice)
		}
		if text := compAssistantText(forcedRun.events); text != forcedArgs {
			t.Errorf("forced-schema assistant text = %q, want the response JSON %q as plain text", text, forcedArgs)
		}
		if n := len(forcedRecorder.effects()); n != 0 {
			t.Errorf("forced output executed %d local tool effects, want none", n)
		}
		if started := compStarted(forcedRun.events); len(started) != 0 {
			t.Errorf("forced output started %d local tool calls, want none", len(started))
		}
		if !compDone(forcedRun.events) {
			t.Errorf("forced-schema response did not complete the conversation")
		}

		// Control: a valid {} completion executes exactly once.
		validFx := newCompFixture(t,
			compTurn{events: []string{
				compMsgStart(),
				compToolStart(0, "toolu_v", "search"),
				compArgsDelta(0, `{}`),
				compBlockStop(0),
				compMsgDelta("tool_use"),
				compStop(),
			}},
		)
		validRecorder := &compRecorder{}
		validStack := compStack(t, compModel(t, validFx), validRecorder, compAssemble)
		validRun := compSend(compCtx(t), compConversation(t, validStack), "s-valid-empty-object", nil)
		if got := validRecorder.effects(); len(got) != 1 || got[0] != `{}` {
			t.Errorf("valid {} completion recorded effects %v, want exactly one execution with {}", got)
		}
		if !compDone(validRun.events) {
			t.Errorf("valid {} completion did not end the conversation")
		}

		// Control: EOF with an unclosed tool block never completes the call.
		eofFx := newCompFixture(t,
			compTurn{events: []string{
				compMsgStart(),
				compToolStart(0, "toolu_e", "search"),
				compArgsDelta(0, `{"q":"par`),
			}},
		)
		eofRecorder := &compRecorder{}
		eofStack := compStack(t, compModel(t, eofFx), eofRecorder, compAssemble)
		eofRun := compSend(compCtx(t), compConversation(t, eofStack), "s-eof-open-block", nil)
		if got := eofRecorder.effects(); len(got) != 0 {
			t.Errorf("open-block EOF run executed effects %v, want zero tool effects", got)
		}
		if !compDone(eofRun.events) {
			t.Errorf("open-block EOF run did not complete the conversation")
		}

		// The same bare-EOF body must end the public Generate stream with
		// exactly one FinishMaxTokens chunk.
		eofGenFx := newCompFixture(t,
			compTurn{events: []string{
				compMsgStart(),
				compToolStart(0, "toolu_e", "search"),
				compArgsDelta(0, `{"q":"par`),
			}},
		)
		eofGenFinishes := fbFinishes(fbCollect(t, compModel(t, eofGenFx), fbRequest((&compRecorder{}).Spec())))
		if len(eofGenFinishes) != 1 || eofGenFinishes[0] != types.FinishMaxTokens {
			t.Errorf("bare EOF over an open ordinary tool index reported finishes %v, want exactly one FinishMaxTokens chunk in the public Generate stream", eofGenFinishes)
		}

		// Control: max_tokens truncation of an unclosed block executes nothing.
		truncFx := newCompFixture(t,
			compTurn{events: []string{
				compMsgStart(),
				compToolStart(0, "toolu_t", "search"),
				compArgsDelta(0, `{"q":"go"}`),
				compMsgDelta("max_tokens"),
				compStop(),
			}},
		)
		truncRecorder := &compRecorder{}
		truncStack := compStack(t, compModel(t, truncFx), truncRecorder, compAssemble)
		truncRun := compSend(compCtx(t), compConversation(t, truncStack), "s-max-tokens-open-block", nil)
		if got := truncRecorder.effects(); len(got) != 0 {
			t.Errorf("max_tokens truncation run executed effects %v, want zero tool effects", got)
		}
		if !compDone(truncRun.events) {
			t.Errorf("max_tokens truncation run did not complete the conversation")
		}

		// Control: message_delta end_turn while a tool block is open is a
		// truncation terminal, not a clean stop.
		endTurnFx := newCompFixture(t,
			compTurn{events: []string{
				compMsgStart(),
				compToolStart(0, "toolu_s", "search"),
				compArgsDelta(0, `{"q":"op`),
				compMsgDelta("end_turn"),
				compStop(),
			}},
		)
		endTurnFinishes := fbFinishes(fbCollect(t, compModel(t, endTurnFx), fbRequest((&compRecorder{}).Spec())))
		if len(endTurnFinishes) != 1 || endTurnFinishes[0] != types.FinishMaxTokens {
			t.Errorf("end_turn stop over an open ordinary tool index reported finishes %v, want exactly one FinishMaxTokens, not FinishStop", endTurnFinishes)
		}

		t.Run("block_stop_empty_args", func(t *testing.T) {
			fx := newCompFixture(t,
				compTurn{events: []string{
					compMsgStart(),
					compToolStart(0, "toolu_b1", "search"),
					compBlockStop(0),
				}},
			)
			recorder := &compRecorder{}
			stack := compStack(t, compModel(t, fx), recorder, compAssemble)
			run, errs, afterErr := compSendAll(compCtx(t), compConversation(t, stack), "s-stop-empty-args")
			if got := recorder.effects(); len(got) != 0 {
				t.Fatalf("empty-args completion executed effects %v, want zero tool effects", got)
			}
			if run.err == nil {
				t.Fatalf("explicit content_block_stop after empty argument bytes surfaced no error, want the public Generate error callback with a zero-effect outcome")
			}
			var modelErr *types.ModelError
			if !errors.As(run.err, &modelErr) || modelErr.Class != types.ClassPermanent {
				t.Fatalf("empty-args completion error = %v (%T), want a *types.ModelError with Class ClassPermanent", run.err, run.err)
			}
			if errs != 1 {
				t.Errorf("empty-args completion surfaced %d errors over the whole conversation stream, want exactly one terminal error", errs)
			}
			if afterErr != 0 {
				t.Errorf("empty-args completion emitted %d events after the terminal error, want no usage or finish event after the error", afterErr)
			}
		})

		t.Run("block_stop_incomplete_args", func(t *testing.T) {
			fx := newCompFixture(t,
				compTurn{events: []string{
					compMsgStart(),
					compToolStart(0, "toolu_b2", "search"),
					compArgsDelta(0, `{"q":`),
					compBlockStop(0),
				}},
			)
			recorder := &compRecorder{}
			stack := compStack(t, compModel(t, fx), recorder, compAssemble)
			run, errs, afterErr := compSendAll(compCtx(t), compConversation(t, stack), "s-stop-incomplete-args")
			if got := recorder.effects(); len(got) != 0 {
				t.Fatalf("incomplete-args completion executed effects %v, want zero tool effects", got)
			}
			if run.err == nil {
				t.Fatalf("explicit content_block_stop after the incomplete prefix {\"q\": surfaced no error, want the public Generate error callback with a zero-effect outcome")
			}
			var modelErr *types.ModelError
			if !errors.As(run.err, &modelErr) || modelErr.Class != types.ClassPermanent {
				t.Fatalf("incomplete-args completion error = %v (%T), want a *types.ModelError with Class ClassPermanent", run.err, run.err)
			}
			if errs != 1 {
				t.Errorf("incomplete-args completion surfaced %d errors over the whole conversation stream, want exactly one terminal error", errs)
			}
			if afterErr != 0 {
				t.Errorf("incomplete-args completion emitted %d events after the terminal error, want no usage or finish event after the error", afterErr)
			}
		})

		t.Run("block_stop_malformed_args", func(t *testing.T) {
			fx := newCompFixture(t,
				compTurn{events: []string{
					compMsgStart(),
					compToolStart(0, "toolu_b3", "search"),
					compArgsDelta(0, `{]`),
					compBlockStop(0),
				}},
			)
			recorder := &compRecorder{}
			stack := compStack(t, compModel(t, fx), recorder, compAssemble)
			run, errs, afterErr := compSendAll(compCtx(t), compConversation(t, stack), "s-stop-malformed-args")
			if got := recorder.effects(); len(got) != 0 {
				t.Fatalf("malformed-args completion executed effects %v, want zero tool effects", got)
			}
			if run.err == nil {
				t.Fatalf("explicit content_block_stop after malformed bytes {] surfaced no error, want the public Generate error callback with a zero-effect outcome")
			}
			var modelErr *types.ModelError
			if !errors.As(run.err, &modelErr) || modelErr.Class != types.ClassPermanent {
				t.Fatalf("malformed-args completion error = %v (%T), want a *types.ModelError with Class ClassPermanent", run.err, run.err)
			}
			if errs != 1 {
				t.Errorf("malformed-args completion surfaced %d errors over the whole conversation stream, want exactly one terminal error", errs)
			}
			if afterErr != 0 {
				t.Errorf("malformed-args completion emitted %d events after the terminal error, want no usage or finish event after the error", afterErr)
			}
		})

		t.Run("decode_error_accepted_terminal", func(t *testing.T) {
			fx := newCompFixture(t,
				compTurn{events: []string{
					compMsgStart(),
					event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":`),
					compToolStart(0, "toolu_d", "search"),
					compArgsDelta(0, `{}`),
					compBlockStop(0),
					compMsgDelta("tool_use"),
					compStop(),
				}},
			)
			var modelErrs []*types.ModelError
			var after []types.ModelChunk
			accepted := false
			// The consumer accepts the error verdict, so only production
			// can make the terminal decode error final.
			for chunk, err := range compModel(t, fx).Generate(compCtx(t), fbRequest((&compRecorder{}).Spec())) {
				if err != nil {
					accepted = true
					var me *types.ModelError
					if !errors.As(err, &me) {
						t.Fatalf("malformed content_block_delta surfaced %v (%T), want a *types.ModelError", err, err)
					}
					modelErrs = append(modelErrs, me)
					continue
				}
				if accepted {
					after = append(after, chunk)
				}
			}
			if len(modelErrs) != 1 {
				t.Fatalf("accepted terminal decode error surfaced %d errors, want exactly one ModelError", len(modelErrs))
			}
			if len(after) != 0 {
				t.Fatalf("accepted terminal decode error was followed by %d completion/usage chunks (%v), want the error to be final with no completion or usage after it", len(after), after)
			}
		})

		t.Run("stop_less_delta_then_eof", func(t *testing.T) {
			// A message_delta without stop_reason over an open tool index
			// must not swallow the truncation terminal: the pending flush
			// marks the block emitted, but clean EOF still owes the public
			// Generate stream exactly one FinishMaxTokens chunk.
			stopLessFx := newCompFixture(t,
				compTurn{events: []string{
					compMsgStart(),
					compToolStart(0, "toolu_sl", "search"),
					compArgsDelta(0, `{"q":"sl`),
					event("message_delta", `{"type":"message_delta","delta":{},"usage":{"output_tokens":5}}`),
				}},
			)
			stopLessChunks := fbCollect(t, compModel(t, stopLessFx), fbRequest((&compRecorder{}).Spec()))
			var stopLessPreviews int
			for _, chunk := range stopLessChunks {
				if chunk.Kind != types.DeltaToolArgs || chunk.ToolUse == nil {
					continue
				}
				if len(chunk.ToolUse.Args) > 0 {
					t.Errorf("stop-less delta completed tool args %s, want identity-only previews that never complete the call", chunk.ToolUse.Args)
					continue
				}
				if chunk.ToolUse.ID != "toolu_sl" || chunk.ToolUse.Name != "search" {
					t.Errorf("stop-less preview identity = %s/%s, want toolu_sl/search", chunk.ToolUse.ID, chunk.ToolUse.Name)
					continue
				}
				stopLessPreviews++
			}
			if stopLessPreviews == 0 {
				t.Errorf("stop-less delta over an open tool index surfaced no identity preview, want the pending fragment surfaced with the call identity before EOF")
			}
			stopLessFinishes := fbFinishes(stopLessChunks)
			if len(stopLessFinishes) != 1 || stopLessFinishes[0] != types.FinishMaxTokens {
				t.Errorf("stop-less message_delta followed by clean EOF over an open ordinary tool index reported finishes %v, want exactly one FinishMaxTokens chunk in the public Generate stream", stopLessFinishes)
			}
		})
	})
}
