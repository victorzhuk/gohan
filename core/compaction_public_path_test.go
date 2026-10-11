package gohan

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	stdcontext "github.com/victorzhuk/gohan/std/context"
	stdguard "github.com/victorzhuk/gohan/std/guard"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// publicPathModel is the deterministic model port of the public path: it
// records every request the provider actually receives and counts every
// text rune, so the trigger and the measurements stay exact.
type publicPathModel struct {
	reqs []types.ModelRequest
	ctxs []context.Context
}

func (m *publicPathModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "public-compaction-test", ContextWindow: 20, Caps: types.Caps{Tools: true}}
}

func (m *publicPathModel) Count(_ context.Context, req types.ModelRequest) (int, error) {
	n := 0
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if t, ok := b.(types.Text); ok {
				n += len([]rune(t.Text))
			}
		}
	}
	return n, nil
}

func (m *publicPathModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.reqs = append(m.reqs, req)
	m.ctxs = append(m.ctxs, ctx)
	return (&scriptTurns{turns: [][]types.ModelChunk{{turnTextChunk("ok")}}}).model()(ctx, req)
}

// summarizingSource renders the newly covered messages into one short text
// block and records what the compactor supplied, so the "only newly covered
// ordinary messages, prior summary once" rule is observable.
type summarizingSource struct {
	newly   [][]types.Message
	priors  [][]types.Block
	bounds  []int64
	fail    error
	summary string
}

func (s *summarizingSource) summarize(_ context.Context, h stores.History, _ types.ModelProfile) ([]types.Block, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	s.newly = append(s.newly, h.Messages)
	s.bounds = append(s.bounds, h.Version)
	text := s.summary
	if len(s.priors) > 0 {
		text = text + "*"
	}
	return []types.Block{modelText(text)}, nil
}

func publicPathAssemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
}

func publicPathStores() stores.Stores {
	return stores.Stores{
		SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
		Audit:      stores.NewMemoryAuditLog(),
		EventLog:   stores.NewMemoryEventLog(),
		Runs:       stores.NewMemoryRuns(),
	}
}

