//go:build full

package main

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/examples/testinfra"
)

func s2Principal() types.Principal {
	return types.Principal{
		Tenant:  "local",
		Subject: "operator",
		Scopes:  []string{types.ScopeSessionRead, types.ScopeSessionWrite},
	}
}

type allowPolicy struct{}

func (allowPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

// allowMap lets the named calls run without approval; unnamed calls are
// denied so a transaction case never suspends.
type allowMap map[string]permission.Verdict

func (m allowMap) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	v, ok := m[inv.Spec.Name]
	if !ok {
		v = permission.DenyVerdict
	}
	return types.Decision[permission.Verdict]{Value: v, Confidence: 1}, nil
}

func s2Assemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: append([]types.Message(nil), in.History...)}, nil
}

// s2Model issues the scripted booking call on its first turn and answers
// with text once the history carries the booking's tool result.
type s2Model struct {
	mu       sync.Mutex
	call     types.ToolUse
	requests int
}

func (m *s2Model) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "scripted", Caps: types.Caps{Tools: true}}
}

func (m *s2Model) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.mu.Lock()
		m.requests++
		answered := false
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if _, ok := b.(types.ToolResult); ok {
					answered = true
				}
			}
		}
		call := m.call
		m.mu.Unlock()
		if answered {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "Booking recorded."}, nil)
			yield(types.ModelChunk{Finish: types.FinishStop}, nil)
			return
		}
		yield(types.ModelChunk{ToolUse: &call, Finish: types.FinishToolUse}, nil)
	}
}

// pgBooking is the S2 stand-in for the enterprise booking tool: it opens
// its own transaction on the store database, inserts the booking row, and
// completes the run's journal reservation on that same transaction, so
// the tool write and the journal completion commit or roll back together.
type pgBooking struct {
	db      *postgres.DB
	journal *postgres.Journal
	mu      sync.Mutex
	exec    int
}

func (b *pgBooking) Spec() types.ToolSpec {
	return types.ToolSpec{
		Name:        "create_booking",
		Description: "Book a mountain cabin.",
		Effect:      types.SideEffect,
		Risk:        types.RiskMedium,
	}
}

