package xmp

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// packetID is the fixed identifier of an XMP packet wrapper (ISO 16684-1 7.3.2),
// the string a scanner recognises a packet by in a file it is not parsing.
const packetID = "W5M0MpCehiHzreSzNTczkc9d"

// New returns an empty packet: the xpacket wrapper, x:xmpmeta and an empty
// rdf:RDF, ready for properties.
func New() *Packet {
	// The wrapper's begin attribute is a byte-order mark, written as a rune so
	// no source file has to carry one.
	bom := string(rune(0xFEFF))
	src := `<?xpacket begin="` + bom + `" id="` + packetID + `"?>` + "\n" +
		`<x:xmpmeta xmlns:x="adobe:ns:meta/">` + "\n" +
		`  <rdf:RDF xmlns:rdf="` + NSRDF + `">` + "\n" +
		`  </rdf:RDF>` + "\n" +
		`</x:xmpmeta>` + "\n" +
		`<?xpacket end="w"?>`
	p, err := Parse([]byte(src))
	if err != nil {
		panic("xmp: the empty packet template does not parse: " + err.Error())
	}
	p.dirty = true
	return p
}

// Modified reports whether any edit has been made since the packet was parsed.
func (p *Packet) Modified() bool { return p.dirty }

// CheckText reports whether s can be written as XMP text: valid UTF-8 made only
// of characters XML 1.0 admits (production [2] Char). Anything else — a
// stray byte, a C0 control, U+FFFE — cannot be put in a packet at all, not even
// as a character reference, so it is refused rather than dropped or replaced:
// a value silently changed on the way in is a value the caller did not write.
func CheckText(s string) error {
	for i, r := range s {
		if r == utf8.RuneError {
			if _, size := utf8.DecodeRuneInString(s[i:]); size <= 1 {
				return fmt.Errorf("%w: invalid UTF-8 at byte %d", ErrInvalidText, i)
			}
		}
		if !isXMLChar(r) {
			return fmt.Errorf("%w: character %U at byte %d is not allowed in XML", ErrInvalidText, r, i)
		}
	}
	return nil
}

func isXMLChar(r rune) bool {
	switch {
	case r == 0x9 || r == 0xA || r == 0xD:
		return true
	case r >= 0x20 && r <= 0xD7FF:
		return true
	case r >= 0xE000 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0x10FFFF:
		return true
	}
	return false
}

// checkName reports whether s is an XML NCName pdf0 is prepared to write. The
// names come from pdf0's own constants; the check is there so that a mistake
// in one is an error rather than a packet that does not parse.
func checkName(s string) error {
	if s == "" {
		return fmt.Errorf("xmp: empty name")
	}
	for i, r := range s {
		ok := r == '_' || unicode.IsLetter(r) || (i > 0 && (r == '-' || r == '.' || unicode.IsDigit(r)))
		if !ok {
			return fmt.Errorf("xmp: %q is not a valid XML name", s)
		}
	}
	return nil
}

// scopeAt returns the namespace bindings in force at n, nearest declaration
// winning.
func scopeAt(n *node) map[string]string {
	out := map[string]string{"xml": NSXML}
	var chain []*node
	for x := n; x != nil; x = x.parent {
		chain = append(chain, x)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		for _, a := range chain[i].attrs {
			if pfx, ok := a.declares(); ok {
				out[pfx] = a.value
			}
		}
	}
	return out
}

// usesPrefixOtherwise reports whether any element or attribute in n's subtree is
// written with prefix pfx but means something other than ns, which is what
// declaring pfx on n could change.
func usesPrefixOtherwise(n *node, pfx, ns string) bool {
	if n.kind != elemNode {
		return false
	}
	if n.prefix == pfx && n.space != ns {
		return true
	}
	for _, a := range n.attrs {
		if a.prefix == pfx && !a.isNamespaceDecl() && a.space != ns {
			return true
		}
	}
	for _, c := range n.children {
		if usesPrefixOtherwise(c, pfx, ns) {
			return true
		}
	}
	return false
}

// builder makes new nodes to be inserted under parent at, choosing prefixes
// that mean the right thing there.
//
// A namespace the new nodes need that is not already bound at `at` is declared
// on the rdf:Description when `at` is one — where XMP writers conventionally put
// declarations — and otherwise on the new element itself, which cannot disturb
// anything that was already in the packet.
type builder struct {
	at    *node
	scope map[string]string
	// pending are declarations to put on the new element, when at is not a
	// description.
	pending []attr
}

func newBuilder(at *node) *builder {
	return &builder{at: at, scope: scopeAt(at)}
}

