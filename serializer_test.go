package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

func serializeObject(t *testing.T, obj object.Object) string {
	t.Helper()
	var buf bytes.Buffer
	s := NewSerializer(&buf)
	if err := s.WriteObject(obj); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestSerializeBoolean(t *testing.T) {
	if got := serializeObject(t, object.Boolean(true)); got != "true" {
		t.Errorf("expected 'true', got %q", got)
	}
	if got := serializeObject(t, object.Boolean(false)); got != "false" {
		t.Errorf("expected 'false', got %q", got)
	}
}

func TestSerializeInteger(t *testing.T) {
	tests := []struct {
		val  object.Integer
		want string
	}{
		{0, "0"},
		{42, "42"},
		{-98, "-98"},
		{123456789, "123456789"},
	}
	for _, tt := range tests {
		if got := serializeObject(t, tt.val); got != tt.want {
			t.Errorf("Integer(%d): expected %q, got %q", tt.val, tt.want, got)
		}
	}
}

func TestSerializeReal(t *testing.T) {
	tests := []struct {
		val  object.Real
		want string
	}{
		{3.14, "3.14"},
		{-0.002, "-0.002"},
		{0.0, "0.0"},
		{34.0, "34.0"},
	}
	for _, tt := range tests {
		if got := serializeObject(t, tt.val); got != tt.want {
			t.Errorf("Real(%f): expected %q, got %q", tt.val, tt.want, got)
		}
	}
}

func TestSerializeLiteralString(t *testing.T) {
	tests := []struct {
		val  object.String
		want string
	}{
		{object.String{Value: []byte("Hello"), IsHex: false}, "(Hello)"},
		{object.String{Value: []byte(""), IsHex: false}, "()"},
		{object.String{Value: []byte("a(b)c"), IsHex: false}, "(a\\(b\\)c)"},
		{object.String{Value: []byte("a\\b"), IsHex: false}, "(a\\\\b)"},
		{object.String{Value: []byte("line1\nline2"), IsHex: false}, "(line1\\nline2)"},
	}
	for _, tt := range tests {
		if got := serializeObject(t, tt.val); got != tt.want {
			t.Errorf("String(%q): expected %q, got %q", tt.val.Value, tt.want, got)
		}
	}
}

func TestSerializeHexString(t *testing.T) {
	s := object.String{Value: []byte("Hello"), IsHex: true}
	got := serializeObject(t, s)
	if got != "<48656C6C6F>" {
		t.Errorf("expected '<48656C6C6F>', got %q", got)
	}
}

func TestSerializeName(t *testing.T) {
	tests := []struct {
		val  object.Name
		want string
	}{
		{"Type", "/Type"},
		{"Catalog", "/Catalog"},
		{"", "/"},
		{"Adobe Green", "/Adobe#20Green"},
		{".notdef", "/.notdef"},
	}
	for _, tt := range tests {
		if got := serializeObject(t, tt.val); got != tt.want {
			t.Errorf("Name(%q): expected %q, got %q", tt.val, tt.want, got)
		}
	}
}

func TestSerializeArray(t *testing.T) {
	arr := object.Array{object.Integer(1), object.Integer(2), object.Integer(3)}
	got := serializeObject(t, arr)
	if got != "[1 2 3]" {
		t.Errorf("expected '[1 2 3]', got %q", got)
	}
}

func TestSerializeEmptyArray(t *testing.T) {
	got := serializeObject(t, object.Array{})
	if got != "[]" {
		t.Errorf("expected '[]', got %q", got)
	}
}

func TestSerializeDictionary(t *testing.T) {
	dict := &object.Dictionary{}
	dict.Set("Type", object.Name("Catalog"))
	dict.Set("Pages", object.IndirectRef{Number: 3, Generation: 0})

	got := serializeObject(t, dict)
	expected := "<< /Type /Catalog /Pages 3 0 R >>"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSerializeEmptyDictionary(t *testing.T) {
	got := serializeObject(t, &object.Dictionary{})
	if got != "<< >>" {
		t.Errorf("expected '<< >>', got %q", got)
	}
}

func TestSerializeNull(t *testing.T) {
	got := serializeObject(t, object.Null{})
	if got != "null" {
		t.Errorf("expected 'null', got %q", got)
	}
}

func TestSerializeIndirectRef(t *testing.T) {
	got := serializeObject(t, object.IndirectRef{Number: 10, Generation: 0})
	if got != "10 0 R" {
		t.Errorf("expected '10 0 R', got %q", got)
	}
}

func TestSerializeIndirectObject(t *testing.T) {
	obj := &object.IndirectObject{
		Number:     1,
		Generation: 0,
		Value:      object.Integer(42),
	}
	got := serializeObject(t, obj)
	expected := "1 0 obj\n42\nendobj\n"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSerializeStream(t *testing.T) {
	stream := &object.Stream{
		Dict: object.Dictionary{},
		Data: []byte("Hello World"),
	}
	got := serializeObject(t, stream)
	expected := "<< /Length 11 >>\nstream\r\nHello World\nendstream"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSerializeOffset(t *testing.T) {
	var buf bytes.Buffer
	s := NewSerializer(&buf)

	if s.Offset() != 0 {
		t.Errorf("expected initial offset 0, got %d", s.Offset())
	}

	s.WriteObject(object.Integer(42))
	if s.Offset() != 2 { // "42" is 2 bytes
		t.Errorf("expected offset 2 after writing '42', got %d", s.Offset())
	}
}

// Round-trip tests: parse → serialize → re-parse → compare
func TestRoundTripBoolean(t *testing.T) {
	roundTripObject(t, "true")
	roundTripObject(t, "false")
}

func TestRoundTripInteger(t *testing.T) {
	roundTripObject(t, "42")
	roundTripObject(t, "-98")
	roundTripObject(t, "0")
}

func TestRoundTripReal(t *testing.T) {
	roundTripObject(t, "3.14")
	roundTripObject(t, "-0.002")
	roundTripObject(t, "0.0")
}

func TestRoundTripString(t *testing.T) {
	roundTripObject(t, "(Hello World)")
	roundTripObject(t, "<48656C6C6F>")
	roundTripObject(t, "()")
}

func TestRoundTripName(t *testing.T) {
	roundTripObject(t, "/Type")
	roundTripObject(t, "/Adobe#20Green")
}

func TestRoundTripArray(t *testing.T) {
	roundTripObject(t, "[1 2 3]")
	roundTripObject(t, "[]")
	roundTripObject(t, "[/Name (string) 42 true null]")
}

func TestRoundTripDictionary(t *testing.T) {
	roundTripObject(t, "<< /Type /Catalog /Pages 3 0 R >>")
	roundTripObject(t, "<< >>")
}

func TestRoundTripIndirectRef(t *testing.T) {
	roundTripObject(t, "10 0 R")
}

func TestRoundTripNull(t *testing.T) {
	roundTripObject(t, "null")
}

func roundTripObject(t *testing.T, input string) {
	t.Helper()

	// Parse
	p1 := NewParser([]byte(input))
	obj1, err := p1.ParseObject()
	if err != nil {
		t.Fatalf("parse %q: %v", input, err)
	}

	// Serialize
	var buf bytes.Buffer
	s := NewSerializer(&buf)
	if err := s.WriteObject(obj1); err != nil {
		t.Fatalf("serialize %q: %v", input, err)
	}
	serialized := buf.String()

	// Re-parse
	p2 := NewParser([]byte(serialized))
	obj2, err := p2.ParseObject()
	if err != nil {
		t.Fatalf("re-parse %q (serialized as %q): %v", input, serialized, err)
	}

	// Compare
	if !Equal(obj1, obj2) {
		t.Errorf("round-trip failed for %q: serialized as %q, re-parsed differs", input, serialized)
	}
}
