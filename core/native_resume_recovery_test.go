package gohan

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

var (
	nrrOwner = types.Principal{Subject: "owner", Tenant: "t1"}
	nrrCtx   = func() context.Context {
		return types.WithPrincipal(context.Background(), nrrOwner)
	}
)

// bookTool is a SideEffect tool: the default batch gate asks for it, so
// the run suspends on HumanApproval before the first execution.
type bookTool struct {
	mu     sync.Mutex
	ranID  []string
	scopes []string
}

func (b *bookTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "book", Effect: types.SideEffect, RequiredScopes: b.scopes}
}

func (b *bookTool) Call(_ context.Context, args jsontext.Value) (types.ToolResult, error) {
	var in struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(args, &in)
	b.record(in.ID)
	return types.ToolResult{Content: []types.Block{types.Text{Text: "booked"}}, Outcome: types.Succeeded}, nil
}

func (b *bookTool) record(id string) {
	b.mu.Lock()
	b.ranID = append(b.ranID, id)
	b.mu.Unlock()
}

func (b *bookTool) ran() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.ranID...)
}

type nrrAllowPolicy struct{}

func (nrrAllowPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

// costRecordingMW observes the ledger the resumed drive carries in its
// context at each model call.
type costRecordingMW struct {
	mu  sync.Mutex
	saw []float64
}

func (m *costRecordingMW) middleware() types.ModelMiddleware {
	return func(next types.ModelFunc) types.ModelFunc {
		return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			if ls, ok := chains.LimitsStateFrom(ctx); ok {
				m.mu.Lock()
				m.saw = append(m.saw, ls.Cost())
				m.mu.Unlock()
			}
			return next(ctx, req)
		}
	}
}

func (m *costRecordingMW) costs() []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]float64(nil), m.saw...)
}

// nrrModel scripts the model: one scripted call per tool result the
// history already carries, done after the last one is answered.
type nrrModel struct {
	mu    sync.Mutex
	calls int
	steps []types.ToolUse
}

func (m *nrrModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *nrrModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.mu.Lock()
		m.calls++
		m.mu.Unlock()
		answered := 0
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if _, ok := b.(types.ToolResult); ok {
					answered++
				}
			}
		}
		if answered >= len(m.steps) {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
			yield(types.ModelChunk{Finish: types.FinishStop}, nil)
			return
		}
		call := m.steps[answered]
		yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &call}, nil)
		yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
	}
}

func (m *nrrModel) reset(steps ...types.ToolUse) {
	m.mu.Lock()
	m.calls = 0
	m.steps = steps
	m.mu.Unlock()
}

// costReportingRuns wraps the runs store so ByID reports the cost the
// record carries, the way a store persisting spend does.
type costReportingRuns struct {
	*stores.MemoryRuns
	cost float64
}

func (s *costReportingRuns) ByID(ctx context.Context, runID string) (stores.Run, error) {
	run, err := s.MemoryRuns.ByID(ctx, runID)
	if err != nil {
		return run, err
	}
	run.Cost = s.cost
	return run, nil
}

// nrrNoteTool is a ReadOnly tool the default gate allows, so its calls
// execute without a suspension.
type nrrNoteTool struct {
	mu     sync.Mutex
	ranID  []string
	scopes []string
}

func (n *nrrNoteTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "note", Effect: types.ReadOnly, RequiredScopes: n.scopes}
}

func (n *nrrNoteTool) Call(_ context.Context, args jsontext.Value) (types.ToolResult, error) {
	var in struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(args, &in)
	n.record(in.ID)
	return types.ToolResult{Content: []types.Block{types.Text{Text: "noted"}}, Outcome: types.Succeeded}, nil
}

func (n *nrrNoteTool) record(id string) {
	n.mu.Lock()
	n.ranID = append(n.ranID, id)
	n.mu.Unlock()
}

func (n *nrrNoteTool) ran() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.ranID...)
}

