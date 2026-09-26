package syntax

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// The serializer's contract (audit 2026-09-22, C115, C124): it writes only what
// this package's parser accepts, and a malformed model is an error, never a
// panic and never bytes the parser would reject.

func serialize(o object.Object) (string, error) {
	var b bytes.Buffer
	err := NewSerializer(&b).WriteObject(o)
	return b.String(), err
}

// mustNotPanic runs f and turns a panic into a test failure.
func mustNotPanic(t *testing.T, what string, f func() error) error {
	t.Helper()
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("%s panicked: %v", what, r)
			}
		}()
		err = f()
	}()
	return err
}

func TestSerializerRefusesWhatParserRejects(t *testing.T) {
	var b bytes.Buffer
	s := NewSerializer(&b)
	cases := []struct {
		name string
		f    func() error
	}{
		{"negative object number", func() error {
			return s.WriteIndirectObject(&object.IndirectObject{Number: -1, Value: object.Integer(1)})
		}},
		{"negative generation", func() error {
			return s.WriteIndirectObject(&object.IndirectObject{Number: 1, Generation: -1, Value: object.Integer(1)})
		}},
		{"nested indirect object in a dictionary", func() error {
			return s.WriteObject(object.NewDictionary(object.Entry{Key: "A", Value: &object.IndirectObject{Number: 3, Value: object.Integer(1)}}))
		}},
		{"nested indirect object in an array", func() error {
			return s.WriteObject(object.Array{&object.IndirectObject{Number: 3, Value: object.Integer(1)}})
		}},
		{"indirect object as an indirect object's value", func() error {
			return s.WriteIndirectObject(&object.IndirectObject{Number: 1, Value: &object.IndirectObject{Number: 2, Value: object.Integer(1)}})
		}},
		{"negative reference number", func() error {
			return s.WriteObject(object.IndirectRef{Number: -4})
		}},
		{"negative reference generation", func() error {
			return s.WriteObject(object.IndirectRef{Number: 4, Generation: -1})
		}},
		{"nil value", func() error { return s.WriteObject(nil) }},
		{"nil array element", func() error { return s.WriteObject(object.Array{nil}) }},
		{"indirect object with a nil value", func() error {
			return s.WriteIndirectObject(&object.IndirectObject{Number: 1})
		}},
		{"nil *Dictionary", func() error { return s.WriteDictionary(nil) }},
		{"nil *IndirectObject", func() error { return s.WriteIndirectObject(nil) }},
		{"nil *Stream", func() error { return s.WriteObject((*object.Stream)(nil)) }},
	}
	for _, c := range cases {
		b.Reset()
		err := mustNotPanic(t, c.name, c.f)
		if err == nil {
			t.Errorf("%s: wrote %q, want an error", c.name, b.String())
			// Whatever was written must at least be what the parser rejects,
			// which is the bug: confirm so the failure message says so.
			if _, perr := NewParser(b.Bytes()).ParseIndirectObject(); perr != nil && strings.Contains(c.name, "object") {
				t.Logf("%s: the parser rejects that output: %v", c.name, perr)
			}
		}
	}
}
