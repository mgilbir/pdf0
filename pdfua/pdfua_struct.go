package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file owns the structure-tree side of PDF/UA validation: element
// parent/child nesting (ISO 14289-1 7.2), Table/L/TOC container
// well-formedness (ISO 32000-1 14.8.4.3), heading strength (7.4.4), Note
// identifiers (7.9), /Suspects, and the UA-1 header version. Types are
// compared only after /RoleMap resolution, and the tree is flattened once per
// run into a cached pre-order list so each check iterates that rather than
// re-descending the tree.

// Structure-element nesting constraints from the veraPDF PDF/UA-1 profile
// (clause 7.2). allowedParents maps a child type to the parent types that may
// contain it; allowedChildren maps a parent type to the only child types it may
// contain. Types are compared after resolving through the structure tree's
// /RoleMap.
var uaAllowedParents = map[object.Name][]object.Name{
	"LBody": {"LI"},
	"LI":    {"L"},
	"TBody": {"Table"},
	"THead": {"Table"},
	"TFoot": {"Table"},
	"TD":    {"TR"},
	"TH":    {"TR"},
	"TR":    {"Table", "THead", "TBody", "TFoot"},
	"TOCI":  {"TOC"},
}

var uaAllowedChildren = map[object.Name]map[object.Name]bool{
	"LI":    {"Lbl": true, "LBody": true},
	"L":     {"L": true, "LI": true, "Caption": true},
	"TBody": {"TR": true},
	"THead": {"TR": true},
	"TFoot": {"TR": true},
	"TR":    {"TH": true, "TD": true},
	"Table": {"TR": true, "THead": true, "TBody": true, "TFoot": true, "Caption": true},
	"TOC":   {"TOC": true, "TOCI": true, "Caption": true},
}

// The structure tree itself — the flattened element list, /RoleMap resolution
// and the standard type vocabulary — lives in internal/core, because PDF/A
// Level A asks the same tree the same questions. These are the names this
// package reads it under.
type structNode = core.StructNode

var standardStructTypes = core.StandardStructTypes

func standardStructType(d core.View, elem *object.Dictionary, roleMap *object.Dictionary) object.Name {
	return core.StandardStructType(d, elem, roleMap)
}

func structKids(d core.View, elem *object.Dictionary) []object.Object {
	return core.StructKids(d, elem)
}

func structTree(d core.View, cat *object.Dictionary) []structNode {
	return core.StructTree(d, cat)
}

func walkStructElems(d core.View, cat *object.Dictionary, fn func(elem *object.Dictionary, stdType object.Name)) {
	core.WalkStructElems(d, cat, fn)
}

// checkUAStructNesting enforces the structure-element parent/child constraints
// (tables, lists, table of contents) from the PDF/UA profile, on resolved
// types. It reads the flattened tree rather than descending /K itself, so the
// types it compares are the ones every other check compares.
//
// Only structure elements reached through structure elements take part: the
// descent stops at a dictionary with no /S (an MCR or OBJR), as it always has.
func checkUAStructNesting(d core.View, cat *object.Dictionary) []Violation {
	nodes := structTree(d, cat)
	var v []Violation
	// inTree[i]: node i and all its ancestors are structure elements.
	inTree := make([]bool, len(nodes))
	for i, n := range nodes {
		if !n.HasS || (n.Parent >= 0 && !inTree[n.Parent]) {
			continue
		}
		inTree[i] = true
		t := n.StdType
		var parentType object.Name
		if n.Parent >= 0 {
			parentType = nodes[n.Parent].StdType
		}

		// Parent constraint.
		if parents, ok := uaAllowedParents[t]; ok && !containsName(parents, parentType) {
			v = append(v, Violation{Clause: "7.2", Message: "<" + string(t) + "> element must be contained in a " + orList(parents) + " element, not <" + string(parentType) + ">", Object: 0})
		}

		// Child constraint: check each structure-element child's type.
		if allowed, ok := uaAllowedChildren[t]; ok {
			for _, ct := range n.ChildTypes {
				if !allowed[ct] {
					v = append(v, Violation{Clause: "7.2", Message: "<" + string(t) + "> element must not contain a <" + string(ct) + "> element", Object: 0})
				}
			}
		}
	}
	return v
}

