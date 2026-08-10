package render

import (
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// List markers: the bullet or the number a list item generates.
//
// A marker is not in the document. Nothing in the markup of "<li>one</li>" says
// "•", and nothing in the box tree did either until here — which is why the box
// carries a flag rather than a child, and why the text is worked out at layout
// time. The number in particular cannot be known earlier: it is the item's
// position among its siblings, and an item does not know its own position.

// Marker is the bullet or number drawn beside a list item.
type Marker struct {
	Text string
	Face *fonts.Face
	Size style.Unit
	// At is the origin of the marker's baseline, relative to the fragment's
	// border box.
	At Point
	// Color is the item's own text colour: a marker takes the colour of the
	// text it belongs to, which is why an author never sets it separately.
	Color style.RGBA
}

// markerFor works out the marker a list item generates, or nil.
//
// index is the item's one-based position among the list items of its parent. It
// is a fallback only: what a numbered list counts is the "list-item" counter,
// which the user-agent sheet increments and every list resets. The two agree for
// a plain list and disagree the moment a document says <ol start="5"> or
// <li value="3"> or resets the counter itself.
func (l *layouter) markerFor(b *Box, frag *Fragment) *Marker {
	if markerInside(b) {
		// An inside marker is not drawn beside the box: it is the first thing on
		// the box's first line, and markerItem puts it there. See §12.5.1 and the
		// note on markerItem for why the two positions are different mechanisms
		// rather than two x coordinates.
		return nil
	}
	text, face, ok := l.markerRun(b)
	if !ok {
		return nil
	}

	size := b.FontSize
	width := l.measure(face, text, size)
	lineHeight := l.lineHeight(b)

	// "outside" puts the marker in the margin, clear of the content box, with a
	// gap of half an em between it and the text — which is what keeps a bullet
	// from touching the word after it.
	x := frag.Border.Left.Add(frag.Padding.Left).Sub(width).Sub(markerGap(size))

	return &Marker{
		Text: text, Face: face, Size: size,
		At:    Point{X: x, Y: frag.Border.Top.Add(frag.Padding.Top).Add(l.baselineOf(b, lineHeight))},
		Color: markerColour(b),
	}
}

// markerInside reports "list-style-position: inside".
func markerInside(b *Box) bool {
	return b.ListItem &&
		strings.EqualFold(strings.TrimSpace(b.Style["list-style-position"]), "inside")
}

// markerGap is the space between a marker and the text it belongs to.
func markerGap(size style.Unit) style.Unit { return size.Mul(0.5) }

// markerColour is the item's own text colour: a marker takes the colour of the
// text it belongs to, which is why an author never sets it separately.
func markerColour(b *Box) style.RGBA {
	if c, ok := (&painter{colors: map[string]style.RGBA{}}).color(b, "color"); ok {
		return c
	}
	return style.RGBA{A: 1}
}

// markerRun is the marker's text and the face to set it in, for either position.
func (l *layouter) markerRun(b *Box) (string, *fonts.Face, bool) {
	if !b.ListItem {
		return "", nil, false
	}
	text := markerText(b.Style["list-style-type"], b.ListValue)
	if text == "" {
		return "", nil, false
	}
	face, ok := l.fontFor(b)
	if !ok {
		return "", nil, false
	}
	return text, face, true
}

// markerItems is what a block container's inline content starts with, which for
// a list item numbering itself inside is its marker and for everything else is
// nothing.
//
// It seeds both the line building and the intrinsic-width measurement, and it
// has to seed both: a shrink-to-fit list item whose width was measured without
// its marker is narrower than the marker it then draws.
func (l *layouter) markerItems(b *Box) []inlineItem {
	item, ok := l.markerItem(b)
	if !ok {
		return nil
	}
	return []inlineItem{item}
}

// markerItem is the line item an inside marker contributes, if there is one.
//
// §12.5.1 puts an inside marker "as the first inline box in the principal block
// box, before the element's content" — a *box on the line*, not a mark beside
// one. The difference is not cosmetic and shows in three places at once: the
// marker pushes the first line's text along, it takes part in the line's height
// and its width, and — the case the suite is full of — it makes an *empty* list
// item generate a line box at all. An item with no content and an inside marker
// is one line tall and shows its background; drawn as a mark beside the box it
// was zero-tall, and the background of a dozen tests went missing.
//
// It is deliberately not registered with the bidi builder. A marker is its own
// box rather than part of the run of text after it, so it does not join that
// text's directional run — which is what leaves it at the start of the line in a
// right-to-left list rather than reordered into the middle of the first word.
func (l *layouter) markerItem(b *Box) (inlineItem, bool) {
	if !markerInside(b) {
		return inlineItem{}, false
	}
	text, face, ok := l.markerRun(b)
	if !ok {
		return inlineItem{}, false
	}
	size := b.FontSize
	above, below := l.leading(b)
	return inlineItem{
		text: text, box: b, face: face, size: size,
		// The same half-em the outside marker leaves, spent as width rather than
		// as an offset: here what it separates is the next item on the line.
		width: l.measure(face, text, size).Add(markerGap(size)),
		leads: true, above: above, below: below,
	}, true
}

// markerText renders the marker for a list-style-type and a position.
func markerText(listStyle string, index int) string {
	switch strings.ToLower(strings.TrimSpace(listStyle)) {
	case "none":
		return ""
	case "circle":
		return "◦" // ◦
	case "square":
		return "▪" // ▪
	case "decimal-leading-zero":
		if index < 10 {
			return "0" + strconv.Itoa(index) + "."
		}
		return strconv.Itoa(index) + "."
	case "decimal":
		return strconv.Itoa(index) + "."
	case "lower-alpha", "lower-latin":
		return alphabetic(index, 'a') + "."
	case "upper-alpha", "upper-latin":
		return alphabetic(index, 'A') + "."
	case "lower-roman":
		return strings.ToLower(roman(index)) + "."
	case "upper-roman":
		return roman(index) + "."
	case "lower-greek":
		return alphabeticIn(index, lowerGreek) + "."
	case "armenian":
		return additive(index, armenianNumerals, 1, 9999) + "."
	case "georgian":
		return additive(index, georgianNumerals, 1, 19999) + "."
	default:
		// "disc" and anything unrecognised. An unknown type falling back to a
		// bullet is what browsers do, and a list with no marker at all would
		// look like a deliberate "none".
		return "•" // •
	}
}

// alphabetic numbers a list a, b, … z, aa, ab, …
//
// It is bijective base 26, not ordinary base 26: there is no zero digit, so
// after "z" comes "aa" rather than "ba". Ordinary base-26 arithmetic gets this
// wrong at exactly the 26th item, which is far enough into a list that nobody
// notices until a document has one.
func alphabetic(index int, first rune) string {
	if index < 1 {
		return ""
	}
	var out []rune
	for index > 0 {
		index--
		out = append([]rune{first + rune(index%26)}, out...)
		index /= 26
	}
	return string(out)
}

// lowerGreek is the alphabet §12.6.2's "lower-greek" counts in.
//
// Twenty-four letters and not twenty-five: final sigma, U+03C2, is the same
// letter as U+03C3 in a different position in a word, so it is not a numeral and
// the sequence steps straight over it. An implementation that walked the code
// points from alpha to omega would number every list one out from the eighteenth
// item on.
var lowerGreek = []rune("αβγδεζηθικλμνξοπρστυφχψω")

// armenianNumerals and georgianNumerals are the two additive systems §12.6.2
// names, as CSS Counter Styles §6.2 spells them out.
//
// Additive rather than positional: the number is written as the sum of the
// largest numerals that fit, so 1979 in Armenian is Ռ (1000) Ջ (900) Հ (70) Թ
// (9) and not four digits. Roman numerals are the same idea with subtractive
// pairs on top, which is why roman is a separate function rather than a call to
// this one.
var armenianNumerals = []additiveNumeral{
	{9000, "Ք"}, {8000, "Փ"}, {7000, "Ւ"}, {6000, "Ց"}, {5000, "Ր"},
	{4000, "Տ"}, {3000, "Վ"}, {2000, "Ս"}, {1000, "Ռ"},
	{900, "Ջ"}, {800, "Պ"}, {700, "Չ"}, {600, "Ո"}, {500, "Շ"},
	{400, "Ն"}, {300, "Յ"}, {200, "Մ"}, {100, "Ճ"},
	{90, "Ղ"}, {80, "Ձ"}, {70, "Հ"}, {60, "Կ"}, {50, "Ծ"},
	{40, "Խ"}, {30, "Լ"}, {20, "Ի"}, {10, "Ժ"},
	{9, "Թ"}, {8, "Ը"}, {7, "Է"}, {6, "Զ"}, {5, "Ե"},
	{4, "Դ"}, {3, "Գ"}, {2, "Բ"}, {1, "Ա"},
}

var georgianNumerals = []additiveNumeral{
	{10000, "ჵ"},
	{9000, "ჰ"}, {8000, "ჯ"}, {7000, "ჴ"}, {6000, "ხ"}, {5000, "ჭ"},
	{4000, "წ"}, {3000, "ძ"}, {2000, "ც"}, {1000, "ჩ"},
	{900, "შ"}, {800, "ყ"}, {700, "ღ"}, {600, "ქ"}, {500, "ფ"},
	{400, "ჳ"}, {300, "ტ"}, {200, "ს"}, {100, "რ"},
	{90, "ჟ"}, {80, "პ"}, {70, "ო"}, {60, "ჲ"}, {50, "ნ"},
	{40, "მ"}, {30, "ლ"}, {20, "კ"}, {10, "ი"},
	{9, "თ"}, {8, "ჱ"}, {7, "ზ"}, {6, "ვ"}, {5, "ე"},
	{4, "დ"}, {3, "გ"}, {2, "ბ"}, {1, "ა"},
}

// additiveNumeral is one weight and the mark that stands for it.
type additiveNumeral struct {
	weight int
	symbol string
}

// alphabeticIn numbers a list in an arbitrary alphabet, bijectively.
//
// It is alphabetic's argument with the alphabet given rather than derived from a
// first letter, because Greek's is not a run of consecutive code points. The two
// share the bijective arithmetic and nothing else: there is no zero digit, so
// after the last letter comes the first letter doubled.
func alphabeticIn(index int, alphabet []rune) string {
	n := len(alphabet)
	if index < 1 || n == 0 {
		return strconv.Itoa(index)
	}
	var out []rune
	for index > 0 {
		index--
		out = append([]rune{alphabet[index%n]}, out...)
		index /= n
	}
	return string(out)
}

// additive writes a number as the sum of the largest numerals that fit.
//
// Outside the system's range it falls back to the decimal, which is §12.6.2's
// own instruction for a marker a style cannot represent — Armenian stops at 9999
// and Georgian at 19999, and a list longer than that numbered in nothing at all
// would lose its numbering silently.
func additive(index int, numerals []additiveNumeral, lo, hi int) string {
	if index < lo || index > hi {
		return strconv.Itoa(index)
	}
	var b strings.Builder
	for _, n := range numerals {
		for index >= n.weight {
			b.WriteString(n.symbol)
			index -= n.weight
		}
	}
	return b.String()
}

// roman renders a number in Roman numerals, for the two list styles that ask.
//
// Values outside what the numerals express fall back to the decimal, which is
// what the specification requires — MMMM is not a numeral, and a list of four
// thousand items numbered in Roman is not what the author was imagining anyway.
func roman(index int) string {
	if index < 1 || index > 3999 {
		return strconv.Itoa(index)
	}
	values := [...]int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	symbols := [...]string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var b strings.Builder
	for i, v := range values {
		for index >= v {
			b.WriteString(symbols[i])
			index -= v
		}
	}
	return b.String()
}
