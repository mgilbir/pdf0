package pdf0

import (
	"github.com/mgilbir/pdf0/object"
)

// This file implements deep semantic equality over documents — the oracle the
// round-trip tests rest on. Two documents are equal when their object graphs
// mean the same thing, not when their bytes match. The object comparison
// itself, with its numeric tolerance, key-order independence and depth cap,
// is object.Equal; IndirectRef values compare by object and generation
// number, since object.Equal has no Document and deliberately never resolves a
// reference.

// DocumentEqual compares two Documents for semantic equality.
func DocumentEqual(a, b *Document) bool {
	if a.Version != b.Version {
		return false
	}

	if !object.DictionaryEqual(&a.Trailer, &b.Trailer) {
		return false
	}

	if len(a.Objects) != len(b.Objects) {
		return false
	}

	for num, aObj := range a.Objects {
		bObj, ok := b.Objects[num]
		if !ok {
			return false
		}
		if !object.Equal(aObj, bObj) {
			return false
		}
	}

	return true
}
