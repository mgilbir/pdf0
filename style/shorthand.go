package style

import (
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/css"
)

// The shorthands whose parts are told apart by *type* rather than by position.
//
// "margin: 1px 2px" is positional: the first value is the top whatever it is.
// "border: 1px solid red" is not — the three may be written in any order, and
// which longhand each belongs to is decided by what it is. A length is the
// width, a keyword from a closed set is the style, anything that parses as a
// colour is the colour.
//
// # Why a shorthand resets what it does not mention
//
// This is the rule that makes shorthands worth having and the one most often got
// wrong. "border: solid" does not only set the style: it sets the width and the
// colour to their initial values as well. So a rule that says "border-width: 5px"
// and then "border: solid" has a *medium* border, not a five-pixel one.
//
// An expander that returned only the parts it saw would leave the others at
// whatever an earlier declaration had set, which is a page that is wrong in a way
// the stylesheet does not explain.

// ident builds a component value for a keyword, which is how an expander says
// "this longhand takes its initial value".
func ident(name string) []css.ComponentValue {
	return []css.ComponentValue{{Token: css.Token{Kind: css.Ident, Value: name}}}
}

// borderStyleKeywords is the closed set that identifies the style part.
var borderStyleKeywords = map[string]bool{
	"none": true, "hidden": true, "dotted": true, "dashed": true,
	"solid": true, "double": true, "groove": true, "ridge": true,
	"inset": true, "outset": true,
}

// borderWidthKeywords is the other closed set. It is separate because "none" is
// a style and "medium" is a width, and a single set would make "border: none"
// ambiguous.
var borderWidthKeywords = map[string]bool{
	"thin": true, "medium": true, "thick": true,
}

// borderShorthand expands "border" and the four per-side forms.
//
// sides is which edges it sets: all four for "border", one for "border-top".
func borderShorthand(sides ...string) expander {
	return func(vals []css.ComponentValue) (map[string][]css.ComponentValue, []string, bool) {
		width, style, colour := ident("medium"), ident("none"), ident("currentcolor")
		var seenWidth, seenStyle, seenColour bool

		for _, part := range splitOnWhitespace(vals) {
			switch {
			case isBorderStyle(part) && !seenStyle:
				style, seenStyle = part, true
			case isBorderWidth(part) && !seenWidth:
				width, seenWidth = part, true
			case isColour(part) && !seenColour:
				colour, seenColour = part, true
			default:
				// A part that is none of the three, or a second of one kind.
				// Either way the declaration is invalid, and an invalid
				// shorthand sets nothing at all rather than the parts that did
				// parse — half a border is not what was asked for.
				return nil, nil, false
			}
		}
		if !seenWidth && !seenStyle && !seenColour {
			return nil, nil, false
		}

		out := map[string][]css.ComponentValue{}
		for _, side := range sides {
			out["border-"+side+"-width"] = width
			out["border-"+side+"-style"] = style
			out["border-"+side+"-color"] = colour
		}
		return out, nil, true
	}
}

func isBorderStyle(part []css.ComponentValue) bool {
	if len(part) != 1 || !part[0].IsToken() || part[0].Token.Kind != css.Ident {
		return false
	}
	return borderStyleKeywords[strings.ToLower(part[0].Token.Value)]
}

func isBorderWidth(part []css.ComponentValue) bool {
	if len(part) != 1 || !part[0].IsToken() {
		return false
	}
	t := part[0].Token
	if t.Kind == css.Ident {
		return borderWidthKeywords[strings.ToLower(t.Value)]
	}
	// A length. Zero may be written without a unit, which is why a plain
	// number counts here and nowhere else in this file.
	return t.Kind == css.Dimension || (t.Kind == css.Number && t.Number == 0)
}

func isColour(part []css.ComponentValue) bool {
	_, ok := ParseColor(part)
	return ok
}

