package html

import (
	"strconv"
	"strings"
)

// The tree builder.
//
// HTML5's has twenty-three insertion modes, most of them describing how to
// rebuild a tree from tags that arrived in an impossible order. This one has
// none: tags that cannot nest are refused (see the package comment), and the
// only reordering it does is the one HTML actually defines — the optional end
// tags in elements.go, which are correct markup rather than recovery.

// The bounds on one document. A template is untrusted input, and each of these
// is a place where a few kilobytes of markup would otherwise cost unboundedly
// more.
const (
	// maxDepth bounds how deeply elements may nest. Each level is a stack frame
	// in anything that walks the tree recursively — layout does — and real
	// documents nest tens of levels, not hundreds.
	maxDepth = 256

	// maxNodes bounds the size of the tree. It is the cap that matters for
	// memory: a node is far larger than the markup that creates it, so "<b>"
	// repeated is an amplification.
	maxNodes = 1 << 20

	// maxInputBytes bounds the document itself. It is generous — a book's worth
	// of markup is a few megabytes — and exists so that the caps above are
	// never the first thing a runaway input meets.
	maxInputBytes = 64 << 20
)

// Parse reads a document.
//
// It returns the tree it built, everything it refused, and whether the document
// was read without refusing anything that changes what the page says. A caller
// that renders a document with ok false is rendering something other than what
// its author wrote, so the flag is not advisory.
//
// The tree is returned even when ok is false, because a caller showing an author
// what went wrong is better served by both than by either — which is the
// opposite of the selector parser's arrangement, and for the opposite reason:
// there, a partial result is silently applicable and so dangerous to hand back;
// here, the findings name what is missing and the tree cannot be mistaken for
// complete.
func Parse(src string) (doc *Node, errs []Error, ok bool) {
	if len(src) > maxInputBytes {
		return nil, []Error{{
			Offset: maxInputBytes,
			Message: "the document is larger than this engine will read (" +
				strconv.Itoa(len(src)) + " bytes, limit " + strconv.Itoa(maxInputBytes) + ")",
		}}, false
	}

	p := &parser{tok: newTokenizer(src), src: src}
	p.run()
	return p.doc, p.tok.errs, len(p.tok.errs) == 0
}

type parser struct {
	tok *tokenizer
	src string

	doc  *Node
	html *Node
	head *Node
	body *Node

	// open is the stack of elements not yet closed, innermost last.
	open []*Node

	nodes int
	// bodyStarted records that something has been put in the body, after which
	// head-only elements are out of place.
	bodyStarted bool
	// truncated records that a bound stopped the tree being built, so the
	// caller is never handed a short document that looks complete.
	truncated bool
}

func (p *parser) run() {
	p.doc = &Node{Type: DocumentNode}
	p.html = p.element("html", 0)
	p.doc.appendChild(p.html)
	p.head = p.element("head", 0)
	p.html.appendChild(p.head)
	p.body = p.element("body", 0)
	p.html.appendChild(p.body)
	p.open = []*Node{p.html, p.head}

	for {
		tk := p.tok.next()
		switch tk.kind {
		case tokEOF:
			p.finish()
			return
		case tokDoctype:
			// Nothing to do with it: there is one document model here, and it
			// is not chosen by a doctype.
		case tokText:
			p.text(tk)
		case tokStartTag:
			p.startTag(tk)
		case tokEndTag:
			p.endTag(tk)
		}
		if p.truncated {
			p.finish()
			return
		}
	}
}

func (p *parser) element(name string, offset int) *Node {
	p.nodes++
	return &Node{Type: ElementNode, Name: name, Offset: offset}
}

// current is the element things are being put into.
func (p *parser) current() *Node {
	if len(p.open) == 0 {
		return p.body
	}
	return p.open[len(p.open)-1]
}

