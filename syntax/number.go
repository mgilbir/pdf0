package syntax

import (
	"errors"
	"math"
	"strconv"

	"github.com/mgilbir/pdf0/internal/checked"
)

// ParseNumber reads b as a PDF number (ISO 32000-2 7.3.3): an optional sign,
// then decimal digits with at most one period, and at least one digit. "34.5",
// "-3.62", "+123.6", "4.", "-.002", "+17" and "0" are numbers. Nothing else is:
// PDF has no exponent ("1e3" is not a number), no radix, no "Inf" or "NaN", no
// digit separators, no second period ("1.2.3") and no surrounding white space.
//
// isInt reports that b is written as an integer (no period). v is its value
// either way. A value beyond the range of float64 is the largest finite value
// of its sign, as the object parser reads it (audit C117): an infinity is not a
// PDF number, and nothing downstream expects one. A value below float64's
// precision rounds toward zero. An integer beyond 2^53 loses precision in v; a
// caller that needs it exactly reads the digits itself (internal/checked).
//
// It is the one reader of PDF number syntax. The object parser reads its real
// tokens with it, and the content-stream tokens that begin like a number are
// read with it too, so that the grammar a content-stream rule applies is the one
// the object parser applies. strconv.ParseFloat is not that grammar (it takes
// exponents, hexadecimal floats, "Inf" and "NaN"), and neither was the CFF real
// reader the PDF/A rules once borrowed from forme, which read "1.2.3" as 1.23.
func ParseNumber(b []byte) (v float64, isInt bool, ok bool) {
	i := 0
	if i < len(b) && (b[i] == '+' || b[i] == '-') {
		i++
	}
	digits, points := 0, 0
	for ; i < len(b); i++ {
		switch c := b[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			points++
			if points > 1 {
				return 0, false, false
			}
		default:
			return 0, false, false
		}
	}
	if digits == 0 {
		return 0, false, false
	}
	if points == 0 {
		// Every content stream is mostly small integers: an integer that fits
		// an int64 needs no conversion, and converts to float64 exactly
		// rounded.
		body, neg := b, false
		if body[0] == '+' || body[0] == '-' {
			body, neg = body[1:], body[0] == '-'
		}
		if n, _, fits := checked.Decimal(body); fits {
			if neg {
				n = -n
			}
			return float64(n), true, true
		}
	}
	// The grammar above is a subset of strconv's (no exponent, no prefix, no
	// underscore, no special value), so strconv only does the arithmetic and
	// its correctly rounded conversion.
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false, false // unreachable: the grammar was checked above
	}
	switch {
	case math.IsInf(f, 1):
		f = math.MaxFloat64
	case math.IsInf(f, -1):
		f = -math.MaxFloat64
	}
	return f, points == 0, true
}
