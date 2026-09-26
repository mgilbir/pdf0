package core

import "testing"

// TestDecodePDFTextStringPDFDocEncoding is the C77 regression: a text string
// with no byte-order mark is PDFDocEncoded, not UTF-8 and not Latin-1.
func TestDecodePDFTextStringPDFDocEncoding(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte("Caf\xe9"), "Café"},                 // Latin-1 range
		{[]byte("\x80 list"), "• list"},             // 0x80 is a bullet, not a C1 control
		{[]byte("\x93"), "ﬁ"},                       // fi ligature
		{[]byte("\xa0"), "€"},                       // the euro sign
		{[]byte("plain"), "plain"},                  // ASCII unchanged
		{[]byte("\x7f"), "�"},                       // undefined code
		{[]byte{0xFE, 0xFF, 0x00, 0xE9}, "é"},       // UTF-16BE unchanged
		{[]byte("\xEF\xBB\xBFCaf\xc3\xa9"), "Café"}, // UTF-8 with BOM unchanged
	}
	for _, c := range cases {
		if got := DecodePDFTextString(c.in); got != c.want {
			t.Errorf("DecodePDFTextString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
