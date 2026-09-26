package object

import (
	"bytes"
	"math"
)

// Deep semantic equality over the object model.

// Equal reports whether two PDF objects are semantically equal.
// It compares values deeply. IndirectRef values are compared by their
// object/generation numbers only; Equal does not resolve references (it has no
// document to resolve against), so an IndirectRef is never equal to the object
// it points to.
//
// Numbers compare by value across the two numeric types, because a serializer
// may legally rewrite 1.0 as 1. Two Integers compare exactly. Any comparison
// involving a Real uses one tolerance, whichever the other operand's type:
// the values are equal when they differ by at most 1e-10 of the larger
// magnitude, or by at most 1e-12 absolutely, so float noise near zero is not a
// difference but a small value such as 1e-11 still differs from zero. The comparison is symmetric and reflexive; a NaN equals a NaN
// (so any object equals itself) and nothing else, and an infinity equals only
// the same infinity. Like any tolerance it is not transitive across a chain of
// values each within tolerance of the next.
//
// A pointer to a scalar value (*Name, *Integer, ...) is not a PDF object (see
// the package documentation) and is equal to nothing.
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
			return numbersEqual(float64(av), float64(bv))
		}
		return false

	case Real:
		switch bv := b.(type) {
		case Real:
			return numbersEqual(float64(av), float64(bv))
		case Integer:
			return numbersEqual(float64(av), float64(bv))
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

// DictionaryEqual reports whether two dictionaries hold the same keys with
// Equal values. Key order is ignored. A Dictionary holds at most one entry per
// key, so this is a lookup per entry, linear in the size of the dictionaries.
// Two nil dictionaries are equal; a nil and a non-nil one are not.
func DictionaryEqual(a, b *Dictionary) bool {
	return dictionaryEqualDepth(a, b, 0)
}

func dictionaryEqualDepth(a, b *Dictionary, depth int) bool {
	if depth > maxCompareDepth {
		return false
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Len() != b.Len() {
		return false
	}
	for k, av := range a.All() {
		bv, ok := b.Lookup(k)
		if !ok || !equalDepth(av, bv, depth+1) {
			return false
		}
	}
	return true
}

// numbersEqual is the one tolerance policy for every comparison that involves
// a Real (audit 2026-09-22 C122). The Real-Real comparison used to be
// absolute and the Integer-Real one relative, so Real(1e-11) equalled Real(0)
// but not Integer(0), and Real(1e20) did not equal Real(1e20+16384), a
// difference of one part in 10^16.
func numbersEqual(a, b float64) bool {
	if a == b {
		return true // also ±0, and equal infinities
	}
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	if math.IsInf(a, 0) || math.IsInf(b, 0) {
		return false
	}
	// a-b can overflow to +Inf for values of opposite sign near MaxFloat64,
	// which correctly compares unequal.
	return math.Abs(a-b) <= max(numericAbsTolerance, numericRelTolerance*max(math.Abs(a), math.Abs(b)))
}

// maxCompareDepth bounds recursion through nested arrays and dictionaries so a
// cyclic object graph — which a caller can build, since a dictionary can hold
// itself — cannot exhaust the goroutine stack, an unrecoverable fatal error.
// Beyond the cap the objects are treated as not equal.
const maxCompareDepth = 1000

// numericRelTolerance and numericAbsTolerance are numbersEqual's tolerance:
// relative to the larger magnitude, with an absolute floor near zero. The floor
// sits below any value a PDF writer means (ISO 32000 readers keep about five
// significant decimal digits), so Integer(0) and Real(1e-11) stay different
// (audit 2026-07-26 C32), and above the noise of float arithmetic on values
// of order one (0.1+0.2-0.3 is 5.6e-17).
const (
	numericRelTolerance = 1e-10
	numericAbsTolerance = 1e-12
)
