package pdf0

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func wantOut(t *testing.T, got []float64, ok bool, want ...float64) {
	t.Helper()
	if !ok {
		t.Fatalf("evalFunction returned ok=false, want %v", want)
	}
	if len(got) != len(want) {
		t.Fatalf("output len = %d %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if !approx(got[i], want[i]) {
			t.Errorf("out[%d] = %g, want %g (full %v)", i, got[i], want[i], want)
		}
	}
}

// funcDoc wraps a function object in a Document so streams can be decoded.
func funcDoc() *Document {
	return &Document{Objects: map[int]*IndirectObject{}, Version: "2.0"}
}

func TestFuncType2(t *testing.T) {
	d := funcDoc()
	fn := &Dictionary{}
	fn.Set("FunctionType", Integer(2))
	fn.Set("Domain", Array{Real(0), Real(1)})
	fn.Set("C0", Array{Real(0), Real(0), Real(0)})
	fn.Set("C1", Array{Real(1), Real(0.5), Real(0)})
	fn.Set("N", Real(1))

	// Linear (N=1) midpoint.
	out, ok := evalFunction(d, fn, []float64{0.5})
	wantOut(t, out, ok, 0.5, 0.25, 0)

	// N=2 squares the interpolation parameter.
	fn.Set("N", Real(2))
	out, ok = evalFunction(d, fn, []float64{0.5})
	wantOut(t, out, ok, 0.25, 0.125, 0)

	// Input clamped to Domain: x=2 -> clamped to 1 -> C1.
	fn.Set("N", Real(1))
	out, ok = evalFunction(d, fn, []float64{2})
	wantOut(t, out, ok, 1, 0.5, 0)

	// Defaults: no C0/C1 -> [0]..[1], single output.
	fn2 := &Dictionary{}
	fn2.Set("FunctionType", Integer(2))
	fn2.Set("Domain", Array{Real(0), Real(1)})
	fn2.Set("N", Real(1))
	out, ok = evalFunction(d, fn2, []float64{0.3})
	wantOut(t, out, ok, 0.3)
}

func TestFuncType3Stitching(t *testing.T) {
	d := funcDoc()
	// Two subfunctions over [0,1] split at 0.5.
	// Segment 0: maps [0,0.5] via Encode [0,1] into a type-2 that outputs x.
	// Segment 1: constant 10 (type-2 with C0=C1=10).
	sub0 := &Dictionary{}
	sub0.Set("FunctionType", Integer(2))
	sub0.Set("Domain", Array{Real(0), Real(1)})
	sub0.Set("C0", Array{Real(0)})
	sub0.Set("C1", Array{Real(1)})
	sub0.Set("N", Real(1))

	sub1 := &Dictionary{}
	sub1.Set("FunctionType", Integer(2))
	sub1.Set("Domain", Array{Real(0), Real(1)})
	sub1.Set("C0", Array{Real(10)})
	sub1.Set("C1", Array{Real(10)})
	sub1.Set("N", Real(1))

	fn := &Dictionary{}
	fn.Set("FunctionType", Integer(3))
	fn.Set("Domain", Array{Real(0), Real(1)})
	fn.Set("Functions", Array{sub0, sub1})
	fn.Set("Bounds", Array{Real(0.5)})
	fn.Set("Encode", Array{Real(0), Real(1), Real(0), Real(1)})

	// x=0.25 in segment 0: encoded from [0,0.5] to [0,1] -> 0.5 -> sub0 -> 0.5.
	out, ok := evalFunction(d, fn, []float64{0.25})
	wantOut(t, out, ok, 0.5)

	// x=0.0 -> 0.
	out, ok = evalFunction(d, fn, []float64{0})
	wantOut(t, out, ok, 0)

	// x=0.75 in segment 1 -> constant 10.
	out, ok = evalFunction(d, fn, []float64{0.75})
	wantOut(t, out, ok, 10)

	// x=1.0 (top) uses the last segment.
	out, ok = evalFunction(d, fn, []float64{1})
	wantOut(t, out, ok, 10)
}

func TestFuncType0Sampled(t *testing.T) {
	d := funcDoc()
	// 1 input, 1 output, Size=3 samples: values 0, 128, 255 at grid 0,1,2.
	// 8-bit samples, Domain [0,1], Range [0,1], Encode default [0,2],
	// Decode default = Range.
	st := &Stream{Dict: Dictionary{}, Data: []byte{0, 128, 255}}
	st.Dict.Set("FunctionType", Integer(0))
	st.Dict.Set("Domain", Array{Real(0), Real(1)})
	st.Dict.Set("Range", Array{Real(0), Real(1)})
	st.Dict.Set("Size", Array{Integer(3)})
	st.Dict.Set("BitsPerSample", Integer(8))

	// At the grid points.
	out, ok := evalFunction(d, st, []float64{0})
	wantOut(t, out, ok, 0)
	out, ok = evalFunction(d, st, []float64{0.5})
	wantOut(t, out, ok, 128.0/255.0)
	out, ok = evalFunction(d, st, []float64{1})
	wantOut(t, out, ok, 1)

	// Between grid 0 and 1 (x=0.25 -> encoded 0.5 -> halfway 0 and 128).
	out, ok = evalFunction(d, st, []float64{0.25})
	wantOut(t, out, ok, 64.0/255.0)
}

