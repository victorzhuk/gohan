// Command research-report runs an offline research-report demo: a scripted
// model gathers two archived sources through gohan's governed native
// conversation, the context policy clears spent tool results and folds the
// covered prefix into a summary, and the persisted history is loaded back
// through the session log at the end. No network, no API keys.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	stdcontext "github.com/victorzhuk/gohan/std/context"
	stdguard "github.com/victorzhuk/gohan/std/guard"
	"github.com/victorzhuk/gohan/std/limit"
	stdnotes "github.com/victorzhuk/gohan/std/notes"
	stdtokens "github.com/victorzhuk/gohan/std/tokens"
	"github.com/victorzhuk/gohan/std/toolsearch"
)

// Report is what one research-report run produced: the streamed events,
// the fixture tools the batch drew on, the history the governed
// conversation persisted, and the assembled requests it recorded.
type Report struct {
	Events   []types.Event
	Tools    []types.Tool
	History  []types.Message
	Requests []types.ModelRequest
}

// newClearProjection keeps the last two tool results intact and clears
// older ones, so spent archive lookups leave the assembled requests while
// the session log keeps the originals.
func newClearProjection() stdcontext.ClearToolResults {
	c := stdcontext.NewClearToolResults(stdcontext.WithClearEstimator(stdtokens.Heuristic))
	c.Keep = 2
	c.ClearAtLeast = 1
	return c
}

// buildStack assembles the demo stack: the scripted research flow
// registered as a native definition over the fixture tools, stores and
// policy. Each assembled request is recorded through requests.
// stackOption tunes buildStack for one composition: the scripted model,
// the run cost ceiling, and the notes store the fixture tools share.
type stackOption func(*stackCfg)

type stackCfg struct {
	model      types.Model
	maxCost    float64
	notesStore stores.NotesStore
	sessionLog stores.SessionLog
}

func withModel(m types.Model) stackOption { return func(c *stackCfg) { c.model = m } }

func withMaxCost(c float64) stackOption { return func(cfg *stackCfg) { cfg.maxCost = c } }

func withNotesStore(ns stores.NotesStore) stackOption {
	return func(c *stackCfg) { c.notesStore = ns }
}

// withSessionLog points the stack at a pre-seeded session log, so a
// resumed composition loads the durable history a previous run left.
func withSessionLog(sessions stores.SessionLog) stackOption {
	return func(c *stackCfg) { c.sessionLog = sessions }
}

