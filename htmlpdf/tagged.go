package htmlpdf

import (
	"cmp"
	"encoding/xml"
	"slices"
	"strconv"
	"strings"

	"github.com/mgilbir/forme/html"
	"github.com/mgilbir/forme/layout"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

// Tagged output: the page's structure tree, built from the document's own.
//
// Every operation forme paints says which box painted it and what of the box
// it is (layout.PaintedBy). What the document says — a run of an element's
// text, its picture, a list item's marker — is drawn inside a marked-content
// span with an identifier, and the span is given to the element it belongs to.
// How the document looks — backgrounds, borders, outlines, the lines
// text-decoration draws, shadows — is drawn as an artifact, which a reader
// that reads the page aloud skips (ISO 32000-2 14.8.2.2).
//
// The tree is the HTML element tree, each element given the structure type it
// plays (roleOf), pruned to the elements that hold something drawn. An
// element's kids are in the order the document reads them, which is not the
// order the page is painted in: floats, positioning and z-index reorder the
// painting. So every kid is placed where the document has it: a child element
// by its own place, and a run of the element's own text by the place of the
// text node it was taken from (layout.PaintedBy.Node). Only text the document
// does not hold as a node — generated content, a form control's value — is
// placed by paint order, after the child elements painted before it.

// tagger records, while a page is drawn, the marked-content span each painted
// operation was given and the element it belongs to.
type tagger struct {
	marks []mark
	// links is the element each of pageWriter.links belongs to.
	links []*html.Node
}

// markKind is what a span is to its element.
type markKind uint8

const (
	markContent markKind = iota
	markMarker           // a list item's marker: its Lbl
	markBefore           // ::before content, read first
	markAfter            // ::after content, read last
)

type mark struct {
	mcid int
	elem *html.Node
	kind markKind
	// stream is the form the span is in, nil for the page's own stream.
	stream *formStream
	// node is the text node a run of the document's text was taken from,
	// which says where among its element's children the run is; nil for
	// generated text and for what is not text.
	node *html.Node
}

// renameForms points the spans drawn in forms at the forms' copies in to,
// where AppendPages put the last page of from under new object numbers. The
// copies are found where the originals are: the same names in the same
// resource dictionaries, from the page down.
func (t *tagger) renameForms(from, to *pdf0.Document) {
	fromPages, toPages := from.PageList(), to.PageList()
	if len(fromPages) == 0 || len(toPages) == 0 {
		return
	}
	copies := map[int]object.IndirectRef{}
	var walk func(fr, tr *object.Dictionary, depth int)
	walk = func(fr, tr *object.Dictionary, depth int) {
		if fr == nil || tr == nil || depth > content.MaxNestingDepth {
			return
		}
		fx, tx := from.ResolveDict(fr.Get("XObject")), to.ResolveDict(tr.Get("XObject"))
		if fx == nil || tx == nil {
			return
		}
		for name, v := range fx.All() {
			fref, ok1 := v.(object.IndirectRef)
			tref, ok2 := tx.Get(name).(object.IndirectRef)
			if !ok1 || !ok2 {
				continue
			}
			if _, seen := copies[fref.Number]; seen {
				continue
			}
			copies[fref.Number] = tref
			fs, ok1 := from.Resolve(fref).(*object.Stream)
			ts, ok2 := to.Resolve(tref).(*object.Stream)
			if ok1 && ok2 {
				walk(from.ResolveDict(fs.Dict.Get("Resources")), to.ResolveDict(ts.Dict.Get("Resources")), depth+1)
			}
		}
	}
	walk(from.ResolveDict(fromPages[len(fromPages)-1].Get("Resources")), to.ResolveDict(toPages[len(toPages)-1].Get("Resources")), 0)
	// Each form once: its spans share it, and a form renamed already holds a
	// number of to's, which means nothing in copies.
	renamed := map[*formStream]bool{}
	for i := range t.marks {
		st := t.marks[i].stream
		if st == nil || renamed[st] {
			continue
		}
		renamed[st] = true
		if c, ok := copies[st.ref.Number]; ok {
			st.ref = c
		}
	}
}

// kid is the span as its element's kid: on the page, or in the form it was
// drawn in.
func (m *mark) kid() pdf0.StructKid {
	if m.stream != nil {
		return pdf0.FormMCIDKid(m.stream.ref, m.mcid)
	}
	return pdf0.MCIDKid(m.mcid)
}

// formStream is a form XObject a tagged page draws its content into: a filter
// group. Its spans are numbered in it, and named by the form, whose reference
// exists once the form is written (canvas.group).
type formStream struct {
	ref object.IndirectRef
}

// tagged draws op as tagged content or as an artifact, by what forme says it
// is. A group — a clip, a transform, a filter — is not wrapped: each operation
// inside is, as it is drawn, in the page's stream or in the form a filter
// draws into, which numbers its spans itself.
func (c *canvas) tagged(op layout.Op) error {
	t := c.w.tags
	switch v := op.(type) {
	case layout.ClipPath, layout.TransformGroup, layout.FilterGroup:
		return c.paint(op)
	case layout.Link:
		n := len(c.w.links)
		err := c.paint(op)
		a := linkElement(v.PaintedBy.Element())
		for i := n; i < len(c.w.links); i++ {
			c.w.links[i].Contents = linkDescription(a, v.Href)
			t.links = append(t.links, a)
		}
		return err
	}
	elem, kind, content := classify(op)
	node := layout.OpPaintedBy(op).Node()
	if !content {
		c.b.BeginMarked("Artifact")
		err := c.paint(op)
		c.b.EndMarked()
		return err
	}
	mcid := c.nextMCID
	c.nextMCID++
	t.marks = append(t.marks, mark{mcid: mcid, elem: elem, kind: kind, node: node, stream: c.stream})
	tag := roleOf(elem).tag
	if tag == "" {
		tag = "Span"
	}
	c.b.BeginTagged(object.Name(tag), mcid)
	err := c.paint(op)
	c.b.EndMarked()
	return err
}

// classify says whose content op is, and what of it, or that it is an
// artifact (content false).
func classify(op layout.Op) (elem *html.Node, kind markKind, content bool) {
	pb := layout.OpPaintedBy(op)
	elem = atomic(pb.Element())
	if elem == nil || artifactElement(elem) {
		return nil, 0, false
	}
	switch pb.Part {
	case layout.PartContent:
		if g, ok := op.(layout.DrawGlyphs); ok && g.Text == "" {
			return nil, 0, false // glyphs that stand for no text: a formula's shadow
		}
		return elem, pseudoOf(pb), true
	case layout.PartMarker:
		return elem, markMarker, true
	}
	return nil, 0, false
}

// atomic is the element that stands for n in the tree: n, or the picture or
// formula it is part of. What draws an SVG or a MathML formula is the
// picture's or the formula's, which is one Figure or Formula with its
// alternate text, not a tree of shapes and glyphs.
func atomic(n *html.Node) *html.Node {
	for cur := n; cur != nil && cur.Type == html.ElementNode; cur = cur.Parent {
		if cur.Name == "svg" || cur.Name == "math" {
			n = cur
		}
	}
	return n
}

// pseudoOf says whether the box that painted a run is ::before or ::after
// content of its element. The walk stops at the first box an element made,
// which is the pseudo-element's own box if the run is in one.
func pseudoOf(pb layout.PaintedBy) markKind {
	for b := pb.Box; b != nil; b = b.Parent {
		switch b.Pseudo {
		case "before":
			return markBefore
		case "after":
			return markAfter
		}
		if b.Element != nil {
			break
		}
	}
	return markContent
}

// artifactElement is an element the document says is not content: hidden
// from assistive technology (aria-hidden="true" on it or an ancestor), or a
// picture marked decorative (an empty alt, or role="presentation" or "none").
func artifactElement(n *html.Node) bool {
	for cur := n; cur != nil && cur.Type == html.ElementNode; cur = cur.Parent {
		if v, _ := cur.Attr("aria-hidden"); strings.EqualFold(strings.TrimSpace(v), "true") {
			return true
		}
	}
	switch n.Name {
	case "img", "svg":
		if v, ok := n.Attr("alt"); ok && strings.TrimSpace(v) == "" && n.Name == "img" {
			return true
		}
		if r, _ := n.Attr("role"); r == "presentation" || r == "none" {
			return true
		}
	}
	return false
}

// linkElement is the <a> a link belongs to: the element itself, or the
// nearest one around it, which is where forme's attribution of a link's area
// can land when the area is a child's.
func linkElement(n *html.Node) *html.Node {
	for cur := n; cur != nil && cur.Type == html.ElementNode; cur = cur.Parent {
		if cur.Name == "a" && cur.HasAttr("href") {
			return cur
		}
	}
	return n
}

// linkDescription is what a link's annotation says it is, for a reader that
// cannot see what it covers (/Contents, ISO 14289-1 7.18.5): the <a>'s
// aria-label, its title, its text, or failing all three its address.
func linkDescription(a *html.Node, href string) string {
	if a != nil {
		for _, attr := range []string{"aria-label", "title"} {
			if v, _ := a.Attr(attr); strings.TrimSpace(v) != "" {
				return collapse(v)
			}
		}
		if s := collapse(a.TextContent()); s != "" {
			return s
		}
	}
	return href
}

// collapse is s with runs of white space made one space, trimmed.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// role is the structure type an element plays, and what it says of itself
// that the type alone does not.
type role struct {
	tag string
	// transparent elements are not in the tree; their kids are their
	// parent's.
	transparent bool
}

// roleOf is the structure type of an HTML element (ISO 32000-1 14.8.4, the
// PDF 1.7 standard types, which a PDF 2.0 reader reads too).
func roleOf(n *html.Node) role {
	if n == nil {
		return role{tag: "Span"}
	}
	switch n.Name {
	case "html":
		return role{transparent: true}
	case "body":
		return role{tag: "Document"}
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return role{tag: strings.ToUpper(n.Name)}
	case "p", "pre", "dt", "dd", "figcaption", "legend", "summary":
		return role{tag: "P"}
	case "ul", "ol", "menu":
		return role{tag: "L"}
	case "li":
		return role{tag: "LI"}
	case "table":
		return role{tag: "Table"}
	case "thead":
		return role{tag: "THead"}
	case "tbody":
		return role{tag: "TBody"}
	case "tfoot":
		return role{tag: "TFoot"}
	case "tr":
		return role{tag: "TR"}
	case "th":
		return role{tag: "TH"}
	case "td":
		return role{tag: "TD"}
	case "caption":
		return role{tag: "Caption"}
	case "img", "svg", "canvas", "video", "picture":
		return role{tag: "Figure"}
	case "math":
		return role{tag: "Formula"}
	case "a":
		if n.HasAttr("href") {
			return role{tag: "Link"}
		}
		return role{tag: "Span"}
	case "blockquote":
		return role{tag: "BlockQuote"}
	case "q":
		return role{tag: "Quote"}
	case "code", "kbd", "samp", "var":
		return role{tag: "Code"}
	case "section":
		return role{tag: "Sect"}
	case "article":
		return role{tag: "Art"}
	case "span", "b", "strong", "i", "em", "u", "s", "small", "sub", "sup", "mark", "cite",
		"dfn", "abbr", "time", "data", "bdi", "bdo", "ins", "del", "label", "output",
		"button", "input", "select", "textarea", "font", "big", "tt", "strike", "ruby", "rt", "rp":
		return role{tag: "Span"}
	}
	return role{tag: "Div"}
}

// kid is one thing an element holds, before it is a pdf0.StructKid: a span of
// its own, a child element, or an annotation.
type kid struct {
	key   float64 // the order the element reads it in
	seq   int     // paint order, which breaks ties
	mark  *mark
	child *html.Node
	annot int // index into the page's annotations, when mark and child are nil
}

// tree is the structure tree of a tagged page: page is the page as the
// document holding it names it, and annots its annotations, the links in the
// order they were drawn. The tree's root is the Document the <body> plays.
func (t *tagger) tree(page object.IndirectRef, annots []object.IndirectRef) []pdf0.StructElem {
	if len(t.marks) == 0 && len(t.links) == 0 {
		return nil
	}
	b := treeBuilder{
		kids:  map[*html.Node][]kid{},
		order: map[*html.Node]int{},
	}
	// Document order, from the root of whatever the marks hang from.
	var root *html.Node
	for _, m := range t.marks {
		root = topOf(m.elem)
		break
	}
	for _, a := range t.links {
		if root == nil && a != nil {
			root = topOf(a)
		}
	}
	i := 0
	root.Walk(func(n *html.Node) bool {
		b.order[n] = i
		i++
		return true
	})

	for seq, m := range t.marks {
		b.add(m.elem, kid{mark: &t.marks[seq], seq: seq})
	}
	for i, a := range t.links {
		if a != nil && i < len(annots) {
			b.add(a, kid{annot: i, seq: len(t.marks) + i})
		}
	}
	// A table is its whole grid, empty cells included: a reader that walks
	// its rows and columns needs every cell where it is.
	var tables []*html.Node
	for n := range b.kids {
		if n.Name == "table" {
			tables = append(tables, n)
		}
	}
	for _, n := range tables {
		n.Walk(func(c *html.Node) bool {
			switch c.Name {
			case "thead", "tbody", "tfoot", "tr", "th", "td":
				b.add(c, kid{seq: -1, annot: -1})
			}
			return true
		})
	}
	b.headerIDs = headerIDs(b.kids)
	elems := b.elems(root, page, annots, "")
	return elems
}

// topOf is the root of the element tree a node is in: its document node.
func topOf(n *html.Node) *html.Node {
	for n != nil && n.Parent != nil {
		n = n.Parent
	}
	return n
}

type treeBuilder struct {
	// kids is, for each element in the tree, what it holds, unordered until
	// elems sorts it.
	kids map[*html.Node][]kid
	// order is each node's place in the document.
	order map[*html.Node]int
	// headerIDs are the IDs of the header cells a data cell names in its
	// headers attribute, which the tree gives those cells.
	headerIDs map[string]bool
}

// add gives an element something it holds, and makes it, and each element
// around it, a kid of the one around it. A kid with neither a mark, a child
// nor an annotation (annot -1) only puts the element in the tree.
func (b *treeBuilder) add(n *html.Node, k kid) {
	if n == nil {
		return
	}
	if k.mark != nil || k.annot >= 0 || k.child != nil {
		b.kids[n] = append(b.kids[n], k)
	} else if _, ok := b.kids[n]; !ok {
		b.kids[n] = nil
	}
	for child, parent := n, n.Parent; parent != nil; child, parent = parent, parent.Parent {
		if slices.ContainsFunc(b.kids[parent], func(x kid) bool { return x.child == child }) {
			return
		}
		b.kids[parent] = append(b.kids[parent], kid{child: child, seq: k.seq, annot: -1})
	}
}

// sorted is an element's kids in reading order. A child element, and a span
// of the element's own text, is placed where the document has it: the
// element, and the text node the span was taken from. A span the document
// holds no node for is placed after the child elements painted before it,
// which in the flow of a line is where the document has it too; ::before and
// a marker come first, ::after last, and an annotation after the spans it
// covers.
func (b *treeBuilder) sorted(n *html.Node) []kid {
	ks := slices.Clone(b.kids[n])
	slices.SortStableFunc(ks, func(x, y kid) int { return cmp.Compare(x.seq, y.seq) })
	last := -1.0
	for i := range ks {
		k := &ks[i]
		switch {
		case k.child != nil:
			k.key = float64(b.order[k.child])
			last = max(last, k.key)
		case k.mark != nil && (k.mark.kind == markBefore || k.mark.kind == markMarker):
			k.key = -1
		case k.mark != nil && k.mark.kind == markAfter:
			k.key = 1 << 40
		case k.mark != nil && k.mark.node != nil:
			// A run of the document's text is where its text node is.
			k.key = float64(b.order[k.mark.node])
		case k.mark != nil:
			k.key = last + 0.5
		default:
			k.key = 1<<40 + 1 // an annotation, after what the link draws
		}
	}
	slices.SortStableFunc(ks, func(x, y kid) int { return cmp.Compare(x.key, y.key) })
	return ks
}

// elems is the structure elements n stands for: one, or for a transparent
// element its kids'. lang is the language of the element around it.
func (b *treeBuilder) elems(n *html.Node, page object.IndirectRef, annots []object.IndirectRef, lang string) []pdf0.StructElem {
	r := roleOf(n)
	if n.Type != html.ElementNode {
		r = role{transparent: true}
	}
	own, _ := n.Language()
	var kids []structKid
	var marker []pdf0.StructKid
	var children []pdf0.StructElem // for a transparent element, what stands in for it
	var ownMarks []pdf0.StructKid
	for _, k := range b.sorted(n) {
		switch {
		case k.child != nil:
			for _, e := range b.elems(k.child, page, annots, own) {
				kids = append(kids, structKid{kid: pdf0.ElemKid(e), inline: inlineTags[e.Tag], tag: e.Tag})
				children = append(children, e)
			}
		case k.mark != nil && k.mark.kind == markMarker:
			marker = append(marker, k.mark.kid())
		case k.mark != nil:
			kids = append(kids, structKid{kid: k.mark.kid(), inline: true, text: true})
			ownMarks = append(ownMarks, k.mark.kid())
		case k.annot >= 0:
			kids = append(kids, structKid{kid: pdf0.AnnotKid(annots[k.annot]), inline: true})
		}
	}
	if groupingTags[r.tag] {
		kids = paragraphs(kids)
	}
	if r.transparent {
		// What the element draws itself, which no element of the tree
		// holds otherwise, is kept in a paragraph of its own: a span no
		// element owns is content a reader cannot reach.
		if len(ownMarks) > 0 {
			children = append([]pdf0.StructElem{{Tag: "P", Kids: ownMarks}}, children...)
		}
		return children
	}
	e := pdf0.StructElem{Tag: r.tag}
	if r.tag == "Document" {
		e.Page = &page
	}
	if own != "" && own != lang {
		e.Lang = own
	}
	out := make([]pdf0.StructKid, len(kids))
	for i, k := range kids {
		out[i] = k.kid
	}
	switch r.tag {
	case "LI":
		// A list item is its label, the marker, and its body (ISO 32000-1
		// 14.8.4.3.3).
		var lk []pdf0.StructKid
		if len(marker) > 0 {
			lk = append(lk, pdf0.ElemKid(pdf0.StructElem{Tag: "Lbl", Kids: marker}))
		}
		if len(out) > 0 {
			lk = append(lk, pdf0.ElemKid(pdf0.StructElem{Tag: "LBody", Kids: out}))
		}
		out = lk
	case "L":
		e.Attributes = listNumbering(n)
	case "Figure", "Formula":
		e.Alt = altText(n)
	case "TH", "TD":
		e.Attributes = cellAttributes(n, b.headerIDs)
		if id, _ := n.Attr("id"); r.tag == "TH" && b.headerIDs[id] {
			e.ID = id
		}
	}
	if len(marker) > 0 && r.tag != "LI" {
		out = append(marker, out...)
	}
	e.Kids = out
	return []pdf0.StructElem{e}
}

// structKid is a kid on its way into the tree, with what paragraphs needs to
// know of it.
type structKid struct {
	kid pdf0.StructKid
	// inline is a span of text, an annotation, or an element that sits in a
	// line of text rather than making a block of its own.
	inline bool
	// text is a span of marked content.
	text bool
	// tag is an element's structure type, "" for anything else.
	tag string
}

// inlineTags are the structure types that sit in a line of text.
var inlineTags = map[string]bool{
	"Span": true, "Link": true, "Code": true, "Quote": true, "Figure": true, "Formula": true,
}

// groupingTags are the structure types that group blocks and hold no text of
// their own: ISO 32005 Table 5 has a Document, Art, Sect and Div hold no
// content and no Span, and a BlockQuote no Span. HTML puts text straight
// into a <div> or a <body> all the time, as a browser lays it out: as an
// anonymous block, a paragraph of its own (CSS 2 9.2.1.1).
var groupingTags = map[string]bool{
	"Document": true, "Art": true, "Sect": true, "Div": true, "BlockQuote": true,
}

// paragraphs puts each run of inline kids that holds text, or a Span or a
// link, in a P of its own, as the anonymous block a browser lays it out in.
// A run of nothing but pictures is left as it is: a Figure stands in a
// grouping element by itself.
func paragraphs(kids []structKid) []structKid {
	var out []structKid
	for i := 0; i < len(kids); {
		if !kids[i].inline {
			out = append(out, kids[i])
			i++
			continue
		}
		j := i
		wrap := false
		for j < len(kids) && kids[j].inline {
			if kids[j].text || (kids[j].tag != "" && kids[j].tag != "Figure" && kids[j].tag != "Formula") {
				wrap = true
			}
			j++
		}
		if !wrap {
			out = append(out, kids[i:j]...)
			i = j
			continue
		}
		p := pdf0.StructElem{Tag: "P"}
		for _, k := range kids[i:j] {
			p.Kids = append(p.Kids, k.kid)
		}
		out = append(out, structKid{kid: pdf0.ElemKid(p)})
		i = j
	}
	return out
}

// altText is what a picture or a formula says, for a reader that cannot see
// it: alt, aria-label, a formula's alttext, an SVG's <title>, or a formula's
// own text.
func altText(n *html.Node) string {
	for _, attr := range []string{"alt", "aria-label", "alttext"} {
		if v, _ := n.Attr(attr); strings.TrimSpace(v) != "" {
			return collapse(v)
		}
	}
	if n.Name == "svg" {
		if s := svgTitle(n.Foreign); s != "" {
			return s
		}
	}
	if n.Name == "math" {
		return collapse(n.TextContent())
	}
	return ""
}

// svgTitle is the text of an SVG's own <title>, the one that is a child of
// the <svg> and so titles the whole picture (SVG 2 5.9). forme keeps an
// inline SVG's children as their source (html.Node.Foreign), which is read
// here only as far as that element.
func svgTitle(foreign string) string {
	d := xml.NewDecoder(strings.NewReader("<svg>" + foreign + "</svg>"))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	depth := 0
	for {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		switch v := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 && v.Name.Local == "title" {
				var text strings.Builder
				for {
					tok, err := d.Token()
					if err != nil {
						return ""
					}
					switch c := tok.(type) {
					case xml.CharData:
						text.Write(c)
					case xml.EndElement:
						return collapse(text.String())
					}
				}
			}
		case xml.EndElement:
			depth--
		}
	}
}