func TestFuncType0TwoInput(t *testing.T) {
	d := funcDoc()
	// 2 inputs, 1 output, Size [2,2] -> 4 samples. Bilinear over the unit square.
	// grid(0,0)=0, grid(1,0)=255, grid(0,1)=0, grid(1,1)=255.
	// Sample order: first input varies fastest: [g00, g10, g01, g11].
	st := &Stream{Dict: Dictionary{}, Data: []byte{0, 255, 0, 255}}
	st.Dict.Set("FunctionType", Integer(0))
	st.Dict.Set("Domain", Array{Real(0), Real(1), Real(0), Real(1)})
	st.Dict.Set("Range", Array{Real(0), Real(1)})
	st.Dict.Set("Size", Array{Integer(2), Integer(2)})
	st.Dict.Set("BitsPerSample", Integer(8))

	// Output depends only on the first input.
	out, ok := evalFunction(d, st, []float64{0.5, 0.7})
	wantOut(t, out, ok, 0.5)
	out, ok = evalFunction(d, st, []float64{0, 1})
	wantOut(t, out, ok, 0)
	out, ok = evalFunction(d, st, []float64{1, 0})
	wantOut(t, out, ok, 1)
}

// psFunc builds a type-4 function stream from a program string with the given
// input Domain and output Range.
func psFunc(program string, domain, rng []Object) *Stream {
	st := &Stream{Dict: Dictionary{}, Data: []byte(program)}
	st.Dict.Set("FunctionType", Integer(4))
	st.Dict.Set("Domain", Array(domain))
	st.Dict.Set("Range", Array(rng))
	return st
}

func TestFuncType4Arithmetic(t *testing.T) {
	d := funcDoc()
	dom := []Object{Real(0), Real(1)}
	rng := []Object{Real(-10), Real(10)}

	// add
	out, ok := evalFunction(d, psFunc("{ 2 3 add }", dom, rng), []float64{0})
	wantOut(t, out, ok, 5)
	// sub, mul chain: (10 - 3) then unused input popped
	out, ok = evalFunction(d, psFunc("{ pop 10 3 sub }", dom, rng), []float64{0.5})
	wantOut(t, out, ok, 7)
	// input doubled: x 2 mul
	out, ok = evalFunction(d, psFunc("{ 2 mul }", dom, rng), []float64{0.4})
	wantOut(t, out, ok, 0.8)
	// neg abs sqrt
	out, ok = evalFunction(d, psFunc("{ pop -4 abs sqrt }", dom, rng), []float64{0})
	wantOut(t, out, ok, 2)
	// div and truncate
	out, ok = evalFunction(d, psFunc("{ pop 7 2 div }", dom, rng), []float64{0})
	wantOut(t, out, ok, 3.5)
}

func TestFuncType4Transcendental(t *testing.T) {
	d := funcDoc()
	dom := []Object{Real(0), Real(1)}
	rng := []Object{Real(-10), Real(10)}
	// sin(90 deg) = 1
	out, ok := evalFunction(d, psFunc("{ pop 90 sin }", dom, rng), []float64{0})
	wantOut(t, out, ok, 1)
	// cos(0) = 1
	out, ok = evalFunction(d, psFunc("{ pop 0 cos }", dom, rng), []float64{0})
	wantOut(t, out, ok, 1)
	// exp: 2^10 = 1024, clamped by Range [-10,10] -> 10
	out, ok = evalFunction(d, psFunc("{ pop 2 10 exp }", dom, rng), []float64{0})
	wantOut(t, out, ok, 10)
	// ln(e) = 1 (use exp to make e: 1 exp of e? use 2.718281828)
	out, ok = evalFunction(d, psFunc("{ pop 2.718281828 ln }", dom, rng), []float64{0})
	wantOut(t, out, ok, 1)
	// atan(1,1) = 45
	out, ok = evalFunction(d, psFunc("{ pop 1 1 atan }", dom, []Object{Real(0), Real(360)}), []float64{0})
	wantOut(t, out, ok, 45)
}

func TestFuncType4IfElse(t *testing.T) {
	d := funcDoc()
	dom := []Object{Real(0), Real(1)}
	rng := []Object{Real(0), Real(100)}

	// if: x > 0.5 -> push 1 else leave 0. font.Program: x dup 0.5 gt { pop 1 } if
	prog := "{ dup 0.5 gt { pop 1 } if }"
	out, ok := evalFunction(d, psFunc(prog, dom, rng), []float64{0.8})
	wantOut(t, out, ok, 1)
	out, ok = evalFunction(d, psFunc(prog, dom, rng), []float64{0.2})
	wantOut(t, out, ok, 0.2)

	// ifelse: return 10 if x < 0.5 else 20.
	prog2 := "{ 0.5 lt { 10 } { 20 } ifelse }"
	out, ok = evalFunction(d, psFunc(prog2, dom, rng), []float64{0.1})
	wantOut(t, out, ok, 10)
	out, ok = evalFunction(d, psFunc(prog2, dom, rng), []float64{0.9})
	wantOut(t, out, ok, 20)
}

