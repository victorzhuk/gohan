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
		Prompts: map[string]string{"DataNotInstructions": set.DataNotInstructions},
		Release: "r1",
	}
	if ex.Steps[0].Kind != KindJournal {
		t.Fatalf("StepInfo.Kind = %v, want KindJournal", ex.Steps[0].Kind)
	}
	if ex.Prompts["DataNotInstructions"] != set.DataNotInstructions {
		t.Fatal("Explanation.Prompts must key by PromptSet field name")
	}
	n := reflect.TypeOf(set).NumField()
	if len(setFields(set)) != n {
		t.Fatalf("setFields covered %d of %d fields", len(setFields(set)), n)
	}
}

func setFields(set PromptSet) map[string]string {
	v := reflect.ValueOf(set)
	typ := v.Type()
	out := make(map[string]string, typ.NumField())
	for i := range typ.NumField() {
		out[typ.Field(i).Name] = v.Field(i).String()
	}
	return out
}