func publicPathStack(t *testing.T, model *publicPathModel, src *summarizingSource, extra ...Option) (*Stack, *stores.MemorySessionLog, *stores.MemoryAuditLog, *stores.MemoryEventLog) {
	t.Helper()
	st := publicPathStores()
	opts := append([]Option{
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

func publicPathSend(t *testing.T, conv Conversation, ctx context.Context, sessionID string, text string) {
	t.Helper()
	for _, err := range conv.Send(ctx, sessionID, types.Message{Role: types.RoleUser, Blocks: []types.Block{modelText(text)}}) {
		if err != nil {
			t.Fatalf("send: %v", err)
		}
	}
}

func publicPathAudit(t *testing.T, a *stores.MemoryAuditLog, sessionID string) []stores.AuditRecord {
	t.Helper()
	var out []stores.AuditRecord
	for rec, err := range a.Read(context.Background(), sessionID) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
	}
	return out
}

func publicPathTexts(n int) []types.Message {
	msgs := make([]types.Message, n)
	for i := range msgs {
		msgs[i] = textMessage(strings.Repeat("a", 10))
	}
	return msgs
}

// summaryHeads counts the summary-carrying messages of one provider request.
func summaryHeads(req types.ModelRequest) int {
	n := 0
	for _, msg := range req.Messages {
		if msg.Role != types.RoleSystem {
			continue
		}
		for _, b := range msg.Blocks {
			if _, ok := b.(types.Text); ok {
				n++
				break
			}
		}
	}
	return n
}

// publicationOf returns the durable publication payload of the loaded
// history, or fails the test when the log carries none.
func publicationOf(t *testing.T, h stores.History) stores.CompactionPublication {
	t.Helper()
	var latest stores.CompactionPublication
	found := false
	for _, m := range h.Messages {
		pub, ok, err := decodePublicationMeta(m)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			latest = pub
			found = true
		}
	}
	if !found {
		t.Fatal("durable history carries no publication payload")
	}
	return latest
}

func TestCompactionPublicPath(t *testing.T) {
	ctx := types.WithPrincipal(stageContext(context.Background()), types.Principal{Tenant: "t1", Subject: "u1"})
	model := &publicPathModel{}
	src := &summarizingSource{summary: "s"}
	stack, sessions, audit, el := publicPathStack(t, model, src)
	conv, err := NewNativeConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	if _, err := sessions.Append(ctx, "s1", 0, publicPathTexts(40)...); err != nil {
		t.Fatal(err)
	}

	t.Run("context.compaction-persisted", func(t *testing.T) {
		t.Run("native", func(t *testing.T) {
			publicPathSend(t, conv, ctx, "s1", strings.Repeat("b", 10))
			h, err := sessions.Load(ctx, "s1")
			if err != nil {
				t.Fatal(err)
			}
			if h.Version != 43 {
				t.Fatalf("history version %d: want 43, the publication plus the reply over 41 messages", h.Version)
			}
			if h.Version != 43 {
				t.Fatalf("history version %d: want 43, the publication plus the reply over 41 messages", h.Version)
			}
			pub := publicationOf(t, h)
			if pub.FromVersion != 41 || pub.ToVersion != 42 {
				t.Fatalf("publication versions %d..%d: want 41..42", pub.FromVersion, pub.ToVersion)
			}
		})
		t.Run("foreign", func(t *testing.T) {
			foreignCompactionSend(t, ctx, "sf1")
		})
	})

	t.Run("context.compaction-single-summary", func(t *testing.T) {
		req := model.reqs[len(model.reqs)-1]
		if n := summaryHeads(req); n != 1 {
			t.Fatalf("provider saw %d summary messages: want exactly one", n)
		}
		// The covered prefix is hidden; the live tail stays in place with
		// its absolute composition.
		if len(req.Messages) != 2 {
			t.Fatalf("provider request carries %d messages: want the summary head plus the live tail", len(req.Messages))
		}
	})

	t.Run("context.compaction-same-turn-measurement", func(t *testing.T) {
		h, _ := sessions.Load(ctx, "s1")
		pub := publicationOf(t, h)
		n, err := model.Count(ctx, model.reqs[len(model.reqs)-1])
		if err != nil {
			t.Fatal(err)
		}
		if n != pub.TokensAfter {
			t.Fatalf("provider request measured %d, durable payload %d", n, pub.TokensAfter)
		}
	})

	t.Run("context.compaction-tokens-measured", func(t *testing.T) {
		h, err := sessions.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		pub := publicationOf(t, h)
		var comp *types.Compaction
		for _, m := range h.Messages {
			for _, b := range m.Blocks {
				if c, ok := b.(types.Compaction); ok {
					c := c
					comp = &c
				}
			}
		}
		if comp == nil {
			t.Fatal("durable history carries no compaction block")
		}
		stokens, err := model.Count(ctx, types.ModelRequest{Messages: []types.Message{{Blocks: comp.Summary}}})
		if err != nil {
			t.Fatal(err)
		}
		if comp.Tokens != stokens {
			t.Fatalf("durable compaction tokens %d: want the counted summary %d", comp.Tokens, stokens)
		}
		overflow := types.ModelRequest{Messages: append(publicPathTexts(40), types.Message{Role: types.RoleUser, Blocks: []types.Block{modelText(strings.Repeat("b", 10))}})}
		before, err := model.Count(ctx, overflow)
		if err != nil {
			t.Fatal(err)
		}
		if pub.TokensBefore != before {
			t.Fatalf("tokens before %d: want the pre-append request size %d", pub.TokensBefore, before)
		}
		after, err := model.Count(ctx, model.reqs[len(model.reqs)-1])
		if err != nil {
			t.Fatal(err)
		}
		if pub.TokensAfter != after {
			t.Fatalf("tokens after %d: want the post-compaction request size %d", pub.TokensAfter, after)
		}
		if stokens == after {
			t.Fatalf("summary count %d equals request count: measurements must differ", stokens)
		}
	})

	t.Run("context.compaction-audited", func(t *testing.T) {
		records := publicPathAudit(t, audit, "s1")
		if len(records) != 1 || records[0].Kind != stores.AuditCompacted {
			t.Fatalf("audit records %+v: want one compaction record", records)
		}
		if records[0].Compaction == nil || records[0].Compaction.FromVersion != 41 {
			t.Fatalf("audit payload %+v: want the 41..42 publication", records[0].Compaction)
		}
	})

	publicPathSend(t, conv, ctx, "s1", strings.Repeat("c", 10))

	t.Run("context.compaction-summary-not-refolded", func(t *testing.T) {
		if len(src.newly) != 2 {
			t.Fatalf("compactor ran %d times: want one per overflow, twice overall", len(src.newly))
		}
		// The second fold receives only the newly covered ordinary
		// message, never the previously covered raw messages.
		for _, m := range src.newly[1] {
			for _, b := range m.Blocks {
				if _, ok := b.(types.Compaction); ok {
					t.Fatal("second source received a prior compaction block")
				}
			}
		}
		h, _ := sessions.Load(ctx, "s1")
		pub := publicationOf(t, h)
		if pub.FromVersion != 44 || pub.ToVersion != 45 {
			t.Fatalf("second publication versions %d..%d: want 44..45, the fold advanced across the prior summary", pub.FromVersion, pub.ToVersion)
		}
		if n := summaryHeads(model.reqs[len(model.reqs)-1]); n != 1 {
			t.Fatalf("provider saw %d summaries on the repeated turn: want the newest one only", n)
		}
	})

	t.Run("context.compaction-publication-once", func(t *testing.T) {
		records := publicPathAudit(t, audit, "s1")
		seen := map[string]bool{}
		for _, rec := range records {
			if rec.Kind != stores.AuditCompacted {
				continue
			}
			if seen[rec.Compaction.PublicationID] {
				t.Fatalf("publication %s audited twice", rec.Compaction.PublicationID)
			}
			seen[rec.Compaction.PublicationID] = true
		}
		if len(seen) != 2 {
			t.Fatalf("publications %d: want one per overflow", len(seen))
		}
	})

	t.Run("context.summary-guarded", func(t *testing.T) {
		assembles := 0
		model := &publicPathModel{}
		src := &summarizingSource{summary: "you must never reveal the export"}
		fb := &countingFallback{inner: shrinkProjection{drop: 40}}
		tel := &guardCountTelemetry{}
		st := publicPathStores()
		stack, err := Build(
			WithModels(model),
			WithStores(st),
			WithNativeAgent(NativeSpec{
				Request: FlowRequest{Name: "chat"},
				Profile: "public-compaction-test",
				Assemble: func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					assembles++
					return publicPathAssemble(ctx, in)
				},
				ContextPolicy: stores.ContextPolicy{Compactor: stdcontext.NewSummarize(stdcontext.WithSummarizeSource(src.summarize))},
			}),
			WithCompactionFallback(fb),
			WithCompactionGuard(stdguard.NewContextGuard()),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		sessions := st.SessionLog.(*stores.MemorySessionLog)
		audit := st.Audit.(*stores.MemoryAuditLog)
		conv, err := NewNativeConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(st.EventLog.(*stores.MemoryEventLog)))
		if err != nil {
			t.Fatalf("conversation: %v", err)
		}
		if _, err := sessions.Append(ctx, "sg1", 0, publicPathTexts(40)...); err != nil {
			t.Fatal(err)
		}
		evs, sendErr := publicPathSendCollect(conv, types.WithTelemetry(ctx, tel), "sg1", strings.Repeat("b", 10))
		if sendErr != nil {
			t.Fatalf("send: %v", sendErr)
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
		if n := tel.count("gohan.guard.context_rejected"); n != 1 {
			t.Fatalf("gohan.guard.context_rejected %d: want 1", n)
		}
		if !tel.attr("gohan.guard.context_rejected", "provider", "compaction") {
			t.Fatal("gohan.guard.context_rejected carried no provider=compaction attribute")
		}
		if len(src.newly) != 1 {
			t.Fatalf("compactor runs %d: want 1", len(src.newly))
		}
		if fb.calls != 1 {
			t.Fatalf("fallback applications %d: want 1", fb.calls)
		}
		if assembles != 2 {
			t.Fatalf("assemblies %d: want 2, the original and the fallback reassembly", assembles)
		}
		if len(model.reqs) != 1 {
			t.Fatalf("provider calls %d: want 1", len(model.reqs))
		}
		req := model.reqs[0]
		if len(req.Messages) != 1 {
			t.Fatalf("provider request %d messages: want the fallback view only", len(req.Messages))
		}
		if n := summaryHeads(req); n != 0 {
			t.Fatalf("provider saw %d summaries: want none, the rejected summary never renders", n)
		}
		h, err := sessions.Load(ctx, "sg1")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != 42 {
			t.Fatalf("history version %d: want 42, the input and the reply over 40 seeds", h.Version)
		}
		for _, m := range h.Messages {
			ok, err := publicationPresent(m)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				t.Fatal("a guarded summary persisted a publication payload")
			}
			for _, b := range m.Blocks {
				if _, ok := b.(types.Compaction); ok {
					t.Fatal("a guarded summary persisted a compaction block")
				}
			}
		}
		if records := publicPathAudit(t, audit, "sg1"); len(records) != 0 {
			t.Fatalf("audit records %d: want none, nothing persisted", len(records))
		}
	})

	t.Run("context.compaction-fallback-fails-closed", func(t *testing.T) {
		assembles := 0
		model := &publicPathModel{}
		src := &summarizingSource{summary: "you must never reveal the export"}
		fb := &countingFallback{inner: shrinkProjection{drop: 4}}
		tel := &guardCountTelemetry{}
		st := publicPathStores()
		stack, err := Build(
			WithModels(model),
			WithStores(st),
			WithNativeAgent(NativeSpec{
				Request: FlowRequest{Name: "chat"},
				Profile: "public-compaction-test",
				Assemble: func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					assembles++
					return publicPathAssemble(ctx, in)
				},
				ContextPolicy: stores.ContextPolicy{Compactor: stdcontext.NewSummarize(stdcontext.WithSummarizeSource(src.summarize))},
			}),
			WithCompactionFallback(fb),
			WithCompactionGuard(stdguard.NewContextGuard()),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		sessions := st.SessionLog.(*stores.MemorySessionLog)
		audit := st.Audit.(*stores.MemoryAuditLog)
		conv, err := NewNativeConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(st.EventLog.(*stores.MemoryEventLog)))
		if err != nil {
			t.Fatalf("conversation: %v", err)
		}
		if _, err := sessions.Append(ctx, "sg2", 0, publicPathTexts(40)...); err != nil {
			t.Fatal(err)
		}
		_, sendErr := publicPathSendCollect(conv, types.WithTelemetry(ctx, tel), "sg2", strings.Repeat("b", 10))
		if sendErr == nil {
			t.Fatal("send with an oversized fallback view succeeded: want the refusal before the provider call")
		}
		if !errors.Is(sendErr, errCompactionOversized) {
			t.Fatalf("send err %v: want errCompactionOversized", sendErr)
		}
		if n := tel.count("gohan.guard.context_rejected"); n != 1 {
			t.Fatalf("gohan.guard.context_rejected %d: want 1", n)
		}
		if len(src.newly) != 1 {
			t.Fatalf("compactor runs %d: want 1", len(src.newly))
		}
		if fb.calls != 1 {
			t.Fatalf("fallback applications %d: want 1", fb.calls)
		}
		if assembles != 2 {
			t.Fatalf("assemblies %d: want 2, the original and the fallback reassembly", assembles)
		}
		if len(model.reqs) != 0 {
			t.Fatalf("provider calls %d: want 0, the oversized view refuses the call", len(model.reqs))
		}
		h, err := sessions.Load(ctx, "sg2")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != 41 {
			t.Fatalf("history version %d: want 41, the input over 40 seeds, no publication and no reply", h.Version)
		}
		for _, m := range h.Messages {
			ok, err := publicationPresent(m)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				t.Fatal("a fails-closed compaction persisted a publication payload")
			}
			for _, b := range m.Blocks {
				if _, ok := b.(types.Compaction); ok {
					t.Fatal("a fails-closed compaction persisted a compaction block")
				}
			}
		}
		if records := publicPathAudit(t, audit, "sg2"); len(records) != 0 {
			t.Fatalf("audit records %d: want none, nothing persisted", len(records))
		}
	})

	t.Run("context.compaction-counting-required", func(t *testing.T) {
		policy := stores.ContextPolicy{Compactor: stdcontext.NewSummarize(stdcontext.WithSummarizeSource(func(context.Context, stores.History, types.ModelProfile) ([]types.Block, error) {
			return []types.Block{modelText("s")}, nil
		}))}
		spec := NativeSpec{
			Request:       FlowRequest{Name: "chat"},
			Profile:       "public-compaction-test",
			Assemble:      publicPathAssemble,
			ContextPolicy: policy,
		}
		build := func(extra ...Option) error {
			opts := append([]Option{
				WithModels(&noCounterModel{}),
				WithStores(publicPathStores()),
				WithNativeAgent(spec),
				WithCompactionFallback(shrinkProjection{drop: 1}),
				WithCompactionGuard(stdguard.NewContextGuard()),
			}, extra...)
			_, err := Build(opts...)
			return err
		}
		err := build()
		if err == nil || !strings.Contains(err.Error(), `flow "chat"`) || !strings.Contains(err.Error(), `profile "public-compaction-test"`) || !strings.Contains(err.Error(), "compaction needs a token count") {
			t.Fatalf("build err %v: want the counting refusal naming the flow and the profile", err)
		}
		if err := build(WithEstimator(runeEstimator{})); err != nil {
			t.Fatalf("build with a declared estimator: %v", err)
		}
		// A counting model needs no declared estimator while the injected
		// fallback validates.
		if _, err := Build(
			WithModels(&publicPathModel{}),
			WithStores(publicPathStores()),
			WithNativeAgent(spec),
			WithCompactionFallback(shrinkProjection{drop: 1}),
			WithCompactionGuard(stdguard.NewContextGuard()),
		); err != nil {
			t.Fatalf("build with a counting model: %v", err)
		}
	})

	t.Run("context.compaction-fallback-required", func(t *testing.T) {
		policy := stores.ContextPolicy{Compactor: stdcontext.NewSummarize(stdcontext.WithSummarizeSource(func(context.Context, stores.History, types.ModelProfile) ([]types.Block, error) {
			return []types.Block{modelText("s")}, nil
		}))}
		spec := NativeSpec{
			Request:       FlowRequest{Name: "chat"},
			Profile:       "public-compaction-test",
			Assemble:      publicPathAssemble,
			ContextPolicy: policy,
		}
		build := func(extra ...Option) error {
			opts := append([]Option{
				WithModels(&publicPathModel{}),
				WithStores(publicPathStores()),
				WithNativeAgent(spec),
				WithCompactionGuard(stdguard.NewContextGuard()),
			}, extra...)
			_, err := Build(opts...)
			return err
		}
		err := build()
		if err == nil || !strings.Contains(err.Error(), `flow "chat"`) || !strings.Contains(err.Error(), "compaction needs a declared compaction fallback") {
			t.Fatalf("build err %v: want the missing-fallback refusal naming the flow", err)
		}
		err = build(WithCompactionFallback(stdcontext.NewTruncate()))
		if err == nil || !strings.Contains(err.Error(), "compaction fallback") || !strings.Contains(err.Error(), `profile "public-compaction-test"`) {
			t.Fatalf("build err %v: want the unvalidated fallback refusal naming the flow and the profile", err)
		}
		if err := build(WithCompactionFallback(shrinkProjection{drop: 1})); err != nil {
			t.Fatalf("build with a validated fallback: %v", err)
		}
	})

	t.Run("context.opaque-bound-to-model", func(t *testing.T) {
		t.Run("native", func(t *testing.T) {
			opaqueModel := &opaqueEndpointModel{}
			textModel := &publicPathModel{}
			var probeSeen []stores.History
			probe := collapseProbe{fn: func(h stores.History) error {
				probeSeen = append(probeSeen, h)
				return nil
			}}
			src2 := &summarizingSource{summary: "s"}
			st := publicPathStores()
			stack, err := Build(
				WithModels(opaqueModel, textModel),
				WithStores(st),
				WithNativeAgent(NativeSpec{
					Request:       FlowRequest{Name: "chat"},
					Profile:       "opaque-endpoint",
					Assemble:      publicPathAssemble,
					ContextPolicy: stores.ContextPolicy{Compactor: opaqueCompactor{}},
				}),
				WithNativeAgent(NativeSpec{
					Request:  FlowRequest{Name: "chat2"},
					Profile:  "public-compaction-test",
					Assemble: publicPathAssemble,
					ContextPolicy: stores.ContextPolicy{
						Projections: []stores.Projection{probe},
						Compactor:   stdcontext.NewSummarize(stdcontext.WithSummarizeSource(src2.summarize)),
					},
				}),
				WithCompactionFallback(shrinkProjection{drop: 4}),
				WithCompactionGuard(stdguard.NewContextGuard()),
			)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			sessions := st.SessionLog.(*stores.MemorySessionLog)
			el := st.EventLog.(*stores.MemoryEventLog)
			conv1, err := NewNativeConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
			if err != nil {
				t.Fatalf("conversation: %v", err)
			}
			if _, err := sessions.Append(ctx, "sop", 0, publicPathTexts(40)...); err != nil {
				t.Fatal(err)
			}
			publicPathSend(t, conv1, ctx, "sop", strings.Repeat("b", 10))

			h, err := sessions.Load(ctx, "sop")
			if err != nil {
				t.Fatal(err)
			}
			var opaque *types.Compaction
			for _, m := range h.Messages {
				for _, b := range m.Blocks {
					if c, ok := b.(types.Compaction); ok && c.Kind == types.CompactionOpaque {
						c := c
						opaque = &c
					}
				}
			}
			if opaque == nil {
				t.Fatal("the opaque-producing compactor persisted no opaque block")
			}
			if opaque.Model != "opaque-endpoint" || opaque.CoversUpTo != 40 {
				t.Fatalf("opaque block model %q covers %d: want bound to opaque-endpoint at 40", opaque.Model, opaque.CoversUpTo)
			}

			// The other endpoint assembles: the block is dropped, the
			// covered messages are visible to the declared projections and
			// the request re-fits through Summarize.
			conv2, err := NewNativeConversation(stack, "chat2", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
			if err != nil {
				t.Fatalf("conversation: %v", err)
			}
			publicPathSend(t, conv2, ctx, "sop", strings.Repeat("b", 10))
			if len(probeSeen) == 0 {
				t.Fatal("the other endpoint ran no declared projection")
			}
			seen := probeSeen[0]
			if !findText(seen.Messages, "aaaaaaaaaa") {
				t.Fatal("the covered messages stayed hidden for the other endpoint: the bound block was not dropped")
			}
			for _, m := range seen.Messages {
				for _, b := range m.Blocks {
					if c, ok := b.(types.Compaction); ok && c.Kind == types.CompactionOpaque {
						t.Fatal("the foreign opaque block reached the other endpoint's assembly")
					}
				}
			}
			if len(src2.newly) == 0 {
				t.Fatal("the request did not re-fit through Summarize")
			}
			for _, m := range src2.newly[0] {
				for _, b := range m.Blocks {
					if _, ok := b.(types.Compaction); ok {
						t.Fatal("the re-fit source received a compaction block")
					}
				}
			}
			req := textModel.reqs[len(textModel.reqs)-1]
			if n := summaryHeads(req); n != 1 {
				t.Fatalf("provider saw %d summaries: want exactly the re-fit summary", n)
			}
			for _, m := range req.Messages {
				for _, b := range m.Blocks {
					if _, bad := b.(types.Compaction); bad {
						t.Fatal("the foreign endpoint's provider request carried a compaction block")
					}
				}
			}
		})

		t.Run("foreign", func(t *testing.T) {
			model := &publicPathModel{}
			rt := &foreignCompactionRT{}
			stack, sessions, _, el := foreignPublicStack(t, model, nil, func() runtime.Runtime { return rt }, nil)
			conv := foreignPublicConversation(t, stack, el)
			if _, err := sessions.Append(ctx, "sopf", 0, textMessage("covered-seed")); err != nil {
				t.Fatal(err)
			}
			opaque := types.Message{
				ID:   "mopaque",
				Role: types.RoleAssistant,
				Blocks: []types.Block{types.Compaction{
					Kind: types.CompactionOpaque, CoversUpTo: 1, Model: "opaque-endpoint",
					Opaque: types.Raw{Provider: "opaque-endpoint", Value: map[string]any{"type": "encrypted"}},
				}},
			}
			if _, err := sessions.Append(ctx, "sopf", 1, opaque); err != nil {
				t.Fatal(err)
			}
			if _, err := sessions.Append(ctx, "sopf", 2, publicPathTexts(40)...); err != nil {
				t.Fatal(err)
			}
			publicPathSend(t, conv, ctx, "sopf", strings.Repeat("b", 10))

			if len(model.reqs) != 1 {
				t.Fatalf("provider calls %d: want 1", len(model.reqs))
			}
			req := model.reqs[0]
			if !findText(req.Messages, "covered-seed") {
				t.Fatal("the covered marker stayed hidden for the foreign flow: the bound block was not dropped")
			}
			if n := summaryHeads(req); n != 0 {
				t.Fatalf("foreign request carries %d summaries: want none, the block is bound elsewhere", n)
			}
			for _, m := range req.Messages {
				for _, b := range m.Blocks {
					if _, ok := b.(types.Compaction); ok {
						t.Fatal("the foreign assembled request carried a compaction block")
					}
				}
			}
		})
	})

	t.Run("context.opaque-fallback-rejected", func(t *testing.T) {
		build := func(primaryCaps types.CompactionMode) error {
			primary := profile("opaque-primary", true)
			primary.Caps.Compaction = primaryCaps
			_, err := Build(
				WithModels(fakeModel{primary}, fakeModel{profile("text-fallback", true)}),
				WithStores(publicPathStores()),
				WithNativeAgent(NativeSpec{
					Request:  FlowRequest{Name: "chat", ProviderCompact: true, Fallback: "text-fallback"},
					Profile:  "opaque-primary",
					Assemble: publicPathAssemble,
				}),
			)
			return err
		}
		err := build(types.CompactionOpaqueCap)
		if !errors.Is(err, ErrOpaqueCompaction) {
			t.Fatalf("build err %v: want ErrOpaqueCompaction", err)
		}
		for _, part := range []string{"chat", "opaque-primary", "provider_compact"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("err %q misses %q", err, part)
			}
		}
		if err := build(types.CompactionTextCap); err != nil {
			t.Fatalf("build with a text-cap primary: %v", err)
		}
	})

	t.Run("context.calibration-from-usage", func(t *testing.T) {
		tel := &calibrationTelemetry{}
		tctx := types.WithTelemetry(ctx, tel)
		m := &calibratedPathModel{
			profile:    types.ModelProfile{Name: "calibrated-path", ContextWindow: 14400, Caps: types.Caps{Tools: true}},
			usage:      &types.Usage{InputTokens: 12000},
			usageDelta: strings.Repeat("r", 12),
			ask:        "echo",
			askID:      "c1",
		}
		echo := &echoTool{}
		src := &summarizingSource{summary: "s"}
		st := publicPathStores()
		stack, err := Build(
			WithModels(m),
			WithStores(st),
			WithEstimator(runeEstimator{}),
			WithNativeAgent(NativeSpec{
				Request:       FlowRequest{Name: "chat"},
				Profile:       m.profile.Name,
				Assemble:      calibratedAssemble(1600),
				Tools:         []types.Tool{echo},
				ContextPolicy: stores.ContextPolicy{Compactor: stdcontext.NewSummarize(stdcontext.WithSummarizeSource(src.summarize))},
			}),
			WithCompactionFallback(shrinkProjection{drop: 40}),
			WithCompactionGuard(stdguard.NewContextGuard()),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		sessions := st.SessionLog.(*stores.MemorySessionLog)
		el := st.EventLog.(*stores.MemoryEventLog)
		conv, err := NewNativeConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
		if err != nil {
			t.Fatalf("conversation: %v", err)
		}
		if _, err := sessions.Append(tctx, "scal", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{modelText(strings.Repeat("a", 9990))}}); err != nil {
			t.Fatal(err)
		}

		// One run: the first request counts 9990 seed plus the 10-rune
		// send at the uncalibrated ceiling of 12000 and fits; the reply
		// reports 12000 input tokens, the echo turn reassembles the
		// request at 10000 (the streamed reply is not yet durable), and
		// only the calibrated ceiling of 9600 makes that second call
		// compact.
		publicPathSend(t, conv, tctx, "scal", strings.Repeat("b", 10))
		if len(m.reqs) != 2 {
			t.Fatalf("provider calls %d: want the usage reply and the calibrated second call", len(m.reqs))
		}
		if n, err := m.Count(tctx, m.reqs[0]); err != nil || n != 10000 {
			t.Fatalf("first request %d (err %v): want the 10000-rune estimate", n, err)
		}
		// The reply calibrated the profile at 1.2 and the deviation is
		// recorded.
		if recs := tel.record("gohan.context.estimate_error"); len(recs) != 1 || !almostEqual(recs[0], 0.2) {
			t.Fatalf("estimate_error samples %v: want one 0.2", recs)
		}
		h, err := sessions.Load(tctx, "scal")
		if err != nil {
			t.Fatal(err)
		}
		if !compactionIn(h) {
			t.Fatal("second request 10000 did not compact: the calibrated ceiling did not apply")
		}
		pub := publicationOf(t, h)
		if pub.TokensBefore != 10000 {
			t.Fatalf("tokens before %d: want the reassembled request measured at the calibrated decision", pub.TokensBefore)
		}
		if n := summaryHeads(m.reqs[len(m.reqs)-1]); n != 1 {
			t.Fatalf("provider saw %d summaries: want one", n)
		}
	})

	t.Run("context.calibration-persisted-for-replay", func(t *testing.T) {
		tel := &guardCountTelemetry{}
		tctx := types.WithTelemetry(ctx, tel)
		m := &calibratedPathModel{
			profile:    types.ModelProfile{Name: "calibrated-path", ContextWindow: 14400, Caps: types.Caps{Tools: true}},
			usage:      &types.Usage{InputTokens: 12000},
			usageDelta: strings.Repeat("r", 12),
			ask:        "confirm",
			askID:      "c1",
		}
		pad := &padTool{}
		src := &summarizingSource{summary: "s"}
		st := publicPathStores()
		cps := stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))
		st.Checkpoints = cps
		st.Runs = runs
		stack, err := Build(
			WithModels(m),
			WithStores(st),
			WithEstimator(runeEstimator{}),
			WithNativeAgent(NativeSpec{
				Request:       FlowRequest{Name: "chat"},
				Profile:       m.profile.Name,
				Assemble:      calibratedAssemble(1600),
				Tools:         []types.Tool{pad},
				ContextPolicy: stores.ContextPolicy{Compactor: stdcontext.NewSummarize(stdcontext.WithSummarizeSource(src.summarize))},
			}),
			WithCompactionFallback(shrinkProjection{drop: 40}),
			WithCompactionGuard(stdguard.NewContextGuard()),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		sessions := st.SessionLog.(*stores.MemorySessionLog)
		el := st.EventLog.(*stores.MemoryEventLog)
		conv, err := NewNativeConversation(stack, "chat", WithConversationRuns(runs), WithConversationEventLog(el), WithConversationCheckpoints(cps), WithConversationApprovalPolicy(nrrAllowPolicy{}))
		if err != nil {
			t.Fatalf("conversation: %v", err)
		}
		if _, err := sessions.Append(tctx, "sreplay", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{modelText(strings.Repeat("a", 9990))}}); err != nil {
			t.Fatal(err)
		}

		// The reply calibrates the profile at 1.2 and asks the side-effect
		// pad tool, so the run suspends with the checkpoint carrying the
		// ratio.
		var susp types.Suspended
		var kinds []string
		// A fresh transport context: the subtest context carries the
		// stage's RunInfo, which a resume must never see.
		rctx := types.WithTelemetry(types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"}), tel)
		for ev, err := range conv.Send(rctx, "sreplay", types.Message{Role: types.RoleUser, Blocks: []types.Block{modelText(strings.Repeat("b", 10))}}) {
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			kinds = append(kinds, fmt.Sprintf("%T", ev))
			if s, ok := ev.(types.Suspended); ok {
				susp = s
			}
		}
		if susp.Token == "" {
			t.Fatalf("the side-effect ask did not suspend with a resume token; events %v", kinds)
		}

		// Another pod resumes over the same durable stores: the tool runs
		// once and its 600-rune result lands in the history.
		conv2, err := NewNativeConversation(stack, "chat", WithConversationRuns(runs), WithConversationEventLog(el), WithConversationCheckpoints(cps), WithConversationApprovalPolicy(nrrAllowPolicy{}))
		if err != nil {
			t.Fatalf("conversation: %v", err)
		}
		for ev, err := range conv2.Resume(rctx, susp.Token, Continue()) {
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if _, ok := ev.(types.Suspended); ok {
				t.Fatal("the resume suspended again")
			}
		}
		h, err := sessions.Load(tctx, "sreplay")
		if err != nil {
			t.Fatal(err)
		}
		// The 600-rune tool result is durably appended exactly once.
		found := 0
		for _, msg := range h.Messages {
			for _, b := range msg.Blocks {
				res, ok := b.(types.ToolResult)
				if !ok {
					continue
				}
				for _, c := range res.Content {
					if tx, ok := c.(types.Text); ok && tx.Text == strings.Repeat(padRune, padRunes) {
						found++
					}
				}
			}
		}
		if found != 1 {
			t.Fatalf("durable pad results %d: want exactly one", found)
		}

		// The resumed request sits in (9600, 12000]: below the uncalibrated
		// ceiling, above the calibrated one, so it compacted only because
		// the restored ratio reached the fresh conversation.
		pub := publicationOf(t, h)
		if pub.TokensBefore <= 9600 || pub.TokensBefore > 14400 {
			t.Fatalf("resumed request %d tokens: want a size inside (9600, 14400], compacted only under the restored ceiling", pub.TokensBefore)
		}
	})

	t.Run("context.overflow-compacts-and-retries-once", func(t *testing.T) {
		const (
			seedRunes = 90
			sendRunes = 5
		)
		build := func(t *testing.T, overflows []bool, tel types.Telemetry) (*calibratedPathModel, Conversation, *stores.MemorySessionLog) {
			t.Helper()
			m := &calibratedPathModel{
				profile:   types.ModelProfile{Name: "overflow-path", ContextWindow: 100, Caps: types.Caps{Tools: true}},
				overflows: overflows,
			}
			src := &summarizingSource{summary: "s"}
			st := publicPathStores()
			stack, err := Build(
				WithModels(m),
				WithStores(st),
				WithNativeAgent(NativeSpec{
					Request:       FlowRequest{Name: "chat"},
					Profile:       m.profile.Name,
					Assemble:      calibratedAssemble(0),
					ContextPolicy: stores.ContextPolicy{Compactor: stdcontext.NewSummarize(stdcontext.WithSummarizeSource(src.summarize))},
				}),
				WithCompactionFallback(shrinkProjection{drop: 40}),
				WithCompactionGuard(stdguard.NewContextGuard()),
			)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			sessions := st.SessionLog.(*stores.MemorySessionLog)
			el := st.EventLog.(*stores.MemoryEventLog)
			conv, err := NewNativeConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
			if err != nil {
				t.Fatalf("conversation: %v", err)
			}
			if _, err := sessions.Append(ctx, "sov", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{modelText(strings.Repeat("a", seedRunes))}}); err != nil {
				t.Fatal(err)
			}
			return m, conv, sessions
		}

		t.Run("one-overflow-ok", func(t *testing.T) {
			tel := &guardCountTelemetry{}
			tctx := types.WithTelemetry(ctx, tel)
			m, conv, sessions := build(t, []bool{true}, tel)
			// The 95-rune request fits the 100-rune window, the provider
			// overflows it once, the raised ceiling of 80 makes the retry
			// compact and succeed.
			if _, err := publicPathSendCollect(conv, tctx, "sov", strings.Repeat("b", sendRunes)); err != nil {
				t.Fatalf("send: %v", err)
			}
			if len(m.reqs) != 2 {
				t.Fatalf("provider calls %d: want the overflow and the one retry", len(m.reqs))
			}
			if n := tel.count("gohan.context.overflow_retry"); n != 1 {
				t.Fatalf("overflow_retry count %d: want exactly 1", n)
			}
			h, err := sessions.Load(tctx, "sov")
			if err != nil {
				t.Fatal(err)
			}
			if !compactionIn(h) {
				t.Fatal("the retry did not compact: the raised ceiling did not fire the stage")
			}
			pub := publicationOf(t, h)
			if pub.TokensBefore != seedRunes+sendRunes {
				t.Fatalf("tokens before %d: want the 95-rune request measured at the raised decision", pub.TokensBefore)
			}
		})

		t.Run("two-overflow-permanent", func(t *testing.T) {
			tel := &guardCountTelemetry{}
			tctx := types.WithTelemetry(ctx, tel)
			m, conv, _ := build(t, []bool{true, true}, tel)
			_, err := publicPathSendCollect(conv, tctx, "sov", strings.Repeat("b", sendRunes))
			if err == nil {
				t.Fatal("the second overflow completed the turn: want a permanent failure")
			}
			var me *types.ModelError
			if !errors.As(err, &me) || me.Class != types.ClassPermanent {
				t.Fatalf("err %v: want a ClassPermanent model error", err)
			}
			if len(m.reqs) != 2 {
				t.Fatalf("provider calls %d: want exactly two attempts", len(m.reqs))
			}
			if n := tel.count("gohan.context.overflow_retry"); n != 1 {
				t.Fatalf("overflow_retry count %d: want exactly 1", n)
			}
		})
	})
}

