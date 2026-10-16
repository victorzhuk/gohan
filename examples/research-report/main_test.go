package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func composeReport(t *testing.T) Report {
	t.Helper()
	rep, err := Compose(context.Background(), "windowed joins and exactly-once sinks")
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return rep
}

func historyHasToolUse(history []types.Message, name string) bool {
	for _, m := range history {
		for _, b := range m.Blocks {
			if u, ok := b.(types.ToolUse); ok && u.Name == name {
				return true
			}
		}
	}
	return false
}

func historyHasCompaction(history []types.Message) bool {
	for _, m := range history {
		for _, b := range m.Blocks {
			if _, ok := b.(types.Compaction); ok {
				return true
			}
		}
	}
	return false
}

func historyHasResultText(history []types.Message, want string) bool {
	for _, m := range history {
		for _, b := range m.Blocks {
			r, ok := b.(types.ToolResult)
			if !ok {
				continue
			}
			for _, cb := range r.Content {
				if tx, ok := cb.(types.Text); ok && strings.Contains(tx.Text, want) {
					return true
				}
			}
		}
	}
	return false
}

func requestToolNames(req types.ModelRequest) []string {
	var names []string
	for _, tool := range req.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func requestHasSourceTool(req types.ModelRequest) bool {
	for _, name := range requestToolNames(req) {
		if strings.HasPrefix(name, "source_") {
			return true
		}
	}
	return false
}

func requestHasText(req types.ModelRequest, want string) bool {
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if tx, ok := b.(types.Text); ok && strings.Contains(tx.Text, want) {
				return true
			}
		}
	}
	return false
}

func requestHasCompactionSummary(req types.ModelRequest) bool {
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if tx, ok := b.(types.Text); ok && strings.Contains(tx.Text, "Research so far on") {
				return true
			}
		}
	}
	return false
}

func requestHasClearedResult(req types.ModelRequest) bool {
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			r, ok := b.(types.ToolResult)
			if !ok {
				continue
			}
			for _, cb := range r.Content {
				if tx, ok := cb.(types.Text); ok && strings.Contains(tx.Text, "[tool result cleared]") {
					return true
				}
			}
		}
	}
	return false
}

// canonicalRequest projects a ModelRequest onto the fields the assembly
// equality assertion compares: tool specs carry a Verify function that has no
// JSON form, so tools travel as their ordered name list and every other field
// keeps its exact value.
func canonicalRequest(req types.ModelRequest) ([]byte, error) {
	toolNames := make([]string, len(req.Tools))
	for i, t := range req.Tools {
		toolNames[i] = t.Name
	}
	return json.Marshal(struct {
		System   []types.Block
		Tools    []string
		Messages []types.Message
		Options  types.ModelOptions
		Metadata types.RequestMetadata
	}{req.System, toolNames, req.Messages, req.Options, req.Metadata})
}