func buildStack(topic string, requests *[]types.ModelRequest, opts ...stackOption) (*gohan.Stack, stores.Stores, []types.Tool, error) {
	cfg := stackCfg{model: fixtureModel(), maxCost: 1000, notesStore: stores.NewMemoryNotes()}
	for _, opt := range opts {
		opt(&cfg)
	}
	sessions := cfg.sessionLog
	if sessions == nil {
		sessions = stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	}
	pair, err := stdnotes.New(cfg.notesStore)
	if err != nil {
		return nil, stores.Stores{}, nil, err
	}
	tools := fixtureTools(pair)
	stack, err := gohan.Build(append(std.Defaults(),
		gohan.WithStores(stores.Stores{SessionLog: sessions, Audit: stores.NewMemoryAuditLog(), EventLog: stores.NewMemoryEventLog()}),
		gohan.WithModels(cfg.model),
		gohan.WithEstimator(stdtokens.Heuristic),
		gohan.WithCompactionGuard(stdguard.NewContextGuard()),
		gohan.WithCompactionFallback(stdcontext.NewTruncate(stdcontext.WithTruncateEstimator(stdtokens.Heuristic))),
		gohan.WithModelMiddleware(limit.Limits(types.RunLimits{
			MaxTurns:     20,
			MaxToolCalls: 40,
			MaxCost:      cfg.maxCost,
			MaxWallClock: time.Minute,
			SoftRatio:    0.8,
		}, types.Pricing{Input: 0.001, Output: 0.002})),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "research_report"},
			Profile: "scripted",
			ContextPolicy: stores.ContextPolicy{
				Projections: []stores.Projection{newClearProjection()},
				Compactor: stdcontext.NewSummarize(
					stdcontext.WithSummarizeEstimator(stdtokens.Heuristic),
					stdcontext.WithSummarizeSource(func(_ context.Context, h stores.History, _ types.ModelProfile) ([]types.Block, error) {
						text := fmt.Sprintf("Research so far on %s: %d messages covered.", topic, len(h.Messages))
						if active := discoveredSources(h.Messages); len(active) > 0 {
							text += " Active sources: " + strings.Join(active, ", ") + "."
						}
						return []types.Block{types.Text{Text: text}}, nil
					}),
				),
			},
			Assemble: func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				specs, err := std.NarrowTools(in.Tools, toolsearch.DeferredFilter(discoveredSources(in.History)), len(*requests)+1)
				if err != nil {
					return types.ModelRequest{}, err
				}
				req := types.ModelRequest{Messages: slices.Clone(in.History), Tools: specs}
				blocks, err := pair.Provide(ctx, in.Run)
				if err != nil {
					return types.ModelRequest{}, err
				}
				if len(blocks) > 0 {
					req.Messages = append(req.Messages, types.Message{Role: types.RoleUser, Blocks: blocks})
				}
				*requests = append(*requests, req)
				return req, nil
			},
			Tools: tools,
			Decider: policyDecider{
				"search_archive":       permission.Allow,
				"take_notes":           permission.Allow,
				"search_tools":         permission.Allow,
				"source_corpus_batch":  permission.Allow,
				"source_corpus_stream": permission.Allow,
			},
		}),
	)...)
	if err != nil {
		return nil, stores.Stores{}, nil, err
	}
	return stack, stores.Stores{SessionLog: sessions}, tools, nil
}

// Compose builds a stack with the scripted research flow registered as a
// native definition, obtains the governed native conversation for it, and
// streams one send. The context policy projects spent tool results away
// and summarizes the covered prefix; the example persists nothing by hand.
func Compose(ctx context.Context, topic string) (Report, error) {
	var requests []types.ModelRequest
	stack, storeStack, tools, err := buildStack(topic, &requests)
	if err != nil {
		return Report{}, err
	}
	sessions := storeStack.SessionLog
	conv, err := gohan.NewNativeConversation(stack, "research_report",
		gohan.WithConversationRuns(stores.NewMemoryRuns()),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		return Report{}, err
	}
	ctx = types.WithPrincipal(ctx, reportPrincipal())
	// The archive backlog is persisted before the run starts: the compaction
	// fixed point measures the working history, and only store-appended
	// messages carry the canonical ids the durable reload returns.
	if _, err := sessions.Append(ctx, "research", 0, types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: backlogResult()}},
	}); err != nil {
		return Report{}, err
	}
	var report Report
	for ev, err := range conv.Send(ctx, "research", types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: topic}},
	}) {
		if err != nil {
			return report, err
		}
		report.Events = append(report.Events, ev)
	}
	// The continue send reloads the persisted history: its first assembly
	// crosses the fixture window and folds the covered prefix — the
	// settled archive-and-note pair among it — from store-canonical
	// messages only, then runs the discovered corpus sources and the
	// final report turn.
	for ev, err := range conv.Continue(ctx, "research") {
		if err != nil {
			return report, err
		}
		report.Events = append(report.Events, ev)
	}
	loaded, err := sessions.Load(ctx, "research")
	if err != nil {
		return report, err
	}
	report.Tools = tools
	report.History = loaded.Messages
	report.Requests = requests
	return report, nil
}

