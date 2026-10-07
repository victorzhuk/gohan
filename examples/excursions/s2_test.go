//go:build full

package main

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
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
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// allowPolicy wires an approval source so an Ask verdict can suspend and
// a principal-approved resume is accepted.
type allowPolicy struct{}

func (allowPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

// s2Booking is the book_cabin fixture rewired to Postgres: the booking
// insert and the run's journal completion commit on one transaction.
type s2Booking struct {
	db      *postgres.DB
	journal *postgres.Journal
	mu      sync.Mutex
	exec    int
}

func (b *s2Booking) Spec() types.ToolSpec {
	return types.ToolSpec{
		Name:        "book_cabin",
		Description: "Book a mountain cabin.",
		Effect:      types.SideEffect,
		Risk:        types.RiskMedium,
	}
}

func (b *s2Booking) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	session, _ := types.RunInfoFrom(ctx)
	tx, err := b.db.Begin(ctx)
	if err != nil {
		return types.ToolResult{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	key := types.CallKey{SessionID: session.SessionID, CallID: "book_cabin/rifugio-12"}
	entry, created, err := b.journal.ReserveTx(ctx, tx, key, "book_cabin", "fp:rifugio-12", args)
	if err != nil {
		return types.ToolResult{}, err
	}
	if !created {
		return entry.Result, nil
	}
	b.mu.Lock()
	b.exec++
	b.mu.Unlock()
	if _, err := tx.Exec(ctx, `INSERT INTO cabin_booking (id) VALUES ($1)`, key.CallID); err != nil {
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

func s2ExcursionModel() types.Model {
	call := types.ToolUse{ID: "call_book_cabin", Name: "book_cabin", Args: jsontext.Value(`{"cabin":"rifugio-12","nights":2}`)}
	return &scriptedTurns{turns: [][]types.ModelChunk{
		{{ToolUse: &call, Finish: types.FinishToolUse}},
		{{Kind: types.DeltaText, Delta: "Cabin booked through the governed path."}, {Finish: types.FinishStop}},
	}}
}

func s2ExcursionStack(t *testing.T, st testinfra.Stores, model types.Model, decider types.Decider[*permission.ToolInvocation, permission.Verdict]) (gohan.Conversation, *s2Booking, *gohan.Stack) {
	t.Helper()
	if _, err := st.Pool().Exec(context.Background(), `CREATE TABLE IF NOT EXISTS cabin_booking (id text PRIMARY KEY)`); err != nil {
		t.Fatalf("create cabin_booking table: %v", err)
	}
	booking := &s2Booking{db: st.DB, journal: st.Journal}
	search := gohantest.NewFakeTool("search_trails",
		gohantest.WithToolSpec(func(s *types.ToolSpec) {
			s.Description = "Look up marked trails in a region."
			s.Effect = types.ReadOnly
		}),
		gohantest.WithToolResult(types.ToolResult{
			Outcome: types.Succeeded,
			Content: []types.Block{types.Text{Text: "3 trails found"}},
		}),
	)
	spec := gohan.NativeSpec{
		Request: gohan.FlowRequest{Name: "excursions"},
		Profile: "scripted",
		Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Messages: append([]types.Message(nil), in.History...)}, nil
		},
		Tools: []types.Tool{search, booking},
	}
	if decider != nil {
		spec.Decider = decider
	}
	stack, err := gohan.Build(
		gohan.WithStores(st.Ports()),
		gohan.WithModels(model),
		gohan.WithNativeAgent(spec),
		gohan.WithRecoveryRuntime("excursions", runtime.NewNative()),
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "excursions",
		gohan.WithConversationRuns(st.Runs),
		gohan.WithConversationEventLog(st.EventLog),
		gohan.WithConversationApprovalPolicy(allowPolicy{}),
	)
	if err != nil {
		t.Fatalf("NewNativeConversation: %v", err)
	}
	return conv, booking, stack
}

func s2ExcursionSend(t *testing.T, conv gohan.Conversation, session string) ([]types.Event, error) {
	t.Helper()
	var evs []types.Event
	ctx := types.WithPrincipal(context.Background(), excursionPrincipal())
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

func countBookingRows(t *testing.T, st testinfra.Stores, sql string) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestExcursionsS2Postgres(t *testing.T) {
	t.Run("transaction carries the booking write and the journal completion", func(t *testing.T) {
		st := testinfra.NewStores(t, "excursions_s2_tx")
		conv, booking, _ := s2ExcursionStack(t, st, s2ExcursionModel(), policyDecider{"book_cabin": permission.Allow})
		evs, err := s2ExcursionSend(t, conv, "tx")
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		var done bool
		for _, ev := range evs {
			if _, ok := ev.(types.Done); ok {
				done = true
			}
		}
		if !done {
			t.Fatalf("events %v, want a Done", evs)
		}
		if booking.exec != 1 {
			t.Fatalf("booking executed %d times, want 1", booking.exec)
		}
		if got := countBookingRows(t, st, `SELECT count(*) FROM cabin_booking`); got != 1 {
			t.Fatalf("booking rows = %d, want 1", got)
		}
		if got := countBookingRows(t, st, `SELECT count(*) FROM journal_record WHERE session_id = 'tx' AND call_id = 'book_cabin/rifugio-12' AND state = 1`); got != 1 {
			t.Fatalf("completed journal entries = %d, want 1", got)
		}
	})

	t.Run("cross-pod approval resumes a run recorded by another instance", func(t *testing.T) {
		schema := "excursions_s2_cross"
		podA := testinfra.NewStores(t, schema)
		convA, booking, _ := s2ExcursionStack(t, podA, s2ExcursionModel(), nil)
		evs, err := s2ExcursionSend(t, convA, "cross")
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
		if booking.exec != 0 {
			t.Fatalf("booking executed before approval %d times, want 0", booking.exec)
		}

		podB := testinfra.NewStores(t, schema)
		convB, bookingB, _ := s2ExcursionStack(t, podB, s2ExcursionModel(), nil)
		var done bool
		ctx := types.WithPrincipal(context.Background(), excursionPrincipal())
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
		if got := countBookingRows(t, podB, `SELECT count(*) FROM cabin_booking WHERE id = 'book_cabin/rifugio-12'`); got != 1 {
			t.Fatalf("booking rows after approval = %d, want 1", got)
		}
	})

	t.Run("a pod kill mid-flight is recovered by another instance", func(t *testing.T) {
		schema := "excursions_s2_kill"
		podA := testinfra.NewStores(t, schema)
		convA, booking, _ := s2ExcursionStack(t, podA, s2ExcursionModel(), policyDecider{"book_cabin": permission.Allow})
		ctx, cancel := context.WithCancel(types.WithPrincipal(context.Background(), excursionPrincipal()))
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
				killed = true
				cancel()
				break
			}
		}
		if !killed {
			t.Fatal("pod A stream ended before the tool finished, want a kill point")
		}
		if booking.exec != 1 {
			t.Fatalf("booking executed before the kill %d times, want 1", booking.exec)
		}
		if got := countBookingRows(t, podA, `SELECT count(*) FROM journal_record WHERE session_id = 'kill' AND call_id = 'book_cabin/rifugio-12' AND state = 1`); got != 1 {
			t.Fatalf("completed journal entries after the kill = %d, want 1", got)
		}

		podB := testinfra.NewStores(t, schema)
		ctxB := types.WithPrincipal(context.Background(), excursionPrincipal())
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
		_, bookingB, stackB := s2ExcursionStack(t, podB, s2ExcursionModel(), policyDecider{"book_cabin": permission.Allow})
		if err := stackB.Recover(ctxB, 10); err != nil {
			t.Fatalf("pod B Recover: %v", err)
		}
		if bookingB.exec != 0 {
			t.Fatalf("booking executed after recovery %d times, want 0: the recorded result replays", bookingB.exec)
		}
		if got := countBookingRows(t, podB, `SELECT count(*) FROM cabin_booking WHERE id = 'book_cabin/rifugio-12'`); got != 1 {
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
		if !strings.Contains(text.String(), "Cabin booked through the governed path.") {
			t.Fatalf("pod B history assistant text %q, want the completed reply", text.String())
		}
	})
}