func nrrStack(t *testing.T, model *nrrModel, book *bookTool, note *nrrNoteTool, mw *costRecordingMW, runs stores.Runs, extra ...Option) (*Stack, Conversation) {
	t.Helper()
	if runs == nil {
		runs = stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)
	}
	opts := []Option{
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		WithModels(model),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{book, note},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	}
	if mw != nil {
		opts = append(opts, WithModelMiddleware(mw.middleware()))
	}
	stack, err := Build(append(opts, extra...)...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return stack, conv
}

func suspendToken(t *testing.T, conv Conversation) ResumeToken {
	t.Helper()
	var token ResumeToken
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send: no suspension")
	}
	return token
}

func nrrBook(id string) types.ToolUse {
	return types.ToolUse{ID: id, Name: "book", Args: jsontext.Value(`{"id":"` + id + `"}`)}
}

func nrrNote(id string) types.ToolUse {
	return types.ToolUse{ID: id, Name: "note", Args: jsontext.Value(`{"id":"` + id + `"}`)}
}

// A resumed run drives a fresh governed runtime over the resolved
// configuration: it advances from the persisted phase, the run suspends
// again on the next side-effecting call, an allowed tool executes once
// across the resume, and the second delivery finishes the run.
func TestNativeResumeSuspendsAgain(t *testing.T) {
	model := &nrrModel{}
	book := &bookTool{}
	note := &nrrNoteTool{}
	_, conv := nrrStack(t, model, book, note, nil, nil)
	model.reset(nrrBook("c1"), nrrNote("n1"), nrrBook("c2"))

	var token ResumeToken
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send: no suspension")
	}
	if ran := book.ran(); len(ran) != 0 {
		t.Fatalf("tool ran before delivery: %v", ran)
	}

	var second ResumeToken
	for ev, err := range conv.Resume(nrrCtx(), token, nrrDeliver()) {
		if err != nil {
			t.Fatalf("first Resume: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			second = s.Token
		}
	}
	if second == "" {
		t.Fatal("first Resume: run did not suspend again")
	}
	if ran := note.ran(); len(ran) != 1 || ran[0] != "n1" {
		t.Fatalf("note executions after first resume: %v, want [n1] once", ran)
	}
	if ran := book.ran(); len(ran) != 0 {
		t.Fatalf("book executions: %v, want none", ran)
	}

	var done bool
	for ev, err := range conv.Resume(nrrCtx(), second, nrrDeliver()) {
		if err != nil {
			t.Fatalf("second Resume: %v", err)
		}
		if _, ok := ev.(types.Done); ok {
			done = true
		}
	}
	if !done {
		t.Error("second Resume: no Done event")
	}
	if ran := note.ran(); len(ran) != 1 {
		t.Fatalf("note executions after second resume: %v, want still [n1] once", ran)
	}
}

// The resumed ledger is seeded from the cost the persisted run record
// reports: the model middleware on the resumed drive sees the spent cost
// already in the ledger the fresh run context carries.
func TestNativeResumeSeedsLedgerFromRecord(t *testing.T) {
	runs := &costReportingRuns{MemoryRuns: stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	), cost: 0.05}
	model := &nrrModel{}
	book := &bookTool{}
	note := &nrrNoteTool{}
	mw := &costRecordingMW{}
	_, conv := nrrStack(t, model, book, note, mw, runs)
	model.reset(nrrBook("c1"))

	token := suspendToken(t, conv)
	if len(mw.costs()) == 0 || mw.costs()[0] != 0 {
		t.Fatalf("first drive ledger: %v, want 0 at entry", mw.costs())
	}
	for err := range errors_Resume(nrrCtx(), conv, token) {
		t.Fatalf("Resume: %v", err)
	}
	if len(mw.costs()) < 2 {
		t.Fatalf("resume model calls: %d, want at least one resumed call", len(mw.costs()))
	}
	if got := mw.costs()[len(mw.costs())-1]; got < 0.05 {
		t.Fatalf("resumed ledger: got %v, want at least the seeded 0.05", got)
	}
}

