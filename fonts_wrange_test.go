package pdf0

import "testing"

// TestParseCIDWidthsBoundsRange is the C1 DoS guard: a hostile /W range must not
// drive billions of map inserts. parseCIDWidths runs unconditionally in
// checkCIDFontConsistency (before any render gate), so an unbounded span is a
// reachable memory/CPU exhaustion via the public validator.
func TestParseCIDWidthsBoundsRange(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}

	// [0 2000000000 500] — a ~2e9-wide range. Must be skipped, not expanded.
	// If the guard regresses this test hangs / OOMs instead of failing cleanly.
	hostile := parseCIDWidths(doc, Array{Integer(0), Integer(2_000_000_000), Real(500)})
	if len(hostile) != 0 {
		t.Fatalf("over-wide /W range produced %d entries; must be skipped", len(hostile))
	}

	// Inverted range is skipped too.
	inverted := parseCIDWidths(doc, Array{Integer(100), Integer(10), Real(500)})
	if len(inverted) != 0 {
		t.Fatalf("inverted /W range produced %d entries; must be skipped", len(inverted))
	}

	// A well-formed range still expands correctly.
	valid := parseCIDWidths(doc, Array{Integer(10), Integer(12), Real(500)})
	for cid := 10; cid <= 12; cid++ {
		if valid[cid] != 500 {
			t.Fatalf("valid range: width[%d] = %v, want 500", cid, valid[cid])
		}
	}
	if len(valid) != 3 {
		t.Fatalf("valid range produced %d entries, want 3", len(valid))
	}

	// The array form (c [w0 w1 ...]) is unaffected by the range guard.
	arr := parseCIDWidths(doc, Array{Integer(5), Array{Real(1), Real(2), Real(3)}})
	if arr[5] != 1 || arr[6] != 2 || arr[7] != 3 || len(arr) != 3 {
		t.Fatalf("array-form /W parsed wrong: %v", arr)
	}

	// The full 16-bit CID space is a legal span and stays allowed.
	full := parseCIDWidths(doc, Array{Integer(0), Integer(65535), Real(1000)})
	if len(full) != 65536 {
		t.Fatalf("full CID-space range produced %d entries, want 65536", len(full))
	}
}
