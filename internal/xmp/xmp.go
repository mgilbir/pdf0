// Package xmp is pdf0's one model of an XMP packet: the thing every reader of a
// document's metadata reads through and every writer edits.
//
// Before it existed, metadata was read two ways and written by regeneration.
// Identification values (pdfaid, pdfuaid, pdfxid, pdfvtid, fx) were scraped out
// of the packet text by substring, which a comment, whitespace around "=", an
// attribute on the element or a non-canonical prefix all defeated, and which
// compared escaped text against unescaped text. Writers built a fresh packet
// from a template, so describing a document (SetDocumentInfo) or embedding an
// invoice (EmbedFacturX) silently destroyed every property some other writer
// had put there — the Factur-X, PDF/UA, PDF/X and PDF/VT identification among
// them.
//
// The model is a node tree over the whole packet — processing instructions,
// comments, whitespace, namespace declarations and every element, known or not —
// built by encoding/xml's tokenizer with namespace resolution done here, so that
// each element keeps both the prefix it was written with and the URI that
// prefix means. Readers ask for properties by namespace URI and local name,
// never by prefix, and get values that are already XML-unescaped. Writers set or
// remove individual properties; everything they did not touch is written back
// byte for byte, because each node remembers the source bytes it was parsed from
// and is re-emitted verbatim until an edit reaches it. An unmodified packet
// round-trips to identical bytes.
//
// What goes out is guaranteed well-formed: values are checked before they are
// accepted (valid UTF-8, XML 1.0 characters only — ErrInvalidText otherwise),
// escaping is done here and nowhere else, and Bytes re-parses its own output
// before returning it.
package xmp

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Namespaces pdf0 reads or writes.
const (
	NSRDF  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	NSXML  = "http://www.w3.org/XML/1998/namespace"
	NSMeta = "adobe:ns:meta/"

	NSDC  = "http://purl.org/dc/elements/1.1/"
	NSXMP = "http://ns.adobe.com/xap/1.0/"
	NSPDF = "http://ns.adobe.com/pdf/1.3/"

	NSPDFAID  = "http://www.aiim.org/pdfa/ns/id/"
	NSPDFUAID = "http://www.aiim.org/pdfua/ns/id/"
	NSPDFXID  = "http://www.npes.org/pdfx/ns/id/"
	NSPDFVTID = "http://www.npes.org/pdfvt/ns/id/"

	NSPDFAExtension = "http://www.aiim.org/pdfa/ns/extension/"
	NSPDFASchema    = "http://www.aiim.org/pdfa/ns/schema#"
	NSPDFAProperty  = "http://www.aiim.org/pdfa/ns/property#"
	NSPDFAType      = "http://www.aiim.org/pdfa/ns/type#"
	NSPDFAField     = "http://www.aiim.org/pdfa/ns/field#"
)

// MaxDepth bounds element nesting. Real packets nest a dozen levels; the bound
// exists because the value readers and the serialiser recurse, and a hostile
// packet of nothing but open tags would otherwise take the recursion as deep as
// the packet is long. A packet deeper than this is refused with ErrLimit.
const MaxDepth = 256

// Errors. ErrLimit and ErrMalformed are distinguished because callers treat
// them differently: a malformed packet is a fact about the document, while a
// packet pdf0 declined to model is a limit of pdf0's, and a validator must say
// so rather than report either a violation or a clean result.
var (
	ErrMalformed   = errors.New("xmp: packet is not well-formed XML")
	ErrLimit       = errors.New("xmp: packet exceeds a resource limit")
	ErrNoRDF       = errors.New("xmp: packet has no rdf:RDF element")
	ErrInvalidText = errors.New("xmp: value is not valid XML text")
)

type nodeKind uint8

const (
	elemNode nodeKind = iota
	textNode
	commentNode
	piNode
	directiveNode
)

type attr struct {
	prefix, local string
	// space is the resolved namespace URI. Namespace declarations carry
	// "xmlns", as encoding/xml reports them; an unbound prefix resolves to
	// itself, again as encoding/xml does, so nothing downstream sees a
	// difference from the decoder the readers used before this model.
	space string
	value string
}

