package main

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"iter"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// reportPrincipal is the demo analyst: it may read and write the session
// but carries no approve scope, so tool calls pass through the fixture
// policy below.
func reportPrincipal() types.Principal {
	return types.Principal{
		Tenant:  "local",
		Subject: "analyst",
		Scopes:  []string{types.ScopeSessionRead, types.ScopeSessionWrite},
	}
}

// fixtureBatch is the tool batch the scripted model asks for on its first
// turn: one archive lookup and one notes write, both satisfied offline.
func fixtureBatch() []types.ToolUse {
	return []types.ToolUse{
		{ID: "call_search_archive", Name: "search_archive", Args: jsontextArgs(`{"topic":"signal pipelines"}`)},
		{ID: "call_take_notes", Name: "take_notes", Args: jsontextArgs(`{"note":"compare batch vs stream"}`)},
	}
}

func jsontextArgs(s string) (v jsontext.Value) {
	return jsontext.Value(s)
}

// fixtureTools builds the offline tool doubles. Every result is fixed
// here, so a run never touches a network or a filesystem.
func fixtureTools() []types.Tool {
	search := gohantest.NewFakeTool("search_archive",
		gohantest.WithToolSpec(func(s *types.ToolSpec) {
			s.Description = "Look up archived research notes on a topic."
			s.Effect = types.ReadOnly
		}),
		gohantest.WithToolResult(types.ToolResult{
			Outcome: types.Succeeded,
			Content: []types.Block{types.Text{Text: "2 notes found: windowed joins, exactly-once sinks"}},
		}),
	)
	notes := gohantest.NewFakeTool("take_notes",
		gohantest.WithToolSpec(func(s *types.ToolSpec) {
			s.Description = "Append a research note."
			s.Effect = types.SideEffect
			s.Risk = types.RiskLow
		}),
		gohantest.WithToolResult(types.ToolResult{
			Outcome: types.Succeeded,
			Content: []types.Block{types.Text{Text: "note stored"}},
		}),
	)
	return []types.Tool{search, notes}
}

// fixtureModel scripts one multi-call tool turn followed by the final
// report text; both batch calls arrive in a single model turn.
func fixtureModel() types.Model {
	calls := fixtureBatch()
	return &scriptedTurns{turns: [][]types.ModelChunk{
		{{ToolUse: &calls[0]}, {ToolUse: &calls[1]}, {Finish: types.FinishToolUse}},
		{{Kind: types.DeltaText, Delta: "Report: windowed joins and exactly-once sinks are the two archived threads; noted the batch-vs-stream comparison."}, {Finish: types.FinishStop}},
	}}
}

// scriptedTurns replays fixed chunk sequences, one per model call. It is
// safe for one sequential run, which is all a demo needs.
type scriptedTurns struct {
	turns [][]types.ModelChunk
	i     int
}

func (m *scriptedTurns) Profile() types.ModelProfile { return types.ModelProfile{Name: "scripted"} }

func (m *scriptedTurns) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if m.i >= len(m.turns) {
			yield(types.ModelChunk{}, errors.New("gohantest: no scripted turns left"))
			return
		}
		for _, c := range m.turns[m.i] {
			if !yield(c, nil) {
				return
			}
		}
		m.i++
	}
}

// policyDecider is the fixture permission policy of the demo: archive
// lookups and note writes pass, so the run stays offline and every tool
// result is the fixed fixture payload.
type policyDecider map[string]permission.Verdict

func (p policyDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	v, ok := p[inv.Spec.Name]
	if !ok {
		return types.Decision[permission.Verdict]{Value: permission.Ask}, nil
	}
	return types.Decision[permission.Verdict]{Value: v, Confidence: 1}, nil
}
