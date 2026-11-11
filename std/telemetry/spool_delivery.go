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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type SpoolFlush struct {
	Synced      int64                `json:"synced"`
	Delivered   map[string]int64     `json:"delivered,omitempty"`
	Undelivered types.UnresolvedWork `json:"undelivered"`
}

type BacklogDisposition string

const (
	BacklogDiscard BacklogDisposition = "discard"
	BacklogRetain  BacklogDisposition = "retain"
)

// spoolRec is one pending record in a destination stream: the wire record,
// its durable sequence number and the frame size it occupies on disk.
type spoolRec struct {
	seq   uint64
	rec   types.ExportRecord
	bytes int64
	at    time.Time
}

// destDelivery is the delivery state of one destination stream. The worker
// goroutine owns a pass at a time; Flush and Shutdown request passes by
// bumping drainSeq.
type destDelivery struct {
	dest types.ExportDestination
	st   *spoolStream

	mu      sync.Mutex
	cond    *sync.Cond
	acked   int64
	parked  map[types.RecordID]bool
	expired map[types.RecordID]bool
	retries int64
	headAtt int

	drainSeq      int
	lastSeq       int
	drainDone     bool
	drainCx       context.Context
	passDelivered int64
	stopped       bool
	done          chan struct{}
}

func newDestDelivery(dest types.ExportDestination, st *spoolStream, acked int64) *destDelivery {
	dd := &destDelivery{
		dest:    dest,
		st:      st,
		acked:   acked,
		parked:  make(map[types.RecordID]bool),
		expired: make(map[types.RecordID]bool),
		done:    make(chan struct{}),
	}
	dd.cond = sync.NewCond(&dd.mu)
	return dd
}

func (s *Spool) report(ctx context.Context, d types.ExportDiagnostic) {
	s.sc.diag.Report(ctx, d)
}

// workDest is the one delivery worker per destination. Delivery passes run
// only for an explicit Flush or Shutdown drain, always under the drainer's
// context so a blocked destination cannot hang the spool.
func (s *Spool) workDest(dd *destDelivery) {
	defer close(dd.done)
	for {
		dd.mu.Lock()
		for dd.lastSeq == dd.drainSeq && !dd.stopped {
			dd.cond.Wait()
		}
		if dd.stopped {
			dd.mu.Unlock()
			return
		}
		dd.lastSeq = dd.drainSeq
		cx := dd.drainCx
		dd.passDelivered = 0
		dd.mu.Unlock()
		if cx == nil {
			cx = context.Background()
		}
		s.deliverPass(cx, dd)
		dd.mu.Lock()
		dd.drainCx = nil
		dd.drainDone = true
		dd.cond.Broadcast()
		dd.mu.Unlock()
	}
}

func (dd *destDelivery) head() (spoolRec, bool) {
	dd.st.mu.Lock()
	defer dd.st.mu.Unlock()
	if len(dd.st.pending) == 0 {
		return spoolRec{}, false
	}
	return dd.st.pending[0], true
}

// deliverPass runs one bounded delivery pass: batches in strict sequence
// order, the outcome state machine per record, retry with exponential
// backoff within the pass, then cursor persistence before reclaim.
func (s *Spool) deliverPass(ctx context.Context, dd *destDelivery) {
	var delivered int64
	for {
		dd.mu.Lock()
		head, ok := dd.head()
		if !ok || dd.parked[head.rec.ID] || dd.expired[head.rec.ID] {
			dd.mu.Unlock()
			break
		}
		if time.Since(head.at) > s.cfg.MaxPendingAge {
			dd.expired[head.rec.ID] = true
			dd.mu.Unlock()
			break
		}
		n := min(len(dd.st.pending), DefaultSpoolBatchRecords)
		batch := append([]spoolRec(nil), dd.st.pending[:n]...)
		if m := s.firstErasureMatch(batch); m >= 0 {
			batch = batch[:m]
		}
		dd.mu.Unlock()
		if len(batch) == 0 {
			break
		}

		actx, cancel := context.WithTimeout(ctx, s.cfg.RequestDeadline)
		outcomes, xerr := dd.dest.Export(actx, spoolRecBodies(batch))
		cancel()

		blocked, retryable, nd := s.applyOutcomes(ctx, dd, batch, outcomes, xerr)
		delivered += nd
		if !blocked || ctx.Err() != nil || !retryable {
			break
		}
		dd.mu.Lock()
		if nd > 0 {
			dd.headAtt = 0
		}
		dd.headAtt++
		exhausted := nd == 0 && dd.headAtt >= s.cfg.Retry.MaxAttempts
		if exhausted {
			dd.parked[head.rec.ID] = true
		}
		wait := s.backoffFor(dd.headAtt)
		dd.mu.Unlock()
		if exhausted {
			s.report(ctx, types.ExportDiagnostic{Kind: types.DiagDeliveryDropped, Name: dd.st.name, Dropped: 1,
				Err: fmt.Errorf("gohan/telemetry: spool %s: record %s: retry budget exhausted", dd.st.name, head.rec.ID)})
			break
		}
		if sleepCtx(ctx, wait) {
			break
		}
	}
	if delivered > 0 {
		if err := dd.persistCursor(); err != nil {
			s.report(ctx, types.ExportDiagnostic{Kind: DiagSpoolCorrupt, Name: dd.st.name, Err: err})
		} else {
			s.reclaim(dd)
		}
	}
	dd.mu.Lock()
	dd.passDelivered += delivered
	dd.mu.Unlock()
}

