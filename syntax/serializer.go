package syntax

import (
	"fmt"
	"github.com/mgilbir/pdf0/object"
	"io"
	"math"
	"strconv"
	"strings"
)

// This file implements the object serializer: the byte-level writer for every
// Object type, in the syntax of ISO 32000-2 7.3 (names hex-escaped per 7.3.5,
// literal strings escaped, streams wrapped in stream/endstream). It emits one
// object at a time and tracks the running byte offset the caller turns into
// cross-reference entries; it knows nothing of file structure — the header,
// cross-reference section and trailer belong to document.go.
//
// Its contract is the parser's, mirrored: WriteObject emits only what
// ParseObject accepts and WriteIndirectObject only what ParseIndirectObject
// accepts, so every successful write reparses to an Equal object. A model the
// parser could not have produced is an error, never a panic and never bytes the
// parser rejects: a nil value, a negative object or generation number, an
// indirect object definition in a value position, a NUL in a name, a
// non-finite Real, a pointer to a scalar value. On an error the bytes already
// written are incomplete and must be discarded. Recursion is depth-capped:
// unlike anything the parser produces, a caller-constructed object graph can be
// cyclic.

// maxSerializeDepth bounds recursion through nested arrays/dictionaries so a
// cyclic direct object cannot exhaust the goroutine stack (an unrecoverable
// fatal error). The parser cannot build such cycles, but callers construct
// object graphs programmatically, and a dictionary can hold itself.
const maxSerializeDepth = 1000

// Serializer writes PDF objects to an io.Writer.
type Serializer struct {
	w      io.Writer
	offset int64 // tracks byte offset for xref generation
	depth  int   // current nesting depth (arrays/dictionaries/streams)
}

// NewSerializer creates a new Serializer writing to w.
func NewSerializer(w io.Writer) *Serializer {
	return &Serializer{w: w}
}

// Offset returns the current byte offset (total bytes written).
func (s *Serializer) Offset() int64 {
	return s.offset
}

func (s *Serializer) write(data []byte) error {
	n, err := s.w.Write(data)
	s.offset += int64(n)
	return err
}

func (s *Serializer) WriteString(str string) error {
	return s.write([]byte(str))
}

// WriteObject writes any PDF object. An *IndirectObject is written as a
// definition only when it is the object passed in, where it is equivalent to
// WriteIndirectObject; nested inside an array, a dictionary or another
// definition it is refused, because the parser rejects "N G obj" where a value
// is expected. Reference it with an IndirectRef instead.
func (s *Serializer) WriteObject(obj object.Object) error {
	if s.depth > maxSerializeDepth {
		return fmt.Errorf("maximum nesting depth %d exceeded (cyclic object graph?)", maxSerializeDepth)
	}
	s.depth++
	defer func() { s.depth-- }()

	switch v := obj.(type) {
	case object.Boolean:
		return s.writeBoolean(v)
	case object.Integer:
		return s.writeInteger(v)
	case object.Real:
		return s.writeReal(v)
	case object.String:
		return s.writeStringObj(v)
	case object.Name:
		return s.writeName(v)
	case object.Array:
		return s.writeArray(v)
	case *object.Dictionary:
		if v == nil {
			return fmt.Errorf("cannot serialize a nil *Dictionary")
		}
		return s.WriteDictionary(v)
	case *object.Stream:
		if v == nil {
			return fmt.Errorf("cannot serialize a nil *Stream")
		}
		return s.writeStream(v)
	case object.Null:
		return s.WriteString("null")
	case *object.IndirectObject:
		if s.depth == 1 {
			return s.WriteIndirectObject(v)
		}
		return fmt.Errorf("an indirect object definition (%d %d obj) cannot appear in a value position; reference it with an IndirectRef", indirectNumber(v), indirectGeneration(v))
	case object.IndirectRef:
		return s.writeIndirectRef(v)
	case nil:
		return fmt.Errorf("cannot serialize a nil object")
	case *object.Boolean, *object.Integer, *object.Real, *object.String, *object.Name,
		*object.Array, *object.Null, *object.IndirectRef:
		// These satisfy Object only through Go's method-set rules; the value
		// is the one representation (see the object package documentation).
		return fmt.Errorf("%T is a pointer to a PDF value, not a PDF object: store the value itself", obj)
	default:
		return fmt.Errorf("unsupported object type: %T", obj)
	}
}

func (s *Serializer) writeBoolean(b object.Boolean) error {
	if b {
		return s.WriteString("true")
	}
	return s.WriteString("false")
}

func (s *Serializer) writeInteger(i object.Integer) error {
	return s.WriteString(strconv.FormatInt(int64(i), 10))
}

func (s *Serializer) writeReal(r object.Real) error {
	f := float64(r)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("cannot serialize non-finite real %v as a PDF number", f)
	}
	str := strconv.FormatFloat(f, 'f', -1, 64)
	// Ensure there's a decimal point
	if !strings.Contains(str, ".") {
		str += ".0"
	}
	return s.WriteString(str)
}

func (s *Serializer) writeStringObj(str object.String) error {
	if str.IsHex {
		return s.writeHexString(str.Value)
	}
	return s.writeLiteralString(str.Value)
}

