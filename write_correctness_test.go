package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestWriteXRefStreamHighObjNum is the C5 guard: when a sparse, high-numbered
// object is packed into an object stream on write, the xref-stream field that
// holds the containing object-stream number must be wide enough for that number
// (not just for the byte offsets), or the container reference is truncated and
// the file no longer re-reads.
func TestWriteXRefStreamHighObjNum(t *testing.T) {
	doc := &Document{Version: "2.0", Objects: map[int]*object.IndirectObject{}, usedXRefStream: true}

	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})
	cat.Set("Extra", object.IndirectRef{Number: 100000})
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
	pages.Set("Count", object.Integer(1))
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	high := &object.Dictionary{}
	high.Set("Type", object.Name("ExtGState"))

	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
	doc.Objects[3] = &object.IndirectObject{Number: 3, Value: page}
	doc.Objects[100000] = &object.IndirectObject{Number: 100000, Value: high}
	doc.Trailer = object.Dictionary{}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})

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
	doc := &Document{Version: "2.0", Objects: map[int]*object.IndirectObject{}}
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	s1 := &object.Stream{Data: []byte("abc")}
	s1.Dict.Set("Length", object.IndirectRef{Number: 9})
	s2 := &object.Stream{Data: []byte("abcdefgh")}
	s2.Dict.Set("Length", object.IndirectRef{Number: 9})

	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: s1}
	doc.Objects[6] = &object.IndirectObject{Number: 6, Value: s2}
	doc.Objects[9] = &object.IndirectObject{Number: 9, Value: object.Integer(3)}
	doc.Trailer = object.Dictionary{}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})

	if err := doc.Write(&bytes.Buffer{}); err == nil {
		t.Fatal("Write must reject two streams sharing a /Length target with different lengths")
	}
}

// TestParseXRefStreamRejectsNegativeIndex is the C38 guard: a negative /Index
// start or count is rejected, matching the traditional xref table.
func TestParseXRefStreamRejectsNegativeIndex(t *testing.T) {
	s := &object.Stream{}
	s.Dict.Set("W", object.Array{object.Integer(1), object.Integer(2), object.Integer(2)})
	s.Dict.Set("Index", object.Array{object.Integer(-5), object.Integer(1)})
	s.Dict.Set("Size", object.Integer(10))
	if _, err := ParseXRefStream(s); err == nil {
		t.Fatal("a negative /Index start object must be rejected")
	}
}