// prefix returns a prefix bound to ns at the insertion point, declaring one if
// needed. The preferred prefix is used whenever it can mean ns there, because
// some prefixes are normative (PDF/A requires "pdfaid", "pdfaExtension" and the
// rest), and a reader of the packet expects the conventional one.
func (b *builder) prefix(ns, preferred string) (string, error) {
	if ns == NSXML {
		return "xml", nil
	}
	if preferred == "" {
		// An unprefixed element takes the default namespace; use it when that
		// is ns. Otherwise a prefix has to be invented.
		if b.scope[""] == ns {
			return "", nil
		}
		preferred = "ns"
	}
	if err := checkName(preferred); err != nil {
		return "", err
	}
	if b.scope[preferred] == ns {
		return preferred, nil
	}
	free := func(pfx string) bool {
		if _, bound := b.scope[pfx]; bound {
			return false
		}
		return !usesPrefixOtherwise(b.at, pfx, ns)
	}
	if free(preferred) {
		b.declare(preferred, ns)
		return preferred, nil
	}
	// Another prefix already meaning ns. The bindings are sorted so the choice,
	// and so the output, does not depend on map order.
	bound := make([]string, 0, len(b.scope))
	for pfx, uri := range b.scope {
		if uri == ns && pfx != "" {
			bound = append(bound, pfx)
		}
	}
	sort.Strings(bound)
	if len(bound) > 0 {
		return bound[0], nil
	}
	for i := 1; ; i++ {
		pfx := preferred + strconv.Itoa(i)
		if free(pfx) {
			b.declare(pfx, ns)
			return pfx, nil
		}
	}
}

func (b *builder) declare(pfx, ns string) {
	b.scope[pfx] = ns
	a := attr{prefix: "xmlns", local: pfx, space: "xmlns", value: ns}
	if b.at.is(NSRDF, "Description") {
		b.at.attrs = append(b.at.attrs, a)
		b.at.attrsDirty = true
		b.at.touch()
		return
	}
	b.pending = append(b.pending, a)
}

// elem makes an element ns:local.
func (b *builder) elem(ns, preferred, local string) (*node, error) {
	if err := checkName(local); err != nil {
		return nil, err
	}
	pfx, err := b.prefix(ns, preferred)
	if err != nil {
		return nil, err
	}
	return &node{kind: elemNode, prefix: pfx, local: local, space: ns}, nil
}

// attr makes an attribute ns:local="value".
func (b *builder) attr(ns, preferred, local, value string) (attr, error) {
	if err := CheckText(value); err != nil {
		return attr{}, err
	}
	if preferred == "" {
		// An unprefixed attribute is in no namespace, whatever the default is.
		preferred = "ns"
		if ns == NSRDF {
			preferred = "rdf"
		}
	}
	pfx, err := b.prefix(ns, preferred)
	if err == nil && pfx == "" {
		err = fmt.Errorf("xmp: no prefix for attribute %s", local)
	}
	if err != nil {
		return attr{}, err
	}
	return attr{prefix: pfx, local: local, space: ns, value: value}, nil
}

// finish attaches any pending declarations to the element being inserted.
func (b *builder) finish(top *node) {
	top.attrs = append(b.pending, top.attrs...)
	b.pending = nil
}

func appendChild(parent, n *node) {
	n.parent = parent
	parent.children = append(parent.children, n)
}

func textChild(parent *node, s string) {
	appendChild(parent, &node{kind: textNode, data: s})
}

func isSpace(n *node) bool {
	return n.kind == textNode && strings.TrimSpace(n.data) == ""
}

// insertChild adds n to parent, before any trailing whitespace, and indents it
// like the parent's last element child so a packet a person reads stays
// readable. index < 0 appends; otherwise n goes at that position.
func insertChild(parent, n *node, index int) {
	n.parent = parent
	kids := parent.children
	if index >= 0 {
		parent.children = append(kids[:index:index], append([]*node{n}, kids[index:]...)...)
		parent.touch()
		return
	}
	pos := len(kids)
	if pos > 0 && isSpace(kids[pos-1]) {
		pos--
	}
	indent := ""
	for i := pos - 1; i >= 0; i-- {
		if kids[i].kind == elemNode {
			if i > 0 && isSpace(kids[i-1]) {
				indent = kids[i-1].data
			}
			break
		}
	}
	if indent == "" && pos < len(kids) {
		indent = kids[pos].data + "  "
	}
	ins := []*node{n}
	if indent == "" && len(kids) == 0 {
		// The parent's first child: indent one step past the parent itself,
		// and put the parent's end tag back on a line of its own.
		if outer := precedingSpace(parent); outer != "" {
			ins = []*node{{kind: textNode, data: outer + "  ", parent: parent}, n, {kind: textNode, data: outer, parent: parent}}
		}
	} else if indent != "" {
		ins = []*node{{kind: textNode, data: indent, parent: parent}, n}
	}
	parent.children = append(kids[:pos:pos], append(ins, kids[pos:]...)...)
	parent.touch()
}

