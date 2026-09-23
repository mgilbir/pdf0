package core

import (
	"bytes"

	"github.com/mgilbir/pdf0/object"
)

// ResolvedEqual reports whether two values have the same content once every
// indirect reference in them is followed through the document: the question
// "do these define the same thing?", which a rule asks of two function
// dictionaries or two colour spaces that may be written as different objects.
//
// object.Equal is the value comparison and stays one: it has no document, so
// it compares an indirect reference by its number, and two tint transforms
// that are identical but stored as separate objects — or that differ only in
// whether a nested /Domain array is written inline — compare unequal (audit
// 2026-09-22 C66). This is the comparison with a document behind it.
//
// Scalars compare as object.Equal compares them (numbers across Integer and
// Real, with its one tolerance). Arrays and dictionaries compare element by
// element and key by key after resolving; streams compare their dictionaries
// that way and their raw bytes exactly. A reference to an object the document
// does not have is null, as ISO 32000-2 7.3.10 makes it.
//
// Cycles and shared structure are both bounded. A pair of references already
// being compared further up is assumed equal — the coinductive reading, under
// which two structures are equal unless some finite path tells them apart —
// and a pair already compared is answered from a memo, so a DAG that shares
// sub-objects is compared once per distinct pair of objects, not once per
// path. Nesting deeper than maxResolvedEqualDepth compares unequal.
func ResolvedEqual(v View, a, b object.Object) bool {
	c := resolvedEqual{v: v, done: map[[2]int]bool{}, busy: map[[2]int]bool{}}
	return c.equal(a, b, 0)
}

// maxResolvedEqualDepth bounds the recursion, as object.Equal's cap bounds
// its own: nested direct containers are the input's, and the goroutine stack
// is not.
const maxResolvedEqualDepth = 256

type resolvedEqual struct {
	v    View
	done map[[2]int]bool // pairs of object numbers, compared
	busy map[[2]int]bool // pairs of object numbers, being compared
}

func (c *resolvedEqual) equal(a, b object.Object, depth int) bool {
	if depth > maxResolvedEqualDepth {
		return false
	}
	ra, aRef := a.(object.IndirectRef)
	rb, bRef := b.(object.IndirectRef)
	if aRef && bRef {
		if ra.Number == rb.Number {
			return true // the same object
		}
		key := [2]int{ra.Number, rb.Number}
		if r, ok := c.done[key]; ok {
			return r
		}
		if c.busy[key] {
			return true
		}
		c.busy[key] = true
		r := c.values(a, b, depth)
		delete(c.busy, key)
		c.done[key] = r
		return r
	}
	return c.values(a, b, depth)
}

// values compares what two values resolve to.
func (c *resolvedEqual) values(x, y object.Object, depth int) bool {
	a, b := c.v.Resolve(x), c.v.Resolve(y)
	if a == nil || b == nil {
		_, aNull := a.(object.Null)
		_, bNull := b.(object.Null)
		return (a == nil || aNull) && (b == nil || bNull)
	}
	switch av := a.(type) {
	case object.Array:
		bv, ok := b.(object.Array)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !c.equal(av[i], bv[i], depth+1) {
				return false
			}
		}
		return true
	case *object.Dictionary:
		bv, ok := b.(*object.Dictionary)
		return ok && c.dicts(av, bv, depth)
	case *object.Stream:
		bv, ok := b.(*object.Stream)
		if !ok {
			return false
		}
		if av == bv {
			return true
		}
		return bytes.Equal(av.Data, bv.Data) && c.dicts(&av.Dict, &bv.Dict, depth)
	}
	return object.Equal(a, b)
}

// dicts compares two dictionaries key by key. A key whose value is null (or a
// reference to nothing) is the same as an absent key (ISO 32000-2 7.3.7).
func (c *resolvedEqual) dicts(a, b *object.Dictionary, depth int) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	present := func(d *object.Dictionary, k object.Name) (object.Object, bool) {
		val, ok := d.Lookup(k)
		if !ok {
			return nil, false
		}
		switch c.v.Resolve(val).(type) {
		case nil, object.Null:
			return nil, false
		}
		return val, true
	}
	for k := range a.Keys() {
		av, aok := present(a, k)
		bv, bok := present(b, k)
		if aok != bok {
			return false
		}
		if aok && !c.equal(av, bv, depth+1) {
			return false
		}
	}
	for k := range b.Keys() {
		if _, aok := present(a, k); !aok {
			if _, bok := present(b, k); bok {
				return false
			}
		}
	}
	return true
}
