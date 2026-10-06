package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// EraseReport names what a subject erasure removed and what it held back.
// BlobRefs lists the blob refs carried by the erased sessions' history and
// checkpoints, so a later blob erasure finds them without scanning run
// state; Memory lists the erased subject-memory entry ids.
type EraseReport struct {
	Sessions int
	Entries  int
	Outputs  int
	Held     []string
	BlobRefs []string
	Memory   []string
}

func (s *Stack) EraseSubject(ctx context.Context, tenant, subject string) (EraseReport, error) {
	report := EraseReport{}
	er, ok := s.stores.SessionLog.(stores.SessionEraser)
	if !ok {
		return report, errors.New("erase subject: session log does not support erasure")
	}
	blobRefs, err := s.collectBlobRefs(ctx, tenant, subject)
	if err != nil {
		return report, err
	}
	erased, err := er.EraseSessions(ctx, tenant, subject)
	if err != nil {
		return report, err
	}
	report.Sessions = len(erased.Deleted)
	report.Held = erased.Held
	report.BlobRefs = blobRefs

	var runIDs []string
	if re, ok := s.stores.Runs.(stores.RunEraser); ok {
		ids, err := re.EraseSessions(ctx, erased.Deleted)
		if err != nil {
			return report, fmt.Errorf("erase runs: %w", err)
		}
		runIDs = ids
	}
	if ee, ok := s.stores.EventLog.(stores.EventEraser); ok {
		if _, err := ee.EraseRuns(ctx, runIDs); err != nil {
			return report, fmt.Errorf("erase events: %w", err)
		}
	}
	if ce, ok := s.stores.Checkpoints.(stores.CheckpointEraser); ok {
		cps, err := ce.EraseSessions(ctx, erased.Deleted)
		if err != nil {
			return report, fmt.Errorf("erase checkpoints: %w", err)
		}
		report.BlobRefs = appendUnique(report.BlobRefs, checkpointBlobRefs(cps)...)
	}
	if oe, ok := s.stores.OutputStore.(stores.OutputEraser); ok {
		n, err := oe.EraseSubject(ctx, tenant, subject)
		if err != nil {
			return report, fmt.Errorf("erase outputs: %w", err)
		}
		report.Outputs = n
	}
	if mme, ok := s.stores.Notes.(stores.MemoryEraser); ok {
		ids, err := mme.EraseSubject(ctx, tenant, subject, erased.Deleted)
		if err != nil {
			return report, fmt.Errorf("erase memory: %w", err)
		}
		report.Memory = ids
	}
	if me, ok := s.stores.RedactionMaps.(stores.RedactionMapEraser); ok {
		for _, sessionID := range erased.Deleted {
			m, err := s.stores.RedactionMaps.Get(ctx, tenant, sessionID)
			if err == nil {
				report.Entries += len(m.Entries)
			}
			if err := me.Delete(ctx, tenant, sessionID); err != nil {
				return report, fmt.Errorf("erase redaction map for session %s: %w", sessionID, err)
			}
		}
	}
	return report, nil
}

func (s *Stack) collectBlobRefs(ctx context.Context, tenant, subject string) ([]string, error) {
	idx, ok := s.stores.SessionLog.(stores.SessionIndex)
	if !ok {
		return nil, nil
	}
	var refs []string
	owner := types.SessionOwner{Tenant: tenant, Subject: subject}
	after := ""
	for {
		metas, cursor, err := idx.Sessions(ctx, owner, stores.SessionQuery{After: after})
		if err != nil {
			return nil, fmt.Errorf("list sessions for erasure: %w", err)
		}
		for _, meta := range metas {
			h, err := s.stores.SessionLog.Load(ctx, meta.ID)
			if err != nil {
				return nil, fmt.Errorf("load session %s for erasure: %w", meta.ID, err)
			}
			refs = appendUnique(refs, historyBlobRefs(h.Messages)...)
		}
		if cursor == "" || len(metas) == 0 {
			return refs, nil
		}
		after = cursor
	}
}

func historyBlobRefs(msgs []types.Message) []string {
	var refs []string
	var walkBlocks func(blocks []types.Block)
	walkBlocks = func(blocks []types.Block) {
		for _, b := range blocks {
			switch v := b.(type) {
			case types.Image:
				refs = append(refs, v.Blob.Ref)
			case types.Audio:
				refs = append(refs, v.Blob.Ref)
			case types.File:
				refs = append(refs, v.Blob.Ref)
			case types.ToolResult:
				refs = append(refs, v.Ref)
				walkBlocks(v.Content)
			case types.Document:
				walkBlocks(v.Content)
			}
		}
	}
	for _, m := range msgs {
		walkBlocks(m.Blocks)
	}
	return refs
}

func checkpointBlobRefs(cps []stores.Checkpoint) []string {
	var refs []string
	for _, cp := range cps {
		refs = appendUnique(refs, jsonBlobRefs(cp.Data)...)
	}
	return refs
}

func jsonBlobRefs(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil
	}
	var refs []string
	var walk func(node any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if ref, ok := n["Ref"].(string); ok && ref != "" {
				refs = append(refs, ref)
			}
			for _, child := range n {
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(v)
	return refs
}

func appendUnique(dst []string, add ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, r := range dst {
		seen[r] = true
	}
	for _, r := range add {
		if !seen[r] {
			seen[r] = true
			dst = append(dst, r)
		}
	}
	sort.Strings(dst)
	return dst
}
