package evals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
)

// Manifest sections the check suite enumerates, in the fixed emission
// order. A declared surface is one manifest entry: "<section>:<name>".
const (
	SectionModel      = "model"
	SectionPrompt     = "prompt"
	SectionTool       = "tool"
	SectionSkill      = "skill"
	SectionChain      = "chain"
	SectionDefinition = "definition"
)

// Evidence kinds check results reference.
const (
	EvidenceDependency    = "dependency_evidence"
	EvidenceCompatibility = "compatibility_evidence"
)

// DependencyEvidence is one declared dependency's pinned-version record.
type DependencyEvidence struct {
	Section string `json:"section"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Ref     string `json:"ref,omitempty"`
}

// CompatibilityEvidence is one compatibility policy record for a
// declared surface under a release.
type CompatibilityEvidence struct {
	Surface   string `json:"surface"`
	Policy    string `json:"policy"`
	ReleaseID string `json:"release_id"`
	Ref       string `json:"ref,omitempty"`
}

// CheckStatus is one check's settled outcome. Unscored means the check
// could not run (incomplete record); it denies like a failure does.
type CheckStatus string

const (
	CheckPass     CheckStatus = "pass"
	CheckFail     CheckStatus = "fail"
	CheckUnscored CheckStatus = "unscored"
)

// CheckResult is one manifest entry's outcome for one check family.
type CheckResult struct {
	Name     string              `json:"name"`
	Status   CheckStatus         `json:"status"`
	Reasons  []string            `json:"reasons,omitempty"`
	Evidence []types.EvidenceRef `json:"evidence,omitempty"`
}

// Verdict is the release decision's outcome. Fail-closed: only every
// check passing approves.
type Verdict string

const (
	VerdictApprove Verdict = "approve"
	VerdictDeny    Verdict = "deny"
)

// ErrNoManifest refuses a check run over a manifest that declares
// nothing: an empty manifest is a build error, not an approved release.
var ErrNoManifest = errors.New("gohan/evals: release checks need a manifest with at least one declared entry")

// ReleaseChecks is the manifest-driven suite input.
type ReleaseChecks struct {
	Manifest          gohan.ReleaseManifest
	ReleaseID         string
	Variant           string
	Dependencies      []DependencyEvidence
	Compatibility     []CompatibilityEvidence
	ReportRef         string
	DatasetHashes     []string
	JudgeFingerprints []string
}

// Decision is the durable release decision: verdict, every check result
// with reasons, and the release attributes and report reference. It
// carries no clock, so re-emitting the same decision preserves its
// identity.
type Decision struct {
	ReleaseID         string        `json:"release_id"`
	Variant           string        `json:"variant,omitempty"`
	Verdict           Verdict       `json:"verdict"`
	Checks            []CheckResult `json:"checks"`
	ReportRef         string        `json:"report_ref,omitempty"`
	DatasetHashes     []string      `json:"dataset_hashes,omitempty"`
	JudgeFingerprints []string      `json:"judge_fingerprints,omitempty"`
}

// Denials names every check that did not pass, in check order.
func (d Decision) Denials() []string {
	var denied []string
	for _, c := range d.Checks {
		if c.Status != CheckPass {
			denied = append(denied, c.Name)
		}
	}
	return denied
}

// Identity returns the decision's deterministic identity: SHA-256 over
// the canonical JSON of the decision content.
func (d Decision) Identity() string {
	b, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Run enumerates the checks from the manifest: one dependency check and
// one compatibility check per declared entry, in section order, names
// sorted within each section. A missing evidence record fails naming
// the entry and the missing evidence; an incomplete record settles
// unscored; any non-pass denies. It performs no I/O and never recomputes
// Manifest.ID() beyond the ReleaseID default.
func (rc ReleaseChecks) Run(ctx context.Context) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, fmt.Errorf("release checks: %w", err)
	}
	sections := []struct {
		name    string
		entries map[string]string
	}{
		{SectionModel, rc.Manifest.Models},
		{SectionPrompt, rc.Manifest.Prompts},
		{SectionTool, rc.Manifest.Tools},
		{SectionSkill, rc.Manifest.Skills},
		{SectionChain, rc.Manifest.Chains},
		{SectionDefinition, rc.Manifest.Definitions},
	}
	declared := false
	for _, s := range sections {
		if len(s.entries) > 0 {
			declared = true
			break
		}
	}
	if !declared {
		return Decision{}, ErrNoManifest
	}
	releaseID := rc.ReleaseID
	if releaseID == "" {
		releaseID = rc.Manifest.ID()
	}
	var checks []CheckResult
	for _, s := range sections {
		names := make([]string, 0, len(s.entries))
		for name := range s.entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			checks = append(checks,
				rc.dependencyCheck(s.name, name, s.entries[name]),
				rc.compatibilityCheck(s.name, name, releaseID),
			)
		}
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	verdict := VerdictApprove
	for _, c := range checks {
		if c.Status != CheckPass {
			verdict = VerdictDeny
			break
		}
	}
	return Decision{
		ReleaseID:         releaseID,
		Variant:           rc.Variant,
		Verdict:           verdict,
		Checks:            checks,
		ReportRef:         rc.ReportRef,
		DatasetHashes:     rc.DatasetHashes,
		JudgeFingerprints: rc.JudgeFingerprints,
	}, nil
}

func (rc ReleaseChecks) dependencyCheck(section, name, manifestVersion string) CheckResult {
	check := CheckResult{Name: "dependency:" + section + ":" + name}
	var matched, conflicting bool
	var conflictVersion, evidenceRef string
	for _, d := range rc.Dependencies {
		if d.Section != section || d.Name != name {
			continue
		}
		if d.Version == manifestVersion {
			matched = true
			evidenceRef = d.Ref
			continue
		}
		if d.Version == "" {
			continue
		}
		if !conflicting {
			conflicting = true
			conflictVersion = d.Version
		}
	}
	if conflicting {
		check.Status = CheckFail
		check.Reasons = []string{fmt.Sprintf("evidence version %s does not match manifest %s for %s:%s", conflictVersion, manifestVersion, section, name)}
		return check
	}
	if !matched {
		if !rc.hasDependencyRecord(section, name) {
			check.Status = CheckFail
			check.Reasons = []string{fmt.Sprintf("dependency %s:%s has no pinned-version evidence record", section, name)}
			return check
		}
		check.Status = CheckUnscored
		check.Reasons = []string{fmt.Sprintf("dependency %s:%s: check cannot run, evidence record incomplete", section, name)}
		return check
	}
	check.Status = CheckPass
	if evidenceRef != "" {
		check.Evidence = []types.EvidenceRef{{Kind: EvidenceDependency, Ref: evidenceRef}}
	}
	return check
}

func (rc ReleaseChecks) hasDependencyRecord(section, name string) bool {
	for _, d := range rc.Dependencies {
		if d.Section == section && d.Name == name {
			return true
		}
	}
	return false
}

func (rc ReleaseChecks) compatibilityCheck(section, name, releaseID string) CheckResult {
	surface := section + ":" + name
	check := CheckResult{Name: "compatibility:" + surface}
	var any, covered, mismatched bool
	var otherRelease, policyRef string
	for _, c := range rc.Compatibility {
		if c.Surface != surface {
			continue
		}
		any = true
		if c.ReleaseID != releaseID {
			if !mismatched {
				mismatched = true
				otherRelease = c.ReleaseID
			}
			continue
		}
		if c.Policy != "" {
			covered = true
			policyRef = c.Ref
		}
	}
	if !any {
		check.Status = CheckFail
		check.Reasons = []string{fmt.Sprintf("surface %s has no compatibility policy record for release %s", surface, releaseID)}
		return check
	}
	if mismatched && !covered {
		check.Status = CheckFail
		check.Reasons = []string{fmt.Sprintf("compatibility record for %s covers release %s, not %s", surface, otherRelease, releaseID)}
		return check
	}
	if !covered {
		check.Status = CheckUnscored
		check.Reasons = []string{fmt.Sprintf("compatibility %s: check cannot run, policy record incomplete", surface)}
		return check
	}
	check.Status = CheckPass
	if policyRef != "" {
		check.Evidence = []types.EvidenceRef{{Kind: EvidenceCompatibility, Ref: policyRef}}
	}
	return check
}
