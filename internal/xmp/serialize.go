package xmp

import (
	"bytes"
	"fmt"
	"strings"
)

// Bytes serialises the packet.
//
// A packet nobody edited comes back as the exact bytes it was parsed from.
// After an edit, every node the edit did not reach is still written from its
// source bytes; only the changed elements, and the start tags of elements that
// gained a namespace declaration, are regenerated.
//
// The output is re-parsed before it is returned, and an output that would not
// parse is an error, never a packet: the escaping here and the value checks on
// the way in are meant to make that impossible, and this is what makes the
// guarantee a checked one rather than a hope.
func (p *Packet) Bytes() ([]byte, error) {
	if !p.dirty {
		return append([]byte(nil), p.src...), nil
	}
	var b bytes.Buffer
	for _, n := range p.top {
		writeNode(&b, n)
	}
	out := b.Bytes()
	if _, err := Parse(out); err != nil {
		return nil, fmt.Errorf("xmp: the edited packet does not parse (%v); refusing to write it", err)
	}
	return out, nil
}

func writeNode(b *bytes.Buffer, n *node) {
	if n.raw != nil && !n.dirty && !n.attrsDirty {
		b.Write(n.raw)
		return
	}
	switch n.kind {
	case textNode:
		escapeText(b, n.data)
	case commentNode:
		b.WriteString("<!--")
		b.WriteString(n.data)
		b.WriteString("-->")
	case piNode:
		b.WriteString("<?")
		b.WriteString(n.local)
		if n.data != "" {
			b.WriteByte(' ')
			b.WriteString(n.data)
		}
		b.WriteString("?>")
	case directiveNode:
		b.WriteString("<!")
		b.WriteString(n.data)
		b.WriteByte('>')
	case elemNode:
		writeElement(b, n)
	}
}

func writeElement(b *bytes.Buffer, n *node) {
	parsedSelfClosing := n.startTag != nil && len(n.endTag) == 0
	if n.startTag != nil && !n.attrsDirty && !(parsedSelfClosing && len(n.children) > 0) {
		b.Write(n.startTag)
		if parsedSelfClosing {
			return
		}
	} else {
		b.WriteByte('<')
		b.WriteString(n.qname())
		for _, a := range n.attrs {
			b.WriteByte(' ')
			if a.prefix != "" {
				b.WriteString(a.prefix)
				b.WriteByte(':')
			}
			b.WriteString(a.local)
			b.WriteString(`="`)
			escapeAttr(b, a.value)
			b.WriteByte('"')
		}
		if len(n.children) == 0 {
			b.WriteString("/>")
			return
		}
		b.WriteByte('>')
	}
	for _, c := range n.children {
		writeNode(b, c)
	}
	if len(n.endTag) > 0 {
		b.Write(n.endTag)
		return
	}
	b.WriteString("</")
	b.WriteString(n.qname())
	b.WriteByte('>')
}

// escapeText writes character data. '>' is escaped as well as '<' and '&' so
// that "]]>" can never appear, and CR is written as a reference because a
// parser normalises a literal one away.
func escapeText(b *bytes.Buffer, s string) {
	if !strings.ContainsAny(s, "<>&\r") {
		b.WriteString(s)
		return
	}
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteRune(r)
		}
	}
}

// escapeAttr writes an attribute value for double quotes. Tab, LF and CR are
// written as references because attribute-value normalisation turns literal
// ones into spaces.
func escapeAttr(b *bytes.Buffer, s string) {
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&quot;")
		case '\t':
			b.WriteString("&#x9;")
		case '\n':
			b.WriteString("&#xA;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteRune(r)
		}
	}
}
