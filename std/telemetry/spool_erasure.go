package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/victorzhuk/gohan/core/types"
)

const spoolErasureFile = "erasure.json"

// matchErasure reports whether the record carries a string value of the
// subject attribute across resource identity attrs, span attrs and metric
// dimensions. Matching is string equality on decoded Attr values only.
func matchErasure(rule SubjectErasure, rec types.ExportRecord) bool {
	var vals map[string]bool
	hit := func(attrs []types.Attr) bool {
		for _, a := range attrs {
			if a.Key != rule.Attr {
				continue
			}
			if s, ok := a.Value.(string); ok {
				if vals == nil {
					vals = make(map[string]bool, len(rule.Values))
					for _, v := range rule.Values {
						vals[v] = true
					}
				}
				if vals[s] {
					return true
				}
			}
		}
		return false
	}
	if hit(rec.Resource.Attrs) {
		return true
	}
	if rec.Span != nil && hit(rec.Span.Attrs) {
		return true
	}
	return rec.Metric != nil && hit(rec.Metric.Dimensions)
}

// firstErasureMatch returns the first batch index matching an active
// tombstone, so a delivery pass stops before reaching erased content.
func (s *Spool) firstErasureMatch(batch []spoolRec) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.erasure == nil {
		return -1
	}
	for i, r := range batch {
		if matchErasure(*s.erasure, r.rec) {
			return i
		}
	}
	return -1
}

func writeErasureTombstone(dir string, rule SubjectErasure) error {
	data, err := json.Marshal(rule)
	if err != nil {
		return fmt.Errorf("gohan/telemetry: spool erasure: marshal tombstone: %w", err)
	}
	tmp := filepath.Join(dir, spoolErasureFile+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("gohan/telemetry: spool erasure: write tombstone: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("gohan/telemetry: spool erasure: write tombstone: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("gohan/telemetry: spool erasure: sync tombstone: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("gohan/telemetry: spool erasure: close tombstone: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, spoolErasureFile)); err != nil {
		return fmt.Errorf("gohan/telemetry: spool erasure: persist tombstone: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("gohan/telemetry: spool erasure: sync spool dir: %w", err)
	}
	return nil
}

func clearErasureTombstone(dir string) error {
	if err := os.Remove(filepath.Join(dir, spoolErasureFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("gohan/telemetry: spool erasure: clear tombstone: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("gohan/telemetry: spool erasure: sync spool dir: %w", err)
	}
	return nil
}

// loadErasure returns the active tombstone. A malformed tombstone is an
// error: matching content must never serve past a durable erasure rule.
func loadErasure(dir string) (SubjectErasure, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, spoolErasureFile))
	if errors.Is(err, os.ErrNotExist) {
		return SubjectErasure{}, false, nil
	}
	if err != nil {
		return SubjectErasure{}, false, fmt.Errorf("gohan/telemetry: spool: read erasure tombstone: %w", err)
	}
	var rule SubjectErasure
	if err := json.Unmarshal(data, &rule); err != nil {
		return SubjectErasure{}, false, fmt.Errorf("gohan/telemetry: spool: decode erasure tombstone: %w", err)
	}
	if rule.Attr == "" {
		return SubjectErasure{}, false, errors.New("gohan/telemetry: spool: erasure tombstone names no attribute")
	}
	return rule, true, nil
}

// compactErasure removes every pending record matching the rule from all
// streams. Surviving records keep their sequence numbers and order and the
// cursor compacts with them. Retired segments are unlinked and the stream
// directory fsynced by compactStream before the stream counts as done.
func (s *Spool) compactErasure(ctx context.Context, rule SubjectErasure) (ErasureResult, error) {
	var res ErasureResult
	for _, name := range slices.Sorted(maps.Keys(s.dests)) {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		st := s.dests[name]
		st.mu.Lock()
		kept := make([]spoolRec, 0, len(st.pending))
		var removed, bytes int64
		for _, r := range st.pending {
			if matchErasure(rule, r.rec) {
				removed++
				bytes += r.bytes
				continue
			}
			kept = append(kept, r)
		}
		if removed == 0 {
			st.mu.Unlock()
			continue
		}
		st.pending = kept
		err := s.compactStream(st, kept)
		st.mu.Unlock()
		if err != nil {
			return res, fmt.Errorf("gohan/telemetry: spool %s: erasure compaction: %w", name, err)
		}
		s.mu.Lock()
		s.pending -= bytes
		s.mu.Unlock()
		res.Records += removed
		res.Bytes += bytes
	}
	return res, nil
}

// clearErasureState drops in-memory parked and expired entries for records
// the erasure removed, so no stream stays gated on vanished IDs.
func (s *Spool) clearErasureState(rule SubjectErasure) {
	for _, dd := range s.dds {
		dd.mu.Lock()
		st := dd.st
		st.mu.Lock()
		for _, r := range st.pending {
			if matchErasure(rule, r.rec) {
				delete(dd.parked, r.rec.ID)
				delete(dd.expired, r.rec.ID)
			}
		}
		st.mu.Unlock()
		dd.mu.Unlock()
	}
}

// EraseSubjects removes every record matching the erasure rule from all
// streams, crash-safely, and never lets matching content replay again.
func (s *Spool) EraseSubjects(ctx context.Context, subjects SubjectErasure) (ErasureResult, error) {
	if err := ctx.Err(); err != nil {
		return ErasureResult{}, err
	}
	if subjects.Attr == "" {
		return ErasureResult{}, errors.New("gohan/telemetry: spool erasure: subject attribute key must not be empty")
	}
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return ErasureResult{}, fmt.Errorf("gohan/telemetry: spool erasure: %w", types.ErrExportShutdown)
	}
	if err := writeErasureTombstone(s.dir, subjects); err != nil {
		s.mu.Unlock()
		return ErasureResult{}, err
	}
	s.erasure = &subjects
	s.mu.Unlock()

	res, err := s.compactErasure(ctx, subjects)
	if err != nil {
		return res, fmt.Errorf("gohan/telemetry: spool erasure: %w", err)
	}
	s.clearErasureState(subjects)
	if err := clearErasureTombstone(s.dir); err != nil {
		return res, err
	}
	s.mu.Lock()
	s.erasure = nil
	s.mu.Unlock()
	return res, nil
}
