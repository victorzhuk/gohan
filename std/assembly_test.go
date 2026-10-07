package std

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type fixedProvider struct {
	slot   types.ContextSlot
	blocks []types.Block
}

func (p fixedProvider) Slot() types.ContextSlot { return p.slot }

func (p fixedProvider) Provide(context.Context, types.RunInfo) ([]types.Block, error) {
	return p.blocks, nil
}

func assemble(t *testing.T, in AssembleInput) types.ModelRequest {
	t.Helper()
	req, err := StablePrefix{}.Assemble(context.Background(), in)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return req
}

func marshalBlocks(t *testing.T, blocks []types.Block) []byte {
	t.Helper()
	b, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	return b
}

func namesJSON(t *testing.T, specs []types.ToolSpec) []byte {
	t.Helper()
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	b, err := json.Marshal(names)
	if err != nil {
		t.Fatalf("marshal tool names: %v", err)
	}
	return b
}

func baseInput() AssembleInput {
	return AssembleInput{
		Run:     types.RunInfo{Turn: 3},
		Profile: types.ModelProfile{Name: "gpt", Version: "1"},
		System:  []types.Block{types.Text{Text: "be helpful"}},
		History: []types.Message{
			{ID: "m1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}},
		},
		Input: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "next"}}}},
	}
}

func TestAssembleCarriesCostTags(t *testing.T) {
	tags := types.CostTags{Feature: "search", Environment: "prod", CostCenter: "cc-9"}
	in := baseInput()
	in.Run.CostTags = tags
	req := assemble(t, in)
	if req.Metadata.CostTags != tags {
		t.Fatalf("metadata = %+v, want %+v", req.Metadata.CostTags, tags)
	}
	// The tags are run attribution, never content: no block payload
	// carries them.
	for _, b := range req.System {
		payload, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal block: %v", err)
		}
		for _, v := range []string{tags.Feature, tags.Environment, tags.CostCenter} {
			if bytes.Contains(payload, []byte(v)) {
				t.Fatalf("cost tag %q leaked into a system block: %s", v, payload)
			}
		}
	}
}

func TestStablePrefix(t *testing.T) {
	t.Run("assembly.prefix-stability", func(t *testing.T) {
		providerFor := func(user string) types.ContextProvider {
			return fixedProvider{slot: types.SlotSession, blocks: []types.Block{types.Text{Text: "profile for " + user}}}
		}
		in := baseInput()
		in.Providers = map[types.ContextSlot][]types.ContextProvider{
			types.SlotStatic:  {fixedProvider{slot: types.SlotStatic, blocks: []types.Block{types.Text{Text: "skills"}}}},
			types.SlotSession: {providerFor("alice")},
		}
		first := assemble(t, in)

		other := baseInput()
		other.Run.Principal.Subject = "bob"
		other.Providers = map[types.ContextSlot][]types.ContextProvider{
			types.SlotStatic:  {fixedProvider{slot: types.SlotStatic, blocks: []types.Block{types.Text{Text: "skills"}}}},
			types.SlotSession: {providerFor("bob")},
		}
		second := assemble(t, other)

		breakBytes, err := json.Marshal([]types.Block{types.CacheBreak{}})
		if err != nil {
			t.Fatalf("marshal break: %v", err)
		}
		breakBytes = bytes.Trim(breakBytes, "[]")
		a, b := marshalBlocks(t, first.System), marshalBlocks(t, second.System)
		ia := bytes.Index(a, breakBytes)
		ib := bytes.Index(b, breakBytes)
		if ia < 0 || ib < 0 {
			t.Fatalf("no CacheBreak in marshaled system")
		}
		if !bytes.Equal(a[:ia], b[:ib]) {
			t.Fatalf("prefix up to first CacheBreak differs:\n%s\n%s", a[:ia], b[:ib])
		}
		if bytes.Equal(a, b) {
			t.Fatalf("whole systems identical despite differing session providers")
		}
	})

	t.Run("assembly.tool-order", func(t *testing.T) {
		spec := func(name string) types.ToolSpec { return types.ToolSpec{Name: name} }
		forward := assemble(t, AssembleInput{Tools: []types.ToolSpec{spec("alpha"), spec("beta"), spec("gamma")}})
		backward := assemble(t, AssembleInput{Tools: []types.ToolSpec{spec("gamma"), spec("beta"), spec("alpha")}})
		if !bytes.Equal(namesJSON(t, forward.Tools), namesJSON(t, backward.Tools)) {
			t.Fatalf("tool order not stable across registration orders")
		}
	})

	t.Run("slot order", func(t *testing.T) {
		in := baseInput()
		in.Providers = map[types.ContextSlot][]types.ContextProvider{
			types.SlotStatic: {fixedProvider{slot: types.SlotStatic, blocks: []types.Block{types.Text{Text: "static"}}}},
			types.SlotSession: {
				fixedProvider{slot: types.SlotSession, blocks: []types.Block{types.Text{Text: "session"}}},
			},
			types.SlotTurn: {fixedProvider{slot: types.SlotTurn, blocks: []types.Block{types.Text{Text: "turn"}}}},
		}
		req := assemble(t, in)
		texts := make([]string, 0, len(req.System))
		for _, b := range req.System {
			if txt, ok := b.(types.Text); ok {
				texts = append(texts, txt.Text)
			}
		}
		want := "be helpful\x00static\x00session"
		if got := strings.Join(texts, "\x00"); got != want {
			t.Fatalf("system texts = %q, want %q", got, want)
		}
		if len(req.Messages) != 3 {
			t.Fatalf("messages = %d, want history + turn + input = 3", len(req.Messages))
		}
		if req.Messages[1].Blocks[0].(types.Text).Text != "turn" {
			t.Fatalf("turn provider blocks not between history and input")
		}
		if req.Messages[2].Blocks[0].(types.Text).Text != "next" {
			t.Fatalf("input not last")
		}
	})

	t.Run("cache breaks", func(t *testing.T) {
		in := baseInput()
		in.Providers = map[types.ContextSlot][]types.ContextProvider{
			types.SlotStatic:  {fixedProvider{slot: types.SlotStatic, blocks: []types.Block{types.Text{Text: "s"}}}},
			types.SlotSession: {fixedProvider{slot: types.SlotSession, blocks: []types.Block{types.Text{Text: "m"}}}},
		}
		req := assemble(t, in)
		breaks := 0
		for _, b := range req.System {
			if _, ok := b.(types.CacheBreak); ok {
				breaks++
			}
		}
		if breaks != 2 {
			t.Fatalf("CacheBreak count = %d, want 2", breaks)
		}
		if _, ok := req.System[len(req.System)-1].(types.CacheBreak); !ok {
			t.Fatalf("second CacheBreak not after session slot")
		}
	})

	t.Run("compaction blocks", func(t *testing.T) {
		in := baseInput()
		in.History = append(in.History,
			types.Message{ID: "m2", Role: types.RoleAssistant, Blocks: []types.Block{
				types.Compaction{CoversUpTo: 7, Summary: []types.Block{types.Text{Text: "earlier"}}},
			}})
		req := assemble(t, in)
		last := req.Messages[len(req.Messages)-2]
		comp, ok := last.Blocks[0].(types.Compaction)
		if !ok {
			t.Fatalf("Compaction block not carried through: %T", last.Blocks[0])
		}
		if comp.CoversUpTo != 7 || len(comp.Summary) != 1 {
			t.Fatalf("Compaction fields lost: %+v", comp)
		}
	})
}

