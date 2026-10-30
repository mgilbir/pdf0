package pdfua

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file holds the structure-tree rules only PDF/UA-2 has (ISO 14289-2
// 8.2.5.2, 8.2.5.12, 8.2.5.25 and 8.10.1's one widget per Form). Each reads
// the flattened tree (core.StructTree) and compares resolved types, as every
// structure check does, and each follows what the veraPDF 1.30.3 model reads
// for the property its PDFUA-2 profile tests; where the model is narrower or
// wider than the clause's prose, the comment says so.

// isGroupingType reports whether a node is one of the types veraPDF looks
// through when it asks for an element's children (PDStructElem.isPassThroughTag):
// NonStruct, Div and Part, by resolved type. An element whose type resolves
// to nothing standard is not one.
func isGroupingType(n structNode) bool {
	if !n.HasS || !n.Mapped {
		return false
	}
	switch n.StdType {
	case "NonStruct", "Div", "Part":
		return true
	}
	return false
}

// semanticParents returns, for each node of the flattened tree, the index of
// the nearest ancestor that is not a grouping element (isGroupingType), or -1
// for the structure tree root. veraPDF answers "the children of X" through
// grouping elements this way (GFPDStructTreeNode.getChildrenStandardTypes and
// its structural-significance children): the children of an L are its LIs,
// whether written under the L or under a Div inside it. The tree is
// pre-order, so a parent's answer is known before its children's, and the
// whole list costs one pass.
func semanticParents(d core.View, nodes []structNode) []int {
	sp := make([]int, len(nodes))
	for i, n := range nodes {
		d.Charge(1)
		p := n.Parent
		if p >= 0 && isGroupingType(nodes[p]) {
			p = sp[p]
		}
		sp[i] = p
	}
	return sp
}

// isStdType reports whether a node is a structure element whose type resolves
// to the standard type t. StdType alone is not enough: an element whose type
// resolves to nothing carries its own /S there.
func isStdType(n structNode, t object.Name) bool {
	return n.HasS && n.Mapped && n.NS != core.NSMathML && n.StdType == t
}

// checkUA2DocumentRoot is ISO 14289-2 8.2.5.2: the structure tree root holds a
// single Document element as its only child, and that element is in the PDF
// 2.0 namespace (ISO 32000-2 14.8.6).
//
// It reads the root's children as veraPDF does (the properties 8.2.5.2-t01
// and -t02 test, PDStructTreeRoot.kidsStandardTypes and
// PDStructTreeRoot.firstChildStandardTypeNamespaceURL): through the grouping
// types NonStruct, Div and Part; counting, for the single Document, only the
// children whose type resolves to a standard one; and asking the namespace of
// the first child, whatever its type. So a Part holding the Document passes
// this rule, as it passes veraPDF's, though the clause's prose says "only
// child"; veraPDF reports that file under ISO/TS 32005's containment rules
// instead (StructTreeRoot-Part), which pdf0 does not implement. An element
// listed twice in the root's /K is one element: the flattened tree visits
// each object once.
func checkUA2DocumentRoot(d core.View, cat *object.Dictionary) []Violation {
	if d.ResolveDict(cat.Get("StructTreeRoot")) == nil {
		return nil // 7.1 reports the missing tree
	}
	nodes := structTree(d, cat)
	sp := semanticParents(d, nodes)
	var kids []int     // the root's children, through grouping elements
	var types []string // the standard types among them
	for i, n := range nodes {
		if sp[i] != -1 || !n.HasS || isGroupingType(n) {
			continue
		}
		kids = append(kids, i)
		if n.Mapped {
			types = append(types, string(n.StdType))
		}
	}
	if len(types) != 1 || types[0] != "Document" {
		what := "no structure element"
		if len(types) > 0 {
			what = strings.Join(types, ", ")
		}
		return []Violation{{Clause: "8.2.5.2", Message: "the structure tree root must contain a single Document structure element as its only child; it contains " + what}}
	}
	first := nodes[kids[0]]
	if !first.Mapped {
		return []Violation{{Clause: "8.2.5.2", Message: "the structure tree root's first child, before its Document element, resolves to no standard type, so it is not in the PDF 2.0 namespace (" + core.NSPDF20 + ")", Object: anchorOf(d, first.Elem)}}
	}
	if first.NS != core.NSPDF20 {
		return []Violation{{Clause: "8.2.5.2", Message: "the Document structure element is not in the PDF 2.0 namespace (" + core.NSPDF20 + "); its namespace is " + first.NS, Object: anchorOf(d, first.Elem)}}
	}
	return nil
}

// checkUA2CaptionPosition is ISO 14289-2 8.2.5.27: a Caption is the first or
// the last child of the structure element whose content it captions.
//
// It reads an element's children as veraPDF does (8.2.5.27-t01,
// PDStructElem.kidsStandardTypes): through the grouping types, so a Div's
// children stand in its place, and only those whose type resolves to a
// standard one, the rest left out of the count. Each element is judged,
// grouping ones too, so a Caption between two children of a Div is reported
// for the Div and for the element the Div stands in.
func checkUA2CaptionPosition(d core.View, cat *object.Dictionary) []Violation {
	nodes := structTree(d, cat)
	kids := map[int][]int{} // element index -> its children through grouping elements
	for j, n := range nodes {
		if !n.HasS || !n.Mapped || isGroupingType(n) {
			continue
		}
		for p := n.Parent; p >= 0; p = nodes[p].Parent {
			d.Charge(1)
			kids[p] = append(kids[p], j)
			if !isGroupingType(nodes[p]) {
				break
			}
		}
	}
	var v []Violation
	for p := range nodes {
		ks := kids[p]
		for k := 1; k < len(ks)-1; k++ {
			if isStdType(nodes[ks[k]], "Caption") {
				v = append(v, Violation{Clause: "8.2.5.27", Message: fmt.Sprintf("<%s> has a Caption as its child %d of %d; a Caption is the first or the last child", nodes[p].StdType, k+1, len(ks)), Object: anchorOf(d, nodes[p].Elem)})
				break
			}
		}
	}
	return v
}

// checkUA2NoH is ISO 14289-2 8.2.5.12: a conforming file uses the numbered
// headings H1–Hn and not H (veraPDF 8.2.5.12-t01, every SEH object). An
// element whose type resolves to H through a role map is an H.
func checkUA2NoH(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	for _, n := range structTree(d, cat) {
		if isStdType(n, "H") {
			v = append(v, Violation{Clause: "8.2.5.12", Message: "the H structure type is used; PDF/UA-2 requires the numbered headings H1-Hn", Object: anchorOf(d, n.Elem)})
		}
	}
	return v
}

// checkUA2ListNumbering is ISO 14289-2 8.2.5.25: when a list's items carry Lbl
// elements, the L element has a ListNumbering attribute, and not the value
// None (veraPDF 8.2.5.25-t01).
//
// "Carries labels" is veraPDF's SEL.containsLabels: some LI child of the L —
// through grouping elements — has an Lbl child, again through grouping
// elements. The attribute is read as listNumbering says.
func checkUA2ListNumbering(d core.View, cat *object.Dictionary) []Violation {
	nodes := structTree(d, cat)
	sp := semanticParents(d, nodes)
	labelled := map[int]bool{} // LI index -> it has an Lbl child
	for i, n := range nodes {
		if p := sp[i]; p >= 0 && isStdType(n, "Lbl") && isStdType(nodes[p], "LI") {
			labelled[p] = true
		}
	}
	lists := map[int]bool{} // L index -> one of its LIs is labelled
	for li := range labelled {
		if p := sp[li]; p >= 0 && isStdType(nodes[p], "L") {
			lists[p] = true
		}
	}
	ln := newListNumbering(d, cat)
	var v []Violation
	for i, n := range nodes {
		if !lists[i] {
			continue
		}
		if ln.of(n.Elem) == "None" {
			v = append(v, Violation{Clause: "8.2.5.25", Message: "a list whose items have Lbl elements has no ListNumbering attribute, or ListNumbering None", Object: anchorOf(d, n.Elem)})
		}
	}
	return v
}

// listNumbering reads a list element's ListNumbering attribute as veraPDF does
// (AttributeHelper.getListNumbering): the first attribute object owned by List
// (/O /List) whose ListNumbering is a name, among those in the element's /A
// and then those its /C names in the structure tree root's /ClassMap
// (ownedAttrs, the reader the table rules share); failing that, the parent
// element's (/P), and so on up. A list with none
// anywhere is None, the attribute's default (ISO 32000-2 Table 383).
//
// An element without an attribute of its own has its parent's value, so each
// element's answer is memoised, and the elements on a /P chain are each read
// once however many lists share the chain: nested lists, each asking its whole
// ancestry, would otherwise cost the depth squared. The chain is the file's to
// make, so a /P loop is ended where it closes (with None), and every element
// read is charged to the run's work meter.
type listNumbering struct {
	d        core.View
	classMap *object.Dictionary
	memo     map[*object.Dictionary]string
}

func newListNumbering(d core.View, cat *object.Dictionary) *listNumbering {
	ln := &listNumbering{d: d, memo: map[*object.Dictionary]string{}}
	if root := d.ResolveDict(cat.Get("StructTreeRoot")); root != nil {
		ln.classMap = d.ResolveDict(root.Get("ClassMap"))
	}
	return ln
}

// of returns elem's ListNumbering.
func (ln *listNumbering) of(elem *object.Dictionary) string {
	d := ln.d
	answer := "None"
	var path []*object.Dictionary
	onPath := map[*object.Dictionary]bool{}
	for e := elem; e != nil && !onPath[e]; e = d.ResolveDict(e.Get("P")) {
		if v, ok := ln.memo[e]; ok {
			answer = v
			break
		}
		d.Charge(1)
		onPath[e] = true
		path = append(path, e)
		if v, ok := ln.own(e); ok {
			answer = v
			break
		}
	}
	for _, e := range path {
		ln.memo[e] = answer
	}
	return answer
}

// own is the ListNumbering an element gives itself, through /A or /C.
func (ln *listNumbering) own(e *object.Dictionary) (string, bool) {
	return firstAttrName(ln.d, ownedAttrs(ln.d, e, ln.classMap, "List"), "ListNumbering")
}

// checkUA2FormOneWidget is ISO 14289-2 8.10.1: a Form structure element
// encloses at most one widget annotation (veraPDF 8.10.1-t02,
// SEForm.widgetAnnotsCount: the element's own OBJR children whose object is a
// /Widget annotation). checkUAFormOneWidget is PDF/UA-1's.
func checkUA2FormOneWidget(d core.View, cat *object.Dictionary) []Violation {
	return formWidgets(d, cat, "8.10.1", false)
}

// checkUAFormOneWidget is ISO 14289-1 7.18.4: a Form element without a Role
// attribute (ISO 32000-1 Table 348) has one child, the object reference to
// its widget (veraPDF 7.18.4-2). It is reported, as PDF/UA-2's is, when the
// element encloses more than one widget.
func checkUAFormOneWidget(d core.View, cat *object.Dictionary) []Violation {
	return formWidgets(d, cat, "7.18.4", true)
}

// formWidgets reports each Form element enclosing more than one widget
// annotation, under clause; one with a Role attribute is exempt when
// roleExempts.
func formWidgets(d core.View, cat *object.Dictionary, clause string, roleExempts bool) []Violation {
	nodes := structTree(d, cat)
	widgets := map[int]int{} // Form index -> its widget OBJR children
	for _, n := range nodes {
		if n.HasS || n.Parent < 0 || !isStdType(nodes[n.Parent], "Form") {
			continue
		}
		if t, _ := d.ResolveName(n.Elem.Get("Type")); t != "OBJR" {
			continue
		}
		if obj := d.ResolveDict(n.Elem.Get("Obj")); obj != nil {
			if st, _ := d.ResolveName(obj.Get("Subtype")); st == "Widget" {
				widgets[n.Parent]++
			}
		}
	}
	var v []Violation
	for i, n := range nodes {
		if roleExempts && hasRoleAttribute(d, n.Elem) {
			continue
		}
		if c := widgets[i]; c > 1 {
			v = append(v, Violation{Clause: clause, Message: "a Form structure element encloses " + strconv.Itoa(c) + " widget annotations; it may enclose at most one", Object: anchorOf(d, n.Elem)})
		}
	}
	return v
}

// hasRoleAttribute reports whether a structure element's attributes, a
// dictionary or an array of them (with revision numbers among them), carry
// /Role (ISO 32000-1 Table 348, the PrintField attributes).
func hasRoleAttribute(d core.View, elem *object.Dictionary) bool {
	attrs := d.Resolve(elem.Get("A"))
	if one, ok := attrs.(*object.Dictionary); ok {
		attrs = object.Array{one}
	}
	arr, _ := attrs.(object.Array)
	for _, a := range arr {
		if ad := d.ResolveDict(a); ad != nil && ad.Get("Role") != nil {
			return true
		}
	}
	return false
}

// pdfua2TextStringKeys are the entries ISO 32000-2 types as text strings
// meant for a reader: structure elements' title, alternate and actual text,
// expansion and language; annotations' and form fields' descriptions and
// names; the document information entries; file specifications' description
// and name; outline titles.
var pdfua2TextStringKeys = map[object.Name]bool{
	"T": true, "TU": true, "TM": true, "Alt": true, "ActualText": true, "E": true, "Lang": true,
	"Contents": true, "Title": true, "Author": true, "Subject": true, "Keywords": true,
	"Creator": true, "Producer": true, "Desc": true, "UF": true,
}

// checkUA2TextStringPUA: a text string meant for a reader shall not use the
// Unicode Private Use Area (ISO 14289-2 8.6; veraPDF 8.6-t01: a structure
// element's /T and /Lang holding U+F00C). A code point there means nothing
// to anyone but the font that drew it. Only a Unicode text string can hold
// one: PDFDocEncoding has none.
func checkUA2TextStringPUA(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	for _, r := range d.ReachableDicts() {
		for k, val := range r.Dict.All() {
			if !pdfua2TextStringKeys[k] {
				continue
			}
			// Ciphertext says nothing about the text, and StringValue says
			// when a string is that.
			s, why := d.StringValue(val)
			if why != core.ReasonOK || len(s.Value) < 2 || !(s.Value[0] == 0xFE && s.Value[1] == 0xFF || len(s.Value) >= 3 && s.Value[0] == 0xEF && s.Value[1] == 0xBB && s.Value[2] == 0xBF) {
				continue
			}
			// Alternate and actual text have a rule of their own (8.4.3;
			// veraPDF 8.4.3-2, -3).
			clause := "8.6"
			if k == "Alt" || k == "ActualText" {
				clause = "8.4.3"
			}
			for _, c := range core.DecodePDFTextString(s.Value) {
				if c >= 0xE000 && c <= 0xF8FF || c >= 0xF0000 && c <= 0xFFFFD || c >= 0x100000 && c <= 0x10FFFD {
					v = append(v, Violation{Clause: clause, Object: r.ObjNum,
						Message: fmt.Sprintf("the text string %s uses the Unicode Private Use Area (U+%04X)", k, c)})
					break
				}
			}
		}
	}
	return v
}
