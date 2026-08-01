package pdf0

import (
	"github.com/mgilbir/pdf0/object"
)

// The ISO 32000-2 7.3 object model lives in the object subpackage. It is data
// only — no parsing, no serialization, no document-level reference resolution —
// so it has no dependency on anything else here, which is what let it move out
// of the root package first.
//
// These are type aliases, not new types, so pdf0.Dictionary and
// object.Dictionary are the same type: existing code that names either one
// keeps compiling, values pass between the two packages without conversion, and
// every method declared on the type is reachable through both names. The
// aliases exist so that the object model can be a browsable package of its own
// without moving the public API, which pdf0.Read, pdf0.Document and every
// validator signature are written in terms of.
//
// A regular subpackage rather than internal/: aliasing to an internal package
// renders in godoc as a bare "type Dictionary = core.Dictionary" with the
// fields and methods gone and nothing the reader may click through to, because
// the target cannot be imported from outside. For types whose methods are the
// documented API — Dictionary.Get, Dictionary.Set, Dictionary.Clone — that
// erases the documentation. A public target keeps it one hop away.

// The one-line summaries below are the first lines of the canonical comments in
// the object package, kept identical so the two cannot say different things.
// Anything longer — the method documentation, the parallel-slice rationale —
// lives there and is deliberately not repeated.
type (
	// Object is the interface all PDF objects implement.
	Object = object.Object
	// Boolean represents a PDF boolean value.
	Boolean = object.Boolean
	// Integer represents a PDF integer value.
	Integer = object.Integer
	// Real represents a PDF real (floating-point) value.
	Real = object.Real
	// String represents a PDF string value (literal or hexadecimal).
	String = object.String
	// Name represents a PDF name object.
	Name = object.Name
	// Array represents a PDF array object.
	Array = object.Array
	// Dictionary represents a PDF dictionary object.
	Dictionary = object.Dictionary
	// Stream represents a PDF stream object.
	Stream = object.Stream
	// Null represents the PDF null object.
	Null = object.Null
	// IndirectObject represents a PDF indirect object definition (N G obj ... endobj).
	IndirectObject = object.IndirectObject
	// IndirectRef represents a PDF indirect object reference (N G R).
	IndirectRef = object.IndirectRef
)
