package telemetry_test

import (
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/telemetry"
)

func walKeepSpan(id string) types.ExportRecord {
	return spanRecord(id)
}

// walSubjectSpan carries the subject attribute in resource attrs (where 0)
// or span attrs (where 1).
func walSubjectSpan(id, attr, val string, where int) types.ExportRecord {
	rec := spanRecord(id)
	if where == 0 {
		rec.Resource.Attrs = []types.Attr{{Key: attr, Value: val}}
		return rec
	}
	rec.Span.Attrs = append(rec.Span.Attrs, types.Attr{Key: attr, Value: val})
	return rec
}

func walSubjectMetric(id, attr, val string) types.ExportRecord {
	n := int64(1)
	return types.ExportRecord{
		SchemaVersion: types.CurrentExportSchemaVersion,
		ID:            types.RecordID(id),
		Resource:      types.ResourceIdentity{Name: "gohan"},
		Signal:        types.SignalMetric,
		Metric: &types.MetricRecord{
			Instrument:  types.Instrument{Name: "calls", Kind: types.InstrumentCounter},
			Source:      types.Source{Name: "gohan"},
			Temporality: types.TemporalityCumulative,
			Dimensions:  []types.Attr{{Key: attr, Value: val}},
			Value:       types.MetricValue{Int: &n},
		},
	}
}

func walJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// walEncode frames one record exactly as the storage engine does.
func walEncode(seq uint64, rec types.ExportRecord) []byte {
	body := walJSON(rec)
	buf := make([]byte, spoolHeaderSize+len(body))
	binary.LittleEndian.PutUint32(buf[0:4], telemetry.CurrentSpoolRecordVersion)
	binary.LittleEndian.PutUint64(buf[4:12], seq)
	binary.LittleEndian.PutUint32(buf[12:16], uint32(len(body)))
	binary.LittleEndian.PutUint32(buf[16:20], crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli)))
	copy(buf[spoolHeaderSize:], body)
	return buf
}

func walWriteTombstone(t *testing.T, dir string, rule telemetry.SubjectErasure) {
	t.Helper()
	tmp := filepath.Join(dir, "erasure.json.tmp")
	if err := os.WriteFile(tmp, walJSON(rule), 0o600); err != nil {
		t.Fatalf("write tombstone: %v", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "erasure.json")); err != nil {
		t.Fatalf("persist tombstone: %v", err)
	}
}

// walDropFrames rewrites the destination stream without the named records,
// preserving sequences: the on-disk state of a stream already compacted by
// an erasure that crashed before finishing.
func walDropFrames(t *testing.T, dir, dest string, drop map[string]bool) {
	t.Helper()
	var buf []byte
	for _, f := range readWalFrames(t, dir, dest) {
		if drop[string(f.rec.ID)] {
			continue
		}
		buf = append(buf, walEncode(f.seq, f.rec)...)
	}
	for _, seg := range walSegments(t, dir, dest) {
		if err := os.Remove(filepath.Join(dir, dest, seg)); err != nil {
			t.Fatalf("remove segment: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, dest, "seg-00000000"), buf, 0o600); err != nil {
		t.Fatalf("rewrite segment: %v", err)
	}
}

// walAssertAttempts checks that no delivery attempt ever carried a record
// outside the allowed survivors.
func walAssertAttempts(t *testing.T, attempts [][][]string, allowed []string) {
	t.Helper()
	ok := map[string]bool{}
	for _, id := range allowed {
		ok[id] = true
	}
	for _, dest := range attempts {
		for _, batch := range dest {
			for _, id := range batch {
				if !ok[id] {
					t.Fatalf("delivery carried removed subject record %s in attempt %v", id, batch)
				}
			}
		}
	}
}
