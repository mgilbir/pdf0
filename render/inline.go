package render

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// Inline layout: text into lines.
//
// §1 of the rendering proposal calls this the deceptive one, and it is right —
// line boxes, breaking, baseline alignment and whitespace at line edges are
// individually modest and collectively larger than flexbox. What is here is the
// part that puts words on a page: measuring runs against a real face, finding
// where a line may break, and stacking the lines.
//
// # Where a line may break, and what is refused
//
// Doing this properly is UAX #14, a table-driven Unicode algorithm. What is
// implemented is a documented subset of it: after a space, at an explicit break
// opportunity, after a hyphen, and between ideographs. That covers Latin, Greek,
// Cyrillic and the CJK scripts, which is most of what a document generator sets.
//
// It does *not* cover the scripts that need a dictionary to know where a word
// ends — Thai, Lao, Khmer, Burmese — nor the bidirectional reordering that
// right-to-left text needs once a line is broken. Those are refused and reported
// rather than approximated, because §6.3 is exactly right about them: unshaped
// or unbroken text still looks like text, so the failure mode looks like
// success. A paragraph of Thai run together as one unbreakable word would
// overflow silently and read as a rendering bug rather than as an unimplemented
// feature.

// LineFragment is a line box: one row of text within a block.
type LineFragment struct {
	// Rect is the line box in the same coordinates as the fragment holding it.
	Rect Rect
	// Baseline is the distance from the top of the line box to the baseline the
	// text sits on. Painting needs it, and it is not derivable afterwards —
	// half-leading is split above and below the text.
	Baseline style.Unit
	// Runs are the pieces of text on the line, in reading order.
	Runs []TextRun
}

// TextRun is a piece of text on a line, set in one face at one size.
type TextRun struct {
	Text string
	// Face is what it is set in, and Size the font size.
	Face *fonts.Face
	Size style.Unit
	// X is the offset from the left of the line box, and Width the advance.
	X, Width style.Unit
	// Box is the inline box the text came from, which carries the colour and
	// the decoration painting will need.
	Box *Box
}

// inlineItem is one piece of inline content before it has been put on a line.
type inlineItem struct {
	text  string
	box   *Box
	face  *fonts.Face
	size  style.Unit
	width style.Unit
	// breakBefore marks an item that may begin a line, which is what a break
	// opportunity is once the text has been cut into pieces.
	breakBefore bool
	// space marks an item that is collapsible white space, which is dropped at
	// the end of a line rather than measured into it.
	space bool
}

// inlineContent lays a box's inline children into lines and returns the height
// they need.
func (l *layouter) inlineContent(b *Box, parent *Fragment, width style.Unit) style.Unit {
	items, _ := l.collectInline(b, nil, false)
	if len(items) == 0 {
		return 0
	}

	lineHeight := l.lineHeight(b)
	lines := l.breakLines(items, width)

	var y style.Unit
	for _, runs := range lines {
		line := LineFragment{
			Rect:     Rect{X: 0, Y: y, W: width, H: lineHeight},
			Baseline: l.baselineOf(b, lineHeight),
		}
		var x style.Unit
		for _, item := range runs {
			line.Runs = append(line.Runs, TextRun{
				Text: item.text, Face: item.face, Size: item.size,
				X: x, Width: item.width, Box: item.box,
			})
			x = x.Add(item.width)
		}
		parent.Lines = append(parent.Lines, line)
		y = y.Add(lineHeight)
	}
	return y
}

// collectInline flattens an inline subtree into measurable items.
//
// The tree is flattened because a line break can fall anywhere, including inside
// an <em> — so what goes on a line is a sequence of runs, not a sequence of
// boxes. Each item keeps the box it came from, which is what painting needs to
// know its colour.
//
// pending carries a break opportunity *across* a box boundary, and it is not a
// detail: in "foo <em>bar</em>" the space and the word are in different text
// boxes, so an engine that started each box afresh would find no opportunity
// between them and set the whole phrase as one unbreakable word.
func (l *layouter) collectInline(b *Box, out []inlineItem, pending bool) ([]inlineItem, bool) {
	for _, child := range b.Children {
		if child.IsText() {
			var items []inlineItem
			items, pending = l.itemsFor(child, pending)
			out = append(out, items...)
			continue
		}
		if child.Outer == OuterInline {
			out, pending = l.collectInline(child, out, pending)
		}
	}
	return out, pending
}