// listNumbering is a list's ListNumbering attribute: Decimal and the rest for
// an <ol> by its type, Disc for a <ul> (ISO 32000-1 Table 347).
func listNumbering(n *html.Node) []pdf0.StructAttr {
	v := "Disc"
	if n.Name == "ol" {
		v = "Decimal"
		switch t, _ := n.Attr("type"); t {
		case "a":
			v = "LowerAlpha"
		case "A":
			v = "UpperAlpha"
		case "i":
			v = "LowerRoman"
		case "I":
			v = "UpperRoman"
		}
	}
	return []pdf0.StructAttr{{Owner: "List", Key: "ListNumbering", Value: object.Name(v)}}
}

// cellAttributes are a table cell's Table attributes: a header cell's Scope,
// a data cell's Headers, and a span of more than one row or column.
func cellAttributes(n *html.Node, headerIDs map[string]bool) []pdf0.StructAttr {
	var attrs []pdf0.StructAttr
	if n.Name == "th" {
		attrs = append(attrs, pdf0.StructAttr{Owner: "Table", Key: "Scope", Value: object.Name(headerScope(n))})
	}
	if h, ok := n.Attr("headers"); ok {
		var ids object.Array
		for _, id := range strings.Fields(h) {
			if headerIDs[id] {
				ids = append(ids, object.String{Value: []byte(id)})
			}
		}
		if len(ids) > 0 {
			attrs = append(attrs, pdf0.StructAttr{Owner: "Table", Key: "Headers", Value: ids})
		}
	}
	for _, s := range []struct{ html, pdf string }{{"rowspan", "RowSpan"}, {"colspan", "ColSpan"}} {
		if v, ok := n.Attr(s.html); ok {
			if k, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && k > 1 {
				attrs = append(attrs, pdf0.StructAttr{Owner: "Table", Key: s.pdf, Value: object.Integer(k)})
			}
		}
	}
	return attrs
}