// A run whose record reports no usage resumes with an empty ledger: the
// store record is the only seed, and none is invented.
func TestNativeResumeEmptyRecordStartsEmptyLedger(t *testing.T) {
	model := &nrrModel{}
	book := &bookTool{}
	note := &nrrNoteTool{}
	mw := &costRecordingMW{}
	_, conv := nrrStack(t, model, book, note, mw, nil)
	model.reset(nrrBook("c1"))

	token := suspendToken(t, conv)
	for err := range errors_Resume(nrrCtx(), conv, token) {
		t.Fatalf("Resume: %v", err)
	}
	if len(mw.costs()) < 2 {
		t.Fatalf("resume model calls: %d", len(mw.costs()))
	}
	if got := mw.costs()[len(mw.costs())-1]; got != 0 {
		t.Fatalf("resumed ledger: got %v, want the empty 0 an unreported record yields", got)
	}
}

// nrrDeliver is the wake the AwaitingBatch suspension resumes with: the
// delivered bytes become the pending calls' tool result.
func nrrDeliver() stores.ResumeInput {
	return Deliver(json.RawMessage(`approved`))
}

func errors_Resume(ctx context.Context, conv Conversation, token ResumeToken) <-chan error {
	ch := make(chan error, 8)
	go func() {
		defer close(ch)
		for _, err := range conv.Resume(ctx, token, nrrDeliver()) {
			if err != nil {
				ch <- err
			}
		}
	}()
	return ch
}

// nrrScopedCtx carries the originator plus the run identity the fresh
// Send's gate reads its principal from. Resume refuses any caller-supplied
// RunInfo, so the resumed drive's identity can only come from the
// checkpoint and the run record.
func nrrScopedCtx(p types.Principal) context.Context {
	return types.WithRunInfo(types.WithPrincipal(context.Background(), p), types.RunInfo{Flow: "chat", Principal: p})
}

// A resumed run carries its restored identity into the gate: the scope
// check reads the checkpoint originator's scopes, so an approved call on a
// scope-guarded tool executes even though the resuming caller carries no
// RunInfo.
func TestNativeResumeRestoresRunIdentity(t *testing.T) {
	owner := types.Principal{Subject: "owner", Tenant: "t1", Scopes: []string{"invoice.send"}}
	model := &nrrModel{}
	book := &bookTool{}
	note := &nrrNoteTool{scopes: []string{"invoice.send"}}
	_, conv := nrrStack(t, model, book, note, nil, nil)
	model.reset(nrrBook("c1"), nrrNote("n2"))

	var token ResumeToken
	for ev, err := range conv.Send(nrrScopedCtx(owner), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send: no suspension")
	}
	if ran := note.ran(); len(ran) != 0 {
		t.Fatalf("note executions before delivery: %v, want none", ran)
	}

	var done bool
	for ev, err := range conv.Resume(nrrCtx(), token, nrrDeliver()) {
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, ok := ev.(types.Done); ok {
			done = true
		}
	}
	if !done {
		t.Error("Resume: no Done event")
	}
	if ran := note.ran(); len(ran) != 1 || ran[0] != "n2" {
		t.Fatalf("re-issued call executions after resume: %v, want [n2]", ran)
	}
}

// The restored identity governs the replay, not the caller's: an
// originator whose scopes do not cover the tool is denied on the resumed
// drive even when the suspension was reached under a caller-supplied
// identity that carried the scope.
func TestNativeResumeUnscopedOriginatorDenies(t *testing.T) {
	// The caller supplies a scoped run identity, but the transport
	// principal the checkpoint records as originator carries no scope.
	caller := types.Principal{Subject: "owner", Tenant: "t1", Scopes: []string{"invoice.send"}}
	originator := types.Principal{Subject: "owner", Tenant: "t1"}
	model := &nrrModel{}
	book := &bookTool{}
	note := &nrrNoteTool{scopes: []string{"invoice.send"}}
	_, conv := nrrStack(t, model, book, note, nil, nil)
	model.reset(nrrBook("c1"), nrrNote("n2"))
	sendCtx := types.WithRunInfo(
		types.WithPrincipal(context.Background(), originator),
		types.RunInfo{Flow: "chat", Principal: caller},
	)

	var token ResumeToken
	for ev, err := range conv.Send(sendCtx, "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send: no suspension")
	}

	for ev, err := range conv.Resume(nrrCtx(), token, nrrDeliver()) {
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_ = ev
	}
	if ran := note.ran(); len(ran) != 0 {
		t.Fatalf("unscoped originator executions after resume: %v, want none", ran)
	}
}

