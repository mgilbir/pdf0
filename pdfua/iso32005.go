package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/iso32005"
	"github.com/mgilbir/pdf0/object"
)

// checkUA2Containment holds the structure tree to ISO/TS 32005:2023 Table 5
// (iso32005.Children), which ISO 14289-2 makes the containment rules of a
// PDF/UA-2 file. Each finding is named as veraPDF's PDF/UA-2 profile names
// the rule — "Table 5. Parent-Child", or "Table 5. Parent-content" for
// marked content where the row lists none — so its verdicts and pdf0's can
// be compared.
//
// Elements are judged by their resolved standard type, in the PDF 1.7 or
// PDF 2.0 namespace (a custom type role mapped to one is that type); an
// element of another namespace, or one that resolves to nothing, takes no
// part. The grouping elements Part, Div and NonStruct, whose rows defer to
// their descriptions (‡), are looked through: an element's parent is its
// nearest ancestor that is not one (semanticParents), as veraPDF reads it,
// and a grouping element is itself judged as its parent's child.
//
// An ø* child is permitted only where its parent is used as a grouping
// element: one with no marked content of its own. A Caption occurs at most
// once under any parent (ISO 32000-2 Table 372).
func checkUA2Containment(d core.View, cat *object.Dictionary) []Violation {
	if d.ResolveDict(cat.Get("StructTreeRoot")) == nil {
		return nil
	}
	nodes := structTree(d, cat)
	sp := semanticParents(d, nodes)
	var v []Violation
	report := func(clause, msg string, elem *object.Dictionary) {
		obj := 0
		if elem != nil {
			obj = anchorOf(d, elem)
		}
		v = append(v, Violation{Clause: "Table 5. " + clause, Message: "ISO/TS 32005 Table 5: " + msg, Object: obj})
	}

	// Each element's own marked content, read once: whether it is used as
	// a grouping element, and the "content" rule.
	content := make([]bool, len(nodes))
	for i, n := range nodes {
		d.Charge(1)
		if n.HasS {
			content[i] = hasContentItems(d, n.Elem)
		}
	}
	// counts[parent][type] is how many semantic children of each type a
	// parent has; -1 is the structure tree root.
	counts := map[int]map[object.Name]int{}

	for i, n := range nodes {
		d.Charge(1)
		t, ok := iso32005Type(n)
		if !ok {
			continue
		}
		parent, parentType := sp[i], object.Name("StructTreeRoot")
		if parent >= 0 {
			if parentType, ok = iso32005Type(nodes[parent]); !ok {
				continue
			}
		}
		row, ok := iso32005.Children[parentType]
		if !ok {
			continue
		}
		if counts[parent] == nil {
			counts[parent] = map[object.Name]int{}
		}
		counts[parent][t]++
		var parentElem *object.Dictionary
		if parent >= 0 {
			parentElem = nodes[parent].Elem
		}
		if iso32005.Unenforced[string(parentType)+"-"+string(t)] {
			continue
		}
		switch row[t] {
		case 0:
			report(string(parentType)+"-"+string(t), describe(parentType)+" shall not contain <"+string(t)+">", n.Elem)
		case iso32005.Grouping:
			if parent >= 0 && content[parent] {
				report(string(parentType)+"-"+string(t), describe(parentType)+", when used as a non-grouping element, shall not contain <"+string(t)+">", parentElem)
			}
		}
	}

	// Occurrences: at most one where the row says 0..1, and one Caption
	// under any parent; exactly one Document under the root.
	for parent, byType := range counts {
		parentType, parentElem := object.Name("StructTreeRoot"), (*object.Dictionary)(nil)
		if parent >= 0 {
			parentType, _ = iso32005Type(nodes[parent])
			parentElem = nodes[parent].Elem
		}
		row := iso32005.Children[parentType]
		for t, c := range byType {
			// "The number of captions cannot exceed 1" (ISO 32000-2 Table
			// 372), under any parent, wherever the table lets one in.
			atMostOne := row[t] == iso32005.AtMostOne || t == "Caption"
			if atMostOne && c > 1 {
				report(string(parentType)+"-"+string(t), describe(parentType)+" shall contain at most one <"+string(t)+">", parentElem)
			}
		}
	}
	if root := iso32005.Children["StructTreeRoot"]; root["Document"] == iso32005.ExactlyOne {
		if c := counts[-1]["Document"]; c != 1 {
			report("StructTreeRoot-Document", "StructTreeRoot shall contain exactly one <Document>", nil)
		}
	}

	// Marked content where the row lists no content item.
	for i, n := range nodes {
		if !content[i] {
			continue
		}
		t, ok := iso32005Type(n)
		if !ok {
			continue
		}
		if row, ok := iso32005.Children[t]; ok && row[iso32005.ContentItem] == 0 && !iso32005.AllDescribed(row) {
			report(string(t)+"-content", "<"+string(t)+"> shall not contain content items", n.Elem)
		}
	}
	if root := d.ResolveDict(cat.Get("StructTreeRoot")); root != nil && hasContentItems(d, root) {
		report("StructTreeRoot-content", "StructTreeRoot shall not contain content items", nil)
	}
	return v
}

// iso32005Type is the Table 5 row a node's element is judged by: its
// resolved standard type in the PDF 1.7 or PDF 2.0 namespace, with H1, H2
// and so on as Hn. The second result is false for anything else.
func iso32005Type(n structNode) (object.Name, bool) {
	if !n.HasS || !n.Mapped || (n.NS != core.NSPDF17 && n.NS != core.NSPDF20) {
		return "", false
	}
	if isNumberedHeading(n.StdType) {
		return "Hn", true
	}
	if _, ok := iso32005.Children[n.StdType]; !ok {
		return "", false
	}
	return n.StdType, true
}

// isNumberedHeading reports whether t is H1, H2, and so on.
func isNumberedHeading(t object.Name) bool {
	if len(t) < 2 || t[0] != 'H' || t[1] < '1' || t[1] > '9' {
		return false
	}
	for _, c := range t[2:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// describe is a parent type as the rule's message names it.
func describe(t object.Name) string {
	if t == "StructTreeRoot" {
		return "StructTreeRoot"
	}
	return "<" + string(t) + ">"
}

// hasContentItems reports whether a structure element (or the structure
// tree root) has marked content among its own kids: an MCID, or a marked-
// content reference dictionary.
func hasContentItems(d core.View, elem *object.Dictionary) bool {
	k := d.Resolve(elem.Get("K"))
	kids, ok := k.(object.Array)
	if !ok {
		kids = object.Array{k}
	}
	for _, kid := range kids {
		d.Charge(1)
		switch kv := d.Resolve(kid).(type) {
		case object.Integer:
			return true
		case *object.Dictionary:
			if t, _ := d.ResolveName(kv.Get("Type")); t == "MCR" {
				return true
			}
		}
	}
	return false
}