func (p *parser) text(tk token) {
	if tk.text == "" {
		return
	}
	parent := p.current()

	// Text only decides where the body begins when it is *loose* — directly
	// inside the head or the html element rather than inside something open.
	// Inside an element it simply belongs to that element, and this distinction
	// is the whole of it: a <title> or a <style> lives in the head and has text
	// in it, and treating that text as the start of the body would close the
	// element out from under its own end tag.
	if parent == p.head || parent == p.html {
		// Whitespace between elements belongs to neither head nor body;
		// dropping it keeps "</head>\n<p>" from putting a stray text node at
		// the front of the document.
		if strings.TrimSpace(tk.text) == "" {
			return
		}
		p.enterBody()
		parent = p.current()
	}
	if !p.room(tk.offset) {
		return
	}
	// Adjacent runs are merged, so no element ever has two text children in a
	// row — a shape every consumer would otherwise have to handle.
	if n := len(parent.Children); n > 0 && parent.Children[n-1].Type == TextNode {
		parent.Children[n-1].Text += tk.text
		return
	}
	p.nodes++
	parent.appendChild(&Node{Type: TextNode, Text: tk.text, Offset: tk.offset})
}

// room reports whether another node may be added, recording the trip if not.
func (p *parser) room(off int) bool {
	if p.nodes >= maxNodes {
		if !p.truncated {
			p.tok.fail(off, "the document has more elements than this engine will build ("+
				strconv.Itoa(maxNodes)+"); the rest was not read")
			p.truncated = true
		}
		return false
	}
	return true
}

func (p *parser) startTag(tk token) {
	name := tk.name

	// The three frame elements always exist already, because run() built them
	// before reading anything. A start tag for one of them is therefore not an
	// element to create — it is the author naming a box that is already there,
	// and the only thing it carries that the frame does not is its attributes.
	//
	// Creating a second one instead is a fault that hides well: <html> and
	// <body> would nest inside the frame, every document with explicit tags
	// would apply body's margin twice, and a test asking only whether an <html>
	// element *exists* would pass — which is exactly how this survived until a
	// layout comparison noticed the doubled margin.
	switch name {
	case "html":
		mergeAttributes(p.html, tk.attrs)
		return
	case "head":
		mergeAttributes(p.head, tk.attrs)
		return
	case "body":
		mergeAttributes(p.body, tk.attrs)
		p.enterBody()
		return
	}

	if why, dropped := droppedElements[name]; dropped {
		p.tok.unsupported(tk.offset, "<"+name+"> is dropped: "+why)
		// Its content goes with it. For the raw-text ones that means consuming
		// to the end tag, or the script body would be read as markup.
		if rawTextElements[name] && !tk.selfClosing {
			p.skipRaw(name, tk.offset)
		} else if !voidElements[name] && !tk.selfClosing {
			p.skipElement(name)
		}
		return
	}

	if !knownElements[name] {
		p.tok.unsupported(tk.offset, "<"+name+"> is not an element this engine lays out")
		return
	}

	if tk.selfClosing && !voidElements[name] {
		// "<div/>" is not an empty div. HTML has no self-closing syntax for
		// ordinary elements, so a browser reads this as an open <div> and every
		// following element ends up inside it — a shape that looks like a
		// typo's worth of markup and moves half the page.
		p.tok.fail(tk.offset, "<"+name+"/> is not an empty element; HTML has no "+
			"self-closing syntax outside void elements, so a browser reads this as <"+name+">")
		return
	}

	// The optional end tags of HTML: an incoming start tag can close what is
	// open.
	p.closeImplied(name)

	if headElements[name] && !p.bodyStarted {
		p.appendTo(p.head, tk)
		return
	}
	if !metadataElements[name] && !p.bodyStarted {
		p.enterBody()
	}

	el := p.insert(tk)
	if el == nil {
		return
	}
	if voidElements[name] {
		return
	}
	if rawTextElements[name] {
		p.tok.raw, p.tok.rcdata = name, false
	} else if rcdataElements[name] {
		p.tok.raw, p.tok.rcdata = name, true
	}
	p.open = append(p.open, el)

	if len(p.open) > maxDepth {
		p.tok.fail(tk.offset, "elements are nested more deeply than this engine will read ("+
			strconv.Itoa(maxDepth)+")")
		p.truncated = true
	}
}

// mergeAttributes copies the attributes a frame element's start tag carried onto
// the element the parser had already made.
//
// An attribute already present wins over the one arriving, which is the rule
// HTML gives: the first value of a repeated attribute is the one that counts, and
// the frame's own is the first by construction.
func mergeAttributes(el *Node, attrs []Attribute) {
	if el == nil {
		return
	}
	for _, a := range attrs {
		if el.HasAttr(a.Name) {
			continue
		}
		el.Attrs = append(el.Attrs, a)
	}
}

