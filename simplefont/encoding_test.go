package simplefont

import "testing"

// TestEncodingsAreFormesTables: each encoding answers exactly what forme's
// table says, code by code, and Codes walks them in code order.
func TestEncodingsAreFormesTables(t *testing.T) {
	for _, c := range []struct {
		e    Encoding
		want map[byte]string
	}{
		{StandardEncoding, formeStandard()},
		{MacRomanEncoding, formeMacRoman()},
		{WinAnsiEncoding, formeWinAnsi()},
	} {
		if len(c.want) == 0 {
			t.Fatalf("%v: forme's table is empty", c.e)
		}
		if c.e.Len() != len(c.want) {
			t.Errorf("%v: %d codes, forme has %d", c.e, c.e.Len(), len(c.want))
		}
		for code := 0; code < 256; code++ {
			got, ok := c.e.GlyphName(byte(code))
			want, wantOK := c.want[byte(code)]
			if got != want || ok != wantOK {
				t.Errorf("%v.GlyphName(%d) = (%q, %v), forme says (%q, %v)", c.e, code, got, ok, want, wantOK)
			}
		}
		last := -1
		for code, name := range c.e.Codes() {
			if int(code) <= last || name != c.want[code] {
				t.Errorf("%v.Codes: %d %q after %d", c.e, code, name, last)
			}
			last = int(code)
		}
		if e, ok := EncodingNamed(c.e.String()); !ok || e != c.e {
			t.Errorf("EncodingNamed(%q) = %v, %v", c.e.String(), e, ok)
		}
	}
	// The codes where the encodings deliberately disagree.
	for _, c := range []struct {
		e    Encoding
		code byte
		want string
	}{
		{StandardEncoding, 0x27, "quoteright"},
		{WinAnsiEncoding, 0x27, "quotesingle"},
		{WinAnsiEncoding, 0x80, "Euro"},
		{MacRomanEncoding, 0x80, "Adieresis"},
	} {
		if got, _ := c.e.GlyphName(c.code); got != c.want {
			t.Errorf("%v.GlyphName(%#x) = %q, want %q", c.e, c.code, got, c.want)
		}
	}
	for _, n := range []string{"MacExpertEncoding", "Identity-H", "", "winansiencoding"} {
		if e, ok := EncodingNamed(n); ok {
			t.Errorf("EncodingNamed(%q) = %v, want none", n, e)
		}
	}
	if _, ok := Encoding(0).GlyphName('A'); ok {
		t.Error("the zero Encoding named a glyph")
	}
}

// TestTheRepertoires pins ISO 32000-2 Annex D's two character sets as ISO
// 19005-1 6.3.8 uses them: the standard Latin set is every name the three
// Latin encodings give (and nothing else), and the Symbol set is the Symbol
// font's own 190 names, including the pieces its encoding does not reach.
func TestTheRepertoires(t *testing.T) {
	latin := map[string]bool{}
	for _, e := range []Encoding{StandardEncoding, MacRomanEncoding, WinAnsiEncoding} {
		for _, n := range e.Codes() {
			latin[n] = true
		}
	}
	for n := range latin {
		if !IsStandardLatin(n) {
			t.Errorf("%q is in a Latin encoding and not in the standard Latin set", n)
		}
	}
	if n := len(standardLatinNames()); n != len(latin) {
		t.Errorf("standard Latin set has %d names, the encodings %d", n, len(latin))
	}
	if len(symbolSetNames) != 190 {
		t.Errorf("Symbol set has %d names, want 190", len(symbolSetNames))
	}
	for _, c := range []struct {
		name          string
		latin, symbol bool
	}{
		{"Aacute", true, false},
		{"Euro", true, true},
		{"space", true, true},
		{"alpha", false, true},
		{"integralbt", false, true},
		{"apple", false, true},
		{".notdef", false, false},
		{"", false, false},
		{"uni0041", false, false},
	} {
		if IsStandardLatin(c.name) != c.latin || IsSymbolSet(c.name) != c.symbol {
			t.Errorf("%q: latin %v symbol %v, want %v %v", c.name, IsStandardLatin(c.name), IsSymbolSet(c.name), c.latin, c.symbol)
		}
	}
}
