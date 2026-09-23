package core

import (
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

func equalView(objs map[int]object.Object) View {
	v := View{Objects: map[int]*object.IndirectObject{}, Trailer: &object.Dictionary{}, Limits: DefaultLimits()}
	for n, o := range objs {
		v.Objects[n] = &object.IndirectObject{Number: n, Value: o}
	}
	return v
}

func ref(n int) object.IndirectRef { return object.IndirectRef{Number: n} }

// type2 is an exponential-interpolation function dictionary with the given C1,
// plus any extra entries.
func type2(c1 object.Object, extra ...object.Entry) *object.Dictionary {
	return object.NewDictionary(append([]object.Entry{
		{Key: "FunctionType", Value: object.Integer(2)},
		{Key: "Domain", Value: object.Array{object.Integer(0), object.Integer(1)}},
		{Key: "C0", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(0), object.Integer(0)}},
		{Key: "C1", Value: c1},
		{Key: "N", Value: object.Integer(1)},
	}, extra...)...)
}

func cmyk(c float64) object.Array {
	return object.Array{object.Real(c), object.Integer(0), object.Integer(0), object.Integer(0)}
}

func dictOf(k object.Name, val object.Object) *object.Dictionary {
	return object.NewDictionary(object.Entry{Key: k, Value: val})
}

// TestResolvedEqualComparesContent: two definitions are the same when their
// content is, however it is split into objects — which object.Equal cannot
// say, since it compares a reference by its number (audit 2026-09-22 C66).
func TestResolvedEqualComparesContent(t *testing.T) {
	v := equalView(map[int]object.Object{
		1: type2(cmyk(0.5)),
		2: type2(cmyk(0.5)),
		3: type2(ref(9)), // C1 written as its own object
		4: type2(cmyk(0.25)),
		9: cmyk(0.5),
	})
	extraRange := object.Entry{Key: "Range", Value: object.Array{object.Integer(0), object.Integer(1)}}
	cases := []struct {
		name string
		a, b object.Object
		want bool
	}{
		{"one object", ref(1), ref(1), true},
		{"identical content in two objects", ref(1), ref(2), true},
		{"a nested value inline and as its own object", ref(1), ref(3), true},
		{"a direct dictionary and a referenced one", type2(cmyk(0.5)), ref(1), true},
		{"different content", ref(1), ref(4), false},
		{"a dictionary with an extra key", ref(1), type2(cmyk(0.5), extraRange), false},
		{"Integer and Real of one value", object.Integer(1), object.Real(1), true},
		{"a reference to nothing is null", ref(77), object.Null{}, true},
		{"a null entry is an absent one",
			object.NewDictionary(object.Entry{Key: "A", Value: object.Integer(1)}, object.Entry{Key: "B", Value: ref(77)}),
			dictOf("A", object.Integer(1)), true},
		{"streams with the same dictionary and bytes",
			object.NewStream(dictOf("FunctionType", object.Integer(4)), []byte("{ pop }")),
			object.NewStream(dictOf("FunctionType", object.Integer(4)), []byte("{ pop }")), true},
		{"streams with different bytes",
			object.NewStream(dictOf("FunctionType", object.Integer(4)), []byte("{ pop }")),
			object.NewStream(dictOf("FunctionType", object.Integer(4)), []byte("{ pop 1 }")), false},
	}
	for _, c := range cases {
		if got := ResolvedEqual(v, c.a, c.b); got != c.want {
			t.Errorf("%s: ResolvedEqual = %v, want %v", c.name, got, c.want)
		}
		if got := ResolvedEqual(v, c.b, c.a); got != c.want {
			t.Errorf("%s (swapped): ResolvedEqual = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestResolvedEqualIsBoundedOnCyclesAndSharing: a comparison over the file's
// graph must terminate on a cycle and must not walk a shared DAG once per
// path — 2^60 paths here, compared once per pair of objects. It runs capped,
// because the failure it guards against is a hang.
func TestResolvedEqualIsBoundedOnCyclesAndSharing(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		chains := func(bottomA, bottomB int) View {
			objs := map[int]object.Object{
				// Two self-referential dictionaries, and one that differs.
				1: dictOf("Next", ref(1)),
				2: dictOf("Next", ref(2)),
				3: object.NewDictionary(object.Entry{Key: "Next", Value: ref(3)}, object.Entry{Key: "X", Value: object.Integer(1)}),
			}
			// Two chains of 60 levels, each level an array naming the next
			// level twice, ending in the given integers.
			const levels = 60
			for i := 0; i < levels; i++ {
				objs[100+i] = object.Array{ref(101 + i), ref(101 + i)}
				objs[300+i] = object.Array{ref(301 + i), ref(301 + i)}
			}
			objs[100+levels] = object.Integer(bottomA)
			objs[300+levels] = object.Integer(bottomB)
			return equalView(objs)
		}
		v := chains(1, 2)
		if !ResolvedEqual(v, ref(1), ref(2)) {
			t.Error("two identical self-referential dictionaries compared unequal")
		}
		if ResolvedEqual(v, ref(1), ref(3)) {
			t.Error("self-referential dictionaries with different keys compared equal")
		}
		if ResolvedEqual(v, ref(100), ref(300)) {
			t.Error("chains that differ at the bottom compared equal")
		}
		if !ResolvedEqual(chains(1, 1), ref(100), ref(300)) {
			t.Error("identical chains compared unequal")
		}
	})
}
