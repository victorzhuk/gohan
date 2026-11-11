package telemetry

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

const CurrentSpoolRecordVersion = 1

const (
	DefaultSpoolAdmissionDepth = 2048
	DefaultSpoolBatchRecords   = 128
)

type RetryPolicy struct {
	MaxAttempts int
	Backoff     time.Duration
	MaxBackoff  time.Duration
}

type SpoolConfig struct {
	ByteQuota       int64
	SegmentBytes    int64
	MaxPendingAge   time.Duration
	Retry           RetryPolicy
	Workers         int
	RequestDeadline time.Duration
}

type spoolConfig struct {
	diag      types.Diagnostics
	diagGiven bool
	redactor  types.Redactor
	policy    types.RedactionPolicy
	content   ContentMapping
	admission int
}

type SpoolOption func(*spoolConfig)

func WithSpoolDiagnostics(d types.Diagnostics) SpoolOption {
	return func(c *spoolConfig) { c.diag = d; c.diagGiven = d != nil }
}

func WithSpoolRedactor(r types.Redactor, p types.RedactionPolicy) SpoolOption {
	return func(c *spoolConfig) { c.redactor = r; c.policy = p }
}

func WithSpoolContentCapture(content ContentMapping) SpoolOption {
	return func(c *spoolConfig) { c.content = content }
}

func WithSpoolAdmissionDepth(n int) SpoolOption {
	return func(c *spoolConfig) { c.admission = n }
}

const (
	spoolHeaderSize  = 20
	spoolSegmentGlob = "seg-*"
)