// backgroundShorthand expands "background".
//
// It is the widest shorthand this engine has: eight longhands, seven of which
// may be written in any order within a layer, and a layer list separated by
// commas on top of that. Three things about its grammar are worth stating
// because each is a place an expander goes quietly wrong.
//
// The colour belongs to the *last* layer only. CSS puts it there because there
// is one background colour however many images are stacked over it, and an
// expander that accepted a colour in any layer would silently let
// "background: red url(a), url(b)" through — a declaration a browser rejects
// whole, so the page it produces would be one no browser shows.
//
// Two <box> values are the origin and then the clip, in that order; one sets
// both. So "background: url(x) content-box" clips to the content box as well as
// starting there, which is not what the two properties' *initial* values do —
// they differ, padding-box against border-box — and is the single most
// surprising line in the grammar.
//
// The size follows the position after a slash, and only there. That is what
// makes the slash load-bearing rather than decorative: "center / cover" and
// "center cover" are a valid declaration and an invalid one.
//
// The reset happens in full for every longhand the declaration does not mention,
// which is what makes "background: red" undo an image an earlier rule set.
func backgroundShorthand(vals []css.ComponentValue) (map[string][]css.ComponentValue, []string, bool) {
	layers := splitOnComma(vals)
	if len(layers) == 0 {
		return nil, nil, false
	}
	if len(layers) > maxBackgroundLayers {
		// A declaration is untrusted input, and every layer here becomes a
		// painting pass over a box. The bound is far past any design and far
		// short of a stylesheet that asks for a hundred thousand of them.
		return nil, nil, false
	}

	colour := ident("transparent")
	per := map[string][]([]css.ComponentValue){}
	add := func(name string, v []css.ComponentValue) {
		per[name] = append(per[name], v)
	}

	for i, layer := range layers {
		last := i == len(layers)-1
		got, ok := backgroundLayer(layer, last)
		if !ok {
			return nil, nil, false
		}
		if got.colour != nil {
			colour = got.colour
		}
		add("background-image", got.image)
		add("background-repeat", got.repeat)
		add("background-attachment", got.attachment)
		add("background-position", got.position)
		add("background-size", got.size)
		add("background-origin", got.origin)
		add("background-clip", got.clip)
	}

	out := map[string][]css.ComponentValue{"background-color": colour}
	for name, values := range per {
		out[name] = joinOnComma(values...)
	}
	return out, nil, true
}

// maxBackgroundLayers bounds one declaration's layer list.
//
// It is a variable rather than a constant so a test can lower it far enough to
// watch it fire without writing a stylesheet with a thousand commas in it.
var maxBackgroundLayers = 1024

// bgLayer is one layer's worth of longhand values, each already defaulted.
type bgLayer struct {
	image, repeat, attachment, position, size, origin, clip []css.ComponentValue
	// colour is nil unless this layer carried one, which only the last may.
	colour []css.ComponentValue
}

// backgroundLayer reads one comma-separated layer of the shorthand.
//
// last says whether a colour is allowed here. Everything else is identified by
// what it is rather than by where it sits, which is why this is a loop over
// parts with a case per longhand rather than a positional read.
func backgroundLayer(vals []css.ComponentValue, last bool) (bgLayer, bool) {
	out := bgLayer{
		image:      ident("none"),
		repeat:     ident("repeat"),
		attachment: ident("scroll"),
		position:   percentPair(0, 0),
		size:       ident("auto"),
		origin:     ident("padding-box"),
		clip:       ident("border-box"),
	}
	var seenImage, seenRepeat, seenAttachment, seenPosition, seenColour bool
	var boxes int

	parts := splitSlashes(splitOnWhitespace(vals))
	if len(parts) == 0 {
		// An empty layer is "background: ,". Nothing to set and nothing that
		// could have been meant.
		return bgLayer{}, false
	}

	for i := 0; i < len(parts); {
		part := parts[i]
		switch {
		case isBackgroundImage(part) && !seenImage:
			out.image, seenImage = part, true
			i++

		case isRepeatKeyword(part) && !seenRepeat:
			// "repeat-x" and "repeat-y" are whole values rather than per-axis
			// ones, so neither may be half of a pair: "repeat-x no-repeat" is
			// not a repeat style, it is an invalid declaration.
			if i+1 < len(parts) && isRepeatKeyword(parts[i+1]) &&
				!isAxisRepeatKeyword(part) && !isAxisRepeatKeyword(parts[i+1]) {
				out.repeat = joinParts(part, parts[i+1])
				i += 2
			} else {
				out.repeat = part
				i++
			}
			seenRepeat = true

		case isAttachmentKeyword(part) && !seenAttachment:
			out.attachment, seenAttachment = part, true
			i++

		case isBoxKeyword(part):
			switch boxes {
			case 0:
				// One <box> sets both, and the second overrides the clip.
				out.origin, out.clip = part, part
			case 1:
				out.clip = part
			default:
				return bgLayer{}, false
			}
			boxes++
			i++

		case isPositionComponent(part) && !seenPosition:
			j := i
			for j < len(parts) && j-i < 4 && isPositionComponent(parts[j]) {
				j++
			}
			out.position, seenPosition = joinParts(parts[i:j]...), true
			i = j
			if i < len(parts) && isSlash(parts[i]) {
				i++
				k := i
				for k < len(parts) && k-i < 2 && isSizeComponent(parts[k]) {
					k++
				}
				if k == i {
					// A slash with nothing after it that is a size.
					return bgLayer{}, false
				}
				out.size = joinParts(parts[i:k]...)
				i = k
			}

		case last && isColour(part) && !seenColour:
			out.colour, seenColour = part, true
			i++

		default:
			// A part that belongs to no slot, a second of one kind, or a colour
			// in a layer that is not the last. An invalid shorthand sets nothing
			// at all rather than the parts that happened to parse.
			return bgLayer{}, false
		}
	}
	return out, true
}

