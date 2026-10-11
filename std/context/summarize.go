// Summarize folds covered history into a persisted types.Compaction. It
// implements the stores.Compactor contract: the stage persists the returned
// block, the compactor itself never writes.
package context

import (
	"context"
	"errors"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// SummarySource renders the newly covered history into summary blocks. The
// history it receives carries only the newly covered ordinary messages:
// previously covered raw messages and prior Compaction blocks never enter
// it. History.Version carries the selected absolute CoversUpTo boundary, so
// a source can report the boundary it was given.
type SummarySource func(context.Context, stores.History, types.ModelProfile) ([]types.Block, error)

// Summarize is the harness-side stores.Compactor. A nil source is inert:
// Compact returns an error rather than persisting an empty summary.
type Summarize struct {
	source    SummarySource
	estimator types.TokenEstimator
}

// SummarizeOption configures a Summarize compactor.
type SummarizeOption func(*Summarize)

// WithSummarizeSource declares the summary source. Without one the
// compactor never produces a block.
func WithSummarizeSource(src SummarySource) SummarizeOption {
	return func(s *Summarize) { s.source = src }
}

// WithSummarizeEstimator sets the TokenEstimator that measures the rendered
// summary block into Compaction.Tokens. Without one Tokens stays at zero;
// the driver measures the request itself before and after the append.
func WithSummarizeEstimator(est types.TokenEstimator) SummarizeOption {
	return func(s *Summarize) { s.estimator = est }
}

// NewSummarize builds a Summarize compactor with options applied.
func NewSummarize(opts ...SummarizeOption) Summarize {
	var s Summarize
	for _, opt := range opts {
		opt(&s)
	}
	return s
}

// Compact folds the covered prefix into one types.Compaction. The returned
// block carries Kind CompactionText, the absolute CoversUpTo boundary taken
// from History.Version, and the composed Summary: the unchanged prior
// summary blocks followed by the new source output, whose blocks carry
// Origin{Kind: OriginModel}. Prior summary text is never re-folded.
func (s Summarize) Compact(ctx context.Context, h stores.History, p types.ModelProfile) (types.Compaction, error) {
	if s.source == nil {
		return types.Compaction{}, errors.New("gohan: summarize compactor has no summary source")
	}
	newly, prior := uncoveredMessages(h.Messages)
	blocks, err := s.source(ctx, stores.History{
		Owner:      h.Owner,
		Messages:   newly,
		Version:    h.Version,
		ForkedFrom: h.ForkedFrom,
	}, p)
	if err != nil {
		return types.Compaction{}, err
	}
	for _, b := range blocks {
		if _, ok := b.(types.Compaction); ok {
			return types.Compaction{}, errors.New("gohan: summarize source returned a compaction block")
		}
	}
	summary := make([]types.Block, 0, len(prior)+len(blocks))
	summary = append(summary, prior...)
	summary = append(summary, modelOrigin(blocks)...)
	if len(summary) == 0 {
		return types.Compaction{}, errors.New("gohan: summarize source produced no summary content")
	}
	c := types.Compaction{
		BlockBase:  types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
		CoversUpTo: h.Version,
		Kind:       types.CompactionText,
		Summary:    summary,
		Model:      p.Name,
	}
	if s.estimator != nil {
		c.Tokens = s.estimator.Estimate(types.ModelRequest{Messages: []types.Message{{Blocks: summary}}}, p.Caps)
	}
	return c, nil
}

// uncoveredMessages splits a covered history into the newly covered ordinary
// messages and the latest prior summary blocks. Message positions align with
// the absolute log (Version == len(Messages)): a message at index i is
// previously covered when i < the newest prior CoversUpTo seen before it.
func uncoveredMessages(msgs []types.Message) ([]types.Message, []types.Block) {
	var covered int64
	var prior []types.Block
	for _, m := range msgs {
		for _, b := range m.Blocks {
			c, ok := b.(types.Compaction)
			if !ok {
				continue
			}
			if c.CoversUpTo > covered {
				covered = c.CoversUpTo
				prior = c.Summary
			}
		}
	}
	newly := make([]types.Message, 0, len(msgs))
	for i, m := range msgs {
		isCompaction := false
		for _, b := range m.Blocks {
			if _, ok := b.(types.Compaction); ok {
				isCompaction = true
				break
			}
		}
		if isCompaction || int64(i) < covered {
			continue
		}
		newly = append(newly, m)
	}
	return newly, prior
}

// modelOrigin stamps the model origin onto the content blocks a source
// returned. Blocks without content of their own pass through unchanged.
func modelOrigin(blocks []types.Block) []types.Block {
	out := make([]types.Block, len(blocks))
	for i, b := range blocks {
		switch v := b.(type) {
		case types.Text:
			v.Origin = types.Origin{Kind: types.OriginModel}
			out[i] = v
		case types.Reasoning:
			v.Origin = types.Origin{Kind: types.OriginModel}
			out[i] = v
		case types.Image:
			v.Origin = types.Origin{Kind: types.OriginModel}
			out[i] = v
		case types.Audio:
			v.Origin = types.Origin{Kind: types.OriginModel}
			out[i] = v
		case types.File:
			v.Origin = types.Origin{Kind: types.OriginModel}
			out[i] = v
		case types.Document:
			v.Origin = types.Origin{Kind: types.OriginModel}
			out[i] = v
		default:
			out[i] = b
		}
	}
	return out
}
