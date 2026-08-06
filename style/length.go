package style

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
)

// Lengths: turning the text a declaration carries into a number layout can use.
//
// A length is not resolvable on its own. "1em" needs a font size, "50%" needs
// something to be a percentage of, and "10vw" needs the page. So this is two
// steps rather than one: a length is *parsed* into a value and a unit, and
// *resolved* against a context that supplies whatever the unit refers to. The
// split matters because the two happen at different times — parsing during the
// cascade, resolving during layout, when a containing block finally exists.

// LengthKind says what a parsed length still needs before it is a number.
type LengthKind uint8

const (
	// LengthAbsolute is already a number of layout units: px, pt, cm and the
	// rest, and the font-relative units once a font size is known.
	LengthAbsolute LengthKind = iota
	// LengthPercent is a percentage of something the containing block decides.
	// It cannot be resolved here, and carrying it as though it could is how a
	// width ends up a fraction of the wrong box.
	LengthPercent
	// LengthAuto is the keyword, which is not a length at all — it is an
	// instruction to layout to work the value out.
	LengthAuto
)

// Length is a parsed length: a number, and what it is a number of.
type Length struct {
	Kind LengthKind
	// Value is the resolved length when Kind is LengthAbsolute, and meaningless
	// otherwise.
	Value Unit
	// Percent is the percentage when Kind is LengthPercent, on a 0-100 scale.
	Percent float64
}

// Auto is the keyword.
var Auto = Length{Kind: LengthAuto}

// Zero is a length of nothing, which is what most box properties start at.
var Zero = Length{Kind: LengthAbsolute}

// Resolve turns a length into a number, given what a percentage is of.
//
// A percentage of an indefinite size is itself indefinite — a height of "50%"
// inside a box whose own height is auto resolves to auto, not to zero, and
// treating it as zero is how a box silently collapses. definite says whether
// basis means anything.
func (l Length) Resolve(basis Unit, definite bool) (Unit, bool) {
	switch l.Kind {
	case LengthAbsolute:
		return l.Value, true
	case LengthPercent:
		if !definite {
			return 0, false
		}
		return basis.Mul(l.Percent / 100), true
	}
	return 0, false
}

// LengthContext is what the font-relative and viewport-relative units refer to.
type LengthContext struct {
	// FontSize is the computed font-size of the element the length belongs to,
	// which is what "em" is relative to.
	//
	// The one exception is font-size itself: an "em" in a font-size declaration
	// is relative to the *parent's* font size, because the element's own is what
	// is being computed. A caller resolving font-size passes the parent's here.
	FontSize Unit

	// RootFontSize is the font-size of the root element, which "rem" is relative
	// to. It is what makes rem useful: a length that does not compound as
	// elements nest.
	RootFontSize Unit

	// ViewportWidth and ViewportHeight are the page box, which is this engine's
	// viewport — there is no window. They are zero when the page is not yet
	// known, and a viewport-relative length is then unresolvable rather than
	// zero.
	ViewportWidth, ViewportHeight Unit
	ViewportKnown                 bool

	// ZeroAdvance is the width of "0" in the element's own font, which is what
	// "ch" is. It is zero when no face has been chosen — during the cascade,
	// where a length is parsed before layout knows what will set it — and
	// FontMetricsKnown says which of the two a zero means.
	ZeroAdvance      Unit
	FontMetricsKnown bool
}

