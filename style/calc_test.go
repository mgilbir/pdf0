package style

import (
	"math"
	"testing"

	"github.com/mgilbir/pdf0/css"
)

// calc(), and above all the two things about it that are not arithmetic: which
// expressions are lengths and which are not, and what happens to a percentage
// that cannot be resolved where the expression is read.

// calcCtx is a context with every relative unit fixed, so that an expression
// mixing them has one right answer.
var calcCtx = LengthContext{
	FontSize:         calcPx(20),
	RootFontSize:     calcPx(16),
	ZeroAdvance:      calcPx(10),
	FontMetricsKnown: true,
	XHeight:          calcPx(8),
	XHeightKnown:     true,
	ViewportWidth:    calcPx(1000),
	ViewportHeight:   calcPx(500),
	ViewportKnown:    true,
}

func calcPx(v float64) Unit {
	u, _ := FromPx(v)
	return u
}

// TestCalcArithmetic is the grammar, evaluated.
//
// Each case is one rule: the two levels of precedence, the parentheses that
// override them, the units resolved against the context, and a nested calc()
// which CSS allows and which has to mean the same as the parentheses.
func TestCalcArithmetic(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64 // in px
	}{
		{"calc(10px)", 10},
		{"calc(10px + 5px)", 15},
		{"calc(10px - 15px)", -5},
		{"calc(2em)", 40},
		{"calc(1rem + 1em)", 36},
		{"calc(3 * 4px)", 12},
		{"calc(4px * 3)", 12},
		{"calc(12px / 4)", 3},
		{"calc(15px / 2)", 7.5},
		// Multiplication binds tighter than addition, so this is not 9.
		{"calc(1px + 2px * 3)", 7},
		{"calc((1px + 2px) * 3)", 9},
		{"calc(1px + calc(2px * 2))", 5},
		// The two-level grammar over a chain, left to right.
		{"calc(100px / 4 / 5)", 5},
		{"calc(1px + 2px + 3px)", 6},
		// A number times a number is a number, and it may still scale a length.
		{"calc(2 * 3 * 4px)", 24},
		// The suite's own shape.
		{"calc(15.1ch / 4)", 37.75},
	} {
		got := mustLength(t, tc.in, calcCtx)
		if got.Kind != LengthAbsolute {
			t.Errorf("%s came out as kind %v, want an absolute length", tc.in, got.Kind)
			continue
		}
		if math.Abs(got.Value.Px()-tc.want) > 0.02 {
			t.Errorf("%s is %gpx, want %g", tc.in, got.Value.Px(), tc.want)
		}
	}
}

// TestCalcCarriesAPercentageThroughToLayout is the half that cannot be settled
// where the expression is read.
//
// What a percentage is a percentage *of* is the containing block, and the
// containing block is not known here. So the two halves travel together and are
// added at the end — and a calc() with only one of them is that one, because the
// mixed kind exists for the mixture rather than for the function.
func TestCalcCarriesAPercentageThroughToLayout(t *testing.T) {
	if got := mustLength(t, "calc(50%)", calcCtx); got.Kind != LengthPercent ||
		got.Percent != 50 {
		t.Errorf("calc(50%%) is %+v, want a plain 50 per cent", got)
	}
	mixed := mustLength(t, "calc(100% - 20px)", calcCtx)
	if mixed.Kind != LengthCalc {
		t.Fatalf("calc(100%% - 20px) is kind %v, want the mixed one", mixed.Kind)
	}
	if mixed.Percent != 100 || mixed.Value.Px() != -20 {
		t.Errorf("calc(100%% - 20px) is %+v, want 100 per cent less 20px", mixed)
	}
	// Resolved against a containing block, the two are added.
	if got, ok := mixed.Resolve(calcPx(200), true); !ok || got.Px() != 180 {
		t.Errorf("against 200px it is %gpx (ok=%v), want 180", got.Px(), ok)
	}
	// And against one that has not been decided it is as indefinite as a bare
	// percentage: "calc(50% + 1em)" of a height nothing has settled is not one em.
	if _, ok := mixed.Resolve(0, false); ok {
		t.Error("it resolved against an indefinite basis; the percentage half " +
			"cannot be answered there and the absolute half is not the answer")
	}
	// A percentage scales with the rest of the expression.
	half := mustLength(t, "calc((100% + 20px) / 2)", calcCtx)
	if half.Kind != LengthCalc || half.Percent != 50 || half.Value.Px() != 10 {
		t.Errorf("calc((100%% + 20px) / 2) is %+v, want 50 per cent plus 10px", half)
	}
}

// TestCalcRefusesWhatIsNotALength is the type checking, and it matters more than
// the arithmetic.
//
// An expression that does not typecheck is not a value this engine merely fails
// to compute: CSS says the declaration holding it is invalid. Guessing at one —
// reading "1px + 2" as three pixels, say — would put a number on the page that
// nobody wrote, so it produces no length at all and no report either.
//
// What CSS then asks for is that the declaration before it stand, and that this
// engine does not yet do — for calc() or for anything else. "width: 40px;
// width: banana" comes out auto as well, because declaration values are kept as
// text through the cascade and read by the property that wants them, so a value
// only turns out to be nonsense long after the one it replaced is gone. It is a
// gap in the cascade rather than in this file.
//
// An expression the input ended in the middle of is deliberately not among
// them. "calc(1px + 2px" with no closing bracket is three pixels and not an
// error: CSS Syntax closes an unclosed block when the input ends, with a
// parse error logged and the value kept, and every browser reads it so.
func TestCalcRefusesWhatIsNotALength(t *testing.T) {
	for _, in := range []string{
		"calc(1px + 2)",   // a length and a number do not add
		"calc(2 + 1px)",   //
		"calc(1px * 2px)", // two lengths multiply to an area
		"calc(1px / 2px)", // and divide to a number with nowhere to go
		"calc(2 / 1px)",   //
		"calc(1px / 0)",   // division by zero is not infinity, it is invalid
		"calc(4)",         // a number is not a length
		"calc()",          // and neither is nothing
		"calc(1px +)",     // nor half an expression
		"calc(+ 1px)",     //
		"calc(1px 2px)",   // two values with no operator
	} {
		vals, _ := css.ParseComponentValues(in)
		l, unsupported, ok := ParseLength(vals, calcCtx)
		if ok {
			t.Errorf("%s was read as the length %+v", in, l)
		}
		if unsupported {
			t.Errorf("%s was reported as unsupported; it is invalid CSS, and the "+
				"declaration holding it is dropped rather than noted", in)
		}
	}
}