// splitOnComma divides a value at its top-level commas.
//
// A comma inside a function — rgb(1, 2, 3) — is not a top level one, and
// arrives here inside a single component value rather than as a token, so
// nothing has to be done to skip it.
func splitOnComma(vals []css.ComponentValue) [][]css.ComponentValue {
	var out [][]css.ComponentValue
	start := 0
	for i, v := range vals {
		if v.IsToken() && v.Token.Kind == css.Comma {
			out = append(out, vals[start:i])
			start = i + 1
		}
	}
	return append(out, vals[start:])
}

// joinOnComma is the inverse, used to rebuild one longhand's layer list.
func joinOnComma(parts ...[]css.ComponentValue) []css.ComponentValue {
	var out []css.ComponentValue
	for i, p := range parts {
		if i > 0 {
			out = append(out, css.ComponentValue{Token: css.Token{Kind: css.Comma}})
		}
		out = append(out, p...)
	}
	return out
}

// splitSlashes makes every "/" a part of its own.
//
// A slash is a delimiter rather than whitespace, so "center/cover" arrives as
// one part of three tokens while "center / cover" arrives as three parts. The
// grammar does not distinguish them, so neither does anything after this.
func splitSlashes(parts [][]css.ComponentValue) [][]css.ComponentValue {
	var out [][]css.ComponentValue
	for _, part := range parts {
		start := 0
		for i, v := range part {
			if !v.IsToken() || !v.Token.IsDelim('/') {
				continue
			}
			if i > start {
				out = append(out, part[start:i])
			}
			out = append(out, part[i:i+1])
			start = i + 1
		}
		if start < len(part) {
			out = append(out, part[start:])
		}
	}
	return out
}

func isSlash(part []css.ComponentValue) bool {
	return len(part) == 1 && part[0].IsToken() && part[0].Token.IsDelim('/')
}

// percentPair builds "x% y%", which is how the position's initial value is
// written.
func percentPair(x, y float64) []css.ComponentValue {
	pct := func(v float64) css.ComponentValue {
		return css.ComponentValue{Token: css.Token{
			Kind: css.Percentage, Number: v, Repr: strconv.FormatFloat(v, 'f', -1, 64),
		}}
	}
	return []css.ComponentValue{
		pct(x), {Token: css.Token{Kind: css.Whitespace}}, pct(y),
	}
}

// isBackgroundImage reports whether a part is an <image> or the keyword that
// stands for none of one.
//
// A gradient is accepted here and refused later, by the stage that would have to
// paint it. That division is deliberate: the shorthand's job is to decide which
// longhand a part belongs to, and "linear-gradient(...)" belongs to
// background-image whether or not anything can draw it. Rejecting it here would
// make the whole declaration invalid, which would throw away the repeat and the
// position the author wrote beside it and report the wrong thing.
func isBackgroundImage(part []css.ComponentValue) bool {
	if len(part) != 1 {
		return false
	}
	v := part[0]
	if v.IsFunction() {
		switch strings.ToLower(v.Token.Value) {
		case "url", "src", "image", "image-set", "cross-fade", "element",
			"linear-gradient", "radial-gradient", "conic-gradient",
			"repeating-linear-gradient", "repeating-radial-gradient",
			"repeating-conic-gradient":
			return true
		}
		return false
	}
	if !v.IsToken() {
		return false
	}
	switch v.Token.Kind {
	case css.URL:
		return true
	case css.Ident:
		return strings.EqualFold(v.Token.Value, "none")
	}
	return false
}