func OpenSpool(dir string, dests []types.ExportDestination, cfg SpoolConfig, opts ...SpoolOption) (*Spool, error) {
	sc := spoolConfig{content: ContentNone, admission: DefaultSpoolAdmissionDepth}
	for _, opt := range opts {
		opt(&sc)
	}
	if !sc.diagGiven {
		sc.diag = defaultDiagnostics()
	}
	if err := validateSpoolConfig(cfg); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(dests))
	seen := make(map[string]bool, len(dests))
	for _, dest := range dests {
		if dest == nil {
			return nil, fmt.Errorf("gohan/telemetry: OpenSpool needs a non-nil destination")
		}
		name := dest.Name()
		if name == "" {
			return nil, fmt.Errorf("gohan/telemetry: OpenSpool needs a non-empty destination name")
		}
		if seen[name] {
			return nil, fmt.Errorf("gohan/telemetry: duplicate destination %q", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("open spool dir: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open spool lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("gohan/telemetry: open spool %s: %w", dir, ErrSpoolLocked)
	}
	s := &Spool{
		dir:   dir,
		cfg:   cfg,
		sc:    sc,
		lock:  lock,
		dests: make(map[string]*spoolStream, len(names)),
		dds:   make(map[string]*destDelivery, len(names)),
	}
	destIfc := make(map[string]types.ExportDestination, len(dests))
	for _, dest := range dests {
		destIfc[dest.Name()] = dest
	}
	for _, name := range names {
		st, acked, err := s.recoverStream(name)
		if err != nil {
			s.sc.diag.Report(context.Background(), types.ExportDiagnostic{Kind: DiagSpoolCorrupt, Name: name, Err: err})
			lock.Close()
			return nil, err
		}
		s.dests[name] = st
		s.dds[name] = newDestDelivery(destIfc[name], st, acked)
	}
	for _, dd := range s.dds {
		go s.workDest(dd)
	}
	return s, nil
}

func validateSpoolConfig(cfg SpoolConfig) error {
	switch {
	case cfg.ByteQuota <= 0:
		return spoolConfigErr("ByteQuota")
	case cfg.SegmentBytes <= 0:
		return spoolConfigErr("SegmentBytes")
	case cfg.MaxPendingAge <= 0:
		return spoolConfigErr("MaxPendingAge")
	case cfg.Retry.MaxAttempts <= 0:
		return spoolConfigErr("Retry.MaxAttempts")
	case cfg.Retry.Backoff <= 0:
		return spoolConfigErr("Retry.Backoff")
	case cfg.Retry.MaxBackoff <= 0:
		return spoolConfigErr("Retry.MaxBackoff")
	case cfg.Workers <= 0:
		return spoolConfigErr("Workers")
	case cfg.RequestDeadline <= 0:
		return spoolConfigErr("RequestDeadline")
	}
	return nil
}

func spoolConfigErr(field string) error {
	return fmt.Errorf("gohan/telemetry: spool config: %s must be positive: %w", field, ErrSpoolConfig)
}

type spoolStream struct {
	name string
	dir  string

	mu   sync.Mutex
	f    *os.File
	size int64
	next int

	pending []spoolRec
}

type admittedRec struct {
	dest string
	rec  types.ExportRecord
}

type Spool struct {
	dir   string
	cfg   SpoolConfig
	sc    spoolConfig
	lock  *os.File
	dests map[string]*spoolStream
	dds   map[string]*destDelivery

	mu       sync.Mutex
	seq      uint64
	pending  int64
	admitted []admittedRec
	rejected int64
	corrupt  CorruptionReport
	shutdown bool
}

type CorruptionKind string

const (
	CorruptionTail     CorruptionKind = "incomplete_tail"
	CorruptionChecksum CorruptionKind = "checksum"
	CorruptionFraming  CorruptionKind = "framing"
	CorruptionVersion  CorruptionKind = "version"
)

type CorruptionError struct {
	Kind   CorruptionKind
	Path   string
	Offset int64
	Err    error
}

func (e *CorruptionError) Error() string {
	return fmt.Sprintf("gohan/telemetry: spool segment %s: %s corruption at offset %d: %v", e.Path, e.Kind, e.Offset, e.Err)
}

func (e *CorruptionError) Unwrap() error { return e.Err }

var (
	ErrSpoolLocked   = errors.New("gohan: spool is owned by another writer")
	ErrSpoolConfig   = errors.New("gohan: spool configuration is invalid")
	ErrSpoolQuota    = errors.New("gohan: spool byte quota exceeded")
	ErrSpoolDiskFull = errors.New("gohan: spool device is out of space")
)

const (
	DiagSpoolCorrupt  types.DiagnosticKind = "spool_corrupt"
	DiagSpoolRepaired types.DiagnosticKind = "spool_repaired"
)

type DestinationCursor struct {
	Name  string `json:"name"`
	Acked int64  `json:"acked"`
}

type DestinationHealth struct {
	Name    string            `json:"name"`
	Cursor  DestinationCursor `json:"cursor"`
	Pending int64             `json:"pending"`
	Lag     time.Duration     `json:"lag"`
	Retries int64             `json:"retries"`
	Parked  []types.RecordID  `json:"parked,omitempty"`
	Expired int64             `json:"expired"`
}

type CorruptionReport struct {
	RepairedTails int64 `json:"repaired_tails"`
	RepairedBytes int64 `json:"repaired_bytes"`
}

type SpoolHealth struct {
	PendingBytes     int64               `json:"pending_bytes"`
	AdmissionQueued  int64               `json:"admission_queued"`
	OldestPendingAge time.Duration       `json:"oldest_pending_age"`
	RejectedAppends  int64               `json:"rejected_appends"`
	Corruption       CorruptionReport    `json:"corruption"`
	Destinations     []DestinationHealth `json:"destinations"`
}

// Append is the explicit durable append: it returns nil only after the
// record data and the segment creation and rotation metadata are synced.
func (s *Spool) Append(ctx context.Context, dest string, recs []types.ExportRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return fmt.Errorf("gohan/telemetry: spool append: %w", types.ErrExportShutdown)
	}
	st, ok := s.dests[dest]
	if !ok {
		return fmt.Errorf("gohan/telemetry: spool append %q: %w", dest, types.ErrUnknownDestination)
	}
	for i := range recs {
		if recs[i].Signal != types.SignalSpan && recs[i].Signal != types.SignalMetric {
			return fmt.Errorf("gohan/telemetry: record %s: %w: %s", recs[i].ID, types.ErrExportUnsupportedSignal, recs[i].Signal)
		}
		if err := validateRecord(&recs[i]); err != nil {
			return err
		}
		dropped := s.admitOne(ctx, &recs[i])
		for _, key := range dropped {
			s.sc.diag.Report(ctx, types.ExportDiagnostic{Kind: types.DiagAdmissionRejected, Name: key})
		}
	}
	if err := s.writeRecords(ctx, st, recs); err != nil {
		s.rejected++
		return err
	}
	return nil
}

// Admit is bounded queue acceptance: no sync promise, refused with
// ErrExportQueueFull past the admission depth. Only synced records replay.
func (s *Spool) Admit(ctx context.Context, dest string, recs []types.ExportRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return fmt.Errorf("gohan/telemetry: spool admit: %w", types.ErrExportShutdown)
	}
	if _, ok := s.dests[dest]; !ok {
		return fmt.Errorf("gohan/telemetry: spool admit %q: %w", dest, types.ErrUnknownDestination)
	}
	kept := make([]types.ExportRecord, 0, len(recs))
	for i := range recs {
		if recs[i].Signal != types.SignalSpan && recs[i].Signal != types.SignalMetric {
			return fmt.Errorf("gohan/telemetry: record %s: %w: %s", recs[i].ID, types.ErrExportUnsupportedSignal, recs[i].Signal)
		}
		if err := validateRecord(&recs[i]); err != nil {
			return err
		}
		dropped := s.admitOne(ctx, &recs[i])
		for _, key := range dropped {
			s.sc.diag.Report(ctx, types.ExportDiagnostic{Kind: types.DiagAdmissionRejected, Name: key})
		}
		if hasPayload(recs[i]) {
			kept = append(kept, recs[i])
		}
	}
	if len(s.admitted)+len(kept) > s.sc.admission {
		s.rejected++
		return fmt.Errorf("gohan/telemetry: spool admit %q: %w", dest, types.ErrExportQueueFull)
	}
	for _, rec := range kept {
		s.admitted = append(s.admitted, admittedRec{dest: dest, rec: rec})
	}
	return nil
}

