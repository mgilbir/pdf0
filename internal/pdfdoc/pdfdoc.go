// Package pdfdoc implements PDFDocEncoding, the single-byte encoding of PDF
// text strings that carry no byte order mark (ISO 32000-2 Annex D.3, Table D.3).
//
// It agrees with ISO Latin-1 at 0x20–0x7E and 0xA1–0xFF (except 0xAD), and
// replaces the control ranges with typographic characters: 0x18–0x1F are the
// spacing accents, 0x80–0x9E are bullets, dashes, quotes, ligatures and a few
// Latin letters, and 0xA0 is the euro sign. 0x7F, 0x9F and 0xAD are undefined,
// as are the controls 0x00–0x17 other than TAB, LF and CR.
package pdfdoc

// high maps bytes 0x18–0x1F and 0x80–0xA0 to Unicode. Every other defined byte
// maps to the code point of the same value. Table D.3 prints U+0017 for 0x16, a
// typo — 0x16 is SYNCHRONOUS IDLE, U+0016, as in ISO 32000-1 — but 0x16 is an
// undefined code in either reading.
var high = map[byte]rune{
	0x18: 0x02D8, 0x19: 0x02C7, 0x1A: 0x02C6, 0x1B: 0x02D9,
	0x1C: 0x02DD, 0x1D: 0x02DB, 0x1E: 0x02DA, 0x1F: 0x02DC,
	0x80: 0x2022, 0x81: 0x2020, 0x82: 0x2021, 0x83: 0x2026,
	0x84: 0x2014, 0x85: 0x2013, 0x86: 0x0192, 0x87: 0x2044,
	0x88: 0x2039, 0x89: 0x203A, 0x8A: 0x2212, 0x8B: 0x2030,
	0x8C: 0x201E, 0x8D: 0x201C, 0x8E: 0x201D, 0x8F: 0x2018,
	0x90: 0x2019, 0x91: 0x201A, 0x92: 0x2122, 0x93: 0xFB01,
	0x94: 0xFB02, 0x95: 0x0141, 0x96: 0x0152, 0x97: 0x0160,
	0x98: 0x0178, 0x99: 0x017D, 0x9A: 0x0131, 0x9B: 0x0142,
	0x9C: 0x0153, 0x9D: 0x0161, 0x9E: 0x017E, 0xA0: 0x20AC,
}

// decode is the full byte → rune table; -1 marks an undefined code.
var decode [256]rune

// encode is its inverse.
var encode = map[rune]byte{}

func init() {
	for i := range decode {
		b := byte(i)
		switch {
		case b < 0x18 && b != '\t' && b != '\n' && b != '\r', b == 0x7F, b == 0x9F, b == 0xAD:
			decode[i] = -1
			continue
		}
		if r, ok := high[b]; ok {
			decode[i] = r
		} else {
			decode[i] = rune(b)
		}
		encode[decode[i]] = b
	}
}

// Encode converts s to PDFDocEncoding. ok is false when s contains a character
// PDFDocEncoding cannot represent (or an undefined code), in which case the
// returned bytes are nil.
func Encode(s string) (b []byte, ok bool) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		c, defined := encode[r]
		if !defined {
			return nil, false
		}
		out = append(out, c)
	}
	return out, true
}

// Decode converts PDFDocEncoded bytes to a string. An undefined code becomes
// U+FFFD.
func Decode(b []byte) string {
	out := make([]rune, len(b))
	for i, c := range b {
		if r := decode[c]; r >= 0 {
			out[i] = r
		} else {
			out[i] = 0xFFFD
		}
	}
	return string(out)
}
