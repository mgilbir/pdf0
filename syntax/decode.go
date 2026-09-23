package syntax

import (
	"bytes"
	"errors"
	"fmt"
)

// The decoders for the three token forms whose bytes are not their value:
// literal strings (escapes, line continuations, end-of-line normalisation),
// hexadecimal strings, and names (#xx escapes). ISO 32000-2 7.3.4 and 7.3.5.
//
// They are the one definition of that syntax in the module. The object lexer
// here uses them, and so does the content-stream lexer in internal/core: a
// content stream's strings are the same PDF strings, and when the two lexers
// carried their own copies they drifted — one honoured backslash-newline line
// continuation and the other did not, one skipped a non-hex byte and the
// other read it as a zero nibble — so PDF/A and PDF/UA saw different glyph
// codes in the same Identity-H string (audit 2026-09-22 C150).

// LiteralStringEnd returns the index just past the ')' that closes the literal
// string whose '(' is at data[i], and whether one was found. An unterminated
// string ends at len(data).
func LiteralStringEnd(data []byte, i int) (end int, terminated bool) {
	return scanLiteral(data, i, nil)
}

// DecodeLiteralString decodes the literal string whose '(' is at data[i],
// returning its value, the index just past the closing ')' (len(data) when
// unterminated), and whether it was terminated.
//
// Escapes are those of Table 3: \n \r \t \b \f \( \) \\, one to three octal
// digits (the high-order overflow of \777 is ignored), and a backslash before
// an end-of-line marker, which continues the line and contributes nothing. An
// unescaped end-of-line marker — CR, LF or CR LF — is a single LF. A backslash
// before any other byte is dropped and the byte kept. Balanced parentheses need
// no escape.
func DecodeLiteralString(data []byte, i int) (value []byte, end int, terminated bool) {
	out := make([]byte, 0, 16)
	end, terminated = scanLiteral(data, i, &out)
	return out, end, terminated
}

// scanLiteral is LiteralStringEnd and DecodeLiteralString: the one walk over a
// literal string, collecting its value into *out when out is non-nil, so that
// finding where a string ends and decoding it can never disagree.
func scanLiteral(data []byte, i int, out *[]byte) (int, bool) {
	n := len(data)
	i++ // '('
	depth := 1
	emit := func(b byte) {
		if out != nil {
			*out = append(*out, b)
		}
	}
	for i < n {
		c := data[i]
		i++
		switch c {
		case '(':
			depth++
			emit('(')
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
			emit(')')
		case '\r':
			if i < n && data[i] == '\n' {
				i++
			}
			emit('\n')
		case '\\':
			if i >= n {
				return n, false
			}
			e := data[i]
			i++
			switch e {
			case 'n':
				emit('\n')
			case 'r':
				emit('\r')
			case 't':
				emit('\t')
			case 'b':
				emit('\b')
			case 'f':
				emit('\f')
			case '\r': // line continuation: \<CR> or \<CR><LF>
				if i < n && data[i] == '\n' {
					i++
				}
			case '\n': // line continuation: \<LF>
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && i < n && data[i] >= '0' && data[i] <= '7'; k++ {
						v = v<<3 | int(data[i]-'0')
						i++
					}
					emit(byte(v))
				} else {
					emit(e) // \( \) \\ and any unknown escape: the byte itself
				}
			}
		default:
			emit(c)
		}
	}
	return n, false
}

// DecodeHexString decodes the body of a hexadecimal string — the bytes between
// '<' and '>' — ignoring white space and padding an odd final digit with 0.
// A byte that is neither a hex digit nor white space is skipped, and ok
// reports whether there was none: a lenient reader keeps the digits it can
// read, and a strict one refuses on !ok.
func DecodeHexString(body []byte) (value []byte, ok bool) {
	ok = true
	out := make([]byte, 0, len(body)/2)
	var hi byte
	half := false
	for _, c := range body {
		v, isHex := hexDigitValue(c)
		if !isHex {
			if !IsWhitespace(c) {
				ok = false
			}
			continue
		}
		if half {
			out = append(out, hi<<4|v)
		} else {
			hi = v
		}
		half = !half
	}
	if half {
		out = append(out, hi<<4)
	}
	return out, ok
}

func hexDigitValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// ErrNameNUL is returned by DecodeName for a #00 escape. The specification
// forbids NUL in a name (7.3.5), and it is a common smuggling vector.
var ErrNameNUL = errors.New("name contains #00 (NUL)")

// DecodeName decodes the bytes of a name after its '/', expanding #xx escapes.
// A name without '#' is returned as is, without copying. A '#' not followed by
// two hex digits is an error, as is #00; the bytes decoded so far are returned
// with it, for a lenient caller.
func DecodeName(raw []byte) ([]byte, error) {
	if bytes.IndexByte(raw, '#') < 0 {
		return raw, nil
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '#' {
			out = append(out, c)
			continue
		}
		if i+2 >= len(raw) {
			return out, fmt.Errorf("incomplete #xx escape in name")
		}
		hi, ok1 := hexDigitValue(raw[i+1])
		lo, ok2 := hexDigitValue(raw[i+2])
		if !ok1 || !ok2 {
			return out, fmt.Errorf("invalid #xx escape in name")
		}
		if hi<<4|lo == 0 {
			return out, ErrNameNUL
		}
		out = append(out, hi<<4|lo)
		i += 2
	}
	return out, nil
}
