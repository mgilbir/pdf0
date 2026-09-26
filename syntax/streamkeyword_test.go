package syntax

import (
	"bytes"
	"testing"
)

// TestStreamKeywordIsTheOneTaken: after ParseIndirectObject returns a stream,
// StreamKeyword is the offset of the keyword the parser took — after a ">>"
// with no white space, and not the letters "stream" in a string before it —
// and a parser that has read no stream reports none. The byte-level PDF/A
// rules measure a stream from here (audit 2026-09-22 C70).
func TestStreamKeywordIsTheOneTaken(t *testing.T) {
	cases := []struct {
		name, src string
		want      int64 // -1: not a stream
	}{
		{"after white space", "1 0 obj\n<< /Length 1 >>\nstream\nA\nendstream\nendobj\n", 24},
		{"directly after >>", "1 0 obj\n<< /Length 1 >>stream\nA\nendstream\nendobj\n", 23},
		{"after a string holding the word", "1 0 obj\n<< /N (a stream) /Length 1 >>stream\nA\nendstream\nendobj\n", 37},
		{"not a stream", "1 0 obj\n<< /N (a stream) >>\nendobj\n", -1},
	}
	for _, c := range cases {
		p := NewParser([]byte(c.src))
		if _, err := p.ParseIndirectObject(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, ok := p.StreamKeyword()
		if !ok {
			got = -1
		}
		if got != c.want {
			t.Errorf("%s: StreamKeyword = %d, want %d", c.name, got, c.want)
		}
		if c.want >= 0 && !bytes.HasPrefix([]byte(c.src[got:]), []byte("stream")) {
			t.Errorf("%s: offset %d is not the keyword", c.name, got)
		}
	}
}

// TestStreamLengthIsTheDeclaredOne: StreamLength is the /Length the stream
// declares as the parse read it — even when the data turned out longer — and
// reports nothing it did not read as a non-negative integer, so the caller
// resolves that itself.
func TestStreamLengthIsTheDeclaredOne(t *testing.T) {
	cases := []struct {
		name, src string
		want      int64
		ok        bool
	}{
		{"direct", "1 0 obj\n<< /Length 1 >>\nstream\nA\nendstream\nendobj\n", 1, true},
		{"wrong, the data recovered by search", "1 0 obj\n<< /Length 3 >>\nstream\nABCDEFGH\nendstream\nendobj\n", 3, true},
		{"indirect, with no resolver", "1 0 obj\n<< /Length 9 0 R >>\nstream\nA\nendstream\nendobj\n", 0, false},
		{"negative", "1 0 obj\n<< /Length -4 >>\nstream\nA\nendstream\nendobj\n", 0, false},
		{"absent", "1 0 obj\n<< >>\nstream\nA\nendstream\nendobj\n", 0, false},
		{"not a stream", "1 0 obj\n<< /Length 1 >>\nendobj\n", 0, false},
	}
	for _, c := range cases {
		p := NewParser([]byte(c.src))
		if _, err := p.ParseIndirectObject(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, ok := p.StreamLength()
		if ok != c.ok || ok && got != c.want {
			t.Errorf("%s: StreamLength = %d, %v; want %d, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
