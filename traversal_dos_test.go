package pdf0

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
	"testing"
	"time"
)

// directDict builds an n-key dictionary K0..K(n-1). Set is O(1) amortised, so
// building it is linear and the cost measured below is the comparison's.
func directDict(n int) *object.Dictionary {
	d := &object.Dictionary{}
	for i := 0; i < n; i++ {
		d.Set(object.Name(fmt.Sprintf("K%d", i)), object.Integer(i))
	}
	return d
}

// TestDictionaryEqualLinear is the C22 guard: comparing two large dictionaries
// is linear, not O(n^2). (Duplicate keys, the other half of the old guard, are
// no longer representable; TestDictionaryEqualDuplicateKeys covers them.)
func TestDictionaryEqualLinear(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		const n = 50000
		a, b := directDict(n), directDict(n)
		start := time.Now()
		if !object.DictionaryEqual(a, b) {
			t.Fatal("two identical dictionaries compared unequal")
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Fatalf("comparing %d-key dictionaries took %v — dictionaryEqual regressed to O(n^2)", n, d)
		}

		// A single differing value late in the order is still found.
		b.Set("K49999", object.Integer(-1))
		if object.DictionaryEqual(a, b) {
			t.Fatal("dictionaries differing in one value compared equal")
		}
	})
}