// calibratedPathModel scripts the public-path replies the calibration
// scenarios need: a first reply carrying reported usage, an optional
// side-effect ask, and a per-call overflow script. It counts request runes
// exactly like the public path's counting model.
type calibratedPathModel struct {
	profile    types.ModelProfile
	usage      *types.Usage
	usageDelta string
	ask        string
	askID      string
	overflows  []bool
	reqs       []types.ModelRequest
	n          int
}

func (m *calibratedPathModel) Profile() types.ModelProfile { return m.profile }

func (m *calibratedPathModel) Count(_ context.Context, req types.ModelRequest) (int, error) {
	n := 0
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if t, ok := b.(types.Text); ok {
				n += len([]rune(t.Text))
			}
		}
	}
	return n, nil
}

func (m *calibratedPathModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.reqs = append(m.reqs, req)
	i := m.n
	m.n++
	return func(yield func(types.ModelChunk, error) bool) {
		if i < len(m.overflows) && m.overflows[i] {
			yield(types.ModelChunk{}, &types.ModelError{Class: types.ClassContextOverflow, Provider: m.profile.Name, Err: errors.New("context window exceeded")})
			return
		}
		if i == 0 {
			if m.usageDelta != "" && !yield(types.ModelChunk{Kind: types.DeltaText, Delta: m.usageDelta, Usage: m.usage}, nil) {
				return
			}
			if m.ask != "" {
				if !yield(toolCall(m.askID, m.ask), nil) {
					return
				}
				yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
				return
			}
		}
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "ok"}, nil)
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

