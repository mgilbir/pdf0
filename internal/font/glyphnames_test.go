package font

import "testing"

// TestEveryStandardEncodingNameResolves pins the gap this table closed. A
// simple font names its glyphs, and the codes between 0x80 and 0x9F name
// characters nothing about the code implies — so a document setting a
// quotation mark had a character no reader here could name.
func TestEveryStandardEncodingNameResolves(t *testing.T) {
	for _, table := range []struct {
		name  string
		codes map[byte]string
	}{
		{"StandardEncoding", StandardEncodingNames},
		{"WinAnsiEncoding", WinAnsiEncodingNames},
		{"MacRomanEncoding", MacRomanEncodingNames},
	} {
		for code, glyph := range table.codes {
			if _, ok := GlyphNameToRune(glyph, code); !ok {
				t.Errorf("%s code %d (%s) does not resolve to a character", table.name, code, glyph)
			}
		}
	}
}

// TestTypographicNamesAreTheAGLValues pins a handful against Adobe's published
// list. They are generated from it, so this checks the generation and the
// lookup agree with the source.
func TestTypographicNamesAreTheAGLValues(t *testing.T) {
	cases := map[string]rune{
		"Euro": '€', "quoteright": '’', "quotedblleft": '“',
		"emdash": '—', "endash": '–', "bullet": '•',
		"ellipsis": '…', "trademark": '™', "OE": 'Œ',
		"Ydieresis": 'Ÿ', "florin": 'ƒ', "dagger": '†',
	}
	for name, want := range cases {
		got, ok := GlyphNameToRune(name, 0x80) // a code the identity rule refuses
		if !ok {
			t.Errorf("%s does not resolve", name)
			continue
		}
		if got != want {
			t.Errorf("%s = %q (U+%04X), want %q (U+%04X)", name, got, got, want, want)
		}
	}
}

// TestUniNamesStillWin pins that the explicit forms take precedence over
// nothing: a uniXXXX name says what it means and must keep meaning it.
func TestUniNamesStillWin(t *testing.T) {
	if r, ok := GlyphNameToRune("uni20AC", 0); !ok || r != '€' {
		t.Errorf("uni20AC = %q %v", r, ok)
	}
	if r, ok := GlyphNameToRune("u1F600", 0); !ok || r != '\U0001F600' {
		t.Errorf("u1F600 = %q %v", r, ok)
	}
}
