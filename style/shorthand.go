package style

import (
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
// Only the colour is produced, because the colour is the only part this engine
// paints. The rest — image, repeat, attachment, position, size, origin, clip —
// is named back to the caller rather than dropped, so an author who wrote
// "background: url(paper.png)" is told the image did not appear instead of
// wondering why the page is blank.
//
// The reset still happens in full: "background: url(x)" sets the colour to
// transparent, because the shorthand controls it and did not mention it.
func backgroundShorthand(vals []css.ComponentValue) (map[string][]css.ComponentValue, []string, bool) {
	colour := ident("transparent")
	var seenColour bool
	var unsupported []string

	for _, part := range splitOnWhitespace(vals) {
		switch {
		case isColour(part) && !seenColour:
			colour, seenColour = part, true
		case isNone(part):
			// "background: none" is the image being absent, which is the
			// initial value and needs nothing said about it.
		default:
			unsupported = append(unsupported, serialize(part))
		}
	}
	return map[string][]css.ComponentValue{"background-color": colour}, unsupported, true
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

// textDecorationShorthand expands "text-decoration": a line and a colour.
func textDecorationShorthand(vals []css.ComponentValue) (map[string][]css.ComponentValue, []string, bool) {
	line, colour := ident("none"), ident("currentcolor")
	var seenLine, seenColour bool
	var unsupported []string

	for _, part := range splitOnWhitespace(vals) {
		switch {
		case isDecorationLine(part) && !seenLine:
			line, seenLine = part, true
		case isColour(part) && !seenColour:
			colour, seenColour = part, true
		case isIdentPart(part):
			unsupported = append(unsupported, serialize(part))
		default:
			return nil, nil, false
		}
	}
	return map[string][]css.ComponentValue{
		"text-decoration-line":  line,
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
