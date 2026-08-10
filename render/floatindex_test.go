package render

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// The index against the scan it replaced.
//
// floatindex.go answers in logarithmic time what float.go used to answer by
// looking at every float, and the whole of its value depends on the two giving
// the same answer every time. A handful of hand-written cases would not settle
// that: the parts that are hard to get right are the ones that only show up in
// combination — a float added inside the span of one already there, a subtree
// taken back out, a suffix translated after the staircase had merged its
// breakpoints with its neighbours' — and those are combinations rather than
// cases.
//
// So the scan is kept here, verbatim, as the oracle, and random float sets are
// put through both. Every one of the surviving hand-written tests in
// float_test.go and floatextent_test.go pins a *specification* rule; this pins
// the equivalence, which is a different thing and is the only one that can be
// checked exhaustively enough to be believed.

// scanSpans, scanBand, scanNextBottom, scanClearance, scanBottom and scanTopMax
// are the bodies float.go had before the index existed. They are deliberately
// unfactored copies: an oracle that shared code with the thing it checks would
// agree with it for the wrong reason.
func scanSpans(a0, a1, b0, b1 style.Unit) bool {
	if b1 <= b0 {
		return a0 <= b0 && b0 < a1
	}
	return a0 < b1 && a1 > b0
}

func scanBand(boxes []placedFloat, top, bottom, lo, hi style.Unit) (left, right style.Unit) {
	left, right = lo, hi
	for _, f := range boxes {
		if f.rect.H <= 0 || !scanSpans(f.rect.Y, f.rect.Bottom(), top, bottom) {
			continue
		}
		if f.side == FloatLeft {
			if edge := f.rect.Right(); edge > left {
				left = edge
			}
			continue
		}
		if f.rect.X < right {
			right = f.rect.X
		}
	}
	if right < left {
		right = left
	}
	return left, right
}

func scanNextBottom(boxes []placedFloat, y style.Unit) (style.Unit, bool) {
	best, found := style.Unit(0), false
	for _, f := range boxes {
		if f.rect.H <= 0 {
			continue
		}
		if b := f.rect.Bottom(); b > y && (!found || b < best) {
			best, found = b, true
		}
	}
	return best, found
}

func scanClearance(boxes []placedFloat, clear ClearSide) style.Unit {
	if clear == ClearNone {
		return 0
	}
	var lowest style.Unit
	for _, f := range boxes {
		if !clear.clears(f.side) {
			continue
		}
		if b := f.rect.Bottom(); b > lowest {
			lowest = b
		}
	}
	return lowest
}

func scanBottom(boxes []placedFloat) style.Unit {
	var lowest style.Unit
	for _, f := range boxes {
		if b := f.rect.Bottom(); b > lowest {
			lowest = b
		}
	}
	return lowest
}

// scanTopMax is place's rule 5: a float may not start higher than any float
// before it.
func scanTopMax(boxes []placedFloat, top style.Unit) style.Unit {
	y := top
	for _, f := range boxes {
		if f.rect.Y > y {
			y = f.rect.Y
		}
	}
	return y
}

// indexTopMax is what place reads instead.
func indexTopMax(fc *floatContext, top style.Unit) style.Unit {
	fc.sync()
	if n := fc.idx.n; n > 0 && fc.idx.marks[n-1].topMax > top {
		return fc.idx.marks[n-1].topMax
	}
	return top
}

// randomFloat draws a float from a range small enough that spans collide,
// nest and abut constantly.
//
// The numbers are raw layout units rather than pixels because what is under test
// is arithmetic on coordinates, and a spread of a dozen units over a few hundred
// draws puts several floats on every boundary. A realistic spread would place
// every float somewhere of its own and exercise nothing.
//
// Zero and negative heights are drawn on purpose. A float of no height obstructs
// nothing but still counts for clearance and for the height of what contains it,
// and that split is exactly the "zero versus unset" mistake this repository keeps
// making.
func randomFloat(rng *rand.Rand) placedFloat {
	side := FloatRight
	if rng.IntN(2) == 0 {
		side = FloatLeft
	}
	return placedFloat{
		rect: Rect{
			X: style.Unit(rng.IntN(14) - 3),
			Y: style.Unit(rng.IntN(17) - 4),
			W: style.Unit(rng.IntN(7)),
			H: style.Unit(rng.IntN(10) - 2),
		},
		side: side,
	}
}

