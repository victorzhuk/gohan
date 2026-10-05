package main

import (
	"context"
	"encoding/json/jsontext"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// fixtures.go carries the part a real deployment gets for free: the
// process death between signal and recovery. Everything runs on memory
// stores, offline.

// crash simulates the pod dying right after the approval signal was
// consumed: the conversation, the stack and the scripted model are all
// lost, only the stores survive. Recovery must rebuild its position
// from the stores, not from live memory.
func (t *Trip) crash() {
	t.stack = nil
	t.conv = nil
	if err := t.start(); err != nil {
		panic("temporal-travel: " + err.Error())
	}
}

// offlineCredentials re-issues the originator's credential offline, the
// way recovery needs it before any tool executes.
type offlineCredentials struct{}

func (offlineCredentials) Credentials(_ context.Context, _ types.Principal) (types.Credential, error) {
	return types.Credential{Token: "offline", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

// toolChain journals every effectful call: a call whose journal entry is
// Completed replays its recorded result instead of executing again, so a
// replay re-runs the step without re-running the activity.
func (t *Trip) toolChain() chains.ToolChain {
	return chains.ToolChain{{
		Name: "journal",
		Kind: chains.KindJournal,
		Use: func(next types.ToolFunc) types.ToolFunc {
			return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
				// The trip runs one session; recovery re-drives must
				// reach the same journal key the live drive wrote.
				key := types.CallKey{SessionID: flowName, CallID: call.ID}
				entry, created, err := t.journal.Reserve(ctx, key,
					stores.Fingerprint(call.Name+":"+string(call.Args)))
				if err != nil {
					return types.ToolResult{}, err
				}
				if !created && entry.State == stores.Completed {
					return entry.Result, nil
				}
				res, err := next(ctx, call)
				if cerr := t.journal.Complete(ctx, key, res); err == nil {
					err = cerr
				}
				return res, err
			}
		},
	}}
}

// tools builds the offline tool doubles. Every result is fixed here, so
// a run never touches a network or a filesystem. search_flights is
// effectful on purpose: the journal records it, so a replay re-runs the
// step without executing the search again.
func (t *Trip) tools() []types.Tool {
	return []types.Tool{
		&countingTool{
			name:    "search_flights",
			effect:  types.SideEffect,
			hit:     &t.rt.searches,
			outcome: "AF123 Paris->Tokyo",
		},
		&countingTool{
			name:    "book_flight",
			effect:  types.SideEffect,
			hit:     &t.rt.bookings,
			outcome: "booking confirmed",
		},
	}
}

// countingTool is a fixture tool that counts its own executions, so a
// test can tell a replay from a re-run.
type countingTool struct {
	name    string
	effect  types.Effect
	hit     *int
	outcome string
}

func (c *countingTool) Spec() types.ToolSpec {
	return types.ToolSpec{
		Name:        c.name,
		Description: "Offline fixture for the " + c.name + " call.",
		Effect:      c.effect,
	}
}

func (c *countingTool) Call(_ context.Context, _ jsontext.Value) (types.ToolResult, error) {
	*c.hit++
	return types.ToolResult{
		Content: []types.Block{types.Text{Text: c.outcome}},
		Outcome: types.Succeeded,
	}, nil
}

// policyDecider is the fixture permission policy of the demo: the
// search passes, the booking asks for human approval, and a decider
// miss asks rather than widens permission.
type policyDecider map[string]types.Decision[permission.Verdict]

func (p policyDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	if d, ok := p[inv.Spec.Name]; ok {
		return d, nil
	}
	return types.Decision[permission.Verdict]{Value: permission.Ask, Confidence: 1}, nil
}