// A recovered drive re-issues the run's identity from stored state, so the
// gate's scope check sees the restored principal: a call the original run
// was allowed executes on the replay, and an originator without the scope
// is denied rather than granted.
func TestRecoveredDriveRestoresRunIdentity(t *testing.T) {
	setup := func(t *testing.T, scopes []string) (*Stack, *nrrNoteTool, stores.Lease, stores.Run, int64) {
		t.Helper()
		owner := types.Principal{Subject: "owner", Tenant: "t1", Scopes: scopes}
		model := &nrrModel{}
		book := &bookTool{}
		note := &nrrNoteTool{scopes: []string{"invoice.send"}}
		stack, _ := nrrStack(t, model, book, note, nil, nil, WithRecoveryRuntime("chat", runtime.NewNative()))
		ver, err := stack.stores.SessionLog.Append(nrrScopedCtx(owner), "s1", 0, nlUser("go"))
		if err != nil {
			t.Fatalf("seed history: %v", err)
		}
		run := stores.Run{SessionID: "s1", RunID: "r1", Flow: "chat", State: stores.Running, Turn: 1, Seq: 1}
		lease, err := stack.stores.Runs.Start(nrrScopedCtx(owner), run, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("seed run: %v", err)
		}
		return stack, note, lease, run, ver
	}

	t.Run("the replayed call executes under the restored identity", func(t *testing.T) {
		stack, note, lease, run, ver := setup(t, []string{"invoice.send"})
		owner := types.Principal{Subject: "owner", Tenant: "t1", Scopes: []string{"invoice.send"}}
		cp := &stores.Checkpoint{SessionID: "s1", RunID: "r1", Flow: "chat", Originator: owner}
		backend, err := json.Marshal(struct {
			Phase string `json:"phase"`
		}{"batch"})
		if err != nil {
			t.Fatalf("encode phase: %v", err)
		}
		st := runtime.State{Backend: backend, Turn: 1, Pending: []types.ToolUse{nrrNote("n1")}, HistoryVersion: ver}
		rctx, p, err := stack.recoveryContext(nrrScopedCtx(owner), run, st, cp)
		if err != nil {
			t.Fatalf("recovery context: %v", err)
		}
		if err := stack.driveRecovered(rctx, lease, run, runtime.NewNative(), st, cp, stores.ResumeInput{}, p); err != nil {
			t.Fatalf("driveRecovered: %v", err)
		}
		if ran := note.ran(); len(ran) != 1 || ran[0] != "n1" {
			t.Fatalf("replayed call executions: %v, want [n1]", ran)
		}
	})

	t.Run("an unscoped identity denies instead of grants", func(t *testing.T) {
		stack, note, lease, run, ver := setup(t, nil)
		owner := types.Principal{Subject: "owner", Tenant: "t1"}
		cp := &stores.Checkpoint{SessionID: "s1", RunID: "r1", Flow: "chat", Originator: owner}
		backend, err := json.Marshal(struct {
			Phase string `json:"phase"`
		}{"batch"})
		if err != nil {
			t.Fatalf("encode phase: %v", err)
		}
		st := runtime.State{Backend: backend, Turn: 1, Pending: []types.ToolUse{nrrNote("n1")}, HistoryVersion: ver}
		rctx, p, err := stack.recoveryContext(nrrScopedCtx(owner), run, st, cp)
		if err != nil {
			t.Fatalf("recovery context: %v", err)
		}
		if err := stack.driveRecovered(rctx, lease, run, runtime.NewNative(), st, cp, stores.ResumeInput{}, p); err != nil {
			t.Fatalf("driveRecovered: %v", err)
		}
		if ran := note.ran(); len(ran) != 0 {
			t.Fatalf("unscoped replay executions: %v, want none", ran)
		}
	})
}
