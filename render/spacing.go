package render

import (
	"strings"
	"unicode/utf8"

	"github.com/mgilbir/pdf0/style"
)

// The three properties that change how much room text takes: letter-spacing,
// word-spacing and text-indent.
//
// All three were in the registry and unread, and all three fail in the way §6.3
// is written about — a paragraph set without the letter-spacing its author asked
// for is a plausible paragraph. What makes them worth grouping is that none of
// them is a painting property, however much they look like one:
//
//   - letter-spacing and word-spacing change the *advance* of a run, so they
//     change where a line breaks and how wide a float around the text wants to
//     be. An implementation that spread the glyphs at paint time and left the
//     measurement alone would produce lines that overflow their box by exactly
//     the spacing it added, with the guardrails silent because layout never saw
//     a run too wide for its line.
//   - text-indent shortens the first line, so it changes what fits on it.
//
// # Why the measurement cache had to learn about them
//
// l.measure is memoized on the face, the text and the size, because the same
// words recur constantly. Two boxes with different letter-spacing produce
// different advances for the same word in the same face at the same size, so the
// spacing is part of the key. Leaving it out would let the first box to measure
// "the" decide its width for every other box in the document — which is exactly
// the bug lengthKey.zeroAdvance records for the "ch" unit, in the same map, for
// the same reason. It produces a wrong page only in a document that uses two
// values, which is the hardest kind to notice.

// textSpacing is the pair of extra advances, in layout units.
//
// The zero value is what "normal" means for both, which is what makes it cheap:
// a document that sets neither compares equal to the zero key and shares one
// cache entry per word with every other such document.
type textSpacing struct {
	// letter is added after every typographic character unit, including the last
	// one of a run. That is what CSS Text §8.2 says and what browsers do, and it
	// is why "letter-spacing: 1em" on a single word leaves a gap after it.
	letter style.Unit
	// word is added to every word-separator character.
	word style.Unit
}

// spacingFor reads the two properties off a box.
//
// Both are inherited, so this is answered from the box's own computed style and
// needs no walk. "normal" is zero for both — for letter-spacing that is what the
// keyword means outright, and for word-spacing it means "the font's own space,
// unmodified", which is the same thing expressed as an addition of nothing.
func (l *layouter) spacingFor(b *Box) textSpacing {
	var out textSpacing
	if v, ok := l.spacingValue(b, "letter-spacing"); ok {
		out.letter = v
	}
	if v, ok := l.spacingValue(b, "word-spacing"); ok {
		out.word = v
	}
	return out
}

// spacingValue resolves one of the two, or reports that it is "normal".
//
// A percentage is not accepted by either property; the basis of zero means one
// resolves to nothing rather than to a fraction of something arbitrary.
func (l *layouter) spacingValue(b *Box, property string) (style.Unit, bool) {
	raw := strings.ToLower(strings.TrimSpace(b.Style[property]))
	if raw == "" || raw == "normal" {
		return 0, false
	}
	return l.lengthOf(b, property, 0)
}

// spacingAdvance is what the two properties add to a run of text.
//
// It walks the string once, without decoding it into runes: a text node is
// untrusted and arbitrarily large, and counting characters through a []rune
// would buffer four bytes per character to answer a question about a length.
// utf8.RuneCountInString does the same walk without the copy.
func spacingAdvance(text string, sp textSpacing) style.Unit {
	var out style.Unit
	if sp.letter != 0 {
		out = out.Add(sp.letter.Mul(float64(spacedUnits(text))))
	}
	if sp.word != 0 {
		out = out.Add(sp.word.Mul(float64(countWordSeparators(text))))
	}
	return out
}