// checkAgainstScan asks every query both ways.
func checkAgainstScan(t *testing.T, fc *floatContext, want []placedFloat, rng *rand.Rand, where string) {
	t.Helper()

	for i := 0; i < 12; i++ {
		top := style.Unit(rng.IntN(21) - 5)
		bottom := top
		if rng.IntN(2) == 0 {
			// A real range as often as the degenerate one, since the two take
			// different paths through the staircase.
			bottom = top + style.Unit(rng.IntN(9))
		}
		lo := style.Unit(rng.IntN(5) - 2)
		hi := lo + style.Unit(rng.IntN(14))

		before := fc.consulted
		gotLeft, gotRight := fc.bandOver(top, bottom, lo, hi)
		wantLeft, wantRight := scanBand(want, top, bottom, lo, hi)
		if gotLeft != wantLeft || gotRight != wantRight {
			t.Fatalf("%s: bandOver(%d, %d, %d, %d) = (%d, %d), the scan says (%d, %d)\n%s",
				where, top, bottom, lo, hi, gotLeft, gotRight, wantLeft, wantRight, sketchFloats(want))
		}
		// consulted decides whether a subtree can be translated or has to be
		// laid out again, so a query that stops reporting that it changed
		// something is a correctness bug in layout, not a statistic.
		wantRead := 0
		if wantLeft != lo || wantRight != hi {
			wantRead = 1
		}
		if got := fc.consulted - before; got != wantRead {
			t.Fatalf("%s: bandOver(%d, %d, %d, %d) counted %d reads, want %d",
				where, top, bottom, lo, hi, got, wantRead)
		}

		y := style.Unit(rng.IntN(21) - 5)
		gotB, gotOK := fc.nextBottomBelow(y)
		wantB, wantOK := scanNextBottom(want, y)
		if gotOK != wantOK || (gotOK && gotB != wantB) {
			t.Fatalf("%s: nextBottomBelow(%d) = (%d, %v), the scan says (%d, %v)\n%s",
				where, y, gotB, gotOK, wantB, wantOK, sketchFloats(want))
		}
	}

	for _, clear := range []ClearSide{ClearNone, ClearLeft, ClearRight, ClearBoth} {
		before := fc.consulted
		got, want2 := fc.clearance(clear), scanClearance(want, clear)
		if got != want2 {
			t.Fatalf("%s: clearance(%v) = %d, the scan says %d\n%s",
				where, clear, got, want2, sketchFloats(want))
		}
		wantRead := 0
		if want2 > 0 {
			wantRead = 1
		}
		if n := fc.consulted - before; n != wantRead {
			t.Fatalf("%s: clearance(%v) counted %d reads, want %d", where, clear, n, wantRead)
		}
	}

	if got, want2 := fc.bottom(), scanBottom(want); got != want2 {
		t.Fatalf("%s: bottom() = %d, the scan says %d\n%s", where, got, want2, sketchFloats(want))
	}

	for _, top := range []style.Unit{-8, 0, 3, 20} {
		if got, want2 := indexTopMax(fc, top), scanTopMax(want, top); got != want2 {
			t.Fatalf("%s: the highest top at or below %d is %d, the scan says %d\n%s",
				where, top, got, want2, sketchFloats(want))
		}
	}

	checkStairs(t, fc, where)
}

// checkStairs pins the staircase's own shape, which no answer above can see.
//
// A staircase that is out of order or that carries two adjacent steps with the
// same value still answers most queries correctly — binary search on an unsorted
// array finds something — so a representation that has quietly rotted would show
// up as a rare wrong answer rather than as a failure. It is cheaper to say what
// the array must look like.
func checkStairs(t *testing.T, fc *floatContext, where string) {
	t.Helper()
	for name, s := range map[string]*stair{"left": &fc.idx.left, "right": &fc.idx.right} {
		for i, st := range s.steps {
			if i > 0 && st.y <= s.steps[i-1].y {
				t.Fatalf("%s: the %s staircase is out of order at %d: %v", where, name, i, s.steps)
			}
			if i > 0 {
				prev := s.steps[i-1]
				if prev.set == st.set && (!st.set || prev.edge == st.edge) {
					t.Fatalf("%s: the %s staircase repeats itself at %d: %v",
						where, name, i, s.steps)
				}
			}
		}
		if len(s.steps) > 0 && !s.steps[0].set {
			t.Fatalf("%s: the %s staircase begins with a gap: %v", where, name, s.steps)
		}
	}
}