func (s *Spool) admitOne(ctx context.Context, rec *types.ExportRecord) []string {
	var dropped []string
	apply := func(attrs []types.Attr) []types.Attr {
		out := make([]types.Attr, 0, len(attrs))
		for _, a := range attrs {
			if isContentKey(a.Key) {
				switch s.sc.content {
				case ContentNone:
					dropped = append(dropped, a.Key)
					continue
				case ContentHashes:
					a.Value = hashAttr(a.Value)
				case ContentRedacted:
					a.Value = "[redacted]"
				}
			}
			out = append(out, a)
		}
		return out
	}
	if rec.Span != nil {
		attrs := apply(rec.Span.Attrs)
		rec.Span.Attrs, _ = s.redact(ctx, attrs)
	}
	if rec.Metric != nil {
		dims := apply(rec.Metric.Dimensions)
		redacted, _ := s.redact(ctx, dims)
		rec.Metric.Dimensions = dropForbiddenDimensions(redacted)
	}
	return dropped
}

func (s *Spool) redact(ctx context.Context, attrs []types.Attr) ([]types.Attr, []string) {
	if s.sc.redactor == nil {
		return attrs, nil
	}
	out := make([]types.Attr, 0, len(attrs))
	var lost []string
	for _, a := range attrs {
		v, ok := a.Value.(string)
		if !ok {
			out = append(out, a)
			continue
		}
		blocks, _, err := s.sc.redactor.Redact(ctx, []types.Block{types.Text{Text: v}}, s.sc.policy)
		var t types.Text
		var isText bool
		if len(blocks) > 0 {
			t, isText = blocks[0].(types.Text)
		}
		if err != nil || len(blocks) == 0 || !isText {
			if err == nil {
				err = errors.New("gohan/telemetry: redaction returned no usable result")
			}
			s.sc.diag.Report(ctx, types.ExportDiagnostic{Kind: types.DiagAdmissionRejected, Name: a.Key, Err: err})
			lost = append(lost, a.Key)
			continue
		}
		a.Value = t.Text
		out = append(out, a)
	}
	return out, lost
}

// writeRecords frames and persists recs into the destination stream under
// the spool lock. The quota check runs before any byte is written, so a
// refusal leaves the stream unchanged; a failed write rolls the stream
// back to its pre-call boundary.
func (s *Spool) writeRecords(ctx context.Context, st *spoolStream, recs []types.ExportRecord) error {
	bodies := make([][]byte, len(recs))
	var total int64
	for i, rec := range recs {
		body, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("gohan/telemetry: spool %s: marshal record: %w", st.name, err)
		}
		bodies[i] = body
		total += int64(spoolHeaderSize + len(body))
	}
	if s.pending+total > s.cfg.ByteQuota {
		return fmt.Errorf("gohan/telemetry: spool append %q: %w", st.name, ErrSpoolQuota)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	boundary := st.size
	created := false
	for i, body := range bodies {
		framed := int64(spoolHeaderSize + len(body))
		if st.f != nil && st.size > 0 && st.size+framed > s.cfg.SegmentBytes {
			if err := st.roll(); err != nil {
				return st.rollback(boundary, err)
			}
			created = true
		}
		if st.f == nil {
			if err := st.openSegment(); err != nil {
				return st.rollback(boundary, err)
			}
			created = true
		}
		s.seq++
		frame := spoolFrame(s.seq, body)
		if _, err := st.f.Write(frame); err != nil {
			if errors.Is(err, syscall.ENOSPC) {
				err = fmt.Errorf("gohan/telemetry: spool append %q: %w: %w", st.name, ErrSpoolDiskFull, err)
			}
			return st.rollback(boundary, err)
		}
		st.size += framed
		st.pending = append(st.pending, spoolRec{seq: s.seq, rec: recs[i], bytes: framed, at: time.Now()})
	}
	if err := st.f.Sync(); err != nil {
		return st.rollback(boundary, err)
	}
	if created {
		if err := syncDir(st.dir); err != nil {
			return st.rollback(boundary, err)
		}
	}
	s.pending += total
	return nil
}