// precedingSpace is the whitespace that indents n within its parent, if the
// node before n is whitespace that starts a new line.
func precedingSpace(n *node) string {
	if n.parent == nil {
		return ""
	}
	sib := n.parent.children
	for i, c := range sib {
		if c == n && i > 0 && isSpace(sib[i-1]) && strings.Contains(sib[i-1].data, "\n") {
			return sib[i-1].data
		}
	}
	return ""
}

// removeChild takes c out of parent along with the whitespace that indented
// it, so repeated edits do not accumulate blank lines.
func removeChild(parent, c *node) {
	kids := parent.children
	for i, k := range kids {
		if k != c {
			continue
		}
		from := i
		if i > 0 && isSpace(kids[i-1]) {
			from = i - 1
		}
		parent.children = append(kids[:from:from], kids[i+1:]...)
		parent.touch()
		return
	}
}

// target finds where a property ns:name lives or should go: the description
// holding its first element-form occurrence and the index there, or else the
// first description with a property in the same namespace, or else the first
// description. A packet with rdf:RDF but no description gets a new one.
func (p *Packet) target(ns, name string) (desc *node, existing *node, err error) {
	rdf := p.rdf()
	if rdf == nil {
		return nil, nil, ErrNoRDF
	}
	descs := p.descriptions()
	for _, d := range descs {
		for _, c := range d.children {
			if c.is(ns, name) {
				return d, c, nil
			}
		}
	}
	for _, d := range descs {
		for _, a := range d.attrs {
			if a.space == ns && a.local == name {
				return d, nil, nil
			}
		}
	}
	for _, d := range descs {
		for _, c := range d.children {
			if c.kind == elemNode && c.space == ns {
				return d, nil, nil
			}
		}
		for _, a := range d.attrs {
			if a.space == ns && !a.isNamespaceDecl() {
				return d, nil, nil
			}
		}
	}
	if len(descs) > 0 {
		return descs[0], nil, nil
	}
	// Every description of a packet describes the same resource, so a new one
	// carries the rdf:about the packet already uses, which for a PDF is "".
	b := newBuilder(rdf)
	d, err := b.elem(NSRDF, rdf.prefix, "Description")
	if err != nil {
		return nil, nil, err
	}
	about, err := b.attr(NSRDF, rdf.prefix, "about", "")
	if err != nil {
		return nil, nil, err
	}
	d.attrs = append(d.attrs, about)
	b.finish(d)
	insertChild(rdf, d, -1)
	p.dirty = true
	return d, nil, nil
}

// removeOccurrences deletes every occurrence of ns:name except keep, in both
// forms, across every description.
func (p *Packet) removeOccurrences(ns, name string, keep *node) bool {
	removed := false
	for _, d := range p.descriptions() {
		attrs := d.attrs[:0:0]
		for _, a := range d.attrs {
			if a.space == ns && a.local == name && !a.isNamespaceDecl() {
				removed = true
				d.attrsDirty = true
				d.touch()
				continue
			}
			attrs = append(attrs, a)
		}
		d.attrs = attrs
		for _, c := range append([]*node(nil), d.children...) {
			if c != keep && c.is(ns, name) {
				removeChild(d, c)
				removed = true
			}
		}
	}
	if removed {
		p.dirty = true
	}
	return removed
}

// Remove deletes the property ns:name wherever it occurs, and reports whether
// there was one.
func (p *Packet) Remove(ns, name string) bool {
	return p.removeOccurrences(ns, name, nil)
}

// set replaces the property ns:name with the element build makes, keeping its
// position when it already existed in element form.
func (p *Packet) set(ns, name string, build func(b *builder) (*node, error)) error {
	if err := checkName(name); err != nil {
		return err
	}
	desc, existing, err := p.target(ns, name)
	if err != nil {
		return err
	}
	b := newBuilder(desc)
	el, err := build(b)
	if err != nil {
		return err
	}
	b.finish(el)
	p.removeOccurrences(ns, name, existing)
	if existing != nil {
		for i, c := range desc.children {
			if c == existing {
				el.parent = desc
				desc.children[i] = el
				desc.touch()
				break
			}
		}
	} else {
		insertChild(desc, el, -1)
	}
	p.dirty = true
	return nil
}

