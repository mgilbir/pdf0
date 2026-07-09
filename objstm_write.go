package pdf0

import (
	"bytes"
	"fmt"
	"sort"
)

// buildObjectStream packs the given non-stream objects (in their plaintext form)
// into a /Type /ObjStm container numbered objStmNum, FlateDecode-compressed. It
// returns the container and each packed object's index within the stream.
//
// The layout matches parseObjStmIndex: a leading index of N "objnum offset"
// pairs, then the object bodies; /First is the byte length of the index and each
// offset is relative to it.
func buildObjectStream(nums []int, objects map[int]*IndirectObject, objStmNum int) (*IndirectObject, map[int]int) {
	sort.Ints(nums)
	var header, bodies bytes.Buffer
	index := make(map[int]int, len(nums))
	for i, num := range nums {
		fmt.Fprintf(&header, "%d %d ", num, bodies.Len())
		NewSerializer(&bodies).WriteObject(objects[num].Value)
		bodies.WriteByte('\n')
		index[num] = i
	}
	first := header.Len()
	raw := append(append([]byte(nil), header.Bytes()...), bodies.Bytes()...)
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

	encNum := -1
	if d.security != nil {
		encNum = d.security.encryptObjNum
	}
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
		// Streams, non-zero generations, the /Encrypt dictionary, and indirect
		// /Length targets cannot (or must not) be compressed.
		if num == encNum || iobj.Generation != 0 || lengthTargets[num] {
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

	objStmNum := maxObj + 1
	container, index := buildObjectStream(packable, d.Objects, objStmNum)

	packed := make(map[int]bool, len(packable))
	for _, num := range packable {
		packed[num] = true
	}
	out := make(map[int]*IndirectObject, len(d.Objects))
	for num, iobj := range d.Objects {
		if !packed[num] {
			out[num] = iobj
		}
	}
	out[objStmNum] = container

	type2 := make(map[int][2]int, len(index))
	for num, idx := range index {
		type2[num] = [2]int{objStmNum, idx}
	}
	return out, type2
}
