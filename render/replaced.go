package render

import "github.com/mgilbir/pdf0/style"

// Sizing a replaced element: CSS 2.1 §10.3.2, §10.6.2 and the constraint table
// of §10.4.
//
// Every other box in this engine is sized from the outside in — a width is
// declared, or it fills what the containing block leaves, or it shrinks to fit
// its content. A replaced element is sized from the inside out, and the rules
// are more than "use the intrinsic size" in one way that matters: an image has
// an *intrinsic ratio*, so a declaration on one axis decides the other.
//
// That single sentence is most of what the sections say and most of what an
// implementation gets wrong. "<img src=x width=200>" on a 100 × 50 image is
// 200 × 100, not 200 × 50, and an engine that clamped the height to its
// intrinsic value would produce a squashed picture that looks like a bad
// photograph rather than like a layout bug.
//
// # Where each section applies
//
// The four sections that size a replaced element differ only in what happens
// *around* the box, not in how the box itself is measured:
//
//   - §10.3.2 inline replaced: the rules below, used as they stand.
//   - §10.3.4 block-level replaced: the width comes from §10.3.2 and then the
//     ordinary block margin arithmetic runs against it, which is what lets
//     "margin: 0 auto" centre an image.
//   - §10.3.5 floating replaced: the same, and "auto" is *not* shrink-to-fit —
//     a float's shrink-to-fit is about content, and replaced content has a size
//     rather than a preference.
//   - §10.3.6 and §10.6.5 absolutely positioned replaced: the same again, with
//     the resulting width and height treated as given by the author so that
//     §10.3.7's constraint solves for the offsets instead of for the size.
//
// So this is one function, called from four places, and every one of them
// substitutes its answer for a declared value rather than reimplementing it.
//
// # The default sizes
//
// 300 × 150 appears twice below and is not arbitrary: it is what CSS 2.1 gives
// a replaced element with no intrinsic dimensions and no ratio, and it is the
// size of an <iframe>, which is where the number came from. It is nearly
// unreachable here — an image that decoded has both dimensions, and an image
// that did not is not a replaced element at all — but the branch is written
// because the alternative is a zero-sized box, and a box of no size is the one
// answer that is silently wrong.

// defaultReplacedWidth and defaultReplacedHeight are CSS 2.1 §10.3.2's fallback
// dimensions.
var (
	defaultReplacedWidth  = mustPx(300)
	defaultReplacedHeight = mustPx(150)
)

// replacedSize resolves a replaced box's used content width and height.
//
// containing is the containing block's width, which is what a percentage width
// and every horizontal percentage resolve against. cbHeight and cbDefinite are
// its height and whether that height is known — a percentage height against a
// block still being sized by its content computes to auto rather than to zero,
// which is the distinction §10.5 draws and the one that stops an image
// collapsing inside an auto-height parent.
func (l *layouter) replacedSize(b *Box, containing, cbHeight style.Unit, cbDefinite bool) Size {
	rc := b.Replaced
	if rc == nil {
		return Size{}
	}

	width, hasWidth := l.lengthOf(b, "width", containing)
	height, hasHeight := l.verticalLength(b, "height", cbHeight, cbDefinite)
	// A negative width or height is not a declaration this engine honours; the
	// property does not accept one and the initial value stands. The test is made
	// against the *declared* value, before box-sizing takes the padding out of it,
	// because a border-box width smaller than its own padding is a legitimate
	// declaration for a content box of zero rather than an invalid one.
	if hasWidth && width < 0 {
		width, hasWidth = 0, false
	}
	if hasHeight && height < 0 {
		height, hasHeight = 0, false
	}
	// Everything below — §10.3.2's cases, the ratio arithmetic, and §10.4's
	// constraint table — is stated about the content box, so a border-box
	// declaration is converted once here and once for the limits in clampReplaced
	// rather than being carried through the table.
	insetH, insetV := l.sizingInset(b, containing)
	if hasWidth {
		width = maxZero(width.Sub(insetH))
	}
	if hasHeight {
		height = maxZero(height.Sub(insetV))
	}

	hasIntrinsicW := rc.Width > 0
	hasIntrinsicH := rc.Height > 0
	ratio := rc.Ratio

	var w, h style.Unit
	switch {
	case hasWidth && hasHeight:
		w, h = width, height

	case hasWidth:
		// §10.6.2: an auto height with a used width and a ratio is the width
		// divided by the ratio. This is the case that keeps "<img width=200>"
		// in proportion.
		w = width
		switch {
		case ratio > 0:
			h = w.Div(ratio)
		case hasIntrinsicH:
			h = rc.Height
		default:
			h = defaultReplacedHeight
		}

	case hasHeight:
		h = height
		switch {
		case ratio > 0:
			w = h.Mul(ratio)
		case hasIntrinsicW:
			w = rc.Width
		default:
			w = defaultReplacedWidth
		}

	default:
		// Both auto: §10.3.2's four cases, in the order it writes them.
		switch {
		case hasIntrinsicW && hasIntrinsicH:
			w, h = rc.Width, rc.Height
		case hasIntrinsicW:
			w = rc.Width
			if ratio > 0 {
				h = w.Div(ratio)
			} else {
				h = defaultReplacedHeight
			}
		case hasIntrinsicH:
			h = rc.Height
			if ratio > 0 {
				w = h.Mul(ratio)
			} else {
				w = defaultReplacedWidth
			}
		case ratio > 0:
			// A ratio and no dimensions. §10.3.2 asks for the largest box with
			// that ratio that does not overflow the containing block and is no
			// wider than 300px — which is what this is, written as the smaller
			// of the two.
			w = style.Min(containing, defaultReplacedWidth)
			h = w.Div(ratio)
		default:
			w, h = defaultReplacedWidth, defaultReplacedHeight
		}
	}

	return l.clampReplaced(b, w, h, ratio, containing, cbHeight, cbDefinite)
}

