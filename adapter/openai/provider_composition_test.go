package openai_test

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/adapter/openai"
	"github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const (
	sseDone       = "data: [DONE]\n\n"
	usageTotals   = `{"prompt_tokens":40,"completion_tokens":10}`
	usageSmall    = `{"prompt_tokens":11,"completion_tokens":7}`
	usageSmallCmp = `{"prompt_tokens":11,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}`
	argsValid     = `{\"q\":\"go\"}`
	argsOpen      = `{\"q\":`
	argsMalformed = `{]`
)

func sse(frames ...string) string { return strings.Join(frames, "") }

func textFrame(text string) string {
	return `data: {"model":"served-1","choices":[{"index":0,"delta":{"content":"` + text + `"}}]}` + "\n\n"
}

func reasoningFrame(text string) string {
	return `data: {"model":"served-1","choices":[{"index":0,"delta":{"reasoning_content":"` + text + `"}}]}` + "\n\n"
}

func toolFrame(index int, id, name, args string) string {
	call := `{"index":` + strconv.Itoa(index) + `,`
	if id != "" || name != "" {
		call += `"id":"` + id + `","function":{"name":"` + name + `","arguments":"` + args + `"}}`
	} else {
		call += `"function":{"arguments":"` + args + `"}}`
	}
	return `data: {"model":"served-1","choices":[{"index":0,"delta":{"tool_calls":[` + call + `]}}]}` + "\n\n"
}

func finishFrame(reason, usageJSON string) string {
	out := `data: {"model":"served-1","choices":[{"index":0,"delta":{},"finish_reason":"` + reason + `"}]`
	if usageJSON != "" {
		out += `,"usage":` + usageJSON
	}
	return out + "}\n\n"
}

func usageFrame(usageJSON string) string {
	return `data: {"model":"served-1","choices":[],"usage":` + usageJSON + "}\n\n"
}

type trackedBody struct {
	reader io.Reader
	mu     sync.Mutex
	closes int
}

func (b *trackedBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

func (b *trackedBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closes++
	return nil
}

func (b *trackedBody) closeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closes
}

type errAfterBytes struct {
	data []byte
	off  int
	read error
}

func (r *errAfterBytes) Read(p []byte) (int, error) {
	if r.off < len(r.data) {
		n := copy(p, r.data[r.off:])
		r.off += n
		return n, nil
	}
	return 0, r.read
}

type sseTransport struct {
	status int
	raw    string
	// reader replaces the raw body when set, for failing-read fixtures.
	reader io.Reader
	mu     sync.Mutex
	bodies []*trackedBody
}

func (t *sseTransport) Do(r *http.Request) (*http.Response, error) {
	var body io.Reader = strings.NewReader(t.raw)
	if t.reader != nil {
		body = t.reader
	}
	b := &trackedBody{reader: body}
	t.mu.Lock()
	t.bodies = append(t.bodies, b)
	t.mu.Unlock()
	return &http.Response{
		StatusCode: t.status,
		Status:     fmt.Sprintf("%d %s", t.status, http.StatusText(t.status)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       b,
		Request:    r,
	}, nil
}

func (t *sseTransport) closeCounts() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]int, 0, len(t.bodies))
	for _, b := range t.bodies {
		out = append(out, b.closeCount())
	}
	return out
}

func readFailTransportFor(frame string, read error) *sseTransport {
	return &sseTransport{status: 200, reader: &errAfterBytes{data: []byte(frame), read: read}}
}

type silentTransport struct {
	entered chan struct{}
	mu      sync.Mutex
	bodies  []*silentBody
}

func newSilentTransport() *silentTransport { return &silentTransport{entered: make(chan struct{})} }

func (t *silentTransport) Do(r *http.Request) (*http.Response, error) {
	b := &silentBody{ctx: r.Context(), entered: t.entered}
	t.mu.Lock()
	t.bodies = append(t.bodies, b)
	t.mu.Unlock()
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       b,
		Request:    r,
	}, nil
}

func (t *silentTransport) closeCounts() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]int, 0, len(t.bodies))
	for _, b := range t.bodies {
		out = append(out, b.closeCount())
	}
	return out
}

type silentBody struct {
	ctx     context.Context
	entered chan struct{}
	once    sync.Once
	mu      sync.Mutex
	closes  int
}

func (b *silentBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *silentBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closes++
	return nil
}

func (b *silentBody) closeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closes
}