// ParseLength reads a length from component values.
//
// unsupported distinguishes a unit that is correct CSS this engine does not
// resolve — "3lh" needs the line height, which is not threaded here — from input
// that is not a length at all. The caller reports them differently, because they
// send an author to different places.
func ParseLength(vals []css.ComponentValue, ctx LengthContext) (l Length, unsupported bool, ok bool) {
	parts := splitOnWhitespace(vals)
	if len(parts) != 1 || len(parts[0]) != 1 {
		return Length{}, false, false
	}
	v := parts[0][0]
	if !v.IsToken() {
		// A function such as calc() is a length this engine does not compute
		// yet, and is correct CSS.
		if v.IsFunction() && strings.EqualFold(v.Token.Value, "calc") {
			return Length{}, true, false
		}
		return Length{}, false, false
	}
	t := v.Token

	switch t.Kind {
	case css.Ident:
		if strings.EqualFold(t.Value, "auto") {
			return Auto, false, true
		}
		return Length{}, false, false

	case css.Percentage:
		return Length{Kind: LengthPercent, Percent: t.Number}, false, true

	case css.Number:
		// Only zero may be written without a unit. Every other bare number is a
		// mistake that browsers also refuse, and accepting it would silently
		// read "margin: 10" as ten of something.
		if t.Number == 0 {
			return Zero, false, true
		}
		return Length{}, false, false

	case css.Dimension:
		px, known, supported := pxPerUnit(t.Unit, ctx)
		if !supported {
			// Either a real unit this engine does not resolve, or not a unit at
			// all — pxPerUnit knows which, and only the first is "unsupported".
			return Length{}, unresolvedUnits[strings.ToLower(t.Unit)], false
		}
		if !known {
			// A unit this engine resolves, in a context that does not yet have
			// what it refers to.
			return Length{}, true, false
		}
		u, fits := FromPx(t.Number * px)
		if !fits {
			// A length past what the range holds. It is saturated rather than
			// wrapped, and reported: "width: 1e9px" is a stylesheet saying
			// something impossible, and laying out the saturated value silently
			// would produce a page with one enormous box and no explanation.
			return Length{Kind: LengthAbsolute, Value: u}, false, false
		}
		return Length{Kind: LengthAbsolute, Value: u}, false, true
	}
	return Length{}, false, false
}

// pxPerUnit returns how many CSS pixels one of a unit is.
//
// known is false when the unit is one this engine resolves but the context does
// not supply what it refers to — a viewport unit before the page is decided.
// supported is false when the unit needs something not threaded here at all.
func pxPerUnit(unit string, ctx LengthContext) (px float64, known, supported bool) {
	switch strings.ToLower(unit) {
	case "px":
		return 1, true, true
	case "pt":
		return pxPerPt, true, true
	case "pc":
		return pxPerPc, true, true
	case "in":
		return pxPerIn, true, true
	case "cm":
		return pxPerCm, true, true
	case "mm":
		return pxPerMm, true, true
	case "q":
		return pxPerQ, true, true

	case "em":
		return ctx.FontSize.Px(), true, true
	case "rem":
		return ctx.RootFontSize.Px(), true, true

	// ch is the advance of "0" in the element's own font. It is what an author
	// reaches for to size a box in characters — "width: 40ch" is a column forty
	// digits wide — and it is exact for the monospaced fonts that use is nearly
	// always about.
	case "ch":
		if !ctx.FontMetricsKnown {
			return 0, false, true
		}
		return ctx.ZeroAdvance.Px(), true, true

	// ex is the font's x-height.
	//
	// The face layer does not carry one: OS/2 has the field but forme's
	// descriptor stops at cap height, and recovering it would mean measuring the
	// outline of "x". CSS Values §5.1.2 settles what to do — "in the cases where
	// it is impossible or impractical to determine the x-height, a value of
	// 0.5em must be assumed" — so this is the specified answer rather than an
	// approximation standing in for one.
	case "ex":
		return ctx.FontSize.Px() / 2, true, true

	// The viewport units, and the small/large/dynamic variants of them.
	//
	// The variants exist because a phone's viewport changes as its toolbars come
	// and go, so "svh" is the smallest it gets and "lvh" the largest. A page has
	// no toolbars and does not scroll, so all three are the same number here —
	// which is not an approximation but what the definition reduces to when the
	// viewport cannot change.
	case "vw", "svw", "lvw", "dvw":
		if !ctx.ViewportKnown {
			return 0, false, true
		}
		return ctx.ViewportWidth.Px() / 100, true, true
	case "vh", "svh", "lvh", "dvh":
		if !ctx.ViewportKnown {
			return 0, false, true
		}
		return ctx.ViewportHeight.Px() / 100, true, true
	case "vmin", "svmin", "lvmin", "dvmin":
		if !ctx.ViewportKnown {
			return 0, false, true
		}
		return Min(ctx.ViewportWidth, ctx.ViewportHeight).Px() / 100, true, true
	case "vmax", "svmax", "lvmax", "dvmax":
		if !ctx.ViewportKnown {
			return 0, false, true
		}
		return Max(ctx.ViewportWidth, ctx.ViewportHeight).Px() / 100, true, true
	}

	if unresolvedUnits[strings.ToLower(unit)] {
		return 0, false, false
	}
	// Not a unit at all. "1foo" is a typo, not a limit of this engine, and
	// telling an author it is unsupported would send them looking for a feature
	// request instead of a spelling mistake.
	return 0, false, false
}

