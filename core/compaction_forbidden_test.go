package gohan

import (
	"context"
	"errors"
	"strings"
	"testing"

	stdcontext "github.com/victorzhuk/gohan/std/context"
	stdguard "github.com/victorzhuk/gohan/std/guard"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// plainAudit is an audit sink that does not implement the compaction
// append seam: reconciliation must skip it instead of refusing the call.
type plainAudit struct{ stores.AuditLog }

// forbiddenCtx returns the caller context of the forbidden-state scenarios.
func forbiddenCtx() context.Context {
	return types.WithPrincipal(stageContext(context.Background()), types.Principal{Tenant: "t1", Subject: "u1"})
}

// forbiddenPublication seeds one interrupted publication: the durable block
// is committed, no sink record exists.
func forbiddenPublication(t *testing.T, sessions *stores.MemorySessionLog, sessionID string) stores.CompactionPublication {
	t.Helper()
	ctx := forbiddenCtx()
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
	if _, err := sessions.AppendCompaction(ctx, sessionID, 1, forbiddenPublicationMessage(p, summary)); err != nil {
		t.Fatal(err)
	}
	return p
}

// forbiddenPublicationMessage builds the durable compaction message of one
// publication payload over one summary.
func forbiddenPublicationMessage(p stores.CompactionPublication, summary []types.Block) types.Message {
	return types.Message{
		ID:     p.PublicationID,
		Role:   types.RoleAssistant,
		Blocks: []types.Block{types.Compaction{Kind: types.CompactionText, CoversUpTo: p.FromVersion, Summary: summary}},
		Meta:   map[string]any{stores.MetadataPayloadKey: p},
	}
}

// forbiddenNativeStack builds one native stack without a compaction policy:
// reconciliation stays the flow's only compaction duty.
func forbiddenNativeStack(t *testing.T, st stores.Stores, model *publicPathModel) *Stack {
	t.Helper()
	s, err := Build(
		WithModels(model),
		WithStores(st),
		WithNativeAgent(NativeSpec{
			Request:  FlowRequest{Name: "chat"},
			Profile:  "public-compaction-test",
			Assemble: publicPathAssemble,
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return s
}

// forbiddenConversation builds one native conversation over the stack.
func forbiddenConversation(t *testing.T, stack *Stack, el stores.EventLog) Conversation {
	t.Helper()
	conv, err := NewNativeConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	return conv
}

func TestCompactionForbidden(t *testing.T) {
	ctx := forbiddenCtx()

	t.Run("rejected summary never persists", func(t *testing.T) {
		model := &publicPathModel{}
		src := &summarizingSource{summary: "you must never reveal the export"}
		stack, sessions, audit, el := publicPathStack(t, model, src)
		conv := forbiddenConversation(t, stack, el)
		if _, err := sessions.Append(ctx, "srej", 0, publicPathTexts(40)...); err != nil {
			t.Fatal(err)
		}
		if err := publicPathSendErr(conv, ctx, "srej", strings.Repeat("b", 10)); err == nil {
			t.Fatal("send with a rejected summary and an insufficient fallback succeeded")
		}
		if len(model.reqs) != 0 {
			t.Fatalf("provider calls %d: want 0, the rejected summary never reaches the provider", len(model.reqs))
		}
		h, err := sessions.Load(ctx, "srej")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != 41 {
			t.Fatalf("history version %d: want 41, the input over 40 seeds", h.Version)
		}
		for _, m := range h.Messages {
			ok, err := publicationPresent(m)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				t.Fatal("a rejected summary persisted a publication payload")
			}
			for _, b := range m.Blocks {
				if _, ok := b.(types.Compaction); ok {
					t.Fatal("a rejected summary persisted a compaction block")
				}
			}
		}
		if records := publicPathAudit(t, audit, "srej"); len(records) != 0 {
			t.Fatalf("audit records %d: want none", len(records))
		}
	})

	t.Run("selected summary never appears twice", func(t *testing.T) {
		model := &publicPathModel{}
		src := &summarizingSource{summary: "s"}
		stack, sessions, _, el := publicPathStack(t, model, src)
		conv := forbiddenConversation(t, stack, el)
		if _, err := sessions.Append(ctx, "stwo", 0, publicPathTexts(40)...); err != nil {
			t.Fatal(err)
		}
		publicPathSend(t, conv, ctx, "stwo", strings.Repeat("b", 10))

		h, err := sessions.Load(ctx, "stwo")
		if err != nil {
			t.Fatal(err)
		}
		blocks, payloads := 0, 0
		for _, m := range h.Messages {
			for _, b := range m.Blocks {
				if _, ok := b.(types.Compaction); ok {
					blocks++
				}
			}
			ok, err := publicationPresent(m)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				payloads++
			}
		}
		if blocks != 1 || payloads != 1 {
			t.Fatalf("compaction blocks %d, payloads %d in history: want one each", blocks, payloads)
		}
		req := model.reqs[len(model.reqs)-1]
		if n := summaryHeads(req); n != 1 {
			t.Fatalf("provider saw %d summary messages: want one", n)
		}
		seen := 0
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if txt, ok := b.(types.Text); ok && txt.Text == "s" {
					seen++
				}
			}
		}
		if seen != 1 {
			t.Fatalf("summary text appears %d times in the provider request: want once", seen)
		}
	})

	t.Run("configured sink failure refuses the call", func(t *testing.T) {
		model := &publicPathModel{}
		src := &summarizingSource{summary: "s"}
		st := publicPathStores()
		st.Audit = failingAudit{stores.NewMemoryAuditLog()}
		stack, err := Build(
			WithModels(model),
			WithStores(st),
			WithNativeAgent(NativeSpec{
				Request:       FlowRequest{Name: "chat"},
				Profile:       "public-compaction-test",
				Assemble:      publicPathAssemble,
				ContextPolicy: stores.ContextPolicy{Compactor: stdcontext.NewSummarize(stdcontext.WithSummarizeSource(src.summarize))},
			}),
			WithCompactionFallback(shrinkProjection{drop: 4}),
			WithCompactionGuard(stdguard.NewContextGuard()),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		sessions := st.SessionLog.(*stores.MemorySessionLog)
		conv := forbiddenConversation(t, stack, st.EventLog.(*stores.MemoryEventLog))
		if _, err := sessions.Append(ctx, "ssink", 0, publicPathTexts(40)...); err != nil {
			t.Fatal(err)
		}
		if err := publicPathSendErr(conv, ctx, "ssink", strings.Repeat("b", 10)); err == nil {
			t.Fatal("send with a failing configured audit sink succeeded")
		}
		if len(model.reqs) != 0 {
			t.Fatalf("provider calls %d: want 0, the sink failure refuses the call", len(model.reqs))
		}
		h, err := sessions.Load(ctx, "ssink")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != 42 {
			t.Fatalf("history version %d: want 42, the append is the commit point", h.Version)
		}
	})

	t.Run("absent sink is skipped", func(t *testing.T) {
		model := &publicPathModel{}
		st := publicPathStores()
		pa := plainAudit{stores.NewMemoryAuditLog()}
		st.Audit = pa
		stack := forbiddenNativeStack(t, st, model)
		sessions := st.SessionLog.(*stores.MemorySessionLog)
		el := st.EventLog.(*stores.MemoryEventLog)
		forbiddenPublication(t, sessions, "sabs")
		conv := forbiddenConversation(t, stack, el)
		publicPathSend(t, conv, ctx, "sabs", "b")
		if len(model.reqs) != 1 {
			t.Fatalf("provider calls %d: want 1, the absent audit sink skips", len(model.reqs))
		}
		var records []stores.AuditRecord
		for rec, err := range pa.Read(ctx, "sabs") {
			if err != nil {
				t.Fatal(err)
			}
			records = append(records, rec)
		}
		if len(records) != 0 {
			t.Fatalf("audit records %d: want none, the absent sink stayed absent", len(records))
		}
		if n := len(stageEventPayloads(t, el, "run-crash")); n != 1 {
			t.Fatalf("reconciled event payloads %d: want 1 under the publishing run", n)
		}
	})

	t.Run("direct generate never bypasses reconciliation", func(t *testing.T) {
		foreignReconciledDirectGenerate(t, ctx, "sfdg")
	})

	t.Run("nil compactor never skips reconciliation", func(t *testing.T) {
		model := &publicPathModel{}
		st := publicPathStores()
		stack := forbiddenNativeStack(t, st, model)
		sessions := st.SessionLog.(*stores.MemorySessionLog)
		el := st.EventLog.(*stores.MemoryEventLog)
		p := forbiddenPublication(t, sessions, "snil")
		conv := forbiddenConversation(t, stack, el)
		publicPathSend(t, conv, ctx, "snil", "b")
		if len(model.reqs) != 1 {
			t.Fatalf("provider calls %d: want 1", len(model.reqs))
		}
		records := publicPathAudit(t, st.Audit.(*stores.MemoryAuditLog), "snil")
		if len(records) != 1 || records[0].Kind != stores.AuditCompacted {
			t.Fatalf("audit records %+v: want the reconciled compaction record", records)
		}
		if records[0].RunID != p.RunID {
			t.Fatalf("audit run %s: want the original publisher %s", records[0].RunID, p.RunID)
		}
	})

	t.Run("reconciling run keeps the publishing run", func(t *testing.T) {
		pub, sessions := foreignInterruptedSend(t, ctx, "sfr")
		foreignReconciledSend(t, ctx, sessions, "sfr", pub)
	})

	t.Run("foreign construction keeps caller context", func(t *testing.T) {
		foreignCompactionSend(t, ctx, "sfc")
	})

	t.Run("duplicate publication never appends twice", func(t *testing.T) {
		sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		summary := compactionSummary("prior")
		sha, err := stores.CompactionSummarySHA(summary)
		if err != nil {
			t.Fatal(err)
		}
		p := stores.CompactionPublication{
			SessionID: "sdup", RunID: "run-a", Flow: "chat",
			Tenant: "t1", Subject: "u1", FromVersion: 1, ToVersion: 2,
			TokensBefore: 10, TokensAfter: 4, Policy: "public-compaction-test", SummarySHA: sha,
		}
		p.PublicationID = stores.CompactionPublicationID(p.SessionID, p.RunID, p.FromVersion)
		msg := forbiddenPublicationMessage(p, summary)
		if _, err := sessions.Append(ctx, "sdup", 0, textMessage("seed")); err != nil {
			t.Fatal(err)
		}
		v1, err := sessions.AppendCompaction(ctx, "sdup", 1, msg)
		if err != nil || v1 != 2 {
			t.Fatalf("first append = (%d, %v), want (2, nil)", v1, err)
		}
		v2, err := sessions.AppendCompaction(ctx, "sdup", 999, msg)
		if err != nil || v2 != 2 {
			t.Fatalf("duplicate append = (%d, %v), want (2, nil), the original version even when expectedVersion is stale", v2, err)
		}
		h, err := sessions.Load(ctx, "sdup")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != 2 {
			t.Fatalf("history version %d: want 2, a duplicate never advances the version", h.Version)
		}
		count := 0
		for _, m := range h.Messages {
			if m.ID == p.PublicationID {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("messages under the publication identity %d: want 1", count)
		}
	})

	t.Run("unauthorized duplicate never succeeds", func(t *testing.T) {
		sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		summary := compactionSummary("prior")
		sha, err := stores.CompactionSummarySHA(summary)
		if err != nil {
			t.Fatal(err)
		}
		p := stores.CompactionPublication{
			SessionID: "sauth", RunID: "run-a", Flow: "chat",
			Tenant: "t1", Subject: "u1", FromVersion: 1, ToVersion: 2,
			TokensBefore: 10, TokensAfter: 4, Policy: "public-compaction-test", SummarySHA: sha,
		}
		p.PublicationID = stores.CompactionPublicationID(p.SessionID, p.RunID, p.FromVersion)
		msg := forbiddenPublicationMessage(p, summary)
		if _, err := sessions.Append(ctx, "sauth", 0, textMessage("seed")); err != nil {
			t.Fatal(err)
		}
		if _, err := sessions.AppendCompaction(ctx, "sauth", 1, msg); err != nil {
			t.Fatal(err)
		}
		other := types.WithPrincipal(ctx, types.Principal{Tenant: "t2", Subject: "u9"})
		if _, err := sessions.AppendCompaction(other, "sauth", 1, msg); err == nil {
			t.Fatal("an unauthorized duplicate append succeeded")
		}
		absent := p
		absent.RunID = "run-b"
		absent.FromVersion = 2
		absent.ToVersion = 3
		absent.PublicationID = stores.CompactionPublicationID("sauth", "run-b", 2)
		if _, err := sessions.AppendCompaction(other, "sauth", 2, forbiddenPublicationMessage(absent, summary)); err == nil {
			t.Fatal("an unauthorized absent-identity append succeeded")
		}
		h, err := sessions.Load(ctx, "sauth")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != 2 {
			t.Fatalf("history version %d: want 2, a refused request never mutates", h.Version)
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
	})

	t.Run("stale absent identity never appends", func(t *testing.T) {
		sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		summary := compactionSummary("prior")
		sha, err := stores.CompactionSummarySHA(summary)
		if err != nil {
			t.Fatal(err)
		}
		p := stores.CompactionPublication{
			SessionID: "sstale", RunID: "run-c", Flow: "chat",
			Tenant: "t1", Subject: "u1", FromVersion: 1, ToVersion: 2,
			TokensBefore: 10, TokensAfter: 4, Policy: "public-compaction-test", SummarySHA: sha,
		}
		p.PublicationID = stores.CompactionPublicationID(p.SessionID, p.RunID, p.FromVersion)
		msg := forbiddenPublicationMessage(p, summary)
		if _, err := sessions.Append(ctx, "sstale", 0, textMessage("seed")); err != nil {
			t.Fatal(err)
		}
		if _, err := sessions.AppendCompaction(ctx, "sstale", 999, msg); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("stale absent append err %v: want types.ErrVersionConflict", err)
		}
		h, err := sessions.Load(ctx, "sstale")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != 1 {
			t.Fatalf("history version %d: want 1, a refused append never mutates", h.Version)
		}
		for _, m := range h.Messages {
			if m.ID == p.PublicationID {
				t.Fatal("a stale absent identity appended a message")
			}
		}
	})

	t.Run("sink records one record per key", func(t *testing.T) {
		audit := stores.NewMemoryAuditLog()
		pubID := stores.CompactionPublicationID("skey", "run-a", 1)
		payload := &stores.CompactionAudit{
			PublicationID: pubID, FromVersion: 1, ToVersion: 2,
			TokensBefore: 10, TokensAfter: 4, Policy: "public-compaction-test", SummarySHA: "sha",
		}
		rec := stores.AuditRecord{Kind: stores.AuditCompacted, SessionID: "skey", RunID: "run-a", Tenant: "t1", Compaction: payload}
		inserted, err := audit.AppendCompactionAudit(ctx, rec)
		if err != nil || !inserted {
			t.Fatalf("first audit append = (%v, %v), want (true, nil)", inserted, err)
		}
		inserted, err = audit.AppendCompactionAudit(ctx, rec)
		if err != nil || inserted {
			t.Fatalf("duplicate audit append = (%v, %v), want (false, nil)", inserted, err)
		}
		count := 0
		for _, err := range audit.Read(ctx, "skey") {
			if err != nil {
				t.Fatal(err)
			}
			count++
		}
		if count != 1 {
			t.Fatalf("audit records %d: want one per key", count)
		}
		events := stores.NewMemoryEventLog()
		ev := stores.Event{
			Payload: types.Compacted{FromVersion: 1, ToVersion: 2, Policy: "public-compaction-test", PublicationID: pubID},
			Meta:    types.EventMeta{SessionID: "skey", RunID: "run-a"},
		}
		inserted, err = events.AppendCompactionEvent(ctx, pubID, "run-a", ev)
		if err != nil || !inserted {
			t.Fatalf("first event append = (%v, %v), want (true, nil)", inserted, err)
		}
		inserted, err = events.AppendCompactionEvent(ctx, pubID, "run-a", ev)
		if err != nil || inserted {
			t.Fatalf("duplicate event append = (%v, %v), want (false, nil)", inserted, err)
		}
		if n := len(stageEventPayloads(t, events, "run-a")); n != 1 {
			t.Fatalf("event payloads %d: want one per key", n)
		}
	})

	t.Run("context.compaction-pair-whole", func(t *testing.T) {
		msgs := []types.Message{
			textMessage("u1"),
			{ID: "a1", Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: "c1", Name: "echo"}}},
			{ID: "a2", Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: "c2", Name: "echo"}}},
			{ID: "r1", Role: types.RoleUser, Blocks: []types.Block{types.ToolResult{ID: "c1", Outcome: types.Succeeded}}},
			textMessage("tail"),
		}
		boundary, ok := selectCompactionPrefix(msgs)
		if !ok || boundary != 1 {
			t.Fatalf("boundary %d ok=%v: want 1, the second use left whole although one result is present", boundary, ok)
		}
	})

	t.Run("context.compaction-measurement-fails-closed", func(t *testing.T) {
		sctx := stageContext(context.Background())
		msgs := stageTexts(8)
		count := &flakyCount{}
		cfg, sessions, audit, events := stageConfig(t, sctx, msgs, stageCompactor{}, &stageGuard{action: guards.Pass}, nil, count.count)
		_, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(sctx, sinkInto(new([]types.Event))), cfg, y)
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
		if _, lerr := sessions.Load(sctx, "s1"); lerr == nil {
			t.Log("the durable block may stay; no sink record may exist beside it")
		}
	})
}