func isRepeatKeyword(part []css.ComponentValue) bool {
	if !isIdentPart(part) {
		return false
	}
	switch strings.ToLower(part[0].Token.Value) {
	case "repeat", "repeat-x", "repeat-y", "no-repeat", "space", "round":
		return true
	}
	return false
}

// isAxisRepeatKeyword names the two that stand for a pair and so cannot be one
// half of one.
func isAxisRepeatKeyword(part []css.ComponentValue) bool {
	if !isIdentPart(part) {
		return false
	}
	switch strings.ToLower(part[0].Token.Value) {
	case "repeat-x", "repeat-y":
		return true
	}
	return false
}

func isAttachmentKeyword(part []css.ComponentValue) bool {
	if !isIdentPart(part) {
		return false
	}
	switch strings.ToLower(part[0].Token.Value) {
	case "scroll", "fixed", "local":
		return true
	}
	return false
}

func isBoxKeyword(part []css.ComponentValue) bool {
	if !isIdentPart(part) {
		return false
	}
	switch strings.ToLower(part[0].Token.Value) {
	case "border-box", "padding-box", "content-box":
		return true
	}
	return false
}

func isPositionComponent(part []css.ComponentValue) bool {
	if isIdentPart(part) {
		switch strings.ToLower(part[0].Token.Value) {
		case "left", "right", "top", "bottom", "center":
			return true
		}
		return false
	}
	return isLengthOrPercent(part)
}

func isSizeComponent(part []css.ComponentValue) bool {
	if isIdentPart(part) {
		switch strings.ToLower(part[0].Token.Value) {
		case "auto", "cover", "contain":
			return true
		}
		return false
	}
	return isLengthOrPercent(part)
}

// isLengthOrPercent accepts what a background position or size may be written
// as, which includes a bare zero and nothing else without a unit.
func isLengthOrPercent(part []css.ComponentValue) bool {
	if len(part) != 1 || !part[0].IsToken() {
		if len(part) == 1 && part[0].IsFunction() &&
			strings.EqualFold(part[0].Token.Value, "calc") {
			return true
		}
		return false
	}
	switch t := part[0].Token; t.Kind {
	case css.Dimension, css.Percentage:
		return true
	case css.Number:
		return t.Number == 0
	}
	return false
}

func isNone(part []css.ComponentValue) bool {
	return len(part) == 1 && part[0].IsToken() &&
		part[0].Token.Kind == css.Ident &&
		strings.EqualFold(part[0].Token.Value, "none")
}

// listStyleShorthand expands "list-style": a type, a position, and an image.
func listStyleShorthand(vals []css.ComponentValue) (map[string][]css.ComponentValue, []string, bool) {
	kind, position := ident("disc"), ident("outside")
	var seenKind, seenPosition bool
	var unsupported []string

	for _, part := range splitOnWhitespace(vals) {
		switch {
		case isListPosition(part) && !seenPosition:
			position, seenPosition = part, true
		case isNone(part):
			// "none" may be the type or the image. Taking it as the type is
			// what an author means by "list-style: none", which is the whole
			// reason the value is written.
			if !seenKind {
				kind, seenKind = part, true
			}
		case isIdentPart(part) && !seenKind:
			kind, seenKind = part, true
		default:
			unsupported = append(unsupported, serialize(part))
		}
	}
	return map[string][]css.ComponentValue{
		"list-style-type":     kind,
		"list-style-position": position,
	}, unsupported, true
}

func isListPosition(part []css.ComponentValue) bool {
	if !isIdentPart(part) {
		return false
	}
	switch strings.ToLower(part[0].Token.Value) {
	case "inside", "outside":
		return true
	}
	return false
}

func isIdentPart(part []css.ComponentValue) bool {
	return len(part) == 1 && part[0].IsToken() && part[0].Token.Kind == css.Ident
}

