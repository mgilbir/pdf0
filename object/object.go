// Package object defines the PDF object model of ISO 32000-2 7.3: the Object
// interface and every value type a PDF document is built from.
//
// It is data only — no parsing, no serialization, no document-level resolution
// of references — and it depends on nothing else in this module, which is what
// let it be the first package split out of the flat root package.
//
// Every type here is aliased from the root pdf0 package, so pdf0.Dictionary and
// object.Dictionary are the same type: values pass between the two without
// conversion and either name may be used. The canonical documentation is here.
//
// # One representation per type
//
// The composite types — *Dictionary, *Stream and *IndirectObject — are Objects
// only as pointers: their marker method has a pointer receiver, so the compiler
// rejects a Dictionary, Stream or IndirectObject value where an Object is
// wanted. The other types — Boolean, Integer, Real, String, Name, Array, Null
// and IndirectRef — are Objects as values. Go's method-set rules make a pointer
// to one of those (*Name, *Integer, *Array, ...) satisfy the interface too, and
// no declaration can prevent that; such a pointer is not a PDF object. The
// serializer refuses one with an error, and Equal treats one as equal to
// nothing. Every consumer can therefore type-switch on exactly one form.
//
// # Dictionaries
//
// A Dictionary keeps its entries in insertion order, so a document round-trips
// with its keys where they were, and holds at most one entry per key: Set
// replaces the value of an existing key in place, and NewDictionary applies
// the same rule to its arguments, which is also how the parser treats a
// duplicated key in a file. Its storage is unexported and reached only through
// its methods. Reads never write: the lookup index that keeps Get O(1) on a
// large dictionary is maintained by the mutators, so any number of goroutines
// may read one Dictionary concurrently, and the index cannot go stale.
//
// A Dictionary behaves like a Go map: copying the struct (Stream.Dict or
// Document.Trailer copied by value, for instance) copies a reference to the
// same entries, and a mutation through either copy is seen through both. Use
// Clone for an independent copy, and NewStream to put a dictionary you built
// into a stream.
//
// A copy is therefore never a snapshot, which makes an accidental one a bug
// that is easy to write and hard to see. The module's own code is checked for
// them: built with the dictcopycheck tag, Dictionary carries a marker that go
// vet's copylocks analyzer reports on every copy, and
// scripts/check-dict-copies.sh fails on any copy whose line does not say why
// it is intended with a "dictcopy:" comment. Normal builds carry nothing.
//
// String.IsHex records which of the two syntactic forms a string arrived in, so
// it is written back the same way.
package object

import (
	"fmt"
	"iter"
	"math"
)

// Object is the interface all PDF objects implement. See the package
// documentation for the one form each type takes.
type Object interface {
	pdfObject() // marker method
}

// Boolean represents a PDF boolean value.
type Boolean bool

func (Boolean) pdfObject() {}

// Integer represents a PDF integer value.
type Integer int64

func (Integer) pdfObject() {}

// Real represents a PDF real (floating-point) value.
type Real float64

func (Real) pdfObject() {}

// String represents a PDF string value (literal or hexadecimal).
type String struct {
	Value []byte
	IsHex bool // preserve literal vs hex for round-tripping
}

func (String) pdfObject() {}

// Name represents a PDF name object.
type Name string

func (Name) pdfObject() {}

// Array represents a PDF array object.
type Array []Object

func (Array) pdfObject() {}

// Dictionary represents a PDF dictionary object: an ordered set of entries with
// at most one entry per key. The zero value is an empty dictionary ready to
// use. See the package documentation for its copy semantics.
type Dictionary struct {
	_ noCopy // zero-sized; see nocopy.go
	s *dictStore
}

// dictStore is the shared storage behind a Dictionary. keys and values are
// parallel and in insertion order. index, when non-nil, maps every key to its
// slot; it exists once the dictionary reaches dictIndexThreshold entries and is
// kept exact by every mutator, so readers only ever consult it.
type dictStore struct {
	keys   []Name
	values []Object
	index  map[Name]int
}

func (*Dictionary) pdfObject() {}

// Entry is one key/value pair, the argument form of NewDictionary.
type Entry struct {
	Key   Name
	Value Object
}

// dictIndexThreshold is the entry count at which a dictionary starts keeping a
// key→slot index. Below it a linear scan beats a map lookup and the map's
// allocation; above it the scan is what turns an attacker-sized dictionary —
// a /RoleMap, a /Names tree, a resource dictionary from a crafted object
// stream — into super-linear work in the parser and the validators.
const dictIndexThreshold = 64