// checkUATableListStructure enforces the well-formedness rules for Table, List
// (L) and table-of-contents (TOC) containers that go beyond simple parent/child
// typing (UA profile / ISO 32000-1 14.8.4.3): at most one Caption/THead/TFoot,
// a THead or TFoot requires a TBody, and a Caption must sit in the permitted
// position (first-or-last for a Table, first for a List or TOC).
func checkUATableListStructure(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	for _, n := range structTree(d, cat) {
		kids := n.ChildTypes
		switch n.StdType {
		case "Table":
			v = append(v, tableStructErrors(kids)...)
		case "L":
			if c := countName(kids, "Caption"); c > 1 {
				v = append(v, Violation{Clause: "7.2", Message: "list (L) has more than one Caption", Object: 0})
			} else if c == 1 && firstIndexName(kids, "Caption") != 0 {
				v = append(v, Violation{Clause: "7.2", Message: "list (L) Caption must be the first child", Object: 0})
			}
		case "TOC":
			if c := countName(kids, "Caption"); c > 1 {
				v = append(v, Violation{Clause: "7.2", Message: "table of contents (TOC) has more than one Caption", Object: 0})
			} else if c == 1 && firstIndexName(kids, "Caption") != 0 {
				v = append(v, Violation{Clause: "7.2", Message: "table of contents (TOC) Caption must be the first child", Object: 0})
			}
		}
	}
	return v
}

// tableStructErrors reports the Table-container well-formedness violations for a
// table's ordered child-type list.
func tableStructErrors(kids []object.Name) []Violation {
	var v []Violation
	captions := countName(kids, "Caption")
	theads := countName(kids, "THead")
	tfoots := countName(kids, "TFoot")
	tbodies := countName(kids, "TBody")
	if captions > 1 {
		v = append(v, Violation{Clause: "7.2", Message: "table has more than one Caption", Object: 0})
	}
	if theads > 1 {
		v = append(v, Violation{Clause: "7.2", Message: "table has more than one THead", Object: 0})
	}
	if tfoots > 1 {
		v = append(v, Violation{Clause: "7.2", Message: "table has more than one TFoot", Object: 0})
	}
	if (theads > 0 || tfoots > 0) && tbodies == 0 {
		v = append(v, Violation{Clause: "7.2", Message: "table has a THead or TFoot but no TBody", Object: 0})
	}
	if captions == 1 {
		i := firstIndexName(kids, "Caption")
		if i != 0 && i != len(kids)-1 {
			v = append(v, Violation{Clause: "7.2", Message: "table Caption must be the first or last child", Object: 0})
		}
	}
	return v
}

func countName(names []object.Name, want object.Name) int {
	n := 0
	for _, x := range names {
		if x == want {
			n++
		}
	}
	return n
}

func firstIndexName(names []object.Name, want object.Name) int {
	for i, x := range names {
		if x == want {
			return i
		}
	}
	return -1
}

func containsName(names []object.Name, n object.Name) bool {
	for _, x := range names {
		if x == n {
			return true
		}
	}
	return false
}

func orList(names []object.Name) string {
	s := ""
	for i, n := range names {
		if i > 0 {
			if i == len(names)-1 {
				s += " or "
			} else {
				s += ", "
			}
		}
		s += "<" + string(n) + ">"
	}
	return s
}

// checkUAHeaderVersion: PDF/UA-1 is defined against PDF 1.7, so the header must
// declare a 1.n version.
func checkUAHeaderVersion(d core.View) []Violation {
	if len(d.Version) >= 2 && d.Version[0] == '1' && d.Version[1] == '.' {
		return nil
	}
	return []Violation{{Clause: "6.1", Message: "PDF/UA-1 requires a PDF 1.x header, got " + d.Version, Object: 0}}
}

