package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestEncryptIndirectCFNotPacked is the regression for encrypted round-trip data
// loss when the /Encrypt dictionary references its /CF crypt-filter dictionary
// through an indirect reference. buildWriteSet used to pack that referenced
// object into a new object stream; on the next read the security handler needs
// /CF before object streams are materialised, so it resolved to nothing, fell
// back to no stream decryption, and the whole (still-encrypted) object stream
// failed to decode — losing every object packed into it. Objects reachable from
// /Encrypt must stay out of object streams.
func TestEncryptIndirectCFNotPacked(t *testing.T) {
	d := &Document{Objects: map[int]*object.IndirectObject{}, Version: "2.0", usedXRefStream: true}
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	d.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	// Enough small packable objects that Write builds an object stream.
	for i := 10; i < 40; i++ {
		dd := &object.Dictionary{}
		dd.Set("Type", object.Name("Item"))
		dd.Set("K", object.Integer(i))
		d.Objects[i] = &object.IndirectObject{Number: i, Value: dd}
	}
	d.Trailer = object.Dictionary{}
	d.Trailer.Set("Root", object.IndirectRef{Number: 1})

	if err := d.SetEncryption("", ""); err != nil {
		t.Fatal(err)
	}
	// Move the (direct) /CF crypt-filter dictionary into its own indirect object
	// and reference it — the shape that triggered the bug.
	enc := d.ResolveDict(d.Trailer.Get("Encrypt"))
	cf, ok := enc.Get("CF").(*object.Dictionary)
	if !ok {
		t.Fatalf("expected a direct /CF dictionary, got %T", enc.Get("CF"))
	}
	cfNum := 0
	for num := range d.Objects {
		if num > cfNum {
			cfNum = num
		}
	}
	cfNum++
	d.Objects[cfNum] = &object.IndirectObject{Number: cfNum, Value: cf}
	enc.Set("CF", object.IndirectRef{Number: cfNum})

	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.Bytes()
	d2, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if d2.security == nil {
		t.Fatal("re-read did not decrypt (indirect /CF was not resolvable at handler-build time)")
	}
	if len(d2.brokenObjStms) > 0 {
		t.Errorf("object stream(s) failed to decode on re-read: %v", d2.brokenObjStms)
	}
	if len(d2.Objects) != len(d.Objects) {
		t.Errorf("object loss on round-trip: %d in, %d out", len(d.Objects), len(d2.Objects))
	}
	// The /CF object itself must not have been packed into an object stream: it
	// must have a recorded byte offset (uncompressed) in the rewritten file.
	if _, ok := d2.Offsets[cfNum]; !ok {
		t.Errorf("the /CF object %d was packed into an object stream", cfNum)
	}
}

// TestEncryptReachable checks the set of objects reachable from /Encrypt,
// including a transitively referenced object.
func TestEncryptReachable(t *testing.T) {
	d := &Document{Objects: map[int]*object.IndirectObject{}}
	// /Encrypt (obj 5) -> /CF (obj 6) -> /StdCF nested with a further ref (obj 7).
	inner := &object.Dictionary{}
	inner.Set("Extra", object.IndirectRef{Number: 7})
	d.Objects[7] = &object.IndirectObject{Number: 7, Value: &object.Dictionary{}}
	cf := &object.Dictionary{}
	cf.Set("StdCF", inner)
	d.Objects[6] = &object.IndirectObject{Number: 6, Value: cf}
	encDict := &object.Dictionary{}
	encDict.Set("Filter", object.Name("Standard"))
	encDict.Set("CF", object.IndirectRef{Number: 6})
	d.Objects[5] = &object.IndirectObject{Number: 5, Value: encDict}
	d.Trailer = object.Dictionary{}
	d.Trailer.Set("Encrypt", object.IndirectRef{Number: 5})

	got := d.encryptReachable()
	for _, num := range []int{5, 6, 7} {
		if !got[num] {
			t.Errorf("object %d should be reachable from /Encrypt", num)
		}
	}
	if got[1] {
		t.Error("unrelated object 1 must not be reachable")
	}
}
