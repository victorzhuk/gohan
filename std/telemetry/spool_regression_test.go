package telemetry_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/telemetry"
)

type acceptDest struct {
	name string

	mu    sync.Mutex
	steps []scriptStep
	idx   int
	sent  [][]string
}

func newAcceptDest(name string) *acceptDest { return &acceptDest{name: name} }

func (d *acceptDest) script(outcomes []types.RecordOutcome, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.steps = append(d.steps, scriptStep{outcomes: outcomes, err: err})
}

func (d *acceptDest) Name() string { return d.name }

func (d *acceptDest) Capabilities() types.DestinationCapabilities {
	return types.DestinationCapabilities{
		Signals:     []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
		Replay:      types.ReplayDedupByID,
		Temporality: types.TemporalityCumulative,
	}
}

func (d *acceptDest) Export(_ context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var stepErr error
	if len(d.steps) > 0 {
		stepErr = d.steps[min(d.idx, len(d.steps)-1)].err
		d.idx++
	}
	ids := make([]string, 0, len(recs))
	out := make([]types.RecordOutcome, 0, len(recs))
	for _, rec := range recs {
		ids = append(ids, string(rec.ID))
		if stepErr == nil {
			out = append(out, types.RecordOutcome{ID: rec.ID, Status: types.OutcomeAccepted})
		}
	}
	d.sent = append(d.sent, ids)
	return out, stepErr
}

func (d *acceptDest) allAttempts() [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]string, len(d.sent))
	for i, a := range d.sent {
		out[i] = append([]string(nil), a...)
	}
	return out
}

func (d *acceptDest) attemptCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sent)
}

type stuckDest struct {
	name string
	mu   sync.Mutex
	sent [][]string
}

func newStuckDest(name string) *stuckDest { return &stuckDest{name: name} }

func (d *stuckDest) Name() string { return d.name }

func (d *stuckDest) Capabilities() types.DestinationCapabilities {
	return types.DestinationCapabilities{
		Signals:     []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
		Replay:      types.ReplayDedupByID,
		Temporality: types.TemporalityCumulative,
	}
}

func (d *stuckDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	ids := make([]string, 0, len(recs))
	for _, rec := range recs {
		ids = append(ids, string(rec.ID))
	}
	d.mu.Lock()
	d.sent = append(d.sent, ids)
	d.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (d *stuckDest) attemptCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sent)
}

func boundedCtx(t *testing.T) context.Context {
	t.Helper()
	cx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return cx
}

func walAppend(t *testing.T, s *telemetry.Spool, dest, id string) {
	t.Helper()
	if err := s.Append(t.Context(), dest, []types.ExportRecord{spanRecord(id)}); err != nil {
		t.Fatalf("Append %s: %v", id, err)
	}
}

func walFrameBytes(t *testing.T, frames []walFrame) int64 {
	t.Helper()
	var total int64
	for _, f := range frames {
		total += spoolHeaderSize + int64(len(walJSON(f.rec)))
	}
	return total
}

func walWriteSeg(t *testing.T, dir, dest, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, dest), 0o700); err != nil {
		t.Fatalf("mkdir stream dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, dest, name), data, 0o600); err != nil {
		t.Fatalf("write segment %s: %v", name, err)
	}
}

