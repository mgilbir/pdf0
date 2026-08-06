package style

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
)

// The property registry, and the values a property can hold.
//
// # Why a registry at all
//
// Two questions have to be answerable for every property before a cascade can
// run, and neither can be worked out from a declaration: does this property
// inherit, and what is its value when nothing sets it. "color" inherits and
// "margin-top" does not, and no amount of looking at "color: red" reveals which.
//
// A third question is answerable only here and is the reason this file matters
// more than its size suggests: *is this property one the engine implements*. A
// renderer for a subset will meet declarations it does not act on, and §6.3 of
// the rendering proposal argues — correctly, and this is the cheapest guardrail
// it names — that dropping them silently is the worst available option. A page
// where "flex-wrap" was ignored is plausible and wrong, which is harder to
// notice than one that is obviously broken. So every declaration that is parsed
// and not applied is recorded.

// A property this engine knows about.
type property struct {
	// inherits says whether the computed value passes to children.
	inherits bool
	// initial is the value used when nothing in the cascade sets one, written
	// as it would be in a stylesheet.
	initial string
}

// properties is what the engine implements.
//
// A declaration whose name is not here is reported as unsupported and dropped —
// never quietly kept, and never quietly discarded. The set grows with the layout
// engine: a property is added here when something downstream acts on it, not
// when it is recognised, because a property that is stored and never read is
// indistinguishable to an author from one that was ignored.
var properties = map[string]property{
	// Box model.
	"display":        {false, "inline"},
	"width":          {false, "auto"},
	"height":         {false, "auto"},
	"min-width":      {false, "0"},
	"min-height":     {false, "0"},
	"max-width":      {false, "none"},
	"max-height":     {false, "none"},
	"margin-top":     {false, "0"},
	"margin-right":   {false, "0"},
	"margin-bottom":  {false, "0"},
	"margin-left":    {false, "0"},
	"padding-top":    {false, "0"},
	"padding-right":  {false, "0"},
	"padding-bottom": {false, "0"},
	"padding-left":   {false, "0"},
	"box-sizing":     {false, "content-box"},

	// Out-of-flow positioning by the one mechanism CSS had before there was a
	// positioning scheme. Neither inherits: a float that inherited would make
	// every descendant of a floated sidebar float too, which is the opposite of
	// what an author writing "float: left" on one box means.
	"float": {false, "none"},
	"clear": {false, "none"},

	// The positioning schemes of CSS 2.1 §9.3. None of them inherits, and
	// "position" is the one worth saying why about: a relative position that
	// reached every descendant would offset the subtree once per level, so a
	// paragraph three elements deep inside a box nudged 10px down would land
	// 30px down. The offset belongs to the box the author wrote it on.
	//
	// The initial value of the four offsets is "auto" rather than "0", and the
	// difference between those two is the whole of §10.3.7. A box with "left:
	// auto" is placed where the flow would have put it; one with "left: 0" is
	// pinned to its containing block's left padding edge. Reading auto as zero
	// would send every absolutely positioned box that names only a "top" to the
	// left edge of its containing block, which is a plausible-looking page and
	// the wrong one.
	"position": {false, "static"},
	"top":      {false, "auto"},
	"right":    {false, "auto"},
	"bottom":   {false, "auto"},
	"left":     {false, "auto"},

	// z-index's initial value is "auto" and not "0" for a reason of the same
	// shape: "0" makes the box a stacking context and "auto" leaves it in its
	// parent's, so a descendant with a negative z-index paints behind an
	// ancestor with "z-index: auto" and in front of one with "z-index: 0".
	// Collapsing the two would make that descendant unreachable.
	"z-index": {false, "auto"},

	// Borders.
	"border-top-width":    {false, "medium"},
	"border-right-width":  {false, "medium"},
	"border-bottom-width": {false, "medium"},
	"border-left-width":   {false, "medium"},
	"border-top-style":    {false, "none"},
	"border-right-style":  {false, "none"},
	"border-bottom-style": {false, "none"},
	"border-left-style":   {false, "none"},
	"border-top-color":    {false, "currentcolor"},
	"border-right-color":  {false, "currentcolor"},
	"border-bottom-color": {false, "currentcolor"},
	"border-left-color":   {false, "currentcolor"},

	// Text and fonts. Most of these inherit, which is the whole reason
	// inheritance exists: setting a font on <body> has to reach the text.
	"color":                 {true, "black"},
	"font-family":           {true, "serif"},
	"font-size":             {true, "medium"},
	"font-style":            {true, "normal"},
	"font-weight":           {true, "normal"},
	"line-height":           {true, "normal"},
	"letter-spacing":        {true, "normal"},
	"word-spacing":          {true, "normal"},
	"text-align":            {true, "start"},
	"text-indent":           {true, "0"},
	"text-transform":        {true, "none"},
	"white-space":           {true, "normal"},
	"text-decoration-line":  {false, "none"},
	"text-decoration-color": {false, "currentcolor"},
	"vertical-align":        {false, "baseline"},
	"direction":             {true, "ltr"},
	"unicode-bidi":          {false, "normal"},

	// Generated content. It does not inherit — a ::before on a parent must not
	// give every descendant the same marker.
	"content": {false, "normal"},

	// Backgrounds.
	"background-color": {false, "transparent"},

	// Lists.
	"list-style-type":     {true, "disc"},
	"list-style-position": {true, "outside"},

	// Tables.
	"border-collapse": {true, "separate"},
	"border-spacing":  {true, "0"},
	"caption-side":    {true, "top"},
	"empty-cells":     {true, "show"},
	"table-layout":    {false, "auto"},

	// Visibility and overflow.
	"visibility": {true, "visible"},
	"overflow-x": {false, "visible"},
	"overflow-y": {false, "visible"},
	"opacity":    {false, "1"},
}

