package pdfa

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// This file implements XMP packet parsing and PDF/A metadata property
// validation (ISO 19005-1, 6.7.9 "Properties"; ISO 19005-2/-3, 6.6.2.3
// "Schemas"). Properties in predefined schemas must exist in the schema and
// carry the schema's value form (simple/Bag/Seq/LangAlt/structure, plus
// simple-value syntax like Integer or Rational); properties in any other
// namespace must be declared by an embedded PDF/A extension schema.

// RDF and XMP container namespaces.
const (
	nsRDF = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	nsXML = "http://www.w3.org/XML/1998/namespace"

	nsPDFAExtension = "http://www.aiim.org/pdfa/ns/extension/"
	nsPDFASchema    = "http://www.aiim.org/pdfa/ns/schema#"
	nsPDFAProperty  = "http://www.aiim.org/pdfa/ns/property#"
	nsPDFAType      = "http://www.aiim.org/pdfa/ns/type#"
	nsPDFAField     = "http://www.aiim.org/pdfa/ns/field#"
)

// xmpKind is the structural form of an XMP property value.
type xmpKind int

const (
	xmpSimple xmpKind = iota
	xmpStruct
	xmpBag
	xmpSeq
	xmpAlt
)

func (k xmpKind) String() string {
	switch k {
	case xmpSimple:
		return "simple value"
	case xmpStruct:
		return "structure"
	case xmpBag:
		return "Bag"
	case xmpSeq:
		return "Seq"
	case xmpAlt:
		return "Alt"
	}
	return "?"
}

// xmpValue is a parsed XMP property value.
type xmpValue struct {
	Kind    xmpKind
	Text    string     // simple value text (or rdf:resource URI)
	IsURI   bool       // value given as rdf:resource attribute
	HasLang bool       // xml:lang present (on this value; used for Alt items)
	Items   []xmpValue // array items (Bag/Seq/Alt)
	Fields  []xmpField // structure fields
}

type xmpField struct {
	NS    string
	Name  string
	Value xmpValue
}

// xmpProperty is one top-level property from an rdf:Description block.
type xmpProperty struct {
	NS    string
	Name  string
	Value xmpValue
}

// xmlNode is a minimal DOM used to interpret RDF/XMP structure.
type xmlNode struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Children []*xmlNode
	Text     string
}

// The default bound on an XMP packet that parseXMPProperties will build a node
// tree for is defaultMaxXMPPacketBytes; a caller can change it with
// WithMaxXMPPacketBytes. Building the tree is O(n²): a large packet produces
// hundreds of thousands of nodes, and its incremental construction (millions of
// small allocations from streaming tokens) triggers thousands of GC cycles, each
// rescanning the ever-growing live tree. A 14 MB packet took ~37 s.
//
// The default was 2 MiB, justified by "the largest in the veraPDF corpus is
// 66 KB — orders of magnitude above any legitimate packet". Measuring a
// 978-file Common Crawl sample falsified that: the largest real packet there is
// 1,639,865 bytes, 25x the corpus maximum and 78% of the old cap. The
// conformance corpus is not a guide to the real-world tail.
//
// It is now 4 MiB rather than 8: because the cost is quadratic the worst case
// runs roughly 3 s at 4 MiB but about 12 s at 8 MiB, so the extra headroom is
// not worth what it would cost on a hostile packet. A pathological packet has
// only its property checks skipped; well-formedness is still validated by
// streaming (see xmpWellFormed).

// xmpWellFormed streams an XMP packet and reports whether it is well-formed XML
// and whether it contains a properly namespaced rdf:RDF element, WITHOUT
// building a node tree. Well-formedness needs no tree — the streaming decoder
// already validates tag structure as it goes — so this stays O(n) even on an
// adversarially large packet, where parseXMLTree would blow up (see
// xmpPropertyMaxBytes). wellFormed is false for an empty or non-element packet,
// matching parseXMLTree's "no XML content" error.
func xmpWellFormed(data []byte) (wellFormed, hasRDF bool) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	sawElement := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return sawElement, hasRDF
		}
		if err != nil {
			return false, hasRDF
		}
		if se, ok := tok.(xml.StartElement); ok {
			sawElement = true
			if se.Name.Space == nsRDF && se.Name.Local == "RDF" {
				hasRDF = true
			}
		}
	}
}

// parseXMLTree parses an XML document into an xmlNode tree.
func parseXMLTree(data []byte) (*xmlNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	// XMP is UTF-8 by the time we get here (core.DecodeXMPToUTF8); some packets
	// still carry an encoding declaration.
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	var root *xmlNode
	var stack []*xmlNode
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			node := &xmlNode{Name: t.Name, Attrs: append([]xml.Attr(nil), t.Attr...)}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			} else if root == nil {
				root = node
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(t)
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("no XML content")
	}
	return root, nil
}

// findRDF locates the rdf:RDF element anywhere under root.
func findRDF(root *xmlNode) *xmlNode {
	if root.Name.Space == nsRDF && root.Name.Local == "RDF" {
		return root
	}
	for _, c := range root.Children {
		if r := findRDF(c); r != nil {
			return r
		}
	}
	return nil
}

// attrIs reports whether a is the given namespaced attribute. encoding/xml
// resolves prefixes to namespace URIs, but leaves the bare "xmlns" and
// prefixed declarations distinguishable via Space=="xmlns".
func attrIs(a xml.Attr, space, local string) bool {
	return a.Name.Space == space && a.Name.Local == local
}