// SetText sets ns:name to the simple value value. prefix is the prefix to
// write it with when the namespace is not already bound.
func (p *Packet) SetText(ns, prefix, name, value string) error {
	if err := CheckText(value); err != nil {
		return fmt.Errorf("xmp: %s: %w", name, err)
	}
	return p.set(ns, name, func(b *builder) (*node, error) {
		el, err := b.elem(ns, prefix, name)
		if err != nil {
			return nil, err
		}
		textChild(el, value)
		return el, nil
	})
}

// SetSeq sets ns:name to an ordered array of simple values.
func (p *Packet) SetSeq(ns, prefix, name string, items []string) error {
	return p.setArray(ns, prefix, name, "Seq", items)
}

// SetBag sets ns:name to an unordered array of simple values.
func (p *Packet) SetBag(ns, prefix, name string, items []string) error {
	return p.setArray(ns, prefix, name, "Bag", items)
}

func (p *Packet) setArray(ns, prefix, name, container string, items []string) error {
	for _, it := range items {
		if err := CheckText(it); err != nil {
			return fmt.Errorf("xmp: %s: %w", name, err)
		}
	}
	return p.set(ns, name, func(b *builder) (*node, error) {
		el, err := b.elem(ns, prefix, name)
		if err != nil {
			return nil, err
		}
		arr, err := b.elem(NSRDF, "rdf", container)
		if err != nil {
			return nil, err
		}
		appendChild(el, arr)
		for _, it := range items {
			li, err := b.elem(NSRDF, "rdf", "li")
			if err != nil {
				return nil, err
			}
			textChild(li, it)
			appendChild(arr, li)
		}
		return el, nil
	})
}

// SetAltText sets the lang item of the language alternative ns:name. Other
// languages already present are kept: the caller said what the title is in
// one language, not that the others are gone. "x-default" is the item a reader
// shows when no language matches, and it goes first, as XMP requires.
//
// A property that exists but is not a language alternative is replaced by one.
func (p *Packet) SetAltText(ns, prefix, name, lang, value string) error {
	if err := CheckText(value); err != nil {
		return fmt.Errorf("xmp: %s: %w", name, err)
	}
	if err := CheckText(lang); err != nil || lang == "" {
		return fmt.Errorf("xmp: %s: invalid language %q", name, lang)
	}
	if _, existing, err := p.target(ns, name); err == nil && existing != nil {
		if alt := childElement(existing, NSRDF, "Alt"); alt != nil && len(existing.elementChildren()) == 1 {
			p.removeOccurrences(ns, name, existing)
			for _, li := range alt.children {
				if !li.is(NSRDF, "li") {
					continue
				}
				if l, ok := li.attrValue(NSXML, "lang"); ok && strings.EqualFold(l, lang) {
					li.children = nil
					textChild(li, value)
					li.touch()
					p.dirty = true
					return nil
				}
			}
			b := newBuilder(alt)
			li, err := b.elem(NSRDF, alt.prefix, "li")
			if err != nil {
				return err
			}
			la, err := b.attr(NSXML, "xml", "lang", lang)
			if err != nil {
				return err
			}
			li.attrs = append(li.attrs, la)
			textChild(li, value)
			b.finish(li)
			index := -1
			if strings.EqualFold(lang, "x-default") {
				index = firstElementIndex(alt)
			}
			insertChild(alt, li, index)
			p.dirty = true
			return nil
		}
	}
	return p.set(ns, name, func(b *builder) (*node, error) {
		el, err := b.elem(ns, prefix, name)
		if err != nil {
			return nil, err
		}
		alt, err := b.elem(NSRDF, "rdf", "Alt")
		if err != nil {
			return nil, err
		}
		appendChild(el, alt)
		li, err := b.elem(NSRDF, "rdf", "li")
		if err != nil {
			return nil, err
		}
		la, err := b.attr(NSXML, "xml", "lang", lang)
		if err != nil {
			return nil, err
		}
		li.attrs = append(li.attrs, la)
		textChild(li, value)
		appendChild(alt, li)
		return el, nil
	})
}

func (n *node) elementChildren() []*node {
	var out []*node
	for _, c := range n.children {
		if c.kind == elemNode {
			out = append(out, c)
		}
	}
	return out
}

func firstElementIndex(n *node) int {
	for i, c := range n.children {
		if c.kind == elemNode {
			return i
		}
	}
	return -1
}

// ExtensionSchema is one entry of the PDF/A extension schema container
// (ISO 19005-1 6.7.8; -2/-3 6.6.2.3.3): the declaration PDF/A requires for any
// metadata namespace outside the predefined schemas.
type ExtensionSchema struct {
	Schema       string // human-readable description
	NamespaceURI string
	Prefix       string
	Properties   []ExtensionProperty
}

