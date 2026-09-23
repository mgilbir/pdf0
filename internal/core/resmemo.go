package core

import "github.com/mgilbir/pdf0/object"

// ResMemo memoises a result that depends on nothing but a content stream's
// bytes and the resource dictionary its names resolve in.
//
// Pages that share a content stream (a template, a stamped letterhead) very
// often each carry their own resource dictionary with the same entries, so a
// memo keyed by the dictionary's identity misses on every page, and whatever
// it guards is redone once per page: pages × stream size for a shape that is
// entirely legitimate. ResMemo matches resources by value instead
// (object.Equal, which compares references as references and does not follow
// them), against at most MaxResVariants dictionaries per stream, so a lookup
// costs a bounded number of comparisons. A stream drawn with more distinct
// resource dictionaries than that is recomputed for the rest, and bounded by
// the run's work meter as any other work.
//
// key is a stream or a key from View.ContentBytesAndKey; a nil key is never
// memoised. The zero value is ready to use.
type ResMemo[T any] struct {
	m map[*object.Stream][]resMemoEntry[T]
}

type resMemoEntry[T any] struct {
	res *object.Dictionary
	val T
}

// MaxResVariants bounds the resource dictionaries a ResMemo remembers per
// stream.
const MaxResVariants = 8

// Get returns the value memoised for (key, res), if any.
func (m *ResMemo[T]) Get(v View, key *object.Stream, res *object.Dictionary) (T, bool) {
	if key != nil {
		for _, e := range m.m[key] {
			v.Charge(1)
			if e.res == res || (e.res != nil && res != nil && object.Equal(e.res, res)) {
				return e.val, true
			}
		}
	}
	var zero T
	return zero, false
}

// Put remembers val for (key, res).
func (m *ResMemo[T]) Put(key *object.Stream, res *object.Dictionary, val T) {
	if key == nil {
		return
	}
	if m.m == nil {
		m.m = map[*object.Stream][]resMemoEntry[T]{}
	}
	if len(m.m[key]) < MaxResVariants {
		m.m[key] = append(m.m[key], resMemoEntry[T]{res, val})
	}
}

// DictInterner maps dictionaries that are equal by value (object.Equal, which
// compares references as references) to one representative, so that a memo
// keyed by dictionary identity treats every copy of a dictionary as the
// dictionary. Each page of a template commonly writes its own
// << /F1 4 0 R >> font dictionary; keyed by identity, those are as many
// different resources as there are pages.
//
// Candidates are grouped by the dictionary's keys in order, and at most
// MaxResVariants are kept per group, so interning one costs a bounded number
// of comparisons; a dictionary beyond that represents itself, which is a
// memo miss and never a wrong answer. The zero value is ready to use.
type DictInterner struct {
	canon   map[*object.Dictionary]*object.Dictionary
	byShape map[string][]*object.Dictionary
}

// Of returns the representative of d's value (nil for nil).
func (in *DictInterner) Of(v View, d *object.Dictionary) *object.Dictionary {
	if d == nil {
		return nil
	}
	if c, ok := in.canon[d]; ok {
		return c
	}
	if in.canon == nil {
		in.canon = map[*object.Dictionary]*object.Dictionary{}
		in.byShape = map[string][]*object.Dictionary{}
	}
	v.Charge(1 + d.Len())
	var shape []byte
	for k := range d.Keys() {
		shape = append(shape, k...)
		shape = append(shape, 0)
	}
	group := in.byShape[string(shape)]
	for _, c := range group {
		v.Charge(1)
		if object.Equal(c, d) {
			in.canon[d] = c
			return c
		}
	}
	if len(group) < MaxResVariants {
		in.byShape[string(shape)] = append(group, d)
	}
	in.canon[d] = d
	return d
}