type yieldLog struct {
	mu         sync.Mutex
	chunks     []types.ModelChunk
	errs       []error
	seq        []byte
	calls      int
	rejectedAt int
	reject     func(types.ModelChunk, error) bool
}

func (l *yieldLog) accept(c types.ModelChunk, e error) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	l.seq = append(l.seq, 'c')
	l.chunks = append(l.chunks, c)
	if e != nil {
		l.seq[len(l.seq)-1] = 'e'
		l.errs = append(l.errs, e)
	}
	if l.reject != nil && l.reject(c, e) {
		l.rejectedAt = l.calls
		return false
	}
	return true
}

func (l *yieldLog) callsCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func (l *yieldLog) callbacksAfterRejection() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rejectedAt == 0 {
		return 0
	}
	return l.calls - l.rejectedAt
}

func (l *yieldLog) chunksAfterError() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	after := 0
	seenErr := false
	for _, s := range l.seq {
		if s == 'e' {
			seenErr = true
			continue
		}
		if seenErr {
			after++
		}
	}
	return after
}

func (l *yieldLog) firstModelErr() *types.ModelError {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.errs {
		var me *types.ModelError
		if errors.As(e, &me) {
			return me
		}
	}
	return nil
}

func (l *yieldLog) usageReports() []*types.Usage {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*types.Usage
	for _, c := range l.chunks {
		if c.Usage != nil {
			out = append(out, c.Usage)
		}
	}
	return out
}

func (l *yieldLog) finalFinish() types.FinishReason {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.chunks) - 1; i >= 0; i-- {
		if l.chunks[i].Finish != "" {
			return l.chunks[i].Finish
		}
	}
	return ""
}

func drain(t *testing.T, p *openai.Provider, req types.ModelRequest) *yieldLog {
	t.Helper()
	log := &yieldLog{}
	for c, e := range p.Generate(context.Background(), req) {
		log.accept(c, e)
	}
	return log
}

// consumeGuarded runs the consumer loop in a recovered goroutine: the
// pre-repair deferred yield after a false return panics the whole binary.
func consumeGuarded(t *testing.T, run func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("iteration panicked after the consumer stopped: %v; no callback may follow a false yield", r)
			}
		}()
		run()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Errorf("consumer loop did not return within 10s")
	}
}

func fixtureProvider(t *testing.T, caps types.Caps, tp openai.Transport) *openai.Provider {
	t.Helper()
	p, err := openai.New(openai.Config{
		Profile: types.ModelProfile{
			Name:    fixtureProfile,
			Version: fixtureVersion,
			Keys:    types.PlatformKey,
			Caps:    caps,
		},
		Keys:      simpleKeys(fixtureKeyID, fixtureToken),
		Transport: tp,
	})
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	return p
}

func compositionRequest(note string) types.ModelRequest {
	req := proofRequest(note)
	req.Tools = []types.ToolSpec{{Name: "echo"}}
	return req
} // compositionRequest pins the local tool every zero-effect fixture registers.

type recordingTool struct {
	mu    sync.Mutex
	calls []jsontext.Value
}

func (r *recordingTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "echo", Effect: types.ReadOnly}
}

func (r *recordingTool) Call(_ context.Context, args jsontext.Value) (types.ToolResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
	return types.ToolResult{Content: []types.Block{types.Text{Text: "echoed"}}, Outcome: types.Succeeded}, nil
}

func (r *recordingTool) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func principalCtx(ctx context.Context) context.Context {
	return gohan.WithPrincipal(ctx, types.Principal{Tenant: "t-a", Subject: "u1"})
}

func userMsg(text string) types.Message {
	return types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: text}},
	}
}

func nativeCase(t *testing.T, raw string) (*recordingTool, []types.Event, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// A turn carrying a tool result asks for the terminal response, so
		// the conversation ends after one authorized execution instead of
		// looping the same tool stream until the turn budget.
		payload, _ := io.ReadAll(r.Body)
		if strings.Contains(string(payload), `"role":"tool"`) {
			_, _ = io.WriteString(w, sse(textFrame("done"), finishFrame("stop", usageTotals), sseDone))
			return
		}
		_, _ = io.WriteString(w, raw)
	}))
	t.Cleanup(server.Close)
	provider, err := openai.New(openai.Config{
		Profile: types.ModelProfile{
			Name:    fixtureProfile,
			Version: fixtureVersion,
			Keys:    types.PlatformKey,
			Caps:    proofCaps(nil),
		},
		Keys:      simpleKeys(fixtureKeyID, fixtureToken),
		Transport: server.Client(),
		Endpoint:  server.URL,
	})
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	tool := &recordingTool{}
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}),
		gohan.WithModels(provider),
		gohan.WithLimits(fixtureProfile, types.RunLimits{
			MaxTurns:            3,
			MaxToolCalls:        2,
			MaxWallClock:        time.Minute,
			SoftRatio:           0.8,
			MaxDepth:            4,
			MaxParallelChildren: 2,
			MaxParallelTools:    2,
		}),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "chat"},
			Profile: fixtureProfile,
			Tools:   []types.Tool{tool},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	var evs []types.Event
	var sendErr error
	for ev, err := range conv.Send(principalCtx(ctx), t.Name(), userMsg("run the tool")) {
		if err != nil {
			sendErr = err
			break
		}
		evs = append(evs, ev)
	}
	return tool, evs, sendErr
}

