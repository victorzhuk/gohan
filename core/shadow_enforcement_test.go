package gohan_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	"github.com/victorzhuk/gohan/std/taint"
	"github.com/victorzhuk/gohan/std/tool/provider"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type shadowCount struct {
	name  string
	value int64
	attrs []types.Attr
}

type shadowRecord struct {
	name  string
	value float64
	attrs []types.Attr
}

type shadowTelemetry struct {
	mu      sync.Mutex
	counts  []shadowCount
	records []shadowRecord
}

func (t *shadowTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *shadowTelemetry) Count(_ context.Context, name string, value int64, attrs ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts = append(t.counts, shadowCount{name: name, value: value, attrs: append([]types.Attr(nil), attrs...)})
}

func (t *shadowTelemetry) Record(_ context.Context, name string, value float64, attrs ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.records = append(t.records, shadowRecord{name: name, value: value, attrs: append([]types.Attr(nil), attrs...)})
}

func (t *shadowTelemetry) countEntries(name string) []shadowCount {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []shadowCount
	for _, c := range t.counts {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

type shadowRuns struct {
	*stores.MemoryRuns
	mu   sync.Mutex
	rows []stores.Run
}

func (s *shadowRuns) Start(ctx context.Context, r stores.Run, ttl time.Duration) (stores.Lease, error) {
	s.mu.Lock()
	s.rows = append(s.rows, r)
	s.mu.Unlock()
	return s.MemoryRuns.Start(ctx, r, ttl)
}

func (s *shadowRuns) captured() []stores.Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stores.Run(nil), s.rows...)
}

type shadowJournal struct {
	*stores.MemoryJournal
	mu        sync.Mutex
	reserves  int
	completes int
}

func (j *shadowJournal) Reserve(ctx context.Context, k types.CallKey, tool string, fp stores.Fingerprint, args json.RawMessage) (stores.Entry, bool, error) {
	j.mu.Lock()
	j.reserves++
	j.mu.Unlock()
	return j.MemoryJournal.Reserve(ctx, k, tool, fp, args)
}

func (j *shadowJournal) Complete(ctx context.Context, k types.CallKey, res types.ToolResult) error {
	j.mu.Lock()
	j.completes++
	j.mu.Unlock()
	return j.MemoryJournal.Complete(ctx, k, res)
}

func (j *shadowJournal) writes() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.reserves + j.completes
}

func (j *shadowJournal) reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.reserves = 0
	j.completes = 0
}

type shadowCps struct {
	*stores.MemoryCheckpoints
	mu   sync.Mutex
	puts int
}

func (c *shadowCps) Put(ctx context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
	c.mu.Lock()
	c.puts++
	c.mu.Unlock()
	return c.MemoryCheckpoints.Put(ctx, cp)
}

func (c *shadowCps) writes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.puts
}

func (c *shadowCps) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.puts = 0
}

type shadowAskPolicy struct{}

func (shadowAskPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

type shadowHumanSessions struct {
	*stores.MemorySessionLog
}

func (s *shadowHumanSessions) Control(context.Context, string) (stores.SessionControl, error) {
	return stores.ControlHuman, nil
}

type shadowStores struct {
	tel      *shadowTelemetry
	sessions *stores.MemorySessionLog
	runs     *shadowRuns
	cps      *shadowCps
	journal  *shadowJournal
	audit    *stores.MemoryAuditLog
	events   *stores.MemoryEventLog
}

func newShadowStores() *shadowStores {
	return &shadowStores{
		tel:      &shadowTelemetry{},
		sessions: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
		runs:     &shadowRuns{MemoryRuns: stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))},
		cps:      &shadowCps{MemoryCheckpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))},
		journal:  &shadowJournal{MemoryJournal: stores.NewMemoryJournal(stores.WithMemoryJournalClock(time.Now))},
		audit:    stores.NewMemoryAuditLog(stores.WithMemoryAuditClock(time.Now)),
		events:   stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now)),
	}
}