func TestWalFlushTwiceCounts(t *testing.T) {
	ctx := boundedCtx(t)
	dir := t.TempDir()
	dest := newAcceptDest("wal")
	s := walOpenDests(t, dir, nil, walConfig(8<<20, 1<<20), dest)

	recs := make([]types.ExportRecord, 0, 300)
	for i := range 300 {
		recs = append(recs, spanRecord(fmt.Sprintf("r%d", i)))
	}
	if err := s.Admit(ctx, "wal", recs); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	f1, err := s.Flush(ctx)
	if err != nil {
		t.Fatalf("first Flush: %v", err)
	}
	if f1.Synced != 300 {
		t.Fatalf("first flush synced = %d, want 300", f1.Synced)
	}
	if f1.Delivered["wal"] != 300 {
		t.Fatalf("first flush delivered = %v, want 300 for wal: one pass drains the whole queue", f1.Delivered)
	}
	if f1.Undelivered.Records != 0 {
		t.Fatalf("first flush undelivered = %+v, want none", f1.Undelivered)
	}

	more := make([]types.ExportRecord, 0, 5)
	for i := range 5 {
		more = append(more, spanRecord(fmt.Sprintf("x%d", i)))
	}
	if err := s.Admit(ctx, "wal", more); err != nil {
		t.Fatalf("Admit more: %v", err)
	}
	f2, err := s.Flush(ctx)
	if err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	if f2.Synced != 5 {
		t.Fatalf("second flush synced = %d, want 5", f2.Synced)
	}
	if f2.Delivered["wal"] != 5 {
		t.Fatalf("second flush delivered = %v, want 5: second flush must report its own pass, not stale %d", f2.Delivered, 300)
	}
	if f2.Undelivered.Records != 0 {
		t.Fatalf("second flush undelivered = %+v, want none", f2.Undelivered)
	}
	if _, err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestWalNoRedeliveryAcceptedHead(t *testing.T) {
	ctx := boundedCtx(t)
	dir := t.TempDir()
	dest := newAcceptDest("wal")
	s := walOpenDests(t, dir, nil, walConfig(1<<20, 1<<20), dest)
	for i := range 10 {
		walAppend(t, s, "wal", fmt.Sprintf("n%d", i))
	}
	f, err := s.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if f.Delivered["wal"] != 10 {
		t.Fatalf("flush delivered = %v, want 10", f.Delivered)
	}

	counts := map[string]int{}
	for _, batch := range dest.allAttempts() {
		for _, id := range batch {
			counts[id]++
		}
	}
	if len(counts) != 10 {
		t.Fatalf("attempted ids = %v, want 10 distinct", counts)
	}
	for id, n := range counts {
		if n != 1 {
			t.Fatalf("id %s delivered %d times, want exactly once overall", id, n)
		}
	}

	before := dest.attemptCount()
	f2, err := s.Flush(ctx)
	if err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	if len(f2.Delivered) != 0 {
		t.Fatalf("second flush delivered = %v, want none", f2.Delivered)
	}
	if dest.attemptCount() != before {
		t.Fatalf("second flush made %d new attempts, want 0", dest.attemptCount()-before)
	}
	if _, err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestWalDiscardThenAppendRestart(t *testing.T) {
	ctx := boundedCtx(t)
	dir := t.TempDir()
	stuck := newStuckDest("wal")
	cfg := walConfig(1<<20, 1<<20)
	cfg.RequestDeadline = 20 * time.Millisecond
	s := walOpenDests(t, dir, nil, cfg, stuck)
	for _, id := range []string{"a1", "a2", "a3"} {
		walAppend(t, s, "wal", id)
	}
	removed, err := s.Discard(ctx, "wal", "a2")
	if err != nil {
		t.Fatalf("Discard a2: %v", err)
	}
	if removed != 1 {
		t.Fatalf("Discard removed = %d, want 1", removed)
	}
	walAppend(t, s, "wal", "a4")

	frames := readWalFrames(t, dir, "wal")
	if len(frames) != 3 {
		t.Fatalf("frames after discard+append = %v, want a1 a3 a4", walIDs(frames))
	}
	var prev uint64
	for _, f := range frames {
		if f.seq <= prev {
			t.Fatalf("sequence %d after %d, want strictly increasing", f.seq, prev)
		}
		prev = f.seq
	}
	if frames[1].rec.ID != "a3" || frames[1].seq != 3 || frames[2].rec.ID != "a4" || frames[2].seq != 4 {
		t.Fatalf("sequences = %v/%v, want a3 at 3 and a4 at 4: discarded a2 number never reused", walIDs(frames), []uint64{frames[0].seq, frames[1].seq, frames[2].seq})
	}
	if _, err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	dest := newAcceptDest("wal")
	s2 := walOpenDests(t, dir, nil, cfg, dest)
	f, err := s2.Flush(ctx)
	if err != nil {
		t.Fatalf("reopen Flush: %v", err)
	}
	if f.Delivered["wal"] != 3 {
		t.Fatalf("reopen delivered = %v, want 3", f.Delivered)
	}
	var delivered []string
	for _, batch := range dest.allAttempts() {
		delivered = append(delivered, batch...)
	}
	if len(delivered) != 3 || delivered[0] != "a1" || delivered[1] != "a3" || delivered[2] != "a4" {
		t.Fatalf("delivered stream = %v, want [a1 a3 a4] with unchanged ids in sequence order", delivered)
	}
	for _, id := range delivered {
		if id == "a2" {
			t.Fatal("discarded record a2 reappeared after restart")
		}
	}
	if _, err := s2.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestWalFullDrainRestart(t *testing.T) {
	ctx := boundedCtx(t)
	dir := t.TempDir()
	cfg := walConfig(1<<20, 1<<20)
	dest := newAcceptDest("wal")
	s := walOpenDests(t, dir, nil, cfg, dest)
	walAppend(t, s, "wal", "a1")
	walAppend(t, s, "wal", "a2")
	f, err := s.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if f.Delivered["wal"] != 2 {
		t.Fatalf("flush delivered = %v, want 2", f.Delivered)
	}
	if segs := walSegments(t, dir, "wal"); len(segs) != 0 {
		t.Fatalf("acked prefix not reclaimed: segments %v", segs)
	}
	if _, err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	dest2 := newAcceptDest("wal")
	s2 := walOpenDests(t, dir, nil, cfg, dest2)
	walAppend(t, s2, "wal", "b1")
	frames := readWalFrames(t, dir, "wal")
	if len(frames) != 1 || frames[0].rec.ID != "b1" || frames[0].seq != 3 {
		t.Fatalf("frames after restart append = %v, want only b1 at seq 3", walIDs(frames))
	}
	f2, err := s2.Flush(ctx)
	if err != nil {
		t.Fatalf("reopen Flush: %v", err)
	}
	if f2.Delivered["wal"] != 1 {
		t.Fatalf("reopen delivered = %v, want 1", f2.Delivered)
	}
	var delivered []string
	for _, batch := range dest2.allAttempts() {
		delivered = append(delivered, batch...)
	}
	if len(delivered) != 1 || delivered[0] != "b1" {
		t.Fatalf("replayed attempts = %v, want only b1 with no a1/a2 replay", delivered)
	}
	if _, err := s2.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestWalBacklogRetainSurvives(t *testing.T) {
	ctx := boundedCtx(t)
	dir := t.TempDir()
	cfg := walConfig(1<<20, 1<<20)
	const attr = "wal.actor"
	c1 := newAcceptDest("c1")
	c2 := newAcceptDest("c2")
	c2.script(nil, errors.New("transient"))
	s := walOpenDests(t, dir, nil, cfg, c1, c2)
	walAppend(t, s, "c1", "x1")
	walAppend(t, s, "c1", "x2")
	if err := s.Append(ctx, "c2", []types.ExportRecord{walSubjectSpan("m1", attr, "alice", 0)}); err != nil {
		t.Fatalf("Append m1: %v", err)
	}
	walAppend(t, s, "c2", "m2")

	c2Frames := readWalFrames(t, dir, "c2")
	if len(c2Frames) != 2 {
		t.Fatalf("c2 frames = %v, want m1 m2", walIDs(c2Frames))
	}
	retainedBytes := walFrameBytes(t, c2Frames)

	f, err := s.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if f.Delivered["c1"] != 2 || f.Delivered["c2"] != 0 {
		t.Fatalf("flush delivered = %v, want c1 2 and c2 0", f.Delivered)
	}
	if err := s.RemoveDestination(ctx, "c2", telemetry.BacklogRetain); err != nil {
		t.Fatalf("RemoveDestination retain: %v", err)
	}
	if _, err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	c1b := newAcceptDest("c1")
	s2 := walOpenDests(t, dir, nil, cfg, c1b)
	h := s2.Health()
	var c2Health *telemetry.DestinationHealth
	for i := range h.Destinations {
		if h.Destinations[i].Name == "c2" {
			c2Health = &h.Destinations[i]
		}
	}
	if c2Health == nil {
		t.Fatalf("health destinations = %+v, want retained c2 still counted", h.Destinations)
	}
	if c2Health.Pending != 2 {
		t.Fatalf("c2 pending = %d, want 2 retained records", c2Health.Pending)
	}
	if h.PendingBytes != retainedBytes {
		t.Fatalf("pending bytes = %d, want %d: retained backlog stays quota-counted", h.PendingBytes, retainedBytes)
	}

	rule := telemetry.SubjectErasure{Attr: attr, Values: []string{"alice"}}
	res, err := s2.EraseSubjects(ctx, rule)
	if err != nil {
		t.Fatalf("EraseSubjects: %v", err)
	}
	if res.Records != 1 {
		t.Fatalf("erasure records = %d, want 1 (m1 carries the subject)", res.Records)
	}
	var m1Bytes int64
	for _, fr := range c2Frames {
		if string(fr.rec.ID) == "m1" {
			m1Bytes = spoolHeaderSize + int64(len(walJSON(fr.rec)))
		}
	}
	if res.Bytes != m1Bytes {
		t.Fatalf("erasure bytes = %d, want %d", res.Bytes, m1Bytes)
	}
	if _, err := s2.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	c1c := newAcceptDest("c1")
	c2c := newAcceptDest("c2")
	s3 := walOpenDests(t, dir, nil, cfg, c1c, c2c)
	defer func() { _, _ = s3.Shutdown(ctx) }()
	got := readWalFrames(t, dir, "c2")
	if len(got) != 1 || got[0].rec.ID != "m2" {
		t.Fatalf("c2 stream after erasure = %v, want only survivor m2", walIDs(got))
	}
	wantSeq := uint64(2)
	for _, fr := range c2Frames {
		if string(fr.rec.ID) == "m2" {
			wantSeq = fr.seq
		}
	}
	if got[0].seq != wantSeq {
		t.Fatalf("survivor m2 seq = %d, want preserved %d", got[0].seq, wantSeq)
	}
	if _, err := s3.Flush(ctx); err != nil {
		t.Fatalf("reopen Flush: %v", err)
	}
	walAssertAttempts(t, [][][]string{c1c.allAttempts(), c2c.allAttempts()}, []string{"m2"})
}

func TestWalErasureBypassAdmissionQueue(t *testing.T) {
	ctx := boundedCtx(t)
	dir := t.TempDir()
	stuck := newStuckDest("wal")
	cfg := walConfig(1<<20, 1<<20)
	cfg.RequestDeadline = 20 * time.Millisecond
	s := walOpenDests(t, dir, nil, cfg, stuck)

	rec := walSubjectSpan("q1", "wal.actor", "alice", 0)
	if err := s.Admit(ctx, "wal", []types.ExportRecord{rec}); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	res, err := s.EraseSubjects(ctx, telemetry.SubjectErasure{Attr: "wal.actor", Values: []string{"alice"}})
	if err != nil {
		t.Fatalf("EraseSubjects: %v", err)
	}
	if res.Records != 1 {
		t.Fatalf("erasure records = %d, want 1: matched record purged from the admission queue", res.Records)
	}

	f, err := s.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if f.Synced != 0 {
		t.Fatalf("flush synced = %d, want 0: erased record never written", f.Synced)
	}
	if f.Delivered["wal"] != 0 {
		t.Fatalf("flush delivered = %v, want 0", f.Delivered)
	}
	if segs := walSegments(t, dir, "wal"); len(segs) != 0 {
		t.Fatalf("segments = %v, want none: matched record must never reach a segment", segs)
	}
	if stuck.attemptCount() != 0 {
		t.Fatalf("delivery attempts = %d, want 0", stuck.attemptCount())
	}
	if _, err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestWalFramingCorruptionClasses(t *testing.T) {
	ctx := t.Context()
	mkStream := func(t *testing.T, mutate func(b2 []byte) []byte) (string, string) {
		t.Helper()
		dir := t.TempDir()
		b1 := walEncode(1, spanRecord("a1"))
		b2 := walEncode(2, spanRecord("a2"))
		b3 := walEncode(3, spanRecord("a3"))
		walWriteSeg(t, dir, "wal", "seg-00000000", append(append([]byte{}, b1...), mutate(b2)...))
		walWriteSeg(t, dir, "wal", "seg-00000001", b3)
		return dir, filepath.Join(dir, "wal", "seg-00000000")
	}

	cases := []struct {
		name   string
		mutate func(b2 []byte) []byte
		kind   telemetry.CorruptionKind
	}{
		{"interior truncated header", func(b2 []byte) []byte { return b2[:10] }, telemetry.CorruptionFraming},
		{"interior body length past file", func(b2 []byte) []byte {
			binary.LittleEndian.PutUint32(b2[12:16], uint32(len(b2)-spoolHeaderSize+1000))
			return b2
		}, telemetry.CorruptionFraming},
		{"unknown schema version", func(b2 []byte) []byte {
			rec := spanRecord("a2")
			rec.SchemaVersion = 99
			return walEncode(2, rec)
		}, telemetry.CorruptionVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, path := mkStream(t, tc.mutate)
			diag := &spoolDiags{}
			s, err := telemetry.OpenSpool(dir, []types.ExportDestination{walDest()}, walConfig(1<<20, 1<<20), telemetry.WithSpoolDiagnostics(diag))
			if err == nil {
				_, _ = s.Shutdown(ctx)
				t.Fatal("open succeeded over interior framing corruption")
			}
			var ce *telemetry.CorruptionError
			if !errors.As(err, &ce) {
				t.Fatalf("open err = %T(%v), want *CorruptionError", err, err)
			}
			if ce.Kind != tc.kind {
				t.Fatalf("kind = %s, want %s", ce.Kind, tc.kind)
			}
			if ce.Path != path {
				t.Fatalf("path = %s, want %s", ce.Path, path)
			}
			reports := diag.ofKind(telemetry.DiagSpoolCorrupt)
			if len(reports) != 1 {
				t.Fatalf("corruption diagnostics = %d, want exactly 1", len(reports))
			}
		})
	}
}

func TestWalDiskFullMapping(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ENOSPC mapping is asserted on Linux only")
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs("/dev/shm", &st); err != nil {
		t.Skipf("statfs /dev/shm: %v", err)
	}
	const quota = int64(256 << 10)
	reserve := quota + int64(256<<10)
	free := int64(st.Frsize) * int64(st.Bavail)
	if free < reserve+int64(1<<20) {
		t.Skipf("/dev/shm has %d bytes free, need %d to constrain the spool safely", free, reserve+int64(1<<20))
	}
	base, err := os.MkdirTemp("/dev/shm", "wal-enospc-")
	if err != nil {
		t.Skipf("mkdir in /dev/shm: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	dir := filepath.Join(base, "spool")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir spool dir: %v", err)
	}

	filler, err := os.Create(filepath.Join(base, "filler"))
	if err != nil {
		t.Skipf("create filler in /dev/shm: %v", err)
	}
	if err := syscall.Fallocate(int(filler.Fd()), 0, 0, free-reserve); err != nil {
		_ = filler.Close()
		t.Skipf("fallocate filler: %v", err)
	}
	_ = filler.Close()
	if err := syscall.Statfs("/dev/shm", &st); err != nil {
		t.Fatalf("statfs after filler: %v", err)
	}
	if left := int64(st.Frsize) * int64(st.Bavail); left > quota {
		t.Skipf("could not constrain /dev/shm below the spool quota: %d bytes still free", left)
	}

	cfg := walConfig(quota*16, 1<<20)
	s := walOpen(t, dir, nil, cfg)
	ctx := boundedCtx(t)
	appended := 0
	diskFull := false
	for i := range 6000 {
		aerr := s.Append(ctx, "wal", []types.ExportRecord{spanRecord(fmt.Sprintf("f%d", i))})
		if aerr == nil {
			appended++
			continue
		}
		if errors.Is(aerr, telemetry.ErrSpoolDiskFull) {
			diskFull = true
		} else {
			t.Fatalf("Append under space pressure = %v, want ErrSpoolDiskFull", aerr)
		}
		break
	}
	if !diskFull {
		t.Skipf("ENOSPC not triggered after %d appends within the constrained window", appended)
	}

	_ = os.Remove(filepath.Join(base, "filler"))
	_, _ = s.Shutdown(ctx)

	s2 := walOpen(t, dir, nil, cfg)
	defer func() { _, _ = s2.Shutdown(ctx) }()
	frames := readWalFrames(t, dir, "wal")
	if len(frames) != appended {
		t.Fatalf("recovered %d frames, want %d: no partial record persisted and nothing lost", len(frames), appended)
	}
	prev := uint64(0)
	for _, f := range frames {
		if f.seq <= prev {
			t.Fatalf("recovered sequence %d after %d, want strictly increasing", f.seq, prev)
		}
		prev = f.seq
	}
}

func TestWalTmpsegCrashState(t *testing.T) {
	ctx := boundedCtx(t)
	dir := t.TempDir()
	rule := telemetry.SubjectErasure{Attr: "wal.actor", Values: []string{"alice"}}
	walWriteTombstone(t, dir, rule)

	b := func(seq uint64, id string) []byte { return walEncode(seq, spanRecord(id)) }
	walWriteSeg(t, dir, "sink1", "seg-00000000",
		append(append([]byte{}, b(10, "p1")...), b(11, "p2")...))
	walWriteSeg(t, dir, "sink2", "seg-00000000",
		append(append([]byte{}, b(1, "k1")...), walEncode(2, walSubjectSpan("bad", "wal.actor", "alice", 0))...))
	walWriteSeg(t, dir, "sink2", "tmpseg-0000",
		append(append([]byte{}, b(3, "k2")...), b(4, "k3")...))

	d1 := newAcceptDest("sink1")
	d2 := newAcceptDest("sink2")
	s := walOpenDests(t, dir, nil, walConfig(1<<20, 1<<20), d1, d2)
	if _, err := os.Stat(filepath.Join(dir, "erasure.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open left the tombstone in place: %v", err)
	}
	f, err := s.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if f.Delivered["sink1"] != 2 || f.Delivered["sink2"] != 3 {
		t.Fatalf("flush delivered = %v, want sink1 2 and sink2 3", f.Delivered)
	}
	walAssertAttempts(t, [][][]string{d1.allAttempts(), d2.allAttempts()}, []string{"p1", "p2", "k1", "k2", "k3"})

	got1 := readWalFrames(t, dir, "sink1")
	if len(got1) != 0 {
		t.Fatalf("sink1 survivors not reclaimed: %v", walIDs(got1))
	}
	if _, err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestWalPermissionHardening(t *testing.T) {
	ctx := boundedCtx(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod spool dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	stream := filepath.Join(dir, "wal")
	if err := os.MkdirAll(stream, 0o755); err != nil {
		t.Fatalf("mkdir stream dir: %v", err)
	}
	segPath := filepath.Join(stream, "seg-00000000")
	if err := os.WriteFile(segPath, walEncode(1, spanRecord("a1")), 0o644); err != nil {
		t.Fatalf("write segment: %v", err)
	}
	decoy := filepath.Join(stream, "decoy")
	if err := os.WriteFile(decoy, walEncode(2, spanRecord("evil")), 0o644); err != nil {
		t.Fatalf("write decoy: %v", err)
	}
	if err := os.Symlink(decoy, filepath.Join(stream, "seg-99999999")); err != nil {
		if runtime.GOOS != "linux" {
			t.Skipf("symlinks unavailable on %s: %v", runtime.GOOS, err)
		}
		t.Fatalf("symlink segment: %v", err)
	}

	dest := newAcceptDest("wal")
	s := walOpenDests(t, dir, nil, walConfig(1<<20, 1<<20), dest)
	checks := []struct {
		path string
		want os.FileMode
	}{
		{dir, 0o700},
		{stream, 0o700},
		{segPath, 0o600},
	}
	for _, c := range checks {
		info, err := os.Stat(c.path)
		if err != nil {
			t.Fatalf("stat %s: %v", c.path, err)
		}
		if got := info.Mode().Perm(); got != c.want {
			t.Fatalf("%s mode = %o, want tightened %o", c.path, got, c.want)
		}
	}

	f, err := s.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if f.Delivered["wal"] != 1 || f.Synced != 0 {
		t.Fatalf("flush = %+v, want exactly the one recovered record delivered with nothing admitted", f)
	}
	var delivered []string
	for _, batch := range dest.allAttempts() {
		delivered = append(delivered, batch...)
	}
	if len(delivered) != 1 || delivered[0] != "a1" {
		t.Fatalf("attempts = %v, want only a1: symlinked seg must be skipped, never followed", delivered)
	}
	if err := os.Remove(filepath.Join(stream, "seg-99999999")); err == nil {
		frames := readWalFrames(t, dir, "wal")
		if len(frames) != 0 {
			t.Fatalf("acked prefix not reclaimed: %d frames left", len(frames))
		}
	}
	if h := s.Health(); h.Destinations[0].Pending != 0 {
		t.Fatalf("pending after flush = %+v, want fully drained stream", h.Destinations[0])
	}
	if _, err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
