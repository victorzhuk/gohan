package telemetry_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/telemetry"
)

const spoolHeaderSize = 20

type walFrame struct {
	path   string
	offset int64
	seq    uint64
	rec    types.ExportRecord
}

type spoolDiags struct {
	mu  sync.Mutex
	got []types.ExportDiagnostic
}

func (d *spoolDiags) Report(_ context.Context, diag types.ExportDiagnostic) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.got = append(d.got, diag)
}

func (d *spoolDiags) ofKind(k types.DiagnosticKind) []types.ExportDiagnostic {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []types.ExportDiagnostic
	for _, g := range d.got {
		if g.Kind == k {
			out = append(out, g)
		}
	}
	return out
}

func walConfig(quota, seg int64) telemetry.SpoolConfig {
	return telemetry.SpoolConfig{
		ByteQuota:       quota,
		SegmentBytes:    seg,
		MaxPendingAge:   time.Hour,
		Retry:           telemetry.RetryPolicy{MaxAttempts: 3, Backoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond},
		Workers:         1,
		RequestDeadline: time.Second,
	}
}

func walDest() types.ExportDestination {
	return newScriptDest("wal")
}

func walOpen(t *testing.T, dir string, diag *spoolDiags, cfg telemetry.SpoolConfig) *telemetry.Spool {
	t.Helper()
	opts := []telemetry.SpoolOption{}
	if diag != nil {
		opts = append(opts, telemetry.WithSpoolDiagnostics(diag))
	}
	s, err := telemetry.OpenSpool(dir, []types.ExportDestination{walDest()}, cfg, opts...)
	if err != nil {
		t.Fatalf("OpenSpool: %v", err)
	}
	return s
}

// walPopulated creates a spool directory holding three synced records.
func walPopulated(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	s := walOpen(t, dir, nil, walConfig(1<<20, 1<<20))
	for _, id := range []string{"a1", "a2", "a3"} {
		if err := s.Append(t.Context(), "wal", []types.ExportRecord{spanRecord(id)}); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}
	if _, err := s.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	return dir
}

func walSegments(t *testing.T, dir, dest string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, dest))
	if err != nil {
		t.Fatalf("read stream dir: %v", err)
	}
	var segs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "seg-") {
			segs = append(segs, e.Name())
		}
	}
	sort.Strings(segs)
	return segs
}

// readWalFrames decodes every complete record frame in the destination
// stream, verifying the envelope layout the storage engine writes.
func readWalFrames(t *testing.T, dir, dest string) []walFrame {
	t.Helper()
	var frames []walFrame
	for _, seg := range walSegments(t, dir, dest) {
		path := filepath.Join(dir, dest, seg)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read segment: %v", err)
		}
		offset := int64(0)
		for offset < int64(len(data)) {
			if offset+spoolHeaderSize > int64(len(data)) {
				t.Fatalf("truncated header in %s at %d", seg, offset)
			}
			version := binary.LittleEndian.Uint32(data[offset : offset+4])
			if version != telemetry.CurrentSpoolRecordVersion {
				t.Fatalf("frame version %d at %s:%d", version, seg, offset)
			}
			seq := binary.LittleEndian.Uint64(data[offset+4 : offset+12])
			bodyLen := int64(binary.LittleEndian.Uint32(data[offset+12 : offset+16]))
			if offset+spoolHeaderSize+bodyLen > int64(len(data)) {
				t.Fatalf("truncated body in %s at %d", seg, offset)
			}
			body := data[offset+spoolHeaderSize : offset+spoolHeaderSize+bodyLen]
			var rec types.ExportRecord
			if err := json.Unmarshal(body, &rec); err != nil {
				t.Fatalf("decode frame at %s:%d: %v", seg, offset, err)
			}
			frames = append(frames, walFrame{path: path, offset: offset, seq: seq, rec: rec})
			offset += spoolHeaderSize + bodyLen
		}
	}
	return frames
}

func walIDs(frames []walFrame) []string {
	ids := make([]string, 0, len(frames))
	for _, f := range frames {
		ids = append(ids, string(f.rec.ID))
	}
	return ids
}

func walSegmentSizes(t *testing.T, dir, dest string) map[string]int64 {
	t.Helper()
	sizes := make(map[string]int64)
	for _, seg := range walSegments(t, dir, dest) {
		info, err := os.Stat(filepath.Join(dir, dest, seg))
		if err != nil {
			t.Fatalf("stat segment: %v", err)
		}
		sizes[seg] = info.Size()
	}
	return sizes
}

