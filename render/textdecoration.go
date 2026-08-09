package render

import (
	"strings"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// text-decoration: the lines drawn through, under and over a run of text.
//
// CSS 2.1 §16.3.1. The property was in the registry — and in the user-agent
// stylesheet, on <a>, <u>, <ins>, <del>, <s> and <strike> — long before anything
// read it, so every link in every document came out with no underline and the
// engine said nothing. That is the failure §6.3 exists to prevent, and it is
// worth recording again that a property table claiming support is not support.
//
// # Propagation, which is not inheritance
//
// This is the part that reads as a technicality and is the whole of the
// property's behaviour. text-decoration-line does *not* inherit — the registry
// is right about that — and yet an <em> inside a decorated <p> is underlined.
// §16.3.1 says why: the decoration is drawn across the *descendant boxes* of the
// box that declared it, rather than being a value each descendant computes for
// itself. Two things follow, and an implementation that used inheritance gets
// both wrong:
//
//   - The colour is the *declaring* box's, not the text's. "p { text-decoration:
//     underline; color: black } em { color: red }" underlines the red words in
//     black. Inheritance would give each descendant its own colour and the line
//     would change colour halfway along a word.
//   - The decoration does not reach into a float, an absolutely positioned
//     descendant, or an atomic inline such as an inline-block. Those are drawn as
//     units of their own, and a line ruled across a paragraph does not continue
//     through the picture floated inside it.
//
// So a decoration is carried on the run rather than read off the box the run
// came from, and each one remembers which box asked for it.
//
// # Where the lines go, and where the numbers came from
//
// A face carries an underline position and thickness in its post table, and
// forme's descriptor does not expose either — it stops at ascent, descent, cap
// height and the bounding box. Rather than guess, the numbers below are the ones
// the fourteen standard faces actually declare in their own AFM metrics: every
// one of Times, Helvetica and Courier states an underline position of -100 and a
// thickness of 50 per 1000 em. So an underline is a band 0.05em thick centred
// 0.1em below the baseline, which is exactly right for the faces this engine
// sets by default and within a few thousandths of an em for the rest.
//
// The other two lines are not in a font's metrics at all — a strikeout position
// is in OS/2, which the descriptor does not carry either — so they are placed
// from metrics that are: an overline sits on the face's own ascent, and a
// line-through is centred on half the x-height, the same estimate strutFor uses
// to place a "vertical-align: middle" box.

// decorationKind is one of the three lines §16.3.1 defines.
type decorationKind uint8

const (
	decorationUnderline decorationKind = iota
	decorationOverline
	decorationLineThrough
)

// textDecoration is one line to draw, together with the box that asked for it.
type textDecoration struct {
	kind decorationKind
	// by is the box whose declaration produced the line. It is kept rather than a
	// resolved colour because the colour is not known until painting — and
	// because "currentcolor", which is text-decoration-color's initial value,
	// means the declaring box's own colour rather than the colour of whatever
	// text the line happens to cross.
	by *Box
}

// decorationsFor is every decoration drawn across a box's text.
//
// It walks upwards rather than being pushed downwards, because the answer is
// wanted per text box and a text box is reached from several directions — the
// line breaking, the intrinsic-width measurement, and a second layout of a
// subtree that had to be settled. Each box's answer is memoized and built from
// its parent's, so the whole tree costs one step per box however deep it is.
//
// # What a nest of decorated boxes costs
//
// The list grows by one per decorated ancestor, so N boxes each declaring an
// underline inside one another cost N²/2 entries across the tree and N bands per
// run. That is quadratic and it is deliberately not deduplicated: every one of
// those lines lands on the same rectangle, so keeping only the innermost would
// produce the same page in every case *except* one — an inner decoration whose
// colour is transparent, which paints nothing and must let the outer one show
// through. Dropping the outer would erase a line the author asked for.
//
// It is safe because the html parser caps nesting at 256, which bounds the whole
// document at about 33 000 bands and was measured at 15ms. Should that cap ever
// be raised, this is the place that stops being linear.
func (l *layouter) decorationsFor(b *Box) []textDecoration {
	if b == nil {
		return nil
	}
	if got, ok := l.decorations[b]; ok {
		return got
	}
	// Once per box, on the cache miss: a value this engine cannot draw is
	// reported where it is first read rather than by a pass of its own.
	l.checkDecorationValue(b)

	own := ownDecorations(b)
	var above []textDecoration
	if decorationReaches(b) {
		above = l.decorationsFor(b.Parent)
	}

	var out []textDecoration
	switch {
	case len(own) == 0:
		// The common case by a long way, and it shares the parent's slice rather
		// than copying it: nothing ever appends to one of these.
		out = above
	case len(above) == 0:
		out = own
	default:
		out = make([]textDecoration, 0, len(above)+len(own))
		out = append(out, above...)
		out = append(out, own...)
	}
	l.decorations[b] = out
	return out
}

// decorationReaches reports whether a decoration declared on a box's ancestors
// is drawn across this box's content.
//
// §16.3.1's three exceptions. Each is a box that is painted as a unit of its
// own, and a line ruled across the paragraph around it stops at its edge.
func decorationReaches(b *Box) bool {
	if b.Parent == nil {
		return false
	}
	if b.outOfFlow() {
		// A float or an absolutely positioned box.
		return false
	}
	// An atomic inline: an inline-block, or a replaced element. Its contents are
	// its own.
	return !isAtomicInline(b) && b.Replaced == nil
}

// ownDecorations reads a box's own text-decoration-line.
func ownDecorations(b *Box) []textDecoration {
	if b.IsText() {
		// A text box has no declarations of its own. It carries its parent
		// element's whole computed style so that a consumer never has to walk up
		// for one, which means reading a decoration off it would count the
		// parent's declaration twice — once here and once when the walk reaches
		// the element itself — and draw the line on top of itself.
		return nil
	}
	raw := b.Style["text-decoration-line"]
	if raw == "" {
		return nil
	}
	var out []textDecoration
	for _, word := range strings.Fields(raw) {
		switch strings.ToLower(word) {
		case "underline":
			out = append(out, textDecoration{kind: decorationUnderline, by: b})
		case "overline":
			out = append(out, textDecoration{kind: decorationOverline, by: b})
		case "line-through":
			out = append(out, textDecoration{kind: decorationLineThrough, by: b})
		}
		// "none" contributes nothing, and so does anything else — "blink" among
		// them, which the shorthand already reports as a part it cannot produce.
		// A longhand naming it is reported by checkDecorationValue.
	}
	return out
}

// checkDecorationValue reports a text-decoration-line this engine does not draw.
//
// The shorthand's expander catches "text-decoration: blink" on its way through
// the cascade; this catches the longhand, which the cascade accepts whole
// because the property is in the registry. Without it, "text-decoration-line:
// blink" would be a declaration that is understood, stored, and silently drawn
// as nothing.
func (l *layouter) checkDecorationValue(b *Box) {
	if b.IsText() {
		return
	}
	raw := b.Style["text-decoration-line"]
	if raw == "" {
		return
	}
	for _, word := range strings.Fields(raw) {
		switch strings.ToLower(word) {
		case "none", "underline", "overline", "line-through":
			continue
		}
		// Reported once per *value* for the whole document rather than once per
		// element, and the Path is what makes that so: the Recorder suppresses a
		// repeat of an identical finding, and two findings differing only in which
		// of four hundred elements carried the declaration are not two things an
		// author needs to be told. Naming one arbitrary element of the four
		// hundred would read as though the others were fine.
		//
		// A suppression map of this function's own was written here first and
		// removed: with the Path left off it never fired, because the Recorder had
		// already collapsed every call after the first. Keeping it would have cost
		// nothing and proved nothing, which is how a guard that has never been
		// seen to work gets kept. rec.Count still reports how many times the value
		// was met, which is the number that is actually informative.
		l.rec.ReportDetail(Finding{
			Rule:   RuleUnsupportedValue,
			Source: AtHTML(offsetOf(b)),
			Message: "\"text-decoration-line: " + quoteValue(word) +
				"\" is not a line this engine draws, so nothing was drawn for it",
			Property: "text-decoration-line",
		})
	}
}

// decorationMetrics is where the three lines sit for one face at one size, as
// distances from the baseline with the CSS convention that down is positive.
type decorationMetrics struct {
	// thickness is the height of every one of the bands, except a line-through
	// drawn from a face that states a strikeout size of its own.
	thickness style.Unit
	// strikeThickness is that size, and is zero when the face did not state one
	// — in which case the line-through is drawn at thickness like the rest.
	strikeThickness style.Unit
	// underline, overline and strike are the *top* edge of each band.
	underline, overline, strike style.Unit
}

// decorationMetricsFor works the three positions out from a face.
//
// See the file comment for where the numbers come from. A face with no usable
// units-per-em falls back to the same fractions of the font size, which is what
// the standard faces would have given anyway.
func decorationMetricsFor(face *fonts.Face, size style.Unit) decorationMetrics {
	// 0.05em, the thickness every standard face declares. It is floored at one
	// layout unit so that a decoration on very small text is thin rather than
	// absent: a band of zero height paints nothing at all, and an underline that
	// silently disappears below some size is the shape of failure this engine is
	// written against.
	thickness := style.Max(size.Mul(0.05), 1)

	// The underline's centre is 0.1em below the baseline, so its top edge is half
	// a thickness above that.
	m := decorationMetrics{
		thickness: thickness,
		underline: size.Mul(0.1).Sub(thickness.Div(2)),
		// Fallbacks for a face that reports no metrics: the em box's own top, and
		// a strike through the middle of a half-em x-height.
		overline: style.Unit(0).Sub(size.Mul(0.8)),
		strike:   style.Unit(0).Sub(size.Mul(0.25)).Sub(thickness.Div(2)),
	}
	if face == nil {
		return m
	}
	upem := float64(face.UnitsPerEm())
	if upem == 0 {
		return m
	}
	d := face.Descriptor()

	// The underline, when the face states one. post gives the *top* of the
	// stroke and a thickness, which is exactly the band this draws, so there is
	// no arithmetic to get wrong beyond the sign: the position is measured up
	// from the baseline and an underline is below it, so a negative number is a
	// positive offset downwards.
	//
	// The thickness is still floored at one layout unit. A face is free to state
	// a thickness that rounds to nothing at a small size, and a decoration that
	// silently disappears below some font size is worse than one a fraction too
	// thick.
	if d.Has(fonts.MetricUnderline) && d.UnderlineThickness > 0 {
		m.thickness = style.Max(size.Mul(float64(d.UnderlineThickness)/upem), 1)
		m.underline = style.Unit(0).Sub(size.Mul(float64(d.UnderlinePosition) / upem))
	}

	// The overline sits on the face's own ascent — the top of its content area,
	// which is where a browser draws it and is above every letter the face sets.
	ascent := size.Mul(float64(d.Ascent) / upem)
	m.overline = style.Unit(0).Sub(ascent)

	// The line-through goes through the middle of the lower-case letters. No
	// table this engine reads states a strikeout position, so the x-height is
	// estimated exactly as strutFor estimates it for "vertical-align: middle" —
	// seven tenths of the cap height, or half an em where no cap height is
	// declared. Two places using one estimate is deliberate: a document where the
	// two disagreed would have a strike and a middle-aligned box at different
	// heights for the same reason.
	// The line-through, when the face states one. OS/2 gives the position of the
	// stroke's top and its size, in the same convention as the underline.
	if d.Has(fonts.MetricStrikeout) && d.StrikeoutSize > 0 {
		strikeThickness := style.Max(size.Mul(float64(d.StrikeoutSize)/upem), 1)
		m.strike = style.Unit(0).Sub(size.Mul(float64(d.StrikeoutPosition) / upem))
		// A strikeout has a size of its own and it is not always the underline's.
		// Using the underline's here would draw a line the font asked to be
		// thinner or thicker, at the right height, which is the sort of wrong
		// that looks like a rendering choice.
		m.strikeThickness = strikeThickness
		return m
	}

	// No strikeout stated, so it is placed through the middle of the lower-case
	// letters. The x-height is the face's own when it states one, and otherwise
	// the same estimate strutFor uses for "vertical-align: middle" — seven
	// tenths of the cap height, or half an em. Two places sharing one estimate is
	// deliberate: a document where they disagreed would have a strike and a
	// middle-aligned box at different heights for the same reason.
	xHeight := size.Mul(0.5)
	switch {
	case d.Has(fonts.MetricXHeight) && d.XHeight > 0:
		xHeight = size.Mul(float64(d.XHeight) / upem)
	case d.CapHeight > 0:
		xHeight = size.Mul(float64(d.CapHeight) / upem * 0.7)
	}
	m.strike = style.Unit(0).Sub(xHeight.Div(2)).Sub(m.thickness.Div(2))
	return m
}

// decorationBand is the rectangle one line occupies for a run.
//
// x and width are the run's, and baseline is the y of the text's baseline in the
// same coordinates.
func decorationBand(kind decorationKind, x, width, baseline style.Unit,
	m decorationMetrics) Rect {

	top, height := m.underline, m.thickness
	switch kind {
	case decorationOverline:
		top = m.overline
	case decorationLineThrough:
		top = m.strike
		if m.strikeThickness > 0 {
			// The face stated a size for this line and it need not be the
			// underline's. Drawing it at the underline's would put a line the
			// font asked to be thinner or thicker at exactly the right height,
			// which reads as a choice rather than as a mistake.
			height = m.strikeThickness
		}
	}
	return Rect{X: x, Y: baseline.Add(top), W: width, H: height}
}
