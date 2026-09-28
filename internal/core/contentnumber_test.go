package core

import (
	"math"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// TestContentNumbersArePDFNumbers: the content lexer calls every run that
// begins with a digit, a sign or a period a number token, and the value read
// from it is PDF number syntax (ISO 32000-2 7.3.3), the grammar the object
// parser applies — not strconv's, which took "1e3" as a thousand and "-Inf" as
// an infinity.
func TestContentNumbersArePDFNumbers(t *testing.T) {
	src := []byte(".5 -.25 4. +17 1e3 1.2.3 -Inf 0x1p3 -")
	want := []struct {
		v  float64
		ok bool
	}{{0.5, true}, {-0.25, true}, {4, true}, {17, true}, {0, false}, {0, false}, {0, false}, {0, false}, {0, false}}
	lx := NewContentLexer(Canceler{}, src)
	var tok ContentTok
	i := 0
	for lx.Next(&tok) {
		if tok.Kind != ContentNumber {
			t.Fatalf("token %q: kind %v, want a number token", tok.Raw, tok.Kind)
		}
		if i >= len(want) {
			t.Fatalf("more tokens than expected: %q", tok.Raw)
		}
		v, _, ok := tok.ParseNumber()
		if v != want[i].v || ok != want[i].ok || tok.Number() != want[i].v {
			t.Errorf("%q: ParseNumber = (%v, %v), Number = %v; want (%v, %v)", tok.Raw, v, ok, tok.Number(), want[i].v, want[i].ok)
		}
		i++
	}
	if i != len(want) {
		t.Fatalf("%d tokens, want %d", i, len(want))
	}

	// The decoded-operand view reads the same grammar.
	var got []float64
	for ct := range TokenizeContent(Canceler{}, []byte("[1e3 -Inf .5] TJ")) {
		if ct.Kind == KindNumber {
			got = append(got, ct.Number())
		}
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 0 || got[2] != 0.5 {
		t.Errorf("TokenizeContent numbers = %v, want [0 0 0.5]", got)
	}
}

// TestPostScriptNumbersArePostScript: a type 4 function's operands are
// PostScript numbers (PLRM 3.3.1). "NaN", "Inf" and "0x1p-1" are not; they are
// names, which the calculator does not define, so the function is refused.
// strconv read them as numbers, and a program returning NaN came back as an
// evaluated colour of NaN.
func TestPostScriptNumbersArePostScript(t *testing.T) {
	eval := func(prog string) ([]float64, bool) {
		st := object.NewStream(object.NewDictionary(
			object.Entry{Key: "FunctionType", Value: object.Integer(4)},
			object.Entry{Key: "Domain", Value: object.Array{object.Integer(0), object.Integer(1)}},
			object.Entry{Key: "Range", Value: object.Array{object.Integer(0), object.Integer(1)}},
		), []byte(prog))
		v := View{Objects: map[int]*object.IndirectObject{}, Limits: DefaultLimits(), Run: NewRun(&Recorder{})}
		return v.EvalFunction(st, []float64{0.25})
	}
	for _, c := range []struct {
		prog   string
		want   float64
		wantOK bool
	}{
		{"{ pop 5e-1 }", 0.5, true},
		{"{ pop .5E0 }", 0.5, true},
		{"{ pop -.002 abs }", 0.002, true},
		{"{ pop +1. }", 1, true},
		{"{ pop 1e-400 }", 0, true},
		{"{ pop NaN }", 0, false},
		{"{ pop Inf }", 0, false},
		{"{ pop -Inf }", 0, false},
		{"{ pop 0x1p-1 }", 0, false},
		{"{ pop 1e999 }", 0, false},
		{"{ pop 1e }", 0, false},
		{"{ pop 16#1 }", 0, false},
	} {
		out, ok := eval(c.prog)
		if ok != c.wantOK || (ok && (len(out) != 1 || math.Abs(out[0]-c.want) > 1e-12)) {
			t.Errorf("%s: (%v, %v), want ([%v], %v)", c.prog, out, ok, c.want, c.wantOK)
		}
	}
}
