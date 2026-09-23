package xmp

import "strings"

// Kind is the structural form of an XMP property value.
type Kind int

const (
	Simple Kind = iota
	Struct
	Bag
	Seq
	Alt
)

func (k Kind) String() string {
	switch k {
	case Simple:
		return "simple value"
	case Struct:
		return "structure"
	case Bag:
		return "Bag"
	case Seq:
		return "Seq"
	case Alt:
		return "Alt"
	}
	return "?"
}

// Value is a property value as XMP defines it (ISO 16684-1 6.2): a simple value,
// a structure of fields, or an array of values. Text is unescaped: what the
// packet means, not how it was written.
type Value struct {
	Kind    Kind
	Text    string  // simple value text (or rdf:resource URI)
	IsURI   bool    // value given as an rdf:resource attribute
	HasLang bool    // xml:lang present on this value
	Lang    string  // the xml:lang value, when HasLang
	Items   []Value // array items (Bag/Seq/Alt)
	Fields  []Field // structure fields
}

// Field is one field of a structure value.
type Field struct {
	NS     string
	Name   string
	Prefix string // as written; informational, never used to identify the field
	Value  Value
}

// Property is one top-level property of an rdf:Description.
type Property struct {
	NS     string
	Name   string
	Prefix string // as written; informational, never used to identify the property
	// Attr reports the attribute form (<rdf:Description ns:name="value">).
	Attr  bool
	Value Value
}

// AltText selects the item of a language alternative for lang, compared
// case-insensitively as xml:lang values are. Asking for "x-default" when no item
// carries it returns the first item: XMP places the default first (ISO 16684-1
// 8.2.2.4) and a reader shown a title in some language is better served than
// one shown nothing. A value that is not an Alt answers with its own text, so a
// producer that wrote a plain string where an Alt belongs is still read.
func (v Value) AltText(lang string) (string, bool) {
	if v.Kind == Simple {
		return v.Text, true
	}
	if v.Kind != Alt && v.Kind != Seq && v.Kind != Bag {
		return "", false
	}
	for _, it := range v.Items {
		if it.HasLang && strings.EqualFold(it.Lang, lang) {
			return it.Text, true
		}
	}
	if strings.EqualFold(lang, "x-default") && len(v.Items) > 0 {
		return v.Items[0].Text, true
	}
	return "", false
}

// Properties returns every top-level property of every rdf:Description in the
// packet, in document order, attribute-form properties of a description before
// its element-form ones.
func (p *Packet) Properties() []Property {
	var props []Property
	for _, desc := range p.descriptions() {
		props = appendDescriptionProperties(props, desc)
	}
	return props
}

func appendDescriptionProperties(props []Property, desc *node) []Property {
	for _, a := range desc.attrs {
		if a.isNamespaceDecl() || a.space == NSRDF || a.space == NSXML || a.space == "" {
			continue
		}
		props = append(props, Property{NS: a.space, Name: a.local, Prefix: a.prefix, Attr: true,
			Value: Value{Kind: Simple, Text: a.value}})
	}
	for _, el := range desc.children {
		if el.kind != elemNode || el.space == NSRDF {
			continue
		}
		props = append(props, Property{NS: el.space, Name: el.local, Prefix: el.prefix, Value: parseValue(el)})
	}
	return props
}

// Lookup returns every occurrence of the property ns:name. XMP allows one; a
// packet that carries two is malformed metadata, and a reader that must know
// which one it saw asks here rather than through Get.
func (p *Packet) Lookup(ns, name string) []Property {
	var out []Property
	for _, pr := range p.Properties() {
		if pr.NS == ns && pr.Name == name {
			out = append(out, pr)
		}
	}
	return out
}

// Get returns the first occurrence of the property ns:name.
func (p *Packet) Get(ns, name string) (Property, bool) {
	for _, desc := range p.descriptions() {
		for _, a := range desc.attrs {
			if a.space == ns && a.local == name && !a.isNamespaceDecl() {
				return Property{NS: ns, Name: name, Prefix: a.prefix, Attr: true, Value: Value{Kind: Simple, Text: a.value}}, true
			}
		}
		for _, el := range desc.children {
			if el.kind == elemNode && el.space == ns && el.local == name {
				return Property{NS: ns, Name: name, Prefix: el.prefix, Value: parseValue(el)}, true
			}
		}
	}
	return Property{}, false
}

