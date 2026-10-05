package chains

import (
	"reflect"
	"testing"
)

func TestExplanation(t *testing.T) {
	set := PromptSet{
		FenceOpen:           "<data>",
		FenceClose:          "</data>",
		DataNotInstructions: "data only",
		Version:             "1",
	}
	info := StepInfo{Name: "journal", Kind: KindJournal, Applies: []string{"effect"}}
	ex := Explanation{
		Flow:    "booking",
		Profile: "fast",
		Steps:   []StepInfo{info},
		Prompts: PromptFields(set),
		Release: "r1",
	}
	if ex.Steps[0].Kind != KindJournal {
		t.Fatalf("StepInfo.Kind = %v, want KindJournal", ex.Steps[0].Kind)
	}
	if ex.Prompts["DataNotInstructions"] != set.DataNotInstructions {
		t.Fatal("Explanation.Prompts must key by PromptSet field name")
	}
	n := reflect.TypeOf(set).NumField()
	if len(ex.Prompts) != n {
		t.Fatalf("PromptFields covered %d of %d fields", len(ex.Prompts), n)
	}
	for name, val := range ex.Prompts {
		if got := reflect.ValueOf(set).FieldByName(name).String(); got != val {
			t.Errorf("PromptFields[%s] = %q, want %q", name, val, got)
		}
	}
}
