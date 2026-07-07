package pdf0

import (
	"bytes"
	"math"
)

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
			// Cross-type numeric equality is deliberate (serializers may
			// legally rewrite 1.0 as 1); it uses the same epsilon as
			// Real-Real so the tolerance is consistent in every direction.
			return realEqual(float64(av), float64(bv))
		}
		return false

	case Real:
		switch bv := b.(type) {
		case Real:
			return realEqual(float64(av), float64(bv))
		case Integer:
			return realEqual(float64(av), float64(bv))
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
	for i, key := range a.Keys {
		bVal := b.Get(key)
		if bVal == nil {
			return false
		}
		if !equalDepth(a.Values[i], bVal, depth+1) {
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
