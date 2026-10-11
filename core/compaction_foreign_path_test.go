package gohan

import (
	"context"
	"strings"
	"sync"
	"testing"

	stdcontext "github.com/victorzhuk/gohan/std/context"
	stdguard "github.com/victorzhuk/gohan/std/guard"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// foreignCompactionRT is the backend runtime stand-in of the foreign public
// path: one step that drives the run's governed model once and finishes.
// Direct mode fabricates the request from the durable history and calls
// Generate without ever passing harness assembly.
type foreignCompactionRT struct {
	mu     sync.Mutex
	ag     runtime.AgentRun
	direct bool
}

func (r *foreignCompactionRT) Name() string                         { return "compaction.test" }
func (r *foreignCompactionRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *foreignCompactionRT) Start(_ context.Context, ag runtime.AgentRun) (runtime.State, error) {
	r.mu.Lock()
	r.ag = ag
	r.mu.Unlock()
	return runtime.State{}, nil
}

func (r *foreignCompactionRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	ag, direct := r.ag, r.direct
	r.mu.Unlock()
	req := types.ModelRequest{Messages: append([]types.Message{}, ag.History.Messages...)}
	if !direct {
		var err error
		// The sent input is already durable history here; a backend
		// that folded ag.Input in would send it twice.
		req, err = ag.Assemble(ctx, types.AssembleInput{History: ag.History.Messages})
		if err != nil {
			return st, nil, runtime.Continue, err
		}
	}
	for _, err := range ag.Model.Generate(ctx, req) {
		if err != nil {
			return st, nil, runtime.Continue, err
		}
	}
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, runtime.DoneStatus, nil
}

// foreignPublicStackStores swaps the session log a second stack instance
// shares with the first one; the audit and event sinks stay fresh so the
// missing publication has somewhere to land.
func foreignPublicStackStores(sessions *stores.MemorySessionLog) stores.Stores {
	st := publicPathStores()
	if sessions != nil {
		st.SessionLog = sessions
	}
	return st
}

// foreignPublicStackOver builds one foreign stack over the given stores.
func foreignPublicStackOver(t *testing.T, st stores.Stores, model *publicPathModel, src *summarizingSource, newRT func() runtime.Runtime, extra ...Option) (*Stack, *stores.MemorySessionLog, *stores.MemoryAuditLog, *stores.MemoryEventLog) {
	t.Helper()
	policy := stores.ContextPolicy{}
	if src != nil {
		policy = stores.ContextPolicy{Compactor: stdcontext.NewSummarize(stdcontext.WithSummarizeSource(src.summarize))}
	}
	opts := append([]Option{
		WithModels(model),
		WithStores(st),
		WithForeignAgent(ForeignSpec{
			Request:       FlowRequest{Name: "chat"},
			Profile:       "public-compaction-test",
			Assemble:      publicPathAssemble,
			ContextPolicy: policy,
			NewRuntime:    newRT,
		}),
		WithCompactionFallback(shrinkProjection{drop: 4}),
		WithCompactionGuard(stdguard.NewContextGuard()),
	}, extra...)
	s, err := Build(opts...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	sl, _ := st.SessionLog.(*stores.MemorySessionLog)
	al, _ := st.Audit.(*stores.MemoryAuditLog)
	el, _ := st.EventLog.(*stores.MemoryEventLog)
	return s, sl, al, el
}

// foreignPublicStack builds one foreign stack over fresh public-path
// stores. A nil source declares no compactor: the flow resolves without
// one and reconciliation stays its only compaction duty. A non-nil audit
// replaces the memory audit sink.
func foreignPublicStack(t *testing.T, model *publicPathModel, src *summarizingSource, newRT func() runtime.Runtime, audit stores.AuditLog, extra ...Option) (*Stack, *stores.MemorySessionLog, *stores.MemoryAuditLog, *stores.MemoryEventLog) {
	t.Helper()
	st := publicPathStores()
	if audit != nil {
		st.Audit = audit
	}
	return foreignPublicStackOver(t, st, model, src, newRT, extra...)
}

func foreignPublicConversation(t *testing.T, stack *Stack, el *stores.MemoryEventLog) Conversation {
	t.Helper()
	conv, err := NewForeignConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	return conv
}

// publicPathSendErr drives one Send and reports the last error the stream
// delivered, so a refused run stays observable.
func publicPathSendErr(conv Conversation, ctx context.Context, sessionID, text string) error {
	var sendErr error
	for _, err := range conv.Send(ctx, sessionID, types.Message{Role: types.RoleUser, Blocks: []types.Block{modelText(text)}}) {
		if err != nil {
			sendErr = err
		}
	}
	return sendErr
}

// foreignCompactionSend drives one foreign Send through the public
// constructor over the real foreign AgentRun path and asserts the
// append-and-hide contract: the guarded summary persists once under the
// publication payload, the provider request is the reassembled view of the
// reloaded durable history, and the caller's lineage reaches the provider
// call.
func foreignCompactionSend(t *testing.T, ctx context.Context, sessionID string) {
	t.Helper()
	model := &publicPathModel{}
	src := &summarizingSource{summary: "s"}
	rt := &foreignCompactionRT{}
	stack, sessions, audit, el := foreignPublicStack(t, model, src, func() runtime.Runtime { return rt }, nil)
	conv := foreignPublicConversation(t, stack, el)
	if _, err := sessions.Append(ctx, sessionID, 0, publicPathTexts(40)...); err != nil {
		t.Fatal(err)
	}
	publicPathSend(t, conv, ctx, sessionID, strings.Repeat("b", 10))

	h, err := sessions.Load(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != 42 {
		t.Fatalf("history version %d: want 42, the publication over 41 messages", h.Version)
	}
	pub := publicationOf(t, h)
	if pub.FromVersion != 41 || pub.ToVersion != 42 {
		t.Fatalf("publication versions %d..%d: want 41..42", pub.FromVersion, pub.ToVersion)
	}
	if pub.TokensBefore != 410 {
		t.Fatalf("tokens before %d: want 410", pub.TokensBefore)
	}
	if len(model.reqs) != 1 {
		t.Fatalf("provider calls %d: want 1", len(model.reqs))
	}
	req := model.reqs[0]
	if len(req.Messages) != 2 || summaryHeads(req) != 1 {
		t.Fatalf("provider request %d messages, %d summaries: want the reloaded summary head plus the live tail", len(req.Messages), summaryHeads(req))
	}
	if len(src.newly) != 1 || src.bounds[0] != 40 {
		t.Fatalf("source runs %d, boundary %v: want one fold at the selected prefix 40", len(src.newly), src.bounds)
	}
	// The caller's lineage survives the foreign construction: the
	// provider call carries the caller principal and the run identity the
	// conversation acquired for this session.
	if len(model.ctxs) != 1 {
		t.Fatalf("model contexts %d: want 1", len(model.ctxs))
	}
	mctx := model.ctxs[0]
	if p, ok := types.PrincipalFrom(mctx); !ok || p.Tenant != "t1" || p.Subject != "u1" {
		t.Fatalf("model principal %+v ok=%v: want the caller principal", p, ok)
	}
	info, ok := types.RunInfoFrom(mctx)
	if !ok || info.SessionID != sessionID {
		t.Fatalf("model run info %+v ok=%v: want session %s", info, ok, sessionID)
	}
	records := publicPathAudit(t, audit, sessionID)
	if len(records) != 1 || records[0].Kind != stores.AuditCompacted || records[0].RunID != pub.RunID {
		t.Fatalf("audit records %+v: want one compaction record under the publishing run %s", records, pub.RunID)
	}
	compacted := 0
	for _, ev := range stageEventPayloads(t, el, pub.RunID) {
		if _, ok := ev.(types.Compacted); ok {
			compacted++
		}
	}
	if compacted != 1 {
		t.Fatalf("publication events %d: want 1 under the publishing run", compacted)
	}
}

// foreignInterruptedSend creates the interrupted publication through the
// public foreign compaction path: a configured audit sink refuses after
// the append, so the call fails before the provider and the durable block
// stays the commit point. It returns the durable payload and the session
// log the second stack instance loads the same session from.
func foreignInterruptedSend(t *testing.T, ctx context.Context, sessionID string) (stores.CompactionPublication, *stores.MemorySessionLog) {
	t.Helper()
	model := &publicPathModel{}
	src := &summarizingSource{summary: "s"}
	rt := &foreignCompactionRT{}
	stack, sessions, _, el := foreignPublicStack(t, model, src, func() runtime.Runtime { return rt }, failingAudit{stores.NewMemoryAuditLog()})
	conv := foreignPublicConversation(t, stack, el)
	if _, err := sessions.Append(ctx, sessionID, 0, publicPathTexts(40)...); err != nil {
		t.Fatal(err)
	}
	if err := publicPathSendErr(conv, ctx, sessionID, strings.Repeat("b", 10)); err == nil {
		t.Fatal("send with a down audit sink succeeded: want the refusal before the provider call")
	}
	if len(model.reqs) != 0 {
		t.Fatalf("provider calls %d: want 0, the sink failure refuses the call", len(model.reqs))
	}
	h, err := sessions.Load(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != 42 {
		t.Fatalf("history version %d: want 42, the append is the commit point", h.Version)
	}
	return publicationOf(t, h), sessions
}

// foreignReconciledSend runs a second foreign stack instance over the same
// session log and asserts the missing sink completes under the original
// publisher identity with no second block and no second publication.
func foreignReconciledSend(t *testing.T, ctx context.Context, sessions *stores.MemorySessionLog, sessionID string, pub stores.CompactionPublication) {
	t.Helper()
	model := &publicPathModel{}
	src := &summarizingSource{summary: "s"}
	rt := &foreignCompactionRT{}
	stack, _, audit, el := foreignPublicStackOver(t, foreignPublicStackStores(sessions), model, src, func() runtime.Runtime { return rt })
	conv := foreignPublicConversation(t, stack, el)
	publicPathSend(t, conv, ctx, sessionID, "c")

	records := publicPathAudit(t, audit, sessionID)
	if len(records) != 1 || records[0].Kind != stores.AuditCompacted {
		t.Fatalf("audit records %+v: want the reconciled compaction record", records)
	}
	if records[0].RunID != pub.RunID {
		t.Fatalf("audit run %s: want the original publisher %s", records[0].RunID, pub.RunID)
	}
	if n := len(stageEventPayloads(t, el, pub.RunID)); n != 1 {
		t.Fatalf("reconciled event payloads %d: want 1 under the original run", n)
	}
	h, err := sessions.Load(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range h.Messages {
		ok, err := publicationPresent(m)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("publications in history %d: want the original one only", count)
	}
	if n := summaryHeads(model.reqs[len(model.reqs)-1]); n != 1 {
		t.Fatalf("provider saw %d summaries: want the durable one only", n)
	}
}

// foreignReconciledDirectGenerate runs a foreign runtime whose Generate
// never passes harness assembly over a flow with no active compactor and
// asserts the direct call still reconciles the interrupted publication
// under the original publisher identity.
func foreignReconciledDirectGenerate(t *testing.T, ctx context.Context, sessionID string) {
	t.Helper()
	model := &publicPathModel{}
	rt := &foreignCompactionRT{direct: true}
	stack, sessions, audit, el := foreignPublicStack(t, model, nil, func() runtime.Runtime { return rt }, nil)
	conv := foreignPublicConversation(t, stack, el)

	summary := compactionSummary("prior")
	sha, err := stores.CompactionSummarySHA(summary)
	if err != nil {
		t.Fatal(err)
	}
	p := stores.CompactionPublication{
		SessionID: sessionID, RunID: "run-crash", RootRunID: "root-crash", Flow: "chat",
		Tenant: "t1", Subject: "u1", FromVersion: 1, ToVersion: 2,
		TokensBefore: 200, TokensAfter: 21, Policy: "public-compaction-test", SummarySHA: sha,
	}
	p.PublicationID = stores.CompactionPublicationID(p.SessionID, p.RunID, p.FromVersion)
	if _, err := sessions.Append(ctx, sessionID, 0, textMessage("seed")); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendCompaction(ctx, sessionID, 1, types.Message{
		ID:     p.PublicationID,
		Role:   types.RoleAssistant,
		Blocks: []types.Block{types.Compaction{Kind: types.CompactionText, CoversUpTo: 1, Summary: summary}},
		Meta:   map[string]any{stores.MetadataPayloadKey: p},
	}); err != nil {
		t.Fatal(err)
	}

	publicPathSend(t, conv, ctx, sessionID, "b")

	records := publicPathAudit(t, audit, sessionID)
	if len(records) != 1 || records[0].Kind != stores.AuditCompacted {
		t.Fatalf("audit records %+v: want the reconciled compaction record", records)
	}
	if records[0].RunID != "run-crash" {
		t.Fatalf("audit run %s: want the original publisher run-crash", records[0].RunID)
	}
	if n := len(stageEventPayloads(t, el, "run-crash")); n != 1 {
		t.Fatalf("reconciled event payloads %d: want 1 under the original run", n)
	}
	// The direct Generate never passed assembly: the provider received
	// the durable history verbatim, so the boundary binds at the model
	// call, not in assembly.
	req := model.reqs[len(model.reqs)-1]
	if len(req.Messages) != 3 {
		t.Fatalf("provider request %d messages: want the seed, the publication and the input verbatim", len(req.Messages))
	}
	h, err := sessions.Load(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != 3 {
		t.Fatalf("history version %d: want 3, reconciliation appends no second block", h.Version)
	}
}

func publicationPresent(m types.Message) (bool, error) {
	_, ok, err := decodePublicationMeta(m)
	return ok, err
}