// DetachedReport is what one detached research-report run produced for a
// client that dropped mid-run and reattached: the run's id, the Seq cursor
// the client had consumed when it dropped, every event the run logged with
// its metadata, the events Attach delivered after the cursor, every request
// the original run assembled, the requests the resumed run assembled, and
// the persisted history.
type DetachedReport struct {
	RunID           string
	Cursor          int64
	Logged          []stores.Event
	Reattached      []types.Event
	Requests        []types.ModelRequest
	ResumedRequests []types.ModelRequest
	History         []types.Message
}

// ComposeDetached runs the research composition detached: gohan.Detached()
// mints the run, Send starts it and returns without streaming, a client
// consumes a prefix of the logged events, drops at a known Seq, and
// reattaches through Attach with that cursor. The original run then
// continues through Continue and reassembles from the compacted history.
// detachedModel scripts the detached composition across three sends: the
// detached send takes the always-loaded batch, runs the search_tools
// discovery, and closes with an interim digest; the original continue
// send reassembles from the reloaded history, publishes the compaction,
// and closes with a short ack; the resumed continue send consumes the
// discovered corpus sources and the final report turn.
func detachedModel() types.Model {
	calls := append(fixtureBatch(),
		types.ToolUse{ID: "call_search_tools", Name: toolsearch.SearchToolsName, Args: jsontextArgs(`{"query":"corpus"}`)},
		types.ToolUse{ID: "call_source_corpus_batch", Name: "source_corpus_batch", Args: jsontextArgs(`{"kind":"batch"}`)},
		types.ToolUse{ID: "call_source_corpus_stream", Name: "source_corpus_stream", Args: jsontextArgs(`{"kind":"stream"}`)},
	)
	return &scriptedTurns{turns: [][]types.ModelChunk{
		{{ToolUse: &calls[0]}, {ToolUse: &calls[1]}, {Finish: types.FinishToolUse, Usage: turnUsage(160, 40)}},
		{{ToolUse: &calls[2]}, {Finish: types.FinishToolUse, Usage: turnUsage(140, 30)}},
		{{Kind: types.DeltaText, Delta: interimDigest()}, {Finish: types.FinishStop, Usage: turnUsage(180, 45)}},
		{{Kind: types.DeltaText, Delta: "Interim acknowledged."}, {Finish: types.FinishStop, Usage: turnUsage(180, 10)}},
		{{ToolUse: &calls[3]}, {ToolUse: &calls[4]}, {Finish: types.FinishToolUse, Usage: turnUsage(180, 45)}},
		{{Kind: types.DeltaText, Delta: "Resumed report: the batch and stream threads still compare as recorded."}, {Finish: types.FinishStop, Usage: turnUsage(210, 70)}},
	}}
}

// capturingRuns wraps the runs store so the example learns the run id the
// detached Send minted: a detached Send returns without streaming, so the
// caller cannot read the id from the iterator.
type capturingRuns struct {
	stores.Runs
	mu sync.Mutex
	id string
}

func (r *capturingRuns) Start(ctx context.Context, run stores.Run, ttl time.Duration) (stores.Lease, error) {
	l, err := r.Runs.Start(ctx, run, ttl)
	if err == nil {
		r.mu.Lock()
		r.id = run.RunID
		r.mu.Unlock()
	}
	return l, err
}

func (r *capturingRuns) captured() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.id
}

