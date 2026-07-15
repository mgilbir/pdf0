package pdf0

import (
	"bytes"
	"fmt"
	"sort"
)

// objStmMaxRaw bounds one object stream's decompressed (index + bodies) size.
// A reader caps flate output at maxDecodeSize (100 MB), so a container whose
// decompressed size exceeds that would be written but rejected on the next read,
// silently losing every object it holds. Keeping each container well under the
// cap lets buildWriteSet split a large object set across several containers that
// all round-trip. Half the cap leaves generous margin for the index header.
//
// It is a var, not a const, only so tests can lower it to exercise the split
// without constructing a 100 MB document.
var objStmMaxRaw = maxDecodeSize / 2

// buildObjectStream packs the given non-stream objects, whose plaintext bodies
// are supplied pre-serialized in bodies, into a /Type /ObjStm container numbered
// objStmNum, FlateDecode-compressed. It returns the container and each packed
// object's index within the stream.
//
// The layout matches parseObjStmIndex: a leading index of N "objnum offset"
// pairs, then the object bodies; /First is the byte length of the index and each
// offset is relative to it.
func buildObjectStream(nums []int, bodies map[int][]byte, objStmNum int) (*IndirectObject, map[int]int) {
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
	encoded := flateEncode(raw)

	dict := &Dictionary{}
	dict.Set("Type", Name("ObjStm"))
	dict.Set("N", Integer(len(nums)))
	dict.Set("First", Integer(first))
	dict.Set("Filter", Name("FlateDecode"))
	dict.Set("Length", Integer(len(encoded)))
	return &IndirectObject{Number: objStmNum, Value: &Stream{Dict: *dict, Data: encoded}}, index
}

// buildWriteSet returns the objects Write should serialize. When regenerating a
// cross-reference stream it packs eligible objects into an object stream and
// returns type2, mapping each packed object number to {objStmNum, index} for
// the cross-reference stream's type-2 entries. Otherwise it returns d.Objects
// unchanged with a nil map.
//
// It never mutates d.Objects: packing builds a fresh map.
func (d *Document) buildWriteSet() (map[int]*IndirectObject, map[int][2]int) {
	if !d.usedXRefStream {
		return d.Objects, nil
	}
	// An encrypted document we could not decrypt is written back as a passthrough:
	// each object still holds its original per-object-encrypted bytes. Packing
	// those into a new object stream would be wrong — a reader does not apply
	// per-object decryption to objects inside an /ObjStm — so leave every object
	// individually addressable and let Write emit an all-uncompressed xref stream.
	if (d.Encrypted || d.Trailer.Get("Encrypt") != nil) && d.security == nil {
		return d.Objects, nil
	}

	encNum := -1
	if d.security != nil {
		encNum = d.security.encryptObjNum
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
		if st, ok := iobj.Value.(*Stream); ok {
			if ref, ok := st.Dict.Get("Length").(IndirectRef); ok {
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
		// references), and indirect /Length targets cannot (or must not) be
		// compressed.
		if num == encNum || encReachable[num] || iobj.Generation != 0 || lengthTargets[num] {
			continue
		}
		if _, isStream := iobj.Value.(*Stream); isStream {
			continue
		}
		packable = append(packable, num)
	}
	if len(packable) < 2 {
		return d.Objects, nil // not worth an object stream
	}
	sort.Ints(packable)

	// Serialize each object once: the bytes size the chunks and are reused when
	// building containers (WriteObject is not cheap on large objects).
	bodies := make(map[int][]byte, len(packable))
	for _, num := range packable {
		var buf bytes.Buffer
		NewSerializer(&buf).WriteObject(d.Objects[num].Value)
		bodies[num] = buf.Bytes()
	}

	// Group objects into chunks whose decompressed size stays under objStmMaxRaw,
	// emitting one container per chunk. This keeps every written container
	// readable (see objStmMaxRaw); a small object set yields a single container,
	// preserving the previous output byte-for-byte. An object whose body alone
	// exceeds the budget cannot be packed safely, so it is left as an individual
	// indirect object.
	out := make(map[int]*IndirectObject, len(d.Objects)+4)
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
		if len(bodies[num]) >= objStmMaxRaw {
			continue // too large to pack; stays an individual object
		}
		if chunkBytes+cost >= objStmMaxRaw {
			flush()
		}
		chunk = append(chunk, num)
		chunkBytes += cost
	}
	flush()

	if len(type2) == 0 {
		return d.Objects, nil // nothing packable after all
	}
	return out, type2
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
	var walk func(o Object)
	walk = func(o Object) {
		switch v := o.(type) {
		case IndirectRef:
			if !reachable[v.Number] {
				reachable[v.Number] = true
				stack = append(stack, v.Number)
			}
		case *Dictionary:
			for _, val := range v.Values {
				walk(val)
			}
		case Array:
			for _, e := range v {
				walk(e)
			}
		case *Stream:
			for _, val := range v.Dict.Values {
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