func spoolFrame(seq uint64, body []byte) []byte {
	buf := make([]byte, spoolHeaderSize+len(body))
	binary.LittleEndian.PutUint32(buf[0:4], CurrentSpoolRecordVersion)
	binary.LittleEndian.PutUint64(buf[4:12], seq)
	binary.LittleEndian.PutUint32(buf[12:16], uint32(len(body)))
	binary.LittleEndian.PutUint32(buf[16:20], crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli)))
	copy(buf[spoolHeaderSize:], body)
	return buf
}

func (st *spoolStream) openSegment() error {
	path := filepath.Join(st.dir, fmt.Sprintf("seg-%08d", st.next))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("open segment: %w", err)
	}
	st.f = f
	st.size = 0
	return nil
}

func (st *spoolStream) roll() error {
	st.f.Close()
	st.f = nil
	st.next++
	return st.openSegment()
}

// rollback truncates the stream back to boundary after a failed write,
// counting the discarded frame as a repaired tail. The caller returns the
// write error; the rollback error only overrides it if truncation failed.
func (st *spoolStream) rollback(boundary int64, werr error) error {
	if st.size == boundary {
		return werr
	}
	st.size = boundary
	if err := st.f.Truncate(boundary); err != nil {
		return fmt.Errorf("gohan/telemetry: spool rollback: %v (write error: %w)", err, werr)
	}
	if err := st.f.Sync(); err != nil {
		return fmt.Errorf("gohan/telemetry: spool rollback: %v (write error: %w)", err, werr)
	}
	return werr
}

// Health snapshots the spool locally; it never enqueues records.
func (s *Spool) Health() SpoolHealth {
	s.mu.Lock()
	h := SpoolHealth{
		PendingBytes:    s.pending,
		AdmissionQueued: int64(len(s.admitted)),
		RejectedAppends: s.rejected,
		Corruption:      s.corrupt,
		Destinations:    make([]DestinationHealth, 0, len(s.dests)),
	}
	s.mu.Unlock()
	var oldest time.Time
	for _, name := range slices.Sorted(maps.Keys(s.dests)) {
		dd := s.dds[name]
		dh := DestinationHealth{Name: name}
		if dd != nil {
			dd.mu.Lock()
			dh.Cursor = DestinationCursor{Name: name, Acked: dd.acked}
			dh.Retries = dd.retries
			dh.Parked = sortedIDs(dd.parked)
			dh.Expired = int64(len(dd.expired))
			dd.mu.Unlock()
		}
		st := s.dests[name]
		st.mu.Lock()
		dh.Pending = int64(len(st.pending))
		for _, r := range st.pending {
			if oldest.IsZero() || r.at.Before(oldest) {
				oldest = r.at
			}
			if age := time.Since(r.at); age > dh.Lag {
				dh.Lag = age
			}
		}
		st.mu.Unlock()
		h.Destinations = append(h.Destinations, dh)
	}
	if !oldest.IsZero() {
		h.OldestPendingAge = time.Since(oldest)
	}
	return h
}

// recoverStream replays the destination's segment files, verifies every
// record, repairs an incomplete final record and fails open on any
// interior corruption. Pending records are the sequence range above the
// durable cursor.
func (s *Spool) recoverStream(name string) (*spoolStream, int64, error) {
	dir := filepath.Join(s.dir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, 0, fmt.Errorf("open stream dir: %w", err)
	}
	acked, _ := loadCursor(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, fmt.Errorf("read stream dir: %w", err)
	}
	var segs []string
	for _, e := range entries {
		if e.IsDir() || (!strings.HasPrefix(e.Name(), "seg-") && !strings.HasPrefix(e.Name(), "tmpseg-")) {
			continue
		}
		segs = append(segs, e.Name())
	}
	sort.Strings(segs)
	st := &spoolStream{name: name, dir: dir, next: len(segs)}
	var recs []spoolRec
	for i, seg := range segs {
		path := filepath.Join(dir, seg)
		at, serr := os.Stat(path)
		if serr != nil {
			return nil, 0, fmt.Errorf("stat segment: %w", serr)
		}
		_, maxSeq, repaired, segRecs, err := scanSegment(path, i == len(segs)-1)
		if repaired > 0 {
			s.corrupt.RepairedTails++
			s.corrupt.RepairedBytes += repaired
			s.sc.diag.Report(context.Background(), types.ExportDiagnostic{Kind: DiagSpoolRepaired, Name: name, Dropped: repaired})
		}
		if err != nil {
			return nil, 0, err
		}
		for _, r := range segRecs {
			r.at = at.ModTime()
			recs = append(recs, r)
		}
		if maxSeq > s.seq {
			s.seq = maxSeq
		}
	}
	slices.SortFunc(recs, func(a, b spoolRec) int { return cmp.Compare(a.seq, b.seq) })
	var bytes int64
	for _, r := range recs {
		if int64(r.seq) > acked {
			st.pending = append(st.pending, r)
			bytes += r.bytes
		}
	}
	s.pending += bytes
	return st, acked, nil
}

