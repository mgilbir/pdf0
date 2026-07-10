package pdf0

import (
	"bytes"
	"testing"
)

// TestObjectStreamIndexOverflow guards the cross-reference-stream field-3 width:
// an object stream that packs more than 65536 objects produces indices above
// 65535, which must not be truncated to two bytes (that silently wraps the
// index and corrupts the xref). A document with >65536 compressible objects
// must survive a Read -> Write -> Read round trip.
func TestObjectStreamIndexOverflow(t *testing.T) {
	const n = 70000
	doc := &Document{
		Objects:        make(map[int]*IndirectObject, n+2),
		usedXRefStream: true, // triggers object-stream packing on Write
		Version:        "2.0",
	}
	catalog := &Dictionary{}
	catalog.Set("Type", Name("Catalog"))
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{})
	pages.Set("Count", Integer(0))
	catalog.Set("Pages", IndirectRef{Number: 2})
	doc.Objects[1] = &IndirectObject{Number: 1, Value: catalog}
	doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
	// Many small, compressible (non-stream) objects to fill one object stream.
	for i := 3; i < n; i++ {
		d := &Dictionary{}
		d.Set("V", Integer(i))
		doc.Objects[i] = &IndirectObject{Number: i, Value: d}
	}
	doc.Trailer = Dictionary{}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rt, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("re-Read after Write (index overflow?): %v", err)
	}
	// Spot-check an object whose stream index exceeds 65535.
	if o := rt.Objects[68000]; o == nil {
		t.Error("object 68000 lost across the round trip")
	}
}
