package pdf0

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/crypt"
	"github.com/mgilbir/pdf0/object"
)

// This file implements the write side of object streams (ISO 32000-2 7.5.7):
// packing eligible objects into /Type /ObjStm containers and deciding which
// objects may be packed at all. It runs only when a document is written back
// with a cross-reference stream; a traditional table means no packing.
//
// The exclusions carry the weight. Streams and non-zero generations cannot be
// packed by the format; the /Encrypt dictionary and everything reachable from
// it must not be, because the security handler consults them while reading,
// before object streams are materialized; and an indirect /Length target has to
// stay individually addressable so Write can correct its value after encryption
// changes the data length. Containers are split to stay under a size the reader
// will still accept.

// One object stream's decompressed (index + bodies) size is bounded by
// limits.objStmMaxRaw. A reader caps flate output at the decoded-stream limit,
// so a container whose decompressed size exceeds that would be written but
// rejected on the next read, silently losing every object it holds. Keeping each
// container well under the cap lets buildWriteSet split a large object set
// across several containers that all round-trip.
//
// It derives from the reader's cap rather than being configured separately, so
// a document written with WithMaxDecodedStreamBytes stays readable under the
// same configuration; see limits.objStmMaxRaw.

// buildObjectStream packs the given non-stream objects, whose plaintext bodies
// are supplied pre-serialized in bodies, into a /Type /ObjStm container numbered
// objStmNum, FlateDecode-compressed. It returns the container and each packed
// object's index within the stream.
//
// The layout matches parseObjStmIndex: a leading index of N "objnum offset"
// pairs, then the object bodies; /First is the byte length of the index and each
// offset is relative to it.
func buildObjectStream(nums []int, bodies map[int][]byte, objStmNum int) (*object.IndirectObject, map[int]int) {
	sort.Ints(nums)
	var header, body bytes.Buffer
	index := make(map[int]int, len(nums))
	for i, num := range nums {
		fmt.Fprintf(&header, "%d %d ", num, body.Len())
		body.Write(bodies[num])
		body.WriteByte('\n')
		index[num] = i
	}
	first := header.Len()
	raw := append(append([]byte(nil), header.Bytes()...), body.Bytes()...)
	encoded := core.FlateEncode(raw)

	dict := &object.Dictionary{}
	dict.Set("Type", object.Name("ObjStm"))
	dict.Set("N", object.Integer(len(nums)))
	dict.Set("First", object.Integer(first))
	dict.Set("Filter", object.Name("FlateDecode"))
	dict.Set("Length", object.Integer(len(encoded)))
	return &object.IndirectObject{Number: objStmNum, Value: object.NewStream(dict, encoded)}, index
}