// applyOutcomes maps one Export result onto the durable outcome machine.
// The cursor advances only past a contiguous accepted prefix; a permanent
// rejection parks the stream at that record, an ambiguous one retains the
// record for a duplicating replay, and everything else retries.
func (s *Spool) applyOutcomes(ctx context.Context, dd *destDelivery, batch []spoolRec, outcomes []types.RecordOutcome, xerr error) (blocked, retryable bool, delivered int64) {
	byID := make(map[types.RecordID]types.RecordOutcome, len(outcomes))
	for _, o := range outcomes {
		byID[o.ID] = o
	}
	dd.mu.Lock()
	defer dd.mu.Unlock()
	for _, r := range batch {
		o, ok := byID[r.rec.ID]
		switch {
		case ok && o.Status == types.OutcomeAccepted:
			dd.acked = int64(r.seq)
			delivered++
		case ok && o.Status == types.OutcomePermanent:
			dd.parked[r.rec.ID] = true
			s.report(ctx, types.ExportDiagnostic{Kind: types.DiagDeliveryDropped, Name: dd.st.name, Err: messageErr(o.Message)})
			return true, false, delivered
		case ok && o.Status == types.OutcomeAmbiguous:
			s.report(ctx, types.ExportDiagnostic{Kind: types.DiagDeliveryAmbiguous, Name: dd.st.name})
			return true, false, delivered
		default:
			dd.retries++
			return true, true, delivered
		}
	}
	return false, false, delivered
}

func (s *Spool) backoffFor(att int) time.Duration {
	b := s.cfg.Retry.Backoff
	for i := 1; i < att && b < s.cfg.Retry.MaxBackoff; i++ {
		b *= 2
	}
	return min(b, s.cfg.Retry.MaxBackoff)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if err := ctx.Err(); err != nil {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}

func spoolRecBodies(batch []spoolRec) []types.ExportRecord {
	recs := make([]types.ExportRecord, 0, len(batch))
	for _, r := range batch {
		recs = append(recs, r.rec)
	}
	return recs
}

// persistCursor fsyncs the destination cursor. It runs before any
// acknowledged prefix is reclaimed, so a crash past remote acceptance can
// only replay the record, never lose it.
func (dd *destDelivery) persistCursor() error {
	dd.mu.Lock()
	acked := dd.acked
	dd.mu.Unlock()
	data, err := json.Marshal(DestinationCursor{Name: dd.st.name, Acked: acked})
	if err != nil {
		return fmt.Errorf("gohan/telemetry: spool %s: marshal cursor: %w", dd.st.name, err)
	}
	tmp := filepath.Join(dd.st.dir, "cursor.json.tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("gohan/telemetry: spool %s: write cursor: %w", dd.st.name, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("gohan/telemetry: spool %s: write cursor: %w", dd.st.name, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("gohan/telemetry: spool %s: sync cursor: %w", dd.st.name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("gohan/telemetry: spool %s: close cursor: %w", dd.st.name, err)
	}
	if err := os.Rename(tmp, filepath.Join(dd.st.dir, "cursor.json")); err != nil {
		return fmt.Errorf("gohan/telemetry: spool %s: persist cursor: %w", dd.st.name, err)
	}
	if err := syncDir(dd.st.dir); err != nil {
		return err
	}
	return nil
}

func loadCursor(dir string) (int64, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "cursor.json"))
	if err != nil {
		return 0, false
	}
	var c DestinationCursor
	if err := json.Unmarshal(data, &c); err != nil {
		return 0, false
	}
	return c.Acked, true
}