// itemsFor cuts one text box into items at its break opportunities and measures
// each.
//
// pendingIn says whether the text before this box ended at an opportunity;
// pendingOut says whether this one does.
func (l *layouter) itemsFor(b *Box, pendingIn bool) ([]inlineItem, bool) {
	face, ok := l.fontFor(b)
	if !ok {
		return nil, pendingIn
	}
	l.checkScript(b)
	l.checkGlyphs(b, face)

	size := b.FontSize
	pieces, pendingOut := splitAtBreaks(b.Text)

	var out []inlineItem
	for i, piece := range pieces {
		item := inlineItem{
			text: piece.text, box: b, face: face, size: size,
			breakBefore: piece.breakBefore, space: piece.space,
		}
		if i == 0 && pendingIn {
			item.breakBefore = true
		}
		item.width = l.measure(face, piece.text, size)
		out = append(out, item)
	}
	if len(pieces) == 0 {
		// A box that produced nothing passes an opportunity through rather than
		// swallowing it — and it may have created one of its own, which is what
		// a <span> holding a single zero-width space is. Either source counts.
		return out, pendingIn || pendingOut
	}
	return out, pendingOut
}

// measure returns the advance width of a string, memoized.
//
// Measuring is the inner loop of line breaking, and the same words recur
// constantly in a document — every "the" in a page measures the same. The key
// includes the face and the size because both scale the answer.
func (l *layouter) measure(face *fonts.Face, text string, size style.Unit) style.Unit {
	if text == "" {
		return 0
	}
	key := measureKey{face: face, text: text, size: size}
	if got, ok := l.measured[key]; ok {
		return got
	}
	// Measure returns the advance in the units the size was given in, so a size
	// in CSS pixels gives an advance in CSS pixels.
	w, _ := style.FromPx(face.Measure(text, size.Px()))
	l.measured[key] = w
	return w
}

type measureKey struct {
	face *fonts.Face
	text string
	size style.Unit
}

// piece is a run of text between two break opportunities.
type piece struct {
	text        string
	breakBefore bool
	space       bool
}

// splitAtBreaks cuts text at the break opportunities this engine implements.
//
// The subset is stated in the file comment. Each rule below is one of UAX #14's,
// named by what it does rather than by its class letters, and the ones left out
// are left out loudly — checkScript reports text that needs them.
func splitAtBreaks(text string) ([]piece, bool) {
	var out []piece
	var cur strings.Builder
	breakNext := false

	flush := func(isSpace bool) {
		if cur.Len() == 0 {
			return
		}
		out = append(out, piece{text: cur.String(), breakBefore: breakNext, space: isSpace})
		cur.Reset()
		breakNext = false
	}

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		switch {
		case r == ' ' || r == '\t':
			// A space ends the run before it and is itself a run, so that it can
			// be dropped when it lands at the end of a line.
			flush(false)
			start := i
			for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
				i++
			}
			i--
			out = append(out, piece{text: string(runes[start : i+1]), space: true})
			// What follows a space may begin a line.
			breakNext = true

		case r == '​':
			// A zero-width space is a break opportunity and nothing else: it is
			// how an author marks one inside a word.
			flush(false)
			breakNext = true

		case isIdeographic(r):
			// CJK breaks between ideographs, which is why it needs no spaces.
			flush(false)
			cur.WriteRune(r)
			flush(false)
			breakNext = true

		case r == '-' && i+1 < len(runes) && !unicode.IsSpace(runes[i+1]):
			// A hyphen ends a run and the next may begin a line — which is what
			// lets a hyphenated compound break where it is written.
			cur.WriteRune(r)
			flush(false)
			breakNext = true

		default:
			cur.WriteRune(r)
		}
	}
	flush(false)
	// breakNext survives the last piece: it says the text ended at an
	// opportunity, which matters when what follows is in another box.
	return out, breakNext
}

// isIdeographic reports whether a rune breaks on both sides, which is what makes
// CJK line breaking possible without word boundaries.
func isIdeographic(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // Extension A
		return true
	case r >= 0xF900 && r <= 0xFAFF: // Compatibility Ideographs
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana and Katakana
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul syllables
		return true
	case r >= 0x20000 && r <= 0x2FA1F: // Extensions B and beyond
		return true
	}
	return false
}

