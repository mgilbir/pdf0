package font

import "sync"

// StandardLatinName reports whether a glyph name belongs to the Adobe standard
// Latin character set — the repertoire PDF Reference Appendix D.2 tabulates and
// that ISO 19005-1 6.3.8 names as one of the two vocabularies a font may draw on
// without carrying a ToUnicode CMap.
//
// The set is derived from the encoding tables rather than written out again. D.2
// is one table with four encoding columns, and StandardEncoding, MacRoman and
// WinAnsi between them name every glyph in it; PDFDocEncoding, the fourth
// column, adds no glyph the other three lack. Deriving it keeps the repertoire
// and the encodings from drifting apart, which two hand-maintained lists of the
// same 200-odd names would eventually do.
func StandardLatinName(name string) bool {
	return standardLatinNames()[name]
}

var standardLatinNames = sync.OnceValue(func() map[string]bool {
	m := make(map[string]bool, 256)
	for _, table := range []map[byte]string{StandardEncodingNames, MacRomanEncodingNames, WinAnsiEncodingNames} {
		for _, n := range table {
			if n != "" && n != ".notdef" {
				m[n] = true
			}
		}
	}
	return m
})
