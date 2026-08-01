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
// Two representation choices are load-bearing for round-tripping. Dictionary
// holds parallel key and value slices rather than a map, so key order is
// preserved and duplicate keys stay representable; String.IsHex records which of
// the two syntactic forms a string arrived in. Dictionary's lookup index is a
// lazily built cache owned by that Dictionary: copying a Dictionary by value and
// then mutating both copies is unsupported, exactly as it already is for the
// shared Keys/Values backing arrays.
package object

import "fmt"

// Object is the interface all PDF objects implement.
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

// Dictionary represents a PDF dictionary object.
// Uses parallel slices to preserve key insertion order for round-tripping.
type Dictionary struct {
	Keys   []Name
	Values []Object

	// index maps a key to the slot of its FIRST occurrence, giving Get/Set
	// amortized O(1) lookup once the dictionary grows past dictLookupThreshold.
	// Below the threshold a linear scan is cheaper than allocating the map.
	//
	// The index is built lazily and is self-healing: indexLen records len(Keys)
	// at build time, and any mismatch forces a rebuild. Structural changes that
	// shift slots (Delete) drop it. This keeps a large dictionary walked in a
	// loop — a /RoleMap, a /Names tree, a big resource dict — from being O(n)
	// per lookup, which is what turns an attacker-sized dictionary into a
	// super-linear CPU DoS through the validators. The index is owned by the
	// Dictionary: copying a Dictionary by value and then mutating both copies is
	// unsupported (as it already is for the shared Values backing array).
	index    map[Name]int
	indexLen int
}

func (Dictionary) pdfObject() {}

// dictLookupThreshold is the key count at or above which Get/Set maintain a
// name→slot index instead of scanning linearly. It matches the parser's own
// dictIndexThreshold: below it the linear scan beats a map allocation.
const dictLookupThreshold = 64

// buildIndex populates d.index with the slot of the first occurrence of each
// key, matching the first-match semantics of the linear scan.
func (d *Dictionary) buildIndex() {
	idx := make(map[Name]int, len(d.Keys))
	for i, k := range d.Keys {
		if _, ok := idx[k]; !ok {
			idx[k] = i
		}
	}
	d.index = idx
	d.indexLen = len(d.Keys)
}

// Get returns the value associated with the given key, or nil if not found.
func (d *Dictionary) Get(key Name) Object {
	if len(d.Keys) >= dictLookupThreshold {
		if d.index == nil || d.indexLen != len(d.Keys) {
			d.buildIndex()
		}
		if i, ok := d.index[key]; ok {
			return d.Values[i]
		}
		return nil
	}
	for i, k := range d.Keys {
		if k == key {
			return d.Values[i]
		}
	}
	return nil
}

// Set sets the value for the given key. If the key already exists, it updates the
// value in place (its slot is unchanged, so the lookup index stays valid).
// Otherwise it appends a new key-value pair and drops the index, which the next
// lookup rebuilds lazily.
//
// Set deliberately scans linearly rather than consulting the index: building the
// index here would make append-heavy construction O(n^2) (each Set would rebuild
// an O(n) map). The index accelerates the read-heavy Get path, which is where the
// super-linear validator traversals live; the parser, the one producer of very
// large dictionaries, populates Keys/Values directly and never routes through Set.
func (d *Dictionary) Set(key Name, value Object) {
	for i, k := range d.Keys {
		if k == key {
			d.Values[i] = value
			return
		}
	}
	d.Keys = append(d.Keys, key)
	d.Values = append(d.Values, value)
	// A new slot invalidates the index. Clearing this Dictionary's own field
	// (never writing into the possibly-shared map) keeps value-copies safe: an
	// aliasing copy still points at the old, still-correct read-only map.
	d.index = nil
}

// Delete removes the key-value pair for the given key.
// Returns true if the key was found and removed.
func (d *Dictionary) Delete(key Name) bool {
	for i, k := range d.Keys {
		if k == key {
			d.Keys = append(d.Keys[:i], d.Keys[i+1:]...)
			d.Values = append(d.Values[:i], d.Values[i+1:]...)
			d.index = nil // slots shifted; rebuild lazily on next lookup
			return true
		}
	}
	return false
}

// Len returns the number of key-value pairs.
func (d *Dictionary) Len() int {
	return len(d.Keys)
}

// Clone returns a copy of the dictionary whose Keys and Values live in fresh
// backing arrays, so that Set/Delete on the copy do not mutate the original.
// Value objects are shared, not deep-copied.
func (d *Dictionary) Clone() *Dictionary {
	keys := make([]Name, len(d.Keys))
	copy(keys, d.Keys)
	values := make([]Object, len(d.Values))
	copy(values, d.Values)
	return &Dictionary{Keys: keys, Values: values}
}

// Stream represents a PDF stream object.
type Stream struct {
	Dict Dictionary
	Data []byte // raw (encoded) stream data
}

func (Stream) pdfObject() {}

// Null represents the PDF null object.
type Null struct{}

func (Null) pdfObject() {}

// IndirectObject represents a PDF indirect object definition (N G obj ... endobj).
type IndirectObject struct {
	Number     int
	Generation int
	Value      Object
}

func (IndirectObject) pdfObject() {}

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

func (obj IndirectObject) String() string {
	return fmt.Sprintf("%d %d obj", obj.Number, obj.Generation)
}