// waitLogged drains the event log until the run records a terminal event
// or the deadline passes.
func waitLogged(ctx context.Context, log stores.EventLog, runID string) ([]stores.Event, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		var logged []stores.Event
		done := false
		for ev, err := range log.Read(ctx, runID, 0) {
			if err != nil {
				return nil, err
			}
			logged = append(logged, ev)
			switch ev.Payload.(type) {
			case types.Done, types.Suspended:
				done = true
			}
		}
		if done {
			return logged, nil
		}
		if time.Now().After(deadline) {
			return logged, fmt.Errorf("detached run %s recorded no terminal event within 10s", runID)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func ComposeDetached(ctx context.Context, topic string) (DetachedReport, error) {
	var requests []types.ModelRequest
	notesStore := stores.NewMemoryNotes()
	model := detachedModel()
	stack, storeStack, _, err := buildStack(topic, &requests, withModel(model), withNotesStore(notesStore))
	if err != nil {
		return DetachedReport{}, err
	}
	sessions := storeStack.SessionLog
	log := stores.NewMemoryEventLog()
	runs := &capturingRuns{Runs: stores.NewMemoryRuns()}
	conv, err := gohan.NewNativeConversation(stack, "research_report",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(log),
		gohan.Detached(),
	)
	if err != nil {
		return DetachedReport{}, err
	}
	ctx = types.WithPrincipal(ctx, reportPrincipal())
	// The archive backlog is persisted before the run starts: only
	// store-appended messages carry the canonical ids the durable reload
	// returns, and the compaction folds only store-canonical history.
	if _, err := sessions.Append(ctx, "research", 0, types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: backlogResult()}},
	}); err != nil {
		return DetachedReport{}, err
	}
	for _, err := range conv.Send(ctx, "research", types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: topic}},
	}) {
		if err != nil {
			return DetachedReport{}, err
		}
	}
	runID := runs.captured()
	if runID == "" {
		return DetachedReport{}, errors.New("detached send minted no run id")
	}
	logged, err := waitLogged(ctx, log, runID)
	if err != nil {
		return DetachedReport{}, err
	}
	var rep DetachedReport
	rep.RunID = runID
	rep.Logged = logged
	// The client consumes a prefix of the logged events and drops at a
	// known Seq before the logged tail.
	rep.Cursor = logged[len(logged)-2].Meta.Seq
	for ev, err := range conv.Attach(ctx, runID, rep.Cursor) {
		if err != nil {
			return rep, err
		}
		rep.Reattached = append(rep.Reattached, ev)
	}
	// The original run continues on the reloaded history: its first
	// assembly crosses the fixture window and publishes the compaction
	// over store-canonical messages, and the short ack turn that follows
	// closes the run so the log holds exactly one message past the
	// compacted history the resumed run replays.
	for _, err := range conv.Continue(ctx, "research") {
		if err != nil {
			return rep, err
		}
	}
	loaded, err := sessions.Load(ctx, "research")
	if err != nil {
		return rep, err
	}
	rep.History = loaded.Messages
	rep.Requests = slices.Clone(requests)
	// The resumed run reassembles from the compacted history the log
	// already holds: the persisted messages as they stood at the last
	// assembly, with the store-canonical ids, minus the terminal assistant
	// response the finished run appended after its last request. The
	// cloned history lands in a fresh session log under the same session
	// name, so the compaction publication the history carries still names
	// the session that loads it, and the resumed run shares the fixture
	// notes store and the scripted model, whose next turn it consumes.
	resumedLog := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	resumed := slices.Clone(loaded.Messages)
	if len(resumed) > 0 {
		resumed = resumed[:len(resumed)-1]
	}
	// The durable compaction message keeps its predetermined publication
	// identity only through AppendCompaction; a plain append would remint
	// its id and the resumed run would refuse the payload.
	head := 0
	ver := int64(0)
	slot := 0
	// appendOriginal replays a plain history span while keeping the stored
	// bytes the original log committed: Append remints message ids, so the
	// payload is captured first and re-seeded into the slot Append filled.
	appendOriginal := func(msgs []types.Message) error {
		payloads := make([]json.RawMessage, len(msgs))
		for j := range msgs {
			b, err := json.Marshal(msgs[j])
			if err != nil {
				return fmt.Errorf("marshal replay message: %w", err)
			}
			payloads[j] = b
		}
		next, err := resumedLog.Append(ctx, "research", ver, msgs...)
		if err != nil {
			return err
		}
		for j, b := range payloads {
			identity := fmt.Sprintf("session/research/message/%d", slot+j)
			if err := resumedLog.SeedRecord(ctx, identity, stores.RecordEnvelope{
				Shape:         "Message",
				SchemaVersion: int(stores.CurrentSchemaVersion),
				Payload:       b,
			}); err != nil {
				return err
			}
		}
		slot += len(msgs)
		ver = next
		return nil
	}
	for i, m := range resumed {
		isPublication := false
		for _, b := range m.Blocks {
			if _, ok := b.(types.Compaction); ok {
				isPublication = true
			}
		}
		if !isPublication {
			continue
		}
		if head < i {
			if err := appendOriginal(resumed[head:i]); err != nil {
				return rep, err
			}
		}
		next, err := resumedLog.AppendCompaction(ctx, "research", ver, m)
		if err != nil {
			return rep, err
		}
		slot++
		ver = next
		head = i + 1
	}
	if head < len(resumed) {
		if err := appendOriginal(resumed[head:]); err != nil {
			return rep, err
		}
	}
	resumedStack, _, _, err := buildStack(topic, &requests, withModel(model), withNotesStore(notesStore), withSessionLog(resumedLog))
	if err != nil {
		return rep, err
	}
	resumedConv, err := gohan.NewNativeConversation(resumedStack, "research_report",
		gohan.WithConversationRuns(stores.NewMemoryRuns()),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		return rep, err
	}
	n := len(requests)
	for _, err := range resumedConv.Continue(ctx, "research") {
		if err != nil {
			return rep, err
		}
	}
	rep.ResumedRequests = slices.Clone(requests[n:])
	return rep, nil
}

