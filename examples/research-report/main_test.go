package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

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
}
