package object

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/hostile"
	"math/rand"
	"slices"
	"sync"
	"testing"
	"time"
)

// bigDict builds a dictionary with n distinct keys K0..K(n-1) mapped to
// Integer(i), forcing the indexed lookup path (n > dictIndexThreshold).
func bigDict(n int) *Dictionary {
	d := &Dictionary{}
	for i := 0; i < n; i++ {
		d.Set(Name(fmt.Sprintf("K%d", i)), Integer(i))
	}
	return d
}

// checkInvariant asserts the storage invariants every mutator must keep: keys
// and values parallel, keys unique, and the index — present exactly when a
// mutator has built it — mapping every key to its own slot and nothing else.
func checkInvariant(t *testing.T, d *Dictionary) {
	t.Helper()
	if d.s == nil {
		return
	}
	s := d.s
	if len(s.keys) != len(s.values) {
		t.Fatalf("keys/values lengths %d/%d", len(s.keys), len(s.values))
	}
	seen := map[Name]int{}
	for i, k := range s.keys {
		if j, dup := seen[k]; dup {
			t.Fatalf("duplicate key %q at slots %d and %d", k, j, i)
		}
		seen[k] = i
	}
	if len(s.keys) >= dictIndexThreshold && s.index == nil {
		t.Fatalf("%d keys and no index", len(s.keys))
	}
	if s.index != nil {
		if len(s.index) != len(s.keys) {
			t.Fatalf("index has %d entries for %d keys", len(s.index), len(s.keys))
		}
		for k, i := range s.index {
			if i < 0 || i >= len(s.keys) || s.keys[i] != k {
				t.Fatalf("index maps %q to slot %d, which holds %q", k, i, s.keys[i])
			}
		}
	}
}

// TestDictIndexedGetSetDelete exercises the >64-key indexed path for parity
// with the linear scan it replaces.
func TestDictIndexedGetSetDelete(t *testing.T) {
	const n = 500
	d := bigDict(n)
	checkInvariant(t, d)
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
	checkInvariant(t, d)
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

// TestDictDuplicateKeys pins the one duplicate-key rule (C121): a Dictionary
// holds at most one entry per key. NewDictionary, like the parser and like
// successive Sets, keeps the first position and the last value, so Delete
// removes the key entirely.
func TestDictDuplicateKeys(t *testing.T) {
	for _, n := range []int{2, 100} { // below and above the index threshold
		var entries []Entry
		for i := 0; i < n; i++ {
			entries = append(entries, Entry{Key: Name(fmt.Sprintf("K%d", i)), Value: Integer(i)})
		}
		entries = append(entries, Entry{Key: "K0", Value: Integer(-1)})
		d := NewDictionary(entries...)
		checkInvariant(t, d)
		if d.Len() != n {
			t.Fatalf("n=%d: Len = %d, want %d (duplicate collapsed)", n, d.Len(), n)
		}
		if got := d.Get("K0"); got != Integer(-1) {
			t.Fatalf("n=%d: Get(K0) = %v, want the last value -1", n, got)
		}
		if first := slices.Collect(d.Keys())[0]; first != "K0" {
			t.Fatalf("n=%d: first key = %q, want K0 at its first position", n, first)
		}
		if !d.Delete("K0") {
			t.Fatalf("n=%d: Delete(K0) = false", n)
		}
		if got, ok := d.Lookup("K0"); ok {
			t.Fatalf("n=%d: after Delete, Lookup(K0) = %v, true; want absent", n, got)
		}
		checkInvariant(t, d)
	}
}

// TestDictRandomOpsMatchModel drives random Set/Delete sequences across the
// index threshold in both directions and compares every read against a plain
// ordered model. It is the C98 guard: with the storage private and the index
// maintained by the mutators, no sequence of API calls can make a lookup
// disagree with the entries.
func TestDictRandomOpsMatchModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	type kv struct {
		k Name
		v Object
	}
	for round := 0; round < 20; round++ {
		d := &Dictionary{}
		var model []kv
		find := func(k Name) int {
			for i, e := range model {
				if e.k == k {
					return i
				}
			}
			return -1
		}
		keySpace := 40 + rng.Intn(120)
		for op := 0; op < 3000; op++ {
			k := Name(fmt.Sprintf("K%d", rng.Intn(keySpace)))
			// Bias toward growth in the first half and shrinkage in the second,
			// so the index is built, dropped and rebuilt.
			grow := op < 1500
			if (grow && rng.Intn(4) != 0) || (!grow && rng.Intn(4) == 0) {
				v := Integer(op)
				d.Set(k, v)
				if i := find(k); i >= 0 {
					model[i].v = v
				} else {
					model = append(model, kv{k, v})
				}
			} else {
				got := d.Delete(k)
				i := find(k)
				if got != (i >= 0) {
					t.Fatalf("round %d op %d: Delete(%s) = %v, model has it: %v", round, op, k, got, i >= 0)
				}
				if i >= 0 {
					model = slices.Delete(model, i, i+1)
				}
			}
			if op%97 == 0 {
				checkInvariant(t, d)
			}
			if d.Len() != len(model) {
				t.Fatalf("round %d op %d: Len = %d, model %d", round, op, d.Len(), len(model))
			}
		}
		checkInvariant(t, d)
		i := 0
		for k, v := range d.All() {
			if model[i].k != k || model[i].v != v {
				t.Fatalf("round %d: entry %d = %s:%v, model %s:%v", round, i, k, v, model[i].k, model[i].v)
			}
			i++
		}
		for j := 0; j < keySpace; j++ {
			k := Name(fmt.Sprintf("K%d", j))
			got, ok := d.Lookup(k)
			if mi := find(k); ok != (mi >= 0) || (ok && got != model[mi].v) {
				t.Fatalf("round %d: Lookup(%s) = %v,%v", round, k, got, ok)
			}
		}
	}
}