// FanoutReport reports a wider retrieval fan-out against the declared cost
// ceiling: whether the run refused the fan-out, the refusal reason, the
// parent's accumulated total, and the per-source child costs it accumulated.
type FanoutReport struct {
	Refused    bool
	Reason     string
	ParentCost float64
	ChildCosts []float64
}

// ComposeWiderFanout composes the same research flow with a retrieval
// fan-out wider than the declared ceiling admits and reports the refusal.
// fanoutModel wraps the scripted wider fan-out and attributes each model
// call's charge, at the fixture pricing, to the source that turn serves.
type fanoutModel struct {
	turns *scriptedTurns
	order []string

	mu    sync.Mutex
	costs map[string]float64
}

func (m *fanoutModel) Profile() types.ModelProfile { return m.turns.Profile() }

func (m *fanoutModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		src := ""
		if m.turns.i < len(m.order) {
			src = m.order[m.turns.i]
		}
		for chunk, err := range m.turns.Generate(ctx, req) {
			if chunk.Usage != nil && src != "" {
				m.mu.Lock()
				m.costs[src] += float64(chunk.Usage.InputTokens)*0.001 + float64(chunk.Usage.OutputTokens)*0.002
				m.mu.Unlock()
			}
			if !yield(chunk, err) {
				return
			}
			if err != nil {
				return
			}
		}
	}
}

func (m *fanoutModel) sources() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	seen := map[string]bool{}
	for _, n := range m.order {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	return names
}

func (m *fanoutModel) costOf(src string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.costs[src]
}

