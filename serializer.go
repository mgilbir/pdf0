package pdf0

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Serializer writes PDF objects to an io.Writer.
type Serializer struct {
	w      io.Writer
	offset int64 // tracks byte offset for xref generation
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

func (s *Serializer) writeString(str string) error {
	return s.write([]byte(str))
}

// WriteObject writes any PDF object to the output.
func (s *Serializer) WriteObject(obj Object) error {
	switch v := obj.(type) {
	case Boolean:
		return s.writeBoolean(v)
	case Integer:
		return s.writeInteger(v)
	case Real:
		return s.writeReal(v)
	case String:
		return s.writeStringObj(v)
	case Name:
		return s.writeName(v)
	case Array:
		return s.writeArray(v)
	case *Dictionary:
		return s.writeDictionary(v)
	case *Stream:
		return s.writeStream(v)
	case Null:
		return s.writeString("null")
	case *IndirectObject:
		return s.WriteIndirectObject(v)
	case IndirectRef:
		return s.writeIndirectRef(v)
	default:
		return fmt.Errorf("unsupported object type: %T", obj)
	}
}

func (s *Serializer) writeBoolean(b Boolean) error {
	if b {
		return s.writeString("true")
	}
	return s.writeString("false")
}

func (s *Serializer) writeInteger(i Integer) error {
	return s.writeString(strconv.FormatInt(int64(i), 10))
}

func (s *Serializer) writeReal(r Real) error {
	f := float64(r)
	str := strconv.FormatFloat(f, 'f', -1, 64)
	// Ensure there's a decimal point
	if !strings.Contains(str, ".") {
		str += ".0"
	}
	return s.writeString(str)
}

func (s *Serializer) writeStringObj(str String) error {
	if str.IsHex {
		return s.writeHexString(str.Value)
	}
	return s.writeLiteralString(str.Value)
}

func (s *Serializer) writeLiteralString(data []byte) error {
	if err := s.writeString("("); err != nil {
		return err
	}
	for _, b := range data {
		switch b {
		case '\\':
			if err := s.writeString("\\\\"); err != nil {
				return err
			}
		case '(':
			if err := s.writeString("\\("); err != nil {
				return err
			}
		case ')':
			if err := s.writeString("\\)"); err != nil {
				return err
			}
		case '\r':
			if err := s.writeString("\\r"); err != nil {
				return err
			}
		case '\n':
			if err := s.writeString("\\n"); err != nil {
				return err
			}
		case '\t':
			if err := s.writeString("\\t"); err != nil {
				return err
			}
		case '\b':
			if err := s.writeString("\\b"); err != nil {
				return err
			}
		case '\f':
			if err := s.writeString("\\f"); err != nil {
				return err
			}
		default:
			if err := s.write([]byte{b}); err != nil {
				return err
			}
		}
	}
	return s.writeString(")")
}

func (s *Serializer) writeHexString(data []byte) error {
	if err := s.writeString("<"); err != nil {
		return err
	}
	for _, b := range data {
		if err := s.writeString(fmt.Sprintf("%02X", b)); err != nil {
			return err
		}
	}
	return s.writeString(">")
}

func (s *Serializer) writeName(n Name) error {
	if err := s.writeString("/"); err != nil {
		return err
	}
	for i := 0; i < len(n); i++ {
		b := n[i]
		// Escape characters that must be hex-encoded in names:
		// - non-printable, whitespace, delimiters, #
		if b < '!' || b > '~' || isDelimiter(b) || b == '#' {
			if err := s.writeString(fmt.Sprintf("#%02X", b)); err != nil {
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

func (s *Serializer) writeArray(arr Array) error {
	if err := s.writeString("["); err != nil {
		return err
	}
	for i, obj := range arr {
		if i > 0 {
			if err := s.writeString(" "); err != nil {
				return err
			}
		}
		if err := s.WriteObject(obj); err != nil {
			return err
		}
	}
	return s.writeString("]")
}

func (s *Serializer) writeDictionary(dict *Dictionary) error {
	if err := s.writeString("<<"); err != nil {
		return err
	}
	for i, key := range dict.Keys {
		if err := s.writeString(" "); err != nil {
			return err
		}
		if err := s.writeName(key); err != nil {
			return err
		}
		if err := s.writeString(" "); err != nil {
			return err
		}
		if err := s.WriteObject(dict.Values[i]); err != nil {
			return err
		}
	}
	return s.writeString(" >>")
}

func (s *Serializer) writeStream(stream *Stream) error {
	// Update Length in dictionary
	dict := stream.Dict
	dict.Set("Length", Integer(len(stream.Data)))

	if err := s.writeDictionary(&dict); err != nil {
		return err
	}
	if err := s.writeString("\nstream\r\n"); err != nil {
		return err
	}
	if err := s.write(stream.Data); err != nil {
		return err
	}
	return s.writeString("\nendstream")
}

// WriteIndirectObject writes an indirect object definition to the output.
func (s *Serializer) WriteIndirectObject(obj *IndirectObject) error {
	if err := s.writeString(fmt.Sprintf("%d %d obj\n", obj.Number, obj.Generation)); err != nil {
		return err
	}
	if err := s.WriteObject(obj.Value); err != nil {
		return err
	}
	return s.writeString("\nendobj\n")
}

func (s *Serializer) writeIndirectRef(ref IndirectRef) error {
	return s.writeString(fmt.Sprintf("%d %d R", ref.Number, ref.Generation))
}