func TestWalAcceptance(t *testing.T) {
	t.Run("telemetry.wal-corruption", func(t *testing.T) {
		ctx := t.Context()
		baseline := walPopulated(t)
		want := readWalFrames(t, baseline, "wal")
		if len(want) != 3 {
			t.Fatalf("populated %d frames, want 3", len(want))
		}

		t.Run("incomplete tail is repaired", func(t *testing.T) {
			dir := t.TempDir()
			populate(t, dir, want)
			segs := walSegments(t, dir, "wal")
			tail := filepath.Join(dir, "wal", segs[len(segs)-1])
			f, err := os.OpenFile(tail, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatalf("open tail segment: %v", err)
			}
			torn := make([]byte, 7)
			binary.LittleEndian.PutUint32(torn[0:4], telemetry.CurrentSpoolRecordVersion)
			if _, err := f.Write(torn); err != nil {
				t.Fatalf("write torn tail: %v", err)
			}
			f.Close()

			diag := &spoolDiags{}
			s := walOpen(t, dir, diag, walConfig(1<<20, 1<<20))
			defer s.Shutdown(ctx)

			h := s.Health()
			if h.Corruption.RepairedTails != 1 || h.Corruption.RepairedBytes != int64(len(torn)) {
				t.Fatalf("corruption report = %+v, want one repair of %d bytes", h.Corruption, len(torn))
			}
			got := readWalFrames(t, dir, "wal")
			if len(got) != len(want) {
				t.Fatalf("after repair %d frames, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i].rec.ID != want[i].rec.ID || got[i].seq != want[i].seq {
					t.Fatalf("frame %d = id %s seq %d, want id %s seq %d", i, got[i].rec.ID, got[i].seq, want[i].rec.ID, want[i].seq)
				}
			}
			repairs := diag.ofKind(telemetry.DiagSpoolRepaired)
			if len(repairs) != 1 || repairs[0].Dropped != int64(len(torn)) {
				t.Fatalf("repair diagnostics = %+v, want one with %d dropped bytes", repairs, len(torn))
			}
			if len(diag.ofKind(telemetry.DiagSpoolCorrupt)) != 0 {
				t.Fatal("tail repair reported as corruption")
			}
		})

		t.Run("interior checksum fails open", func(t *testing.T) {
			dir := t.TempDir()
			populate(t, dir, want)
			target := readWalFrames(t, dir, "wal")[1]
			data, err := os.ReadFile(target.path)
			if err != nil {
				t.Fatalf("read segment: %v", err)
			}
			data[target.offset+spoolHeaderSize+3] ^= 0xff
			if err := os.WriteFile(target.path, data, 0o600); err != nil {
				t.Fatalf("write corrupt byte: %v", err)
			}

			diag := &spoolDiags{}
			s, err := telemetry.OpenSpool(dir, []types.ExportDestination{walDest()}, walConfig(1<<20, 1<<20), telemetry.WithSpoolDiagnostics(diag))
			if err == nil {
				s.Shutdown(ctx)
				t.Fatal("open succeeded over an interior checksum failure")
			}
			var ce *telemetry.CorruptionError
			if !errors.As(err, &ce) {
				t.Fatalf("open err = %T(%v), want *CorruptionError", err, err)
			}
			if ce.Kind != telemetry.CorruptionChecksum {
				t.Fatalf("kind = %s, want %s", ce.Kind, telemetry.CorruptionChecksum)
			}
			if ce.Path != target.path {
				t.Fatalf("path = %s, want %s", ce.Path, target.path)
			}
			if ce.Offset != target.offset {
				t.Fatalf("offset = %d, want %d", ce.Offset, target.offset)
			}
			if ce.Err == nil {
				t.Fatal("corruption error carries no cause")
			}
			reports := diag.ofKind(telemetry.DiagSpoolCorrupt)
			if len(reports) != 1 {
				t.Fatalf("corruption diagnostics = %d, want 1", len(reports))
			}
			var reported *telemetry.CorruptionError
			if !errors.As(reports[0].Err, &reported) {
				t.Fatalf("diagnostic err = %T, want *CorruptionError", reports[0].Err)
			}
			if reported.Kind != telemetry.CorruptionChecksum || reported.Offset != target.offset {
				t.Fatalf("diagnostic corruption = %+v", reported)
			}
		})

		t.Run("unsupported envelope version fails open", func(t *testing.T) {
			dir := t.TempDir()
			populate(t, dir, want)
			target := readWalFrames(t, dir, "wal")[1]
			data, err := os.ReadFile(target.path)
			if err != nil {
				t.Fatalf("read segment: %v", err)
			}
			binary.LittleEndian.PutUint32(data[target.offset:target.offset+4], 99)
			if err := os.WriteFile(target.path, data, 0o600); err != nil {
				t.Fatalf("write version byte: %v", err)
			}

			diag := &spoolDiags{}
			s, err := telemetry.OpenSpool(dir, []types.ExportDestination{walDest()}, walConfig(1<<20, 1<<20), telemetry.WithSpoolDiagnostics(diag))
			if err == nil {
				s.Shutdown(ctx)
				t.Fatal("open succeeded over an unsupported envelope version")
			}
			var ce *telemetry.CorruptionError
			if !errors.As(err, &ce) {
				t.Fatalf("open err = %T(%v), want *CorruptionError", err, err)
			}
			if ce.Kind != telemetry.CorruptionVersion {
				t.Fatalf("kind = %s, want %s", ce.Kind, telemetry.CorruptionVersion)
			}
			if ce.Path != target.path || ce.Offset != target.offset {
				t.Fatalf("corruption = %+v, want path %s offset %d", ce, target.path, target.offset)
			}
			if len(diag.ofKind(telemetry.DiagSpoolCorrupt)) != 1 {
				t.Fatal("missing spool_corrupt diagnostic")
			}
		})
	})

	t.Run("telemetry.wal-capacity-ownership", func(t *testing.T) {
		ctx := t.Context()
		dir := t.TempDir()
		cfg := walConfig(512, 4096)
		s1 := walOpen(t, dir, nil, cfg)
		if err := s1.Append(ctx, "wal", []types.ExportRecord{spanRecord("a1")}); err != nil {
			t.Fatalf("Append a1: %v", err)
		}
		pending := s1.Health().PendingBytes
		if pending <= 0 {
			t.Fatalf("pending bytes = %d, want positive", pending)
		}

		if s2, err := telemetry.OpenSpool(dir, []types.ExportDestination{walDest()}, cfg); !errors.Is(err, telemetry.ErrSpoolLocked) {
			if s2 != nil {
				s2.Shutdown(ctx)
			}
			t.Fatalf("second open err = %v, want ErrSpoolLocked", err)
		}

		before := walSegmentSizes(t, dir, "wal")
		big := spanRecord("big")
		big.Span.Attrs = append(big.Span.Attrs, types.String("gohan.release", strings.Repeat("x", 1024)))
		err := s1.Append(ctx, "wal", []types.ExportRecord{big})
		if !errors.Is(err, telemetry.ErrSpoolQuota) {
			t.Fatalf("Append past quota err = %v, want ErrSpoolQuota", err)
		}
		after := walSegmentSizes(t, dir, "wal")
		if len(before) != len(after) {
			t.Fatalf("segment set changed on quota refusal: %v -> %v", before, after)
		}
		for seg, size := range before {
			if after[seg] != size {
				t.Fatalf("segment %s size %d -> %d on quota refusal", seg, size, after[seg])
			}
		}
		h := s1.Health()
		if h.PendingBytes != pending {
			t.Fatalf("pending bytes = %d, want %d after quota refusal", h.PendingBytes, pending)
		}
		if h.RejectedAppends != 1 {
			t.Fatalf("rejected appends = %d, want 1", h.RejectedAppends)
		}

		if _, err := s1.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if _, err := s1.Shutdown(ctx); err != nil {
			t.Fatalf("second Shutdown: %v", err)
		}

		s3 := walOpen(t, dir, nil, cfg)
		defer s3.Shutdown(ctx)
		got := readWalFrames(t, dir, "wal")
		if len(got) != 1 || got[0].rec.ID != "a1" {
			t.Fatalf("recovered frames = %v, want only a1 unchanged", walIDs(got))
		}
		if got[0].rec.Span == nil || got[0].rec.Span.Name != "chat" {
			t.Fatalf("recovered record lost its payload: %+v", got[0].rec)
		}
		if h3 := s3.Health(); h3.PendingBytes != pending {
			t.Fatalf("pending bytes after reopen = %d, want %d", h3.PendingBytes, pending)
		}
	})
}

func populate(t *testing.T, dir string, frames []walFrame) {
	t.Helper()
	s := walOpen(t, dir, nil, walConfig(1<<20, 1<<20))
	for _, f := range frames {
		if err := s.Append(t.Context(), "wal", []types.ExportRecord{spanRecord(string(f.rec.ID))}); err != nil {
			t.Fatalf("Append %s: %v", f.rec.ID, err)
		}
	}
	if _, err := s.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
