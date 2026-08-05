package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
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
		Objects:        make(map[int]*object.IndirectObject, n+2),
		usedXRefStream: true, // triggers object-stream packing on Write
		Version:        "2.0",
	}
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{})
	pages.Set("Count", object.Integer(0))
	catalog.Set("Pages", object.IndirectRef{Number: 2})
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: catalog}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
	// Many small, compressible (non-stream) objects to fill one object stream.
	for i := 3; i < n; i++ {
		d := &object.Dictionary{}
		d.Set("V", object.Integer(i))
		doc.Objects[i] = &object.IndirectObject{Number: i, Value: d}
	}
	doc.Trailer = object.Dictionary{}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})

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
