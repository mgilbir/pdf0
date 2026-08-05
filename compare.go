package pdf0

import (
	"github.com/mgilbir/pdf0/object"
)

// This file implements deep semantic equality over the object model — the
// oracle the round-trip tests rest on. Two documents are equal when their
// object graphs mean the same thing, not when their bytes match: key order is
// ignored, Integer and Real compare across types (a serializer may legally
// rewrite 1.0 as 1), and IndirectRef values compare by number alone, since
// Equal has no Document and deliberately never resolves a reference.
//
// Dictionary comparison is a one-to-one matching of entries rather than a key
// lookup, so duplicate keys keep exact multiset semantics; candidates are
// grouped by key to keep that matching linear. Recursion is depth-capped: a
// programmatically constructed object graph can be cyclic.

// maxCompareDepth bounds recursion through nested arrays/dictionaries so that a
// cyclic direct object (constructable programmatically, since Dictionary fields
// are exported) cannot exhaust the goroutine stack — an unrecoverable fatal
// error. Beyond the cap the objects are treated as not-equal.

// Equal reports whether two objects are deeply equal, comparing an Integer and
// a Real that hold the same number as equal.
func Equal(a, b object.Object) bool { return object.Equal(a, b) }

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
		if !Equal(aObj, bObj) {
			return false
		}
	}

	return true
}