// clampReplaced applies the minimum and maximum constraints of CSS 2.1 §10.4.
//
// For a box with no intrinsic ratio the two axes are independent and this is
// two clamps. For a box with one they are not, and §10.4 gives a table of ten
// rows for the combinations — because clamping the width alone would change the
// shape of the picture, and clamping both independently would change it
// differently on each axis.
//
// The table is transcribed rather than derived. Two of its rows exist only to
// break the tie when both maxima are violated at once, and which of the two
// applies is decided by comparing the *ratios* of the violations rather than
// their sizes; an implementation that reasoned about it instead of copying it
// gets that comparison the wrong way round about half the time.
func (l *layouter) clampReplaced(b *Box, w, h style.Unit, ratio float64,
	containing, cbHeight style.Unit, cbDefinite bool) Size {

	// The limits are declared values, so under "border-box" they name the border
	// box and the table below is about the content box. Each is converted the
	// same way the declared width and height were.
	insetH, insetV := l.sizingInset(b, containing)

	minW := style.Unit(0)
	if v, ok := l.lengthOf(b, "min-width", containing); ok && v > 0 {
		minW = maxZero(v.Sub(insetH))
	}
	maxW := style.MaxUnit
	if v, ok := l.lengthOf(b, "max-width", containing); ok && v >= 0 {
		maxW = maxZero(v.Sub(insetH))
	}
	minH := style.Unit(0)
	if v, ok := l.verticalLength(b, "min-height", cbHeight, cbDefinite); ok && v > 0 {
		minH = maxZero(v.Sub(insetV))
	}
	maxH := style.MaxUnit
	if v, ok := l.verticalLength(b, "max-height", cbHeight, cbDefinite); ok && v >= 0 {
		maxH = maxZero(v.Sub(insetV))
	}

	// A minimum wins over a maximum that contradicts it, everywhere in CSS.
	// Folding that in here rather than at each of the ten branches below is
	// what keeps the table readable, and it is exactly what style.Clamp does
	// for the unconstrained case.
	if maxW < minW {
		maxW = minW
	}
	if maxH < minH {
		maxH = minH
	}

	if ratio <= 0 || w <= 0 || h <= 0 {
		// No ratio to preserve, so the axes do not interact. A zero extent is
		// here too: every row of the table divides by the tentative size, and a
		// box with none has no shape to keep.
		return Size{W: style.Clamp(w, minW, maxW), H: style.Clamp(h, minH, maxH)}
	}

	wide, narrow := w > maxW, w < minW
	tall, short := h > maxH, h < minH
	wf, hf := w.Px(), h.Px()

	switch {
	case wide && tall:
		// Both maxima violated. The axis that has to give way more is the one
		// that decides, which is a comparison of ratios and not of lengths.
		if maxW.Px()/wf <= maxH.Px()/hf {
			return Size{W: maxW, H: style.Max(minH, maxW.Mul(hf/wf))}
		}
		return Size{W: style.Max(minW, maxH.Mul(wf/hf)), H: maxH}

	case narrow && short:
		if minW.Px()/wf <= minH.Px()/hf {
			return Size{W: style.Min(maxW, minH.Mul(wf/hf)), H: minH}
		}
		return Size{W: minW, H: style.Min(maxH, minW.Mul(hf/wf))}

	case narrow && tall:
		// The two constraints pull in opposite directions and neither can be
		// traded for the other, so the ratio is abandoned. §10.4 says so
		// outright, and it is the only pair of rows that does.
		//
		// These two rows are the specification's and they are also, given
		// everything above, unobservable: a box narrower than its minimum and
		// taller than its maximum would reach the single-violation rows below
		// and get the same answer, because scaling it up to the minimum width
		// can only make it taller still, and the clamp there is the maximum
		// height. That was found by planting the removal of both and watching
		// nothing fail. They stay because the table is transcribed rather than
		// derived — an implementation that reasoned its way to which rows were
		// redundant would have to reason again every time one of the rows above
		// changed — and this note is here so that nobody hunts for the test
		// that pins them.
		return Size{W: minW, H: maxH}

	case wide && short:
		return Size{W: maxW, H: minH}

	case wide:
		return Size{W: maxW, H: style.Max(minH, maxW.Mul(hf/wf))}

	case narrow:
		return Size{W: minW, H: style.Min(maxH, minW.Mul(hf/wf))}

	case tall:
		return Size{W: style.Max(minW, maxH.Mul(wf/hf)), H: maxH}

	case short:
		return Size{W: style.Min(maxW, minH.Mul(wf/hf)), H: minH}
	}
	return Size{W: w, H: h}
}

