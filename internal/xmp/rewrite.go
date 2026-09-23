package xmp

import (
	"bytes"
	"fmt"
)

// Rewrite serialises the packet from the model alone, ignoring the source
// bytes every node remembers. Bytes never does this for an untouched node; it
// exists so the writer can be proved on real packets — a rewritten packet must
// parse to a tree Equivalent to the original — rather than only on the nodes
// edits happen to reach.
func (p *Packet) Rewrite() ([]byte, error) {
	var b bytes.Buffer
	for _, n := range p.top {
		rewriteNode(&b, n)
	}
	out := b.Bytes()
	if _, err := Parse(out); err != nil {
		return nil, fmt.Errorf("xmp: the rewritten packet does not parse: %v", err)
	}
	return out, nil
}

func rewriteNode(b *bytes.Buffer, n *node) {
	if n.kind != elemNode {
		if n.kind == textNode && n.parent == nil && n.data == string(rune(0xFEFF)) {
			// The leading byte-order mark Parse set aside: it is not
			// character data, and must stay a BOM.
			b.WriteString(n.data)
			return
		}
		c := *n
		c.raw = nil
		writeNode(b, &c)
		return
	}
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
	for _, c := range n.children {
		rewriteNode(b, c)
	}
	b.WriteString("</")
	b.WriteString(n.qname())
	b.WriteByte('>')
}

// Equivalent reports whether two packets have the same tree: the same
// elements with the same prefixes, namespaces and attributes in the same
// order, the same comments and processing instructions, and the same text —
// adjacent text nodes compared as one, since how character data was split
// into sections (a CDATA block, an entity) is spelling, not content. It
// returns nil when they do, and otherwise describes the first difference.
func Equivalent(a, b *Packet) error {
	return equivalentNodes(a.top, b.top, "")
}

func mergedText(nodes []*node) []*node {
	var out []*node
	for _, n := range nodes {
		if n.kind == textNode && len(out) > 0 && out[len(out)-1].kind == textNode {
			m := *out[len(out)-1]
			m.data += n.data
			out[len(out)-1] = &m
			continue
		}
		out = append(out, n)
	}
	return out
}

func equivalentNodes(an, bn []*node, path string) error {
	an, bn = mergedText(an), mergedText(bn)
	if len(an) != len(bn) {
		return fmt.Errorf("%s: %d children, %d children", path, len(an), len(bn))
	}
	for i := range an {
		x, y := an[i], bn[i]
		here := fmt.Sprintf("%s/%d", path, i)
		if x.kind != y.kind || x.local != y.local || x.data != y.data {
			return fmt.Errorf("%s: node %q %q differs from %q %q", here, x.local, x.data, y.local, y.data)
		}
		if x.kind != elemNode {
			continue
		}
		here = path + "/" + x.qname()
		if x.prefix != y.prefix || x.space != y.space {
			return fmt.Errorf("%s: element {%s}%s differs from {%s}%s", here, x.space, x.qname(), y.space, y.qname())
		}
		if len(x.attrs) != len(y.attrs) {
			return fmt.Errorf("%s: %d attributes, %d attributes", here, len(x.attrs), len(y.attrs))
		}
		for j := range x.attrs {
			if x.attrs[j] != y.attrs[j] {
				return fmt.Errorf("%s: attribute %+v differs from %+v", here, x.attrs[j], y.attrs[j])
			}
		}
		if err := equivalentNodes(x.children, y.children, here); err != nil {
			return err
		}
	}
	return nil
}
