package fontcheck

import (
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

// TestParseCIDWidthsDoesNotExpand is the DoS guard for /W (audit 2026-07-26
// C1, and 2026-09-22 C10, its aggregate form): a range of any width, and any
// number of overlapping ranges, is read in time proportional to the number of
// entries, not the CIDs they cover. ParseCIDWidths runs unconditionally in
// LoadCID, before any render gate, so an expansion would be reachable through
// both public validators. If the guard regresses this test exceeds its memory
// or time cap instead of passing.
func TestParseCIDWidthsDoesNotExpand(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		doc := mkView(nil)

		// [0 2000000000 500]: two billion CIDs in eleven bytes.
		w, _ := ParseCIDWidths(doc, object.Array{object.Integer(0), object.Integer(2_000_000_000), object.Real(500)})
		if got, ok := w.Width(123_456_789); !ok || got != 500 {
			t.Fatalf("width in a wide range = %v, %v; want 500", got, ok)
		}

		// 2,000 ranges of the whole 16-bit CID space each, plus 2,000 more
		// above it: 262 million CIDs in all. The last range wins.
		var arr object.Array
		for i := 0; i < 4000; i++ {
			arr = append(arr, object.Integer(0), object.Integer(65535), object.Integer(i))
		}
		many, _ := ParseCIDWidths(doc, arr)
		if got, ok := many.Width(40000); !ok || got != 3999 {
			t.Fatalf("overlapping ranges: width = %v, %v; want the last, 3999", got, ok)
		}

		// Inverted range declares nothing.
		inverted, _ := ParseCIDWidths(doc, object.Array{object.Integer(100), object.Integer(10), object.Real(500)})
		if _, ok := inverted.Width(50); ok {
			t.Fatal("inverted /W range declared a width")
		}

		// The array form (c [w0 w1 ...]).
		sub, _ := ParseCIDWidths(doc, object.Array{object.Integer(5), object.Array{object.Real(1), object.Real(2), object.Real(3)}})
		for cid, want := range map[int]float64{5: 1, 6: 2, 7: 3} {
			if got, ok := sub.Width(cid); !ok || got != want {
				t.Fatalf("array-form width(%d) = %v, %v; want %v", cid, got, ok, want)
			}
		}
		if _, ok := sub.Width(8); ok {
			t.Fatal("array-form /W declared a width past its end")
		}
	})
}

func TestParseCIDWidths(t *testing.T) {
	doc := mkView(nil)
	// [ 1 [100 200] 5 7 300 ]  -> CID1=100, CID2=200, CID5..7=300
	w := object.Array{object.Integer(1), object.Array{object.Integer(100), object.Integer(200)}, object.Integer(5), object.Integer(7), object.Integer(300)}
	m, _ := ParseCIDWidths(doc, w)
	for cid, want := range map[int]float64{1: 100, 2: 200, 5: 300, 6: 300, 7: 300} {
		if got, ok := m.Width(cid); !ok || got != want {
			t.Errorf("width(%d) = %v, %v; want %v", cid, got, ok, want)
		}
	}
	for _, cid := range []int{0, 3, 4, 8, -1} {
		if _, ok := m.Width(cid); ok {
			t.Errorf("CID %d should be unset", cid)
		}
	}
	// A later entry overrides an earlier one where they overlap, as the
	// expansion into a map did.
	o, _ := ParseCIDWidths(doc, object.Array{object.Integer(0), object.Integer(9), object.Integer(1), object.Integer(4), object.Array{object.Integer(7), object.Integer(8)}})
	for cid, want := range map[int]float64{0: 1, 3: 1, 4: 7, 5: 8, 6: 1, 9: 1} {
		if got, ok := o.Width(cid); !ok || got != want {
			t.Errorf("overlap: width(%d) = %v, %v; want %v", cid, got, ok, want)
		}
	}
}
