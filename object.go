package pdf0

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
}

func (Dictionary) pdfObject() {}

// Get returns the value associated with the given key, or nil if not found.
func (d *Dictionary) Get(key Name) Object {
	for i, k := range d.Keys {
		if k == key {
			return d.Values[i]
		}
	}
	return nil
}

// Set sets the value for the given key. If the key already exists, it updates the value.
// Otherwise, it appends a new key-value pair.
func (d *Dictionary) Set(key Name, value Object) {
	for i, k := range d.Keys {
		if k == key {
			d.Values[i] = value
			return
		}
	}
	d.Keys = append(d.Keys, key)
	d.Values = append(d.Values, value)
}

// Delete removes the key-value pair for the given key.
// Returns true if the key was found and removed.
func (d *Dictionary) Delete(key Name) bool {
	for i, k := range d.Keys {
		if k == key {
			d.Keys = append(d.Keys[:i], d.Keys[i+1:]...)
			d.Values = append(d.Values[:i], d.Values[i+1:]...)
			return true
		}
	}
	return false
}

// Len returns the number of key-value pairs.
func (d *Dictionary) Len() int {
	return len(d.Keys)
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
