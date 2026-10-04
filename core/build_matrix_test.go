package gohan

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

func testProfile(name, version string, constrained bool) types.ModelProfile {
	return types.ModelProfile{
		Name:    name,
		Version: version,
		Caps: types.Caps{
			Tools:         true,
			ParallelTools: true,
			Constrained:   constrained,
		},
	}
}

func matrixStack(t *testing.T, buf *bytes.Buffer) (*Stack, []matrixEntry) {
	t.Helper()
	s, err := Build(WithLogger(slog.New(slog.NewTextHandler(buf, nil))))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	profiles := map[string]types.ModelProfile{
		"primary": testProfile("primary", "2026-10-01", true),
	}
	req := FlowRequest{Name: "assist", Structured: true}
	plan, err := s.ResolveStrategies(req, profiles, "primary")
	if err != nil {
		t.Fatalf("ResolveStrategies: %v", err)
	}
	entries := []matrixEntry{{
		Flow:             req.Name,
		Profile:          "primary",
		Structured:       entryName(plan.Structured),
		ParallelTools:    plan.ParallelTools,
		MaxParallelTools: plan.MaxParallelTools,
	}}
	return s, entries
}

func TestBuildResolvedMatrix(t *testing.T) {
	t.Run("one record per build", func(t *testing.T) {
		var buf bytes.Buffer
		_, err := Build(WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if got := strings.Count(buf.String(), "resolved strategy matrix"); got != 1 {
			t.Fatalf("want exactly one resolved-matrix record per build, got %d", got)
		}
	})

	t.Run("build.resolved-matrix", func(t *testing.T) {
		var buf bytes.Buffer
		_, entries := matrixStack(t, &buf)
		buf.Reset() // Build already logged its resolved-matrix record.
		logResolvedMatrix(slog.New(slog.NewTextHandler(&buf, nil)), entries)
		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		if len(lines) != 1 {
			t.Fatalf("want one record, got %d", len(lines))
		}
		for _, want := range []string{"assist", "primary", "constrained", "matrix"} {
			if !strings.Contains(lines[0], want) {
				t.Errorf("record %q lacks %q", lines[0], want)
			}
		}
	})

	t.Run("release.id-deterministic", func(t *testing.T) {
		prompts := chains.PromptSet{FenceOpen: "<fence>", Version: "v1"}
		pinned := &types.PinnedManifest{Version: 1, Tools: []types.PinnedTool{{Name: "search", Hash: "abc"}}}

		var bufA, bufB bytes.Buffer
		_, entriesA := matrixStack(t, &bufA)
		_, entriesB := matrixStack(t, &bufB)
		idA := manifestID(computeReleaseManifest(testProfiles(), prompts, pinned), entriesA)
		idB := manifestID(computeReleaseManifest(testProfiles(), prompts, pinned), entriesB)
		if idA == "" || idA != idB {
			t.Fatalf("identical builds must yield identical IDs: %q vs %q", idA, idB)
		}

		changed := prompts
		changed.FenceOpen = "<changed>"
		if got := manifestID(computeReleaseManifest(testProfiles(), changed, pinned), entriesA); got == idA {
			t.Error("changing one prompt string must change the ID")
		}

		otherPinned := &types.PinnedManifest{Version: 1, Tools: []types.PinnedTool{{Name: "search", Hash: "def"}}}
		if got := manifestID(computeReleaseManifest(testProfiles(), prompts, otherPinned), entriesA); got == idA {
			t.Error("a changed pinned tool hash must change the ID")
		}

		fbProfiles := testProfiles()
		p := fbProfiles["primary"]
		p.Version = "2026-11-01"
		fbProfiles["primary"] = p
		if got := manifestID(computeReleaseManifest(fbProfiles, prompts, pinned), entriesA); got == idA {
			t.Error("a changed profile version must change the ID")
		}

		var bufC bytes.Buffer
		_, entriesC := matrixStack(t, &bufC)
		entriesC[0].Structured = "none"
		if got := manifestID(computeReleaseManifest(testProfiles(), prompts, pinned), entriesC); got == idA {
			t.Error("a changed resolution must change the ID")
		}
	})

	t.Run("manifest names absent sections", func(t *testing.T) {
		m := computeReleaseManifest(testProfiles(), chains.PromptSet{Version: "v1"}, nil)
		for _, section := range []string{"Skills", "Chains", "Definitions"} {
			if m.Models == nil || m.Prompts == nil || m.Tools == nil {
				t.Fatalf("manifest sections must be non-nil")
			}
			switch section {
			case "Skills":
				if len(m.Skills) != 0 {
					t.Errorf("Skills must be empty in M0, got %v", m.Skills)
				}
			case "Chains":
				if len(m.Chains) != 0 {
					t.Errorf("Chains must be empty in M0, got %v", m.Chains)
				}
			case "Definitions":
				if len(m.Definitions) != 0 {
					t.Errorf("Definitions must be empty in M0, got %v", m.Definitions)
				}
			}
		}
		if len(m.Prompts) == 0 {
			t.Error("Prompts must carry the prompt set hashes")
		}
		if id := m.ID(); len(id) != 64 {
			t.Errorf("ID must be a SHA-256 hex string, got %q", id)
		}
	})
}

func testProfiles() map[string]types.ModelProfile {
	return map[string]types.ModelProfile{
		"primary": testProfile("primary", "2026-10-01", true),
	}
}
