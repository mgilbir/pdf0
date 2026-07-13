package pdf0

import (
	"bytes"
	"testing"
)

// TestObjectStreamSplitBudget guards that Write never packs more objects into a
// single object stream than a reader will decompress. A reader caps flate output
// at maxDecodeSize; a container whose decompressed size exceeds that is written
// but rejected on the next Read, silently losing every object it holds. Write
// must therefore split a large object set across several containers, each under
// objStmMaxRaw, so the whole document survives Read -> Write -> Read.
//
// The test lowers objStmMaxRaw so a modest set of objects forces a split without
// building a 100 MB document; it then asserts every object round-trips, more
// than one container was emitted, and no container decompresses beyond the cap.
func TestObjectStreamSplitBudget(t *testing.T) {
	saved := objStmMaxRaw
	objStmMaxRaw = 4096 // small enough that a few padded objects need several containers
	defer func() { objStmMaxRaw = saved }()

	const n = 200
	doc := &Document{
		Objects:        make(map[int]*IndirectObject, n+2),
		usedXRefStream: true, // triggers object-stream packing on Write
		Version:        "2.0",
	}
	catalog := &Dictionary{}
	catalog.Set("Type", Name("Catalog"))
	catalog.Set("Pages", IndirectRef{Number: 2})
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{})
	pages.Set("Count", Integer(0))
	doc.Objects[1] = &IndirectObject{Number: 1, Value: catalog}
	doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
	// Each object carries a ~300-byte string so ~14 fill one 4096-byte container:
	// 200 objects then require well over one container.
	pad := String{Value: bytes.Repeat([]byte("x"), 300)}
	for i := 3; i < n; i++ {
		d := &Dictionary{}
		d.Set("V", Integer(i))
		d.Set("Pad", pad)
		doc.Objects[i] = &IndirectObject{Number: i, Value: d}
	}
	doc.Trailer = Dictionary{}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})

	// The write set must span more than one container, and none may exceed the cap.
	writeSet, type2 := doc.buildWriteSet()
	containers := map[int]bool{}
	for _, loc := range type2 {
		containers[loc[0]] = true
	}
	if len(containers) < 2 {
		t.Fatalf("expected the object set to split across multiple containers, got %d", len(containers))
	}
	for cnum := range containers {
		st := writeSet[cnum].Value.(*Stream)
		raw, err := decodeStreamData(st)
		if err != nil {
			t.Fatalf("container %d: decode: %v", cnum, err)
		}
		if len(raw) >= maxDecodeSize {
			t.Errorf("container %d decompresses to %d bytes, at/over the reader cap %d", cnum, len(raw), maxDecodeSize)
		}
	}

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rt, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("re-Read after Write: %v", err)
	}
	if len(rt.brokenObjStms) != 0 {
		t.Fatalf("re-Read reported %d broken object stream(s): a written container was not readable", len(rt.brokenObjStms))
	}
	// Every original object must survive, unchanged.
	for num, orig := range doc.Objects {
		got, ok := rt.Objects[num]
		if !ok {
			t.Fatalf("object %d lost across the round trip", num)
		}
		if !Equal(orig.Value, got.Value) {
			t.Errorf("object %d changed across the round trip", num)
		}
	}
	// And a second Write must succeed (no missing objects from a broken re-read).
	var buf2 bytes.Buffer
	if err := rt.Write(&buf2); err != nil {
		t.Fatalf("second Write: %v", err)
	}
}