func shadowBuild(s *shadowStores, model types.Model, native gohan.NativeSpec, shadow bool, log stores.SessionLog) (*gohan.Stack, error) {
	if native.TaintPolicy == nil {
		hook, err := taint.New(taint.Default(), s.tel)
		if err != nil {
			return nil, err
		}
		native.TaintPolicy = hook
	}
	opts := []gohan.Option{
		gohan.AllowAnonymous(),
		gohan.WithTelemetry(s.tel),
	}
	if shadow {
		opts = append(opts, gohan.WithShadowMode())
	}
	opts = append(opts,
		gohan.WithStores(stores.Stores{
			SessionLog:  log,
			Runs:        s.runs,
			Checkpoints: s.cps,
			Journal:     s.journal,
			Audit:       s.audit,
			EventLog:    s.events,
		}),
		gohan.WithModels(model),
		gohan.WithNativeAgent(native),
	)
	return gohan.Build(opts...)
}

func shadowConversation(t *testing.T, s *shadowStores, model types.Model, native gohan.NativeSpec, shadow bool) gohan.Conversation {
	return shadowConversationOnLog(t, s, model, native, shadow, s.sessions)
}

func shadowConversationOnLog(t *testing.T, s *shadowStores, model types.Model, native gohan.NativeSpec, shadow bool, log stores.SessionLog) gohan.Conversation {
	t.Helper()
	stack, err := shadowBuild(s, model, native, shadow, log)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "chat",
		gohan.WithConversationRuns(s.runs),
		gohan.WithConversationEventLog(s.events),
		gohan.WithConversationApprovalPolicy(shadowAskPolicy{}),
	)
	if err != nil {
		t.Fatalf("NewNativeConversation: %v", err)
	}
	return conv
}

func shadowDrain(t *testing.T, conv gohan.Conversation, ctx context.Context, sessionID string) []types.Event {
	t.Helper()
	var evs []types.Event
	for ev, sendErr := range conv.Send(ctx, sessionID, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "book a slot"}}}) {
		if sendErr != nil {
			t.Fatalf("Send: %v", sendErr)
		}
		if ev != nil {
			evs = append(evs, ev)
		}
	}
	return evs
}

func shadowNative(tools []types.Tool, liveControl func(context.Context, *permission.ToolInvocation) (types.TaintAction, string)) gohan.NativeSpec {
	return gohan.NativeSpec{
		Request:     gohan.FlowRequest{Name: "chat"},
		Profile:     "primary",
		Tools:       tools,
		Assemble:    std.StablePrefix{}.Assemble,
		LiveControl: liveControl,
	}
}

func shadowContext(tel types.Telemetry) context.Context {
	return types.WithTelemetry(
		types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"}),
		tel,
	)
}

