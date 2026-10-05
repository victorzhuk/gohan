package chains

import (
	"reflect"

	"github.com/victorzhuk/gohan/core/types"
)

// PromptSet collects every string gohan itself can place in a model
// request. Core declares no default; a service picks one and Build records
// its hash and Version.
type PromptSet struct {
	FenceOpen, FenceClose string
	DataNotInstructions   string
	OutcomeUnknown        string
	ReadBackHint          string
	OutputRefHint         string
	RepairInstruction     string
	NotesPreamble         string
	OperatorTurn          string
	Version               string
}

// PromptFields maps every PromptSet field name to its value. Release
// hashing and Explain derive their accounting from this one enumeration,
// so a new field is covered without a hand-maintained list.
func PromptFields(set PromptSet) map[string]string {
	v := reflect.ValueOf(set)
	t := v.Type()
	out := make(map[string]string, t.NumField())
	for i := range t.NumField() {
		out[t.Field(i).Name] = v.Field(i).String()
	}
	return out
}

// Explanation describes a resolved flow: its chains step by step, every
// prompt string in place, and the release it was built from.
type Explanation struct {
	Flow        string
	Profile     string
	Steps       []StepInfo
	ToolSteps   []StepInfo
	Strategies  StrategyInfo
	Fallback    string
	Limits      types.RunLimits
	Granularity string
	Sample      types.ModelRequest
	Prompts     map[string]string
	Skills      map[string]string
	Release     string
}

// StrategyInfo is the resolved strategy plan as Explain reports it.
// Structured carries the strategy name the resolver picked.
type StrategyInfo struct {
	Structured       string
	ParallelTools    bool
	MaxParallelTools int
}

// StepInfo is one resolved step as Explain reports it. Applies names the
// registered tools the step's Applies predicate admits; a nil predicate
// applies to every registered tool.
type StepInfo struct {
	Name    string
	Kind    StepKind
	Applies []string
}