func isNamespaceDecl(a xml.Attr) bool {
	return a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns")
}

// parseXMPProperties extracts all top-level properties from every
// rdf:Description block in the packet.
func parseXMPProperties(data []byte, maxPacketBytes int) ([]xmpProperty, error) {
	// Bound the O(n²) tree build; a pathologically large packet is not parsed
	// for properties (the caller treats this as "no properties to check", not a
	// violation — its well-formedness is validated separately by streaming).
	if len(data) > maxPacketBytes {
		return nil, fmt.Errorf("XMP packet too large to parse for properties (%d bytes)", len(data))
	}
	root, err := parseXMLTree(data)
	if err != nil {
		return nil, err
	}
	rdf := findRDF(root)
	if rdf == nil {
		return nil, fmt.Errorf("no rdf:RDF element")
	}
	var props []xmpProperty
	for _, desc := range rdf.Children {
		if desc.Name.Space != nsRDF || desc.Name.Local != "Description" {
			continue
		}
		// Attribute-form properties.
		for _, a := range desc.Attrs {
			if isNamespaceDecl(a) || a.Name.Space == nsRDF || a.Name.Space == nsXML || a.Name.Space == "" {
				continue
			}
			props = append(props, xmpProperty{
				NS:   a.Name.Space,
				Name: a.Name.Local,
				Value: xmpValue{
					Kind: xmpSimple,
					Text: a.Value,
				},
			})
		}
		// Element-form properties.
		for _, el := range desc.Children {
			if el.Name.Space == nsRDF {
				continue
			}
			props = append(props, xmpProperty{
				NS:    el.Name.Space,
				Name:  el.Name.Local,
				Value: parseXMPValue(el),
			})
		}
	}
	return props, nil
}

// parseXMPValue interprets one property (or field, or array item) element.
func parseXMPValue(el *xmlNode) xmpValue {
	var v xmpValue
	hasLang := false
	for _, a := range el.Attrs {
		switch {
		case attrIs(a, nsRDF, "parseType") && a.Value == "Resource":
			// rdf:value inside means a QUALIFIED simple value, not a struct.
			for _, c := range el.Children {
				if c.Name.Space == nsRDF && c.Name.Local == "value" {
					v.Kind = xmpSimple
					v.Text = strings.TrimSpace(c.Text)
					v.HasLang = hasLang
					return v
				}
			}
			v.Kind = xmpStruct
			v.Fields = parseXMPFields(el)
			v.HasLang = hasLang
			return v
		case attrIs(a, nsRDF, "resource"):
			v.Kind = xmpSimple
			v.Text = a.Value
			v.IsURI = true
			return v
		case attrIs(a, nsXML, "lang"):
			hasLang = true
		}
	}
	v.HasLang = hasLang

	// A single rdf container child makes this an array.
	var containers, descriptions, others []*xmlNode
	var rdfValue *xmlNode
	for _, c := range el.Children {
		if c.Name.Space == nsRDF {
			switch c.Name.Local {
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

	// rdf:value marks a QUALIFIED simple value (the siblings are
	// qualifiers, e.g. xmpidq:Scheme on xmp:Identifier items), not a
	// structure.
	if rdfValue != nil {
		v.Kind = xmpSimple
		v.Text = strings.TrimSpace(rdfValue.Text)
		return v
	}

	switch {
	case len(containers) == 1:
		switch containers[0].Name.Local {
		case "Bag":
			v.Kind = xmpBag
		case "Seq":
			v.Kind = xmpSeq
		case "Alt":
			v.Kind = xmpAlt
		}
		for _, li := range containers[0].Children {
			if li.Name.Space == nsRDF && li.Name.Local == "li" {
				v.Items = append(v.Items, parseXMPValue(li))
			}
		}
	case len(descriptions) == 1 && len(others) == 0:
		// Struct in nested-Description form — unless it carries rdf:value,
		// which makes it a qualified simple value.
		for _, c := range descriptions[0].Children {
			if c.Name.Space == nsRDF && c.Name.Local == "value" {
				v.Kind = xmpSimple
				v.Text = strings.TrimSpace(c.Text)
				return v
			}
		}
		v.Kind = xmpStruct
		v.Fields = parseXMPFields(descriptions[0])
	case len(others) > 0:
		// Non-rdf children without parseType: treat as structure fields.
		v.Kind = xmpStruct
		v.Fields = parseXMPFields(el)
	default:
		v.Kind = xmpSimple
		v.Text = strings.TrimSpace(el.Text)
	}
	return v
}

// parseXMPFields reads structure fields from an element's children (and
// non-rdf attributes, which are shorthand fields).
func parseXMPFields(el *xmlNode) []xmpField {
	var fields []xmpField
	for _, a := range el.Attrs {
		if isNamespaceDecl(a) || a.Name.Space == nsRDF || a.Name.Space == nsXML || a.Name.Space == "" {
			continue
		}
		fields = append(fields, xmpField{
			NS:    a.Name.Space,
			Name:  a.Name.Local,
			Value: xmpValue{Kind: xmpSimple, Text: a.Value},
		})
	}
	for _, c := range el.Children {
		if c.Name.Space == nsRDF {
			continue
		}
		fields = append(fields, xmpField{NS: c.Name.Space, Name: c.Name.Local, Value: parseXMPValue(c)})
	}
	return fields
}