func TestAssembleFilterPerCall(t *testing.T) {
	filterFor := func(keep string) ToolFilter {
		return func(specs []types.ToolSpec, _ int) []types.ToolSpec {
			var out []types.ToolSpec
			for _, s := range specs {
				if s.Name == keep {
					out = append(out, s)
				}
			}
			return out
		}
	}
	in := baseInput()
	in.Tools = []types.ToolSpec{{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"}}
	in.Providers = map[types.ContextSlot][]types.ContextProvider{
		types.SlotStatic: {fixedProvider{slot: types.SlotStatic, blocks: []types.Block{types.Text{Text: "skills"}}}},
	}

	for _, keep := range []string{"gamma", "alpha", "beta"} {
		in.Filter = filterFor(keep)
		req := assemble(t, in)
		if len(req.Tools) != 1 || req.Tools[0].Name != keep {
			t.Fatalf("filter keeping %q: tools = %s", keep, namesJSON(t, req.Tools))
		}
	}
}

func TestOperatorFence(t *testing.T) {
	marker := "operator turn follows"
	operator := types.Origin{Kind: types.OriginOperator, Name: "op-1"}
	newHistory := func() []types.Message {
		return []types.Message{
			{ID: "m1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}},
			{ID: "m2", Role: types.RoleAssistant, Blocks: []types.Block{types.Text{BlockBase: types.BlockBase{Origin: operator}, Text: "operator says A"}}},
			{ID: "m3", Role: types.RoleAssistant, Blocks: []types.Block{types.Text{BlockBase: types.BlockBase{Origin: operator}, Text: "operator says B"}}},
		}
	}
	countBlocks := func(req types.ModelRequest) (markers, operatorTurns int) {
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if txt, ok := b.(types.Text); ok {
					switch txt.BlockOrigin().Kind {
					case types.OriginSystem:
						if txt.Text == marker {
							markers++
						}
					case types.OriginOperator:
						operatorTurns++
					}
				}
			}
		}
		return markers, operatorTurns
	}

	t.Run("assembly-fences-operator-turns", func(t *testing.T) {
		history := newHistory()
		p := NewStablePrefix(WithOperatorTurnPrompt(marker))
		req, err := p.Assemble(context.Background(), AssembleInput{History: history})
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		markers, operatorTurns := countBlocks(req)
		if markers != 2 || operatorTurns != 2 {
			t.Fatalf("markers=%d operatorTurns=%d, want 2/2", markers, operatorTurns)
		}
		for _, msg := range history {
			for _, b := range msg.Blocks {
				if txt, ok := b.(types.Text); ok && txt.BlockOrigin().Kind == types.OriginSystem && txt.Text == marker {
					t.Fatal("fence rewrote the input history")
				}
			}
		}
	})

	t.Run("fence-disabled-without-prompt", func(t *testing.T) {
		history := newHistory()
		req, err := (StablePrefix{}).Assemble(context.Background(), AssembleInput{History: history})
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		markers, _ := countBlocks(req)
		if markers != 0 {
			t.Fatalf("markers=%d, want none without the prompt wired", markers)
		}
	})
}
