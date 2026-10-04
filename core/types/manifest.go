package types

// PinnedTool is one entry of a pinned manifest.
type PinnedTool struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// PinnedManifest is the pinned-file shape for a reviewed tool set. The
// driver checks it at build time, so the value type lives on the floor;
// the hash algorithm and file format stay in std.
type PinnedManifest struct {
	Version int          `json:"version"`
	Tools   []PinnedTool `json:"tools"`
}
