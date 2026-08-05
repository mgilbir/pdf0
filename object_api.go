package pdf0

import (
	"github.com/mgilbir/pdf0/object"
)

// The ISO 32000-2 7.3 object model is declared in the object package, which
// sits at the bottom of the module: syntax, core, every validator and this
// package all read it, so it can depend on none of them. These aliases give it
// a second name at the top, where callers are.
//
// They exist because naming these types is unavoidable. A caller cannot build
// or inspect a PDF without writing Dictionary, Name and Integer — Document's
// own Trailer is an object.Dictionary and its Objects a map of
// *object.IndirectObject — so requiring a second import for the one group every
// caller touches buys nothing. Nothing else is re-exported: a PDF/A finding is
// pdfa.Violation, a conformance level is pdfa.PDFA2b, and each is named from
// the package that owns it.
//
// An alias is the same type, not a wrapper: pdf0.Dictionary and
// object.Dictionary are interchangeable in every position, and every method is
// reachable through both names. Because object is a regular package rather than
// internal, godoc renders each alias with a link to the real declaration, so
// the documentation is one hop away rather than lost — which is why object was
// made importable in the first place.
//
// This package's own code writes object.Dictionary, not the short name. The
// aliases are for callers; inside pdf0 the qualified form says which package a
// type comes from without the reader having to know.

// The one-line summaries are the first lines of the canonical comments in the
// object package, kept identical so the two cannot say different things.
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
