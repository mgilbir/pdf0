package syntax

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

// TestParseNumberIsPDFNumberSyntax pins ISO 32000-2 7.3.3: an optional sign,
// digits and at most one period, and nothing else. Every spelling the clause
// gives is a number; an exponent, a second period, a special value and a bare
// sign are not.
func TestParseNumberIsPDFNumberSyntax(t *testing.T) {
	huge := "1" + strings.Repeat("0", 400)
	for _, c := range []struct {
		in     string
		want   float64
		isInt  bool
		wantOK bool
	}{
		// The clause's own examples.
		{"123", 123, true, true},
		{"43445", 43445, true, true},
		{"+17", 17, true, true},
		{"-98", -98, true, true},
		{"0", 0, true, true},
		{"34.5", 34.5, false, true},
		{"-3.62", -3.62, false, true},
		{"+123.6", 123.6, false, true},
		{"4.", 4, false, true},
		{"-.002", -0.002, false, true},
		{"0.0", 0, false, true},
		{".5", 0.5, false, true},
		{"-.25", -0.25, false, true},
		// Integers past int64 and past float64: nearest, then clamped.
		{"9223372036854775808", 9223372036854775808, true, true},
		{huge, math.MaxFloat64, true, true},
		{"-" + huge + ".5", -math.MaxFloat64, false, true},
		{"0." + strings.Repeat("0", 400) + "1", 0, false, true},
		// Not PDF numbers.
		{"1e3", 0, false, false},
		{"1E3", 0, false, false},
		{"1.2.3", 0, false, false},
		{"Inf", 0, false, false},
		{"-Inf", 0, false, false},
		{"NaN", 0, false, false},
		{"0x1p3", 0, false, false},
		{"1_000", 0, false, false},
		{" 1", 0, false, false},
		{"1 ", 0, false, false},
		{"", 0, false, false},
		{"+", 0, false, false},
		{"-", 0, false, false},
		{".", 0, false, false},
		{"+.", 0, false, false},
		{"--1", 0, false, false},
		{"1-", 0, false, false},
	} {
		v, isInt, ok := ParseNumber([]byte(c.in))
		if ok != c.wantOK || (ok && (v != c.want || isInt != c.isInt)) {
			t.Errorf("ParseNumber(%.40q) = (%v, %v, %v), want (%v, %v, %v)", c.in, v, isInt, ok, c.want, c.isInt, c.wantOK)
		}
	}
}

// TestParseNumberFastPathAgreesWithStrconv holds the integer shortcut to the
// conversion it skips, at the edge of the digits it takes.
func TestParseNumberFastPathAgreesWithStrconv(t *testing.T) {
	for _, s := range []string{"999999999999999999", "-999999999999999999", "+000000000000000001", "1234567890123456789", "9999999999999999999", "-9999999999999999999"} {
		v, _, ok := ParseNumber([]byte(s))
		w, err := parseFloatRef(s)
		if !ok || err != nil || v != w {
			t.Errorf("ParseNumber(%q) = %v (ok %v), strconv says %v", s, v, ok, w)
		}
	}
}

func parseFloatRef(s string) (float64, error) { return strconv.ParseFloat(s, 64) }
