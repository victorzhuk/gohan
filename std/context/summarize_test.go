package context

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// summarizingSource records the history it received and returns one Text
// block per message, carrying the message's role and text.
type summarizingSource struct {
	got    stores.History
	calls  int
	blocks []types.Block
	err    error
}

func (s *summarizingSource) summarize(_ context.Context, h stores.History, _ types.ModelProfile) ([]types.Block, error) {
	s.calls++
	s.got = h
	if s.err != nil {
		return nil, s.err
	}
	if s.blocks != nil {
		return s.blocks, nil
	}
	var out []types.Block
	for _, m := range h.Messages {
		out = append(out, types.Text{Text: string(m.Role) + ":" + messageText(m)})
	}
	return out, nil
}

func messageText(m types.Message) string {
	var sb strings.Builder
	for _, b := range m.Blocks {
		if t, ok := b.(types.Text); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

func compactHistory(msgs ...[]types.Message) stores.History {
	var h stores.History
	for _, turn := range msgs {
		h.Messages = append(h.Messages, turn...)
	}
	h.Version = int64(len(h.Messages))
	return h
}

func TestSummarizeProducesText(t *testing.T) {
	t.Run("context.compaction-tokens-measured", func(t *testing.T) {
		src := &summarizingSource{}
		s := NewSummarize(WithSummarizeSource(src.summarize), WithSummarizeEstimator(textEstimator{}))
		h := compactHistory(textTurn(10), textTurn(10), textTurn(10))
		c, err := s.Compact(context.Background(), h, profile(200))
		if err != nil {
			t.Fatal(err)
		}
		if c.Kind != types.CompactionText {
			t.Fatalf("Kind = %v, want CompactionText", c.Kind)
		}
		if c.CoversUpTo != h.Version {
			t.Fatalf("CoversUpTo = %d, want the absolute boundary %d", c.CoversUpTo, h.Version)
		}
		if c.Model != "m" {
			t.Fatalf("Model = %q, want the profile name", c.Model)
		}
		if len(c.Summary) == 0 {
			t.Fatal("empty summary persisted")
		}
		for _, b := range c.Summary {
			txt, ok := b.(types.Text)
			if !ok {
				t.Fatalf("summary block %T is not Text", b)
			}
			if txt.Origin.Kind != types.OriginModel {
				t.Fatalf("summary block origin = %v, want OriginModel", txt.Origin.Kind)
			}
		}
		want := textEstimator{}.Estimate(types.ModelRequest{Messages: []types.Message{{Blocks: c.Summary}}}, types.Caps{})
		if c.Tokens != want {
			t.Fatalf("Tokens = %d, want the rendered summary count %d", c.Tokens, want)
		}
	})

	t.Run("source-receives-covered-messages-only", func(t *testing.T) {
		src := &summarizingSource{}
		s := NewSummarize(WithSummarizeSource(src.summarize))
		h := compactHistory(textTurn(10), toolTurn("a"), textTurn(10))
		if _, err := s.Compact(context.Background(), h, profile(200)); err != nil {
			t.Fatal(err)
		}
		if src.calls != 1 {
			t.Fatalf("source called %d times, want once", src.calls)
		}
		if src.got.Version != h.Version {
			t.Fatalf("source Version = %d, want the absolute boundary %d", src.got.Version, h.Version)
		}
		if len(src.got.Messages) != len(h.Messages) {
			t.Fatalf("source received %d messages, want all %d newly covered ones", len(src.got.Messages), len(h.Messages))
		}
		for i, m := range src.got.Messages {
			if m.Role != h.Messages[i].Role || messageText(m) != messageText(h.Messages[i]) {
				t.Fatalf("source message %d differs from the covered history", i)
			}
		}
	})

	t.Run("nil-source-refused", func(t *testing.T) {
		if _, err := NewSummarize().Compact(context.Background(), compactHistory(textTurn(10)), profile(200)); err == nil {
			t.Fatal("nil source persisted a compaction")
		}
	})

	t.Run("empty-summary-refused", func(t *testing.T) {
		s := NewSummarize(WithSummarizeSource(func(context.Context, stores.History, types.ModelProfile) ([]types.Block, error) {
			return nil, nil
		}))
		if _, err := s.Compact(context.Background(), compactHistory(textTurn(10)), profile(200)); err == nil {
			t.Fatal("empty source output persisted a compaction")
		}
	})

	t.Run("source-error-propagates", func(t *testing.T) {
		want := errors.New("source failed")
		s := NewSummarize(WithSummarizeSource(func(context.Context, stores.History, types.ModelProfile) ([]types.Block, error) {
			return nil, want
		}))
		_, err := s.Compact(context.Background(), compactHistory(textTurn(10)), profile(200))
		if !errors.Is(err, want) {
			t.Fatalf("err = %v, want the source error", err)
		}
	})
}

func TestSummarizeExcludesPriorSummaries(t *testing.T) {
	t.Run("summary-guarded-prior-blocks-excluded", func(t *testing.T) {
		src := &summarizingSource{}
		s := NewSummarize(WithSummarizeSource(src.summarize))
		prior := []types.Block{types.Text{Text: "prior summary"}}
		compaction := types.Message{
			Role:   types.RoleAssistant,
			Blocks: []types.Block{types.Compaction{CoversUpTo: 4, Kind: types.CompactionText, Summary: prior}},
		}
		h := compactHistory(textTurn(10), textTurn(10), textTurn(10))
		h.Messages = append(h.Messages, compaction)
		h.Messages = append(h.Messages, textTurn(10)...)
		h.Version = int64(len(h.Messages))

		c, err := s.Compact(context.Background(), h, profile(200))
		if err != nil {
			t.Fatal(err)
		}

		covered := messageText(h.Messages[0]) + messageText(h.Messages[1])
		for _, m := range src.got.Messages {
			if messageText(m) == covered {
				t.Fatal("previously covered raw message entered the source")
			}
		}
		if len(src.got.Messages) != 4 {
			t.Fatalf("source received %d messages, want the 4 newly covered ones", len(src.got.Messages))
		}
		if len(c.Summary) != len(prior)+4 {
			t.Fatalf("summary holds %d blocks, want prior + %d source blocks", len(c.Summary), 4)
		}
		first, ok := c.Summary[0].(types.Text)
		if !ok || first.Text != "prior summary" {
			t.Fatalf("first summary block = %v, want the unchanged prior block", c.Summary[0])
		}
		for _, b := range c.Summary[1:] {
			if strings.Contains(messageText(types.Message{Blocks: []types.Block{b}}), "prior summary") {
				t.Fatal("prior summary text was re-folded into the source output")
			}
		}
	})

	t.Run("latest-prior-summary-carried-once", func(t *testing.T) {
		src := &summarizingSource{blocks: []types.Block{types.Text{Text: "new"}}}
		s := NewSummarize(WithSummarizeSource(src.summarize))
		older := types.Message{Blocks: []types.Block{types.Compaction{CoversUpTo: 2, Kind: types.CompactionText, Summary: []types.Block{types.Text{Text: "older"}}}}}
		newer := types.Message{Blocks: []types.Block{types.Compaction{CoversUpTo: 4, Kind: types.CompactionText, Summary: []types.Block{types.Text{Text: "newer"}}}}}
		h := compactHistory(textTurn(10), textTurn(10), textTurn(10))
		h.Messages = append(h.Messages, older, newer)
		h.Version = int64(len(h.Messages))

		c, err := s.Compact(context.Background(), h, profile(200))
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Summary) != 2 {
			t.Fatalf("summary holds %d blocks, want the latest prior plus one new block", len(c.Summary))
		}
		if got := c.Summary[0].(types.Text).Text; got != "newer" {
			t.Fatalf("carried prior summary = %q, want the newest one", got)
		}
		if got := c.Summary[1].(types.Text).Text; got != "new" {
			t.Fatalf("new summary block = %q, want the source output", got)
		}
	})

	t.Run("source-compaction-block-refused", func(t *testing.T) {
		s := NewSummarize(WithSummarizeSource(func(context.Context, stores.History, types.ModelProfile) ([]types.Block, error) {
			return []types.Block{types.Compaction{Kind: types.CompactionText}}, nil
		}))
		if _, err := s.Compact(context.Background(), compactHistory(textTurn(10)), profile(200)); err == nil {
			t.Fatal("source folded a compaction block into the summary")
		}
	})
}

func TestValidateCompactionFallback(t *testing.T) {
	t.Run("context.compaction-fallback-required-nil-estimator", func(t *testing.T) {
		if err := NewTruncate().ValidateCompactionFallback(profile(200)); err == nil {
			t.Fatal("nil estimator accepted")
		}
	})
	t.Run("context.compaction-fallback-required-no-budget", func(t *testing.T) {
		tr := NewTruncate(WithTruncateEstimator(textEstimator{}))
		if err := tr.ValidateCompactionFallback(profile(0)); err == nil {
			t.Fatal("non-positive usable budget accepted")
		}
	})
	t.Run("valid-fallback", func(t *testing.T) {
		tr := NewTruncate(WithTruncateEstimator(textEstimator{}))
		if err := tr.ValidateCompactionFallback(profile(200)); err != nil {
			t.Fatalf("valid fallback refused: %v", err)
		}
	})
}