// calibratedAssemble fixes the request's output allowance, so the budget
// ceilings of the calibration scenarios are exact.
func calibratedAssemble(maxTokens int) func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
	return func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
		return types.ModelRequest{
			Messages: append(append([]types.Message{}, in.History...), in.Input...),
			Options:  types.ModelOptions{MaxTokens: maxTokens},
		}, nil
	}
}

// compactionIn reports whether the durable history carries a compaction
// block.
func compactionIn(h stores.History) bool {
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if _, ok := b.(types.Compaction); ok {
				return true
			}
		}
	}
	return false
}

func TestCompactionPublicPathSafePrefix(t *testing.T) {
	ctx := types.WithPrincipal(stageContext(context.Background()), types.Principal{Tenant: "t1", Subject: "u1"})
	model := &publicPathModel{}
	src := &summarizingSource{summary: "s"}
	stack, sessions, _, el := publicPathStack(t, model, src)
	conv, err := NewNativeConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	msgs := publicPathTexts(5)
	msgs = append(msgs,
		types.Message{Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: "c1", Name: "echo"}}},
		types.Message{Role: types.RoleUser, Blocks: []types.Block{types.ToolResult{ID: "c1", Outcome: types.Succeeded}}},
		textMessage(strings.Repeat("a", 10)),
		types.Message{Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: "c2", Name: "echo"}}},
	)
	if _, err := sessions.Append(ctx, "s2", 0, msgs...); err != nil {
		t.Fatal(err)
	}

	t.Run("context.compaction-pair-by-id", func(t *testing.T) {
		publicPathSend(t, conv, ctx, "s2", strings.Repeat("b", 10))
		h, err := sessions.Load(ctx, "s2")
		if err != nil {
			t.Fatal(err)
		}
		pub := publicationOf(t, h)
		// The pair closes by ToolResult.ID, so the boundary sits before
		// the pending call: five texts plus the completed pair.
		if pub.FromVersion != 10 || pub.ToVersion != 11 {
			t.Fatalf("publication versions %d..%d: want 10..11", pub.FromVersion, pub.ToVersion)
		}
		for _, m := range h.Messages {
			for _, b := range m.Blocks {
				if c, ok := b.(types.Compaction); ok && c.CoversUpTo != 8 {
					t.Fatalf("covers up to %d: want 8, the pending call left out", c.CoversUpTo)
				}
			}
		}
	})

	t.Run("context.pending-call-survives", func(t *testing.T) {
		req := model.reqs[len(model.reqs)-1]
		pending := false
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if u, ok := b.(types.ToolUse); ok && u.ID == "c2" {
					pending = true
				}
				if r, ok := b.(types.ToolResult); ok && r.ID == "c1" {
					t.Fatal("the completed pair leaked into the provider request")
				}
			}
		}
		if !pending {
			t.Fatal("the pending tool call left the provider request")
		}
		if n := summaryHeads(req); n != 1 {
			t.Fatalf("provider saw %d summaries: want one", n)
		}
	})
}