func TestResearchReportOffline(t *testing.T) {
	t.Run("tools.activation-persists-across-resume", func(t *testing.T) {
		t.Skip("unmet prerequisite: no shipped native resume path restores a per-run deferred-tool selection; the checkpoint envelope carries no activation payload and runtime.State.ActiveTools has no production reader (design D3)")
	})
	t.Run("tools.governed-while-deferred", func(t *testing.T) {
		t.Skip("unmet prerequisite: this example installs no visibility-scoped permission lookup, so governed execution resolves over the full registered toolset (design D3)")
	})

	t.Run("composition", func(t *testing.T) {
		rep := composeReport(t)
		if len(deferredCatalog) != 40 {
			t.Fatalf("deferred source catalog has %d entries: want exactly 40", len(deferredCatalog))
		}
		if len(rep.Tools) == 0 {
			t.Fatal("the report carries no tools")
		}
		if len(rep.Events) == 0 {
			t.Fatal("the report carries no events")
		}
		if len(rep.Requests) == 0 {
			t.Fatal("the report carries no requests")
		}
		if !historyHasToolUse(rep.History, "search_tools") {
			t.Fatal("the composed run never called search_tools: deferred discovery is not part of the composition")
		}
	})

	t.Run("tools.not-assembled-until-discovered", func(t *testing.T) {
		rep := composeReport(t)
		if len(rep.Requests) == 0 {
			t.Fatal("the report carries no requests")
		}
		alwaysLoaded := map[string]bool{
			"search_archive": true,
			"take_notes":     true,
			"search_tools":   true,
		}
		for _, name := range requestToolNames(rep.Requests[0]) {
			if !alwaysLoaded[name] {
				t.Fatalf("the first assembled request carries %q: only the always-loaded fixtures and search_tools may appear before discovery", name)
			}
		}
		discovered := false
		for _, req := range rep.Requests[1:] {
			if requestHasSourceTool(req) {
				discovered = true
			}
		}
		if !discovered {
			t.Fatal("no assembled request after the search_tools result carries the discovered source definitions recomputed from the persisted discovery")
		}
	})

	t.Run("context.compaction-persisted", func(t *testing.T) {
		rep := composeReport(t)
		if !historyHasCompaction(rep.History) {
			t.Fatal("SessionLog.Load returned no Compaction block: the composed run never published a durable compaction")
		}
	})

	t.Run("working-state.notes-survive-compaction", func(t *testing.T) {
		rep := composeReport(t)
		if len(rep.Requests) == 0 {
			t.Fatal("the report carries no requests")
		}
		noteIdx, compactionIdx := -1, -1
		for i, m := range rep.History {
			for _, b := range m.Blocks {
				if u, ok := b.(types.ToolUse); ok && u.Name == "take_notes" && noteIdx < 0 {
					noteIdx = i
				}
				if _, ok := b.(types.Compaction); ok && compactionIdx < 0 {
					compactionIdx = i
				}
			}
		}
		if noteIdx < 0 {
			t.Fatal("the run never wrote a progress note through take_notes")
		}
		if compactionIdx < 0 {
			t.Fatal("the run never published a compaction")
		}
		if noteIdx > compactionIdx {
			t.Fatalf("the compaction folded the prefix before the note write (note at message %d, compaction at %d): the survival invariant binds only a note written before the fold", noteIdx, compactionIdx)
		}
		post := false
		for _, req := range rep.Requests {
			if requestHasCompactionSummary(req) && requestHasText(req, "compare batch vs stream") {
				post = true
			}
		}
		if !post {
			t.Fatal("no request assembled after the compaction carries the session note text block: the SlotSession provider did not inject the stored note after the fold")
		}
		last := rep.Requests[len(rep.Requests)-1]
		if !requestHasText(last, "compare batch vs stream") {
			t.Fatal("the request after the compaction carries no session note text block: the SlotSession provider did not inject the stored note")
		}
	})

	t.Run("limits.cost-accumulates", func(t *testing.T) {
		rep := composeReport(t)
		var done *types.Done
		for _, ev := range rep.Events {
			if d, ok := ev.(types.Done); ok {
				d := d
				done = &d
			}
		}
		if done == nil {
			t.Fatal("the composed run emitted no Done event")
		}
		if done.Cost <= 0 {
			t.Fatalf("run Done.Cost = %v: want the per-source fan-out spends accumulated into a positive parent total", done.Cost)
		}
	})

	t.Run("context.research-report-compaction", func(t *testing.T) {
		rep := composeReport(t)
		if !historyHasToolUse(rep.History, "search_tools") {
			t.Fatal("the composed run never called search_tools")
		}
		if !historyHasCompaction(rep.History) {
			t.Fatal("the composed run never published a compaction")
		}
		if len(rep.Requests) == 0 {
			t.Fatal("the report carries no requests")
		}
		last := rep.Requests[len(rep.Requests)-1]
		if !requestHasSourceTool(last) {
			t.Fatal("the final assembled request carries no discovered source definitions")
		}
		if !requestHasText(last, "compare batch vs stream") {
			t.Fatal("the final assembled request carries no progress note")
		}
	})

	t.Run("context.projection-preserves-version", func(t *testing.T) {
		topic := "windowed joins and exactly-once sinks"
		var requests []types.ModelRequest
		stack, storeStack, _, err := buildStack(topic, &requests)
		if err != nil {
			t.Fatalf("buildStack: %v", err)
		}
		sessions := storeStack.SessionLog
		conv, err := gohan.NewNativeConversation(stack, "research_report",
			gohan.WithConversationRuns(stores.NewMemoryRuns()),
			gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
		)
		if err != nil {
			t.Fatalf("NewNativeConversation: %v", err)
		}
		ctx := types.WithPrincipal(context.Background(), reportPrincipal())
		if _, err := sessions.Append(ctx, "research", 0, types.Message{
			Role:   types.RoleUser,
			Blocks: []types.Block{types.Text{Text: backlogResult()}},
		}); err != nil {
			t.Fatalf("seed backlog: %v", err)
		}
		for _, err := range conv.Send(ctx, "research", types.Message{
			Role:   types.RoleUser,
			Blocks: []types.Block{types.Text{Text: topic}},
		}) {
			if err != nil {
				t.Fatalf("send: %v", err)
			}
		}
		before, err := sessions.Load(ctx, "research")
		if err != nil {
			t.Fatalf("load before projection: %v", err)
		}
		pol, ok := stack.ContextPolicy("research_report")
		if !ok {
			t.Fatal("ContextPolicy: the research_report flow declares no policy")
		}
		if len(pol.Projections) == 0 {
			t.Fatal("the flow declares no projection")
		}
		projected, err := pol.Projections[0].Project(ctx, before, fixtureModel().Profile())
		if err != nil {
			t.Fatalf("project: %v", err)
		}
		cleared := false
		for _, m := range projected.Messages {
			for _, b := range m.Blocks {
				if r, ok := b.(types.ToolResult); ok {
					for _, cb := range r.Content {
						if tx, ok := cb.(types.Text); ok && strings.Contains(tx.Text, "[tool result cleared]") {
							cleared = true
						}
					}
				}
			}
		}
		if !cleared {
			t.Fatal("the projection cleared nothing over the persisted history: the version check would be vacuous")
		}
		after, err := sessions.Load(ctx, "research")
		if err != nil {
			t.Fatalf("load after projection: %v", err)
		}
		if after.Version != before.Version {
			t.Fatalf("history version changed across an assembly-time projection of the same persisted history: %d -> %d", before.Version, after.Version)
		}
	})

	t.Run("explain", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "run", ".", "explain")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go run . explain: %v", err)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(out, &payload); err != nil {
			t.Fatalf("explain output is not the ExplainHandler JSON object: %v\noutput: %q", err, string(out))
		}
		for _, key := range []string{
			"Flow", "Profile", "Steps", "ToolSteps", "Tools", "Strategies",
			"Fallback", "Limits", "Granularity", "Sample", "Prompts",
			"Skills", "Release", "Warnings",
		} {
			if _, ok := payload[key]; !ok {
				t.Fatalf("explain JSON lacks the %q field the shipped explain surface serves", key)
			}
		}
		var flow string
		if err := json.Unmarshal(payload["Flow"], &flow); err != nil {
			t.Fatalf("explain Flow is not a string: %v", err)
		}
		if flow != "research_report" {
			t.Fatalf("explain Flow = %q: want research_report", flow)
		}
	})

	t.Run("context.projection-does-not-write", func(t *testing.T) {
		rep := composeReport(t)
		if len(rep.Requests) == 0 {
			t.Fatal("the report carries no requests")
		}
		cleared := false
		for _, req := range rep.Requests {
			if requestHasClearedResult(req) {
				cleared = true
			}
		}
		if !cleared {
			t.Fatal("no assembled request carries a [tool result cleared] replacement: the production ClearToolResults projection is inert")
		}
		if !historyHasResultText(rep.History, "2 notes found: windowed joins, exactly-once sinks") {
			t.Fatal("SessionLog.Load no longer returns the original retrieved payload the projection cleared")
		}
	})

	t.Run("limits.ceiling-refuses-wider-fanout", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ComposeWiderFanout is not implemented: %v", r)
			}
		}()
		fr, err := ComposeWiderFanout(context.Background(), "windowed joins and exactly-once sinks")
		if err != nil {
			t.Fatalf("ComposeWiderFanout: %v", err)
		}
		if len(fr.ChildCosts) == 0 {
			t.Fatal("the wider fan-out reported no per-source child costs")
		}
		var sum float64
		for _, c := range fr.ChildCosts {
			sum += c
		}
		if math.Abs(fr.ParentCost-sum) > 1e-9 {
			t.Fatalf("parent total %v does not equal the accumulated per-source child costs %v", fr.ParentCost, sum)
		}
		if !fr.Refused {
			t.Fatal("the wider fan-out crossed the declared cost ceiling but the run completed instead of refusing it")
		}
		if !strings.Contains(strings.ToLower(fr.Reason), "cost") {
			t.Fatalf("refusal reason %q does not name the cost ceiling", fr.Reason)
		}
	})

	t.Run("streams.detached-reattach", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ComposeDetached is not implemented: %v", r)
			}
		}()
		rep, err := ComposeDetached(context.Background(), "windowed joins and exactly-once sinks")
		if err != nil {
			t.Fatalf("ComposeDetached: %v", err)
		}
		if rep.RunID == "" {
			t.Fatal("the detached composition reported no run id")
		}
		if rep.Cursor <= 0 {
			t.Fatalf("detached client cursor = %d: the client must consume a prefix of the logged events and drop at a known Seq", rep.Cursor)
		}
		for i := 1; i < len(rep.Logged); i++ {
			if rep.Logged[i].Meta.Seq != rep.Logged[i-1].Meta.Seq+1 {
				t.Fatalf("logged events are not gapless: Seq %d follows Seq %d", rep.Logged[i].Meta.Seq, rep.Logged[i-1].Meta.Seq)
			}
		}
		var want []stores.Event
		for _, e := range rep.Logged {
			if e.Meta.Seq > rep.Cursor {
				want = append(want, e)
			}
		}
		if len(want) == 0 {
			t.Fatalf("cursor %d already sits at the logged tail: reattachment proves nothing without at least one event after it", rep.Cursor)
		}
		if len(rep.Reattached) != len(want) {
			t.Fatalf("reattach delivered %d events: want exactly the %d logged events after the cursor %d, with no gap and no duplicate", len(rep.Reattached), len(want), rep.Cursor)
		}
		for i := range want {
			if !reflect.DeepEqual(rep.Reattached[i], want[i].Payload) {
				t.Fatalf("reattach event %d is not the logged payload at Seq %d: delivery diverged from the event log", i, want[i].Meta.Seq)
			}
		}
		if len(rep.ResumedRequests) == 0 {
			t.Fatal("the resumed run assembled no request")
		}
		post := -1
		for i := range rep.Requests {
			if requestHasCompactionSummary(rep.Requests[i]) {
				post = i
			}
		}
		if post < 0 {
			t.Fatal("no request the original run assembled carries the compaction summary: the compaction never reached an assembled request")
		}
		wantBytes, err := canonicalRequest(rep.Requests[post])
		if err != nil {
			t.Fatalf("marshal original request: %v", err)
		}
		gotBytes, err := canonicalRequest(rep.ResumedRequests[0])
		if err != nil {
			t.Fatalf("marshal resumed request: %v", err)
		}
		if !bytes.Equal(wantBytes, gotBytes) {
			t.Fatalf("the resumed run assembled different request bytes than the original assembled after compaction:\nwant %s\ngot  %s", wantBytes, gotBytes)
		}
		resumedLast := rep.ResumedRequests[len(rep.ResumedRequests)-1]
		if !requestHasSourceTool(resumedLast) {
			t.Fatal("the resumed request carries no discovered source definitions: the recomputed activation did not survive the resume")
		}
		if !requestHasText(resumedLast, "compare batch vs stream") {
			t.Fatal("the resumed request carries no session note text block")
		}
	})
}
