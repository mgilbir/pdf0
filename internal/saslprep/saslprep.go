// Package saslprep implements the SASLprep profile (RFC 4013) of stringprep
// (RFC 3454), the preparation ISO 32000-2 7.6.4.1 requires for every password
// of the revision-6 standard security handler.
//
// The steps, in the order RFC 3454 §3 prescribes:
//
//  1. Map: table B.1 characters are removed; table C.1.2 characters (non-ASCII
//     spaces) become U+0020. U+200B ZERO WIDTH SPACE is in both tables, and
//     the RFCs do not order the two mappings; it is removed (B.1 first), so a
//     zero-width space never turns into a visible one.
//  2. Normalize with Unicode normalization form KC.
//  3. Prohibit: tables C.1.2, C.2.1, C.2.2, C.3, C.4, C.5, C.6, C.7, C.8, C.9.
//  4. Check bidi (RFC 3454 §6): a string containing a D.1 (RandALCat)
//     character must not contain a D.2 (LCat) character, and must begin and
//     end with a RandALCat character.
//  5. Unassigned code points (table A.1): prohibited in a "stored string" and
//     permitted in a "query" (RFC 3454 §7). A password being set is a stored
//     string; a password being checked against a file is a query.
//
// The tables are generated from the RFC's own text (tables.go; see
// internal/cmd/gensaslprep). Normalization uses golang.org/x/text/unicode/norm,
// which implements the current Unicode version rather than the Unicode 3.2 that
// stringprep pins. Unicode's normalization stability policy makes the two agree
// on every code point assigned in 3.2 except the handful fixed by Unicode
// corrigenda #3–#5; TestNFKCMatchesUnicode32 measures the difference against
// Python's frozen Unicode 3.2 database and pins it. Characters assigned after
// 3.2 are table A.1 entries, which a stored string may not contain at all.
//
// golang.org/x/text/secure/precis is deliberately not used: its OpaqueString
// profile (RFC 8265) is SASLprep's successor, not SASLprep — it normalizes to
// NFC, not NFKC, and prohibits a different set of characters — and the PDF
// specification requires SASLprep by name (ISO 32000-2 Introduction: RFC 3454
// and RFC 4013 "continue to be used to maintain backward compatibility even
// though these RFCs are marked as obsolete").
package saslprep

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// ErrProhibited reports that a string cannot be prepared: it contains a
// character SASLprep prohibits, fails the bidi check, is not valid UTF-8, or —
// for a stored string — contains a code point unassigned in Unicode 3.2.
var ErrProhibited = errors.New("saslprep: string cannot be prepared")

// table is a sorted list of inclusive, non-overlapping code point ranges.
type table [][2]rune

func (t table) contains(r rune) bool {
	i := sort.Search(len(t), func(i int) bool { return t[i][1] >= r })
	return i < len(t) && t[i][0] <= r
}

// prohibited are the tables of RFC 4013 §2.3.
var prohibited = []struct {
	t    table
	what string
}{
	{nonASCIISpaceC12, "a non-ASCII space (C.1.2)"},
	{asciiControlC21, "an ASCII control character (C.2.1)"},
	{nonASCIIControlC22, "a non-ASCII control character (C.2.2)"},
	{privateUseC3, "a private use character (C.3)"},
	{nonCharacterC4, "a non-character code point (C.4)"},
	{surrogateC5, "a surrogate code point (C.5)"},
	{notPlainTextC6, "a character inappropriate for plain text (C.6)"},
	{notCanonicalC7, "a character inappropriate for canonical representation (C.7)"},
	{displayPropertyC8, "a display-property or deprecated character (C.8)"},
	{taggingC9, "a tagging character (C.9)"},
}

// Prepare applies SASLprep to s. Set stored for a string being stored — a
// password being set — and clear it for a query, a password being checked:
// only a stored string refuses code points unassigned in Unicode 3.2.
//
// The error wraps ErrProhibited and names the offending character.
func Prepare(s string, stored bool) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%w: not valid UTF-8", ErrProhibited)
	}
	// 1. Map, and 2. normalize.
	//
	// Normalization runs over the runs between code points unassigned in
	// Unicode 3.2, which are copied through untouched. That is what Unicode 3.2
	// NFKC does with them — an unassigned code point has no decomposition and
	// combining class 0, so it is a starter nothing composes across — whereas
	// the current Unicode version may decompose a character assigned since
	// (U+2097 → "l"), which would make a query differ from what a Unicode 3.2
	// implementation computes. A stored string may not contain one at all.
	var b, seg strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case mappedToNothingB1.contains(r):
		case nonASCIISpaceC12.contains(r):
			seg.WriteRune(' ')
		case unassignedA1.contains(r):
			if stored {
				return "", fmt.Errorf("%w: U+%04X is unassigned in Unicode 3.2 (A.1)", ErrProhibited, r)
			}
			b.WriteString(norm.NFKC.String(seg.String()))
			seg.Reset()
			b.WriteRune(r)
		default:
			seg.WriteRune(r)
		}
	}
	b.WriteString(norm.NFKC.String(seg.String()))
	out := b.String()

	// 3. Prohibit; 5. unassigned (stored strings only); and collect what the
	// bidi check needs.
	hasRandAL, hasL := false, false
	for _, r := range out {
		for _, p := range prohibited {
			if p.t.contains(r) {
				return "", fmt.Errorf("%w: U+%04X is %s", ErrProhibited, r, p.what)
			}
		}
		if stored && unassignedA1.contains(r) {
			return "", fmt.Errorf("%w: U+%04X is unassigned in Unicode 3.2 (A.1)", ErrProhibited, r)
		}
		if randALCatD1.contains(r) {
			hasRandAL = true
		}
		if lCatD2.contains(r) {
			hasL = true
		}
	}
	// 4. Bidi.
	if hasRandAL {
		if hasL {
			return "", fmt.Errorf("%w: mixes right-to-left (D.1) and left-to-right (D.2) characters", ErrProhibited)
		}
		first, _ := utf8.DecodeRuneInString(out)
		last, _ := utf8.DecodeLastRuneInString(out)
		if !randALCatD1.contains(first) || !randALCatD1.contains(last) {
			return "", fmt.Errorf("%w: a right-to-left string must begin and end with a right-to-left character", ErrProhibited)
		}
	}
	return out, nil
}