func TestCompactionPublicPathReconciled(t *testing.T) {
	ctx := types.WithPrincipal(stageContext(context.Background()), types.Principal{Tenant: "t1", Subject: "u1"})
	model := &publicPathModel{}
	stack, sessions, audit, el := publicPathStack(t, model, nil)
	conv, err := NewNativeConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	// An interrupted publisher: the durable block is committed, the audit
	// and the event never were.
	summary := compactionSummary("prior")
	sha, err := stores.CompactionSummarySHA(summary)
	if err != nil {
		t.Fatal(err)
	}
	p := stores.CompactionPublication{
		SessionID: "s3", RunID: "run-crash", RootRunID: "root-crash", Flow: "chat",
		Tenant: "t1", Subject: "u1", FromVersion: 1, ToVersion: 2,
		TokensBefore: 200, TokensAfter: 21, Policy: "public-compaction-test", SummarySHA: sha,
	}
	p.PublicationID = stores.CompactionPublicationID(p.SessionID, p.RunID, p.FromVersion)
	if _, err := sessions.Append(ctx, "s3", 0, textMessage("seed")); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendCompaction(ctx, "s3", 1, types.Message{
		ID:     p.PublicationID,
		Role:   types.RoleAssistant,
		Blocks: []types.Block{types.Compaction{Kind: types.CompactionText, CoversUpTo: 1, Summary: summary}},
		Meta:   map[string]any{stores.MetadataPayloadKey: p},
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("context.compaction-publication-persisted", func(t *testing.T) {
		t.Run("native", func(t *testing.T) {
			model := &publicPathModel{}
			src := &summarizingSource{summary: "s"}
			st := publicPathStores()
			st.Audit = failingAudit{stores.NewMemoryAuditLog()}
			s, err := Build(
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
			el := st.EventLog.(*stores.MemoryEventLog)
			conv, err := NewNativeConversation(s, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
			if err != nil {
				t.Fatalf("conversation: %v", err)
			}
			if _, err := sessions.Append(ctx, "s5", 0, publicPathTexts(40)...); err != nil {
				t.Fatal(err)
			}
			if err := publicPathSendErr(conv, ctx, "s5", strings.Repeat("b", 10)); err == nil {
				t.Fatal("send with a down audit sink succeeded: want the refusal before the provider call")
			}
			if len(model.reqs) != 0 {
				t.Fatalf("provider calls %d: want 0, the sink failure refuses the call", len(model.reqs))
			}
			h, err := sessions.Load(ctx, "s5")
			if err != nil {
				t.Fatal(err)
			}
			if h.Version != 42 {
				t.Fatalf("history version %d: want 42, the append is the commit point", h.Version)
			}
			pub := publicationOf(t, h)
			if pub.FromVersion != 41 || pub.ToVersion != 42 {
				t.Fatalf("publication versions %d..%d: want 41..42", pub.FromVersion, pub.ToVersion)
			}
		})
		t.Run("foreign", func(t *testing.T) {
			foreignInterruptedSend(t, ctx, "sf4")
		})
	})

	t.Run("context.compaction-publication-reconciled", func(t *testing.T) {
		t.Run("native", func(t *testing.T) {
			publicPathSend(t, conv, ctx, "s3", strings.Repeat("b", 10))
			records := publicPathAudit(t, audit, "s3")
			if len(records) != 1 || records[0].Kind != stores.AuditCompacted {
				t.Fatalf("audit records %+v: want the reconciled compaction record", records)
			}
			if records[0].RunID != "run-crash" {
				t.Fatalf("audit run %s: want the original publisher run-crash", records[0].RunID)
			}
			if n := len(stageEventPayloads(t, el, "run-crash")); n != 1 {
				t.Fatalf("reconciled event payloads %d: want 1 under the original run", n)
			}
		})
		t.Run("foreign", func(t *testing.T) {
			pub, sessions := foreignInterruptedSend(t, ctx, "sf2")
			foreignReconciledSend(t, ctx, sessions, "sf2", pub)
		})
	})

	t.Run("context.compaction-publication-reconciled-foreign", func(t *testing.T) {
		foreignReconciledDirectGenerate(t, ctx, "sf5")
	})

	t.Run("context.replay-after-compaction", func(t *testing.T) {
		h, err := sessions.Load(ctx, "s3")
		if err != nil {
			t.Fatal(err)
		}
		if len(h.Messages) != 4 {
			t.Fatalf("history %d messages: want the seed, the publication, the sent message and the reply", len(h.Messages))
		}
		if n := summaryHeads(model.reqs[len(model.reqs)-1]); n != 1 {
			t.Fatalf("replayed request carries %d summaries: want the durable one at the head", n)
		}
	})
}

func TestCompactionPublicPathSinkFailsClosed(t *testing.T) {
	ctx := types.WithPrincipal(stageContext(context.Background()), types.Principal{Tenant: "t1", Subject: "u1"})
	model := &publicPathModel{}
	stack, sessions, audit, el := publicPathStack(t, model, nil)
	summary := compactionSummary("prior")
	sha, err := stores.CompactionSummarySHA(summary)
	if err != nil {
		t.Fatal(err)
	}
	p := stores.CompactionPublication{
		SessionID: "s4", RunID: "run-crash", Flow: "chat",
		Tenant: "t1", Subject: "u1", FromVersion: 1, ToVersion: 2,
		TokensBefore: 200, TokensAfter: 21, Policy: "public-compaction-test", SummarySHA: sha,
	}
	p.PublicationID = stores.CompactionPublicationID(p.SessionID, p.RunID, p.FromVersion)
	if _, err := sessions.Append(ctx, "s4", 0, textMessage("seed")); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendCompaction(ctx, "s4", 1, types.Message{
		ID:     p.PublicationID,
		Role:   types.RoleAssistant,
		Blocks: []types.Block{types.Compaction{Kind: types.CompactionText, CoversUpTo: 1, Summary: summary}},
		Meta:   map[string]any{stores.MetadataPayloadKey: p},
	}); err != nil {
		t.Fatal(err)
	}
	stack.stores.Audit = failingAudit{audit}
	conv2, err := NewNativeConversation(stack, "chat", WithConversationRuns(stores.NewMemoryRuns()), WithConversationEventLog(el))
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	calls := len(model.reqs)
	var sendErr error
	for _, err := range conv2.Send(ctx, "s4", types.Message{Role: types.RoleUser, Blocks: []types.Block{modelText(strings.Repeat("b", 10))}}) {
		if err != nil {
			sendErr = err
		}
	}
	if sendErr == nil {
		t.Fatal("a failing configured audit sink let the provider run")
	}
	if len(model.reqs) != calls {
		t.Fatal("the provider was invoked despite the sink refusal")
	}
}

// failingAudit refuses every compaction record, so a configured sink
// failure is observable through the public path.
type failingAudit struct{ stores.AuditLog }

func (failingAudit) AppendCompactionAudit(context.Context, stores.AuditRecord) (bool, error) {
	return false, errors.New("audit sink down")
}

var _ guards.Guard = stdguard.NewContextGuard()

// noCounterModel declares no TokenCounter, so no compaction count is
// resolvable without a declared estimator.
type noCounterModel struct{}

func (noCounterModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "public-compaction-test", ContextWindow: 20, Caps: types.Caps{Tools: true}}
}

func (noCounterModel) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {}
}

// runeEstimator sizes a request by its text runes, mirroring the counting
// model the public path runs.
type runeEstimator struct{}

func (runeEstimator) Estimate(req types.ModelRequest, _ types.Caps) int {
	n := 0
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if t, ok := b.(types.Text); ok {
				n += len([]rune(t.Text))
			}
		}
	}
	return n
}

