package pdf0

import (
	"bytes"
	"math"
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
const maxCompareDepth = 1000

// Equal reports whether two PDF objects are semantically equal.
// It compares values deeply. IndirectRef values are compared by their
// object/generation numbers only; Equal does not resolve references (it has no
// document to resolve against), so an IndirectRef is never equal to the object
// it points to.
func Equal(a, b Object) bool {
	return equalDepth(a, b, 0)
}

func equalDepth(a, b Object, depth int) bool {
	if depth > maxCompareDepth {
		return false
	}
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	switch av := a.(type) {
	case Boolean:
		bv, ok := b.(Boolean)
		return ok && av == bv

	case Integer:
		switch bv := b.(type) {
		case Integer:
			return av == bv
		case Real:
			// Cross-type numeric equality is deliberate (serializers may legally
			// rewrite 1.0 as 1). It uses a RELATIVE tolerance rather than the
			// absolute Real-Real epsilon: an absolute 1e-10 is both too loose near
			// zero (Integer(0) would equal Real(1e-11)) and too tight at large
			// magnitudes (audit C32).
			return intRealEqual(int64(av), float64(bv))
		}
		return false

	case Real:
		switch bv := b.(type) {
		case Real:
			return realEqual(float64(av), float64(bv))
		case Integer:
			return intRealEqual(int64(bv), float64(av))
		}
		return false

	case String:
		bv, ok := b.(String)
		if !ok {
			return false
		}
		return bytes.Equal(av.Value, bv.Value)

	case Name:
		bv, ok := b.(Name)
		return ok && av == bv

	case Array:
		bv, ok := b.(Array)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !equalDepth(av[i], bv[i], depth+1) {
				return false
			}
		}
		return true

	case *Dictionary:
		bv, ok := b.(*Dictionary)
		if !ok {
			return false
		}
		if av == nil || bv == nil {
			return av == nil && bv == nil
		}
		return dictionaryEqualDepth(av, bv, depth)

	case Dictionary:
		bv, ok := b.(Dictionary)
		if !ok {
			return false
		}
		return dictionaryEqualDepth(&av, &bv, depth)

	case *Stream:
		bv, ok := b.(*Stream)
		if !ok {
			return false
		}
		if av == nil || bv == nil {
			return av == nil && bv == nil
		}
		if !dictionaryEqualDepth(&av.Dict, &bv.Dict, depth) {
			return false
		}
		return bytes.Equal(av.Data, bv.Data)

	case Stream:
		bv, ok := b.(Stream)
		if !ok {
			return false
		}
		if !dictionaryEqualDepth(&av.Dict, &bv.Dict, depth) {
			return false
		}
		return bytes.Equal(av.Data, bv.Data)

	case Null:
		_, ok := b.(Null)
		return ok

	case *IndirectObject:
		bv, ok := b.(*IndirectObject)
		if !ok {
			return false
		}
		if av == nil || bv == nil {
			return av == nil && bv == nil
		}
		return av.Number == bv.Number &&
			av.Generation == bv.Generation &&
			equalDepth(av.Value, bv.Value, depth+1)

	case IndirectRef:
		bv, ok := b.(IndirectRef)
		if !ok {
			return false
		}
		return av.Number == bv.Number && av.Generation == bv.Generation
	}

	return false
}

// dictionaryEqual compares two dictionaries semantically.
// Key order is ignored for semantic comparison.
func dictionaryEqual(a, b *Dictionary) bool {
	return dictionaryEqualDepth(a, b, 0)
}

func dictionaryEqualDepth(a, b *Dictionary, depth int) bool {
	if depth > maxCompareDepth {
		return false
	}
	if a.Len() != b.Len() {
		return false
	}
	// Match each of a's (key, value) entries to a distinct entry of b. Using
	// b.Get (first occurrence) would give false positives on duplicate keys —
	// e.g. {A:1, A:1} would compare equal to {A:1, B:99}, and {A:1, A:2} would
	// not compare equal to itself (audit C26). Equal lengths plus a full
	// one-to-one matching is correct multiset equality.
	//
	// Group b's slots by key so the candidates for each of a's keys are only the
	// same-key slots, not all of b: a dictionary with distinct keys then compares
	// in linear time instead of O(n^2), which a crafted large tint-transform dict
	// otherwise exploited (audit C22). Duplicate keys keep exact multiset
	// semantics (their slots share a candidate list).
	bByKey := make(map[Name][]int, len(b.Keys))
	for j, k := range b.Keys {
		bByKey[k] = append(bByKey[k], j)
	}
	used := make([]bool, len(b.Keys))
	for i, key := range a.Keys {
		matched := false
		for _, j := range bByKey[key] {
			if used[j] {
				continue
			}
			if equalDepth(a.Values[i], b.Values[j], depth+1) {
				used[j] = true
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

const floatEpsilon = 1e-10

func realEqual(a, b float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) < floatEpsilon
}

// intRealEqual compares an integer to a real with a relative tolerance, so
// equality is neither spuriously granted near zero (an absolute epsilon makes
// Integer(0) equal Real(1e-11)) nor withheld at large magnitudes where an
// absolute 1e-10 is far below the rounding a serializer or float64 can preserve.
func intRealEqual(i int64, r float64) bool {
	fi := float64(i)
	if fi == r {
		return true
	}
	return math.Abs(fi-r) <= floatEpsilon*math.Max(math.Abs(fi), math.Abs(r))
}

// DocumentEqual compares two Documents for semantic equality.
func DocumentEqual(a, b *Document) bool {
	if a.Version != b.Version {
		return false
	}

	if !dictionaryEqual(&a.Trailer, &b.Trailer) {
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
