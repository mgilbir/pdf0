package pdf0

import (
	"bytes"
	"testing"
)

// This file systematically exercises the re-encryption path (decrypt on Read →
// re-encrypt on Write, or verbatim passthrough) across the structural
// dimensions that have produced round-trip data-loss bugs: whether the file uses
// object streams, whether the /Encrypt dictionary reaches other objects through
// indirect references, whether metadata is encrypted, and whether the file is
// decryptable at all. Rather than wait for a corpus file to hit each combination,
// the matrix builds every one and asserts a lossless round-trip.

// encMatrixDoc builds a document with the ingredients that make encryption
// round-trips interesting: an encrypted content stream, an encrypted string, an
// optional metadata stream, and enough small packable objects that Write forms an
// object stream when the file uses a cross-reference stream.
func encMatrixDoc(usedXRefStream, withMetadata bool) *Document {
	d := &Document{Objects: map[int]*IndirectObject{}, Version: "2.0", usedXRefStream: usedXRefStream}

	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	d.Objects[1] = &IndirectObject{Number: 1, Value: cat}

	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{IndirectRef{Number: 3}})
	pages.Set("Count", Integer(1))
	d.Objects[2] = &IndirectObject{Number: 2, Value: pages}

	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 2})
	page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
	page.Set("Contents", IndirectRef{Number: 4})
	d.Objects[3] = &IndirectObject{Number: 3, Value: page}

	// A FlateDecode content stream: after a round-trip it must still inflate,
	// which only holds if it was decrypted correctly.
	content := flateEncode([]byte("BT /F1 12 Tf 72 720 Td (round-trip sentinel) Tj ET"))
	cs := &Dictionary{}
	cs.Set("Length", Integer(len(content)))
	cs.Set("Filter", Name("FlateDecode"))
	d.Objects[4] = &IndirectObject{Number: 4, Value: &Stream{Dict: *cs, Data: content}}

	// A dictionary carrying a string, to exercise string encryption.
	info := &Dictionary{}
	info.Set("Title", String{Value: []byte("sentinel-string-\x00-with-binary")})
	d.Objects[5] = &IndirectObject{Number: 5, Value: info}

	if withMetadata {
		xmp := []byte(`<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?><x:xmpmeta xmlns:x="adobe:ns:meta/"></x:xmpmeta><?xpacket end="w"?>`)
		ms := &Dictionary{}
		ms.Set("Type", Name("Metadata"))
		ms.Set("Subtype", Name("XML"))
		ms.Set("Length", Integer(len(xmp)))
		d.Objects[6] = &IndirectObject{Number: 6, Value: &Stream{Dict: *ms, Data: xmp}}
		cat.Set("Metadata", IndirectRef{Number: 6})
	}

	// Packable filler objects so Write builds an object stream (usedXRefStream).
	for i := 10; i < 30; i++ {
		dd := &Dictionary{}
		dd.Set("Type", Name("Filler"))
		dd.Set("N", Integer(i))
		d.Objects[i] = &IndirectObject{Number: i, Value: dd}
	}

	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})
	return d
}

// nextObjNum returns one past the highest object number in use.
func nextObjNum(d *Document) int {
	n := 0
	for num := range d.Objects {
		if num > n {
			n = num
		}
	}
	return n + 1
}

// makeIndirect moves the value of key in the resolved /Encrypt dictionary into a
// fresh indirect object and references it, reproducing the shape where the
// security handler must resolve a separate object before object streams exist.
func makeIndirect(d *Document, key Name) {
	enc := d.ResolveDict(d.Trailer.Get("Encrypt"))
	val := enc.Get(key)
	if _, already := val.(IndirectRef); already || val == nil {
		return
	}
	num := nextObjNum(d)
	d.Objects[num] = &IndirectObject{Number: num, Value: val}
	enc.Set(key, IndirectRef{Number: num})
}

// stripStreamLength returns a copy of o with a stream's /Length removed, so two
// streams that differ only in the declared length (an encrypted length on Write
// vs the decrypted length after Read) compare equal.
func stripStreamLength(o Object) Object {
	s, ok := o.(*Stream)
	if !ok {
		return o
	}
	nd := s.Dict.Clone()
	nd.Delete("Length")
	return &Stream{Dict: *nd, Data: s.Data}
}

// docsEqualModuloLength reports whether two documents hold the same objects once
// stream /Length is ignored. It is the lossless-content check for an encrypted
// round-trip, where decryption leaves the (larger) encrypted /Length in place.
func docsEqualModuloLength(d1, d2 *Document) bool {
	if len(d1.Objects) != len(d2.Objects) {
		return false
	}
	for num, io1 := range d1.Objects {
		io2, ok := d2.Objects[num]
		if !ok || !Equal(stripStreamLength(io1.Value), stripStreamLength(io2.Value)) {
			return false
		}
	}
	return true
}

