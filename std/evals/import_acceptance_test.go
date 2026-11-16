package evals

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// entityDetector is a types.Detector fake that finds the configured
// entity in every text block.
type entityDetector struct {
	entity string
}

func (d entityDetector) Detect(ctx context.Context, blocks []types.Block) ([]types.Span, error) {
	var spans []types.Span
	for i, b := range blocks {
		tb, ok := b.(types.Text)
		if !ok {
			continue
		}
		for off := 0; ; {
			at := strings.Index(tb.Text[off:], d.entity)
			if at < 0 {
				break
			}
			at += off
			spans = append(spans, types.Span{Block: i, Start: at, End: at + len(d.entity), Kind: types.EntityPhone})
			off = at + len(d.entity)
		}
	}
	return spans, nil
}

func textMessage(role types.Role, origin types.OriginKind, text string) types.Message {
	return types.Message{
		Role: role,
		Blocks: []types.Block{types.Text{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: origin}},
			Text:      text,
		}},
	}
}

func TestReleaseImport(t *testing.T) {
	t.Run("release.import-redacted", func(t *testing.T) {
		const phone = "+1 555 010 9999"
		const pseudonym = "subject-3f91"
		red := &pseudonymRedactor{entity: phone, pseudonym: pseudonym, secret: "\x00absent"}

		session := stores.History{
			Messages: []types.Message{
				textMessage(types.RoleUser, types.OriginUser,
					"My number is "+phone+", where is my order?"),
				{
					Role: types.RoleAssistant,
					Blocks: []types.Block{
						types.ToolUse{ID: "c1", Name: "lookup_order", Args: []byte(`{"email_subject":"x"}`)},
					},
				},
				{
					Role: types.RoleAssistant,
					Blocks: []types.Block{
						types.ToolUse{ID: "c2", Name: "refund", Args: []byte(`{"order":"7"}`)},
						types.Text{Text: "Refunded order 7; confirmation sent to " + phone + "."},
					},
				},
			},
		}

		cases, err := Import(context.Background(), session,
			WithImportRedactor(red, types.RedactionPolicy{Detectors: []types.Detector{entityDetector{entity: phone}}}))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if len(cases) != 1 {
			t.Fatalf("Import yielded %d cases; want one per recorded session", len(cases))
		}
		c := cases[0]

		if strings.Contains(c.Input, phone) {
			t.Errorf("case input holds the raw phone number; the imported case must carry the pseudonymised value")
		}
		if !strings.Contains(c.Input, pseudonym) {
			t.Errorf("case input %q lacks the pseudonym %q", c.Input, pseudonym)
		}
		if !strings.Contains(c.Expected, pseudonym) {
			t.Errorf("case expected output %q lacks the pseudonym %q", c.Expected, pseudonym)
		}
		if strings.Contains(c.Expected, phone) {
			t.Errorf("case expected output holds the raw phone number")
		}
		wantTools := []string{"lookup_order", "refund"}
		if !slices.Equal(c.Tags, wantTools) {
			t.Errorf("case tags = %v; want the original tool sequence in call order %v", c.Tags, wantTools)
		}

		if _, err := Import(context.Background(), session); err != ErrImportRedactorRequired {
			t.Errorf("Import without a redactor = %v; want ErrImportRedactorRequired", err)
		}
	})
}
