package core

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

func ntView() View {
	return View{Objects: map[int]*object.IndirectObject{}, Trailer: &object.Dictionary{}, Limits: DefaultLimits()}
}

func ntKeys(v View, root object.Object) string {
	entries, _ := v.NameTreeEntries(root)
	var keys []string
	for _, e := range entries {
		keys = append(keys, string(e.Key))
	}
	return strings.Join(keys, ",")
}

func str(s string) object.String { return object.String{Value: []byte(s)} }

// TestNameTreeInsert: the insert every writer that adds to a name tree uses
// (C44): sorted, replacing an equal key, descending /Kids by /Limits and
// widening them, and refusing a tree it cannot place a key in.
func TestNameTreeInsert(t *testing.T) {
	v := ntView()

	flat := object.NewDictionary()
	for _, k := range []string{"m", "a", "z", "m"} {
		if err := v.NameTreeInsert(flat, []byte(k), object.Integer(len(k))); err != nil {
			t.Fatal(err)
		}
	}
	if got := ntKeys(v, flat); got != "a,m,z" {
		t.Errorf("flat tree keys = %s, want a,m,z (the second m replaces the first)", got)
	}
	if flat.Get("Limits") != nil {
		t.Error("the root of a name tree must not carry /Limits")
	}

	leaf := func(n int, keys ...string) object.IndirectRef {
		var names object.Array
		for _, k := range keys {
			names = append(names, str(k), object.Integer(0))
		}
		v.Objects[n] = &object.IndirectObject{Number: n, Value: object.NewDictionary(
			object.Entry{Key: "Names", Value: names},
			object.Entry{Key: "Limits", Value: object.Array{str(keys[0]), str(keys[len(keys)-1])}},
		)}
		return object.IndirectRef{Number: n}
	}
	root := object.NewDictionary(object.Entry{Key: "Kids", Value: object.Array{leaf(2, "b", "d"), leaf(3, "p", "r")}})
	for _, k := range []string{"a", "e", "q", "s"} {
		if err := v.NameTreeInsert(root, []byte(k), object.Integer(1)); err != nil {
			t.Fatal(err)
		}
	}
	if got := ntKeys(v, root); got != "a,b,d,e,p,q,r,s" {
		t.Errorf("kids tree keys = %s", got)
	}
	lim := func(n int) string {
		d := v.ResolveDict(object.IndirectRef{Number: n})
		l, _ := d.Get("Limits").(object.Array)
		lo, _ := l[0].(object.String)
		hi, _ := l[1].(object.String)
		return string(lo.Value) + ".." + string(hi.Value)
	}
	if lim(2) != "a..d" || lim(3) != "e..s" {
		t.Errorf("limits = %s, %s; want a..d, e..s", lim(2), lim(3))
	}

	noLimits := object.NewDictionary(object.Entry{Key: "Kids", Value: object.Array{object.NewDictionary(object.Entry{Key: "Names", Value: object.Array{}})}})
	if err := v.NameTreeInsert(noLimits, []byte("x"), object.Integer(1)); err == nil {
		t.Error("inserting under a child without /Limits succeeded; it cannot be placed without guessing")
	}

	removed := v.NameTreeRemove(root, func(k []byte, _ object.Object) bool { return string(k) == "q" || string(k) == "a" })
	if removed != 2 || ntKeys(v, root) != "b,d,e,p,r,s" {
		t.Errorf("remove: %d removed, keys %s", removed, ntKeys(v, root))
	}
}

// TestNameTreeCycleIsNotFollowed: a /Kids cycle is a structure the file
// controls; the walk stops and says it is incomplete, and the insert refuses.
func TestNameTreeCycleIsNotFollowed(t *testing.T) {
	v := ntView()
	node := object.NewDictionary(object.Entry{Key: "Limits", Value: object.Array{str("a"), str("z")}})
	v.Objects[5] = &object.IndirectObject{Number: 5, Value: node}
	node.Set("Kids", object.Array{object.IndirectRef{Number: 5}})
	if _, complete := v.NameTreeEntries(object.IndirectRef{Number: 5}); complete {
		t.Error("a cyclic name tree was reported complete")
	}
	if err := v.NameTreeInsert(node, []byte("m"), object.Integer(1)); err == nil {
		t.Error("inserting into a cyclic name tree succeeded")
	}
}