func (a attr) isNamespaceDecl() bool { return a.space == "xmlns" }

// declares reports the prefix a namespace declaration binds ("" for the
// default namespace), and whether a is one.
func (a attr) declares() (string, bool) {
	switch {
	case a.prefix == "xmlns":
		return a.local, true
	case a.prefix == "" && a.local == "xmlns":
		return "", true
	}
	return "", false
}

type node struct {
	kind   nodeKind
	parent *node

	prefix, local, space string
	attrs                []attr
	children             []*node

	// data is the unescaped text of a text node, the body of a comment or
	// directive, or the instruction of a processing instruction (whose target
	// is in local).
	data string

	// raw is the node's source bytes: for an element, from the '<' of its
	// start tag to the end of its end tag. startTag and endTag are the element's
	// own tags; endTag is empty for a self-closing element. All three are nil
	// on a node that was built rather than parsed.
	raw, startTag, endTag []byte
	rawStart              int

	// dirty says the node or a descendant changed, so it must be written out
	// piece by piece rather than as raw. attrsDirty says the start tag itself
	// must be regenerated.
	dirty, attrsDirty bool
}

func (n *node) qname() string {
	if n.prefix == "" {
		return n.local
	}
	return n.prefix + ":" + n.local
}

func (n *node) is(space, local string) bool {
	return n.kind == elemNode && n.space == space && n.local == local
}

// text is the concatenated character data directly inside an element — what
// the encoding/xml-based readers before this called the element's text.
// Comments and child elements interrupt it without ending it.
func (n *node) text() string {
	var b strings.Builder
	for _, c := range n.children {
		if c.kind == textNode {
			b.WriteString(c.data)
		}
	}
	return b.String()
}

func (n *node) attrValue(space, local string) (string, bool) {
	for _, a := range n.attrs {
		if a.space == space && a.local == local {
			return a.value, true
		}
	}
	return "", false
}

// touch marks n and every ancestor as needing re-serialisation.
func (n *node) touch() {
	for x := n; x != nil; x = x.parent {
		x.dirty = true
	}
}

// Packet is a parsed (or new) XMP packet.
type Packet struct {
	top   []*node
	src   []byte
	dirty bool
}

