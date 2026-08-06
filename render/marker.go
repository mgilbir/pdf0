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
// index is the item's one-based position among the list items of its parent,
// which is what a numbered list counts.
func (l *layouter) markerFor(b *Box, frag *Fragment, index int) *Marker {
	if !b.ListItem {
		return nil
	}
	text := markerText(b.Style["list-style-type"], index)
	if text == "" {
		return nil
	}
	face, ok := l.fontFor(b)
	if !ok {
		return nil
	}

	size := b.FontSize
	width := l.measure(face, text, size)
	lineHeight := l.lineHeight(b)

	// "outside" puts the marker in the margin, clear of the content box;
	// "inside" puts it at the start of the first line, where it pushes the text
	// along. Outside is the default and is what a list looks like.
	x := frag.Border.Left.Add(frag.Padding.Left)
	if !strings.EqualFold(strings.TrimSpace(b.Style["list-style-position"]), "inside") {
		// A gap of half an em between the marker and the text, which is what
		// keeps a bullet from touching the word after it.
		x = x.Sub(width).Sub(size.Mul(0.5))
	}

	colour := style.RGBA{A: 1}
	if c, ok := (&painter{colors: map[string]style.RGBA{}}).color(b, "color"); ok {
		colour = c
	}
	return &Marker{
		Text: text, Face: face, Size: size,
		At:    Point{X: x, Y: frag.Border.Top.Add(frag.Padding.Top).Add(l.baselineOf(b, lineHeight))},
		Color: colour,
	}
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