func assertNoToolEffects(t *testing.T, tool *recordingTool, evs []types.Event) {
	t.Helper()
	if n := tool.callCount(); n != 0 {
		t.Errorf("recording tool executed %d times, want 0 tool effects", n)
	}
	for _, ev := range evs {
		if _, ok := ev.(types.ToolStarted); ok {
			t.Errorf("conversation emitted %T; an invalid pending batch must produce no tool events", ev)
		}
	}
}

func assertSingleUsage(t *testing.T, log *yieldLog, wantEstimate bool) *types.Usage {
	t.Helper()
	reports := log.usageReports()
	if len(reports) != 1 {
		t.Fatalf("streamed %d usage reports, want exactly one final usage report", len(reports))
	}
	if reports[0].Estimated != wantEstimate {
		t.Errorf("usage estimated=%v, want %v", reports[0].Estimated, wantEstimate)
	}
	return reports[0]
}

func TestProviderComposition(t *testing.T) {
	t.Run("model.provider-argument-preview-identity", func(t *testing.T) {
		raw := sse(
			toolFrame(0, "call-a", "echo", `{\"q\":`),
			toolFrame(1, "call-b", "echo", `{\"n\":`),
			toolFrame(0, "", "", `1}`),
			toolFrame(1, "", "", `2}`),
			finishFrame("tool_calls", usageTotals),
			sseDone,
		)
		tr := &sseTransport{status: 200, raw: raw}
		p := fixtureProvider(t, proofCaps(nil), tr)
		log := drain(t, p, compositionRequest("preview identity"))
		var previews []types.ModelChunk
		var completes []types.ToolUse
		for _, c := range log.chunks {
			if c.Kind == types.DeltaToolArgs {
				previews = append(previews, c)
				continue
			}
			if c.ToolUse != nil {
				completes = append(completes, *c.ToolUse)
			}
		}
		if len(previews) != 4 {
			t.Errorf("streamed %d identity-bearing DeltaToolArgs previews, want 4, one per argument fragment", len(previews))
		} else {
			wantID := []string{"call-a", "call-b", "call-a", "call-b"}
			for i, pv := range previews {
				if pv.ToolUse == nil || pv.ToolUse.ID != wantID[i] || pv.ToolUse.Name != "echo" {
					t.Errorf("preview %d lost its call identity (%v), want %s on echo", i, pv.ToolUse, wantID[i])
				}
				if args := strings.TrimSpace(string(pv.ToolUse.Args)); args != "" {
					t.Errorf("preview %d carried executable args %q; previews must stay identity-only until completion", i, args)
				}
			}
		}
		if len(completes) != 2 {
			t.Errorf("streamed %d complete calls, want 2", len(completes))
		} else {
			if completes[0].ID != "call-a" || string(completes[0].Args) != `{"q":1}` {
				t.Errorf("complete call 0 is %s(%s), want call-a({\"q\":1})", completes[0].ID, string(completes[0].Args))
			}
			if completes[1].ID != "call-b" || string(completes[1].Args) != `{"n":2}` {
				t.Errorf("complete call 1 is %s(%s), want call-b({\"n\":2})", completes[1].ID, string(completes[1].Args))
			}
		}
		nativeRaw := sse(
			toolFrame(0, "call-1", "echo", `{\"q\":`),
			toolFrame(0, "", "", `\"go\"}`),
			finishFrame("tool_calls", usageTotals),
			sseDone,
		)
		tool, evs, sendErr := nativeCase(t, nativeRaw)
		if sendErr != nil {
			t.Fatalf("native send: %v", sendErr)
		}
		if n := tool.callCount(); n != 1 {
			t.Errorf("recording tool executed %d times, want the one completed call exactly once", n)
		}
		for _, ev := range evs {
			if started, ok := ev.(types.ToolStarted); ok {
				if started.Call.ID != "call-1" || string(started.Call.Args) != `{"q":"go"}` {
					t.Errorf("executed call %s(%s), want call-1({\"q\":\"go\"})", started.Call.ID, string(started.Call.Args))
				}
			}
		}
	})

	t.Run("model.forced-output-and-tool-truncation", func(t *testing.T) {
		pendingFrames := sse(
			toolFrame(0, "call-1", "echo", argsOpen),
			toolFrame(0, "", "", `\"g`),
		)
		pending := []struct {
			name         string
			raw          string
			wantFinish   types.FinishReason
			wantClass    types.ErrorClass
			wantEstimate bool
		}{
			{"pending_length", sse(pendingFrames, finishFrame("length", usageTotals), sseDone), types.FinishMaxTokens, 0, false},
			{"pending_done", sse(pendingFrames, sseDone), types.FinishMaxTokens, 0, true},
			{"pending_eof", sse(toolFrame(0, "call-1", "echo", argsValid)), types.FinishMaxTokens, 0, true},
			{"pending_stop", sse(pendingFrames, finishFrame("stop", usageTotals), sseDone), types.FinishMaxTokens, 0, false},
			{"pending_content_filter", sse(pendingFrames, finishFrame("content_filter", usageTotals), sseDone), types.FinishRefusal, 0, false},
			{"pending_error", sse(pendingFrames, finishFrame("error", usageTotals), sseDone), "", types.ClassTransient, false},
			{"pending_unknown_finish", sse(pendingFrames, finishFrame("rebalanced", usageTotals), sseDone), "", types.ClassTransient, false},
		}
		for _, row := range pending {
			t.Run(row.name, func(t *testing.T) {
				p := fixtureProvider(t, proofCaps(nil), &sseTransport{status: 200, raw: row.raw})
				log := drain(t, p, compositionRequest("pending terminal "+row.name))
				for _, c := range log.chunks {
					if c.ToolUse != nil && strings.TrimSpace(string(c.ToolUse.Args)) != "" {
						t.Errorf("pending call completed with executable args %q; abandoned arguments must never execute", string(c.ToolUse.Args))
					}
				}
				if row.wantClass != 0 {
					merr := log.firstModelErr()
					if merr == nil {
						t.Fatalf("terminal %q produced no ModelError; pending calls must end in one ClassTransient failure", row.name)
					}
					if merr.Class != row.wantClass {
						t.Errorf("terminal error class %d, want %d", merr.Class, row.wantClass)
					}
					if n := len(log.errs); n != 1 {
						t.Errorf("terminal %q yielded %d errors, want exactly one", row.name, n)
					}
					if after := log.chunksAfterError(); after != 0 {
						t.Errorf("%d chunks yielded after the terminal error; no usage or completion may follow it", after)
					}
				}
				if row.wantFinish != "" {
					if fin := log.finalFinish(); fin != row.wantFinish {
						t.Errorf("terminal %q ended with finish %q, want %q; the abandoned batch must end as this terminal", row.name, fin, row.wantFinish)
					}
					assertSingleUsage(t, log, row.wantEstimate)
				}
				tool, evs, _ := nativeCase(t, row.raw)
				assertNoToolEffects(t, tool, evs)
			})
		}
		malformed := []struct{ name, raw string }{
			{"tool_calls_empty_args", sse(toolFrame(0, "call-1", "echo", ""), finishFrame("tool_calls", usageTotals), sseDone)},
			{"tool_calls_incomplete_args", sse(toolFrame(0, "call-1", "echo", argsOpen), finishFrame("tool_calls", usageTotals), sseDone)},
			{"tool_calls_malformed_args", sse(toolFrame(0, "call-1", "echo", argsMalformed), finishFrame("tool_calls", usageTotals), sseDone)},
			{"tool_calls_valid_sibling_before_invalid", sse(
				toolFrame(0, "call-ok", "echo", `{}`),
				toolFrame(1, "call-bad", "echo", argsMalformed),
				finishFrame("tool_calls", usageTotals), sseDone,
			)},
		}
		for _, row := range malformed {
			t.Run(row.name, func(t *testing.T) {
				p := fixtureProvider(t, proofCaps(nil), &sseTransport{status: 200, raw: row.raw})
				log := drain(t, p, compositionRequest("invalid completion "+row.name))
				merr := log.firstModelErr()
				if merr == nil {
					t.Fatalf("invalid completion yielded no ModelError; the whole pending batch must be refused with one ClassPermanent error")
				}
				if merr.Class != types.ClassPermanent {
					t.Errorf("refusal class %d, want ClassPermanent", merr.Class)
				}
				if merr.Provider != fixtureProfile {
					t.Errorf("refusal provider %q, want %q", merr.Provider, fixtureProfile)
				}
				if n := len(log.errs); n != 1 {
					t.Errorf("invalid completion yielded %d errors, want exactly one", n)
				}
				if after := log.chunksAfterError(); after != 0 {
					t.Errorf("%d chunks yielded after the refusal; no complete call or usage may follow it", after)
				}
				for _, c := range log.chunks {
					if c.ToolUse != nil && strings.TrimSpace(string(c.ToolUse.Args)) != "" {
						t.Errorf("complete call with args %q emitted despite the invalid batch; whole-batch validation must withhold every call", string(c.ToolUse.Args))
					}
				}
				tool, evs, _ := nativeCase(t, row.raw)
				assertNoToolEffects(t, tool, evs)
			})
		}
		t.Run("tool_calls_valid_completion_control", func(t *testing.T) {
			raw := sse(toolFrame(0, "call-1", "echo", argsValid), finishFrame("tool_calls", usageTotals), sseDone)
			p := fixtureProvider(t, proofCaps(nil), &sseTransport{status: 200, raw: raw})
			log := drain(t, p, compositionRequest("valid completion control"))
			var completes []types.ToolUse
			for _, c := range log.chunks {
				if c.ToolUse != nil && c.Kind != types.DeltaToolArgs {
					completes = append(completes, *c.ToolUse)
				}
			}
			if len(completes) != 1 || completes[0].ID != "call-1" || string(completes[0].Args) != `{"q":"go"}` {
				t.Errorf("valid completion produced %v, want one call-1({\"q\":\"go\"})", completes)
			}
			if u := assertSingleUsage(t, log, false); u.InputTokens != 40 || u.OutputTokens != 10 {
				t.Errorf("reported totals (%d,%d), want (40,10)", u.InputTokens, u.OutputTokens)
			}
			tool, evs, sendErr := nativeCase(t, raw)
			if sendErr != nil {
				t.Fatalf("native send: %v", sendErr)
			}
			if tool.callCount() != 1 {
				t.Errorf("recording tool executed %d times, want the one authorized call", tool.callCount())
			}
			for _, ev := range evs {
				if started, ok := ev.(types.ToolStarted); ok && started.Call.ID != "call-1" {
					t.Errorf("executed call id %q, want call-1", started.Call.ID)
				}
			}
		})
		t.Run("forced_output_stays_text_control", func(t *testing.T) {
			raw := sse(textFrame(`{\"q\":\"go\"}`), finishFrame("stop", usageTotals), sseDone)
			p := fixtureProvider(t, proofCaps(nil), &sseTransport{status: 200, raw: raw})
			req := proofRequest("forced output control")
			req.Options.ResponseSchema = json.RawMessage(`{"type":"object"}`)
			log := drain(t, p, req)
			var text strings.Builder
			for _, c := range log.chunks {
				if c.Kind == types.DeltaText {
					text.WriteString(c.Delta)
				}
				if c.ToolUse != nil {
					t.Errorf("forced output synthesized tool %v; structured output must stay text", c.ToolUse)
				}
			}
			if !strings.Contains(text.String(), `{"q":"go"}`) {
				t.Errorf("forced output text %q lost the structured payload", text.String())
			}
			assertSingleUsage(t, log, false)
		})
	})

	t.Run("model.openai-consumer-stop-final", func(t *testing.T) {
		stopAt := func(name, raw string, reject func(types.ModelChunk, error) bool, mustReach bool) {
			t.Run(name, func(t *testing.T) {
				tr := &sseTransport{status: 200, raw: raw}
				p := fixtureProvider(t, proofCaps(nil), tr)
				log := &yieldLog{reject: reject}
				consumeGuarded(t, func() {
					for c, e := range p.Generate(context.Background(), proofRequest("consumer stop "+name)) {
						if !log.accept(c, e) {
							break
						}
					}
				})
				if mustReach && log.rejectedAt == 0 {
					t.Fatalf("stream never reached the rejection target after %d callbacks; the fixture chunk must arrive before usage", log.callsCount())
				}
				if after := log.callbacksAfterRejection(); after != 0 {
					t.Errorf("%d callbacks after the consumer returned false; iteration must return permanently at rejection", after)
				}
				if counts := tr.closeCounts(); len(counts) != 1 || counts[0] != 1 {
					t.Errorf("body close counts %v, want exactly one close for the acquired body", counts)
				}
			})
		}
		stopAt("reject_after_text", sse(textFrame("he"), reasoningFrame("hm"), finishFrame("stop", usageTotals), sseDone),
			func(c types.ModelChunk, _ error) bool { return c.Kind == types.DeltaText }, true)
		stopAt("reject_after_reasoning", sse(textFrame("he"), reasoningFrame("hm"), finishFrame("stop", usageTotals), sseDone),
			func(c types.ModelChunk, _ error) bool { return c.Kind == types.DeltaReasoning }, true)
		stopAt("reject_after_preview", sse(toolFrame(0, "call-1", "echo", argsOpen), toolFrame(0, "", "", `\"g`), finishFrame("tool_calls", usageTotals), sseDone),
			func(c types.ModelChunk, _ error) bool { return c.Kind == types.DeltaToolArgs }, true)
		stopAt("reject_after_completion", sse(toolFrame(0, "call-1", "echo", argsValid), finishFrame("tool_calls", usageTotals), sseDone),
			func(c types.ModelChunk, _ error) bool { return c.ToolUse != nil && c.Kind != types.DeltaToolArgs }, true)
		stopAt("reject_after_finish", sse(textFrame("ok"), finishFrame("stop", usageTotals), sseDone),
			func(c types.ModelChunk, _ error) bool { return c.Finish != "" }, true)
		stopAt("reject_after_usage", sse(textFrame("ok"), finishFrame("stop", ""), usageFrame(usageTotals), sseDone),
			func(c types.ModelChunk, _ error) bool { return c.Usage != nil }, true)
		t.Run("range_break_does_not_panic", func(t *testing.T) {
			tr := &sseTransport{status: 200, raw: sse(textFrame("a"), textFrame("b"), finishFrame("stop", usageTotals), sseDone)}
			p := fixtureProvider(t, proofCaps(nil), tr)
			consumeGuarded(t, func() {
				for range p.Generate(context.Background(), proofRequest("range break")) {
					break
				}
			})
			if counts := tr.closeCounts(); len(counts) != 1 || counts[0] != 1 {
				t.Errorf("body close counts %v, want exactly one close after the break", counts)
			}
		})
		t.Run("clean_done_reports_single_usage", func(t *testing.T) {
			raw := sse(textFrame("ok"), finishFrame("stop", ""), usageFrame(usageTotals), sseDone)
			p := fixtureProvider(t, proofCaps(nil), &sseTransport{status: 200, raw: raw})
			log := drain(t, p, compositionRequest("clean usage timing"))
			u := assertSingleUsage(t, log, false)
			if u.InputTokens != 40 || u.OutputTokens != 10 {
				t.Errorf("reported totals (%d,%d), want the provider totals (40,10) without a manufactured estimate", u.InputTokens, u.OutputTokens)
			}
		})
		t.Run("clean_done_estimates_once_with_counts", func(t *testing.T) {
			raw := sse(toolFrame(0, "call-1", "web_search", argsValid), finishFrame("tool_calls", ""), sseDone)
			p := fixtureProvider(t, proofCaps(nil), &sseTransport{status: 200, raw: raw})
			req := proofRequest("clean estimate")
			req.Tools = []types.ToolSpec{{Name: "web_search", Executor: types.ByProvider}}
			log := drain(t, p, req)
			u := assertSingleUsage(t, log, true)
			if u.ProviderToolCalls["web_search"] != 1 {
				t.Errorf("estimated usage provider-call counts %v, want web_search=1", u.ProviderToolCalls)
			}
			if u.ModelVersion != fixtureVersion {
				t.Errorf("estimated usage model version %q, want the served version %q", u.ModelVersion, fixtureVersion)
			}
		})
	})

	t.Run("model.openai-stream-error-final", func(t *testing.T) {
		errorCase := func(name string, tp openai.Transport) {
			t.Run(name, func(t *testing.T) {
				p := fixtureProvider(t, proofCaps(nil), tp)
				log := drain(t, p, compositionRequest("stream error "+name))
				if n := len(log.errs); n != 1 {
					t.Errorf("terminal failure yielded %d errors, want exactly one", n)
				}
				merr := log.firstModelErr()
				if merr == nil {
					t.Fatalf("terminal failure was swallowed; the iterator must yield one ModelError and return")
				}
				if after := log.chunksAfterError(); after != 0 {
					t.Errorf("%d chunks yielded after the terminal error; no estimated usage or completion may follow it", after)
				}
				if log.callsCount() != 2 {
					t.Errorf("consumer received %d callbacks, want the content chunk plus the single terminal error", log.callsCount())
				}
			})
		}
		errorCase("decode_error_no_usage_after", &sseTransport{status: 200, raw: sse(textFrame("ok"), "data: {broken}\n\n", sseDone)})
		errorCase("read_error_no_usage_after", readFailTransportFor(textFrame("ok"), errors.New("tracked read failure")))
		t.Run("clean_estimate_control", func(t *testing.T) {
			raw := sse(textFrame("ok"), finishFrame("stop", ""), sseDone)
			p := fixtureProvider(t, proofCaps(nil), &sseTransport{status: 200, raw: raw})
			log := drain(t, p, compositionRequest("clean estimate control"))
			assertSingleUsage(t, log, true)
		})
		t.Run("body_closes_once_on_every_exit", func(t *testing.T) {
			cases := []struct {
				name string
				tr   openai.Transport
			}{
				{"clean", &sseTransport{status: 200, raw: sse(textFrame("ok"), finishFrame("stop", usageTotals), sseDone)}},
				{"decode_error", &sseTransport{status: 200, raw: sse("data: {broken}\n\n", sseDone)}},
				{"read_error", readFailTransportFor(textFrame("ok"), errors.New("tracked read failure"))},
				{"http_error", &sseTransport{status: 500, raw: "server exploded"}},
			}
			for _, row := range cases {
				t.Run(row.name, func(t *testing.T) {
					p := fixtureProvider(t, proofCaps(nil), row.tr)
					drain(t, p, compositionRequest("closure matrix "+row.name))
					var counts []int
					switch tr := row.tr.(type) {
					case *sseTransport:
						counts = tr.closeCounts()
					}
					if len(counts) != 1 || counts[0] != 1 {
						t.Errorf("body close counts %v, want exactly one close on the %s exit", counts, row.name)
					}
				})
			}
		})
	})

	t.Run("model.openai-cache-details-absent", func(t *testing.T) {
		absent := func(name string, caps types.Caps, usageJSON string, check func(*testing.T, *types.Usage)) {
			t.Run(name, func(t *testing.T) {
				raw := sse(textFrame("ok"), finishFrame("stop", usageJSON), sseDone)
				p := fixtureProvider(t, caps, &sseTransport{status: 200, raw: raw})
				log := &yieldLog{}
				var panicValue any
				func() {
					defer func() { panicValue = recover() }()
					for c, e := range p.Generate(context.Background(), proofRequest("cache details "+name)) {
						log.accept(c, e)
					}
				}()
				if panicValue != nil {
					t.Errorf("usage frame panicked: %v; absent optional details must read as zero cache tokens", panicValue)
					return
				}
				reports := log.usageReports()
				if len(reports) != 1 {
					t.Fatalf("streamed %d usage reports, want exactly one", len(reports))
				}
				check(t, reports[0])
			})
		}
		absent("absent_details_zero_cached", proofCaps(func(c *types.Caps) { c.Cache = types.CacheAuto }), usageSmall,
			func(t *testing.T, u *types.Usage) {
				if u.InputTokens != 11 || u.OutputTokens != 7 {
					t.Errorf("reported totals (%d,%d), want the nonzero fixture totals (11,7)", u.InputTokens, u.OutputTokens)
				}
				if u.CachedInputTokens != 0 {
					t.Errorf("CachedInputTokens %d, want 0 without prompt-token details", u.CachedInputTokens)
				}
				if u.Estimated {
					t.Errorf("usage marked estimated; reported totals must stay unestimated")
				}
			})
		absent("present_details_counted_control", proofCaps(func(c *types.Caps) { c.Cache = types.CacheAuto }), usageSmallCmp,
			func(t *testing.T, u *types.Usage) {
				if u.CachedInputTokens != 4 {
					t.Errorf("CachedInputTokens %d, want the reported 4", u.CachedInputTokens)
				}
			})
		absent("cache_explicit_unchanged_control", proofCaps(func(c *types.Caps) { c.Cache = types.CacheExplicit }), usageSmallCmp,
			func(t *testing.T, u *types.Usage) {
				if u.CachedInputTokens != 4 || u.CacheWriteTokens != 4 {
					t.Errorf("explicit cache tokens (read %d, write %d), want (4,4) unchanged", u.CachedInputTokens, u.CacheWriteTokens)
				}
			})
	})

	t.Run("model.cancel-returns-promptly", func(t *testing.T) {
		t.Run("silent_cancel", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tr := newSilentTransport()
				p := fixtureProvider(t, proofCaps(nil), tr)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				log := &yieldLog{}
				done := make(chan *yieldLog, 1)
				go func() {
					for c, e := range p.Generate(ctx, proofRequest("silent cancel")) {
						log.accept(c, e)
					}
					done <- log
				}()
				<-tr.entered
				cancel()
				select {
				case <-done:
					if len(log.errs) != 1 || !errors.Is(log.errs[0], context.Canceled) {
						t.Errorf("silent cancellation produced errors %v; the iterator must report exactly one context.Canceled", log.errs)
					}
					for _, c := range log.chunks {
						if c != (types.ModelChunk{}) {
							t.Errorf("chunk %+v yielded after cancellation; no success chunks may follow silent cancellation", c)
						}
					}
					if counts := tr.closeCounts(); len(counts) != 1 || counts[0] != 1 {
						t.Errorf("body close counts %v, want exactly one close for the acquired body", counts)
					}
				case <-time.After(time.Second):
					t.Fatal("Generate did not return within the one-second read deadline after cancellation")
				}
			})
		})
		t.Run("cancel_before_generate", func(t *testing.T) {
			tr := &sseTransport{status: 200, raw: sse(textFrame("a"), sseDone)}
			p := fixtureProvider(t, proofCaps(nil), tr)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			defer cancel()
			log := &yieldLog{}
			for c, e := range p.Generate(ctx, proofRequest("cancel before generate")) {
				log.accept(c, e)
			}
			if len(log.errs) != 1 || !errors.Is(log.errs[0], context.Canceled) {
				t.Errorf("pre-cancelled context produced errors %v; Generate must report exactly one context.Canceled", log.errs)
			}
			for _, c := range log.chunks {
				if c != (types.ModelChunk{}) {
					t.Errorf("chunk %+v yielded for a pre-cancelled context, want no content chunk", c)
				}
			}
			if n := len(tr.bodies); n != 0 {
				t.Errorf("transport acquired %d bodies; a cancelled call must never reach the transport", n)
			}
		})
		t.Run("cancel_after_accept", func(t *testing.T) {
			raw := sse(textFrame("a"), textFrame("b"), finishFrame("stop", usageTotals), sseDone)
			tr := &sseTransport{status: 200, raw: raw}
			p := fixtureProvider(t, proofCaps(nil), tr)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			log := &yieldLog{}
			first := true
			for c, e := range p.Generate(ctx, proofRequest("cancel after accept")) {
				if !log.accept(c, e) {
					break
				}
				if first && c.Kind == types.DeltaText {
					first = false
					cancel()
				}
			}
			if len(log.errs) != 1 || !errors.Is(log.errs[0], context.Canceled) {
				t.Errorf("cancellation after an accepting callback produced errors %v; exactly one terminal context.Canceled must follow", log.errs)
			}
			if after := log.chunksAfterError(); after != 0 {
				t.Errorf("%d chunks yielded after the cancellation error; no usage or success may follow it", after)
			}
			if counts := tr.closeCounts(); len(counts) != 1 || counts[0] != 1 {
				t.Errorf("body close counts %v, want exactly one close", counts)
			}
		})
		t.Run("cancel_with_rejection", func(t *testing.T) {
			raw := sse(textFrame("a"), textFrame("b"), finishFrame("stop", usageTotals), sseDone)
			tr := &sseTransport{status: 200, raw: raw}
			p := fixtureProvider(t, proofCaps(nil), tr)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			log := &yieldLog{}
			for c, e := range p.Generate(ctx, proofRequest("cancel with rejection")) {
				log.accept(c, e)
				if c.Kind == types.DeltaText {
					cancel()
					break
				}
			}
			if n := log.callsCount(); n != 1 {
				t.Errorf("consumer received %d callbacks, want exactly the one rejecting callback; no callback may follow consumer rejection", n)
			}
			if counts := tr.closeCounts(); len(counts) != 1 || counts[0] != 1 {
				t.Errorf("body close counts %v, want exactly one close", counts)
			}
		})
	})
}
