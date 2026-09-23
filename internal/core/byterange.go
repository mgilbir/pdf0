package core

import (
	"math"

	"github.com/mgilbir/pdf0/object"
)

// ByteRange is a signature's /ByteRange (ISO 32000-2 Table 255) read as the
// four integers every signature layout in use has: [start1 len1 start2 len2],
// the two spans of the file the digest covers.
//
// It is the one reader of /ByteRange, shared by signature verification (sign)
// and the PDF/A signature rule (pdfa), so that the arithmetic on these
// file-controlled values is written once. The values are attacker-chosen
// integers anywhere in int64: every derived quantity is computed here with its
// overflow checked, and nothing here allocates or slices by them. The audit of
// 2026-09-22 found both readers computing start+len unchecked (C5: a panic out
// of the public verifier) and one of them concatenating an unbounded number of
// segments (C6: an out-of-memory kill).
type ByteRange struct {
	Start1, Len1, Start2, Len2 int64
}

// ReadByteRange reads obj, resolved through v, as a /ByteRange of exactly four
// integers. Any other shape — another length, a non-integer element, a
// non-array — is reported as !ok. Four is not a PDF limit (the array holds
// "pairs"), but it is the only layout that can leave exactly one gap, the
// signature value, and no reader in pdf0 accepts another.
func ReadByteRange(v View, obj object.Object) (ByteRange, bool) {
	arr, ok := v.Resolve(obj).(object.Array)
	if !ok || len(arr) != 4 {
		return ByteRange{}, false
	}
	var vals [4]int64
	for i, e := range arr {
		n, ok := v.Resolve(e).(object.Integer)
		if !ok {
			return ByteRange{}, false
		}
		vals[i] = int64(n)
	}
	return ByteRange{Start1: vals[0], Len1: vals[1], Start2: vals[2], Len2: vals[3]}, true
}

// Ordered reports whether the range starts at the beginning of the file and
// its two spans are non-negative, ascending and non-overlapping: 0 = start1,
// len1 >= 0, len2 >= 0 and start2 >= start1+len1.
func (b ByteRange) Ordered() bool {
	return b.Start1 == 0 && b.Len1 >= 0 && b.Len2 >= 0 && b.Start2 >= b.Len1
}

// End returns start2+len2, the offset one past the last covered byte, and
// false when that sum does not fit in an int64. Call it on an Ordered range
// (both terms non-negative), where the only failure is overflow upward.
func (b ByteRange) End() (int64, bool) {
	if b.Start2 < 0 || b.Len2 < 0 || b.Len2 > math.MaxInt64-b.Start2 {
		return 0, false
	}
	return b.Start2 + b.Len2, true
}

// Within reports whether both spans lie inside a file of size bytes. It
// compares each length against what remains after its start, never a sum
// against size, so it cannot overflow. The range must be Ordered.
func (b ByteRange) Within(size int64) bool {
	return b.Ordered() && size >= 0 &&
		b.Len1 <= size &&
		b.Start2 <= size && b.Len2 <= size-b.Start2
}
