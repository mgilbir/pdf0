// Package textorder puts a line of text extracted from a page back into
// reading order, as the readers text is most often copied with do it.
//
// A page draws right-to-left text in the order its glyphs stand on the line,
// left to right, which is the reverse of the order it is read in. PDFium
// (Chrome, Edge) and poppler read it back by applying the Unicode
// bidirectional algorithm's reordering (UAX #9, rule L2) to the line as
// drawn, taking it as if it were logical: for the text a shaper lays out
// that is its own inverse, and it gives the reading order back. Reorder does
// the same, so that pdf0's Document.ExtractText reads a page as they do, and
// so that the fonts package can ask, before it writes a run, whether such a
// reader will read it right without an /ActualText.
//
// It works on units, not characters: the text one glyph stands for, which a
// reader takes from the font's ToUnicode CMap as one piece. A ligature's
// "لا" is one unit and is not turned round inside itself, and a combining
// mark drawn after its base moves with the base. A unit can also be fixed —
// an /ActualText, which is already in reading order — and is then placed as
// a neutral, and not looked into.
package textorder

import (
	"strings"

	"github.com/mgilbir/forme/bidi"
)

// placeholder stands for a fixed unit while the line is resolved: U+FFFC
// OBJECT REPLACEMENT CHARACTER, an other neutral, which takes the direction
// of the text around it.
const placeholder = '￼'

// Reorder returns the order to read a line's units in, as indices into
// units, or nil when they are read in the order drawn: the line has no
// right-to-left text outside its fixed units. fixed may be nil, or as long
// as units.
//
// The line's direction is its majority: right to left when it has more
// strongly right-to-left characters than left-to-right ones. A line drawn in
// order has nothing else to say which way its paragraph ran, and a mostly
// Hebrew line with a Latin word in it is a Hebrew paragraph.
func Reorder(units []string, fixed []bool) []int {
	isFixed := func(i int) bool { return fixed != nil && fixed[i] }
	rtl := false
	for i, u := range units {
		if !isFixed(i) && bidi.NeedsAlgorithm(u) {
			rtl = true
			break
		}
	}
	if !rtl {
		return nil
	}

	// The line as runes, each fixed unit one placeholder.
	var runes []rune
	strongL, strongR := 0, 0
	for i, u := range units {
		if isFixed(i) {
			runes = append(runes, placeholder)
			continue
		}
		for _, r := range u {
			runes = append(runes, r)
			switch bidi.ClassOf(r) {
			case bidi.L:
				strongL++
			case bidi.R, bidi.AL:
				strongR++
			}
		}
	}
	dir := bidi.LeftToRight
	if strongR > strongL {
		dir = bidi.RightToLeft
	}
	levels := bidi.Resolve(runes, dir).LineLevels(0, len(runes))

	// Each unit's level is its first character's that is not a combining
	// mark; a unit of marks alone joins the unit before it, which is the
	// base it was drawn on.
	type group struct{ first, last, level int }
	var groups []group
	at := 0
	for i, u := range units {
		n := 1
		if !isFixed(i) {
			n = len([]rune(u))
		}
		level, base := -1, false
		for k := at; k < at+n; k++ {
			if bidi.ClassOf(runes[k]) != bidi.NSM || isFixed(i) {
				level, base = levels[k], true
				break
			}
		}
		if n > 0 && level < 0 {
			level = levels[at]
		}
		at += n
		if g := len(groups); g > 0 && (!base || n == 0) {
			groups[g-1].last = i
			continue
		}
		groups = append(groups, group{first: i, last: i, level: max(level, 0)})
	}
	groupLevels := make([]int, len(groups))
	for i, g := range groups {
		groupLevels[i] = g.level
	}
	order := make([]int, 0, len(units))
	for _, gi := range bidi.VisualOrder(groupLevels) {
		for u := groups[gi].first; u <= groups[gi].last; u++ {
			order = append(order, u)
		}
	}
	return order
}

// Line is units read in the order Reorder gives, joined.
func Line(units []string, fixed []bool) string {
	order := Reorder(units, fixed)
	var b strings.Builder
	if order == nil {
		for _, u := range units {
			b.WriteString(u)
		}
		return b.String()
	}
	for _, i := range order {
		b.WriteString(units[i])
	}
	return b.String()
}
