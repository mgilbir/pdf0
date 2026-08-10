package render

import (
	"testing"
	"time"

	"github.com/mgilbir/pdf0/style"
)

// TestFloatPlacementIsLinearInFloatCount guards the shape of bug that float
// placement had and that no correctness test could see.
//
// Every question a floatContext answered used to be a scan of every float in it,
// and placing one float asks several of them — so the work was quadratic while
// every answer was right. It showed only as time: laying out one block of empty
// 3×3 floats took 7 ms at a thousand floats and 2.0 s at thirty-two thousand,
// which is 32× the document for 280× the work, and a hundred thousand floats —
// seven hundred kilobytes of markup, which anyone can hand this engine — took
// eighteen seconds. It is 0.45 s now.
//
// The guard is a wall-clock bound, which is not an assertion to reach for
// lightly. It is the right one here because the quantity under test *is* time,
// and the margin is what makes it safe. The loop below takes 80 ms. The same
// loop written against the scans takes 0.57 s at sixteen thousand floats, 2.3 s
// at thirty-two thousand and 9.2 s at sixty-four thousand — exactly four times
// the time for twice the floats, three doublings running — which puts the two
// hundred thousand here at ninety seconds. A four-second bound is fifty times
// the honest cost, so no loaded machine trips it, and a twenty-second of the
// quadratic cost, so no reintroduction survives it.
//
// It drives the context directly rather than a document, for the reason
// html/parse_scale_test.go drives the tokenizer directly: the subject is one
// data structure, and going through Build would spend nine tenths of the time
// parsing and styling the markup that carries the floats and leave the bound
// measuring the wrong thing.
func TestFloatPlacementIsLinearInFloatCount(t *testing.T) {
	const floats = 200000
	size := Size{W: 3 * 64, H: 3 * 64}
	lo, hi := style.Unit(0), style.Unit(800*64)

	fc := &floatContext{}
	start := time.Now()
	for i := 0; i < floats; i++ {
		side := FloatLeft
		if i%7 == 0 {
			// Both staircases, and both are asked for every band.
			side = FloatRight
		}
		r := fc.place(size, side, 0, lo, hi)

		// The queries the rest of layout makes, so that a scan reintroduced in
		// any one of them is caught here and not only in place's own search.
		// They are asked about the float just placed, which is where the answers
		// are least likely to be trivial.
		fc.bandOver(r.Y, r.Bottom(), lo, hi)
		fc.nextBottomBelow(r.Y)
		if i%64 == 0 {
			fc.clearance(ClearBoth)
			fc.bottom()
		}
	}
	elapsed := time.Since(start)

	if n := len(fc.boxes); n != floats {
		t.Fatalf("%d floats were placed, want %d", n, floats)
	}
	// The floats tile the block, so the last one has to have been pushed a long
	// way down: an assertion that the search really ran, rather than a bound met
	// by a placement that gave up.
	if last := fc.boxes[floats-1].rect; last.Y <= 0 {
		t.Fatalf("the last float is at y=%d, so nothing was stacked", last.Y)
	}
	if elapsed > 4*time.Second {
		t.Errorf("placing %d floats took %v; it is linear work and should take "+
			"tens of milliseconds, so this is the quadratic scan coming back",
			floats, elapsed)
	}
}