// buildWriteSet returns the objects Write should serialize. When regenerating a
// cross-reference stream it packs eligible objects into an object stream and
// returns type2, mapping each packed object number to {objStmNum, index} for
// the cross-reference stream's type-2 entries. Otherwise it returns d.Objects
// unchanged with a nil map.
//
// It never mutates d.Objects: packing builds a fresh map. An object that does
// not serialise is an error, as it is on the traditional-table path: the
// packed body used to be written with the error dropped, so a NaN in an array
// produced a container that no reader, pdf0 included, could parse back (audit
// 2026-09-22 C100).
func (d *Document) buildWriteSet() (map[int]*object.IndirectObject, map[int][2]int, error) {
	if !d.usedXRefStream {
		return d.Objects, nil, nil
	}
	// An encrypted document we could not decrypt is written back as a passthrough:
	// each object still holds its original per-object-encrypted bytes. Packing
	// those into a new object stream would be wrong — a reader does not apply
	// per-object decryption to objects inside an /ObjStm — so leave every object
	// individually addressable and let Write emit an all-uncompressed xref stream.
	if d.Locked() {
		return d.Objects, nil, nil
	}

	encNum := -1
	if d.security != nil {
		encNum = d.security.EncryptObjNum
	}
	// Objects reachable from the /Encrypt dictionary (e.g. an indirectly
	// referenced /CF crypt-filter dictionary) must never be packed into an
	// object stream. The security handler consults them while reading the file,
	// BEFORE object streams are materialised, so an object packed into a
	// container the handler cannot yet decode resolves to nothing — silently
	// disabling stream decryption and losing every object in the (still
	// encrypted) container on the next read.
	encReachable := d.encryptReachable()
	// An indirect /Length target must stay individually addressable so Write can
	// correct its value after (possibly length-changing) encryption.
	lengthTargets := map[int]bool{}
	for _, iobj := range d.Objects {
		if st, ok := iobj.Value.(*object.Stream); ok {
			if ref, ok := st.Dict.Get("Length").(object.IndirectRef); ok {
				lengthTargets[ref.Number] = true
			}
		}
	}

	maxObj := 0
	var packable []int
	for num, iobj := range d.Objects {
		if num > maxObj {
			maxObj = num
		}
		// Streams, non-zero generations, the /Encrypt dictionary (and anything it
		// references), indirect /Length targets, and anything holding a
		// signature dictionary cannot (or must not) be compressed.
		if num == encNum || encReachable[num] || iobj.Generation != 0 || lengthTargets[num] {
			continue
		}
		if _, isStream := iobj.Value.(*object.Stream); isStream {
			continue
		}
		if holdsSignatureDict(iobj.Value) {
			continue
		}
		packable = append(packable, num)
	}
	if len(packable) < 2 {
		return d.Objects, nil, nil // not worth an object stream
	}
	sort.Ints(packable)

	// Serialize each object once: the bytes size the chunks and are reused when
	// building containers (WriteObject is not cheap on large objects).
	bodies := make(map[int][]byte, len(packable))
	for _, num := range packable {
		var buf bytes.Buffer
		if err := NewSerializer(&buf).WriteObject(d.Objects[num].Value); err != nil {
			return nil, nil, fmt.Errorf("serializing object %d: %w", num, err)
		}
		bodies[num] = buf.Bytes()
	}

	// Group objects into chunks whose decompressed size stays under objStmMax,
	// emitting one container per chunk. This keeps every written container
	// readable (see limits.objStmMaxRaw); a small object set yields a single
	// container, preserving the previous output byte-for-byte. An object whose
	// body alone exceeds the budget cannot be packed safely, so it is left as an
	// individual indirect object.
	objStmMax := d.lim().ObjStmMaxRaw()
	out := make(map[int]*object.IndirectObject, len(d.Objects)+4)
	for num, iobj := range d.Objects {
		out[num] = iobj
	}
	type2 := map[int][2]int{}
	nextNum := maxObj + 1
	var chunk []int
	var chunkBytes int
	flush := func() {
		if len(chunk) < 1 {
			return
		}
		container, index := buildObjectStream(chunk, bodies, nextNum)
		out[nextNum] = container
		for num, idx := range index {
			type2[num] = [2]int{nextNum, idx}
			delete(out, num)
		}
		nextNum++
		chunk = nil
		chunkBytes = 0
	}
	for _, num := range packable {
		// Each object costs its body plus a newline and an index entry
		// ("objnum offset ", at most ~24 bytes for realistic numbers).
		cost := len(bodies[num]) + 1 + 24
		if len(bodies[num]) >= objStmMax {
			continue // too large to pack; stays an individual object
		}
		if chunkBytes+cost >= objStmMax {
			flush()
		}
		chunk = append(chunk, num)
		chunkBytes += cost
	}
	flush()

	if len(type2) == 0 {
		return d.Objects, nil, nil // nothing packable after all
	}
	return out, type2, nil
}

// holdsSignatureDict reports whether v is, or directly contains, a signature
// dictionary (crypt.IsSignatureDict): one whose /ByteRange and /Contents must
// appear literally in the file.
//
// Such an object must never be packed into an object stream. Signing writes a
// placeholder /ByteRange and /Contents and patches the real values into the
// output bytes afterwards, which it can only do if the placeholder is there to
// find: packed into a compressed container it never appears, and WriteSigned
// failed on every document read from an xref-stream file (audit 2026-09-22
// C21). A filled signature must stay literal too — its /ByteRange names byte
// offsets of the file it is in — and ISO 32000-2 7.6.2 exempts its /Contents
// from encryption, which an object inside an encrypted container cannot be.
// The walk goes through direct values only: a signature dictionary reached by
// reference is its own object and is judged on its own.
func holdsSignatureDict(v object.Object) bool {
	switch o := v.(type) {
	case *object.Dictionary:
		if crypt.IsSignatureDict(o) {
			return true
		}
		for val := range o.Values() {
			if holdsSignatureDict(val) {
				return true
			}
		}
	case object.Array:
		for _, e := range o {
			if holdsSignatureDict(e) {
				return true
			}
		}
	}
	return false
}

// encryptReachable returns the object numbers reachable from the /Encrypt
// dictionary via indirect references (transitively, including the dictionary's
// own object if it is indirect). The standard security handler reads these while
// building itself — before object streams are materialised — so they must stay
// out of object streams; see the call site in buildWriteSet. The common case (a
// direct /Encrypt with a direct /CF) yields the empty set.
func (d *Document) encryptReachable() map[int]bool {
	enc := d.Trailer.Get("Encrypt")
	if enc == nil {
		return nil
	}
	reachable := map[int]bool{}
	var stack []int
	var walk func(o object.Object)
	walk = func(o object.Object) {
		switch v := o.(type) {
		case object.IndirectRef:
			if !reachable[v.Number] {
				reachable[v.Number] = true
				stack = append(stack, v.Number)
			}
		case *object.Dictionary:
			for val := range v.Values() {
				walk(val)
			}
		case object.Array:
			for _, e := range v {
				walk(e)
			}
		case *object.Stream:
			for val := range v.Dict.Values() {
				walk(val)
			}
		}
	}
	walk(enc)
	for len(stack) > 0 {
		num := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if iobj, ok := d.Objects[num]; ok {
			walk(iobj.Value)
		}
	}
	return reachable
}
