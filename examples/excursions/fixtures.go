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

// excursionPrincipal is the demo operator: it may read and write the
// session but carries no approve scope, so every risky call depends on
// the fixture policy below.
func excursionPrincipal() types.Principal {
	return types.Principal{
		Tenant:  "local",
		Subject: "reader",
		Scopes:  []string{types.ScopeSessionRead, types.ScopeSessionWrite},
	}
}

// fixtureBatch is the tool batch the scripted model asks for on its first
// turn: one read-only lookup and one side-effecting booking the demo
// policy denies.
func fixtureBatch() []types.ToolUse {
	return []types.ToolUse{
		{ID: "call_search_trails", Name: "search_trails", Args: jsontextArgs(`{"region":"dolomites"}`)},
		{ID: "call_book_cabin", Name: "book_cabin", Args: jsontextArgs(`{"cabin":"rifugio-12","nights":2}`)},
	}
}

func jsontextArgs(s string) (v jsontext.Value) {
	return jsontext.Value(s)
}

// fixtureTools builds the offline tool doubles. Every result is fixed
// here, so a run never touches a network or a filesystem.
func fixtureTools() []types.Tool {
	search := gohantest.NewFakeTool("search_trails",
		gohantest.WithToolSpec(func(s *types.ToolSpec) {
			s.Description = "Look up marked trails in a region."
			s.Effect = types.ReadOnly
		}),
		gohantest.WithToolResult(types.ToolResult{
			Outcome: types.Succeeded,
			Content: []types.Block{types.Text{Text: "3 trails found: Alpe Devero, Rifugio Cadini, Tre Cime loop"}},
		}),
	)
	book := gohantest.NewFakeTool("book_cabin",
		gohantest.WithToolSpec(func(s *types.ToolSpec) {
			s.Description = "Book a mountain cabin."
			s.Effect = types.SideEffect
			s.Risk = types.RiskMedium
		}),
		gohantest.WithToolResult(types.ToolResult{
			Outcome: types.Succeeded,
			Content: []types.Block{types.Text{Text: "cabin booked"}},
		}),
	)
	return []types.Tool{search, book}
}

// fixtureModel scripts one multi-call tool turn followed by the final
// text reply; both batch calls arrive in a single model turn.
func fixtureModel() types.Model {
	calls := fixtureBatch()
	return &scriptedTurns{turns: [][]types.ModelChunk{
		{{ToolUse: &calls[0]}, {ToolUse: &calls[1]}, {Finish: types.FinishToolUse}},
		{{Kind: types.DeltaText, Delta: "Cabin denied by policy; pick from the 3 trails found instead."}, {Finish: types.FinishStop}},
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

// policyDecider is the fixture permission policy of the demo: lookups
// pass, bookings are denied outright so the run stays offline. A reader
// sees the denial as a not_executed result in the appended history.
type policyDecider map[string]permission.Verdict

func (p policyDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	v, ok := p[inv.Spec.Name]
	if !ok {
		return types.Decision[permission.Verdict]{Value: permission.Ask}, nil
	}
	return types.Decision[permission.Verdict]{Value: v, Confidence: 1}, nil
}