// checkUASuspects: a MarkInfo /Suspects value of true means the tagging may be
// unreliable and is not permitted.
func checkUASuspects(d core.View, cat *object.Dictionary) []Violation {
	if mark := d.ResolveDict(cat.Get("MarkInfo")); mark != nil && d.IsTrue(mark.Get("Suspects")) {
		return []Violation{{Clause: "7.1", Message: "/MarkInfo /Suspects must not be true", Object: 0}}
	}
	return nil
}

// checkUAStrongWeak: a document must be either strongly structured (H1–H6) or
// weakly structured (H), not both (7.4.4).
func checkUAStrongWeak(d core.View, cat *object.Dictionary) []Violation {
	var hasH, hasHn bool
	walkStructElems(d, cat, func(_ *object.Dictionary, t object.Name) {
		switch {
		case t == "H":
			hasH = true
		case len(t) == 2 && t[0] == 'H' && t[1] >= '1' && t[1] <= '6':
			hasHn = true
		}
	})
	if hasH && hasHn {
		return []Violation{{Clause: "7.4.4", Message: "document mixes <H> and <H1>–<H6> headings; it must be either strongly or weakly structured", Object: 0}}
	}
	return nil
}

// checkUANotes: every Note structure element must carry a unique /ID (7.9).
func checkUANotes(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	ids := map[string]bool{}
	walkStructElems(d, cat, func(elem *object.Dictionary, t object.Name) {
		if t != "Note" {
			return
		}
		id, r := d.StringValue(elem.Get("ID"))
		if !d.NonEmptyStringOrLocked(elem.Get("ID")) {
			v = append(v, Violation{Clause: "7.9", Message: "<Note> structure element has no /ID", Object: 0})
			return
		}
		if r != core.ReasonOK {
			return // ciphertext: present, but uniqueness cannot be judged
		}
		if ids[string(id.Value)] {
			v = append(v, Violation{Clause: "7.9", Message: "<Note> structure elements share a non-unique /ID", Object: 0})
		}
		ids[string(id.Value)] = true
	})
	return v
}

// checkUA2NamespaceRoleMaps enforces ISO 14289-2 8.2.4 (veraPDF test 3):
// within an explicitly provided namespace, a structure type shall not be role
// mapped to another type in the same namespace — directly, or along a chain
// that leaves the namespace and comes back to it. An element with no /NS is in
// the default namespace, whose /RoleMap maps within it by design, and is not
// held to this. Reported once per written type and namespace.
//
// It reads RawS: the rule is about the type as written.
func checkUA2NamespaceRoleMaps(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	type key struct {
		t  object.Name
		ns *object.Dictionary
	}
	seen := map[key]bool{}
	for _, n := range structTree(d, cat) {
		if !n.SameNSMap {
			continue
		}
		k := key{n.RawS, d.ResolveDict(n.Elem.Get("NS"))}
		if seen[k] {
			continue
		}
		seen[k] = true
		v = append(v, Violation{Clause: "8.2.4", Message: "structure type /" + string(n.RawS) + " is role mapped to a structure type in its own namespace"})
	}
	return v
}

// checkUA2MathParent enforces ISO 14289-2 8.2.5.29 (veraPDF: hasParentFormulaOrMathML):
// the MathML math element shall occur only as a child of a Formula structure
// element — or inside other MathML. Both sides are resolved types: a custom
// /Math role-mapped to MathML's math is a math element.
func checkUA2MathParent(d core.View, cat *object.Dictionary) []Violation {
	nodes := structTree(d, cat)
	var v []Violation
	for _, n := range nodes {
		if !n.HasS || n.NS != core.NSMathML || n.StdType != "math" {
			continue
		}
		if n.Parent >= 0 {
			if p := nodes[n.Parent]; p.StdType == "Formula" || p.NS == core.NSMathML {
				continue
			}
		}
		v = append(v, Violation{Clause: "8.2.5.29", Message: "a MathML math element is not a child of a Formula structure element"})
	}
	return v
}