// NewDictionary returns a dictionary holding entries in order. A key that
// appears more than once keeps the position of its first occurrence and the
// value of its last, exactly as successive Set calls would.
func NewDictionary(entries ...Entry) *Dictionary {
	d := &Dictionary{}
	if len(entries) > 0 {
		d.s = &dictStore{
			keys:   make([]Name, 0, len(entries)),
			values: make([]Object, 0, len(entries)),
		}
		for _, e := range entries {
			d.Set(e.Key, e.Value)
		}
	}
	return d
}

// find returns the slot of key, or -1. It never writes.
func (s *dictStore) find(key Name) int {
	if s.index != nil {
		if i, ok := s.index[key]; ok {
			return i
		}
		return -1
	}
	for i, k := range s.keys {
		if k == key {
			return i
		}
	}
	return -1
}

// buildIndex indexes every key; called by a mutator when the store reaches the
// threshold. Keys are unique, so each maps to its one slot.
func (s *dictStore) buildIndex() {
	s.index = make(map[Name]int, 2*len(s.keys))
	for i, k := range s.keys {
		s.index[k] = i
	}
}

// Len returns the number of entries. A nil *Dictionary has none.
func (d *Dictionary) Len() int {
	if d == nil || d.s == nil {
		return 0
	}
	return len(d.s.keys)
}

// Get returns the value stored under key, or nil if there is none. A nil
// *Dictionary has no entries. Get never modifies the dictionary.
func (d *Dictionary) Get(key Name) Object {
	v, _ := d.Lookup(key)
	return v
}

// Lookup returns the value stored under key and whether the key is present.
func (d *Dictionary) Lookup(key Name) (Object, bool) {
	if d == nil || d.s == nil {
		return nil, false
	}
	if i := d.s.find(key); i >= 0 {
		return d.s.values[i], true
	}
	return nil, false
}

// Has reports whether key is present.
func (d *Dictionary) Has(key Name) bool {
	_, ok := d.Lookup(key)
	return ok
}

// Set stores value under key. An existing key keeps its position and takes the
// new value; a new key is appended. Set is O(1) amortised at every size.
func (d *Dictionary) Set(key Name, value Object) {
	if d.s == nil {
		d.s = &dictStore{}
	}
	s := d.s
	if i := s.find(key); i >= 0 {
		s.values[i] = value
		return
	}
	s.keys = append(s.keys, key)
	s.values = append(s.values, value)
	if s.index != nil {
		s.index[key] = len(s.keys) - 1
	} else if len(s.keys) >= dictIndexThreshold {
		s.buildIndex()
	}
}

// Delete removes key and reports whether it was present. The entries after it
// keep their order.
func (d *Dictionary) Delete(key Name) bool {
	if d == nil || d.s == nil {
		return false
	}
	s := d.s
	i := s.find(key)
	if i < 0 {
		return false
	}
	copy(s.keys[i:], s.keys[i+1:])
	s.keys[len(s.keys)-1] = ""
	s.keys = s.keys[:len(s.keys)-1]
	copy(s.values[i:], s.values[i+1:])
	s.values[len(s.values)-1] = nil
	s.values = s.values[:len(s.values)-1]
	if s.index != nil {
		if len(s.keys) < dictIndexThreshold/2 {
			// Hysteresis: drop the index well below the threshold so a
			// dictionary hovering at it does not rebuild on every edit.
			s.index = nil
		} else {
			delete(s.index, key)
			for j := i; j < len(s.keys); j++ {
				s.index[s.keys[j]] = j
			}
		}
	}
	return true
}

// All yields the entries in order. Setting an existing key's value during the
// iteration is allowed (the entry is not yielded again); adding or deleting
// keys during it may skip entries or yield added ones, but is memory-safe.
func (d *Dictionary) All() iter.Seq2[Name, Object] {
	return func(yield func(Name, Object) bool) {
		if d == nil || d.s == nil {
			return
		}
		s := d.s
		for i := 0; i < len(s.keys); i++ {
			if !yield(s.keys[i], s.values[i]) {
				return
			}
		}
	}
}

// Keys yields the keys in order, with the iteration rules of All.
func (d *Dictionary) Keys() iter.Seq[Name] {
	return func(yield func(Name) bool) {
		for k := range d.All() {
			if !yield(k) {
				return
			}
		}
	}
}

// Values yields the values in key order, with the iteration rules of All.
func (d *Dictionary) Values() iter.Seq[Object] {
	return func(yield func(Object) bool) {
		for _, v := range d.All() {
			if !yield(v) {
				return
			}
		}
	}
}

