package syntax

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// The serializer's promise is that whatever it writes, the parser reads back
// as the same object (audit 2026-09-22, C115). These tests hold it to that over
// inputs chosen to break an escaper or a number formatter, and over the shapes
// the parser used to reject.

// exactEqual is object.Equal made strict where Equal is deliberately lenient:
// reals must round-trip bit for bit, integers must stay integers, and a
// string must keep its literal or hex form.
func exactEqual(a, b object.Object) bool {
	switch av := a.(type) {
	case object.Real:
		bv, ok := b.(object.Real)
		return ok && math.Float64bits(float64(av)) == math.Float64bits(float64(bv))
	case object.Integer:
		bv, ok := b.(object.Integer)
		return ok && av == bv
	case object.String:
		bv, ok := b.(object.String)
		return ok && av.IsHex == bv.IsHex && bytes.Equal(av.Value, bv.Value)
	case object.Array:
		bv, ok := b.(object.Array)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !exactEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case *object.Dictionary:
		bv, ok := b.(*object.Dictionary)
		if !ok || av.Len() != bv.Len() {
			return false
		}
		for k, v := range av.All() {
			w, ok := bv.Lookup(k)
			if !ok || !exactEqual(v, w) {
				return false
			}
		}
		return true
	case *object.Stream:
		bv, ok := b.(*object.Stream)
		if !ok || !bytes.Equal(av.Data, bv.Data) {
			return false
		}
		// The serializer sets /Length; compare the rest.
		ad, bd := av.Dict.Clone(), bv.Dict.Clone()
		ad.Delete("Length")
		bd.Delete("Length")
		return exactEqual(ad, bd)
	}
	return object.Equal(a, b)
}

