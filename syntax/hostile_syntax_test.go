package syntax

import (
	"bytes"
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// Parser and lexer behaviour on hostile or unusual input (audit 2026-09-22,
// C116, C117, C119, C120, C123).

// TestIntegerLookaheadKeepsValidInteger (C116): the three-token look-ahead that
// tells "N G R" from a plain integer must not fail a valid integer because the
// object after it is malformed. The malformation is reported by the parse that
// reaches it, exactly as it is after any other object.
func TestIntegerLookaheadKeepsValidInteger(t *testing.T) {
	for _, in := range []string{"5 <ZZ> ", "5 7 <ZZ>", "5 )"} {
		p := NewParser([]byte(in))
		obj, err := p.ParseObject()
		if err != nil {
			t.Errorf("%q: first object: unexpected error %v", in, err)
			continue
		}
		if obj != object.Integer(5) {
			t.Errorf("%q: first object = %v, want 5", in, obj)
		}
	}
	// The malformed object is still an error when it is the one parsed.
	p := NewParser([]byte("5 <ZZ> "))
	if _, err := p.ParseObject(); err != nil {
		t.Fatalf("first object: %v", err)
	}
	if _, err := p.ParseObject(); err == nil || !strings.Contains(err.Error(), "invalid hex") {
		t.Errorf("second object: err = %v, want the invalid hex string error", err)
	}
	// Inside an array the malformed element still fails the array.
	if _, err := NewParser([]byte("[5 <ZZ>]")).ParseObject(); err == nil {
		t.Error("[5 <ZZ>]: want an error for the malformed element")
	}
	// The sibling look-ahead: a dictionary is complete at ">>", and a
	// malformed token after it (where "stream" might have been) does not fail it.
	obj, err := NewParser([]byte("<< /A 1 >> <ZZ>")).ParseObject()
	if d, ok := obj.(*object.Dictionary); err != nil || !ok || d.Get("A") != object.Integer(1) {
		t.Errorf("<< /A 1 >> <ZZ>: %#v, %v; want the dictionary", obj, err)
	}
	// A real reference is unaffected.
	obj, err = NewParser([]byte("5 0 R")).ParseObject()
	if err != nil || obj != (object.IndirectRef{Number: 5}) {
		t.Errorf("5 0 R = %v, %v", obj, err)
	}
}

// TestParserOffset (C116): the parser reports where the parse is, not where
// look-ahead has lexed to, and SetOffset repositions it without stale
// look-ahead.
func TestParserOffset(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"5 /Next 7", 1}, // integer; look-ahead read /Next and 7
		{"5 0 R /X", 5},  // reference
		{"5 7 /X", 1},    // integer followed by an integer
		{"/Name  8", 5},  // name
		{"[1 2] 3", 5},   // array
		{"<< /A 1 >>x", 10},
		{"<< /Length 3 >>\nstream\nabc\nendstream 9", 36},
		{"<< /Length 3 >>\nstream\nabc\nendstreamendobj", 36},
	}
	for _, c := range cases {
		p := NewParser([]byte(c.in))
		if _, err := p.ParseObject(); err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got := p.Offset(); got != c.want {
			t.Errorf("%q: Offset() = %d, want %d", c.in, got, c.want)
		}
	}

	// SetOffset drops look-ahead: after parsing 5 (which looked ahead at 6 and
	// /X), moving to the /Y must parse /Y, not the buffered 6.
	p := NewParser([]byte("5 6 /X /Y"))
	if _, err := p.ParseObject(); err != nil {
		t.Fatal(err)
	}
	p.SetOffset(7)
	obj, err := p.ParseObject()
	if err != nil || obj != object.Name("Y") {
		t.Errorf("after SetOffset(7): %v, %v; want /Y", obj, err)
	}
}

