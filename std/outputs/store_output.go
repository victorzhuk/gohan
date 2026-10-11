package outputs

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// DefaultMaxOutput is the per-stream inline cap (tools contract default):
// bytes beyond it are dropped from the inline result, and content larger
// than the cap is stored whole with a retrievable ref.
const DefaultMaxOutput = 64 * 1024

// SanitizeFunc rewrites one stream's bytes before they are capped or
// stored. It returns the clean bytes and the number of occurrences it
// replaced; it emits nothing itself.
type SanitizeFunc func(ctx context.Context, stream string, data []byte) (clean []byte, leaks int, err error)

// OutputStoreParams carries one stream through the shared cap-and-store
// step: sanitize, resolve the effective cap, conditionally store, render
// the excerpt and marker, then count.
type OutputStoreParams struct {
	Store      stores.OutputStore
	Run        types.RunInfo
	Tool       string
	Max        int
	Stream     string
	Data       []byte
	ForceStore bool
	Sanitize   SanitizeFunc
	Tel        types.Telemetry
}

// StoreOutput renders one stream into model blocks and returns the blocks,
// the ref of the stored document ("" when nothing was stored) and an error.
//
// Sanitize runs first, so no raw byte reaches the store or the excerpt.
// Max at or below zero resolves to DefaultMaxOutput; a positive Max is
// unchanged. The sanitized bytes go to the store when ForceStore is set or
// they exceed the effective cap; a Put error returns unchanged with no
// blocks and no ref. A successful Put emits one gohan.output.stored count
// named by tool (no count when Tel is nil). Under the cap the text block is
// the bytes unchanged; over the cap it is the head excerpt plus a marker
// naming the cap, the dropped bytes and either the stored ref or the
// missing store. The leak count Sanitize returns is emitted once through
// the context telemetry as gohan.sandbox.secret_leak; a nil Sanitize
// changes no bytes and leaks nothing.
func StoreOutput(ctx context.Context, s OutputStoreParams) ([]types.Block, string, error) {
	max := s.Max
	if max <= 0 {
		max = DefaultMaxOutput
	}
	data := s.Data
	if s.Sanitize != nil {
		clean, leaks, err := s.Sanitize(ctx, s.Stream, data)
		if err != nil {
			return nil, "", err
		}
		data = clean
		if leaks > 0 {
			if tel := types.TelemetryFrom(ctx); tel != nil {
				tel.Count(ctx, "gohan.sandbox.secret_leak", int64(leaks))
			}
		}
	}
	ref := ""
	if s.Store != nil && (s.ForceStore || len(data) > max) {
		var err error
		ref, err = s.Store.Put(ctx, s.Run, []types.Block{types.Text{Text: string(data)}})
		if err != nil {
			return nil, "", err
		}
		if s.Tel != nil {
			s.Tel.Count(ctx, "gohan.output.stored", 1, types.String(types.KeyToolName, s.Tool))
		}
	}
	if len(data) <= max {
		return []types.Block{types.Text{Text: string(data)}}, ref, nil
	}
	dropped := len(data) - max
	var marker string
	if ref != "" {
		marker = fmt.Sprintf("\n[%s truncated at %d bytes, %d bytes dropped; full output ref: %s]",
			s.Stream, max, dropped, ref)
	} else {
		marker = fmt.Sprintf("\n[%s truncated at %d bytes, %d bytes dropped; no output store configured]",
			s.Stream, max, dropped)
	}
	return []types.Block{types.Text{Text: string(data[:max]) + marker}}, ref, nil
}