func sketchFloats(boxes []placedFloat) string {
	out := ""
	for i, f := range boxes {
		out += fmt.Sprintf("  [%d] %v x=%d y=%d w=%d h=%d\n", i, f.side,
			f.rect.X, f.rect.Y, f.rect.W, f.rect.H)
	}
	return out
}

// TestFloatIndexAgreesWithTheScan is the property test proper: random floats,
// random rewinds and random translations, every query answered both ways.
func TestFloatIndexAgreesWithTheScan(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x5eed, 0xf10a7))
	for trial := 0; trial < 120; trial++ {
		fc := &floatContext{}
		var want []placedFloat
		for step := 0; step < 40; step++ {
			where := fmt.Sprintf("trial %d step %d", trial, step)
			switch n := len(want); {
			case n == 0 || rng.IntN(10) < 6:
				f := randomFloat(rng)
				fc.boxes = append(fc.boxes, f)
				want = append(want, f)
			case rng.IntN(2) == 0:
				// The rewind a subtree that has to be laid out again leaves
				// behind. Truncating to the whole length is included: it is the
				// no-op case, and a structure that only works when something
				// actually changes is a structure with a hidden state machine.
				k := rng.IntN(n + 1)
				fc.truncate(k)
				want = want[:k]
			default:
				// The translation settleIn applies to a subtree that added
				// floats and never read one. Negative deltas are drawn because
				// a collapsed margin can be smaller than the predicted one.
				from := rng.IntN(n + 1)
				dy := style.Unit(rng.IntN(11) - 5)
				fc.shift(from, dy)
				for i := from; i < len(want); i++ {
					want[i].rect.Y = want[i].rect.Y.Add(dy)
				}
			}
			checkAgainstScan(t, fc, want, rng, where)
		}
	}
}

// TestFloatIndexCatchesUpWithALiteralList covers the way the tests in
// floatextent_test.go build a context: the rectangles are assigned straight to
// the field and no float was ever placed.
//
// The index is derived state, so it has to notice that it is behind. If it only
// learned about floats through place, every such test would be asking an empty
// index and passing for that reason.
func TestFloatIndexCatchesUpWithALiteralList(t *testing.T) {
	fc := &floatContext{boxes: []placedFloat{
		{rect: Rect{X: 0, Y: 0, W: 50, H: 75}, side: FloatLeft},
		{rect: Rect{X: 300, Y: 40, W: 100, H: 20}, side: FloatRight},
	}}
	left, right := fc.bandAt(50, 0, 400)
	if left != 50 || right != 300 {
		t.Errorf("the band at 50 is [%d, %d], want [50, 300]", left, right)
	}
	if got := fc.bottom(); got != 75 {
		t.Errorf("the floats reach %d, want 75", got)
	}
}

// TestFloatIndexRewindRestoresTheEarlierAnswer states the invariant the undo
// depends on, separately from the random test that exercises it: a context
// rewound to k floats answers exactly as it did when it held k floats.
//
// The floats are chosen so that the later ones merge into the earlier ones'
// breakpoints — same spans, wider edges — because a rewind that merely dropped
// the last breakpoints would still be right for floats that never touched them.
func TestFloatIndexRewindRestoresTheEarlierAnswer(t *testing.T) {
	fc := &floatContext{}
	steps := []placedFloat{
		{rect: Rect{X: 0, Y: 0, W: 10, H: 100}, side: FloatLeft},
		{rect: Rect{X: 0, Y: 20, W: 40, H: 20}, side: FloatLeft},
		{rect: Rect{X: 0, Y: 0, W: 60, H: 200}, side: FloatLeft},
		{rect: Rect{X: 0, Y: 30, W: 5, H: 5}, side: FloatLeft},
	}
	var snapshots [][2]style.Unit
	for i := range steps {
		fc.boxes = append(fc.boxes, steps[i])
		l, r := fc.bandOver(25, 35, 0, 500)
		snapshots = append(snapshots, [2]style.Unit{l, r})
	}
	for k := len(steps); k > 0; k-- {
		fc.truncate(k)
		l, r := fc.bandOver(25, 35, 0, 500)
		if want := snapshots[k-1]; l != want[0] || r != want[1] {
			t.Errorf("rewound to %d floats the band is [%d, %d], want [%d, %d]",
				k, l, r, want[0], want[1])
		}
	}
	fc.truncate(0)
	if l, r := fc.bandOver(25, 35, 0, 500); l != 0 || r != 500 {
		t.Errorf("with no floats left the band is [%d, %d], want the whole block", l, r)
	}
}