// Text returns the simple value of ns:name, whitespace-trimmed, and whether
// the property is present at all — a present property may have an empty value,
// which is not the same as an absent one. A property that is not a simple value
// is present with an empty text.
func (p *Packet) Text(ns, name string) (string, bool) {
	pr, ok := p.Get(ns, name)
	if !ok {
		return "", false
	}
	if pr.Value.Kind != Simple {
		return "", true
	}
	return strings.TrimSpace(pr.Value.Text), true
}

// parseValue interprets one property (or field, or array item) element.
func parseValue(el *node) Value {
	var v Value
	for _, a := range el.attrs {
		switch {
		case a.space == NSRDF && a.local == "parseType" && a.value == "Resource":
			// rdf:value inside means a QUALIFIED simple value, not a struct.
			if val := childElement(el, NSRDF, "value"); val != nil {
				return Value{Kind: Simple, Text: strings.TrimSpace(val.text()), HasLang: v.HasLang, Lang: v.Lang}
			}
			v.Kind = Struct
			v.Fields = parseFields(el)
			return v
		case a.space == NSRDF && a.local == "resource":
			return Value{Kind: Simple, Text: a.value, IsURI: true}
		case a.space == NSXML && a.local == "lang":
			v.HasLang, v.Lang = true, a.value
		}
	}

	// A single rdf container child makes this an array.
	var containers, descriptions, others []*node
	var rdfValue *node
	for _, c := range el.children {
		if c.kind != elemNode {
			continue
		}
		if c.space == NSRDF {
			switch c.local {
			case "Bag", "Seq", "Alt":
				containers = append(containers, c)
			case "Description":
				descriptions = append(descriptions, c)
			case "value":
				rdfValue = c
			}
			continue
		}
		others = append(others, c)
	}

	// rdf:value marks a QUALIFIED simple value (the siblings are qualifiers,
	// e.g. xmpidq:Scheme on xmp:Identifier items), not a structure.
	if rdfValue != nil {
		v.Kind = Simple
		v.Text = strings.TrimSpace(rdfValue.text())
		return v
	}

	switch {
	case len(containers) == 1:
		switch containers[0].local {
		case "Bag":
			v.Kind = Bag
		case "Seq":
			v.Kind = Seq
		case "Alt":
			v.Kind = Alt
		}
		for _, li := range containers[0].children {
			if li.is(NSRDF, "li") {
				v.Items = append(v.Items, parseValue(li))
			}
		}
	case len(descriptions) == 1 && len(others) == 0:
		// Struct in nested-Description form — unless it carries rdf:value,
		// which makes it a qualified simple value.
		if val := childElement(descriptions[0], NSRDF, "value"); val != nil {
			v.Kind = Simple
			v.Text = strings.TrimSpace(val.text())
			return v
		}
		v.Kind = Struct
		v.Fields = parseFields(descriptions[0])
	case len(others) > 0:
		// Non-rdf children without parseType: treat as structure fields.
		v.Kind = Struct
		v.Fields = parseFields(el)
	default:
		v.Kind = Simple
		v.Text = strings.TrimSpace(el.text())
	}
	return v
}

func childElement(el *node, space, local string) *node {
	for _, c := range el.children {
		if c.is(space, local) {
			return c
		}
	}
	return nil
}

// parseFields reads structure fields from an element's children (and non-rdf
// attributes, which are shorthand fields).
func parseFields(el *node) []Field {
	var fields []Field
	for _, a := range el.attrs {
		if a.isNamespaceDecl() || a.space == NSRDF || a.space == NSXML || a.space == "" {
			continue
		}
		fields = append(fields, Field{NS: a.space, Name: a.local, Prefix: a.prefix, Value: Value{Kind: Simple, Text: a.value}})
	}
	for _, c := range el.children {
		if c.kind != elemNode || c.space == NSRDF {
			continue
		}
		fields = append(fields, Field{NS: c.space, Name: c.local, Prefix: c.prefix, Value: parseValue(c)})
	}
	return fields
}

// Binding is one namespace declaration as written: xmlns:Prefix="URI", or
// xmlns="URI" with an empty Prefix.
type Binding struct {
	Prefix string
	URI    string
}

// Declarations returns every namespace declaration in the packet, in document
// order. Rules about which prefix a namespace is written with — PDF/A's
// canonical prefixes for its own schemas — read them here rather than out of
// the packet text, where a declaration inside a comment would count.
func (p *Packet) Declarations() []Binding {
	var out []Binding
	var walk func(n *node)
	walk = func(n *node) {
		if n.kind != elemNode {
			return
		}
		for _, a := range n.attrs {
			if pfx, ok := a.declares(); ok {
				out = append(out, Binding{Prefix: pfx, URI: a.value})
			}
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