// ExtensionProperty declares one property of an extension schema.
type ExtensionProperty struct {
	Name        string
	ValueType   string
	Category    string // "internal" or "external"
	Description string
}

// SetExtensionSchema declares s in the packet's pdfaExtension:schemas bag,
// replacing a declaration already there for the same namespace URI and leaving
// every other declaration as it was.
func (p *Packet) SetExtensionSchema(s ExtensionSchema) error {
	for _, v := range []string{s.Schema, s.NamespaceURI, s.Prefix} {
		if err := CheckText(v); err != nil {
			return err
		}
	}
	for _, pr := range s.Properties {
		for _, v := range []string{pr.Name, pr.ValueType, pr.Category, pr.Description} {
			if err := CheckText(v); err != nil {
				return err
			}
		}
	}
	buildLi := func(b *builder, rdfPrefix string) (*node, error) {
		li, err := b.elem(NSRDF, rdfPrefix, "li")
		if err != nil {
			return nil, err
		}
		pt, err := b.attr(NSRDF, rdfPrefix, "parseType", "Resource")
		if err != nil {
			return nil, err
		}
		li.attrs = append(li.attrs, pt)
		field := func(parent *node, ns, pfx, name, value string) error {
			f, err := b.elem(ns, pfx, name)
			if err != nil {
				return err
			}
			textChild(f, value)
			appendChild(parent, f)
			return nil
		}
		for _, f := range [][2]string{{"schema", s.Schema}, {"namespaceURI", s.NamespaceURI}, {"prefix", s.Prefix}} {
			if err := field(li, NSPDFASchema, "pdfaSchema", f[0], f[1]); err != nil {
				return nil, err
			}
		}
		if len(s.Properties) > 0 {
			prop, err := b.elem(NSPDFASchema, "pdfaSchema", "property")
			if err != nil {
				return nil, err
			}
			seq, err := b.elem(NSRDF, rdfPrefix, "Seq")
			if err != nil {
				return nil, err
			}
			appendChild(prop, seq)
			for _, pr := range s.Properties {
				item, err := b.elem(NSRDF, rdfPrefix, "li")
				if err != nil {
					return nil, err
				}
				pt, err := b.attr(NSRDF, rdfPrefix, "parseType", "Resource")
				if err != nil {
					return nil, err
				}
				item.attrs = append(item.attrs, pt)
				for _, f := range [][2]string{{"name", pr.Name}, {"valueType", pr.ValueType}, {"category", pr.Category}, {"description", pr.Description}} {
					if err := field(item, NSPDFAProperty, "pdfaProperty", f[0], f[1]); err != nil {
						return nil, err
					}
				}
				appendChild(seq, item)
			}
			appendChild(li, prop)
		}
		return li, nil
	}

	_, existing, err := p.target(NSPDFAExtension, "schemas")
	if err != nil {
		return err
	}
	if existing != nil {
		if bag := childElement(existing, NSRDF, "Bag"); bag != nil {
			for _, li := range append([]*node(nil), bag.children...) {
				if li.is(NSRDF, "li") && liNamespaceURI(li) == s.NamespaceURI {
					removeChild(bag, li)
				}
			}
			b := newBuilder(bag)
			li, err := buildLi(b, bag.prefix)
			if err != nil {
				return err
			}
			b.finish(li)
			insertChild(bag, li, -1)
			p.dirty = true
			return nil
		}
	}
	return p.set(NSPDFAExtension, "schemas", func(b *builder) (*node, error) {
		el, err := b.elem(NSPDFAExtension, "pdfaExtension", "schemas")
		if err != nil {
			return nil, err
		}
		rdfPrefix, err := b.prefix(NSRDF, "rdf")
		if err != nil {
			return nil, err
		}
		bag, err := b.elem(NSRDF, rdfPrefix, "Bag")
		if err != nil {
			return nil, err
		}
		appendChild(el, bag)
		li, err := buildLi(b, rdfPrefix)
		if err != nil {
			return nil, err
		}
		appendChild(bag, li)
		return el, nil
	})
}

// liNamespaceURI is the pdfaSchema:namespaceURI of an extension schema entry,
// in whichever of the RDF forms the entry was written.
func liNamespaceURI(li *node) string {
	v := parseValue(li)
	for _, f := range v.Fields {
		if f.NS == NSPDFASchema && f.Name == "namespaceURI" {
			return strings.TrimSpace(f.Value.Text)
		}
	}
	return ""
}
