package gohan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// stageCompactor folds the prefix into one summary block stamped with the
// boundary it was given, so a CoversUpTo mismatch is observable.
type stageCompactor struct{ mismatch bool }

func (c stageCompactor) Compact(_ context.Context, h stores.History, _ types.ModelProfile) (types.Compaction, error) {
	boundary := h.Version
	if c.mismatch {
		boundary++
	}
	return types.Compaction{
		BlockBase:  types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
		CoversUpTo: boundary,
		Kind:       types.CompactionText,
		Summary:    compactionSummary("summary"),
		Model:      "stage-test",
	}, nil
}

// stageGuard decides by script and records the consultations.
type stageGuard struct {
	action guards.GuardAction
	blocks []types.Block
	seen   int
}

func (g *stageGuard) Decide(_ context.Context, _ guards.GuardInput) (types.Decision[guards.GuardVerdict], error) {
	g.seen++
	return types.Decision[guards.GuardVerdict]{Value: guards.GuardVerdict{Action: g.action, Blocks: g.blocks, Reason: "stage-test"}}, nil
}

// shrinkProjection drops the first n messages, so the fallback shrinks the
// request below the budget.
type shrinkProjection struct{ drop int }

func (p shrinkProjection) Project(_ context.Context, h stores.History, _ types.ModelProfile) (stores.History, error) {
	if p.drop > len(h.Messages) {
		p.drop = len(h.Messages)
	}
	h.Messages = h.Messages[p.drop:]
	return h, nil
}

// ValidateCompactionFallback accepts every profile, so the fixture never
// fails the build gate it is not exercising.
func (p shrinkProjection) ValidateCompactionFallback(types.ModelProfile) error { return nil }

// flakyCount returns 7 for the trigger, the summary, and the prospective
// passes, then 20, so the same-turn measurement disagrees with the durable
// payload.
type flakyCount struct{ calls int }

func (f *flakyCount) count(_ context.Context, _ types.ModelRequest) (int, error) {
	f.calls++
	if f.calls <= 4 {
		return 7, nil
	}
	return 20, nil
}

// blockCount counts the system blocks plus one per message block.
func blockCount(req types.ModelRequest) int {
	n := len(req.System)
	for _, m := range req.Messages {
		n += len(m.Blocks)
	}
	return n
}

func stageRunInfo() types.RunInfo {
	return types.RunInfo{
		Flow:      "stage-flow",
		SessionID: "s1",
		RunID:     "run-1",
		RootRunID: "root-1",
		Principal: types.Principal{Tenant: "t1", Subject: "u1"},
	}
}

func stageTexts(n int) []types.Message {
	msgs := make([]types.Message, 0, n)
	for i := range n {
		msgs = append(msgs, textMessage(strings.Repeat("x", i+1)))
	}
	return msgs
}

func stageSessionLog() *stores.MemorySessionLog {
	return stores.NewMemorySessionLog(stores.WithSessionPrincipals(func(context.Context) (types.Principal, bool) {
		return types.Principal{Tenant: "t1", Subject: "u1", Scopes: []string{"session:read", "session:write"}}, true
	}))
}

// stageConfig builds a drive over the memory stores with the compaction
// seams bound the way nativeTurnConfig binds them.
func stageConfig(t *testing.T, ctx context.Context, msgs []types.Message, compactor stores.Compactor, guard guards.Guard, fallback stores.CompactionFallback, count func(context.Context, types.ModelRequest) (int, error)) (turnConfig, *stores.MemorySessionLog, *stores.MemoryAuditLog, *stores.MemoryEventLog) {
	t.Helper()
	sessions := stageSessionLog()
	audit := stores.NewMemoryAuditLog()
	events := stores.NewMemoryEventLog()
	return turnConfig{
		model:            stageDoneModel(),
		assemble:         collapseBeforeProjections(types.ModelProfile{Name: "stage-test"}, turnAssemble(nil)),
		history:          msgs,
		maxTurns:         3,
		profile:          types.ModelProfile{Name: "stage-test", ContextWindow: 6},
		compactor:        compactor,
		contextGuard:     guard,
		compactionCount:  count,
		compactionAppend: stageAppendCompaction(sessions, msgs),
		compactionAudit:  audit,
		compactionEvents: events,
		compactionLoad: func(ctx context.Context, sessionID string) ([]types.Message, int64, error) {
			h, err := sessions.Load(ctx, sessionID)
			if err != nil {
				return nil, 0, err
			}
			return h.Messages, h.Version, nil
		},
		compactionFallback: fallback,
	}, sessions, audit, events
}

