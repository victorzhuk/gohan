package gohan

import (
	"context"
	"errors"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// blobAssembly carries what one native assembly needs to resolve blob
// blocks: the target profile name, the session ledger, the content store
// and the adapter's uploader when the adapter has one.
type blobAssembly struct {
	profile string
	ledger  stores.SessionBlobs
	outputs stores.OutputStore
	upload  types.BlobUploader
}

func newBlobAssembly(profile string, model types.Model, st stores.Stores) blobAssembly {
	ba := blobAssembly{profile: profile, outputs: st.OutputStore}
	if log, ok := st.SessionLog.(stores.SessionBlobs); ok {
		ba.ledger = log
	}
	ba.upload, _ = model.(types.BlobUploader)
	return ba
}

// resolve stamps or materialises every blob block before the request is
// sent: a ledger hit for the target profile reuses the issued id with the
// bytes left unloaded, a miss uploads once and records the row, and an
// adapter without the port loads the bytes back from the content store.
func (ba blobAssembly) resolve(ctx context.Context, req types.ModelRequest) (types.ModelRequest, error) {
	run, _ := types.RunInfoFrom(ctx)
	var err error
	for i := range req.System {
		req.System[i], err = ba.block(ctx, run, req.System[i])
		if err != nil {
			return types.ModelRequest{}, err
		}
	}
	req.Messages = append([]types.Message(nil), req.Messages...)
	for i := range req.Messages {
		req.Messages[i].Blocks = append([]types.Block(nil), req.Messages[i].Blocks...)
		for j := range req.Messages[i].Blocks {
			req.Messages[i].Blocks[j], err = ba.block(ctx, run, req.Messages[i].Blocks[j])
			if err != nil {
				return types.ModelRequest{}, err
			}
		}
	}
	return req, nil
}

func (ba blobAssembly) block(ctx context.Context, run types.RunInfo, b types.Block) (types.Block, error) {
	blob, mime, ok := blobOf(b)
	if !ok || blob.Ref == "" {
		return b, nil
	}
	if blob.Provider == ba.profile && blob.ProviderID != "" {
		return b, nil
	}
	if ba.ledger != nil && run.SessionID != "" {
		if provider, id, ok := ba.ledger.BlobProviderID(ctx, run.SessionID, blob.Ref); ok && provider == ba.profile && id != "" {
			return stampBlob(b, provider, id), nil
		}
	}
	if ba.upload != nil {
		id, err := ba.upload.Upload(ctx, blob, mime)
		if err != nil {
			return nil, fmt.Errorf("upload blob %s: %w", blob.Ref, err)
		}
		if ba.ledger != nil && run.SessionID != "" {
			if err := ba.ledger.SetBlobProviderID(ctx, run.SessionID, blob.Ref, ba.profile, id); err != nil {
				return nil, fmt.Errorf("record blob %s: %w", blob.Ref, err)
			}
		}
		return stampBlob(b, ba.profile, id), nil
	}
	data, err := ba.outputs.Get(ctx, blob.Ref)
	if err != nil {
		if errors.Is(err, stores.ErrOutputNotFound) {
			return nil, fmt.Errorf("%w: blob %s cannot be rematerialised", types.ErrInputInvalid, blob.Ref)
		}
		return nil, fmt.Errorf("load blob %s: %w", blob.Ref, err)
	}
	return withBlobData(b, data), nil
}

func blobOf(b types.Block) (types.Blob, string, bool) {
	switch bl := b.(type) {
	case types.Image:
		return bl.Blob, bl.MIME, true
	case types.Audio:
		return bl.Blob, bl.MIME, true
	case types.File:
		return bl.Blob, bl.MIME, true
	}
	return types.Blob{}, "", false
}

func stampBlob(b types.Block, provider, id string) types.Block {
	switch bl := b.(type) {
	case types.Image:
		bl.Blob.Provider, bl.Blob.ProviderID = provider, id
		return bl
	case types.Audio:
		bl.Blob.Provider, bl.Blob.ProviderID = provider, id
		return bl
	case types.File:
		bl.Blob.Provider, bl.Blob.ProviderID = provider, id
		return bl
	}
	return b
}

func withBlobData(b types.Block, data []byte) types.Block {
	switch bl := b.(type) {
	case types.Image:
		bl.Data = data
		return bl
	case types.Audio:
		bl.Data = data
		return bl
	case types.File:
		bl.Data = data
		return bl
	}
	return b
}
