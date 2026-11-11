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
	d := newScriptDest("wal")
	d.script(nil, errors.New("no delivery"))
	return d
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
			_ = f.Close()

			diag := &spoolDiags{}
			s := walOpen(t, dir, diag, walConfig(1<<20, 1<<20))
			defer func() { _, _ = s.Shutdown(ctx) }()

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
				_, _ = s.Shutdown(ctx)
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
				_, _ = s.Shutdown(ctx)
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
				_, _ = s2.Shutdown(ctx)
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
		defer func() { _, _ = s3.Shutdown(ctx) }()
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

	t.Run("telemetry.wal-durable-restart", func(t *testing.T) {
		ctx := t.Context()
		dir := t.TempDir()
		dest := newScriptDest("wal")
		dest.script(nil, errors.New("transient"))
		cfg := walConfig(1<<20, 1<<20)
		s := walOpenDests(t, dir, nil, cfg, dest)
		for _, id := range []string{"a1", "a2", "a3"} {
			if err := s.Append(ctx, "wal", []types.ExportRecord{spanRecord(id)}); err != nil {
				t.Fatalf("Append %s: %v", id, err)
			}
		}
		want := readWalFrames(t, dir, "wal")
		if len(want) != 3 {
			t.Fatalf("synced %d frames, want 3", len(want))
		}

		work, err := s.Shutdown(ctx)
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if work.Records != 3 {
			t.Fatalf("shutdown unresolved = %d, want 3", work.Records)
		}

		s2 := walOpenDests(t, dir, nil, cfg, dest)
		defer func() { _, _ = s2.Shutdown(ctx) }()
		dest.script(acceptAll(want), nil)
		f, err := s2.Flush(ctx)
		if err != nil {
			t.Fatalf("Flush: %v", err)
		}
		if f.Synced != 0 {
			t.Fatalf("flush synced = %d, want 0", f.Synced)
		}
		if f.Delivered["wal"] != 3 {
			t.Fatalf("flush delivered = %v, want 3 for wal", f.Delivered)
		}
		if f.Undelivered.Records != 0 {
			t.Fatalf("flush undelivered = %+v, want none", f.Undelivered)
		}
		att := dest.allAttempts()
		if len(att) < 4 {
			t.Fatalf("attempts after restart = %v, want a post-restart delivery", att)
		}
		for _, ids := range att[3:] {
			if strings.Join(ids, ",") != "a1,a2,a3" {
				t.Fatalf("restarted attempt = %v, want [a1 a2 a3] unchanged", ids)
			}
		}
		h := s2.Health()
		dh := h.Destinations[0]
		if dh.Cursor.Acked != 3 || dh.Pending != 0 {
			t.Fatalf("health after restart = %+v, want cursor 3 pending 0", dh)
		}
		if frames := readWalFrames(t, dir, "wal"); len(frames) != 0 {
			t.Fatalf("acknowledged prefix not reclaimed: %d frames left", len(frames))
		}
	})

	t.Run("telemetry.wal-destination-cursors", func(t *testing.T) {
		ctx := t.Context()
		dir := t.TempDir()
		d1 := newScriptDest("sink1")
		d2 := newScriptDest("sink2")
		d1.script([]types.RecordOutcome{
			{ID: "b1", Status: types.OutcomeAccepted},
			{ID: "b2", Status: types.OutcomeAccepted},
		}, nil)
		d2.script(nil, errors.New("transient"))
		diag := &spoolDiags{}
		cfg := walConfig(1<<20, 1<<20)
		s := walOpenDests(t, dir, diag, cfg, d1, d2)
		for _, id := range []string{"b1", "b2"} {
			if err := s.Append(ctx, "sink1", []types.ExportRecord{spanRecord(id)}); err != nil {
				t.Fatalf("Append sink1 %s: %v", id, err)
			}
		}
		for _, id := range []string{"c1", "c2"} {
			if err := s.Append(ctx, "sink2", []types.ExportRecord{spanRecord(id)}); err != nil {
				t.Fatalf("Append sink2 %s: %v", id, err)
			}
		}

		f, err := s.Flush(ctx)
		if err != nil {
			t.Fatalf("Flush: %v", err)
		}
		if f.Delivered["sink1"] != 2 {
			t.Fatalf("flush delivered = %v, want 2 for sink1", f.Delivered)
		}
		if f.Delivered["sink2"] != 0 {
			t.Fatalf("flush delivered sink2 = %v, want 0", f.Delivered)
		}
		if f.Undelivered.ByDestination["sink2"] != 2 {
			t.Fatalf("flush undelivered = %+v, want 2 for sink2", f.Undelivered)
		}
		if frames := readWalFrames(t, dir, "sink1"); len(frames) != 0 {
			t.Fatalf("acknowledged sink1 prefix not reclaimed: %d frames", len(frames))
		}
		if _, err := s.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}

		d2.script([]types.RecordOutcome{
			{ID: "c1", Status: types.OutcomeAccepted},
			{ID: "c2", Status: types.OutcomeAmbiguous},
		}, nil)
		s2 := walOpenDests(t, dir, diag, cfg, d1, d2)
		f2, err := s2.Flush(ctx)
		if err != nil {
			t.Fatalf("reopen Flush: %v", err)
		}
		if got := d1.allAttempts(); len(got) != 1 || strings.Join(got[0], ",") != "b1,b2" {
			t.Fatalf("sink1 attempts after restart = %v, acknowledged prefix re-delivered", got)
		}
		if f2.Delivered["sink2"] != 1 {
			t.Fatalf("reopen delivered = %v, want 1 for sink2", f2.Delivered)
		}
		if f2.Undelivered.ByDestination["sink2"] != 1 {
			t.Fatalf("reopen undelivered = %+v, want the ambiguous record", f2.Undelivered)
		}
		if amb := diag.ofKind(types.DiagDeliveryAmbiguous); len(amb) == 0 {
			t.Fatal("missing delivery_ambiguous diagnostic")
		}
		h := s2.Health()
		byName := map[string]telemetry.DestinationHealth{}
		for _, dh := range h.Destinations {
			byName[dh.Name] = dh
		}
		if byName["sink1"].Cursor.Acked != 2 || byName["sink1"].Pending != 0 {
			t.Fatalf("sink1 cursor = %+v, want acked 2 pending 0", byName["sink1"].Cursor)
		}
		if byName["sink2"].Cursor.Acked != 3 || byName["sink2"].Pending != 1 {
			t.Fatalf("sink2 cursor = %+v, want acked 3 pending 1", byName["sink2"].Cursor)
		}
		if _, err := s2.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}

		s3 := walOpenDests(t, dir, nil, cfg, d1, d2)
		defer func() { _, _ = s3.Shutdown(ctx) }()
		d2.script([]types.RecordOutcome{{ID: "c2", Status: types.OutcomeAccepted}}, nil)
		f3, err := s3.Flush(ctx)
		if err != nil {
			t.Fatalf("final Flush: %v", err)
		}
		if f3.Delivered["sink2"] != 1 {
			t.Fatalf("final delivered = %v, want 1 for sink2", f3.Delivered)
		}
		att := d2.allAttempts()
		last := att[len(att)-1]
		if len(last) != 1 || last[0] != "c2" {
			t.Fatalf("replayed attempt = %v, want unresolved record with unchanged id c2", last)
		}
		h3 := s3.Health()
		for _, dh := range h3.Destinations {
			if dh.Pending != 0 || dh.Cursor.Acked == 0 {
				t.Fatalf("final health %s = %+v, want drained stream", dh.Name, dh)
			}
		}
	})

	t.Run("telemetry.wal-content-erasure", func(t *testing.T) {
		ctx := t.Context()
		cfg := walConfig(1<<20, 1<<20)
		const attr = "wal.actor"
		rule := telemetry.SubjectErasure{Attr: attr, Values: []string{"alice", "bob"}}
		failed := func(d *scriptDest) { d.script(nil, errors.New("no delivery")) }

		t.Run("in-process erasure removes subjects and reports counts", func(t *testing.T) {
			dir := t.TempDir()
			d1, d2 := newScriptDest("s1"), newScriptDest("s2")
			failed(d1)
			failed(d2)
			s := walOpenDests(t, dir, nil, cfg, d1, d2)
			for _, rec := range []types.ExportRecord{
				walKeepSpan("p1"),
				walSubjectSpan("alice3", attr, "alice", 0),
				walSubjectMetric("bob3", attr, "bob"),
				walKeepSpan("p2"),
			} {
				dest := "s1"
				if strings.HasPrefix(string(rec.ID), "b") || string(rec.ID) == "p2" {
					dest = "s2"
				}
				if err := s.Append(ctx, dest, []types.ExportRecord{rec}); err != nil {
					t.Fatalf("Append %s: %v", rec.ID, err)
				}
			}
			want := map[string]walFrame{}
			for _, f := range readWalFrames(t, dir, "s1") {
				want[string(f.rec.ID)] = f
			}
			for _, f := range readWalFrames(t, dir, "s2") {
				want[string(f.rec.ID)] = f
			}
			var removedBytes int64
			for _, id := range []string{"alice3", "bob3"} {
				removedBytes += spoolHeaderSize + int64(len(walJSON(want[id].rec)))
			}

			res, err := s.EraseSubjects(ctx, rule)
			if err != nil {
				t.Fatalf("EraseSubjects: %v", err)
			}
			if res.Records != 2 || res.Bytes != removedBytes {
				t.Fatalf("erasure result = %+v, want 2 records and %d bytes", res, removedBytes)
			}
			if _, err := os.Stat(filepath.Join(dir, "erasure.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("tombstone survived completion: %v", err)
			}
			got := map[string]uint64{}
			for _, f := range readWalFrames(t, dir, "s1") {
				got[string(f.rec.ID)] = f.seq
			}
			for _, f := range readWalFrames(t, dir, "s2") {
				got[string(f.rec.ID)] = f.seq
			}
			if len(got) != 2 || got["p1"] != want["p1"].seq || got["p2"] != want["p2"].seq {
				t.Fatalf("surviving frames = %v, want p1 seq %d and p2 seq %d with sequences preserved", got, want["p1"].seq, want["p2"].seq)
			}
			d1.script([]types.RecordOutcome{{ID: "p1", Status: types.OutcomeAccepted}}, nil)
			d2.script([]types.RecordOutcome{{ID: "p2", Status: types.OutcomeAccepted}}, nil)
			if _, err := s.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			walAssertAttempts(t, [][][]string{d1.allAttempts(), d2.allAttempts()}, []string{"p1", "p2"})
			if _, err := s.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
		})

		t.Run("interrupted erasure completes at open without replay", func(t *testing.T) {
			dir := t.TempDir()
			d1, d2 := newScriptDest("sink1"), newScriptDest("sink2")
			d1.script(nil, errors.New("no delivery"))
			d2.script([]types.RecordOutcome{{ID: "alice2", Status: types.OutcomeAccepted}}, nil)
			d2.script(nil, errors.New("no delivery"))
			s := walOpenDests(t, dir, nil, cfg, d1, d2)
			for _, rec := range []types.ExportRecord{
				walKeepSpan("k1"),
				walSubjectSpan("alice1", attr, "alice", 0),
				walSubjectMetric("bob1", attr, "bob"),
				walSubjectSpan("k2", attr, "carol", 1),
			} {
				if err := s.Append(ctx, "sink1", []types.ExportRecord{rec}); err != nil {
					t.Fatalf("Append sink1 %s: %v", rec.ID, err)
				}
			}
			for _, rec := range []types.ExportRecord{
				walSubjectSpan("alice2", attr, "alice", 0),
				walKeepSpan("k3"),
				walSubjectSpan("bob2", attr, "bob", 1),
			} {
				if err := s.Append(ctx, "sink2", []types.ExportRecord{rec}); err != nil {
					t.Fatalf("Append sink2 %s: %v", rec.ID, err)
				}
			}
			if _, err := s.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			before := map[string]walFrame{}
			for _, f := range readWalFrames(t, dir, "sink1") {
				before[string(f.rec.ID)] = f
			}
			for _, f := range readWalFrames(t, dir, "sink2") {
				before[string(f.rec.ID)] = f
			}
			if _, err := s.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			var wantAcked int64
			for _, d := range s.Health().Destinations {
				if d.Name == "sink2" {
					wantAcked = d.Cursor.Acked
				}
			}

			failedD1, failedD2 := newScriptDest("sink1"), newScriptDest("sink2")
			failed(failedD1)
			failed(failedD2)
			s2 := walOpenDests(t, dir, nil, cfg, failedD1, failedD2)
			walWriteTombstone(t, dir, rule)
			if _, err := s2.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown with tombstone: %v", err)
			}
			walDropFrames(t, dir, "sink1", map[string]bool{"alice1": true, "bob1": true})

			okD1, okD2 := newScriptDest("sink1"), newScriptDest("sink2")
			okD1.script([]types.RecordOutcome{
				{ID: "k1", Status: types.OutcomeAccepted},
				{ID: "k2", Status: types.OutcomeAccepted},
			}, nil)
			okD2.script([]types.RecordOutcome{{ID: "k3", Status: types.OutcomeAccepted}}, nil)
			s3 := walOpenDests(t, dir, nil, cfg, okD1, okD2)
			if _, err := os.Stat(filepath.Join(dir, "erasure.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("open left the tombstone in place: %v", err)
			}
			h := s3.Health()
			byName := map[string]telemetry.DestinationHealth{}
			for _, d := range h.Destinations {
				byName[d.Name] = d
			}
			if byName["sink2"].Cursor.Acked != wantAcked {
				t.Fatalf("sink2 cursor after recovery = %d, want %d", byName["sink2"].Cursor.Acked, wantAcked)
			}
			got := map[string]uint64{}
			for _, dest := range []string{"sink1", "sink2"} {
				for _, f := range readWalFrames(t, dir, dest) {
					got[string(f.rec.ID)] = f.seq
				}
			}
			for _, id := range []string{"k1", "k2", "k3"} {
				if got[id] != before[id].seq {
					t.Fatalf("survivor %s seq = %d, want %d with sequences never renumbered", id, got[id], before[id].seq)
				}
			}
			if len(got) != 3 {
				t.Fatalf("surviving frames = %v, want exactly k1 k2 k3", got)
			}
			if _, err := s3.Flush(ctx); err != nil {
				t.Fatalf("post-recovery Flush: %v", err)
			}
			walAssertAttempts(t, [][][]string{okD1.allAttempts(), okD2.allAttempts()}, []string{"k1", "k2", "k3"})
			if frames := readWalFrames(t, dir, "sink1"); len(frames) != 0 {
				t.Fatalf("sink1 kept %d frames after delivery", len(frames))
			}
			if frames := readWalFrames(t, dir, "sink2"); len(frames) != 0 {
				t.Fatalf("sink2 kept %d frames after delivery", len(frames))
			}
			if _, err := s3.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
		})
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

func walOpenDests(t *testing.T, dir string, diag *spoolDiags, cfg telemetry.SpoolConfig, dests ...types.ExportDestination) *telemetry.Spool {
	t.Helper()
	opts := []telemetry.SpoolOption{}
	if diag != nil {
		opts = append(opts, telemetry.WithSpoolDiagnostics(diag))
	}
	s, err := telemetry.OpenSpool(dir, dests, cfg, opts...)
	if err != nil {
		t.Fatalf("OpenSpool: %v", err)
	}
	return s
}

func acceptAll(frames []walFrame) []types.RecordOutcome {
	out := make([]types.RecordOutcome, 0, len(frames))
	for _, fr := range frames {
		out = append(out, types.RecordOutcome{ID: fr.rec.ID, Status: types.OutcomeAccepted})
	}
	return out
}