// breakLines puts items onto lines, greedily.
//
// Greedy is what browsers do: a line takes what fits and the next one starts
// after it. The alternative — minimising raggedness across a paragraph, which is
// what TeX does — produces better-looking text and needs the whole paragraph
// before any line is settled, which is a different shape of engine.
func (l *layouter) breakLines(items []inlineItem, width style.Unit) [][]inlineItem {
	var lines [][]inlineItem
	var line []inlineItem
	var used style.Unit

	for i := 0; i < len(items); i++ {
		item := items[i]

		// A space at the start of a line is dropped: it is the space the break
		// happened at, and keeping it would indent every line after the first.
		if item.space && len(line) == 0 {
			continue
		}

		if used.Add(item.width) > width && len(line) > 0 && item.breakBefore {
			lines = append(lines, trimTrailingSpaces(line))
			line, used = nil, 0
			if item.space {
				continue
			}
		}

		// A single item wider than the line has nowhere to go. It is placed and
		// overflows — breaking inside a word would be worse, since a word split
		// at an arbitrary point reads as a different word — and it is reported,
		// because the part past the edge is simply not drawn and nothing else
		// about the page says so.
		if item.width > width && len(line) == 0 && !item.space {
			l.reportOverflow(item, width)
		}
		line = append(line, item)
		used = used.Add(item.width)
	}
	if len(line) > 0 {
		lines = append(lines, trimTrailingSpaces(line))
	}
	return lines
}

// reportOverflow names content too wide for the box holding it.
//
// It is reported once per piece of text rather than once per line, because a
// paragraph containing one impossible word would otherwise complain on every
// line it wraps to.
func (l *layouter) reportOverflow(item inlineItem, width style.Unit) {
	key := item.text
	if l.reportedOverflow[key] {
		return
	}
	l.reportedOverflow[key] = true
	l.rec.ReportDetail(Finding{
		Rule: RuleUnbreakableOverflow,
		Message: "the text " + quoteValue(item.text) + " is " +
			fmtPx(item.width) + " wide and cannot be broken, in a space " +
			fmtPx(width) + " wide; the part past the edge will not be drawn",
		Path: PathOf(item.box.Element),
	})
}

func fmtPx(u style.Unit) string {
	return strconvFormat(u.Px()) + "px"
}

// trimTrailingSpaces removes the collapsible space at the end of a line.
//
// CSS Text removes it because it is the space the break happened at: leaving it
// would make a right-aligned line hang, and a centred one sit off-centre by half
// a space.
func trimTrailingSpaces(line []inlineItem) []inlineItem {
	for len(line) > 0 && line[len(line)-1].space {
		line = line[:len(line)-1]
	}
	return line
}

// lineHeight resolves the line-height property.
//
// "normal" is the face's own recommendation, which for the metrics this engine
// has means about 1.2 times the size — the figure every renderer uses when a
// face does not say otherwise. A bare number is a multiplier rather than a
// length, which is the one place in CSS where that is true and the reason
// line-height is usually written that way: a multiplier inherits as a ratio and
// a length inherits as a fixed distance.
func (l *layouter) lineHeight(b *Box) style.Unit {
	value := strings.ToLower(strings.TrimSpace(b.Style["line-height"]))
	if value == "" || value == "normal" {
		return b.FontSize.Mul(1.2)
	}
	if n, ok := parseNumber(value); ok {
		return b.FontSize.Mul(n)
	}
	if length, ok := l.parseLength(b, "line-height"); ok {
		if v, ok := length.Resolve(b.FontSize, true); ok {
			return v
		}
	}
	return b.FontSize.Mul(1.2)
}

// baselineOf is where the text sits within a line box.
//
// The line box is usually taller than the text, and the difference — the leading
// — is split equally above and below it. That is what makes a paragraph's lines
// evenly spaced rather than crowded against their tops.
func (l *layouter) baselineOf(b *Box, lineHeight style.Unit) style.Unit {
	face, ok := l.fontFor(b)
	if !ok {
		return lineHeight.Mul(0.8)
	}
	d := face.Descriptor()
	unitsPerEm := float64(face.UnitsPerEm())
	if unitsPerEm == 0 {
		return lineHeight.Mul(0.8)
	}
	ascent := b.FontSize.Mul(float64(d.Ascent) / unitsPerEm)
	descent := b.FontSize.Mul(-float64(d.Descent) / unitsPerEm)
	halfLeading := lineHeight.Sub(ascent).Sub(descent).Div(2)
	return halfLeading.Add(ascent)
}

// parseNumber reads a bare number, which line-height accepts as a multiplier.
func parseNumber(s string) (float64, bool) {
	var v float64
	var seenDigit, seenDot bool
	frac := 0.1
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			seenDigit = true
			if seenDot {
				v += float64(c-'0') * frac
				frac /= 10
			} else {
				v = v*10 + float64(c-'0')
			}
		case c == '.' && !seenDot:
			seenDot = true
		default:
			return 0, false
		}
	}
	return v, seenDigit
}