func adversarialObjects() []object.Object {
	var objs []object.Object

	// Names: every byte but NUL alone, all of them together, and the empty name.
	var all []byte
	for c := 1; c < 256; c++ {
		objs = append(objs, object.Name([]byte{byte(c)}))
		all = append(all, byte(c))
	}
	objs = append(objs, object.Name(all), object.Name(""), object.Name("#"), object.Name("##20"),
		object.Name("a b/c(d)e<f>g[h]i{j}k%l"))

	// Strings: every byte, in both forms; unbalanced parentheses; escapes; line ends.
	var every []byte
	for c := 0; c < 256; c++ {
		every = append(every, byte(c))
	}
	for _, s := range [][]byte{every, []byte("((("), []byte(")))"), []byte(")("), []byte(`\`), []byte(`\\(`),
		[]byte("\r\n\r"), []byte("\r"), []byte("a\rb\nc\r\nd"), []byte("\\\r\n"), {}, []byte("\x00")} {
		objs = append(objs, object.String{Value: s}, object.String{Value: s, IsHex: true})
	}

	// Numbers at and near the limits.
	for _, i := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64, math.MaxInt32, math.MinInt32 - 1} {
		objs = append(objs, object.Integer(i))
	}
	for _, f := range []float64{0, math.Copysign(0, -1), 0.5, -0.5, 1.0 / 3, -2.0 / 3, 1e-300, 5e-324,
		math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64, 1e20 + 16384, 123456789.123456789,
		9223372036854775808, 1, -1, 100} {
		objs = append(objs, object.Real(f))
	}

	// Composites built from the above, including a stream whose data contains
	// the keywords that end it.
	objs = append(objs,
		object.Array{},
		object.Array{object.Integer(1), object.Integer(0), object.Name("R")}, // not a reference
		object.Array{object.Integer(5), object.Integer(0), object.IndirectRef{Number: 7}},
		object.Array{object.Array{object.Array{}}, object.NewDictionary()},
		object.NewDictionary(
			object.Entry{Key: object.Name(all), Value: object.String{Value: every}},
			object.Entry{Key: "", Value: object.Null{}},
			object.Entry{Key: "N", Value: object.Real(math.MaxFloat64)},
			object.Entry{Key: "Ref", Value: object.IndirectRef{Number: math.MaxInt32, Generation: 65535}},
		),
		object.Boolean(true), object.Boolean(false), object.Null{},
	)
	for _, data := range [][]byte{{}, []byte("endstream"), []byte("x\nendstream\n"), []byte("endstreamendobj"), every} {
		st := &object.Stream{Data: data}
		st.Dict.Set("Filter", object.Name("Unknown"))
		objs = append(objs, st)
	}
	return objs
}

// TestSerializeParseRoundTrip: WriteObject then ParseObject is the identity,
// and the parse consumes exactly what was written.
func TestSerializeParseRoundTrip(t *testing.T) {
	for _, o := range adversarialObjects() {
		var buf bytes.Buffer
		if err := NewSerializer(&buf).WriteObject(o); err != nil {
			t.Errorf("%.60v: write: %v", o, err)
			continue
		}
		p := NewParser(buf.Bytes())
		back, err := p.ParseObject()
		if err != nil {
			t.Errorf("%.60v: wrote %.80q, which does not parse: %v", o, buf.Bytes(), err)
			continue
		}
		if !exactEqual(o, back) {
			t.Errorf("%.60v: wrote %.80q, read back %.60v", o, buf.Bytes(), back)
		}
		if p.Offset() != int64(buf.Len()) {
			t.Errorf("%.60v: parse consumed %d of %d bytes", o, p.Offset(), buf.Len())
		}
	}
}

// TestIndirectObjectRoundTrip: WriteIndirectObject then ParseIndirectObject is
// the identity, at the edges of the object and generation numbers.
func TestIndirectObjectRoundTrip(t *testing.T) {
	for _, io := range []*object.IndirectObject{
		{Number: 0, Value: object.Null{}},
		{Number: math.MaxInt32, Generation: 65535, Value: object.Integer(-1)},
		{Number: 1, Value: object.IndirectRef{Number: 2}},
		{Number: 3, Value: object.Integer(4)}, // "3 0 obj 4 endobj": the value is not "4 endobj ..."
		{Number: 5, Value: &object.Stream{Data: []byte("endstreamendobj")}},
	} {
		var buf bytes.Buffer
		if err := NewSerializer(&buf).WriteIndirectObject(io); err != nil {
			t.Errorf("%v: write: %v", io, err)
			continue
		}
		back, err := NewParser(buf.Bytes()).ParseIndirectObject()
		if err != nil {
			t.Errorf("%v: wrote %q, which does not parse: %v", io, buf.Bytes(), err)
			continue
		}
		if back.Number != io.Number || back.Generation != io.Generation || !exactEqual(io.Value, back.Value) {
			t.Errorf("%v: read back %v = %v", io, back, back.Value)
		}
	}
}

// TestFormerlyRejectedShapesRoundTrip: input the parser used to reject now
// parses, and what it parses to is written back in a form that parses to the
// same object again.
func TestFormerlyRejectedShapesRoundTrip(t *testing.T) {
	for _, in := range []string{
		"<< /A 9223372036854775808 /B -9223372036854775809 >>",                     // integers past int64 (C117)
		"[1" + strings.Repeat("0", 400) + ".0 -1" + strings.Repeat("0", 400) + "]", // reals past float64
		"<< /Length 3 >>\nstream\nabc\nendstreamendobj",                            // no separator (C120)
		"<< /Length 99 >>\nstream\nabc\nendstreamendobj",                           // same, on the search path
	} {
		first, err := NewParser([]byte(in)).ParseObject()
		if err != nil {
			t.Errorf("%.50q: %v", in, err)
			continue
		}
		var buf bytes.Buffer
		if err := NewSerializer(&buf).WriteObject(first); err != nil {
			t.Errorf("%.50q: write: %v", in, err)
			continue
		}
		second, err := NewParser(buf.Bytes()).ParseObject()
		if err != nil {
			t.Errorf("%.50q: rewritten as %.80q, which does not parse: %v", in, buf.Bytes(), err)
			continue
		}
		if !exactEqual(first, second) {
			t.Errorf("%.50q: %v, then %v after a round trip", in, first, second)
		}
	}
}