// unresolvedUnits are real CSS units this engine does not resolve, as against
// text that is not a unit.
//
// The distinction is only visible in a diagnostic, and that is exactly why it
// matters: "3ex is not implemented" and "1foo is not a unit" send an author to
// different places, and getting it wrong sends them to the wrong one.
//
// The font-relative ones need the face the element will be set in, which layout
// chooses. The container ones need a container query, which needs a layout pass
// before the cascade. The logical ones need a writing mode.
var unresolvedUnits = map[string]bool{
	// Font-relative, needing metrics from the chosen face. "ex" and "ch" are
	// resolved; the rest need a metric the face layer does not carry.
	"cap": true, "ic": true,
	"rex": true, "rch": true, "rcap": true, "ric": true,
	"lh": true, "rlh": true,
	// Container-relative.
	"cqw": true, "cqh": true, "cqi": true, "cqb": true,
	"cqmin": true, "cqmax": true,
	// Logical viewport units, needing a writing mode.
	"vi": true, "vb": true,
	"svi": true, "svb": true, "lvi": true, "lvb": true, "dvi": true, "dvb": true,
}

// absoluteFontSizes is the keyword scale of CSS Fonts, in CSS pixels.
//
// "medium" is 16px, which is where every browser's default sits, and the rest
// are the specification's own ratios rather than a geometric series — the scale
// is not uniform, and inventing one would make "small" and "large" the wrong
// sizes in a document that names them.
var absoluteFontSizes = map[string]float64{
	"xx-small":  9,
	"x-small":   10,
	"small":     13,
	"medium":    16,
	"large":     18,
	"x-large":   24,
	"xx-large":  32,
	"xxx-large": 48,
}

// relativeFontSizes are the two keywords that scale from the parent.
var relativeFontSizes = map[string]float64{
	"larger":  1.2,
	"smaller": 1 / 1.2,
}

// ResolveFontSize computes an element's font-size from its declared value and
// its parent's.
//
// It is separate from ParseLength because font-size is the one property whose
// own em is relative to the *parent* — the element's own size is what is being
// computed — and because it accepts a scale of keywords no other length does.
//
// A percentage is also relative to the parent, which is why it can be resolved
// here when ParseLength would have to decline it.
func ResolveFontSize(vals []css.ComponentValue, parent, root Unit) (u Unit, unsupported bool, ok bool) {
	parts := splitOnWhitespace(vals)
	if len(parts) == 1 && len(parts[0]) == 1 && parts[0][0].IsToken() {
		t := parts[0][0].Token
		if t.Kind == css.Ident {
			name := strings.ToLower(t.Value)
			if px, isAbsolute := absoluteFontSizes[name]; isAbsolute {
				v, fits := FromPx(px)
				return v, false, fits
			}
			if factor, isRelative := relativeFontSizes[name]; isRelative {
				return parent.Mul(factor), false, true
			}
			return 0, false, false
		}
		if t.Kind == css.Percentage {
			// Negative is forbidden however it is written, and the percentage
			// path reaches here before the length path's own check.
			if t.Number < 0 {
				return 0, false, false
			}
			return parent.Mul(t.Number / 100), false, true
		}
	}

	// Everything else is an ordinary length, resolved with the parent's size
	// standing in for "em".
	l, unsupported, ok := ParseLength(vals, LengthContext{FontSize: parent, RootFontSize: root})
	if !ok || l.Kind != LengthAbsolute {
		return 0, unsupported, false
	}
	// A negative font size is not a small one; the specification forbids it.
	if l.Value < 0 {
		return 0, false, false
	}
	return l.Value, false, true
}