// spacedUnits counts what letter-spacing is added between.
//
// CSS Text §8.2 spaces "adjacent typographic character units", and a code point
// the shaper drops before choosing any glyph is not one of them: it draws
// nothing and it is in the text only because the text is what a reader copies
// out of the page. Counting one gave a bidi override an em of its own under
// "letter-spacing: 1em", which pushed every letter after it along — the suite
// writes that document eight times over, as the second half of each
// bidi-text/bidi-00N pair.
//
// Grapheme clusters would be the exact reading of "typographic character unit"
// and this counts runes, so a base and its combining mark are still spaced
// apart. That is a separate question with a separate answer, and it is not
// mixed in here.
func spacedUnits(text string) int {
	n := 0
	for _, r := range text {
		if isDefaultIgnorable(r) {
			continue
		}
		n++
	}
	return n
}

// isDefaultIgnorable is the part of Unicode's Default_Ignorable_Code_Point
// property this engine meets: the bidi controls, the joiners, and the marks that
// are there to say something to an algorithm rather than to be seen.
func isDefaultIgnorable(r rune) bool {
	switch {
	case r == 0x00AD, // soft hyphen
		r == 0x034F,                // combining grapheme joiner
		r >= 0x200B && r <= 0x200F, // zero width space through RLM
		r >= 0x202A && r <= 0x202E, // the embedding and override controls
		r >= 0x2060 && r <= 0x2064, // word joiner and the invisible operators
		r >= 0x2066 && r <= 0x2069, // the isolates
		r == 0xFEFF:                // zero width no-break space
		return true
	}
	return false
}

// countWordSeparators counts the characters word-spacing applies to.
//
// CSS Text §8.1 names a short list of word-separator characters. The two that
// occur in real documents are the ordinary space and the no-break space; the
// remaining four are Ethiopic and Aegean word separators, which are counted too
// because leaving them out would make the property silently do nothing in the
// documents that need it most.
func countWordSeparators(text string) int {
	n := 0
	for i := 0; i < len(text); {
		r, size := rune(text[i]), 1
		if r >= utf8.RuneSelf {
			r, size = utf8.DecodeRuneInString(text[i:])
		}
		i += size
		switch r {
		case ' ', '\u00a0', // space, and the no-break space an author writes
			'\u1361',                   // Ethiopic wordspace
			'\U00010100', '\U00010101', // Aegean word separators
			'\U0001039F', '\U0001091F': // Ugaritic and Phoenician
			n++
		}
	}
	return n
}

// textIndent is how far the first line of a block container is pushed in.
//
// CSS 2.1 §16.1. A percentage is of the containing block's width, which for a
// block container's own first line is its content width — the number this is
// given. A negative value is legal and is how a hanging indent is written, so it
// is not clamped.
func (l *layouter) textIndent(b *Box, width style.Unit) style.Unit {
	raw := strings.TrimSpace(b.Style["text-indent"])
	if raw == "" || raw == "0" {
		return 0
	}
	if v, ok := l.lengthOf(b, "text-indent", width); ok {
		return v
	}
	// A value that did not resolve. The keyword forms — "each-line" and
	// "hanging" — are the likely ones, and both change which lines are indented
	// rather than by how much, so treating them as a plain length would indent
	// the wrong lines. Reported rather than guessed at.
	l.reportIndent(b, raw)
	return 0
}

// reportIndent names an indent that did not resolve, once per value for the
// whole document.
//
// Not once per element, and not with a path: the value came from a stylesheet
// that a hundred paragraphs may share, and a hundred findings naming a hundred
// paragraphs would bury the one thing the author has to change. Leaving the Path
// off is what produces that — the Recorder suppresses a repeat of a finding
// identical in rule, message, property and place, and with no place to differ by
// every call after the first collapses into the first. rec.Count still knows how
// many there were.
func (l *layouter) reportIndent(b *Box, raw string) {
	l.rec.ReportDetail(Finding{
		Rule:   RuleUnsupportedValue,
		Source: AtHTML(offsetOf(b)),
		Message: "the text-indent " + quoteValue(raw) +
			" could not be resolved, so the first line was not indented",
		Property: "text-indent",
	})
}
