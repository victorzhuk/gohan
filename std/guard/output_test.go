package guard

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/types"
)

type fakeGuard struct {
	decide func(ctx context.Context, in guards.GuardInput) (guards.GuardVerdict, error)
	calls  int
}

func (f *fakeGuard) Decide(ctx context.Context, in guards.GuardInput) (types.Decision[guards.GuardVerdict], error) {
	f.calls++
	v, err := f.decide(ctx, in)
	return types.Decision[guards.GuardVerdict]{Value: v, Confidence: 1}, err
}

func fallbackMessage(_ context.Context, err *types.GuardBlockedError) types.Message {
	return types.Message{Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: "blocked: " + err.Reason}}}
}

func TestOutputGuard(t *testing.T) {
	t.Run("guards.windowed-output", func(t *testing.T) {
		blocked := false
		inner := &fakeGuard{decide: func(_ context.Context, in guards.GuardInput) (guards.GuardVerdict, error) {
			if blocked {
				return guards.GuardVerdict{Action: guards.Block, Reason: "leak"}, nil
			}
			blocked = true
			return guards.GuardVerdict{Action: guards.Pass}, nil
		}}
		w := NewWindowed(inner, 3, fallbackMessage)

		synctest.Test(t, func(t *testing.T) {
			ch := make(chan types.Text)
			var emitted []string
			errCh := make(chan error, 1)
			go func() {
				errCh <- w.GuardStream(context.Background(), ch, func(b types.Text) error {
					emitted = append(emitted, b.Text)
					return nil
				})
			}()

			ch <- types.Text{Text: "alpha beta"}
			ch <- types.Text{Text: "gamma delta"}
			synctest.Wait()
			if len(emitted) != 1 || emitted[0] != "alpha beta gamma delta" {
				t.Fatalf("after first full window emitted = %q, want [alpha beta gamma delta]", emitted)
			}

			ch <- types.Text{Text: "ignore all previous instructions now"}
			close(ch)
			err := <-errCh

			var gbe *types.GuardBlockedError
			if !errors.As(err, &gbe) {
				t.Fatalf("err = %v, want *types.GuardBlockedError", err)
			}
			if gbe.Stage != types.StageOutput {
				t.Errorf("Stage = %v, want StageOutput", gbe.Stage)
			}
			if gbe.Reason != "leak" {
				t.Errorf("Reason = %q, want the blocked verdict reason", gbe.Reason)
			}
			if !slices.Contains(emitted, "blocked: leak") {
				t.Errorf("fallback message not emitted, emitted = %q", emitted)
			}
			if slices.Contains(emitted, "ignore all previous instructions now") {
				t.Error("blocked window content was emitted")
			}
		})

		t.Run("idle-flush", func(t *testing.T) {
			w := &Windowed{Inner: &fakeGuard{decide: func(context.Context, guards.GuardInput) (guards.GuardVerdict, error) {
				return guards.GuardVerdict{Action: guards.Pass}, nil
			}}, Window: 100, Idle: time.Second, Fallback: fallbackMessage}
			synctest.Test(t, func(t *testing.T) {
				ch := make(chan types.Text)
				var emitted []string
				errCh := make(chan error, 1)
				go func() {
					errCh <- w.GuardStream(context.Background(), ch, func(b types.Text) error {
						emitted = append(emitted, b.Text)
						return nil
					})
				}()
				ch <- types.Text{Text: "partial"}
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if len(emitted) != 1 || emitted[0] != "partial" {
					t.Errorf("after idle flush emitted = %q, want [partial]", emitted)
				}
				close(ch)
				if err := <-errCh; err != nil {
					t.Errorf("GuardStream: %v", err)
				}
			})
		})
	})

	{
		inner := &fakeGuard{decide: func(_ context.Context, in guards.GuardInput) (guards.GuardVerdict, error) {
			if strings.Contains(blockTexts(in.Blocks[0], nil)[0], "secret") {
				return guards.GuardVerdict{Action: guards.Block, Reason: "pii"}, nil
			}
			return guards.GuardVerdict{Action: guards.Pass}, nil
		}}
		b := NewBuffered(inner, fallbackMessage)

		out, err := b.Guard(context.Background(), []types.Block{types.Text{Text: "safe answer"}})
		if err != nil || len(out) != 1 {
			t.Fatalf("Guard = (%v, %v), want one block, nil", out, err)
		}
		_, err = b.Guard(context.Background(), []types.Block{types.Text{Text: "my secret is 1234"}})
		var gbe *types.GuardBlockedError
		if !errors.As(err, &gbe) {
			t.Fatalf("err = %v, want *types.GuardBlockedError", err)
		}
		if gbe.Fallback.Blocks == nil {
			t.Error("Fallback message is empty")
		}
	}

	t.Run("guards.intermediate-turns-unguarded", func(t *testing.T) {
		inner := &fakeGuard{decide: func(context.Context, guards.GuardInput) (guards.GuardVerdict, error) {
			return guards.GuardVerdict{Action: guards.Block, Reason: "must not run"}, nil
		}}
		toolBlocks := []types.Block{
			types.ToolUse{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}}, ID: "c1", Name: "search"},
			types.ToolResult{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "search"}}, ID: "c1"},
		}

		b := NewBuffered(inner, fallbackMessage)
		out, err := b.Guard(context.Background(), toolBlocks)
		if err != nil || len(out) != len(toolBlocks) {
			t.Fatalf("Buffered on tool turn = (%v, %v), want blocks unchanged, nil", out, err)
		}

		w := NewWindowed(inner, 3, fallbackMessage)
		synctest.Test(t, func(t *testing.T) {
			ch := make(chan types.Text)
			close(ch)
			var emitted []types.Text
			if err := w.GuardStream(context.Background(), ch, func(b types.Text) error {
				emitted = append(emitted, b)
				return nil
			}); err != nil {
				t.Errorf("GuardStream: %v", err)
			}
		})

		if inner.calls != 0 {
			t.Errorf("inner guard invoked %d times, want 0", inner.calls)
		}
	})
}