// Parse builds the model of an XMP packet. data is UTF-8 text (a caller holding
// raw metadata bytes converts first; core.DecodeXMPToUTF8 does). The error wraps
// ErrMalformed for XML that is not well-formed and ErrLimit for nesting deeper
// than MaxDepth.
//
// Well-formedness is judged exactly as encoding/xml's Token judges it, which is
// what every reader used before this model: tags must match and entities must
// be known, but a second top-level element or stray text outside the first is
// tolerated (and kept). Holding the readers to a stricter parser would have
// changed which packets have properties at all.
//
// Parse does not require rdf:RDF: a packet without one is well-formed XML that
// has no properties. Writers, which need somewhere to put a property, refuse
// such a packet with ErrNoRDF.
func Parse(data []byte) (*Packet, error) {
	p := &Packet{src: data}
	body := data
	if bytes.HasPrefix(body, []byte("\xEF\xBB\xBF")) {
		// A byte-order mark before the first token. encoding/xml does not skip
		// one, so it is kept aside as an opaque leading node.
		p.top = append(p.top, &node{kind: textNode, data: "\uFEFF", raw: body[:3]})
		body = body[3:]
	}

	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = true
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }

	var stack []*node
	scope := map[string][]string{} // prefix -> stack of URIs; "" is the default namespace
	var declared [][]string        // per open element, the prefixes it declared
	sawElement := false

	resolve := func(prefix string, isAttr bool) string {
		switch prefix {
		case "xml":
			return NSXML
		case "xmlns":
			return "xmlns"
		}
		if prefix == "" && isAttr {
			return ""
		}
		if s := scope[prefix]; len(s) > 0 {
			return s[len(s)-1]
		}
		return prefix // unbound: as encoding/xml leaves it
	}
	add := func(n *node) {
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			n.parent = parent
			parent.children = append(parent.children, n)
			return
		}
		p.top = append(p.top, n)
	}

	for {
		start := int(dec.InputOffset())
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		end := int(dec.InputOffset())
		raw := body[start:end:end]
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) >= MaxDepth {
				return nil, fmt.Errorf("%w: elements nested deeper than %d", ErrLimit, MaxDepth)
			}
			sawElement = true
			var mine []string
			n := &node{kind: elemNode, prefix: t.Name.Space, local: t.Name.Local, startTag: raw, rawStart: start}
			for _, a := range t.Attr {
				at := attr{prefix: a.Name.Space, local: a.Name.Local, value: a.Value}
				if pfx, ok := at.declares(); ok {
					scope[pfx] = append(scope[pfx], a.Value)
					mine = append(mine, pfx)
				}
				n.attrs = append(n.attrs, at)
			}
			for i := range n.attrs {
				a := &n.attrs[i]
				if _, ok := a.declares(); ok {
					a.space = "xmlns"
				} else {
					a.space = resolve(a.prefix, true)
				}
			}
			n.space = resolve(n.prefix, false)
			declared = append(declared, mine)
			add(n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("%w: unexpected end tag </%s>", ErrMalformed, rawName(t.Name))
			}
			n := stack[len(stack)-1]
			if t.Name.Space != n.prefix || t.Name.Local != n.local {
				return nil, fmt.Errorf("%w: element <%s> closed by </%s>", ErrMalformed, n.qname(), rawName(t.Name))
			}
			stack = stack[:len(stack)-1]
			for _, pfx := range declared[len(declared)-1] {
				scope[pfx] = scope[pfx][:len(scope[pfx])-1]
			}
			declared = declared[:len(declared)-1]
			n.endTag = raw
			n.raw = body[n.rawStart:end:end]
		case xml.CharData:
			add(&node{kind: textNode, data: string(t), raw: raw})
		case xml.Comment:
			add(&node{kind: commentNode, data: string(t), raw: raw})
		case xml.ProcInst:
			add(&node{kind: piNode, local: t.Target, data: string(t.Inst), raw: raw})
		case xml.Directive:
			add(&node{kind: directiveNode, data: string(t), raw: raw})
		}
	}
	if len(stack) > 0 {
		return nil, fmt.Errorf("%w: unexpected end of packet inside <%s>", ErrMalformed, stack[len(stack)-1].qname())
	}
	if !sawElement {
		return nil, fmt.Errorf("%w: no XML content", ErrMalformed)
	}
	return p, nil
}

func rawName(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// root is the packet's document element.
func (p *Packet) root() *node {
	for _, n := range p.top {
		if n.kind == elemNode {
			return n
		}
	}
	return nil
}

// rdf is the packet's rdf:RDF element: the root itself, or the first one found
// beneath it (normally the child of x:xmpmeta).
func (p *Packet) rdf() *node {
	r := p.root()
	if r == nil {
		return nil
	}
	var find func(n *node) *node
	find = func(n *node) *node {
		if n.is(NSRDF, "RDF") {
			return n
		}
		for _, c := range n.children {
			if c.kind == elemNode {
				if f := find(c); f != nil {
					return f
				}
			}
		}
		return nil
	}
	return find(r)
}

// HasRDF reports whether the packet contains a properly namespaced rdf:RDF
// element.
func (p *Packet) HasRDF() bool { return p.rdf() != nil }

// descriptions are the rdf:Description children of every rdf:RDF element in
// the packet, in document order.
//
// Every rdf:RDF, not only the first: producers do write a packet with two (one
// real Factur-X corpus file carries its fx properties in a second rdf:RDF under
// the same x:xmpmeta), and the substring readers this model replaced read the
// whole packet. A property is a property wherever the packet puts it. Writers
// add to the first rdf:RDF.
func (p *Packet) descriptions() []*node {
	var out []*node
	var walk func(n *node)
	walk = func(n *node) {
		if n.kind != elemNode {
			return
		}
		if n.is(NSRDF, "RDF") {
			for _, c := range n.children {
				if c.is(NSRDF, "Description") {
					out = append(out, c)
				}
			}
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, n := range p.top {
		walk(n)
	}
	return out
}
