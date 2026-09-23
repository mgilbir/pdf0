package pdfa

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
)

// TestByteRulesStayInsideTheFile: every slice a byte rule takes is bounded by
// the file. A record whose offsets point past the end, backwards, or into the
// middle of nothing — which a record Read built cannot hold, but which the
// rules must not trust — yields no out-of-range panic, so no "internal"
// finding; and the rules that can still say something do (audit 2026-09-22
// C69: two unguarded slices turned every byte rule into one "internal").
func TestByteRulesStayInsideTheFile(t *testing.T) {
	data := []byte("%PDF-1.7\n%\x80\x80\x80\x80\n1 0 obj\n<< >>\nendobj\n")
	n := int64(len(data))
	f := &core.FileRecord{
		Data: data,
		Objects: []core.FileObject{
			{Num: 1, Offset: 18, End: n + 1000, Stream: true, StreamKeyword: n + 50, Length: 5, LengthOK: true},
			{Num: 2, Offset: n + 10, End: n + 20},
			{Num: 3, Offset: 30, End: 10, Stream: true, StreamKeyword: -4, LengthOK: true},
			{Num: 4, Offset: -8, End: 4, Stream: true, StreamKeyword: 2, LengthOK: true},
		},
		XRefTables:  []int64{n - 2, n + 3, -1},
		XRefStreams: true,
		Trailers:    []core.FileTrailer{{Offset: n + 9, HasID: true, ID0: []byte{1}}, {Offset: 2, HasID: true}},
		Linearized:  true,
		Signatures:  []core.FileSignature{{Num: 9, OK: true}},
	}
	for _, level := range []Level{PDFA1b, PDFA2b, PDFA4} {
		doc := mkPDFAViewT(t, level)
		doc.File = func() *core.FileRecord { return f }
		vs := ValidateView(doc, level)
		for _, v := range vs {
			if v.Rule == finding.InternalRule {
				t.Errorf("%s: a byte rule failed internally on an out-of-range record: %v", level, v)
			}
		}
		// The header is fine and there is no data after %%EOF: that rule
		// still ran, and reported the missing marker.
		if !hasMessage(vs, "%%EOF marker not found") {
			t.Errorf("%s: the %%%%EOF rule did not run alongside the others: %v", level, vs)
		}
	}
}
