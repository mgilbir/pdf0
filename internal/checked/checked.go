// Package checked forms sizes from numbers a file supplies.
//
// An image's /Width and /Height, a fax stream's /Columns and /Rows, a JPEG
// frame header, a JBIG2 region or halftone grid and a sampled function's /Size
// are all numbers the file chooses. Multiplied together they size an allocation
// or a loop, and each of them was once multiplied in plain int arithmetic: a
// product that wraps can come out small, pass the budget it is compared with,
// and then size the buffer from the factors instead — or come out negative and
// index before the start of one (audit 2026-09-22 C11, C12, C15).
//
// Mul is the one way such a product is formed. It is a leaf package so that
// the codecs (internal/ccitt, internal/jbig2) can use it without importing the
// engine; the budget the product is compared with — core.Limits.ImagePixels —
// is handed to them as a number.
package checked

import "math"

// Mul returns the product of factors, and false if any factor is negative or
// the product does not fit in an int64. The empty product is 1.
func Mul(factors ...int64) (int64, bool) {
	p := int64(1)
	for _, f := range factors {
		if f < 0 {
			return 0, false
		}
		if f != 0 && p > math.MaxInt64/f {
			return 0, false
		}
		p *= f
	}
	return p, true
}

// Decimal reads the ASCII decimal digits at the start of s. n is how many it
// read, 0 when s does not start with a digit. fits is false when the value
// does not fit in an int64; v is then math.MaxInt64 and n still counts every
// digit, so a caller can step over the number whatever its size.
//
// It is the one place a decimal accumulator runs over bytes a file supplies.
// The loop it replaces, v = v*10 + d, wraps silently: an inline image's
// /L 9223372036854775807 plus its data offset came out negative, passed an
// "end <= len" check and indexed the content stream before its start (audit
// 2026-09-22 C16). internal/lint rejects the open-coded form everywhere else.
func Decimal[T ~string | ~[]byte](s T) (v int64, n int, fits bool) {
	fits = true
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		d := int64(s[n] - '0')
		if fits && v > (math.MaxInt64-d)/10 {
			fits, v = false, math.MaxInt64
		}
		if fits {
			v = v*10 + d
		}
		n++
	}
	return v, n, fits
}

// Within reports whether the product of factors is at most bound. A product
// that overflows is within no bound.
func Within(bound int64, factors ...int64) bool {
	p, ok := Mul(factors...)
	return ok && p <= bound
}
