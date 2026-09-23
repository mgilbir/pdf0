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

// Within reports whether the product of factors is at most bound. A product
// that overflows is within no bound.
func Within(bound int64, factors ...int64) bool {
	p, ok := Mul(factors...)
	return ok && p <= bound
}