// fontShorthand expands "font".
//
// Unlike the others this one *is* positional at the end: the size, an optional
// line-height after a slash, and then the family list, in that order. What comes
// before is style, variant and weight in any order — which is why it is here
// rather than with the box shorthands.
func fontShorthand(vals []css.ComponentValue) (map[string][]css.ComponentValue, []string, bool) {
	parts := splitOnWhitespace(vals)
	if len(parts) == 0 {
		return nil, nil, false
	}
	// The system-font keywords set every part at once from something this
	// engine has no access to.
	if len(parts) == 1 && isIdentPart(parts[0]) {
		switch strings.ToLower(parts[0][0].Token.Value) {
		case "caption", "icon", "menu", "message-box", "small-caption", "status-bar":
			return nil, []string{"the system font " + serialize(parts[0])}, false
		}
	}

	style, weight := ident("normal"), ident("normal")
	var size, lineHeight, family []css.ComponentValue

	i := 0
	for ; i < len(parts); i++ {
		part := parts[i]
		if isFontSize(part) {
			break
		}
		if !isIdentPart(part) && !isNumberPart(part) {
			return nil, nil, false
		}
		switch strings.ToLower(serialize(part)) {
		case "italic", "oblique":
			style = part
		case "bold", "bolder", "lighter", "100", "200", "300", "400",
			"500", "600", "700", "800", "900":
			weight = part
		case "normal", "small-caps":
			// "normal" says nothing and small-caps is a variant this engine
			// does not set; neither changes what is produced.
		default:
			return nil, nil, false
		}
	}
	if i >= len(parts) {
		// No size, so this is not a font shorthand at all — the size and the
		// family are the two required parts.
		return nil, nil, false
	}

	size = parts[i]
	// A line-height written as "size/height" arrives as one part, because the
	// slash is a delimiter rather than whitespace.
	if s, h, ok := splitOnSlash(size); ok {
		size, lineHeight = s, h
	} else if i+1 < len(parts) {
		if s, h, ok := splitOnSlash(joinParts(parts[i], parts[i+1])); ok {
			size, lineHeight = s, h
			i++
		}
	}
	i++
	if i >= len(parts) {
		return nil, nil, false
	}
	family = joinParts(parts[i:]...)

	out := map[string][]css.ComponentValue{
		"font-style":  style,
		"font-weight": weight,
		"font-size":   size,
		"font-family": family,
	}
	// The shorthand resets line-height whether or not it was written, which is
	// what makes "font: 12px serif" undo an inherited one.
	if lineHeight != nil {
		out["line-height"] = lineHeight
	} else {
		out["line-height"] = ident("normal")
	}
	return out, nil, true
}

func isFontSize(part []css.ComponentValue) bool {
	if len(part) == 0 || !part[0].IsToken() {
		return false
	}
	switch t := part[0].Token; t.Kind {
	case css.Dimension, css.Percentage:
		return true
	case css.Number:
		return t.Number == 0
	case css.Ident:
		if _, ok := absoluteFontSizes[strings.ToLower(t.Value)]; ok {
			return true
		}
		_, ok := relativeFontSizes[strings.ToLower(t.Value)]
		return ok
	}
	return false
}

func isNumberPart(part []css.ComponentValue) bool {
	return len(part) == 1 && part[0].IsToken() && part[0].Token.Kind == css.Number
}

// splitOnSlash divides "12px/1.5" into its two halves.
func splitOnSlash(part []css.ComponentValue) (before, after []css.ComponentValue, ok bool) {
	for i, v := range part {
		if v.IsToken() && v.Token.IsDelim('/') {
			if i == 0 || i == len(part)-1 {
				return nil, nil, false
			}
			return part[:i], part[i+1:], true
		}
	}
	return nil, nil, false
}

func joinParts(parts ...[]css.ComponentValue) []css.ComponentValue {
	var out []css.ComponentValue
	for i, p := range parts {
		if i > 0 {
			out = append(out, css.ComponentValue{Token: css.Token{Kind: css.Whitespace}})
		}
		out = append(out, p...)
	}
	return out
}

// textDecorationShorthand expands "text-decoration": the lines and a colour.
//
// The line part is a *set* rather than a single keyword — "text-decoration:
// underline overline" is one declaration asking for two lines — so the keywords
// are gathered rather than the first one taken. An earlier version kept only the
// first and reported the second as a part it could not produce, which was a
// finding about the engine's own reading rather than about anything unimplemented.
//
// "none" cannot be combined with anything, and a repeated keyword is not a valid
// value either; both make the whole declaration invalid, which sets nothing at
// all rather than the parts that happened to parse.
func textDecorationShorthand(vals []css.ComponentValue) (map[string][]css.ComponentValue, []string, bool) {
	var lines []css.ComponentValue
	colour := ident("currentcolor")
	var seenNone, seenColour bool
	seen := map[string]bool{}
	var unsupported []string

	for _, part := range splitOnWhitespace(vals) {
		switch {
		case isDecorationLine(part):
			name := strings.ToLower(part[0].Token.Value)
			if seen[name] || seenNone || (name == "none" && len(lines) > 0) {
				return nil, nil, false
			}
			seen[name] = true
			seenNone = name == "none"
			if len(lines) > 0 {
				lines = append(lines, css.ComponentValue{
					Token: css.Token{Kind: css.Whitespace},
				})
			}
			lines = append(lines, part...)
		case isColour(part) && !seenColour:
			colour, seenColour = part, true
		case isIdentPart(part):
			// A keyword this engine understood as belonging to the shorthand and
			// cannot produce: "blink", or one of the CSS Text Decoration 3 styles
			// such as "wavy".
			unsupported = append(unsupported, serialize(part))
		default:
			return nil, nil, false
		}
	}
	if len(lines) == 0 {
		// The shorthand resets what it does not mention, so a declaration that
		// named only a colour still turns the lines off.
		lines = ident("none")
	}
	return map[string][]css.ComponentValue{
		"text-decoration-line":  lines,
		"text-decoration-color": colour,
	}, unsupported, true
}

