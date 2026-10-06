package gohan

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

const consolidationShape = `{"type":"object"}`

// consolidateHook stands in for the embedder's adapter: it counts the Done
// invocations, skips a compaction-only history, and otherwise stores one
// entry and appends one audit record from the run identity the lifecycle
// passed. Its stores are the stack's, so the hook observes what a real
// consolidation would.
type consolidateHook struct {
	sessions *stores.MemorySessionLog
	store    *stores.MemoryNotes
	audit    *stores.MemoryAuditLog
	fail     error
	calls    int
	last     types.RunInfo
}

func newConsolidateHook(policy stores.MemoryPolicy) *consolidateHook {
	return &consolidateHook{
		sessions: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
		store:    stores.NewMemoryNotes(stores.WithMemoryNotesPolicy(policy)),
		audit:    stores.NewMemoryAuditLog(),
	}
}

func (h *consolidateHook) Run(ctx context.Context, ri types.RunInfo) error {
	h.calls++
	h.last = ri
	if h.fail != nil {
		return h.fail
	}
	hist, err := h.sessions.Load(ctx, ri.SessionID)
	if err != nil {
		return err
	}
	content := false
	for _, m := range hist.Messages {
		for _, b := range m.Blocks {
			if _, isCompaction := b.(types.Compaction); !isCompaction {
				content = true
			}
		}
	}
	if !content {
		return nil
	}
	key := stores.NotesKey{Scope: stores.ScopeSubject, Tenant: ri.Principal.Tenant, Subject: ri.Principal.Subject}
	if err := h.store.Put(ctx, key, stores.MemoryEntry{ID: "consolidated-" + ri.RunID, Value: json.RawMessage(`{"summary":"kept"}`), RunID: ri.RunID}); err != nil {
		return err
	}
	return h.audit.Append(ctx, stores.AuditRecord{
		Kind:      AuditMemoryConsolidate,
		SessionID: ri.SessionID,
		RunID:     ri.RunID,
		Tenant:    ri.Principal.Tenant,
		Subject:   ri.Principal.Subject,
	})
}

func (h *consolidateHook) entries(t *testing.T, ri types.RunInfo) []stores.MemoryEntry {
	t.Helper()
	key := stores.NotesKey{Scope: stores.ScopeSubject, Tenant: ri.Principal.Tenant, Subject: ri.Principal.Subject}
	got, err := h.store.Entries(context.Background(), key)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	return got
}

func (h *consolidateHook) auditRecords(t *testing.T, sessionID string) []stores.AuditRecord {
	t.Helper()
	var got []stores.AuditRecord
	for rec, err := range h.audit.Read(context.Background(), sessionID) {
		if err != nil {
			t.Fatalf("audit read: %v", err)
		}
		got = append(got, rec)
	}
	return got
}

func consolidationStack(t *testing.T, hook *consolidateHook, policy stores.MemoryPolicy) *Stack {
	t.Helper()
	stack, err := Build(
		AllowAnonymous(),
		WithConsolidator(func(w ConsolidationWiring) MemoryConsolidator {
			if w.Profile != "primary" || w.Policy.Schema == nil || w.Stack == nil {
				t.Errorf("wiring = %+v", w)
			}
			return hook
		}),
		WithStores(stores.Stores{
			SessionLog:  hook.sessions,
			Runs:        stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom)),
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			Notes:       hook.store,
			Audit:       hook.audit,
		}),
		WithModels(gohantest.NewScriptedModel(types.ModelProfile{Name: "primary"}, gohantest.Text("acknowledged"))),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "primary",
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
			MemoryPolicy: policy,
		}),
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return stack
}

func consolidationConv(t *testing.T, stack *Stack) Conversation {
	t.Helper()
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("NewNativeConversation: %v", err)
	}
	return conv
}

func consolidationSend(t *testing.T, conv Conversation, sessionID string) {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	for ev, err := range conv.Send(ctx, sessionID, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "remember the booking"}}}) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if _, done := ev.(types.Done); done {
			return
		}
	}
	t.Fatal("Send never reached Done")
}