// reclaim drops the acknowledged prefix from the stream: pending list and
// segment files. The cursor is already durable at this point.
func (s *Spool) reclaim(dd *destDelivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := dd.st
	st.mu.Lock()
	drop := 0
	for drop < len(st.pending) && int64(st.pending[drop].seq) <= dd.acked {
		drop++
	}
	if drop == 0 {
		st.mu.Unlock()
		return
	}
	var bytes int64
	for _, r := range st.pending[:drop] {
		bytes += r.bytes
	}
	kept := append([]spoolRec(nil), st.pending[drop:]...)
	st.pending = kept
	err := s.compactStream(st, kept)
	st.mu.Unlock()
	s.pending -= bytes
	if err != nil {
		s.report(context.Background(), types.ExportDiagnostic{Kind: DiagSpoolCorrupt, Name: st.name, Err: err})
	}
}

// compactStream rewrites the stream to exactly kept records. Temp files are
// fsynced and dir-synced before the originals are unlinked, so a crash
// mid-compaction leaves a recoverable union of old and temp segments.
// Callers hold st.mu.
func (s *Spool) compactStream(st *spoolStream, kept []spoolRec) error {
	entries, err := os.ReadDir(st.dir)
	if err != nil {
		return fmt.Errorf("gohan/telemetry: spool %s: read stream dir: %w", st.name, err)
	}
	var old []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "seg-") {
			old = append(old, e.Name())
		}
	}
	var cur *os.File
	var curSize int64
	count := 0
	closeCur := func() error {
		if cur == nil {
			return nil
		}
		if err := cur.Sync(); err != nil {
			cur.Close()
			return fmt.Errorf("gohan/telemetry: spool %s: sync compacted segment: %w", st.name, err)
		}
		if err := cur.Close(); err != nil {
			return fmt.Errorf("gohan/telemetry: spool %s: close compacted segment: %w", st.name, err)
		}
		cur = nil
		return nil
	}
	for _, r := range kept {
		body, merr := json.Marshal(r.rec)
		if merr != nil {
			closeCur()
			return fmt.Errorf("gohan/telemetry: spool %s: marshal record: %w", st.name, merr)
		}
		frame := spoolFrame(r.seq, body)
		framed := int64(len(frame))
		if cur != nil && curSize > 0 && curSize+framed > s.cfg.SegmentBytes {
			if err := closeCur(); err != nil {
				return err
			}
		}
		if cur == nil {
			path := filepath.Join(st.dir, fmt.Sprintf("tmpseg-%04d", count))
			f, oerr := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if oerr != nil {
				closeCur()
				return fmt.Errorf("gohan/telemetry: spool %s: open compacted segment: %w", st.name, oerr)
			}
			cur = f
			curSize = 0
			count++
		}
		if _, werr := cur.Write(frame); werr != nil {
			closeCur()
			return fmt.Errorf("gohan/telemetry: spool %s: write compacted segment: %w", st.name, werr)
		}
		curSize += framed
	}
	if err := closeCur(); err != nil {
		return err
	}
	if len(old) == 0 && count == 0 {
		return nil
	}
	for _, name := range old {
		if err := os.Remove(filepath.Join(st.dir, name)); err != nil {
			return fmt.Errorf("gohan/telemetry: spool %s: remove retired segment: %w", st.name, err)
		}
	}
	for i := range count {
		if err := os.Rename(filepath.Join(st.dir, fmt.Sprintf("tmpseg-%04d", i)), filepath.Join(st.dir, fmt.Sprintf("seg-%08d", i))); err != nil {
			return fmt.Errorf("gohan/telemetry: spool %s: promote compacted segment: %w", st.name, err)
		}
	}
	if err := syncDir(st.dir); err != nil {
		return err
	}
	if st.f != nil {
		st.f.Close()
		st.f = nil
	}
	st.size = 0
	if count > 0 {
		path := filepath.Join(st.dir, fmt.Sprintf("seg-%08d", count-1))
		info, serr := os.Stat(path)
		if serr != nil {
			return fmt.Errorf("gohan/telemetry: spool %s: stat compacted segment: %w", st.name, serr)
		}
		f, oerr := os.OpenFile(path, os.O_WRONLY, 0)
		if oerr != nil {
			return fmt.Errorf("gohan/telemetry: spool %s: reopen compacted segment: %w", st.name, oerr)
		}
		st.f = f
		st.size = info.Size()
	}
	st.next = count
	return nil
}