// TestNumericRangeLeniency (C117): ISO 32000 treats the numeric limits as
// implementation limits, not syntax. An integer too large for int64 is read as
// a real, and a real too large for float64 is clamped, instead of failing the
// enclosing object.
func TestNumericRangeLeniency(t *testing.T) {
	cases := []struct {
		in   string
		want object.Object
	}{
		{"9223372036854775807", object.Integer(math.MaxInt64)},
		{"9223372036854775808", object.Real(9223372036854775808)},
		{"-9223372036854775809", object.Real(-9223372036854775809)},
		{"1" + strings.Repeat("0", 400) + ".0", object.Real(math.MaxFloat64)},
		{"-1" + strings.Repeat("0", 400) + ".0", object.Real(-math.MaxFloat64)},
		{"0." + strings.Repeat("0", 400) + "1", object.Real(0)},
	}
	for _, c := range cases {
		obj, err := NewParser([]byte(c.in)).ParseObject()
		if err != nil {
			t.Errorf("%.30q: %v", c.in, err)
			continue
		}
		if obj != c.want {
			t.Errorf("%.30q = %#v, want %#v", c.in, obj, c.want)
		}
	}
	// The enclosing dictionary survives.
	obj, err := NewParser([]byte("<< /A 9223372036854775808 /B 2 >>")).ParseObject()
	if err != nil {
		t.Fatalf("dictionary with an overflowing integer: %v", err)
	}
	if d := obj.(*object.Dictionary); d.Get("B") != object.Integer(2) {
		t.Errorf("/B = %v, want 2", d.Get("B"))
	}
}

// TestWhitespaceGapError (C119): a whitespace run longer than the token-gap
// bound is reported as that, not as an empty unknown keyword.
func TestWhitespaceGapError(t *testing.T) {
	in := "<< /A 1" + strings.Repeat(" ", maxTokenGap+64) + "/B 2 >>"
	_, err := NewParser([]byte(in)).ParseObject()
	if err == nil {
		t.Fatal("want an error")
	}
	if !errors.Is(err, ErrTokenGap) {
		t.Errorf("err = %v, want ErrTokenGap", err)
	}
	if strings.Contains(err.Error(), `unknown keyword ""`) {
		t.Errorf("err = %v still reads as an empty keyword", err)
	}
}

// TestEndstreamEndobjAdjacent (C120): "endstreamendobj" with no separator is
// accepted on the trusted-/Length path and on the search path.
func TestEndstreamEndobjAdjacent(t *testing.T) {
	for _, in := range []string{
		"1 0 obj << /Length 3 >>\nstream\nabc\nendstreamendobj",  // trusted /Length
		"1 0 obj << /Length 99 >>\nstream\nabc\nendstreamendobj", // wrong /Length: search
		"1 0 obj << >>\nstream\nabc\nendstreamendobj",            // no /Length: search
	} {
		io, err := NewParser([]byte(in)).ParseIndirectObject()
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		st, ok := io.Value.(*object.Stream)
		if !ok || string(st.Data) != "abc" {
			t.Errorf("%q: value = %#v, want stream with data abc", in, io.Value)
		}
	}
	// A keyword merely starting with "endstream" is still not endstream.
	if _, err := NewParser([]byte("<< /Length 3 >>\nstream\nabc\nendstreamX")).ParseObject(); err == nil {
		t.Error(`"endstreamX" accepted as endstream`)
	}
}

// sizedReader is an io.ReaderAt over a few bytes that does not advertise its
// size, so a reader cannot learn the true length up front.
type sizedReader struct{ b []byte }

func (r sizedReader) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(r.b).ReadAt(p, off)
}

// TestNewLexerFromReaderAtSize (C123): a negative size is an error, not a
// makeslice panic, and a size larger than the source is an error that does not
// first allocate the claimed size.
func TestNewLexerFromReaderAtSize(t *testing.T) {
	if _, err := NewLexerFromReaderAt(bytes.NewReader([]byte("abc")), -1); err == nil {
		t.Error("negative size: want an error")
	}
	for _, r := range []interface {
		ReadAt([]byte, int64) (int, error)
	}{bytes.NewReader([]byte("abc")), sizedReader{[]byte("abc")}} {
		const claimed = 1 << 32 // 4 GiB claimed, 3 bytes present
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := NewLexerFromReaderAt(r, claimed)
		runtime.ReadMemStats(&after)
		if err == nil {
			t.Errorf("%T: size beyond the source: want an error", r)
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
			t.Errorf("%T: allocated %d bytes for a 3-byte source claiming %d", r, grew, claimed)
		}
	}
	l, err := NewLexerFromReaderAt(sizedReader{[]byte("42")}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if tok, err := l.NextToken(); err != nil || string(tok.Value) != "42" {
		t.Errorf("token = %v, %v", tok, err)
	}
}