func isDecorationLine(part []css.ComponentValue) bool {
	if !isIdentPart(part) {
		return false
	}
	switch strings.ToLower(part[0].Token.Value) {
	case "none", "underline", "overline", "line-through":
		return true
	}
	return false
}

// whiteSpaceShorthand is CSS Text 4's table for the property that used to be one
// keyword.
//
//	white-space-collapse | text-wrap-mode
//	normal        collapse         wrap
//	pre           preserve         nowrap
//	nowrap        collapse         nowrap
//	pre-wrap      preserve         wrap
//	pre-line      preserve-breaks  wrap
//	break-spaces  break-spaces     wrap
//
// The two-value syntax the level 4 draft also allows — "white-space: preserve
// nowrap" — is deliberately not accepted. Nothing in the suite writes it, the
// keywords it takes are the longhands' own, and an expander that guessed at
// which of two idents belonged to which longhand would be inventing a grammar.
func whiteSpaceShorthand(vals []css.ComponentValue) (map[string][]css.ComponentValue, []string, bool) {
	name, ok := singleIdent(vals)
	if !ok {
		return nil, nil, false
	}
	var collapse, mode string
	switch name {
	case "normal":
		collapse, mode = "collapse", "wrap"
	case "pre":
		collapse, mode = "preserve", "nowrap"
	case "nowrap":
		collapse, mode = "collapse", "nowrap"
	case "pre-wrap":
		collapse, mode = "preserve", "wrap"
	case "pre-line":
		collapse, mode = "preserve-breaks", "wrap"
	case "break-spaces":
		collapse, mode = "break-spaces", "wrap"
	default:
		return nil, nil, false
	}
	return map[string][]css.ComponentValue{
		"white-space-collapse": ident(collapse),
		"text-wrap-mode":       ident(mode),
	}, nil, true
}

// textWrapShorthand is "<'text-wrap-mode'> || <'text-wrap-style'>": either, or
// both in either order.
//
// Whichever is absent is reset to its initial value, which is the rule the note
// at the top of this file is about — "text-wrap: balance" after "text-wrap:
// nowrap" wraps, because the shorthand set the mode back to wrap.
func textWrapShorthand(vals []css.ComponentValue) (map[string][]css.ComponentValue, []string, bool) {
	mode, style := "", ""
	for _, part := range splitOnWhitespace(vals) {
		name, ok := singleIdent(part)
		if !ok {
			return nil, nil, false
		}
		switch name {
		case "wrap", "nowrap":
			if mode != "" {
				return nil, nil, false
			}
			mode = name
		case "auto", "balance", "stable", "pretty":
			if style != "" {
				return nil, nil, false
			}
			style = name
		default:
			return nil, nil, false
		}
	}
	if mode == "" && style == "" {
		return nil, nil, false
	}
	if mode == "" {
		mode = "wrap"
	}
	if style == "" {
		style = "auto"
	}
	return map[string][]css.ComponentValue{
		"text-wrap-mode":  ident(mode),
		"text-wrap-style": ident(style),
	}, nil, true
}

// singleIdent reads a value that is exactly one keyword, ignoring the whitespace
// either side of it.
func singleIdent(vals []css.ComponentValue) (string, bool) {
	name := ""
	for _, v := range vals {
		if v.IsToken() && v.Token.Kind == css.Whitespace {
			continue
		}
		if !v.IsToken() || v.Token.Kind != css.Ident || name != "" {
			return "", false
		}
		name = strings.ToLower(v.Token.Value)
	}
	return name, name != ""
}
