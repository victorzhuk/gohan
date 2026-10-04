// Package std holds the default policies and helpers the driver wires in:
// every default, matcher, preset or policy value lives here, not in core.
package std

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

// manifestVersion is the pinned-file schema version. Any change to the
// hash algorithm or the file shape bumps it.
const manifestVersion = 1

// ToolManifestHash pins a tool's identity: SHA-256 over the name,
// description, schema, effect and required scopes, joined with newlines.
// The algorithm and field set are frozen here; a change is a manifest
// version bump and a re-review of every pinned set, like a lockfile bump.
func ToolManifestHash(spec types.ToolSpec) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		spec.Name,
		spec.Description,
		string(spec.Schema),
		fmt.Sprint(int(spec.Effect)),
		strings.Join(spec.RequiredScopes, ","),
	}, "\n")))
	return hex.EncodeToString(h[:])
}

// PinnedTool is one entry of a pinned manifest.
type PinnedTool struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// PinnedManifest is the pinned-file shape. MarshalManifest writes it;
// ParseManifest reads it back. The shape is frozen at version 1.
type PinnedManifest struct {
	Version int          `json:"version"`
	Tools   []PinnedTool `json:"tools"`
}

// ManifestOf hashes every spec into a name → hash map.
func ManifestOf(specs []types.ToolSpec) map[string]string {
	m := make(map[string]string, len(specs))
	for _, s := range specs {
		m[s.Name] = ToolManifestHash(s)
	}
	return m
}

// MarshalManifest renders the pinned file for a tool set, entries sorted
// by name for byte-stable output.
func MarshalManifest(specs []types.ToolSpec) ([]byte, error) {
	names := make([]string, 0, len(specs))
	byName := ManifestOf(specs)
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	pm := PinnedManifest{Version: manifestVersion, Tools: make([]PinnedTool, 0, len(names))}
	for _, name := range names {
		pm.Tools = append(pm.Tools, PinnedTool{Name: name, Hash: byName[name]})
	}
	return json.Marshal(pm)
}

// ParseManifest reads a pinned file back into a name → hash map. A
// version other than the frozen one is rejected.
func ParseManifest(data []byte) (map[string]string, error) {
	var pm PinnedManifest
	if err := json.Unmarshal(data, &pm); err != nil {
		return nil, fmt.Errorf("parse pinned manifest: %w", err)
	}
	if pm.Version != manifestVersion {
		return nil, fmt.Errorf("pinned manifest version %d, want %d", pm.Version, manifestVersion)
	}
	m := make(map[string]string, len(pm.Tools))
	for _, t := range pm.Tools {
		m[t.Name] = t.Hash
	}
	return m, nil
}

// CheckPinnedManifest fails with ErrManifestDrift on the first pinned
// tool whose hash changed or that is no longer registered: a changed
// upstream spec must be re-reviewed before the build proceeds. Unpinned
// current tools are the rename rule's territory, not the hash check's.
func CheckPinnedManifest(pinned, current map[string]string) error {
	names := make([]string, 0, len(pinned))
	for name := range pinned {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h, ok := current[name]
		if !ok || h != pinned[name] {
			return types.ErrManifestDrift{Tool: name}
		}
	}
	return nil
}
