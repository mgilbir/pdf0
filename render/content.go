package render

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
	"github.com/mgilbir/pdf0/style"
)

// Generated content: the boxes ::before and ::after produce.
//
// These are the only boxes in the tree that no element generated and no
// anonymous-box rule required. They come from a declaration — "content" — which
// makes them the one place where the *stylesheet* adds to the document rather
// than describing it.
//
// A pseudo-element with no content generates nothing at all, which is not the
// same as generating an empty box: an empty inline would still contribute a line
// box, and a list of items each preceded by an invisible one would be spaced as
// though something were there.

// contentValue is what a "content" declaration resolves to.
type contentValue struct {
	text string
	// none reports that the declaration asks for no box at all, which "none"
	// and "normal" both do.
	none bool
	// unsupported reports a value that is correct CSS this engine cannot
	// produce — a counter, an image, a quote.
	unsupported string
}

// maxContentLength bounds the text one pseudo-element's content may produce.
//
// Every other source of text in a document is at most as long as the document:
// a literal string is its own length, an attr() is the attribute's. The quote
// keywords are the first that are not, and the amplification is worth writing
// down rather than trusting to good taste. "quotes" holds two strings of the
// author's choosing and "content: open-quote open-quote …" draws one of them per
// keyword, so eleven bytes of declaration fetch a megabyte of mark — a factor of
// a hundred thousand, from a stylesheet, before any element has been matched.
//
// A megabyte of generated content is already past anything a page can show. The
// refusal is reported rather than truncated, because a marker cut off in the
// middle is a page that looks finished and is not.
const maxContentLength = 1 << 20

// resolveContent reads a "content" declaration.
//
// Strings, attr(), counters and quotes are produced. Images are refused and
// named, because they would otherwise be silently dropped and leave a marker
// missing from a page that still looks finished.
//
// depth is the level of quotation the value begins at and quotes the pairs to
// draw from; both come from the document walk, because neither is anything the
// pseudo-element can answer about itself. See quotes.go.
func resolveContent(raw string, el *html.Node, counters counterValues,
	quotes quoteList, depth int) contentValue {

	trimmed := strings.TrimSpace(raw)
	switch strings.ToLower(trimmed) {
	case "", "normal", "none":
		return contentValue{none: true}
	}

	vals, _ := css.ParseComponentValues(trimmed)
	var text strings.Builder
	for _, v := range vals {
		if text.Len() > maxContentLength {
			return contentValue{unsupported: "the content is longer than this engine will generate"}
		}
		switch {
		case v.IsToken() && v.Token.Kind == css.Whitespace:
			// Whitespace separates the parts of the value and contributes
			// nothing of its own.

		case v.IsToken() && v.Token.Kind == css.String:
			text.WriteString(v.Token.Value)

		case v.IsFunction() && strings.EqualFold(v.Token.Value, "attr"):
			name := attrArgument(v)
			if name == "" {
				return contentValue{unsupported: "attr() without an attribute name"}
			}
			// A missing attribute contributes the empty string, which is what
			// the specification says and is why attr() is safe to use for
			// optional data.
			value, _ := el.Attr(name)
			text.WriteString(value)

		case v.IsFunction() && strings.EqualFold(v.Token.Value, "counter"):
			name, listStyle, _, ok := counterArguments(v)
			if !ok {
				return contentValue{unsupported: "counter() without a counter name"}
			}
			// The innermost counter of that name. A counter that was never
			// created reads as zero, which is what §12.4.3 says and is better
			// than refusing: a document referring to a counter it forgot to
			// reset still numbers from its increments.
			vals := counters[name]
			n := 0
			if len(vals) > 0 {
				n = vals[len(vals)-1]
			}
			text.WriteString(formatCounter(n, listStyle))

		case v.IsFunction() && strings.EqualFold(v.Token.Value, "counters"):
			name, listStyle, sep, ok := counterArguments(v)
			if !ok || sep == nil {
				return contentValue{unsupported: "counters() needs a name and a separator"}
			}
			// Every counter of that name in scope, outermost first. This is what
			// numbers a nested list "2.1.3" — the three values are three
			// counters alive at once, one per level.
			var parts []string
			for _, n := range counters[name] {
				parts = append(parts, formatCounter(n, listStyle))
			}
			text.WriteString(strings.Join(parts, *sep))

		case v.IsToken() && v.Token.Kind == css.URL,
			v.IsFunction() && strings.EqualFold(v.Token.Value, "url"):
			return contentValue{unsupported: "an image in generated content needs a resolver"}

		case v.IsToken() && v.Token.Kind == css.Ident:
			// §12.3.1's four keywords. The depth they move is the document's, not
			// this value's, which is why it arrived as an argument — but within one
			// value they still run in order, so "open-quote close-quote" draws a
			// matched pair and ends where it began.
			if op, isQuote := quoteKeyword(v.Token.Value); isQuote {
				var mark string
				mark, depth = applyQuote(op, depth, quotes)
				text.WriteString(mark)
				continue
			}
			return contentValue{unsupported: "\"" + v.Token.Value + "\" is not content this engine can produce"}

		default:
			return contentValue{unsupported: "a value this engine cannot produce"}
		}
	}
	return contentValue{text: text.String()}
}

