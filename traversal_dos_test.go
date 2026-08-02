package pdf0

import (
	"fmt"
	"github.com/mgilbir/pdf0/object"
	"testing"
	"time"
)

// directDict builds an n-key dictionary by populating Keys/Values directly,
// bypassing Set — whose linear existence scan is O(n^2) over n appends — so the
// cost measured below is the comparison's and not the builder's.
//
// The object package's own index tests have an identical helper. It is repeated
// rather than shared because a test helper cannot cross a package boundary
// without being exported into the object package's public surface, and this
// guard is over dictionaryEqual, which lives here.
func directDict(n int) *Dictionary {
	d := &Dictionary{Keys: make([]Name, n), Values: make([]Object, n)}
	for i := 0; i < n; i++ {
		d.Keys[i] = Name(fmt.Sprintf("K%d", i))
		d.Values[i] = Integer(i)
	}
	return d
}

// TestDictionaryEqualLinear is the C22 guard: comparing two large distinct-key
// dictionaries is linear, not O(n^2), while duplicate-key multiset semantics are
// preserved.
func TestDictionaryEqualLinear(t *testing.T) {
	const n = 50000
	a, b := directDict(n), directDict(n)
	start := time.Now()
	if !object.DictionaryEqual(a, b) {
		t.Fatal("two identical dictionaries compared unequal")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("comparing %d-key dictionaries took %v — dictionaryEqual regressed to O(n^2)", n, d)
	}

	// Duplicate-key multiset semantics (audit C26) must survive the change.
	dup12 := &Dictionary{Keys: []Name{"A", "A"}, Values: []Object{Integer(1), Integer(2)}}
	dup12b := &Dictionary{Keys: []Name{"A", "A"}, Values: []Object{Integer(1), Integer(2)}}
	if !object.DictionaryEqual(dup12, dup12b) {
		t.Error("{A:1, A:2} should equal itself")
	}
	dup11 := &Dictionary{Keys: []Name{"A", "A"}, Values: []Object{Integer(1), Integer(1)}}
	ab := &Dictionary{Keys: []Name{"A", "B"}, Values: []Object{Integer(1), Integer(99)}}
	if object.DictionaryEqual(dup11, ab) {
		t.Error("{A:1, A:1} must not equal {A:1, B:99}")
	}
}