// Inherited returns the style an anonymous box has: everything that inherits
// taken from the box it was generated inside, and everything that does not at
// its initial value.
//
// This is what the specification means by an anonymous box having no style of
// its own. It matters far more than it sounds, because the obvious shortcut —
// giving the anonymous box its parent's whole computed style — makes it a copy
// of the parent's *box model* as well: the anonymous block wrapped around a run
// of text inside <body> would take body's 8px margin, indent the text by it, and
// separate it from the block after it by a gap the author never wrote. Every
// number in that document is then plausible and wrong.
func Inherited(cs ComputedStyle) ComputedStyle {
	out := make(ComputedStyle, len(properties))
	for name, prop := range properties {
		if prop.inherits {
			if v, ok := cs[name]; ok {
				out[name] = v
				continue
			}
		}
		out[name] = prop.initial
	}
	return out
}

// shorthands expands a shorthand into the longhands it sets.
//
// Expansion happens before the cascade rather than after, and that ordering is
// not arbitrary: "margin: 0" followed by "margin-top: 1em" must leave the top
// margin at 1em, which only works if the shorthand has already become four
// declarations competing individually. Cascading the shorthand as a unit would
// make the later longhand lose to it or win over all four.
// expander turns a shorthand's value into the longhands it sets.
//
// It returns three things rather than two, and the third is the point:
// unsupported names the parts of the value this engine understood and cannot
// produce — a background image, a font variant. Dropping those silently is the
// failure §6.3 is written about, and only the expander knows what it saw.
type expander func(vals []css.ComponentValue) (longhands map[string][]css.ComponentValue, unsupported []string, ok bool)

// shorthand is an expander together with the longhands it controls.
//
// The list is declared rather than discovered. An earlier version asked the
// expander itself, by handing it a value and seeing which keys came back — which
// worked only while every expander accepted the same probe value, and stopped
// the moment one of them started rejecting values it could not identify. The
// list is needed for the CSS-wide keywords ("border: inherit" sets all twelve
// longhands to inherit), where there is no value to probe with at all.
type shorthand struct {
	expand    expander
	longhands []string
}

var shorthands = map[string]shorthand{
	"margin":  boxShorthand("margin-top", "margin-right", "margin-bottom", "margin-left"),
	"padding": boxShorthand("padding-top", "padding-right", "padding-bottom", "padding-left"),
	"border-width": boxShorthand("border-top-width", "border-right-width",
		"border-bottom-width", "border-left-width"),
	"border-style": boxShorthand("border-top-style", "border-right-style",
		"border-bottom-style", "border-left-style"),
	"border-color": boxShorthand("border-top-color", "border-right-color",
		"border-bottom-color", "border-left-color"),
	"overflow": boxShorthand("overflow-x", "overflow-y"),

	// The shorthands whose parts are told apart by type rather than position.
	// They live in shorthand.go, with the reset rule explained there.
	"border":        borderSides("top", "right", "bottom", "left"),
	"border-top":    borderSides("top"),
	"border-right":  borderSides("right"),
	"border-bottom": borderSides("bottom"),
	"border-left":   borderSides("left"),

	"background": {backgroundShorthand, []string{"background-color"}},
	"list-style": {listStyleShorthand, []string{"list-style-type", "list-style-position"}},
	"font": {fontShorthand, []string{
		"font-style", "font-weight", "font-size", "font-family", "line-height"}},
	"text-decoration": {textDecorationShorthand,
		[]string{"text-decoration-line", "text-decoration-color"}},
}

// boxShorthand builds the expander for a property written as one to four values
// in the order top, right, bottom, left — where one value sets all four, two set
// the vertical and horizontal pairs, and three leave the left to mirror the
// right.
//
// The two-name form is the same rule with two slots, which is what "overflow"
// needs.
// borderSides builds the "border" family, whose longhands are three per side.
func borderSides(sides ...string) shorthand {
	var names []string
	for _, side := range sides {
		names = append(names,
			"border-"+side+"-width", "border-"+side+"-style", "border-"+side+"-color")
	}
	return shorthand{borderShorthand(sides...), names}
}

