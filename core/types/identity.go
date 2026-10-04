package types

// Principal identifies the caller. It never carries secrets; credentials
// travel only in context (see Credential).
type Principal struct {
	Subject string
	Tenant  string
	Scopes  []string
}

// SessionOwner records which principal opened a session.
type SessionOwner struct {
	Tenant  string
	Subject string
}

// CostTags attribute run cost.
type CostTags struct {
	Feature     string
	Environment string
	CostCenter  string
}

// LatencyClass is declared here because RunInfo carries it; the model
// capability owns its admission semantics.
type LatencyClass int

const (
	Interactive LatencyClass = iota
	Agentic
	Batch
)

// RunMode distinguishes a primary run from a shadow run; the release
// capability owns the shadow semantics.
type RunMode int

const (
	Primary RunMode = iota
	Shadow
)

// RunInfo describes the run a context value belongs to. RootRunID keys
// budgets and recovery for the whole tree; ParentRunID is set only on
// sub-flows.
type RunInfo struct {
	Flow         string
	SessionID    string
	RunID        string
	RootRunID    string
	ParentRunID  string
	Turn         int
	Principal    Principal
	LatencyClass LatencyClass
	CostTags     CostTags
	Residency    string
	ReleaseID    string
	Variant      string
	Mode         RunMode
}