func TestEncryptRoundTripMatrix(t *testing.T) {
	variants := []struct {
		name       string
		xrefStream bool
		metadata   bool
		mutate     func(d *Document)
	}{
		{"direct-cf/objstm", true, false, nil},
		{"direct-cf/no-objstm", false, false, nil},
		{"indirect-cf/objstm", true, false, func(d *Document) { makeIndirect(d, "CF") }},
		{"indirect-cf/no-objstm", false, false, func(d *Document) { makeIndirect(d, "CF") }},
		{"indirect-O/objstm", true, false, func(d *Document) { makeIndirect(d, "O") }},
		{"encrypted-metadata/objstm", true, true, nil},
		{"unencrypted-metadata/objstm", true, true, func(d *Document) {
			// Keep the handler (used by Write) and the dictionary (used by Read)
			// consistent, as any real producer does: the metadata stream is left
			// in the clear on both sides.
			d.security.encryptMetadata = false
			d.ResolveDict(d.Trailer.Get("Encrypt")).Set("EncryptMetadata", Boolean(false))
		}},
		{"indirect-cf/encrypted-metadata", true, true, func(d *Document) { makeIndirect(d, "CF") }},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			doc := encMatrixDoc(v.xrefStream, v.metadata)
			if err := doc.SetEncryption("", ""); err != nil {
				t.Fatalf("SetEncryption: %v", err)
			}
			if v.mutate != nil {
				v.mutate(doc)
			}
			nIn := len(doc.Objects)

			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("Write: %v", err)
			}
			out := buf.Bytes()

			back, err := Read(bytes.NewReader(out), int64(len(out)))
			if err != nil {
				t.Fatalf("re-read: %v", err)
			}
			if back.security == nil {
				t.Fatal("re-read did not decrypt with the empty password")
			}
			if len(back.brokenObjStms) > 0 {
				t.Fatalf("object stream(s) failed to decode on re-read: %v", back.brokenObjStms)
			}
			if len(back.Objects) != nIn {
				t.Fatalf("object count changed: %d written, %d read back", nIn, len(back.Objects))
			}
			if !docsEqualModuloLength(doc, back) {
				t.Fatal("content differs after the encrypted round-trip")
			}
			// The content stream must still inflate — proof it decrypted correctly.
			if st, ok := back.Objects[4].Value.(*Stream); ok {
				if _, err := decodeStreamData(st); err != nil {
					t.Errorf("content stream does not inflate after round-trip: %v", err)
				}
			}
		})
	}
}

// TestEncryptPassthroughMatrix covers the non-decryptable side: a file whose
// password we do not hold is written back as a verbatim passthrough and must
// still decrypt with the real password afterwards, across the same structural
// dimensions.
func TestEncryptPassthroughMatrix(t *testing.T) {
	variants := []struct {
		name       string
		xrefStream bool
		mutate     func(d *Document)
	}{
		{"direct-cf/no-objstm", false, nil},
		{"indirect-cf/no-objstm", false, func(d *Document) { makeIndirect(d, "CF") }},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			doc := encMatrixDoc(v.xrefStream, false)
			if err := doc.SetEncryption("owner-secret", "owner-secret"); err != nil {
				t.Fatalf("SetEncryption: %v", err)
			}
			if v.mutate != nil {
				v.mutate(doc)
			}
			var enc bytes.Buffer
			if err := doc.Write(&enc); err != nil {
				t.Fatalf("initial Write: %v", err)
			}
			encBytes := enc.Bytes()

			// Read WITHOUT the password: stays encrypted, then passthrough-write.
			nd, err := Read(bytes.NewReader(encBytes), int64(len(encBytes)))
			if err != nil {
				t.Fatalf("read w/o password: %v", err)
			}
			if nd.security != nil {
				t.Fatal("unexpectedly decrypted without the password")
			}
			var pt bytes.Buffer
			if err := nd.Write(&pt); err != nil {
				t.Fatalf("passthrough Write: %v", err)
			}
			out := pt.Bytes()

			// The passthrough output must still decrypt with the real password.
			dec, err := ReadWithPassword(bytes.NewReader(out), int64(len(out)), "owner-secret")
			if err != nil {
				t.Fatalf("re-read with password: %v", err)
			}
			if dec.security == nil {
				t.Fatal("passthrough output does not decrypt with the correct password")
			}
			if len(dec.brokenObjStms) > 0 {
				t.Fatalf("passthrough output has undecodable object stream(s): %v", dec.brokenObjStms)
			}
		})
	}
}
