package gohan

import (
	"github.com/victorzhuk/gohan/core/types"
)

// FidelityDrop counts the blocks of one kind the projection removed for a
// profile. The per-kind list, not a global metric, is the record callers
// and tests read.
type FidelityDrop struct {
	Kind  types.BlockKind
	Count int
}

// ProjectFidelity filters messages for one target profile: reasoning from
// another provider is opaque and dropped, kinds the profile declares
// Dropped are dropped, and every surviving block keeps its order. The
// blocks themselves are never edited — a dropped block is removed, not
// rewritten — and the returned drop list reports what was removed.
func ProjectFidelity(msgs []types.Message, target string, caps types.Caps) ([]types.Message, []FidelityDrop) {
	dropped := map[types.BlockKind]int{}
	out := make([]types.Message, 0, len(msgs))
	for _, msg := range msgs {
		blocks := make([]types.Block, 0, len(msg.Blocks))
		for _, b := range msg.Blocks {
			if keepBlock(b, target, caps) {
				blocks = append(blocks, b)
				continue
			}
			dropped[blockKindOf(b)]++
		}
		if len(blocks) == len(msg.Blocks) {
			out = append(out, msg)
			continue
		}
		if len(blocks) == 0 {
			continue
		}
		msg.Blocks = blocks
		out = append(out, msg)
	}
	if len(dropped) == 0 {
		return msgs, nil
	}
	drops := make([]FidelityDrop, 0, len(dropped))
	for _, kind := range []types.BlockKind{
		types.KindReasoning, types.KindImage, types.KindAudio, types.KindFile,
		types.KindDocument, types.KindCacheBreak, types.KindRaw, types.KindCompaction,
	} {
		if n := dropped[kind]; n > 0 {
			drops = append(drops, FidelityDrop{Kind: kind, Count: n})
		}
	}
	return out, drops
}

func keepBlock(b types.Block, target string, caps types.Caps) bool {
	switch r := b.(type) {
	case types.Reasoning:
		// Reasoning is opaque: it round-trips only to the provider
		// that produced it.
		return r.Provider == target
	case types.Image:
		return caps.Fidelity[types.KindImage] != types.Dropped
	case types.Audio:
		return caps.Fidelity[types.KindAudio] != types.Dropped
	case types.File:
		return caps.Fidelity[types.KindFile] != types.Dropped
	case types.Document:
		return caps.Fidelity[types.KindDocument] != types.Dropped
	case types.CacheBreak:
		return caps.Fidelity[types.KindCacheBreak] != types.Dropped
	case types.Raw:
		return caps.Fidelity[types.KindRaw] != types.Dropped
	case types.Compaction:
		// An opaque compaction is bound to the model that produced
		// it; another endpoint re-fits without it.
		if r.Kind == types.CompactionOpaque {
			return r.Model == target
		}
		return true
	default:
		return true
	}
}

func blockKindOf(b types.Block) types.BlockKind {
	switch b.(type) {
	case types.Text:
		return types.KindText
	case types.Reasoning:
		return types.KindReasoning
	case types.Image:
		return types.KindImage
	case types.Audio:
		return types.KindAudio
	case types.File:
		return types.KindFile
	case types.Document:
		return types.KindDocument
	case types.ToolUse:
		return types.KindToolUse
	case types.ToolResult:
		return types.KindToolResult
	case types.CacheBreak:
		return types.KindCacheBreak
	case types.Raw:
		return types.KindRaw
	case types.Compaction:
		return types.KindCompaction
	default:
		return types.KindRaw
	}
}