func boxShorthand(names ...string) shorthand {
	return shorthand{boxExpander(names...), names}
}

func boxExpander(names ...string) expander {
	return func(vals []css.ComponentValue) (map[string][]css.ComponentValue, []string, bool) {
		parts := splitOnWhitespace(vals)
		if len(parts) == 0 || len(parts) > len(names) {
			return nil, nil, false
		}
		out := make(map[string][]css.ComponentValue, len(names))
		switch len(names) {
		case 2:
			out[names[0]] = parts[0]
			out[names[1]] = parts[len(parts)-1]
		default:
			top := parts[0]
			right := top
			if len(parts) > 1 {
				right = parts[1]
			}
			bottom := top
			if len(parts) > 2 {
				bottom = parts[2]
			}
			left := right
			if len(parts) > 3 {
				left = parts[3]
			}
			out[names[0]], out[names[1]] = top, right
			out[names[2]], out[names[3]] = bottom, left
		}
		return out, nil, true
	}
}

// splitOnWhitespace divides a value into its space-separated parts.
func splitOnWhitespace(vals []css.ComponentValue) [][]css.ComponentValue {
	var out [][]css.ComponentValue
	var cur []css.ComponentValue
	for _, v := range vals {
		if v.IsToken() && v.Token.Kind == css.Whitespace {
			if len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, v)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// The four CSS-wide keywords, which every property accepts and which mean the
// same thing for all of them. They are handled by the cascade rather than by any
// property's own parsing.
const (
	kwInherit = "inherit"
	kwInitial = "initial"
	kwUnset   = "unset"
	kwRevert  = "revert"
)

// wideKeyword returns the CSS-wide keyword a value consists of, or "".
//
// It has to be the *whole* value: "border: 1px solid initial" is not a use of
// the keyword, it is a declaration with a stray word in it.
func wideKeyword(vals []css.ComponentValue) string {
	parts := splitOnWhitespace(vals)
	if len(parts) != 1 || len(parts[0]) != 1 {
		return ""
	}
	v := parts[0][0]
	if !v.IsToken() || v.Token.Kind != css.Ident {
		return ""
	}
	switch kw := strings.ToLower(v.Token.Value); kw {
	case kwInherit, kwInitial, kwUnset, kwRevert:
		return kw
	}
	return ""
}

// serialize renders component values back to text.
//
// The cascade stores winning values as text rather than as component values, for
// one reason worth stating: a computed value has to be *comparable*, and two
// slices of component values that mean the same thing are not equal in Go. The
// styled tree is compared in tests, cached on, and eventually diffed against a
// reference, and all three want a value that can be a map key.
func serialize(vals []css.ComponentValue) string {
	var b strings.Builder
	writeValues(&b, vals)
	return strings.TrimSpace(b.String())
}

func writeValues(b *strings.Builder, vals []css.ComponentValue) {
	for _, v := range vals {
		writeValue(b, v)
	}
}

func writeValue(b *strings.Builder, v css.ComponentValue) {
	t := v.Token
	switch {
	case v.IsFunction():
		b.WriteString(t.Value)
		b.WriteByte('(')
		writeValues(b, v.Values)
		b.WriteByte(')')
		return
	case v.IsBlock():
		open, close := "(", ")"
		switch t.Kind {
		case css.LeftSquare:
			open, close = "[", "]"
		case css.LeftBrace:
			open, close = "{", "}"
		}
		b.WriteString(open)
		writeValues(b, v.Values)
		b.WriteString(close)
		return
	}

	switch t.Kind {
	case css.Ident, css.Delim:
		b.WriteString(t.Value)
	case css.AtKeyword:
		b.WriteString("@" + t.Value)
	case css.Hash:
		b.WriteString("#" + t.Value)
	case css.String:
		// Quoted, because a string that serialised bare would be
		// indistinguishable from an identifier — and "none" the keyword is not
		// "none" the font family.
		b.WriteString(`"` + strings.ReplaceAll(t.Value, `"`, `\"`) + `"`)
	case css.URL:
		b.WriteString("url(" + t.Value + ")")
	case css.Number:
		b.WriteString(t.Repr)
	case css.Percentage:
		b.WriteString(t.Repr + "%")
	case css.Dimension:
		b.WriteString(t.Repr + t.Unit)
	case css.Whitespace:
		b.WriteByte(' ')
	case css.Colon:
		b.WriteByte(':')
	case css.Semicolon:
		b.WriteByte(';')
	case css.Comma:
		b.WriteByte(',')
	case css.BadString, css.BadURL:
		// A value that did not tokenize cannot be rendered back; anything
		// written here would be a value the author did not type.
		b.WriteString("<invalid>")
	}
}