// checkScript reports text this engine cannot break or order correctly.
//
// It is the unsupported-script guardrail of §6.3, and it is an error by default
// for the reason given there: unbroken or unordered text still looks like text,
// so the failure mode looks like success. A paragraph of Thai run together as
// one word overflows silently; a line of Arabic laid out left to right reads as
// a rendering bug rather than as something this engine declined to do.
func (l *layouter) checkScript(b *Box) {
	for _, r := range b.Text {
		if script, bad := unsupportedScript(r); bad {
			key := script + "\x00" + b.Style["font-family"]
			if l.reportedScripts[key] {
				return
			}
			l.reportedScripts[key] = true
			l.rec.ReportDetail(Finding{
				Rule:    RuleUnsupportedScript,
				Message: script,
				Path:    PathOf(b.Element),
			})
			return
		}
	}
}

// checkGlyphs reports characters the chosen face has no glyph for.
//
// This is the glyph-missing guardrail of §6.3, an error by default because tofu
// is the purest form of silent garbage: a reader who sees a row of boxes where
// letters should be blames their PDF viewer, not the document, and the author
// never hears about it at all.
//
// It is reported once per character rather than once per occurrence, because
// what an author needs to know is *which* characters their font cannot set —
// hearing it four hundred times about the same one is not four hundred times as
// useful.
func (l *layouter) checkGlyphs(b *Box, face *fonts.Face) {
	for _, r := range b.Text {
		if r == '\n' || r == '\t' {
			continue
		}
		if _, ok := face.GlyphID(r); ok {
			continue
		}
		key := string(r) + "\x00" + face.Name()
		if l.reportedGlyphs[key] {
			continue
		}
		l.reportedGlyphs[key] = true
		l.rec.ReportDetail(Finding{
			Rule: RuleGlyphMissing,
			Message: "the face " + quoteValue(face.Name()) + " has no glyph for " +
				describeRune(r) + ", which would be drawn as a blank box",
			Path: PathOf(b.Element),
		})
	}
}

// describeRune names a character for a diagnostic, by code point as well as by
// its shape — the shape is what the author recognises and the code point is what
// they can search for, and a character with no glyph often cannot be shown at
// all in whatever is reading the report.
func describeRune(r rune) string {
	out := "U+" + strings.ToUpper(hex(uint32(r)))
	if unicode.IsPrint(r) {
		out += " (" + string(r) + ")"
	}
	return out
}

func hex(v uint32) string {
	const digits = "0123456789abcdef"
	if v == 0 {
		return "0000"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{digits[v&0xf]}, b...)
		v >>= 4
	}
	for len(b) < 4 {
		b = append([]byte{'0'}, b...)
	}
	return string(b)
}

// unsupportedScript names why a rune cannot be laid out, or reports false.
func unsupportedScript(r rune) (string, bool) {
	switch {
	// Right-to-left. Shaping these is done in forme; ordering them within a
	// broken line is not done here, and text laid out in the wrong order is
	// text that reads as nonsense while looking like a font problem.
	case r >= 0x0590 && r <= 0x05FF, // Hebrew
		r >= 0x0600 && r <= 0x06FF, // Arabic
		r >= 0x0700 && r <= 0x074F, // Syriac
		r >= 0x0780 && r <= 0x07BF, // Thaana
		r >= 0x07C0 && r <= 0x08FF, // NKo, Samaritan, Arabic Extended
		r >= 0xFB1D && r <= 0xFDFF, // Hebrew and Arabic presentation forms
		r >= 0xFE70 && r <= 0xFEFF:
		return "right-to-left text needs the bidirectional algorithm applied to " +
			"each line, which is not implemented; it would be laid out in the wrong order", true

	// Scripts with no spaces between words, which need a dictionary to know
	// where a line may break.
	case r >= 0x0E00 && r <= 0x0E7F, // Thai
		r >= 0x0E80 && r <= 0x0EFF, // Lao
		r >= 0x1780 && r <= 0x17FF, // Khmer
		r >= 0x1000 && r <= 0x109F: // Myanmar
		return "this script writes no spaces between words, so finding a line " +
			"break needs a dictionary, which is not implemented; the text would " +
			"run on as one unbreakable word", true
	}
	return "", false
}

// strconvFormat renders a length for a diagnostic, to a tenth of a pixel — more
// precision than that is noise in a message a person reads.
func strconvFormat(v float64) string {
	return strconv.FormatFloat(float64(int(v*10+0.5))/10, 'f', -1, 64)
}