// stageAppendCompaction adapts the session compaction seam to the drive
// harness: the store holds no session until the stage appends, and the
// expected version is the loaded one.
func stageAppendCompaction(sessions *stores.MemorySessionLog, msgs []types.Message) func(context.Context, string, int64, types.Message) (int64, error) {
	return func(ctx context.Context, sessionID string, expected int64, msg types.Message) (int64, error) {
		if _, err := sessions.Load(ctx, sessionID); err != nil {
			if _, aerr := sessions.Append(ctx, sessionID, 0, msgs...); aerr != nil {
				return 0, aerr
			}
			expected = int64(len(msgs))
		}
		return sessions.AppendCompaction(ctx, sessionID, expected, msg)
	}
}

// stageDoneModel scripts one final text turn.
func stageDoneModel() types.ModelFunc {
	turn := turnTextChunk("done")
	return (&scriptTurns{turns: [][]types.ModelChunk{{turn}}}).model()
}

func stageContext(ctx context.Context) context.Context {
	return types.WithRunInfo(ctx, stageRunInfo())
}

// stageAuditRecords drains the audit log for the stage session.
func stageAuditRecords(t *testing.T, a *stores.MemoryAuditLog) []stores.AuditRecord {
	t.Helper()
	var out []stores.AuditRecord
	for rec, err := range a.Read(context.Background(), "s1") {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
	}
	return out
}

// stageEventPayloads drains the event log of one publishing run.
func stageEventPayloads(t *testing.T, e *stores.MemoryEventLog, runID string) []types.Event {
	t.Helper()
	var out []types.Event
	for ev, err := range e.Read(context.Background(), runID, 0) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev.Payload)
	}
	return out
}