// drainWithin requests one delivery pass and waits for it, allowing a
// grace window past ctx for the in-flight Export to land.
func (dd *destDelivery) drainWithin(ctx context.Context) (delivered int64, unresolved int64) {
	finished := make(chan struct{})
	var grace atomic.Bool
	go func() {
		select {
		case <-ctx.Done():
			t := time.AfterFunc(graceWindow, func() {
				grace.Store(true)
				dd.mu.Lock()
				dd.cond.Broadcast()
				dd.mu.Unlock()
			})
			select {
			case <-t.C:
			case <-finished:
				t.Stop()
			}
		case <-finished:
		}
	}()
	defer close(finished)
	dd.mu.Lock()
	dd.drainSeq++
	dd.drainCx = ctx
	dd.cond.Broadcast()
	for !dd.drainDone && !grace.Load() {
		dd.cond.Wait()
	}
	dd.st.mu.Lock()
	unresolved = int64(len(dd.st.pending))
	dd.st.mu.Unlock()
	delivered = dd.passDelivered
	dd.mu.Unlock()
	return delivered, unresolved
}

func (s *Spool) drainAll(ctx context.Context) (delivered map[string]int64, unresolved map[string]int64) {
	delivered = make(map[string]int64, len(s.dds))
	unresolved = make(map[string]int64, len(s.dds))
	for name, dd := range s.dds {
		d, u := dd.drainWithin(ctx)
		delivered[name] = d
		unresolved[name] = u
	}
	return delivered, unresolved
}

// syncAdmitted writes queued records into their streams under the spool
// lock and returns the count made durable plus per-destination failures.
func (s *Spool) syncAdmitted(ctx context.Context) (synced int64, failed map[string]int64, errs []error) {
	if len(s.admitted) == 0 {
		return 0, nil, nil
	}
	byDest := make(map[string][]types.ExportRecord, len(s.dests))
	for _, a := range s.admitted {
		byDest[a.dest] = append(byDest[a.dest], a.rec)
	}
	s.admitted = nil
	for dest, recs := range byDest {
		if err := s.writeRecords(ctx, s.dests[dest], recs); err != nil {
			s.rejected++
			if failed == nil {
				failed = make(map[string]int64, len(s.dests))
			}
			failed[dest] += int64(len(recs))
			errs = append(errs, err)
			continue
		}
		synced += int64(len(recs))
	}
	return synced, failed, errs
}

// Flush syncs admitted records, waits for one bounded delivery pass within
// ctx and reports synced, delivered and undelivered work separately.
func (s *Spool) Flush(ctx context.Context) (SpoolFlush, error) {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return SpoolFlush{}, nil
	}
	synced, failed, errs := s.syncAdmitted(ctx)
	s.mu.Unlock()

	delivered, unresolved := s.drainAll(ctx)
	s.closeCycle(ctx, false)
	var undelivered types.UnresolvedWork
	for dest, n := range unresolved {
		if f := failed[dest]; f > 0 {
			n += f
		}
		if n == 0 {
			continue
		}
		undelivered.Records += n
		if undelivered.ByDestination == nil {
			undelivered.ByDestination = make(map[string]int64, len(unresolved))
		}
		undelivered.ByDestination[dest] = n
	}
	err := errors.Join(errs...)
	if ctx.Err() != nil {
		err = errors.Join(err, fmt.Errorf("gohan/telemetry: spool flush: %w", ctx.Err()))
	}
	del := make(map[string]int64, len(delivered))
	for dest, n := range delivered {
		if n > 0 {
			del[dest] = n
		}
	}
	return SpoolFlush{Synced: synced, Delivered: del, Undelivered: undelivered}, err
}

func (s *Spool) closeCycle(ctx context.Context, shutdown bool) {
	ender, ok := s.sc.diag.(cycleEnder)
	if !ok {
		return
	}
	if shutdown {
		ender.Shutdown(ctx)
		return
	}
	ender.Flush(ctx)
}

