package evals

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
)

func checkManifest() gohan.ReleaseManifest {
	return gohan.ReleaseManifest{
		Models:      map[string]string{"assist-main": "v2"},
		Prompts:     map[string]string{"greet": "h1"},
		Tools:       map[string]string{"search": "t1"},
		Skills:      map[string]string{},
		Chains:      map[string]string{},
		Definitions: map[string]string{},
	}
}

func checkFullEvidence() ([]DependencyEvidence, []CompatibilityEvidence) {
	deps := []DependencyEvidence{
		{Section: SectionModel, Name: "assist-main", Version: "v2", Ref: "dep-model"},
		{Section: SectionPrompt, Name: "greet", Version: "h1", Ref: "dep-prompt"},
		{Section: SectionTool, Name: "search", Version: "t1", Ref: "dep-tool"},
	}
	compat := []CompatibilityEvidence{
		{Surface: "model:assist-main", Policy: "p1", ReleaseID: "r1", Ref: "compat-model"},
		{Surface: "prompt:greet", Policy: "p1", ReleaseID: "r1", Ref: "compat-prompt"},
		{Surface: "tool:search", Policy: "p1", ReleaseID: "r1", Ref: "compat-tool"},
	}
	return deps, compat
}

func checkSuite(m gohan.ReleaseManifest, deps []DependencyEvidence, compat []CompatibilityEvidence) ReleaseChecks {
	return ReleaseChecks{Manifest: m, ReleaseID: "r1", Dependencies: deps, Compatibility: compat}
}

func checkFind(t *testing.T, d Decision, name string) CheckResult {
	t.Helper()
	for _, c := range d.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not found in decision", name)
	return CheckResult{}
}