func ComposeWiderFanout(ctx context.Context, topic string) (FanoutReport, error) {
	batch := types.ToolUse{ID: "call_wide_batch", Name: "source_corpus_batch", Args: jsontextArgs(`{"kind":"batch"}`)}
	stream := types.ToolUse{ID: "call_wide_stream", Name: "source_corpus_stream", Args: jsontextArgs(`{"kind":"stream"}`)}
	search := types.ToolUse{ID: "call_wide_search_tools", Name: toolsearch.SearchToolsName, Args: jsontextArgs(`{"query":"corpus"}`)}
	calls := append(fixtureBatch(), search, batch, stream, batch, stream)
	m := &fanoutModel{
		turns: &scriptedTurns{turns: [][]types.ModelChunk{
			{{ToolUse: &calls[0]}, {ToolUse: &calls[1]}, {Finish: types.FinishToolUse, Usage: turnUsage(160, 40)}},
			{{ToolUse: &calls[2]}, {Finish: types.FinishToolUse, Usage: turnUsage(140, 30)}},
			{{ToolUse: &calls[3]}, {Finish: types.FinishToolUse, Usage: turnUsage(200, 60)}},
			{{ToolUse: &calls[4]}, {Finish: types.FinishToolUse, Usage: turnUsage(200, 60)}},
			{{ToolUse: &calls[5]}, {Finish: types.FinishToolUse, Usage: turnUsage(200, 60)}},
			{{ToolUse: &calls[6]}, {Finish: types.FinishToolUse, Usage: turnUsage(200, 60)}},
			{{Kind: types.DeltaText, Delta: "Wide report."}, {Finish: types.FinishStop, Usage: turnUsage(210, 70)}},
		}},
		order: []string{"search_archive", "", "source_corpus_batch", "source_corpus_stream", "source_corpus_batch", "source_corpus_stream", ""},
		costs: map[string]float64{},
	}
	var requests []types.ModelRequest
	stack, _, _, err := buildStack(topic, &requests, withModel(m), withMaxCost(1.0))
	if err != nil {
		return FanoutReport{}, err
	}
	conv, err := gohan.NewNativeConversation(stack, "research_report",
		gohan.WithConversationRuns(stores.NewMemoryRuns()),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		return FanoutReport{}, err
	}
	ctx = types.WithPrincipal(ctx, reportPrincipal())
	var refusal error
	for _, err := range conv.Send(ctx, "research", types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: topic}},
	}) {
		if err != nil {
			refusal = err
		}
	}
	var rep FanoutReport
	for _, src := range m.sources() {
		c := m.costOf(src)
		rep.ChildCosts = append(rep.ChildCosts, c)
		rep.ParentCost += c
	}
	var exceeded *types.LimitExceededError
	if refusal != nil {
		if errors.As(refusal, &exceeded) {
			rep.Refused = true
			rep.Reason = refusal.Error()
		} else {
			return rep, refusal
		}
	}
	return rep, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "explain" {
		if _, err := os.Stdout.Stat(); err == nil && !isTerminal(os.Stdout) {
			if err := runExplain(os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "research-report:", err)
				os.Exit(1)
			}
			return
		}
		var buf bytes.Buffer
		if err := runExplain(&buf); err != nil {
			fmt.Fprintln(os.Stderr, "research-report:", err)
			os.Exit(1)
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, buf.Bytes(), "", "  "); err == nil {
			fmt.Println(pretty.String())
		} else {
			fmt.Print(buf.String())
		}
		return
	}
	report, err := Compose(context.Background(), "Research signal pipelines: search the archive and take notes.")
	if err != nil {
		fmt.Fprintln(os.Stderr, "research-report:", err)
		os.Exit(1)
	}
	for _, ev := range report.Events {
		if d, ok := ev.(types.TextDelta); ok {
			fmt.Print(d.Delta)
		}
	}
	fmt.Println()
	for _, m := range report.History {
		for _, b := range m.Blocks {
			if res, ok := b.(types.ToolResult); ok {
				fmt.Printf("source: %s: %v\n", res.ID, res.Outcome)
			}
		}
	}
}

// isTerminal reports whether w is a character device such as an
// interactive terminal.
func isTerminal(w *os.File) bool {
	st, err := w.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// runExplain prints the resolved configuration of the research_report flow
// exactly as std.ExplainHandler serves it: no provider call runs and no run
// budget is spent. The output is JSON when stdout is not a terminal.
func runExplain(w io.Writer) error {
	stack, _, _, err := buildStack("windowed joins and exactly-once sinks", &[]types.ModelRequest{})
	if err != nil {
		return err
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/explain?flow=research_report", nil)
	std.ExplainHandler(stack).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("explain: status %d: %s", rec.Code, rec.Body.String())
	}
	_, err = w.Write(rec.Body.Bytes())
	return err
}
