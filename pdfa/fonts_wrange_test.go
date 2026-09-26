package pdfa

import (
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

// TestParseCIDWidthsDoesNotExpand is the DoS guard for /W (audit 2026-07-26
// C1, and 2026-09-22 C10, its aggregate form): a range of any width, and any
// number of overlapping ranges, is read in time proportional to the number of
// entries, not the CIDs they cover. parseCIDWidths runs unconditionally in
// checkCIDFontConsistency, before any render gate, so an expansion would be
// reachable through the public validator. If the guard regresses this test
// exceeds its memory or time cap instead of passing.
func TestParseCIDWidthsDoesNotExpand(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		doc := mkView(map[int]*object.IndirectObject{}, nil)

		// [0 2000000000 500]: two billion CIDs in eleven bytes.
		w, _ := parseCIDWidths(doc, object.Array{object.Integer(0), object.Integer(2_000_000_000), object.Real(500)})
		if got, ok := w.width(123_456_789); !ok || got != 500 {
			t.Fatalf("width in a wide range = %v, %v; want 500", got, ok)
		}

		// 2,000 ranges of the whole 16-bit CID space each, plus 2,000 more
		// above it: 262 million CIDs in all. The last range wins.
		var arr object.Array
		for i := 0; i < 4000; i++ {
			arr = append(arr, object.Integer(0), object.Integer(65535), object.Integer(i))
		}
		many, _ := parseCIDWidths(doc, arr)
		if got, ok := many.width(40000); !ok || got != 3999 {
			t.Fatalf("overlapping ranges: width = %v, %v; want the last, 3999", got, ok)
		}

		// Inverted range declares nothing.
		inverted, _ := parseCIDWidths(doc, object.Array{object.Integer(100), object.Integer(10), object.Real(500)})
		if _, ok := inverted.width(50); ok {
			t.Fatal("inverted /W range declared a width")
		}

		// The array form (c [w0 w1 ...]).
		sub, _ := parseCIDWidths(doc, object.Array{object.Integer(5), object.Array{object.Real(1), object.Real(2), object.Real(3)}})
		for cid, want := range map[int]float64{5: 1, 6: 2, 7: 3} {
			if got, ok := sub.width(cid); !ok || got != want {
				t.Fatalf("array-form width(%d) = %v, %v; want %v", cid, got, ok, want)
			}
		}
		if _, ok := sub.width(8); ok {
			t.Fatal("array-form /W declared a width past its end")
		}
	})
}
