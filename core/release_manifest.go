package gohan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// ReleaseManifest is the release identity Build computes: a hash per
// section over what the build pinned. The Skills, Chains and Definitions
// sections have no source in M0 — skills and flowdef definitions are
// later milestones — so they are present but empty, and everything that
// exists today lands in Models, Prompts and Tools.
type ReleaseManifest struct {
	Models      map[string]string
	Prompts     map[string]string
	Tools       map[string]string
	Skills      map[string]string
	Chains      map[string]string
	Definitions map[string]string
}

// ID returns the release identity: SHA-256 over the canonical JSON of the
// manifest. Map keys sort, so the same inputs always yield the same ID.
func (m ReleaseManifest) ID() string {
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// computeReleaseManifest hashes what M0 can pin: the configured profile
// versions, every PromptSet string, and the reviewed tool hashes from the
// pinned manifest when one was supplied. The absent sections stay empty
// and named.
func computeReleaseManifest(profiles map[string]types.ModelProfile, prompts chains.PromptSet, pinned *types.PinnedManifest) ReleaseManifest {
	m := ReleaseManifest{
		Models:      map[string]string{},
		Prompts:     map[string]string{},
		Tools:       map[string]string{},
		Skills:      map[string]string{},
		Chains:      map[string]string{},
		Definitions: map[string]string{},
	}
	for name, p := range profiles {
		m.Models[name] = p.Version
	}
	for name, val := range chains.PromptFields(prompts) {
		m.Prompts[name] = hashString(val)
	}
	if pinned != nil {
		for _, t := range pinned.Tools {
			m.Tools[t.Name] = t.Hash
		}
	}
	return m
}

// manifestID combines the resolved matrix and the manifest into the
// release identity Build reports: a change in either changes the ID.
func manifestID(m ReleaseManifest, entries []matrixEntry) string {
	sorted := make([]matrixEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Flow < sorted[j].Flow })
	b, err := json.Marshal(struct {
		Manifest ReleaseManifest
		Matrix   []matrixEntry
	}{m, sorted})
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