func TestReleaseChecks(t *testing.T) {
	ctx := context.Background()

	t.Run("release.check-manifest-driven", func(t *testing.T) {
		m := checkManifest()
		deps, compat := checkFullEvidence()
		suite := checkSuite(m, deps, compat)
		suite.ReleaseID = ""
		suite.Compatibility = slices.Clone(compat)
		for i := range suite.Compatibility {
			suite.Compatibility[i].ReleaseID = m.ID()
		}
		d, err := suite.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(d.Checks) != 6 {
			t.Fatalf("checks = %d, want 6", len(d.Checks))
		}
		var want []string
		for _, section := range []struct {
			name    string
			entries map[string]string
		}{
			{SectionModel, m.Models},
			{SectionPrompt, m.Prompts},
			{SectionTool, m.Tools},
		} {
			for name := range section.entries {
				want = append(want,
					"dependency:"+section.name+":"+name,
					"compatibility:"+section.name+":"+name,
				)
			}
		}
		slices.Sort(want)
		got := make([]string, len(d.Checks))
		for i, c := range d.Checks {
			got[i] = c.Name
		}
		if !slices.Equal(got, want) {
			t.Fatalf("check names = %v, want %v", got, want)
		}
		if d.Verdict != VerdictApprove {
			t.Fatalf("verdict = %q, want approve", d.Verdict)
		}
		if !slices.IsSorted(got) {
			t.Fatalf("checks not sorted by name: %v", got)
		}
		if d.ReleaseID != m.ID() {
			t.Fatalf("release id = %q, want manifest id", d.ReleaseID)
		}
	})

	t.Run("release.check-dependency-evidence", func(t *testing.T) {
		m := checkManifest()
		deps, compat := checkFullEvidence()
		deps = slices.DeleteFunc(deps, func(d DependencyEvidence) bool {
			return d.Name == "assist-main"
		})
		for i := range deps {
			if deps[i].Name == "greet" {
				deps[i].Version = "h2"
			}
		}
		d, err := checkSuite(m, deps, compat).Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if d.Verdict != VerdictDeny {
			t.Fatalf("verdict = %q, want deny", d.Verdict)
		}
		missing := checkFind(t, d, "dependency:model:assist-main")
		if missing.Status != CheckFail {
			t.Fatalf("missing status = %q, want fail", missing.Status)
		}
		if len(missing.Reasons) == 0 || !strings.Contains(missing.Reasons[0], "assist-main") ||
			!strings.Contains(missing.Reasons[0], "no pinned-version evidence record") {
			t.Fatalf("missing reasons = %v, want entry name and no-record reason", missing.Reasons)
		}
		mismatch := checkFind(t, d, "dependency:prompt:greet")
		if mismatch.Status != CheckFail {
			t.Fatalf("mismatch status = %q, want fail", mismatch.Status)
		}
		if len(mismatch.Reasons) == 0 || !strings.Contains(mismatch.Reasons[0], "h2") ||
			!strings.Contains(mismatch.Reasons[0], "h1") {
			t.Fatalf("mismatch reasons = %v, want both versions", mismatch.Reasons)
		}
		if c := checkFind(t, d, "dependency:tool:search"); c.Status != CheckPass {
			t.Fatalf("evidenced status = %q, want pass", c.Status)
		}
		if c := checkFind(t, d, "compatibility:model:assist-main"); c.Status != CheckPass {
			t.Fatalf("compat evidenced status = %q, want pass", c.Status)
		}
	})

	t.Run("release.check-compat-evidence", func(t *testing.T) {
		m := checkManifest()
		deps, compat := checkFullEvidence()

		noRecord := checkSuite(m, deps, slices.DeleteFunc(slices.Clone(compat), func(c CompatibilityEvidence) bool {
			return c.Surface == "model:assist-main"
		}))
		d, err := noRecord.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		c := checkFind(t, d, "compatibility:model:assist-main")
		if c.Status != CheckFail {
			t.Fatalf("no-record status = %q, want fail", c.Status)
		}
		if len(c.Reasons) == 0 || !strings.Contains(c.Reasons[0], "model:assist-main") ||
			!strings.Contains(c.Reasons[0], "no compatibility policy record for release r1") {
			t.Fatalf("no-record reasons = %v, want surface and release", c.Reasons)
		}
		if d.Verdict != VerdictDeny {
			t.Fatalf("no-record verdict = %q, want deny", d.Verdict)
		}

		otherRelease := checkSuite(m, deps, slices.Clone(compat))
		for i := range otherRelease.Compatibility {
			if otherRelease.Compatibility[i].Surface == "model:assist-main" {
				otherRelease.Compatibility[i].ReleaseID = "other"
			}
		}
		d, err = otherRelease.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		c = checkFind(t, d, "compatibility:model:assist-main")
		if c.Status != CheckFail {
			t.Fatalf("wrong-release status = %q, want fail", c.Status)
		}
		if len(c.Reasons) == 0 || !strings.Contains(c.Reasons[0], "other") ||
			!strings.Contains(c.Reasons[0], "r1") {
			t.Fatalf("wrong-release reasons = %v, want both releases", c.Reasons)
		}
		if d.Verdict != VerdictDeny {
			t.Fatalf("wrong-release verdict = %q, want deny", d.Verdict)
		}
	})

	t.Run("release.check-fail-closed", func(t *testing.T) {
		m := checkManifest()
		deps, compat := checkFullEvidence()
		broken := checkSuite(m, deps, compat)
		broken.Dependencies = slices.DeleteFunc(slices.Clone(deps), func(d DependencyEvidence) bool {
			return d.Name == "greet" || d.Name == "search"
		})
		broken.Dependencies = append(broken.Dependencies, DependencyEvidence{Section: SectionTool, Name: "search"})
		d, err := broken.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if d.Verdict != VerdictDeny {
			t.Fatalf("verdict = %q, want deny", d.Verdict)
		}
		want := []string{"dependency:prompt:greet", "dependency:tool:search"}
		slices.Sort(want)
		if got := d.Denials(); !slices.Equal(got, want) {
			t.Fatalf("denials = %v, want %v", got, want)
		}
		for _, name := range d.Denials() {
			c := checkFind(t, d, name)
			if len(c.Reasons) == 0 {
				t.Fatalf("denied check %q has no reasons", name)
			}
		}

		good, err := checkSuite(m, deps, compat).Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if good.Verdict != VerdictApprove {
			t.Fatalf("control verdict = %q, want approve", good.Verdict)
		}
		if got := good.Denials(); len(got) != 0 {
			t.Fatalf("control denials = %v, want empty", got)
		}
	})

	t.Run("release.decision-durable", func(t *testing.T) {
		m := checkManifest()
		deps, compat := checkFullEvidence()
		rc := ReleaseChecks{
			Manifest:          m,
			ReleaseID:         "r1",
			Variant:           "canary",
			Dependencies:      deps,
			Compatibility:     compat,
			ReportRef:         "shadow-report-1",
			DatasetHashes:     []string{"dh1"},
			JudgeFingerprints: []string{"jf1"},
		}
		d1, err := rc.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		b, err := json.Marshal(d1)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var d2 Decision
		if err := json.Unmarshal(b, &d2); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		d3, err := rc.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if d1.Identity() != d2.Identity() || d2.Identity() != d3.Identity() {
			t.Fatalf("identities differ: d1 %q d2 %q d3 %q", d1.Identity(), d2.Identity(), d3.Identity())
		}
		for _, pair := range [][2]Decision{{d1, d2}, {d1, d3}} {
			if !reflect.DeepEqual(pair[0], pair[1]) {
				t.Fatal("round-tripped decision differs from original")
			}
		}
		for _, d := range []Decision{d1, d2, d3} {
			if d.ReleaseID != "r1" || d.Variant != "canary" || d.ReportRef != "shadow-report-1" ||
				!slices.Equal(d.DatasetHashes, []string{"dh1"}) ||
				!slices.Equal(d.JudgeFingerprints, []string{"jf1"}) {
				t.Fatalf("decision attributes changed: %+v", d)
			}
		}

		rc.Compatibility = slices.DeleteFunc(slices.Clone(compat), func(c CompatibilityEvidence) bool {
			return c.Surface == "tool:search"
		})
		d4, err := rc.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if d4.Identity() == d1.Identity() {
			t.Fatal("dropped evidence did not change identity")
		}
	})
}