// headerScope is a header cell's Scope: its scope attribute, or else Column
// for one in a <thead> or in a row of nothing but header cells, and Row for
// one that starts a row of data.
func headerScope(n *html.Node) string {
	switch v, _ := n.Attr("scope"); strings.ToLower(strings.TrimSpace(v)) {
	case "row", "rowgroup":
		return "Row"
	case "col", "colgroup":
		return "Column"
	}
	row := n.Parent
	if row == nil {
		return "Column"
	}
	if row.Parent != nil && row.Parent.Name == "thead" {
		return "Column"
	}
	for _, c := range row.Children {
		if c.Type == html.ElementNode && c.Name == "td" {
			return "Row"
		}
	}
	return "Column"
}

// headerIDs are the ids of the header cells in the tree that some data cell's
// headers attribute names, each given once in the document: what the tree can
// name by ID.
func headerIDs(kids map[*html.Node][]kid) map[string]bool {
	count := map[string]int{}
	isHeader := map[string]bool{}
	named := map[string]bool{}
	for n := range kids {
		if id, ok := n.Attr("id"); ok && id != "" {
			count[id]++
			if n.Name == "th" {
				isHeader[id] = true
			}
		}
		if h, ok := n.Attr("headers"); ok {
			for _, id := range strings.Fields(h) {
				named[id] = true
			}
		}
	}
	ids := map[string]bool{}
	for id := range named {
		if isHeader[id] && count[id] == 1 {
			ids[id] = true
		}
	}
	return ids
}