// Clone returns an independent dictionary with the same entries in the same
// order: Set and Delete on either leave the other unchanged. The values
// themselves are shared, not deep-copied. Clone of a nil *Dictionary is an
// empty dictionary.
func (d *Dictionary) Clone() *Dictionary {
	if d == nil || d.s == nil || len(d.s.keys) == 0 {
		return &Dictionary{}
	}
	s := d.s
	c := &dictStore{
		keys:   append([]Name(nil), s.keys...),
		values: append([]Object(nil), s.values...),
	}
	if s.index != nil {
		c.index = make(map[Name]int, 2*len(c.keys))
		for k, i := range s.index {
			c.index[k] = i
		}
	}
	return &Dictionary{s: c}
}

// Stream represents a PDF stream object. Copying a Stream by value copies its
// Dict, which shares entries with the original (see Dictionary).
type Stream struct {
	Dict Dictionary
	Data []byte // raw (encoded) stream data
}

func (*Stream) pdfObject() {}

// NewStream returns a stream with the given data whose Dict holds dict's
// entries. The stream takes dict over rather than copying it: dict and the
// stream's Dict then refer to the same entries, which is what a caller that
// built dict for this stream wants. Pass dict.Clone() to keep them separate.
// A nil dict gives an empty Dict.
func NewStream(dict *Dictionary, data []byte) *Stream {
	st := &Stream{Data: data}
	if dict != nil {
		st.Dict.s = dict.s
	}
	return st
}

// Null represents the PDF null object.
type Null struct{}

func (Null) pdfObject() {}

// IndirectObject represents a PDF indirect object definition (N G obj ... endobj).
type IndirectObject struct {
	Number     int
	Generation int
	Value      Object
}

func (*IndirectObject) pdfObject() {}

// IndirectRef represents a PDF indirect object reference (N G R).
type IndirectRef struct {
	Number     int
	Generation int
}

func (IndirectRef) pdfObject() {}

// String returns a human-readable representation for debugging.
func (b Boolean) String() string {
	if b {
		return "true"
	}
	return "false"
}

func (i Integer) String() string {
	return fmt.Sprintf("%d", int64(i))
}

func (r Real) String() string {
	return fmt.Sprintf("%g", float64(r))
}

func (s String) String() string {
	if s.IsHex {
		return fmt.Sprintf("<%X>", s.Value)
	}
	return fmt.Sprintf("(%s)", s.Value)
}

func (n Name) String() string {
	return "/" + string(n)
}

func (n Null) String() string {
	return "null"
}

func (ref IndirectRef) String() string {
	return fmt.Sprintf("%d %d R", ref.Number, ref.Generation)
}

func (obj *IndirectObject) String() string {
	return fmt.Sprintf("%d %d obj", obj.Number, obj.Generation)
}

// Int and Float coerce a numeric object to a Go number, returning 0 for
// anything that is not an Integer or a Real. PDF is loose about which of the
// two a number arrives as — a /Width may be either — so nearly every consumer
// wants the value and not the distinction.
//
// Int truncates a Real toward zero and saturates one outside the range of int
// at math.MinInt or math.MaxInt; a NaN Real is 0. The values come from
// untrusted files, so the result is always defined, never the
// implementation-specific value of an out-of-range float conversion (which is
// math.MinInt on amd64 for any overflow, positive or negative). A caller that
// uses the result as a size must still bound it.
func Int(obj Object) int {
	switch n := obj.(type) {
	case Integer:
		// int is 64 bits on every platform this module supports; the clamp
		// keeps a 32-bit build defined too.
		if int64(n) > int64(math.MaxInt) {
			return math.MaxInt
		}
		if int64(n) < int64(math.MinInt) {
			return math.MinInt
		}
		return int(n)
	case Real:
		f := float64(n)
		switch {
		case math.IsNaN(f):
			return 0
		case f >= math.MaxInt: // float64(math.MaxInt) rounds up to 2^63
			return math.MaxInt
		case f <= math.MinInt:
			return math.MinInt
		}
		return int(f)
	}
	return 0
}
func Float(obj Object) float64 {
	switch n := obj.(type) {
	case Integer:
		return float64(n)
	case Real:
		return float64(n)
	}
	return 0
}

// RefNum returns the object number of an indirect reference, or 0 for any other
// object (including a direct value).
func RefNum(o Object) int {
	if r, ok := o.(IndirectRef); ok {
		return r.Number
	}
	return 0
}
