package stores

import (
	"fmt"
	"slices"

	"github.com/victorzhuk/gohan/core/types"
)

// BlobCaps is the profile's blob policy. It stays a parameter here until
// Caps (model) lands in core/types.
type BlobCaps struct {
	MaxBytes      int64
	MaxPerRequest int
	MaxPixels     int
	Formats       []string
}

// URLForwardable reports whether a block's URL may be passed to a provider
// as a URL. Only user- and system-authored blocks qualify; any other origin
// means the harness fetches the URL itself and stores a blob.
func URLForwardable(o types.Origin) bool {
	return o.Kind == types.OriginUser || o.Kind == types.OriginSystem
}

// CheckBlobs rejects a request whose blob blocks violate the profile caps:
// a block over MaxBytes or outside Formats fails with ErrBlobTooLarge, as
// does a request carrying more blob blocks than MaxPerRequest allows.
// ToolResult and Document content is walked. Blocks between InlineBlobBytes
// and MaxBytes pass here: they are spilled to the OutputStore, not rejected.
func CheckBlobs(blocks []types.Block, caps BlobCaps) error {
	var count int
	if err := checkBlobBlocks(blocks, caps, &count); err != nil {
		return err
	}
	if caps.MaxPerRequest > 0 && count > caps.MaxPerRequest {
		return fmt.Errorf("%w: %d blob blocks exceed MaxPerRequest %d", types.ErrBlobTooLarge, count, caps.MaxPerRequest)
	}
	return nil
}

func checkBlobBlocks(blocks []types.Block, caps BlobCaps, count *int) error {
	for _, b := range blocks {
		switch blk := b.(type) {
		case types.Image:
			*count++
			if err := checkBlobData(blk.MIME, blk.Data, caps); err != nil {
				return err
			}
		case types.Audio:
			*count++
			if err := checkBlobData(blk.MIME, blk.Data, caps); err != nil {
				return err
			}
		case types.File:
			*count++
			if err := checkBlobData(blk.MIME, blk.Data, caps); err != nil {
				return err
			}
		case types.ToolResult:
			if err := checkBlobBlocks(blk.Content, caps, count); err != nil {
				return err
			}
		case types.Document:
			if err := checkBlobBlocks(blk.Content, caps, count); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkBlobData(mime string, data []byte, caps BlobCaps) error {
	if int64(len(data)) > caps.MaxBytes {
		return fmt.Errorf("%w: %d bytes exceed MaxBytes %d", types.ErrBlobTooLarge, len(data), caps.MaxBytes)
	}
	if len(caps.Formats) > 0 && !slices.Contains(caps.Formats, mime) {
		return fmt.Errorf("%w: format %q is not in the profile formats", types.ErrBlobTooLarge, mime)
	}
	return nil
}
