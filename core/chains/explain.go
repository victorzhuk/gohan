package chains

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

// Explanation describes a resolved flow: its chains step by step, every
// prompt string in place, and the release it was built from.
type Explanation struct {
	Flow    string
	Profile string
	Steps   []StepInfo
	Prompts map[string]string
	Skills  map[string]string
	Release string
}

// StepInfo is one resolved step as Explain reports it. Applies names the
// spec properties the step's Applies predicate declares.
type StepInfo struct {
	Name    string
	Kind    StepKind
	Applies []string
}
