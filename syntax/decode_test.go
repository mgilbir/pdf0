package syntax

import (
	"bytes"
	"testing"
)

// The shared decoders are the one definition of string and name syntax in the
// module; the object lexer and the content lexer both call them. These pin
// the cases on which the old content decoders disagreed with each other or
// with 7.3.4 (audit 2026-09-22 C150).

func TestDecodeLiteralString(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		end      int
		ok       bool
	}{
		{`(abc)`, "abc", 5, true},
		{`(a(b)c)x`, "a(b)c", 7, true},
		{`(a\)b)`, "a)b", 6, true},
		{`(\n\r\t\b\f\\\(\))`, "\n\r\t\b\f\\()", 18, true},
		{`(\101\1011\7)`, "AA1\a", 13, true},
		{`(\777)`, "\xff", 6, true},   // the high-order overflow is ignored
		{"(a\\\nb)", "ab", 6, true},   // \LF continues the line
		{"(a\\\r\nb)", "ab", 7, true}, // \CR LF too
		{"(a\\\rb)", "ab", 6, true},   // and \CR
		{"(a\r\nb\rc\nd)", "a\nb\nc\nd", 10, true},
		{`(\q)`, "q", 4, true}, // an unknown escape drops the backslash
		{`(abc`, "abc", 4, false},
		{`(abc\`, "abc", 5, false},
	} {
		got, end, ok := DecodeLiteralString([]byte(tc.in), 0)
		if string(got) != tc.want || end != tc.end || ok != tc.ok {
			t.Errorf("DecodeLiteralString(%q) = %q, %d, %v; want %q, %d, %v", tc.in, got, end, ok, tc.want, tc.end, tc.ok)
		}
		if e2, ok2 := LiteralStringEnd([]byte(tc.in), 0); e2 != end || ok2 != ok {
			t.Errorf("LiteralStringEnd(%q) = %d, %v; DecodeLiteralString ended at %d, %v", tc.in, e2, ok2, end, ok)
		}
	}
}

func TestDecodeHexString(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []byte
		ok   bool
	}{
		{"48656c6C6f", []byte("Hello"), true},
		{"48 65\n6c\t6c 6f", []byte("Hello"), true},
		{"414", []byte("A@"), true},
		{"", []byte{}, true},
		{"41zz42", []byte("AB"), false},
	} {
		got, ok := DecodeHexString([]byte(tc.in))
		if !bytes.Equal(got, tc.want) || ok != tc.ok {
			t.Errorf("DecodeHexString(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDecodeName(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		err      bool
	}{
		{"F1", "F1", false},
		{"F#31", "F1", false},
		{"A#20B", "A B", false},
		{"A#2", "A", true},
		{"A#zz", "A", true},
		{"A#00", "A", true},
	} {
		got, err := DecodeName([]byte(tc.in))
		if string(got) != tc.want || (err != nil) != tc.err {
			t.Errorf("DecodeName(%q) = %q, %v; want %q, error %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}
