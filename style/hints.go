package style

import (
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
)

// Presentational hints: the handful of HTML attributes that mean a CSS
// declaration.
//
// "<img width=5 height=96>" is not markup this engine may ignore. It is the
// oldest way to size an image and it is still what half the web platform's own
// reference documents use to draw a rectangle of a known size — so an engine
// that read the attribute as decoration would lay those documents out at the
// image's own pixel size and be wrong in a way that looks like a layout bug.
//
// # Where they sit in the cascade, and why it matters
//
// A hint is *not* an inline style. "img { width: 10px }" beats
// "<img width=5>", and the ordering is what makes a stylesheet able to take
// control of a document it did not write. CSS Cascade puts a hint in the author
// origin with a specificity of zero, ahead of every declaration an author
// actually wrote — which is what is done here: origin OriginAuthor, zero
// specificity, and an order number below every real declaration's.
//
// The consequences are worth stating, because both directions surprise someone:
// a user-agent rule can never beat a hint, and any author rule at all can,
// including "* { width: auto }".
//
// # Why so few
//
// Only the attributes whose element this engine lays out, and only the ones
// that are a length. The rest of HTML's presentational attributes — align,
// bgcolor, border, cellpadding — belong to elements that are refused or to a
// table algorithm that does not exist yet, and a table of hints for boxes that
// are never built would read as coverage.

// hintOrder is the cascade order number every hint carries.
//
// Declarations from stylesheets are numbered from zero upwards, so any negative
// number is below all of them — which is exactly where a hint belongs, and
// stating it as a constant is what keeps that relationship from being an
// accident of two files agreeing.
const hintOrder = -1

// hintedAttributes lists, per element, which attribute sets which property.
//
// Keyed by lower-case element name. The map exists rather than a run of
// conditionals so that a new hint is a line of data rather than a new branch —
// which is how <table width> arrived, once there was a table algorithm for it
// to mean anything to.
var hintedAttributes = map[string]map[string]string{
	"img": {"width": "width", "height": "height"},
	// The HTML Standard's table rendering section maps the width attribute to
	// the width property as a "dimension property", which is the same syntax
	// <img width> takes and so the same reader below: a bare number is pixels
	// and a trailing per-cent sign is a percentage of the containing block.
	//
	// The height attribute is beside it because the standard does describe it,
	// in the same list and the same words — "maps to the dimension property
	// ... on the table element" — and an earlier note here that said otherwise
	// was wrong. The suite settles it without needing the prose: the reference
	// for floats-wrap-bfc-005 draws with a plain "height: 20px" div what the
	// test writes as <table height="20">, so a browser that ignored the
	// attribute would fail its own reftest.
	"table": {"cellspacing": "border-spacing", "width": "width", "height": "height"},
	// <ol start="5"> and <li value="3"> are the counter, written as attributes.
	// They take a signed integer rather than a dimension, so they are read by
	// integerAttr below instead of the table's usual dimensionValue.
	"ol": {"start": "counter-reset"},
	"li": {"value": "counter-reset"},
}

// counterHintAttributes are the entries above whose value is a plain integer
// naming a counter, rather than a length.
var counterHintAttributes = map[string]bool{"start": true, "value": true}

// There is deliberately no cache of parsed hint values.
//
// One was written here first, on the reasoning that a document of a thousand
// thumbnails asks for "150px" a thousand times — and it was a package-level
// map, keyed on text taken straight out of an untrusted document, which is a
// leak that outlives the render that filled it. The parse it saved is two
// tokens, asked at most twice per element, so what it bought was nothing and
// what it cost was unbounded memory across a process's lifetime.

// presentationalHints returns the declarations an element's attributes imply.
//
// The value syntax is HTML's "dimension value": a run of digits, optionally
// followed by a per-cent sign. Anything else — a negative number, a length with
// a unit, a word — is not a dimension and the attribute is ignored, which is
// what HTML requires and is also the safe answer: a value this cannot read must
// not become a length it guessed at.
func presentationalHints(n *html.Node) map[string][]css.ComponentValue {
	name := strings.ToLower(n.Name)
	if name == "td" || name == "th" {
		return cellPaddingHint(n)
	}
	attrs, ok := hintedAttributes[name]
	if !ok {
		return nil
	}
	var out map[string][]css.ComponentValue
	for attr, property := range attrs {
		raw, ok := n.Attr(attr)
		if !ok {
			continue
		}
		var value string
		if counterHintAttributes[attr] {
			// "start" and "value" set the counter to one *below* the number
			// they name, because the item increments it on the way in. That is
			// what makes <li value="3"> show a 3 rather than a 4.
			value, ok = counterResetValue(raw)
		} else {
			value, ok = dimensionValue(raw)
		}
		if !ok {
			continue
		}
		if out == nil {
			out = make(map[string][]css.ComponentValue, len(attrs))
		}
		vals, _ := css.ParseComponentValues(value)
		out[property] = vals
	}
	return out
}

// maxHintDigits bounds the number a dimension attribute may state.
//
// Ten digits cannot overflow the parse below and is already four orders of
// magnitude past any page; the bound is here because the attribute is untrusted
// text and a length is one multiplication away from a box the size of a
// continent. A longer run of digits is not a large image, it is a value nobody
// meant, so it is refused rather than saturated.
const maxHintDigits = 10

// dimensionValue turns an HTML dimension attribute into a CSS length.
//
// Leading and trailing white space is allowed, because HTML's attribute values
// are commonly written with it and every browser strips it. Everything else is
// exact: the digits, then optionally a per-cent sign, then the end.
func dimensionValue(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits > maxHintDigits {
		return "", false
	}
	switch s[digits:] {
	case "":
		// A bare number is a length in CSS pixels.
		return s + "px", true
	case "%":
		return s, true
	}
	return "", false
}

// cellPaddingHint reads the cellpadding an ancestor table declares.
//
// It is the one hint that is not an attribute of the element it styles:
// cellpadding is written once on the table and applies to every cell in it. The
// walk stops at the first table, which is what makes a nested table's cells take
// their own table's padding rather than the one they happen to sit inside.
//
// A percentage is refused here even though the dimension syntax allows one,
// because a percentage padding resolves against the containing block's width and
// that is not what an author writing cellpadding="10%" is asking for. A value
// this cannot read leaves the stylesheet's answer standing, which is the same as
// the attribute not being there.
func cellPaddingHint(n *html.Node) map[string][]css.ComponentValue {
	for anc := n.Parent; anc != nil; anc = anc.Parent {
		if anc.Type != html.ElementNode || !strings.EqualFold(anc.Name, "table") {
			continue
		}
		raw, ok := anc.Attr("cellpadding")
		if !ok {
			return nil
		}
		value, ok := dimensionValue(raw)
		if !ok || strings.HasSuffix(value, "%") {
			return nil
		}
		vals, _ := css.ParseComponentValues(value)
		return map[string][]css.ComponentValue{
			"padding-top": vals, "padding-right": vals,
			"padding-bottom": vals, "padding-left": vals,
		}
	}
	return nil
}

// counterResetValue turns a start or value attribute into a counter-reset.
//
// The attribute names the number the item is to show. The list-item counter is
// incremented as the item is entered, so the reset has to be one less — and
// "one less" is why this is not simply the integer: a value of the most negative
// integer would wrap, and an attribute is untrusted text.
func counterResetValue(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	neg := false
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if s == "" || len(s) > maxHintDigits {
		return "", false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return "", false
		}
		n = n*10 + int(s[i]-'0')
	}
	if neg {
		n = -n
	}
	return "list-item " + strconv.Itoa(n-1), true
}