func TestMemoryConsolidation(t *testing.T) {
	policy := stores.MemoryPolicy{Schema: json.RawMessage(consolidationShape)}
	owner := types.Principal{Tenant: "t1", Subject: "u1"}

	t.Run("working-state.memory-consolidate-at-done", func(t *testing.T) {
		hook := newConsolidateHook(policy)
		conv := consolidationConv(t, consolidationStack(t, hook, policy))
		consolidationSend(t, conv, "s1")

		if hook.calls != 1 {
			t.Fatalf("consolidations = %d, want 1", hook.calls)
		}
		if hook.last.SessionID != "s1" || hook.last.RunID == "" {
			t.Fatalf("run info = %+v", hook.last)
		}
		entries := hook.entries(t, hook.last)
		if len(entries) != 1 {
			t.Fatalf("entries = %d, want 1", len(entries))
		}
		if entries[0].ID != "consolidated-"+hook.last.RunID {
			t.Fatalf("entry id = %s, want consolidated run", entries[0].ID)
		}
		recs := hook.auditRecords(t, "s1")
		if len(recs) != 1 || recs[0].Kind != AuditMemoryConsolidate || recs[0].SessionID != "s1" {
			t.Fatalf("audit records = %+v", recs)
		}
	})

	t.Run("no memory policy consolidates nothing", func(t *testing.T) {
		hook := newConsolidateHook(stores.MemoryPolicy{})
		stack, err := Build(
			AllowAnonymous(),
			WithConsolidator(func(ConsolidationWiring) MemoryConsolidator {
				t.Error("factory ran for a flow without a memory policy")
				return hook
			}),
			WithStores(stores.Stores{
				SessionLog: hook.sessions,
				Runs:       stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom)),
				Notes:      hook.store,
				Audit:      hook.audit,
			}),
			WithModels(gohantest.NewScriptedModel(types.ModelProfile{Name: "primary"}, gohantest.Text("acknowledged"))),
			WithNativeAgent(NativeSpec{
				Request: FlowRequest{Name: "chat"},
				Profile: "primary",
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
				},
			}),
		)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		consolidationSend(t, consolidationConv(t, stack), "s-plain")
		if hook.calls != 0 {
			t.Fatalf("consolidations = %d, want 0", hook.calls)
		}
		if got := hook.entries(t, types.RunInfo{Principal: owner}); len(got) != 0 {
			t.Fatalf("entries = %v, want none", got)
		}
	})

	t.Run("compaction-only history stores nothing", func(t *testing.T) {
		hook := newConsolidateHook(policy)
		ctx := types.WithPrincipal(context.Background(), owner)
		if _, err := hook.sessions.Append(ctx, "s-comp", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Compaction{Summary: []types.Block{types.Text{Text: "earlier turns folded"}}}}}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		rec := &order{}
		lc := NewLifecycle(
			WithLifecycleRuns(&lcRuns{rec: rec}, stores.Lease{RunID: "run-1"}),
			WithLifecycleSession("s-comp"),
			WithLifecycleConsolidation(hook, hook.audit),
		)
		rt := &lcRuntime{script: []lcStep{{status: runtime.DoneStatus}}}
		var evs []types.Event
		for ev, err := range DriveLifecycle(ctx, lc, rt, lcRun(rt, rec)) {
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			evs = append(evs, ev)
		}
		if hook.calls != 1 {
			t.Fatalf("consolidations = %d, want 1", hook.calls)
		}
		if got := hook.entries(t, types.RunInfo{Principal: owner}); len(got) != 0 {
			t.Fatalf("entries = %v, want none", got)
		}
		if recs := hook.auditRecords(t, "s-comp"); len(recs) != 0 {
			t.Fatalf("audit records = %v, want none", recs)
		}
	})

	t.Run("consolidation failure never fails the run", func(t *testing.T) {
		hook := newConsolidateHook(policy)
		hook.fail = context.DeadlineExceeded
		conv := consolidationConv(t, consolidationStack(t, hook, policy))
		consolidationSend(t, conv, "s-fail")
		if hook.calls != 1 {
			t.Fatalf("consolidations = %d, want 1", hook.calls)
		}
		if got := hook.entries(t, hook.last); len(got) != 0 {
			t.Fatalf("entries = %v, want none", got)
		}
		recs := hook.auditRecords(t, "s-fail")
		if len(recs) != 1 || recs[0].Kind != AuditMemoryConsolidate {
			t.Fatalf("audit records = %+v, want one failure record", recs)
		}
	})
}