func TestCompactionStage(t *testing.T) {
	ctx := stageContext(context.Background())

	t.Run("context.compaction-safe-prefix", func(t *testing.T) {
		msgs := []types.Message{
			textMessage("u1"),
			{ID: "a1", Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: "c1", Name: "echo"}}},
			{ID: "r1", Role: types.RoleUser, Blocks: []types.Block{types.ToolResult{ID: "c1", Outcome: types.Succeeded}}},
			{ID: "a2", Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: "c2", Name: "echo"}}},
			textMessage("u2"),
		}
		boundary, ok := selectCompactionPrefix(msgs)
		if !ok || boundary != 3 {
			t.Fatalf("boundary %d ok=%v: want 3, the pair covered whole and the pending call left out", boundary, ok)
		}
		if _, ok := selectCompactionPrefix([]types.Message{textMessage("only")}); ok {
			t.Fatal("covered the last message")
		}
		covered := append(stageTexts(3), compactionMessage(2, "prior"))
		if b, ok := selectCompactionPrefix(covered); !ok || b != 3 {
			t.Fatalf("recovered boundary %d ok=%v: want 3", b, ok)
		}
		// A result matched only through its Ref never closes its call: the
		// pair stays open, so the boundary covers the text before the call
		// and stops there.
		refOnly := []types.Message{
			textMessage("u1"),
			{ID: "a1", Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: "c1", Name: "echo"}}},
			{ID: "r1", Role: types.RoleUser, Blocks: []types.Block{types.ToolResult{Ref: "c1", Outcome: types.Succeeded}}},
			textMessage("u2"),
		}
		if b, ok := selectCompactionPrefix(refOnly); !ok || b != 1 {
			t.Fatalf("ref-matched boundary %d ok=%v: want 1, the pair left whole", b, ok)
		}
		// A prior summary or an internal record stops the prefix before it,
		// so its absolute position stays outside the fold.
		blocked := []types.Message{
			textMessage("u1"),
			textMessage("u2"),
			{ID: "prior", Role: types.RoleSystem, Blocks: []types.Block{types.Compaction{Kind: types.CompactionText, CoversUpTo: 1, Summary: compactionSummary("prior")}}},
			textMessage("u3"),
		}
		// A prior summary never folds: the scan skips it, keeps its
		// absolute position outside the source, and the boundary past it
		// lets the next compaction advance.
		if b, ok := selectCompactionPrefix(blocked); !ok || b != 3 {
			t.Fatalf("summary-skipped boundary %d ok=%v: want 3, the prior summary left outside the fold", b, ok)
		}
		internal := []types.Message{
			textMessage("u1"),
			{ID: "rcpt", Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: approvalReceipt{}}},
			textMessage("u2"),
		}
		if b, ok := selectCompactionPrefix(internal); !ok || b != 1 {
			t.Fatalf("internal-record boundary %d ok=%v: want 1, the receipt left outside the fold", b, ok)
		}
	})

	t.Run("context.compaction-stage-publishes", func(t *testing.T) {
		msgs := stageTexts(8)
		cfg, sessions, audit, events := stageConfig(t, ctx, msgs, stageCompactor{}, &stageGuard{action: guards.Pass}, nil, func(_ context.Context, req types.ModelRequest) (int, error) {
			return blockCount(req), nil
		})
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, sinkInto(new([]types.Event))), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		h, err := sessions.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != 9 {
			t.Fatalf("history version %d: want 9, one compaction append over the working history", h.Version)
		}
		pub, ok, err := decodePublicationMeta(h.Messages[len(h.Messages)-1])
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("durable message carries no publication payload")
		}
		if pub.RunID != "run-1" || pub.ToVersion != pub.FromVersion+1 {
			t.Fatalf("publication %+v: want the publishing run identity", pub)
		}
		if pub.TokensBefore != 8 || pub.TokensAfter == 0 {
			t.Fatalf("publication tokens before=%d after=%d", pub.TokensBefore, pub.TokensAfter)
		}
		wantSHA, err := stores.CompactionSummarySHA(compactionSummary("summary"))
		if err != nil {
			t.Fatal(err)
		}
		if pub.SummarySHA != wantSHA {
			t.Fatalf("summary sha %s: want %s", pub.SummarySHA, wantSHA)
		}
		records := stageAuditRecords(t, audit)
		if len(records) != 1 {
			t.Fatalf("audit records %d: want 1", len(records))
		}
		payloads := stageEventPayloads(t, events, "run-1")
		if len(payloads) != 1 {
			t.Fatalf("event payloads %d: want 1", len(payloads))
		}
		if _, ok := payloads[0].(types.Compacted); !ok {
			t.Fatalf("event payload %v: want Compacted", payloads[0])
		}
	})

	t.Run("context.summary-guarded", func(t *testing.T) {
		msgs := stageTexts(8)
		guard := &stageGuard{action: guards.Block}
		cfg, sessions, _, _ := stageConfig(t, ctx, msgs, stageCompactor{}, guard, shrinkProjection{drop: 6}, func(_ context.Context, req types.ModelRequest) (int, error) {
			return blockCount(req), nil
		})
		var evs []types.Event
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(types.WithSink(ctx, sinkInto(&evs)), cfg, y) }); err != nil {
			t.Fatalf("drive: %v", err)
		}
		if _, err := sessions.Load(ctx, "s1"); !errors.Is(err, stores.ErrSessionNotFound) {
			t.Fatalf("load err %v: want the session absent, a rejected summary persists nothing", err)
		}
		if guard.seen != 1 {
			t.Fatalf("guard consultations %d: want 1", guard.seen)
		}
		blocked := false
		for _, ev := range evs {
			if gb, ok := ev.(types.GuardBlocked); ok && gb.Stage == types.StageContext {
				blocked = true
			}
		}
		if !blocked {
			t.Fatalf("events %v: want GuardBlocked at StageContext", evs)
		}
	})

	t.Run("context.compaction-fallback-oversized", func(t *testing.T) {
		msgs := stageTexts(8)
		cfg, _, _, _ := stageConfig(t, ctx, msgs, stageCompactor{}, &stageGuard{action: guards.Block}, shrinkProjection{drop: 0}, func(_ context.Context, req types.ModelRequest) (int, error) {
			return blockCount(req), nil
		})
		_, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, sinkInto(new([]types.Event))), cfg, y)
		})
		if !errors.Is(err, errCompactionOversized) {
			t.Fatalf("err %v: want errCompactionOversized", err)
		}
	})

	t.Run("context.compaction-measure-mismatch-failclosed", func(t *testing.T) {
		msgs := stageTexts(8)
		count := &flakyCount{}
		cfg, sessions, audit, events := stageConfig(t, ctx, msgs, stageCompactor{}, &stageGuard{action: guards.Pass}, nil, count.count)
		_, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, sinkInto(new([]types.Event))), cfg, y)
		})
		if !errors.Is(err, errCompactionMeasure) {
			t.Fatalf("err %v: want errCompactionMeasure", err)
		}
		if len(stageAuditRecords(t, audit)) != 0 {
			t.Fatal("a mismatched measurement published to the audit sink")
		}
		if len(stageEventPayloads(t, events, "run-1")) != 0 {
			t.Fatal("a mismatched measurement published to the event sink")
		}
		if _, lerr := sessions.Load(ctx, "s1"); lerr == nil {
			t.Log("session may hold the durable block; no sink record may exist")
		}
	})

	t.Run("context.compaction-publication-survives-crash", func(t *testing.T) {
		sessions := stageSessionLog()
		audit := stores.NewMemoryAuditLog()
		events := stores.NewMemoryEventLog()
		summary := compactionSummary("prior")
		msg, payload, err := func() (types.Message, stores.CompactionPublication, error) {
			sha, err := stores.CompactionSummarySHA(summary)
			if err != nil {
				return types.Message{}, stores.CompactionPublication{}, err
			}
			p := stores.CompactionPublication{
				SessionID: "s1", RunID: "run-0", RootRunID: "root-0", Flow: "stage-flow",
				Tenant: "t1", Subject: "u1", FromVersion: 1, ToVersion: 2,
				TokensBefore: 10, TokensAfter: 4, Policy: "stage-test", SummarySHA: sha,
			}
			p.PublicationID = stores.CompactionPublicationID("s1", "run-0", 1)
			return types.Message{ID: p.PublicationID, Role: types.RoleSystem, Blocks: []types.Block{types.Compaction{Kind: types.CompactionText, CoversUpTo: 1, Summary: summary}}, Meta: map[string]any{stores.MetadataPayloadKey: p}}, p, nil
		}()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sessions.Append(ctx, "s1", 0, textMessage("seed")); err != nil {
			t.Fatal(err)
		}
		if _, err := sessions.AppendCompaction(ctx, "s1", 1, msg); err != nil {
			t.Fatal(err)
		}
		h, err := sessions.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		// A flow with no compactor and no policy still reconciles every
		// durable publication before the provider call.
		cfg := turnConfig{
			model:            stageDoneModel(),
			assemble:         turnAssemble(nil),
			history:          h.Messages,
			maxTurns:         2,
			profile:          types.ModelProfile{Name: "stage-test", ContextWindow: 6},
			compactionAudit:  audit,
			compactionEvents: events,
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(stageContext(types.WithSink(ctx, sinkInto(new([]types.Event)))), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		records := stageAuditRecords(t, audit)
		if len(records) != 1 {
			t.Fatalf("audit records %d: want 1", len(records))
		}
		if records[0].RunID != payload.RunID || records[0].Kind != stores.AuditCompacted {
			t.Fatalf("audit record %+v: want the original publisher under kind compaction", records[0])
		}
		if n := len(stageEventPayloads(t, events, "run-0")); n != 1 {
			t.Fatalf("event payloads %d: want 1", n)
		}
	})
}
