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

func resolveRoleMapChain(d core.View, s object.Name, roleMap *object.Dictionary) (object.Name, bool, bool) {
	return core.ResolveRoleMapChain(d, s, roleMap)
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
// (tables, lists, table of contents) from the PDF/UA profile.
func checkUAStructNesting(d core.View, cat *object.Dictionary) []Violation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))

	var v []Violation
	seen := map[int]bool{}
	var walk func(node object.Object, parentType object.Name)
	walk = func(node object.Object, parentType object.Name) {
		if ref, ok := node.(object.IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(object.Array); ok {
				for _, kid := range arr {
					walk(kid, parentType)
				}
			}
			return
		}
		// Only structure elements (those with an /S type) participate.
		if _, hasS := elem.Get("S").(object.Name); !hasS {
			return
		}
		t := standardStructType(d, elem, roleMap)

		// Parent constraint.
		if parents, ok := uaAllowedParents[t]; ok && !containsName(parents, parentType) {
			v = append(v, Violation{"7.2", "<" + string(t) + "> element must be contained in a " + orList(parents) + " element, not <" + string(parentType) + ">", 0})
		}

		// Child constraint: check each structure-element child's type.
		if allowed, ok := uaAllowedChildren[t]; ok {
			for _, ct := range childStructTypes(d, elem, roleMap) {
				if !allowed[ct] {
					v = append(v, Violation{"7.2", "<" + string(t) + "> element must not contain a <" + string(ct) + "> element", 0})
				}
			}
		}

		for _, kid := range structKids(d, elem) {
			walk(kid, t)
		}
	}
	walk(root.Get("K"), "")
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
				v = append(v, Violation{"7.2", "list (L) has more than one Caption", 0})
			} else if c == 1 && firstIndexName(kids, "Caption") != 0 {
				v = append(v, Violation{"7.2", "list (L) Caption must be the first child", 0})
			}
		case "TOC":
			if c := countName(kids, "Caption"); c > 1 {
				v = append(v, Violation{"7.2", "table of contents (TOC) has more than one Caption", 0})
			} else if c == 1 && firstIndexName(kids, "Caption") != 0 {
				v = append(v, Violation{"7.2", "table of contents (TOC) Caption must be the first child", 0})
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
		v = append(v, Violation{"7.2", "table has more than one Caption", 0})
	}
	if theads > 1 {
		v = append(v, Violation{"7.2", "table has more than one THead", 0})
	}
	if tfoots > 1 {
		v = append(v, Violation{"7.2", "table has more than one TFoot", 0})
	}
	if (theads > 0 || tfoots > 0) && tbodies == 0 {
		v = append(v, Violation{"7.2", "table has a THead or TFoot but no TBody", 0})
	}
	if captions == 1 {
		i := firstIndexName(kids, "Caption")
		if i != 0 && i != len(kids)-1 {
			v = append(v, Violation{"7.2", "table Caption must be the first or last child", 0})
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

// childStructTypes returns the resolved standard types of an element's
// structure-element children (ignoring marked-content and object references).
func childStructTypes(d core.View, elem *object.Dictionary, roleMap *object.Dictionary) []object.Name {
	var out []object.Name
	for _, kid := range structKids(d, elem) {
		child := d.ResolveDict(kid)
		if child == nil {
			continue
		}
		if _, hasS := child.Get("S").(object.Name); !hasS {
			continue
		}
		out = append(out, standardStructType(d, child, roleMap))
	}
	return out
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
	return []Violation{{"6.1", "PDF/UA-1 requires a PDF 1.x header, got " + d.Version, 0}}
}

// checkUASuspects: a MarkInfo /Suspects value of true means the tagging may be
// unreliable and is not permitted.
func checkUASuspects(d core.View, cat *object.Dictionary) []Violation {
	if mark := d.ResolveDict(cat.Get("MarkInfo")); mark != nil && d.IsTrue(mark.Get("Suspects")) {
		return []Violation{{"7.1", "/MarkInfo /Suspects must not be true", 0}}
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
		return []Violation{{"7.4.4", "document mixes <H> and <H1>–<H6> headings; it must be either strongly or weakly structured", 0}}
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
		id, _ := d.Resolve(elem.Get("ID")).(object.String)
		if len(id.Value) == 0 {
			v = append(v, Violation{"7.9", "<Note> structure element has no /ID", 0})
			return
		}
		if ids[string(id.Value)] {
			v = append(v, Violation{"7.9", "<Note> structure elements share a non-unique /ID", 0})
		}
		ids[string(id.Value)] = true
	})
	return v
}
