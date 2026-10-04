package structured

import (
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// accumulate joins the deltas a stream delivered for one message. The real
// accumulator is row 23.8; these tests assert the view directly.
func accumulate(deltas []types.ResultDelta) string {
	var acc string
	for _, d := range deltas {
		acc += d.Delta
	}
	return acc
}

func TestPartial(t *testing.T) {
	t.Run("structured-output.result-delta-partial", func(t *testing.T) {
		deltas := []types.ResultDelta{
			{Turn: 1, MessageID: "m1", Delta: `{"total": 12, "lines": [{"sku": "A"`},
		}
		view, err := PartialView(accumulate(deltas))
		if err != nil {
			t.Fatalf("PartialView: %v", err)
		}
		if got := view["total"]; got != float64(12) {
			t.Fatalf("total = %v, want 12", got)
		}
		lines, ok := view["lines"].([]any)
		if !ok || len(lines) != 1 {
			t.Fatalf("lines = %#v, want one entry", view["lines"])
		}
		line, ok := lines[0].(map[string]any)
		if !ok || line["sku"] != "A" {
			t.Fatalf("line = %#v, want sku \"A\"", lines[0])
		}
	})

	t.Run("structured-output.partial-never-validated", func(t *testing.T) {
		// The stream is cut off by max_tokens; the document never completes.
		acc := `{"total": 12, "lines": [{"sku": "A"`

		view, err := PartialView(acc)
		if err != nil {
			t.Fatalf("PartialView on truncated document: %v", err)
		}
		if _, ok := view["total"]; !ok {
			t.Fatalf("partial view missing filled field: %#v", view)
		}

		// The same text never passes the validated path.
		if _, err := Partial[map[string]any](acc); !errors.Is(err, types.ErrStructuredOutput) {
			t.Fatalf("Partial on truncated document = %v, want ErrStructuredOutput", err)
		}
		// Done.Result and storage are row 23.1; the guarantee here is that no
		// validated Out exists for this text.
	})

	t.Run("deltas accumulate into one view", func(t *testing.T) {
		deltas := []types.ResultDelta{
			{Turn: 1, MessageID: "m1", Delta: `{"total": `},
			{Turn: 1, MessageID: "m1", Delta: `12, "currency": "EUR", "lines": [`},
			{Turn: 1, MessageID: "m1", Delta: `{"sku": "A", "qty": 2`},
		}
		view, err := PartialView(accumulate(deltas))
		if err != nil {
			t.Fatalf("PartialView: %v", err)
		}
		if view["currency"] != "EUR" {
			t.Fatalf("currency = %v, want EUR", view["currency"])
		}
		lines, ok := view["lines"].([]any)
		if !ok || len(lines) != 1 {
			t.Fatalf("lines = %#v, want one entry", view["lines"])
		}
	})

	t.Run("unparseable text stays unvalidated and errors", func(t *testing.T) {
		if _, err := PartialView(`not json at all`); !errors.Is(err, types.ErrStructuredOutput) {
			t.Fatalf("PartialView = %v, want ErrStructuredOutput", err)
		}
	})

	t.Run("validated path only from complete document", func(t *testing.T) {
		complete := `{"total": 12, "lines": [{"sku": "A", "qty": 2}]}`
		out, err := Partial[map[string]any](complete)
		if err != nil {
			t.Fatalf("Partial: %v", err)
		}
		if out["total"] != float64(12) {
			t.Fatalf("total = %v, want 12", out["total"])
		}
	})
}
