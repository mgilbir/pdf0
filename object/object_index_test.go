package object

import (
	"fmt"
	"testing"
	"time"
)

// bigDict builds a dictionary with n distinct keys K0..K(n-1) mapped to
// Integer(i), forcing the indexed lookup path (n > dictLookupThreshold).
func bigDict(n int) *Dictionary {
	d := &Dictionary{}
	for i := 0; i < n; i++ {
		d.Set(Name(fmt.Sprintf("K%d", i)), Integer(i))
	}
	return d
}

// TestDictIndexedGetSetDelete exercises the >64-key indexed path for parity
// with the linear scan it replaces.
func TestDictIndexedGetSetDelete(t *testing.T) {
	const n = 500
	d := bigDict(n)
	if d.Len() != n {
		t.Fatalf("Len = %d, want %d", d.Len(), n)
	}
	for i := 0; i < n; i++ {
		if got := d.Get(Name(fmt.Sprintf("K%d", i))); got != Integer(i) {
			t.Fatalf("Get(K%d) = %v, want %d", i, got, i)
		}
	}
	if got := d.Get("missing"); got != nil {
		t.Fatalf("Get(missing) = %v, want nil", got)
	}

	// Update existing key in place.
	d.Set("K42", Integer(-42))
	if got := d.Get("K42"); got != Integer(-42) {
		t.Fatalf("after Set, Get(K42) = %v, want -42", got)
	}
	if d.Len() != n {
		t.Fatalf("Set of existing key changed Len to %d", d.Len())
	}

	// Append a new key.
	d.Set("Knew", Integer(999))
	if got := d.Get("Knew"); got != Integer(999) {
		t.Fatalf("Get(Knew) = %v, want 999", got)
	}

	// Delete shifts slots; the index must not go stale.
	if !d.Delete("K10") {
		t.Fatal("Delete(K10) = false")
	}
	if got := d.Get("K10"); got != nil {
		t.Fatalf("after Delete, Get(K10) = %v, want nil", got)
	}
	if got := d.Get("K11"); got != Integer(11) {
		t.Fatalf("after Delete(K10), Get(K11) = %v, want 11 (slots must stay correct)", got)
	}
	if got := d.Get("K499"); got != Integer(499) {
		t.Fatalf("after Delete(K10), Get(K499) = %v, want 499", got)
	}
}

// TestDictIndexedDuplicateKeys pins first-occurrence semantics, matching the
// linear scan and Dictionary.Set behavior for the (spec-undefined) duplicate case.
func TestDictIndexedDuplicateKeys(t *testing.T) {
	d := bigDict(100)
	// Manually append a duplicate of an existing key at a later slot.
	d.Keys = append(d.Keys, "K5")
	d.Values = append(d.Values, Integer(-5))
	d.index = nil // force rebuild over the now-duplicated key set

	if got := d.Get("K5"); got != Integer(5) {
		t.Fatalf("Get(K5) with a later duplicate = %v, want first occurrence 5", got)
	}
	// Set updates the first occurrence's value, leaving the duplicate slot alone.
	d.Set("K5", Integer(50))
	if d.Values[5] != Integer(50) {
		t.Fatalf("Set(K5) did not update the first occurrence")
	}
}

// TestDictValueCopyIndexAliasing guards the value-copy hazard: a Dictionary
// copied by value shares the (read-only) index map, and mutating either copy
// must not corrupt the other's lookups. crypt.go / objstm_write.go copy
// Stream.Dict by value, so this pattern is real.
func TestDictValueCopyIndexAliasing(t *testing.T) {
	orig := bigDict(100)
	_ = orig.Get("K0") // materialize the shared index on orig

	cp := *orig // value copy: cp.index aliases orig.index

	// Append to the copy. With an incremental-mutate index this would write the
	// shared map and give orig a phantom out-of-range slot.
	cp.Set("copyonly", Integer(1))

	if got := orig.Get("copyonly"); got != nil {
		t.Fatalf("orig sees copy-only key: Get(copyonly) = %v, want nil", got)
	}
	if got := cp.Get("copyonly"); got != Integer(1) {
		t.Fatalf("copy lost its own key: Get(copyonly) = %v, want 1", got)
	}
	// orig's own lookups remain correct.
	for i := 0; i < 100; i++ {
		if got := orig.Get(Name(fmt.Sprintf("K%d", i))); got != Integer(i) {
			t.Fatalf("orig.Get(K%d) = %v, want %d after copy mutation", i, got, i)
		}
	}
}

// directDict builds an n-key dictionary by populating Keys/Values directly, the
// way the parser materializes a large dictionary — without routing through Set,
// whose linear existence scan is O(n^2) over n appends (a pre-existing,
// out-of-scope builder cost). This isolates the Get path under test.
func directDict(n int) *Dictionary {
	d := &Dictionary{Keys: make([]Name, n), Values: make([]Object, n)}
	for i := 0; i < n; i++ {
		d.Keys[i] = Name(fmt.Sprintf("K%d", i))
		d.Values[i] = Integer(i)
	}
	return d
}

// TestDictLookupIsSubLinear is the C20/C22 root-cause guard: repeated Get over
// a large dictionary must be well under quadratic. A linear-scan Get makes this
// O(n^2); the index makes it O(n). We assert a generous wall-clock ceiling so
// the test is not flaky but still fails hard on a regression to linear scan.
func TestDictLookupIsSubLinear(t *testing.T) {
	const n = 200_000
	d := directDict(n)
	start := time.Now()
	// One full sweep of lookups: O(n) with the index, O(n^2) without.
	sum := 0
	for i := 0; i < n; i++ {
		if v, ok := d.Get(Name(fmt.Sprintf("K%d", i))).(Integer); ok {
			sum += int(v)
		}
	}
	elapsed := time.Since(start)
	if sum == 0 {
		t.Fatal("lookups returned nothing; test is not exercising the path")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("full lookup sweep over %d keys took %v — indexed Get regressed to linear scan", n, elapsed)
	}
}