// appendTo puts an element in a named parent rather than the current one, which
// is what the head elements need.
func (p *parser) appendTo(parent *Node, tk token) {
	if !p.room(tk.offset) {
		return
	}
	el := p.element(tk.name, tk.offset)
	el.Attrs = tk.attrs
	parent.appendChild(el)
	if rawTextElements[tk.name] {
		p.tok.raw, p.tok.rcdata = tk.name, false
	} else if rcdataElements[tk.name] {
		p.tok.raw, p.tok.rcdata = tk.name, true
	}
	if !voidElements[tk.name] {
		p.open = append(p.open, el)
	}
}

func (p *parser) insert(tk token) *Node {
	if !p.room(tk.offset) {
		return nil
	}
	el := p.element(tk.name, tk.offset)
	el.Attrs = tk.attrs
	p.current().appendChild(el)
	return el
}

// enterBody moves from the head to the body.
func (p *parser) enterBody() {
	if p.bodyStarted {
		return
	}
	p.bodyStarted = true
	// Everything the head had open is closed by the move.
	for len(p.open) > 0 && p.open[len(p.open)-1] != p.html {
		p.open = p.open[:len(p.open)-1]
	}
	p.open = append(p.open, p.body)
}

// closeImplied pops the elements that an incoming start tag ends.
func (p *parser) closeImplied(incoming string) {
	for len(p.open) > 0 {
		top := p.open[len(p.open)-1]
		closers, ok := closedByStartTag[top.Name]
		if !ok || !closers[incoming] {
			return
		}
		p.open = p.open[:len(p.open)-1]
	}
}

func (p *parser) endTag(tk token) {
	name := tk.name

	if _, dropped := droppedElements[name]; dropped {
		// Its start tag was reported and its content skipped, so a matching end
		// tag here is the tail of something already dealt with.
		return
	}
	if voidElements[name] {
		p.tok.fail(tk.offset, "</"+name+"> is an end tag for a void element, which has none")
		return
	}
	if !knownElements[name] {
		// Its start tag was already reported; saying so twice is noise.
		return
	}

	// Find it on the stack. Anything above it may only be there if HTML lets it
	// end without a tag of its own.
	at := -1
	for i := len(p.open) - 1; i >= 0; i-- {
		if p.open[i].Name == name {
			at = i
			break
		}
	}
	if at < 0 {
		p.tok.fail(tk.offset, "</"+name+"> closes nothing: no <"+name+"> is open here")
		return
	}
	for i := len(p.open) - 1; i > at; i-- {
		if !closedByParentEnd[p.open[i].Name] {
			p.tok.fail(tk.offset, "</"+name+"> would close <"+p.open[i].Name+
				">, which is still open; tags have to nest")
			return
		}
	}
	p.open = p.open[:at]
}

// skipElement consumes to the matching end tag of an element being dropped,
// counting nesting so an inner <iframe> does not end the outer one.
func (p *parser) skipElement(name string) {
	depth := 1
	for {
		tk := p.tok.next()
		switch tk.kind {
		case tokEOF:
			return
		case tokStartTag:
			if tk.name == name && !tk.selfClosing && !voidElements[name] {
				depth++
			}
		case tokEndTag:
			if tk.name == name {
				if depth--; depth == 0 {
					return
				}
			}
		}
	}
}

// skipRaw consumes the body of a dropped raw-text element, whose content is not
// markup and must not be tokenized as any.
func (p *parser) skipRaw(name string, off int) {
	end := p.tok.findEndTag(name, p.tok.pos)
	if end < 0 {
		p.tok.pos = len(p.tok.src)
		return
	}
	p.tok.pos = end
	// Consume the end tag itself.
	if tk := p.tok.next(); tk.kind != tokEndTag {
		p.tok.fail(off, "<"+name+"> is never closed")
	}
}

// finish reports the elements still open at the end of the document.
func (p *parser) finish() {
	for i := len(p.open) - 1; i >= 0; i-- {
		el := p.open[i]
		if el == p.html || el == p.head || el == p.body {
			continue
		}
		if closedByParentEnd[el.Name] {
			continue
		}
		p.tok.fail(el.Offset, "<"+el.Name+"> is never closed")
	}
	p.open = nil
}