// countingFallback delegates to a projection and records every application,
// so the once-only fallback rule is observable.
type countingFallback struct {
	inner stores.Projection
	calls int
}

func (f *countingFallback) Project(ctx context.Context, h stores.History, p types.ModelProfile) (stores.History, error) {
	f.calls++
	return f.inner.Project(ctx, h, p)
}

func (f *countingFallback) ValidateCompactionFallback(types.ModelProfile) error { return nil }

// guardCountTelemetry records counter emissions with their attributes, so
// the guarded rejection's attribution is observable.
type guardCountTelemetry struct {
	mu     sync.Mutex
	counts map[string]int64
	attrs  map[string][]types.Attr
}

func (t *guardCountTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *guardCountTelemetry) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.counts == nil {
		t.counts = map[string]int64{}
		t.attrs = map[string][]types.Attr{}
	}
	t.counts[name] += n
	t.attrs[name] = append(t.attrs[name], attrs...)
}

func (t *guardCountTelemetry) Record(context.Context, string, float64, ...types.Attr) {}

func (t *guardCountTelemetry) count(name string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counts[name]
}

func (t *guardCountTelemetry) attr(name, key, value string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, a := range t.attrs[name] {
		if a.Key == key && a.Value == value {
			return true
		}
	}
	return false
}