func TestShadowMode(t *testing.T) {
	t.Run("release.shadow-suppressed", func(t *testing.T) {
		s := newShadowStores()
		tool := gohantest.NewFakeTool("create_booking", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Effect = types.SideEffect
		}))
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true}},
			gohantest.ToolCall("create_booking", map[string]any{"slot": "9"}),
			gohantest.Text("done"),
		)
		conv := shadowConversation(t, s, model, shadowNative([]types.Tool{tool}, nil), true)
		shadowDrain(t, conv, shadowContext(s.tel), "s1")

		if got := len(tool.Calls()); got != 0 {
			t.Fatalf("tool executions = %d, want 0", got)
		}
		reqs := model.Requests()
		if len(reqs) < 2 {
			t.Fatalf("model requests = %d, want the follow-up turn", len(reqs))
		}
		found := false
		for _, message := range reqs[1].Messages {
			for _, block := range message.Blocks {
				res, ok := block.(types.ToolResult)
				if !ok {
					continue
				}
				if res.Error == types.ShadowSuppressed && res.Outcome == types.Failed {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("follow-up turn lacks the suppressed tool result")
		}
		runsCounts := s.tel.countEntries("gohan.shadow.runs")
		if len(runsCounts) != 1 || runsCounts[0].value != 1 {
			t.Fatalf("gohan.shadow.runs entries = %+v, want exactly one count of 1", runsCounts)
		}
		supp := s.tel.countEntries("gohan.shadow.suppressed")
		if len(supp) != 1 {
			t.Fatalf("gohan.shadow.suppressed entries = %+v, want exactly one", supp)
		}
		if supp[0].value != 1 {
			t.Fatalf("gohan.shadow.suppressed value = %d, want 1", supp[0].value)
		}
		sawTool := false
		for _, a := range supp[0].attrs {
			if a.Key == types.KeyToolName && a.Value == "create_booking" {
				sawTool = true
			}
		}
		if !sawTool {
			t.Fatalf("gohan.shadow.suppressed attrs = %+v, want tool=create_booking", supp[0].attrs)
		}
	})

	t.Run("release.shadow-replayed", func(t *testing.T) {
		s := newShadowStores()
		tool := gohantest.NewFakeTool("create_booking", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Effect = types.SideEffect
		}))
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true}},
			gohantest.ToolCall("create_booking", map[string]any{"slot": "9"}),
			gohantest.Text("done"),
		)
		ctx := shadowContext(s.tel)
		key := types.CallKey{SessionID: "s1", CallID: "call_create_booking"}
		fp := gohan.ToolFingerprint(tool.Spec(), json.RawMessage(`{"slot":"9"}`))
		if _, _, err := s.journal.Reserve(ctx, key, "create_booking", fp, json.RawMessage(`{"slot":"9"}`)); err != nil {
			t.Fatalf("seed Reserve: %v", err)
		}
		if err := s.journal.Complete(ctx, key, types.ToolResult{ID: "call_create_booking", Outcome: types.Succeeded, Content: []types.Block{types.Text{Text: "booked"}}}); err != nil {
			t.Fatalf("seed Complete: %v", err)
		}
		conv := shadowConversation(t, s, model, shadowNative([]types.Tool{tool}, nil), true)
		evs := shadowDrain(t, conv, ctx, "s1")

		if got := len(tool.Calls()); got != 0 {
			t.Fatalf("tool executions = %d, want 0", got)
		}
		reqs := model.Requests()
		if len(reqs) < 2 {
			t.Fatalf("model requests = %d, want the follow-up turn", len(reqs))
		}
		sawBooked := false
		for _, message := range reqs[1].Messages {
			for _, block := range message.Blocks {
				res, ok := block.(types.ToolResult)
				if !ok {
					continue
				}
				for _, b := range res.Content {
					if txt, ok := b.(types.Text); ok && strings.Contains(txt.Text, "booked") {
						sawBooked = true
					}
				}
			}
		}
		if !sawBooked {
			t.Fatalf("model follow-up lacks the replayed booked result")
		}
		replayed := false
		for _, ev := range evs {
			fin, ok := ev.(types.ToolFinished)
			if !ok || !fin.Replayed {
				continue
			}
			replayed = true
			sawBooked = false
			for _, b := range fin.Result.Content {
				if txt, ok := b.(types.Text); ok && strings.Contains(txt.Text, "booked") {
					sawBooked = true
				}
			}
			if !sawBooked {
				t.Fatalf("replayed ToolFinished result lacks booked: %+v", fin.Result)
			}
		}
		if !replayed {
			t.Fatalf("stream lacks a ToolFinished with Replayed=true")
		}
		if got := len(s.tel.countEntries("gohan.shadow.suppressed")); got != 0 {
			t.Fatalf("gohan.shadow.suppressed entries = %d, want 0", got)
		}
	})

	t.Run("release.shadow-isolated", func(t *testing.T) {
		s := newShadowStores()
		readTool := gohantest.NewFakeTool("lookup_slot", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Effect = types.ReadOnly
		}), gohantest.WithToolResult(types.ToolResult{Outcome: types.Succeeded, Content: []types.Block{types.Text{Text: "free"}}}))
		writeTool := gohantest.NewFakeTool("create_booking", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Effect = types.SideEffect
		}))

		primary := gohantest.NewScriptedModel(types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true}},
			gohantest.Text("primary done"),
		)
		primaryConv := shadowConversation(t, s, primary, shadowNative([]types.Tool{readTool, writeTool}, nil), false)
		shadowDrain(t, primaryConv, shadowContext(s.tel), "s1")

		owner := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
		hist, err := s.sessions.Load(owner, "s1")
		if err != nil {
			t.Fatalf("primary Load: %v", err)
		}
		primaryVersion := hist.Version
		primaryMessages := len(hist.Messages)
		primaryAudit := 0
		for rec, aerr := range s.audit.Read(owner, "s1") {
			if aerr != nil {
				t.Fatalf("audit Read: %v", aerr)
			}
			_ = rec
			primaryAudit++
		}
		s.journal.reset()
		s.cps.reset()

		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true}},
			gohantest.ToolCall("create_booking", map[string]any{"slot": "9"}),
			gohantest.Text("done"),
		)
		conv := shadowConversation(t, s, model, shadowNative([]types.Tool{readTool, writeTool}, nil), true)
		shadowDrain(t, conv, shadowContext(s.tel), "s1")

		hist, err = s.sessions.Load(owner, "s1")
		if err != nil {
			t.Fatalf("primary Load after shadow: %v", err)
		}
		if hist.Version != primaryVersion || len(hist.Messages) != primaryMessages {
			t.Fatalf("primary session changed: version %d -> %d, messages %d -> %d", primaryVersion, hist.Version, primaryMessages, len(hist.Messages))
		}
		afterAudit := 0
		for _, aerr := range s.audit.Read(owner, "s1") {
			if aerr != nil {
				t.Fatalf("audit Read after shadow: %v", aerr)
			}
			afterAudit++
		}

		if afterAudit != primaryAudit {
			t.Fatalf("audit records = %d, want %d unchanged", afterAudit, primaryAudit)
		}
		if got := s.journal.writes(); got != 0 {
			t.Fatalf("journal writes = %d, want 0", got)
		}
		if got := s.cps.writes(); got != 0 {
			t.Fatalf("checkpoint writes = %d, want 0", got)
		}
		rows := s.runs.captured()
		var run stores.Run
		found := false
		for _, r := range rows {
			if strings.HasPrefix(r.SessionID, "s1/shadow/") {
				run = r
				found = true
			}
		}
		if !found {
			t.Fatalf("no shadow run row captured among %d rows", len(rows))
		}
		if run.Mode != types.Shadow {
			t.Fatalf("run mode = %d, want Shadow", run.Mode)
		}
		shadowSessionID := "s1/shadow/" + run.RunID
		if run.SessionID != shadowSessionID {
			t.Fatalf("run session = %q, want %q", run.SessionID, shadowSessionID)
		}
		shadowHist, err := s.sessions.Load(owner, shadowSessionID)
		if err != nil {
			t.Fatalf("shadow session Load: %v", err)
		}
		if got := len(shadowHist.Messages); got < 2 {
			t.Fatalf("shadow session messages = %d, want at least the input and the reply", got)
		}
		sawInput := false
		sawReply := false
		sawSuppressed := false
		for _, message := range shadowHist.Messages {
			if message.Role == types.RoleUser {
				for _, block := range message.Blocks {
					if txt, ok := block.(types.Text); ok && strings.Contains(txt.Text, "book a slot") {
						sawInput = true
					}
					if res, ok := block.(types.ToolResult); ok && res.Error != nil && res.Error.Kind == types.ShadowSuppressed.Kind && res.Error.Message == types.ShadowSuppressed.Message {
						sawSuppressed = true
					}
				}
			}
			if message.Role == types.RoleAssistant {
				for _, block := range message.Blocks {
					if txt, ok := block.(types.Text); ok && strings.Contains(txt.Text, "done") {
						sawReply = true
					}
				}
			}
		}
		if !sawInput {
			t.Fatalf("shadow session lacks the persisted user input")
		}
		if !sawReply {
			t.Fatalf("shadow session lacks the persisted assistant reply")
		}
		if !sawSuppressed {
			t.Fatalf("shadow session lacks the persisted suppressed create_booking result")
		}

		sawDone := false
		for ev, err := range s.events.Read(owner, run.RunID, 0) {
			if err != nil {
				t.Fatalf("event Read: %v", err)
			}
			if ev.Meta.Mode != types.Shadow {
				t.Fatalf("event %d meta mode = %d, want Shadow", ev.Meta.Seq, ev.Meta.Mode)
			}
			if _, ok := ev.Payload.(types.Done); ok {
				sawDone = true
			}
		}
		if !sawDone {
			t.Fatalf("shadow run events lack a Done event")
		}

		humanLog := &shadowHumanSessions{MemorySessionLog: s.sessions}
		if _, err := s.sessions.Append(owner, "s1", hist.Version, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "queued for the human"}}}); err != nil {
			t.Fatalf("human hand-off append: %v", err)
		}
		hist, err = s.sessions.Load(owner, "s1")
		if err != nil {
			t.Fatalf("human Load: %v", err)
		}
		humanVersion := hist.Version
		humanMessages := len(hist.Messages)

		humanModel := gohantest.NewScriptedModel(types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true}},
			gohantest.Text("done"),
		)
		humanConv := shadowConversationOnLog(t, s, humanModel, shadowNative([]types.Tool{readTool, writeTool}, nil), true, humanLog)
		shadowDrain(t, humanConv, shadowContext(s.tel), "s1")

		hist, err = s.sessions.Load(owner, "s1")
		if err != nil {
			t.Fatalf("primary Load after human-controlled shadow run: %v", err)
		}
		if hist.Version != humanVersion || len(hist.Messages) != humanMessages {
			t.Fatalf("human-controlled primary session changed: version %d -> %d, messages %d -> %d", humanVersion, hist.Version, humanMessages, len(hist.Messages))
		}
		var humanRun stores.Run
		humanFound := false
		for _, r := range s.runs.captured() {
			if strings.HasPrefix(r.SessionID, "s1/shadow/") {
				humanRun = r
				humanFound = true
			}
		}
		if !humanFound {
			t.Fatal("no shadow run row captured for the human-controlled session")
		}
		humanShadow, err := s.sessions.Load(owner, "s1/shadow/"+humanRun.RunID)
		if err != nil {
			t.Fatalf("human shadow session Load: %v", err)
		}
		if len(humanShadow.Messages) < 2 {
			t.Fatalf("human shadow session messages = %d, want at least the input and the reply", len(humanShadow.Messages))
		}
	})

	t.Run("release.shadow-budget", func(t *testing.T) {
		s := newShadowStores()
		pricing := types.Pricing{Input: 0.04}
		profile := types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true}, Pricing: pricing}
		turn := gohantest.Text("ok").WithUsage(types.Usage{InputTokens: 100})
		baseCtx := func() context.Context {
			return types.WithRunInfo(
				shadowContext(s.tel),
				types.RunInfo{CostTags: types.CostTags{Feature: "assist"}},
			)
		}

		primaryModel := gohantest.NewScriptedModel(profile, turn)
		primaryConv := shadowConversation(t, s, primaryModel, shadowNative(nil, nil), false)
		shadowDrain(t, primaryConv, baseCtx(), "s1")
		s.tel.mu.Lock()
		var primaryCost shadowRecord
		for _, r := range s.tel.records {
			if r.name == "gohan.cost.per_run" {
				primaryCost = r
			}
		}
		s.tel.mu.Unlock()
		if primaryCost.name != "gohan.cost.per_run" {
			t.Fatalf("gohan.cost.per_run missing after primary run")
		}

		shadowModel := gohantest.NewScriptedModel(profile, turn)
		conv := shadowConversation(t, s, shadowModel, shadowNative(nil, nil), true)
		shadowDrain(t, conv, baseCtx(), "s1")

		reqs := shadowModel.Requests()
		if len(reqs) == 0 {
			t.Fatalf("shadow model never called")
		}
		if got := reqs[0].Metadata.CostTags.Feature; got != "assist/shadow" {
			t.Fatalf("shadow request CostTags.Feature = %q, want assist/shadow", got)
		}
		s.tel.mu.Lock()
		records := append([]shadowRecord(nil), s.tel.records...)
		s.tel.mu.Unlock()
		saw := false
		for _, r := range records {
			if r.name != "gohan.cost.per_run" {
				continue
			}
			flowChat := false
			for _, a := range r.attrs {
				if a.Key == types.KeyFlow && a.Value == "chat" {
					flowChat = true
				}
			}
			if !flowChat {
				continue
			}
			saw = true
			if r.value > primaryCost.value+1e-9 {
				t.Fatalf("gohan.cost.per_run rose to %v after shadow run, primary value %v", r.value, primaryCost.value)
			}
		}
		if !saw {
			t.Fatalf("no gohan.cost.per_run record for flow chat after shadow run")
		}
	})

	t.Run("release.shadow-suspension", func(t *testing.T) {
		s := newShadowStores()
		tool := gohantest.NewFakeTool("charge", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Effect = types.SideEffect
		}))
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true}},
			gohantest.ToolCall("charge", map[string]any{"amount": 1}),
			gohantest.Text("done"),
		)
		conv := shadowConversation(t, s, model, shadowNative([]types.Tool{tool}, func(context.Context, *permission.ToolInvocation) (types.TaintAction, string) {
			return types.TaintAsk, ""
		}), true)
		evs := shadowDrain(t, conv, shadowContext(s.tel), "s1")

		if len(evs) == 0 {
			t.Fatal("empty event stream")
		}
		done, ok := evs[len(evs)-1].(types.Done)
		if !ok {
			t.Fatalf("last event = %T, want Done", evs[len(evs)-1])
		}
		if done.Reason != types.StopShadowSuspended {
			t.Fatalf("done reason = %q, want %q", done.Reason, types.StopShadowSuspended)
		}
		for _, ev := range evs {
			if _, ok := ev.(types.Suspended); ok {
				t.Fatalf("stream contains a Suspended event")
			}
		}
		if got := s.cps.writes(); got != 0 {
			t.Fatalf("checkpoint writes = %d, want 0", got)
		}
		if got := len(tool.Calls()); got != 0 {
			t.Fatalf("tool executions = %d, want 0", got)
		}
	})

	t.Run("shadow-requires-native", func(t *testing.T) {
		s := newShadowStores()
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true}},
			gohantest.Text("done"),
		)
		nativeStack, err := shadowBuild(s, model, shadowNative(nil, nil), true, s.sessions)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if _, err := gohan.NewConversation(nativeStack, "chat", runtime.NewNative()); err == nil || !strings.Contains(err.Error(), "shadow mode needs a native conversation") {
			t.Fatalf("NewConversation err = %v, want shadow mode needs a native conversation", err)
		}
		if _, err := gohan.NewNativeConversation(nativeStack, "chat", gohan.WithConversationRuns(s.runs), gohan.WithConversationEventLog(s.events)); err != nil {
			t.Fatalf("NewNativeConversation: %v", err)
		}

		foreignStack, err := gohan.Build(
			gohan.AllowAnonymous(),
			gohan.WithTelemetry(s.tel),
			gohan.WithShadowMode(),
			gohan.WithStores(stores.Stores{
				SessionLog:  s.sessions,
				Runs:        s.runs,
				Checkpoints: s.cps,
				Journal:     s.journal,
				Audit:       s.audit,
				EventLog:    s.events,
			}),
			gohan.WithModels(model),
			gohan.WithNativeAgent(shadowNative(nil, nil)),
			gohan.WithForeignAgent(gohan.ForeignSpec{
				Request: gohan.FlowRequest{Name: "ext"},
				Profile: "primary",
				NewRuntime: func() runtime.Runtime {
					return runtime.NewNative()
				},
			}),
		)
		if err != nil {
			t.Fatalf("foreign Build: %v", err)
		}
		if _, err := gohan.NewForeignConversation(foreignStack, "ext"); err == nil || !strings.Contains(err.Error(), "shadow mode needs a native conversation") {
			t.Fatalf("NewForeignConversation err = %v, want shadow mode needs a native conversation", err)
		}
	})

	t.Run("provider-side-effect-undeclared", func(t *testing.T) {
		s := newShadowStores()
		profile := types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true, ProviderTools: map[string]types.ProviderToolCap{
			"code_exec":  {},
			"web_search": {},
		}}}
		model := gohantest.NewScriptedModel(profile,
			gohantest.ToolCall("code_exec", map[string]any{}),
			gohantest.Text("done"),
		)
		conv := shadowConversation(t, s, model, shadowNative([]types.Tool{provider.CodeExec(), provider.WebSearch()}, nil), true)
		shadowDrain(t, conv, shadowContext(s.tel), "s1")

		reqs := model.Requests()
		if len(reqs) < 2 {
			t.Fatalf("model requests = %d, want the follow-up turn", len(reqs))
		}
		sawSearch := false
		for _, spec := range reqs[0].Tools {
			if spec.Name == "web_search" {
				sawSearch = true
			}
			if spec.Name == "code_exec" {
				t.Fatalf("provider request declares code_exec: %+v", spec)
			}
		}
		if !sawSearch {
			t.Fatalf("provider request lacks the read-only web_search declaration")
		}
		sawSuppressed := false
		for _, message := range reqs[1].Messages {
			for _, block := range message.Blocks {
				res, ok := block.(types.ToolResult)
				if ok && res.ID == "call_code_exec" && res.Error == types.ShadowSuppressed {
					sawSuppressed = true
				}
			}
		}
		if !sawSuppressed {
			t.Fatalf("follow-up turn lacks the suppressed code_exec result")
		}
		supp := s.tel.countEntries("gohan.shadow.suppressed")
		if len(supp) != 1 || supp[0].value != 1 {
			t.Fatalf("gohan.shadow.suppressed entries = %+v, want exactly one count of 1", supp)
		}
		sawTool := false
		for _, a := range supp[0].attrs {
			if a.Key == types.KeyToolName && a.Value == "code_exec" {
				sawTool = true
			}
		}
		if !sawTool {
			t.Fatalf("gohan.shadow.suppressed attrs = %+v, want tool=code_exec", supp[0].attrs)
		}
	})

	t.Run("read-only-denied", func(t *testing.T) {
		s := newShadowStores()
		tool := gohantest.NewFakeTool("lookup_slot", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Effect = types.ReadOnly
		}))
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true}},
			gohantest.ToolCall("lookup_slot", map[string]any{}),
			gohantest.Text("done"),
		)
		native := shadowNative([]types.Tool{tool}, nil)
		native.Decider = shadowDenyDecider{}
		conv := shadowConversation(t, s, model, native, true)
		shadowDrain(t, conv, shadowContext(s.tel), "s1")

		if got := len(tool.Calls()); got != 0 {
			t.Fatalf("tool executions = %d, want 0", got)
		}
		reqs := model.Requests()
		if len(reqs) < 2 {
			t.Fatalf("model requests = %d, want the follow-up turn", len(reqs))
		}
		sawDenied := false
		for _, message := range reqs[1].Messages {
			for _, block := range message.Blocks {
				res, ok := block.(types.ToolResult)
				if ok && res.Outcome == types.Failed && res.Error != nil && strings.HasPrefix(res.Error.Message, "not_executed:") {
					sawDenied = true
				}
			}
		}
		if !sawDenied {
			t.Fatalf("follow-up turn lacks the not_executed denial result")
		}
		if got := len(s.tel.countEntries("gohan.shadow.suppressed")); got != 0 {
			t.Fatalf("gohan.shadow.suppressed entries = %d, want 0: a denial is not a suppression", got)
		}
	})
}

type shadowDenyDecider struct{}

func (shadowDenyDecider) Decide(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	return types.Decision[permission.Verdict]{Value: permission.DenyVerdict, Confidence: 1}, nil
}