func (b *pgBooking) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	session, _ := types.RunInfoFrom(ctx)
	tx, err := b.db.Begin(ctx)
	if err != nil {
		return types.ToolResult{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	key := types.CallKey{SessionID: session.SessionID, CallID: "create_booking/rifugio-12"}
	entry, created, err := b.journal.ReserveTx(ctx, tx, key, "create_booking", "fp:rifugio-12", args)
	if err != nil {
		return types.ToolResult{}, err
	}
	if !created {
		return entry.Result, nil
	}
	b.mu.Lock()
	b.exec++
	b.mu.Unlock()
	if _, err := tx.Exec(ctx, `INSERT INTO booking (id) VALUES ($1)`, key.CallID); err != nil {
		return types.ToolResult{}, err
	}
	res := types.ToolResult{
		Outcome: types.Succeeded,
		Content: []types.Block{types.Text{Text: "cabin booked"}},
	}
	if err := b.journal.CompleteTx(ctx, tx, key, res); err != nil {
		return types.ToolResult{}, err
	}
	return res, tx.Commit(ctx)
}

func s2Stack(t *testing.T, st testinfra.Stores, model types.Model, flow string, decider types.Decider[*permission.ToolInvocation, permission.Verdict]) (gohan.Conversation, *pgBooking, *gohan.Stack) {
	t.Helper()
	if _, err := st.Pool().Exec(context.Background(), `CREATE TABLE IF NOT EXISTS booking (id text PRIMARY KEY)`); err != nil {
		t.Fatalf("create booking table: %v", err)
	}
	booking := &pgBooking{db: st.DB, journal: st.Journal}
	spec := gohan.NativeSpec{
		Request:  gohan.FlowRequest{Name: flow},
		Profile:  "scripted",
		Assemble: s2Assemble,
		Tools:    []types.Tool{booking},
	}
	if decider != nil {
		spec.Decider = decider
	}
	stack, err := gohan.Build(
		gohan.WithStores(st.Ports()),
		gohan.WithModels(model),
		gohan.WithNativeAgent(spec),
		gohan.WithRecoveryRuntime(flow, runtime.NewNative()),
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, flow,
		gohan.WithConversationRuns(st.Runs),
		gohan.WithConversationEventLog(st.EventLog),
		gohan.WithConversationApprovalPolicy(allowPolicy{}),
	)
	if err != nil {
		t.Fatalf("NewNativeConversation: %v", err)
	}
	return conv, booking, stack
}

func s2BookingModel() *s2Model {
	return &s2Model{call: types.ToolUse{
		ID:   "call_booking",
		Name: "create_booking",
		Args: jsontext.Value(`{"cabin":"rifugio-12","nights":2}`),
	}}
}

func s2Send(t *testing.T, conv gohan.Conversation, session string) ([]types.Event, error) {
	t.Helper()
	var evs []types.Event
	ctx := types.WithPrincipal(context.Background(), s2Principal())
	err := func() error {
		for ev, err := range conv.Send(ctx, session, types.Message{
			Role:   types.RoleUser,
			Blocks: []types.Block{types.Text{Text: "Book the Rifugio 12 cabin for two nights."}},
		}) {
			if err != nil {
				return err
			}
			evs = append(evs, ev)
		}
		return nil
	}()
	return evs, err
}

func TestQuickstartS2Postgres(t *testing.T) {
	t.Run("transaction carries the tool write and the journal completion", func(t *testing.T) {
		st := testinfra.NewStores(t, "quickstart_s2_tx")
		conv, booking, _ := s2Stack(t, st, s2BookingModel(), "quickstart", allowMap{"create_booking": permission.Allow})
		evs, err := s2Send(t, conv, "tx")
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if !slicesDone(evs) {
			t.Fatalf("events %v, want a Done", evs)
		}
		if booking.exec != 1 {
			t.Fatalf("booking executed %d times, want 1", booking.exec)
		}
		if got := countRows(t, st, `SELECT count(*) FROM booking`); got != 1 {
			t.Fatalf("booking rows = %d, want 1", got)
		}
		if got := countRows(t, st, `SELECT count(*) FROM journal_record WHERE session_id = 'tx' AND call_id = 'create_booking/rifugio-12' AND state = 1`); got != 1 {
			t.Fatalf("completed journal entries = %d, want 1", got)
		}
	})

	t.Run("a replayed call reads the journal instead of executing again", func(t *testing.T) {
		st := testinfra.NewStores(t, "quickstart_s2_replay")
		model := s2BookingModel()
		conv, booking, _ := s2Stack(t, st, model, "quickstart", allowMap{"create_booking": permission.Allow})
		if _, err := s2Send(t, conv, "replay"); err != nil {
			t.Fatalf("first Send: %v", err)
		}
		if _, err := s2Send(t, conv, "replay"); err != nil {
			t.Fatalf("second Send: %v", err)
		}
		if booking.exec != 1 {
			t.Fatalf("booking executed %d times across two sends, want 1 replayed run", booking.exec)
		}
		if got := countRows(t, st, `SELECT count(*) FROM booking`); got != 1 {
			t.Fatalf("booking rows = %d, want 1", got)
		}
	})

	t.Run("cross-pod approval resumes a run recorded by another instance", func(t *testing.T) {
		schema := "quickstart_s2_cross"
		podA := testinfra.NewStores(t, schema)
		convA, bookingA, _ := s2Stack(t, podA, s2BookingModel(), "quickstart", nil)
		evs, err := s2Send(t, convA, "cross")
		if err != nil {
			t.Fatalf("pod A Send: %v", err)
		}
		var susp types.Suspended
		for _, ev := range evs {
			if s, ok := ev.(types.Suspended); ok {
				susp = s
			}
		}
		if susp.Token == "" {
			t.Fatalf("pod A events %v, want a suspension on the unapproved side effect", evs)
		}
		if bookingA.exec != 0 {
			t.Fatalf("booking executed before approval %d times, want 0", bookingA.exec)
		}
		if got := countRows(t, podA, `SELECT count(*) FROM booking`); got != 0 {
			t.Fatalf("booking rows before approval = %d, want 0", got)
		}

		// Pod B is a separate instance over the same store: a fresh
		// pool, a fresh stack, no in-process state from pod A.
		podB := testinfra.NewStores(t, schema)
		convB, bookingB, _ := s2Stack(t, podB, s2BookingModel(), "quickstart", nil)
		var done bool
		ctx := types.WithPrincipal(context.Background(), s2Principal())
		for ev, err := range convB.Resume(ctx, susp.Token, gohan.Approve()) {
			if err != nil {
				t.Fatalf("pod B Resume: %v", err)
			}
			if _, ok := ev.(types.Done); ok {
				done = true
			}
		}
		if !done {
			t.Fatal("pod B resume produced no Done")
		}
		if bookingB.exec != 1 {
			t.Fatalf("booking executed after approval %d times, want 1", bookingB.exec)
		}
		if got := countRows(t, podB, `SELECT count(*) FROM booking WHERE id = 'create_booking/rifugio-12'`); got != 1 {
			t.Fatalf("booking rows after approval = %d, want 1", got)
		}
		hist, err := podB.SessionLog.Load(ctx, "cross")
		if err != nil {
			t.Fatalf("pod B Load: %v", err)
		}
		var text strings.Builder
		for _, msg := range hist.Messages {
			if msg.Role != types.RoleAssistant {
				continue
			}
			for _, b := range msg.Blocks {
				if tb, ok := b.(types.Text); ok {
					text.WriteString(tb.Text)
				}
			}
		}
		if !strings.Contains(text.String(), "Booking recorded.") {
			t.Fatalf("pod B history assistant text %q, want the completed reply", text.String())
		}
	})

	t.Run("a pod kill mid-flight is recovered by another instance", func(t *testing.T) {
		schema := "quickstart_s2_kill"
		podA := testinfra.NewStores(t, schema)
		convA, bookingA, _ := s2Stack(t, podA, s2BookingModel(), "quickstart", allowMap{"create_booking": permission.Allow})
		ctx, cancel := context.WithCancel(types.WithPrincipal(context.Background(), s2Principal()))
		defer cancel()
		killed := false
		for ev, err := range convA.Send(ctx, "kill", types.Message{
			Role:   types.RoleUser,
			Blocks: []types.Block{types.Text{Text: "Book the Rifugio 12 cabin for two nights."}},
		}) {
			if err != nil {
				break
			}
			if tf, ok := ev.(types.ToolFinished); ok && !tf.Replayed {
				// The pod dies right after the journal recorded the
				// tool result: no Done, no Finish, no drain.
				killed = true
				cancel()
				break
			}
		}
		if !killed {
			t.Fatal("pod A stream ended before the tool finished, want a kill point")
		}
		if bookingA.exec != 1 {
			t.Fatalf("booking executed before the kill %d times, want 1", bookingA.exec)
		}
		if got := countRows(t, podA, `SELECT count(*) FROM journal_record WHERE session_id = 'kill' AND call_id = 'create_booking/rifugio-12' AND state = 1`); got != 1 {
			t.Fatalf("completed journal entries after the kill = %d, want 1", got)
		}

		podB := testinfra.NewStores(t, schema)
		ctxB := types.WithPrincipal(context.Background(), s2Principal())
		var stale []stores.Run
		deadline := time.Now().Add(90 * time.Second)
		for len(stale) == 0 && time.Now().Before(deadline) {
			var err error
			stale, err = podB.Runs.Stale(ctxB, stores.LeaseTTL, 10)
			if err != nil {
				t.Fatalf("Stale: %v", err)
			}
			if len(stale) == 0 {
				time.Sleep(500 * time.Millisecond)
			}
		}
		if len(stale) == 0 {
			t.Fatal("killed run never became stale for recovery")
		}
		_, bookingB, stackB := s2Stack(t, podB, s2BookingModel(), "quickstart", allowMap{"create_booking": permission.Allow})
		if err := stackB.Recover(ctxB, 10); err != nil {
			t.Fatalf("pod B Recover: %v", err)
		}
		if bookingB.exec != 0 {
			t.Fatalf("booking executed after recovery %d times, want 0: the recorded result replays", bookingB.exec)
		}
		if got := countRows(t, podB, `SELECT count(*) FROM booking WHERE id = 'create_booking/rifugio-12'`); got != 1 {
			t.Fatalf("booking rows after recovery = %d, want 1", got)
		}
		run, err := podB.Runs.ByID(ctxB, stale[0].RunID)
		if err != nil {
			t.Fatalf("pod B ByID: %v", err)
		}
		if run.State != stores.Finished {
			t.Fatalf("run state after recovery = %d, want Finished", run.State)
		}
		hist, err := podB.SessionLog.Load(ctxB, "kill")
		if err != nil {
			t.Fatalf("pod B Load: %v", err)
		}
		var text strings.Builder
		for _, msg := range hist.Messages {
			if msg.Role != types.RoleAssistant {
				continue
			}
			for _, b := range msg.Blocks {
				if tb, ok := b.(types.Text); ok {
					text.WriteString(tb.Text)
				}
			}
		}
		if !strings.Contains(text.String(), "Booking recorded.") {
			t.Fatalf("pod B history assistant text %q, want the completed reply", text.String())
		}
	})
}

func slicesDone(evs []types.Event) bool {
	for _, ev := range evs {
		if _, ok := ev.(types.Done); ok {
			return true
		}
	}
	return false
}

func countRows(t *testing.T, st testinfra.Stores, sql string) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
