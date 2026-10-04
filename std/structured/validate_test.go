package structured

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type repairOrder struct {
	Quantity int    `bounds:"min=1,max=10"`
	Note     string `bounds:"-"`
}

const repairInstruction = "Return corrected JSON for the declared type."

func TestValidateRepair(t *testing.T) {
	t.Run("structured-output.validate-and-repair", func(t *testing.T) {
		vr := NewValidateRepair(2)
		d := vr.Decide(nil, errors.New("parse error: bad json"), 1, repairInstruction)
		if !d.Retry {
			t.Fatal("first failed attempt should allow a repair turn")
		}
		if d.ToolResult.Outcome != types.Failed {
			t.Fatalf("Outcome = %v, want Failed", d.ToolResult.Outcome)
		}
		if d.ToolResult.Error == nil || d.ToolResult.Error.Kind != types.Permanent {
			t.Fatalf("ToolResult.Error = %+v, want Permanent kind", d.ToolResult.Error)
		}
		text, ok := d.ToolResult.Content[0].(types.Text)
		if !ok {
			t.Fatalf("Content[0] is %T, want types.Text", d.ToolResult.Content[0])
		}
		if !strings.Contains(text.Text, repairInstruction) || !strings.Contains(text.Text, "bad json") {
			t.Fatalf("repair result text misses instruction or validation error: %q", text.Text)
		}
		// Turn execution belongs to the Drive loop (row 22.4); recorded as a
		// deferral, not asserted here.

		d = vr.Decide(nil, errors.New("parse error: bad json"), 2, repairInstruction)
		if d.Retry || d.Err == nil || !errors.Is(d.Err, types.ErrStructuredOutput) {
			t.Fatalf("after max failures: Retry=%v Err=%v, want exhausted with ErrStructuredOutput", d.Retry, d.Err)
		}
	})

	t.Run("structured-output.bounds-validated-after-constrained-decoding", func(t *testing.T) {
		var out repairOrder
		if err := json.Unmarshal([]byte(`{"Quantity": 0}`), &out); err != nil {
			t.Fatal(err)
		}
		err := Validate(out)
		if !errors.Is(err, types.ErrStructuredOutput) {
			t.Fatalf("Validate = %v, want ErrStructuredOutput", err)
		}
		if !strings.Contains(err.Error(), "Quantity") {
			t.Fatalf("error %q does not name the out-of-bounds field", err)
		}
		vr := NewValidateRepair(2)
		d := vr.Decide(out, err, 1, repairInstruction)
		if !d.Retry {
			t.Fatal("out-of-bounds value should yield a repair turn")
		}
		// The repaired value after the next model turn is runtime work
		// (row 22.4); only the decision is asserted here.
	})

	t.Run("structured-output.refusal-as-json", func(t *testing.T) {
		var out struct {
			Answer string
		}
		if err := json.Unmarshal([]byte(`{"Answer": "I cannot assist with that"}`), &out); err != nil {
			t.Fatal(err)
		}
		class, refused := Classify(out)
		if !refused || class != types.ClassContentPolicy {
			t.Fatalf("Classify = (%v, %v), want (ClassContentPolicy, true)", class, refused)
		}
		vr := NewValidateRepair(2)
		d := vr.Decide(out, nil, 1, repairInstruction)
		if d.Retry {
			t.Fatal("a refusal must not consume a repair turn")
		}
		if d.Class != types.ClassContentPolicy {
			t.Fatalf("decision class = %v, want ClassContentPolicy", d.Class)
		}
		if d.Err == nil || !errors.Is(d.Err, types.ErrStructuredOutput) {
			t.Fatalf("decision error = %v, want ErrStructuredOutput", d.Err)
		}
		// gohan.model.refusal_as_json increments in the metric layer
		// (row 28); no metric API exists here.
	})

	t.Run("inside_bounds_passes", func(t *testing.T) {
		out := repairOrder{Quantity: 3, Note: "rush"}
		if err := Validate(out); err != nil {
			t.Fatalf("Validate = %v, want nil", err)
		}
		if err := Validate(&out); err != nil {
			t.Fatalf("Validate(pointer) = %v, want nil", err)
		}
	})

	t.Run("outside_bounds_rejected_with_repair", func(t *testing.T) {
		out := repairOrder{Quantity: 11}
		err := Validate(out)
		if !errors.Is(err, types.ErrStructuredOutput) {
			t.Fatalf("Validate = %v, want ErrStructuredOutput", err)
		}
		d := NewValidateRepair(2).Decide(out, err, 1, repairInstruction)
		if !d.Retry {
			t.Fatal("expected a repair decision")
		}
		text := d.ToolResult.Content[0].(types.Text)
		if !strings.Contains(text.Text, "above maximum") {
			t.Fatalf("repair text %q does not carry the bound", text.Text)
		}
	})

	t.Run("allowance_exhausted_surfaces", func(t *testing.T) {
		out := repairOrder{Quantity: 0}
		err := Validate(out)
		vr := NewValidateRepair(1)
		d1 := vr.Decide(out, err, 1, repairInstruction)
		if d1.Retry {
			t.Fatal("Max=1 means no repair turns")
		}
		if d1.Err == nil || !errors.Is(d1.Err, types.ErrStructuredOutput) {
			t.Fatalf("exhausted error = %v, want ErrStructuredOutput", d1.Err)
		}
	})
}