// attrArgument reads the attribute name out of attr(name).
func attrArgument(fn css.ComponentValue) string {
	for _, v := range fn.Values {
		if v.IsToken() && v.Token.Kind == css.Ident {
			return v.Token.Value
		}
	}
	return ""
}

// generated builds the box a pseudo-element produces, or nil.
func (b *boxBuilder) generated(n *html.Node, name string, fontSize style.Unit) *Box {
	key := style.PseudoKey{Node: n, Name: name}
	cs, ok := b.pseudo[key]
	if !ok {
		return nil
	}
	outer, inner, listItem := displayOf(cs)
	if outer == OuterNone {
		return nil
	}

	value := resolveContent(cs["content"], n, b.counters.pseudo[key],
		parseQuotes(cs["quotes"]), b.counters.quoteDepth[key])
	if value.unsupported != "" {
		b.rec.ReportDetail(Finding{
			Rule:     RuleUnsupportedValue,
			Source:   AtHTML(n.Offset),
			Message:  "the content of ::" + name + " was not produced: " + value.unsupported,
			Path:     PathOf(n),
			Property: "content",
		})
		return nil
	}
	if value.none {
		return nil
	}
	if !b.room(n) {
		return nil
	}
	order := b.count

	size := b.fontSizeOfStyle(cs, fontSize, b.ownPseudoFontSize[key])
	// A pseudo-element floats like any other box, and "p::before { content: '';
	// float: left }" is how a stylesheet makes a drop cap or a decorative rule
	// without adding an element to the markup — so the §9.7 blockification has
	// to reach here too.
	float := floatOf(cs)
	// A pseudo-element is positioned like any other box too, and the everyday
	// use of that is the same one: "::before { content: ''; position: absolute }"
	// is how a stylesheet draws an overlay without an element to hang it on.
	position := positionOf(cs)
	if position.outOfFlow() {
		float = FloatNone
	}
	outer, inner = outOfFlowDisplay(outer, inner, float, position)
	z, zAuto := zIndexOf(cs)
	box := &Box{
		Outer: outer, Inner: inner, Element: n, Style: cs,
		ListItem: listItem, FontSize: size,
		Float: float, Clear: clearOf(cs),
		Position: position, ZIndex: z, ZAuto: zAuto, Order: order,
	}
	// The text is not collapsed the way document text is: a content string is
	// written by the author as the exact characters wanted, and "content: '  '"
	// asking for two spaces is a legitimate way to indent a marker.
	if value.text != "" {
		if !b.room(n) {
			return box
		}
		text := &Box{
			Outer: OuterInline, Inner: InnerText,
			Style: cs, Text: value.text, FontSize: size, Parent: box,
		}
		box.Children = append(box.Children, text)
	}
	return box
}

// counterArguments reads the name, the optional list style and, for counters(),
// the separator out of a counter function.
//
// The two functions differ in their second argument: counter(name, style) takes
// a style there, counters(name, sep, style) takes the separator. Telling them
// apart by *type* rather than by position is what makes one reader serve both —
// a string is a separator and an identifier is a style, and neither can be
// mistaken for the other.
func counterArguments(fn css.ComponentValue) (name, listStyle string, sep *string, ok bool) {
	for _, v := range fn.Values {
		if !v.IsToken() {
			continue
		}
		switch v.Token.Kind {
		case css.Whitespace, css.Comma:
		case css.Ident:
			if name == "" {
				name = v.Token.Value
				continue
			}
			listStyle = v.Token.Value
		case css.String:
			s := v.Token.Value
			sep = &s
		}
	}
	return name, listStyle, sep, name != ""
}