// scanSegment verifies one segment file. A truncated final record is
// repaired in place and its lost byte count returned; any interior
// checksum, framing or version failure fails the open with a
// *CorruptionError and is never skipped.
func scanSegment(path string, final bool) (bytes int64, maxSeq uint64, repaired int64, recs []spoolRec, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, 0, 0, nil, fmt.Errorf("open segment: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, 0, 0, nil, fmt.Errorf("stat segment: %w", err)
	}
	size := info.Size()
	table := crc32.MakeTable(crc32.Castagnoli)
	offset := int64(0)
	for {
		if offset == size {
			return offset, maxSeq, 0, recs, nil
		}
		header := make([]byte, spoolHeaderSize)
		n, rerr := f.ReadAt(header, offset)
		if n < spoolHeaderSize {
			if final {
				lost, terr := repairTail(f, offset, size)
				return offset, maxSeq, lost, recs, terr
			}
			return 0, 0, 0, nil, corruptErr(CorruptionFraming, path, offset, rerr)
		}
		if rerr != nil {
			return 0, 0, 0, nil, rerr
		}
		version := binary.LittleEndian.Uint32(header[0:4])
		seq := binary.LittleEndian.Uint64(header[4:12])
		bodyLen := int64(binary.LittleEndian.Uint32(header[12:16]))
		crc := binary.LittleEndian.Uint32(header[16:20])
		if version != CurrentSpoolRecordVersion {
			return 0, 0, 0, nil, corruptErr(CorruptionVersion, path, offset, fmt.Errorf("envelope version %d, want %d", version, CurrentSpoolRecordVersion))
		}
		body := make([]byte, bodyLen)
		n, rerr = f.ReadAt(body, offset+spoolHeaderSize)
		if int64(n) < bodyLen {
			if final {
				lost, terr := repairTail(f, offset, size)
				return offset, maxSeq, lost, recs, terr
			}
			return 0, 0, 0, nil, corruptErr(CorruptionFraming, path, offset, fmt.Errorf("body length %d exceeds segment size", bodyLen))
		}
		if rerr != nil {
			return 0, 0, 0, nil, rerr
		}
		if crc32.Checksum(body, table) != crc {
			return 0, 0, 0, nil, corruptErr(CorruptionChecksum, path, offset, fmt.Errorf("crc %08x mismatch", crc))
		}
		var rec types.ExportRecord
		if uerr := json.Unmarshal(body, &rec); uerr != nil {
			return 0, 0, 0, nil, corruptErr(CorruptionFraming, path, offset, fmt.Errorf("decode body: %w", uerr))
		}
		if rec.SchemaVersion != types.CurrentExportSchemaVersion {
			return 0, 0, 0, nil, corruptErr(CorruptionVersion, path, offset, fmt.Errorf("schema version %d, want %d", rec.SchemaVersion, types.CurrentExportSchemaVersion))
		}
		if seq > maxSeq {
			maxSeq = seq
		}
		recs = append(recs, spoolRec{seq: seq, rec: rec, bytes: spoolHeaderSize + bodyLen})
		offset += spoolHeaderSize + bodyLen
	}
}

func repairTail(f *os.File, offset, size int64) (int64, error) {
	lost := size - offset
	if err := f.Truncate(offset); err != nil {
		return 0, fmt.Errorf("repair tail: %w", err)
	}
	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("repair tail: %w", err)
	}
	return lost, nil
}

func corruptErr(kind CorruptionKind, path string, offset int64, err error) error {
	return &CorruptionError{Kind: kind, Path: path, Offset: offset, Err: err}
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open stream dir: %w", err)
	}
	defer d.Close()
	if err := syscall.Fsync(int(d.Fd())); err != nil {
		return fmt.Errorf("sync stream dir: %w", err)
	}
	return nil
}