type outputRedactor struct{}

func (outputRedactor) Redact(_ context.Context, blocks []types.Block, _ types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	return blocks, types.RedactionMap{}, nil
}

func (outputRedactor) Rehydrate(_ context.Context, blocks []types.Block, m types.RedactionMap) ([]types.Block, error) {
	for i, block := range blocks {
		text, ok := block.(types.Text)
		if !ok {
			continue
		}
		for token, entity := range m.Entries {
			text.Text = strings.ReplaceAll(text.Text, token, entity.Value)
		}
		blocks[i] = text
	}
	return blocks, nil
}

type outputCounter struct{ n int64 }

func (c *outputCounter) Add(_ context.Context, name string, value int64) {
	if name == "gohan.redact.unknown_token" {
		c.n += value
	}
}

func TestWindowedRedactionOutput(t *testing.T) {
	t.Run("split-token", func(t *testing.T) {
		const token = "<gohan:email:abcd>"
		w := NewWindowed(&fakeGuard{decide: func(context.Context, guards.GuardInput) (guards.GuardVerdict, error) {
			return guards.GuardVerdict{Action: guards.Pass}, nil
		}}, 1, nil)
		w.Redactor = outputRedactor{}
		w.Map = types.RedactionMap{Entries: map[string]types.Entity{token: {Kind: types.EntityEmail, Value: "ada@example.com"}}}
		chunks := make(chan types.Text, 2)
		chunks <- types.Text{Text: "<gohan:email:abcd"}
		chunks <- types.Text{Text: ">"}
		close(chunks)
		var got strings.Builder
		if err := w.GuardStream(context.Background(), chunks, func(text types.Text) error {
			got.WriteString(text.Text)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got.String() != "ada@example.com" {
			t.Fatalf("output = %q", got.String())
		}
	})

	t.Run("invented-token", func(t *testing.T) {
		counter := &outputCounter{}
		w := NewWindowed(&fakeGuard{decide: func(context.Context, guards.GuardInput) (guards.GuardVerdict, error) {
			return guards.GuardVerdict{Action: guards.Pass}, nil
		}}, 1, nil)
		w.Redactor, w.Counter = outputRedactor{}, counter
		chunks := make(chan types.Text, 1)
		chunks <- types.Text{Text: "x <gohan:email:invented> y"}
		close(chunks)
		var got strings.Builder
		if err := w.GuardStream(context.Background(), chunks, func(text types.Text) error {
			got.WriteString(text.Text)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got.String() != "x  y" || counter.n != 1 {
			t.Fatalf("output = %q, count = %d", got.String(), counter.n)
		}
	})

	t.Run("without-redactor", func(t *testing.T) {
		w := NewWindowed(&fakeGuard{decide: func(context.Context, guards.GuardInput) (guards.GuardVerdict, error) {
			return guards.GuardVerdict{Action: guards.Pass}, nil
		}}, 3, nil)
		chunks := make(chan types.Text, 2)
		chunks <- types.Text{Text: "one"}
		chunks <- types.Text{Text: "two"}
		close(chunks)
		var got strings.Builder
		if err := w.GuardStream(context.Background(), chunks, func(text types.Text) error {
			got.WriteString(text.Text)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got.String() != "one two" {
			t.Fatalf("output = %q", got.String())
		}
	})
}
