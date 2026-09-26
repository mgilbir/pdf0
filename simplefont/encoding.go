package simplefont

import (
	"iter"
	"sync"
)

// Encoding is one of the standard Latin-text encodings a simple font's
// /Encoding or /BaseEncoding names (ISO 32000-2 9.6.5, Annex D.2).
type Encoding int

const (
	StandardEncoding Encoding = iota + 1
	MacRomanEncoding
	WinAnsiEncoding
)

// EncodingNamed returns the encoding a PDF name denotes: "StandardEncoding",
// "MacRomanEncoding" or "WinAnsiEncoding". MacExpertEncoding, which is not a
// Latin-text encoding, and anything else report false.
func EncodingNamed(name string) (Encoding, bool) {
	switch name {
	case "StandardEncoding":
		return StandardEncoding, true
	case "MacRomanEncoding":
		return MacRomanEncoding, true
	case "WinAnsiEncoding":
		return WinAnsiEncoding, true
	}
	return 0, false
}

// String is the encoding's PDF name.
func (e Encoding) String() string {
	switch e {
	case StandardEncoding:
		return "StandardEncoding"
	case MacRomanEncoding:
		return "MacRomanEncoding"
	case WinAnsiEncoding:
		return "WinAnsiEncoding"
	}
	return "Encoding(?)"
}

// GlyphName is the glyph name the encoding gives code, and whether it gives
// one. A code the encoding leaves undefined reports false.
func (e Encoding) GlyphName(code byte) (string, bool) {
	t := e.table()
	if t == nil || t[code] == "" {
		return "", false
	}
	return t[code], true
}

// Codes yields each code the encoding defines and its glyph name, in
// ascending code order.
func (e Encoding) Codes() iter.Seq2[byte, string] {
	return func(yield func(byte, string) bool) {
		t := e.table()
		if t == nil {
			return
		}
		for c := 0; c < 256; c++ {
			if t[c] != "" && !yield(byte(c), t[c]) {
				return
			}
		}
	}
}

// Len is how many codes the encoding defines.
func (e Encoding) Len() int {
	n := 0
	for range e.Codes() {
		n++
	}
	return n
}

func (e Encoding) table() *[256]string {
	t := tables()
	switch e {
	case StandardEncoding:
		return &t.standard
	case MacRomanEncoding:
		return &t.macRoman
	case WinAnsiEncoding:
		return &t.winAnsi
	}
	return nil
}

type encodingTables struct {
	standard, macRoman, winAnsi [256]string
}

// tables reads forme's encoding maps once into arrays nothing outside this
// file can reach, so that a lookup is an index and no caller holds a map it
// could write through.
var tables = sync.OnceValue(func() *encodingTables {
	t := &encodingTables{}
	fill := func(dst *[256]string, src map[byte]string) {
		for c, n := range src {
			dst[c] = n
		}
	}
	fill(&t.standard, formeStandard())
	fill(&t.macRoman, formeMacRoman())
	fill(&t.winAnsi, formeWinAnsi())
	return t
})