func (s *Serializer) writeLiteralString(data []byte) error {
	if err := s.WriteString("("); err != nil {
		return err
	}
	for _, b := range data {
		switch b {
		case '\\':
			if err := s.WriteString("\\\\"); err != nil {
				return err
			}
		case '(':
			if err := s.WriteString("\\("); err != nil {
				return err
			}
		case ')':
			if err := s.WriteString("\\)"); err != nil {
				return err
			}
		case '\r':
			if err := s.WriteString("\\r"); err != nil {
				return err
			}
		case '\n':
			if err := s.WriteString("\\n"); err != nil {
				return err
			}
		case '\t':
			if err := s.WriteString("\\t"); err != nil {
				return err
			}
		case '\b':
			if err := s.WriteString("\\b"); err != nil {
				return err
			}
		case '\f':
			if err := s.WriteString("\\f"); err != nil {
				return err
			}
		default:
			if err := s.write([]byte{b}); err != nil {
				return err
			}
		}
	}
	return s.WriteString(")")
}

func (s *Serializer) writeHexString(data []byte) error {
	if err := s.WriteString("<"); err != nil {
		return err
	}
	for _, b := range data {
		if err := s.WriteString(fmt.Sprintf("%02X", b)); err != nil {
			return err
		}
	}
	return s.WriteString(">")
}

func (s *Serializer) writeName(n object.Name) error {
	if err := s.WriteString("/"); err != nil {
		return err
	}
	for i := 0; i < len(n); i++ {
		b := n[i]
		// A NUL byte cannot appear in a name (ISO 32000-1 7.3.5); emitting
		// "#00" would produce a name this package's own lexer rejects. Refuse
		// rather than write unparseable output (audit C31).
		if b == 0 {
			return fmt.Errorf("name contains a NUL byte, which cannot be serialized")
		}
		// Escape characters that must be hex-encoded in names:
		// - non-printable, whitespace, delimiters, #
		if b < '!' || b > '~' || IsDelimiter(b) || b == '#' {
			if err := s.WriteString(fmt.Sprintf("#%02X", b)); err != nil {
				return err
			}
		} else {
			if err := s.write([]byte{b}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Serializer) writeArray(arr object.Array) error {
	if err := s.WriteString("["); err != nil {
		return err
	}
	for i, obj := range arr {
		if i > 0 {
			if err := s.WriteString(" "); err != nil {
				return err
			}
		}
		if err := s.WriteObject(obj); err != nil {
			return err
		}
	}
	return s.WriteString("]")
}

// WriteDictionary writes dict as a dictionary object.
func (s *Serializer) WriteDictionary(dict *object.Dictionary) error {
	if dict == nil {
		return fmt.Errorf("cannot serialize a nil *Dictionary")
	}
	if err := s.WriteString("<<"); err != nil {
		return err
	}
	for key, val := range dict.All() {
		if err := s.WriteString(" "); err != nil {
			return err
		}
		if err := s.writeName(key); err != nil {
			return err
		}
		if err := s.WriteString(" "); err != nil {
			return err
		}
		if err := s.WriteObject(val); err != nil {
			return err
		}
	}
	return s.WriteString(" >>")
}

func (s *Serializer) writeStream(stream *object.Stream) error {
	// Update Length in a copy so we don't mutate the caller's stream dictionary
	// (Dictionary shares its backing slices on a plain struct copy). Preserve an
	// indirect /Length (which points at a separate length object) rather than
	// shadowing it with an inline value; only synthesize /Length when it's
	// absent or already inline.
	dict := stream.Dict.Clone()
	if _, isRef := dict.Get("Length").(object.IndirectRef); !isRef {
		dict.Set("Length", object.Integer(len(stream.Data)))
	}

	if err := s.WriteDictionary(dict); err != nil {
		return err
	}
	if err := s.WriteString("\nstream\r\n"); err != nil {
		return err
	}
	if err := s.write(stream.Data); err != nil {
		return err
	}
	return s.WriteString("\nendstream")
}

// WriteIndirectObject writes an indirect object definition to the output. The
// object and generation numbers must be non-negative and the value must be a
// value, not another definition, which is what ParseIndirectObject accepts.
// Everything checkable before the first byte is checked before it.
func (s *Serializer) WriteIndirectObject(obj *object.IndirectObject) error {
	if obj == nil {
		return fmt.Errorf("cannot serialize a nil *IndirectObject")
	}
	if obj.Number < 0 || obj.Generation < 0 {
		return fmt.Errorf("indirect object %d %d: object and generation numbers must be non-negative", obj.Number, obj.Generation)
	}
	if obj.Value == nil {
		return fmt.Errorf("indirect object %d %d has no value", obj.Number, obj.Generation)
	}
	if _, nested := obj.Value.(*object.IndirectObject); nested {
		return fmt.Errorf("indirect object %d %d: its value is another indirect object definition", obj.Number, obj.Generation)
	}
	if err := s.WriteString(fmt.Sprintf("%d %d obj\n", obj.Number, obj.Generation)); err != nil {
		return err
	}
	if err := s.WriteObject(obj.Value); err != nil {
		return err
	}
	return s.WriteString("\nendobj\n")
}

func (s *Serializer) writeIndirectRef(ref object.IndirectRef) error {
	if ref.Number < 0 || ref.Generation < 0 {
		return fmt.Errorf("reference %d %d R: object and generation numbers must be non-negative", ref.Number, ref.Generation)
	}
	return s.WriteString(fmt.Sprintf("%d %d R", ref.Number, ref.Generation))
}

// indirectNumber and indirectGeneration read a possibly nil *IndirectObject for
// an error message.
func indirectNumber(o *object.IndirectObject) int {
	if o == nil {
		return 0
	}
	return o.Number
}

func indirectGeneration(o *object.IndirectObject) int {
	if o == nil {
		return 0
	}
	return o.Generation
}
