package pdf0

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

// opaqueReaderAt hides bytes.Reader's Size method, so Read cannot learn the
// source's true length and has to discover it by reading.
type opaqueReaderAt struct{ r *bytes.Reader }

func (o opaqueReaderAt) ReadAt(p []byte, off int64) (int, error) { return o.r.ReadAt(p, off) }

// TestReadSizeClaim is the Read-side sibling of C123 (audit 2026-09-22): the
// size passed to Read is the caller's claim about the source. A negative one is
// an error, not a recovered makeslice panic, and one larger than the source is
// an error that does not first allocate the claimed size.
func TestReadSizeClaim(t *testing.T) {
	src := []byte("%PDF-1.7\n")
	_, err := Read(bytes.NewReader(src), -1)
	if err == nil || strings.Contains(err.Error(), "panic") {
		t.Errorf("negative size: err = %v, want a plain error", err)
	}
	for _, r := range []interface {
		ReadAt([]byte, int64) (int, error)
	}{bytes.NewReader(src), opaqueReaderAt{bytes.NewReader(src)}} {
		const claimed = 1 << 32
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := Read(r, claimed)
		runtime.ReadMemStats(&after)
		if err == nil || !strings.Contains(err.Error(), "short read") {
			t.Errorf("%T: claim beyond the source: err = %v, want a short read", r, err)
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
			t.Errorf("%T: allocated %d bytes for a %d-byte source claiming %d", r, grew, len(src), claimed)
		}
	}
}
