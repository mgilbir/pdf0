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

// resolveContent reads a "content" declaration.
//
// Strings and attr() are produced. Counters, quotes and images are refused and
// named, because each of them would otherwise be silently dropped and leave a
// marker missing from a page that still looks finished.
func resolveContent(raw string, el *html.Node) contentValue {
	trimmed := strings.TrimSpace(raw)
	switch strings.ToLower(trimmed) {
	case "", "normal", "none":
		return contentValue{none: true}
	}

	vals, _ := css.ParseComponentValues(trimmed)
	var text strings.Builder
	for _, v := range vals {
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

		case v.IsFunction() && strings.EqualFold(v.Token.Value, "counter"),
			v.IsFunction() && strings.EqualFold(v.Token.Value, "counters"):
			return contentValue{unsupported: "counters are not implemented"}

		case v.IsToken() && v.Token.Kind == css.URL,
			v.IsFunction() && strings.EqualFold(v.Token.Value, "url"):
			return contentValue{unsupported: "an image in generated content needs a resolver"}

		case v.IsToken() && v.Token.Kind == css.Ident:
			switch strings.ToLower(v.Token.Value) {
			case "open-quote", "close-quote", "no-open-quote", "no-close-quote":
				return contentValue{unsupported: "quotes are not implemented"}
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
	cs, ok := b.pseudo[style.PseudoKey{Node: n, Name: name}]
	if !ok {
		return nil
	}
	outer, inner, listItem := displayOf(cs)
	if outer == OuterNone {
		return nil
	}

	value := resolveContent(cs["content"], n)
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

	size := b.fontSizeOfStyle(cs, fontSize)
	box := &Box{
		Outer: outer, Inner: inner, Element: n, Style: cs,
		ListItem: listItem, FontSize: size,
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
