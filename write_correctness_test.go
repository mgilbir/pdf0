package pdf0

import (
	"bytes"
	"testing"
)

// TestWriteXRefStreamHighObjNum is the C5 guard: when a sparse, high-numbered
// object is packed into an object stream on write, the xref-stream field that
// holds the containing object-stream number must be wide enough for that number
// (not just for the byte offsets), or the container reference is truncated and
// the file no longer re-reads.
func TestWriteXRefStreamHighObjNum(t *testing.T) {
	doc := &Document{Version: "2.0", Objects: map[int]*IndirectObject{}, usedXRefStream: true}

	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	cat.Set("Extra", IndirectRef{Number: 100000})
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{IndirectRef{Number: 3}})
	pages.Set("Count", Integer(1))
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 2})
	page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
	high := &Dictionary{}
	high.Set("Type", Name("ExtGState"))

	doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
	doc.Objects[3] = &IndirectObject{Number: 3, Value: page}
	doc.Objects[100000] = &IndirectObject{Number: 100000, Value: high}
	doc.Trailer = Dictionary{}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	re, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("re-read of written xref-stream document failed: %v", err)
	}
	if re.Objects[100000] == nil {
		t.Fatal("object 100000 was lost on round-trip (truncated object-stream reference)")
	}
}

// TestWriteRejectsSharedLengthConflict is the C40 guard: two streams pointing
// their /Length at one integer object with different data lengths cannot both be
// represented, so Write rejects it instead of emitting a nondeterministic wrong
// length.
func TestWriteRejectsSharedLengthConflict(t *testing.T) {
	doc := &Document{Version: "2.0", Objects: map[int]*IndirectObject{}}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	s1 := &Stream{Data: []byte("abc")}
	s1.Dict.Set("Length", IndirectRef{Number: 9})
	s2 := &Stream{Data: []byte("abcdefgh")}
	s2.Dict.Set("Length", IndirectRef{Number: 9})

	doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	doc.Objects[5] = &IndirectObject{Number: 5, Value: s1}
	doc.Objects[6] = &IndirectObject{Number: 6, Value: s2}
	doc.Objects[9] = &IndirectObject{Number: 9, Value: Integer(3)}
	doc.Trailer = Dictionary{}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})

	if err := doc.Write(&bytes.Buffer{}); err == nil {
		t.Fatal("Write must reject two streams sharing a /Length target with different lengths")
	}
}

// TestParseXRefStreamRejectsNegativeIndex is the C38 guard: a negative /Index
// start or count is rejected, matching the traditional xref table.
func TestParseXRefStreamRejectsNegativeIndex(t *testing.T) {
	s := &Stream{}
	s.Dict.Set("W", Array{Integer(1), Integer(2), Integer(2)})
	s.Dict.Set("Index", Array{Integer(-5), Integer(1)})
	s.Dict.Set("Size", Integer(10))
	if _, err := ParseXRefStream(s); err == nil {
		t.Fatal("a negative /Index start object must be rejected")
	}
}