// TestDictCopySemantics pins the documented copy rule: a Dictionary copied by
// value refers to the same entries (like a map), and Clone is independent.
func TestDictCopySemantics(t *testing.T) {
	orig := bigDict(100)
	cp := *orig
	cp.Set("shared", Integer(1))
	if orig.Get("shared") != Integer(1) {
		t.Fatal("a value copy does not share entries with the original")
	}
	checkInvariant(t, orig)

	cl := orig.Clone()
	cl.Set("cloneonly", Integer(2))
	cl.Delete("K0")
	if orig.Get("cloneonly") != nil || orig.Get("K0") != Integer(0) {
		t.Fatal("mutating a Clone changed the original")
	}
	orig.Set("origonly", Integer(3))
	if cl.Get("origonly") != nil {
		t.Fatal("mutating the original changed its Clone")
	}
	checkInvariant(t, orig)
	checkInvariant(t, cl)
}

// TestDictNilAndZero: reads of a nil *Dictionary and of the zero value are
// defined and empty.
func TestDictNilAndZero(t *testing.T) {
	var nilD *Dictionary
	if nilD.Len() != 0 || nilD.Get("A") != nil || nilD.Has("A") || nilD.Delete("A") {
		t.Fatal("nil *Dictionary reads are not empty")
	}
	for range nilD.All() {
		t.Fatal("nil *Dictionary yielded an entry")
	}
	if nilD.Clone().Len() != 0 {
		t.Fatal("Clone of nil is not empty")
	}
	var z Dictionary
	if z.Len() != 0 || z.Get("A") != nil {
		t.Fatal("zero Dictionary is not empty")
	}
	z.Set("A", Integer(1))
	if z.Get("A") != Integer(1) {
		t.Fatal("Set on the zero Dictionary did not store")
	}
}

// TestDictIterationWithSet: replacing values while iterating visits each
// entry exactly once, in order.
func TestDictIterationWithSet(t *testing.T) {
	d := bigDict(100)
	var seen []Name
	for k, v := range d.All() {
		seen = append(seen, k)
		d.Set(k, v.(Integer)+1000)
	}
	if len(seen) != 100 || seen[0] != "K0" || seen[99] != "K99" {
		t.Fatalf("iteration visited %d entries (%v...)", len(seen), seen[:3])
	}
	if d.Get("K7") != Integer(1007) {
		t.Fatalf("Get(K7) = %v", d.Get("K7"))
	}
}

// TestDictConcurrentReads (C45): reads never write, so concurrent readers of a
// dictionary past the index threshold are race-free. Run under -race.
func TestDictConcurrentReads(t *testing.T) {
	d := bigDict(200)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if d.Get(Name(fmt.Sprintf("K%d", i))) != Integer(i) {
					t.Errorf("Get(K%d) wrong", i)
					return
				}
			}
			n := 0
			for range d.All() {
				n++
			}
			if n != 200 {
				t.Errorf("All yielded %d", n)
			}
			_ = d.Clone()
		}()
	}
	wg.Wait()
}

// TestDictLookupIsSubLinear is the C20/C22 root-cause guard: building and then
// sweeping a large dictionary must be well under quadratic. A linear-scan Set
// or Get makes this O(n^2); the index makes it O(n). The ceiling is generous
// so the test is not flaky but still fails hard on a regression to a scan.
func TestDictLookupIsSubLinear(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		const n = 200_000
		start := time.Now()
		d := bigDict(n)
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
			t.Fatalf("building and sweeping %d keys took %v — Set or Get regressed to a linear scan", n, elapsed)
		}
	})
}

func benchKeys(n int) []Name {
	keys := make([]Name, n)
	for i := range keys {
		keys[i] = Name(fmt.Sprintf("K%d", i))
	}
	return keys
}

// BenchmarkDictBuild measures building a dictionary by Set, the parser's path.
func BenchmarkDictBuild(b *testing.B) {
	for _, n := range []int{8, 64, 100_000} {
		keys := benchKeys(n)
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				d := &Dictionary{}
				for i, k := range keys {
					d.Set(k, Integer(i))
				}
			}
		})
	}
}

// BenchmarkDictGet measures lookups on a built dictionary.
func BenchmarkDictGet(b *testing.B) {
	for _, n := range []int{8, 64, 100_000} {
		keys := benchKeys(n)
		d := &Dictionary{}
		for i, k := range keys {
			d.Set(k, Integer(i))
		}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			i := 0
			for b.Loop() {
				_ = d.Get(keys[i%n])
				i++
			}
		})
	}
}