// Shutdown runs one final bounded drain, persists cursors, stops workers,
// releases the exclusive lock and is idempotent. Undelivered records stay
// durable for the next open.
func (s *Spool) Shutdown(ctx context.Context) (types.UnresolvedWork, error) {
	s.mu.Lock()
	already := s.shutdown
	s.shutdown = true
	var work types.UnresolvedWork
	var errs []error
	if !already {
		_, failed, syncErrs := s.syncAdmitted(ctx)
		errs = append(errs, syncErrs...)
		work = workOf(failed)
		s.mu.Unlock()
		_, unresolved := s.drainAll(ctx)
		for dest, n := range unresolved {
			if n > 0 {
				work.Records += n
				if work.ByDestination == nil {
					work.ByDestination = make(map[string]int64, len(unresolved))
				}
				work.ByDestination[dest] += n
			}
		}
	} else {
		s.mu.Unlock()
	}
	if !already {
		var stopErrs []error
		for _, dd := range s.dds {
			if err := dd.persistCursor(); err != nil {
				stopErrs = append(stopErrs, err)
			}
		}
		errs = append(errs, stopErrs...)
	}
	for _, dd := range s.dds {
		dd.mu.Lock()
		dd.stopped = true
		dd.cond.Broadcast()
		dd.mu.Unlock()
		<-dd.done
	}
	s.closeCycle(ctx, true)
	syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	if err := s.lock.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close spool lock: %w", err))
	}
	if already {
		return types.UnresolvedWork{}, nil
	}
	err := errors.Join(errs...)
	if ctx.Err() != nil {
		err = errors.Join(err, fmt.Errorf("gohan/telemetry: spool shutdown: %w", ctx.Err()))
	}
	return work, err
}

// Discard removes named pending records from dest through a synced
// compaction and returns how many were removed. It is the required
// disposition for parked and expired records.
func (s *Spool) Discard(ctx context.Context, dest string, ids ...types.RecordID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return 0, fmt.Errorf("gohan/telemetry: spool discard: %w", types.ErrExportShutdown)
	}
	st, ok := s.dests[dest]
	if !ok {
		return 0, fmt.Errorf("gohan/telemetry: spool discard %q: %w", dest, types.ErrUnknownDestination)
	}
	dd := s.dds[dest]
	idset := make(map[types.RecordID]bool, len(ids))
	for _, id := range ids {
		idset[id] = true
	}
	st.mu.Lock()
	var removed int64
	var bytes int64
	kept := make([]spoolRec, 0, len(st.pending))
	for _, r := range st.pending {
		if idset[r.rec.ID] {
			removed++
			bytes += r.bytes
			continue
		}
		kept = append(kept, r)
	}
	if removed == 0 {
		st.mu.Unlock()
		return 0, nil
	}
	st.pending = kept
	err := s.compactStream(st, kept)
	st.mu.Unlock()
	dd.mu.Lock()
	for id := range idset {
		delete(dd.parked, id)
		delete(dd.expired, id)
	}
	dd.mu.Unlock()
	s.pending -= bytes
	if err != nil {
		return removed, err
	}
	return removed, nil
}

// RemoveDestination deregisters dest after an explicit disposition of its
// backlog. Retained backlogs stay durable and quota-counted and resume when
// the same name is opened again.
func (s *Spool) RemoveDestination(ctx context.Context, dest string, disposition BacklogDisposition) error {
	dd, ok := s.dds[dest]
	if !ok {
		return fmt.Errorf("gohan/telemetry: spool remove %q: %w", dest, types.ErrUnknownDestination)
	}
	dd.mu.Lock()
	dd.stopped = true
	dd.cond.Broadcast()
	dd.mu.Unlock()
	<-dd.done

	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.dests[dest]
	var bytes int64
	st.mu.Lock()
	for _, r := range st.pending {
		bytes += r.bytes
	}
	st.mu.Unlock()
	delete(s.dests, dest)
	delete(s.dds, dest)
	switch disposition {
	case BacklogDiscard:
		s.pending -= bytes
		if err := os.RemoveAll(st.dir); err != nil {
			return fmt.Errorf("gohan/telemetry: spool remove %q: %w", dest, err)
		}
	case BacklogRetain:
	default:
		return fmt.Errorf("gohan/telemetry: spool remove %q: unknown backlog disposition %q", dest, disposition)
	}
	return nil
}

// SpoolSink adapts a Spool to RecordSink for adapter/otel: Ingest admits
// with bounded queue acceptance, Flush syncs and drains, Shutdown shuts
// down. The bridge promises bounded admission only.
func SpoolSink(s *Spool) types.RecordSink {
	return spoolSink{s: s}
}

type spoolSink struct{ s *Spool }

func (p spoolSink) Ingest(ctx context.Context, dest string, recs []types.ExportRecord) error {
	return p.s.Admit(ctx, dest, recs)
}

func (p spoolSink) Flush(ctx context.Context) (types.UnresolvedWork, error) {
	f, err := p.s.Flush(ctx)
	return f.Undelivered, err
}

func (p spoolSink) Shutdown(ctx context.Context) (types.UnresolvedWork, error) {
	return p.s.Shutdown(ctx)
}

var _ types.RecordSink = spoolSink{}

func sortedIDs(m map[types.RecordID]bool) []types.RecordID {
	return slices.Sorted(maps.Keys(m))
}
