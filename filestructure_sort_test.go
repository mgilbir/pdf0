package pdf0

import (
	"testing"
	"time"
)

// TestSortInt64LargeInputFast guards against the quadratic offset sort: the
// object offsets fed to sortInt64 come from a map and are unordered, so an
// insertion sort was O(n^2) and a many-object file spent tens of seconds there.
// A large reverse-ordered slice (insertion sort's worst case) must sort quickly
// and correctly.
func TestSortInt64LargeInputFast(t *testing.T) {
	const n = 300000
	a := make([]int64, n)
	for i := range a {
		a[i] = int64(n - i) // reverse order
	}
	start := time.Now()
	sortInt64(a)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("sortInt64 took %v on %d reverse-ordered elements — an insertion sort would be quadratic", d, n)
	}
	for i := 1; i < len(a); i++ {
		if a[i-1] > a[i] {
			t.Fatalf("not sorted at index %d: %d > %d", i, a[i-1], a[i])
		}
	}
}