// verticalLength resolves a property whose percentage is of the containing
// block's *height*, reporting whether it resolved at all.
//
// It is separate from lengthOf because the two differ in the one place that
// matters: a horizontal percentage always has a basis, since a containing
// block's width is settled before its contents are laid out, and a vertical one
// often does not. CSS 2.1 makes a percentage of an indefinite height compute to
// auto, and auto is what this reports — not zero, which is the plausible wrong
// answer that collapses an image to nothing inside an ordinary <div>.
func (l *layouter) verticalLength(b *Box, property string, basis style.Unit, definite bool) (style.Unit, bool) {
	length, ok := l.parseLength(b, property)
	if !ok || length.Kind == style.LengthAuto {
		return 0, false
	}
	return length.Resolve(basis, definite)
}

// replacedIntrinsicWidth is what a replaced box contributes to an intrinsic
// width calculation — the shrink-to-fit of a float around it, or the preferred
// width of a line it sits on.
//
// A declared length is used as it stands. A percentage is not: there is no
// containing block to take a percentage of while an intrinsic width is being
// measured, and CSS Sizing says such a percentage behaves as auto here, which
// is the intrinsic size. That is the same approximation intrinsic.go already
// documents for every other box, made in the same place and for the same
// reason.
func (l *layouter) replacedIntrinsicWidth(b *Box) style.Unit {
	rc := b.Replaced
	if rc == nil {
		return 0
	}
	insetH, insetV := l.sizingInset(b, 0)
	if length, ok := l.parseLength(b, "width"); ok && length.Kind == style.LengthAbsolute {
		return maxZero(l.clampWidth(b, maxZero(length.Value.Sub(insetH)), 0))
	}
	// An auto width with a declared height and a ratio is decided by the
	// height, exactly as §10.3.2 decides it in layout.
	if length, ok := l.parseLength(b, "height"); ok && length.Kind == style.LengthAbsolute && rc.Ratio > 0 {
		return maxZero(l.clampWidth(b, maxZero(length.Value.Sub(insetV)).Mul(rc.Ratio), 0))
	}
	return maxZero(l.clampWidth(b, rc.Width, 0))
}