func TestFuncType4StackOps(t *testing.T) {
	d := funcDoc()
	dom := []Object{Real(0), Real(1), Real(0), Real(1), Real(0), Real(1)}
	rng := []Object{Real(0), Real(1), Real(0), Real(1), Real(0), Real(1)}

	// exch swaps two inputs -> outputs (b, a) then keep both, drop the third.
	// inputs a,b,c ; want to output c,b,a using roll: 3 -1 roll then done? Let's
	// test roll: a b c 3 1 roll -> c a b. Output all three.
	out, ok := evalFunction(d, psFunc("{ 3 1 roll }", dom, rng), []float64{0.1, 0.2, 0.3})
	// 3 1 roll on [0.1,0.2,0.3] -> [0.3,0.1,0.2]
	wantOut(t, out, ok, 0.3, 0.1, 0.2)

	// index: copy the 2nd-from-top. inputs a,b,c ; 2 index pushes a.
	// stack a b c -> a b c a ; drop to 3 outputs by popping? Range has 3 outputs;
	// keep last 3: b c a.
	out, ok = evalFunction(d, psFunc("{ 2 index exch pop exch pop }", dom, []Object{Real(0), Real(1)}), []float64{0.1, 0.2, 0.3})
	// 2 index -> a b c a ; exch -> a b a c ; pop -> a b a ; exch -> a a b ; pop -> a a
	// top output = a = 0.1
	wantOut(t, out, ok, 0.1)

	// copy: dup top 2. a b 2 copy -> a b a b, output last 2 -> a b.
	out, ok = evalFunction(d, psFunc("{ pop 2 copy add }", []Object{Real(0), Real(1), Real(0), Real(1), Real(0), Real(1)}, []Object{Real(0), Real(2), Real(0), Real(2), Real(0), Real(2)}), []float64{0.1, 0.2, 0.3})
	// pop -> a b (0.1,0.2) ; 2 copy -> a b a b ; add -> a b (a+b) => 0.1,0.2,0.3
	wantOut(t, out, ok, 0.1, 0.2, 0.3)
}

func TestFuncType4Comparison(t *testing.T) {
	d := funcDoc()
	dom := []Object{Real(0), Real(10)}
	rng := []Object{Real(0), Real(1)}
	// eq
	out, ok := evalFunction(d, psFunc("{ 5 eq { 1 } { 0 } ifelse }", dom, rng), []float64{5})
	wantOut(t, out, ok, 1)
	// and (boolean)
	out, ok = evalFunction(d, psFunc("{ pop true false and { 1 } { 0 } ifelse }", dom, rng), []float64{0})
	wantOut(t, out, ok, 0)
	// not (boolean)
	out, ok = evalFunction(d, psFunc("{ pop false not { 1 } { 0 } ifelse }", dom, rng), []float64{0})
	wantOut(t, out, ok, 1)
	// bitshift: 1 << 3 = 8, clamped to Range [0,10]
	out, ok = evalFunction(d, psFunc("{ pop 1 3 bitshift }", dom, []Object{Real(0), Real(10)}), []float64{0})
	wantOut(t, out, ok, 8)
}

func TestFuncMalformed(t *testing.T) {
	d := funcDoc()
	// Unknown function type.
	fn := &Dictionary{}
	fn.Set("FunctionType", Integer(9))
	fn.Set("Domain", Array{Real(0), Real(1)})
	if _, ok := evalFunction(d, fn, []float64{0.5}); ok {
		t.Error("unknown FunctionType should fail")
	}
	// Not a dict/stream.
	if _, ok := evalFunction(d, Integer(3), []float64{0}); ok {
		t.Error("non-function object should fail")
	}
	// Type 4 with unbalanced braces.
	if _, ok := evalFunction(d, psFunc("{ 1 2 add", []Object{Real(0), Real(1)}, []Object{Real(0), Real(1)}), []float64{0}); ok {
		t.Error("unterminated program should fail")
	}
	// Type 4 with unknown operator.
	if _, ok := evalFunction(d, psFunc("{ pop bogus }", []Object{Real(0), Real(1)}, []Object{Real(0), Real(1)}), []float64{0}); ok {
		t.Error("unknown operator should fail")
	}
	// Type 4 division by zero.
	if _, ok := evalFunction(d, psFunc("{ pop 1 0 div }", []Object{Real(0), Real(1)}, []Object{Real(0), Real(1)}), []float64{0}); ok {
		t.Error("division by zero should fail")
	}
}