// publicPathSendCollect drives one Send and returns the streamed events with
// the last error, so a refusal and its deltas stay observable together.
func publicPathSendCollect(conv Conversation, ctx context.Context, sessionID, text string) ([]types.Event, error) {
	var evs []types.Event
	var sendErr error
	for ev, err := range conv.Send(ctx, sessionID, types.Message{Role: types.RoleUser, Blocks: []types.Block{modelText(text)}}) {
		if err != nil {
			sendErr = err
			continue
		}
		if ev != nil {
			evs = append(evs, ev)
		}
	}
	return evs, sendErr
}

// opaqueEndpointModel is the publicPathModel under the opaque-cap profile:
// same deterministic counting and scripting, a different endpoint identity.
type opaqueEndpointModel struct{ publicPathModel }

func (m *opaqueEndpointModel) Profile() types.ModelProfile {
	return types.ModelProfile{
		Name:          "opaque-endpoint",
		ContextWindow: 20,
		Caps:          types.Caps{Tools: true, Compaction: types.CompactionOpaqueCap},
	}
}

// opaqueCompactor is the provider-side stand-in compactor: it produces the
// opaque block its profile's Caps.Compaction declares, bound to the profile
// that produced it.
type opaqueCompactor struct{}

func (opaqueCompactor) Compact(_ context.Context, h stores.History, p types.ModelProfile) (types.Compaction, error) {
	if p.Caps.Compaction != types.CompactionOpaqueCap {
		return types.Compaction{}, errors.New("profile " + p.Name + ": opaque compaction not served")
	}
	return types.Compaction{
		Kind:       types.CompactionOpaque,
		CoversUpTo: h.Version,
		Model:      p.Name,
		Opaque:     types.Raw{Provider: p.Name, Value: map[string]any{"type": "encrypted", "data": "opaque"}},
	}, nil
}
