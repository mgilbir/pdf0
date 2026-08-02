package pdfua

import (
	"fmt"
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

// standardStructType resolves a structure element's type through /RoleMap to a
// standard type, or returns the element's own /S (which the role-map check
// flags if non-standard).
func standardStructType(d core.View, elem *object.Dictionary, roleMap *object.Dictionary) object.Name {
	s, _ := elem.Get("S").(object.Name)
	t, _, _ := resolveRoleMapChain(d, s, roleMap)
	return t
}

// resolveRoleMapChain follows the /RoleMap mapping from a structure type until
// it reaches a standard type. ISO 32000-1 14.7.3 (Table 323, /RoleMap) maps a
// type to "the standard structure type" it is equivalent to, and a role map may
// reach one through intermediate custom types: MyPara -> Para -> P is a legal
// two-step chain, and stopping after a single hop declared MyPara unmapped —
// which fired "structure type /MyPara is neither standard nor mapped in
// /RoleMap" and then, because every dependent check saw the raw type instead of
// P, a spray of 7.2 nesting findings on a conformant file.
//
// The walk is bounded twice over. A seen-set ends a cyclic map (which
// checkUARoleMapIntegrity reports separately) rather than looping, and the total
// hops are capped by the same /RoleMap step budget that check uses
// (WithMaxRoleMapSteps) rather than a second knob of its own.
//
// It returns the standard type reached (or the input type when none is), whether
// one was reached, and whether the walk ran to completion. A budget trip leaves
// the answer unknown, so a caller must not report "neither standard nor mapped"
// on that basis — the rule the package follows for every truncated structure.
func resolveRoleMapChain(d core.View, s object.Name, roleMap *object.Dictionary) (std object.Name, mapped, complete bool) {
	if standardStructTypes[s] || roleMap == nil || s == "" {
		return s, standardStructTypes[s], true
	}
	budget := d.Limits.RoleMapSteps
	// The first hop needs no seen-set: "already standard" and "one hop to a
	// standard type" are the shapes essentially every file has, and this runs
	// once per structure element, so it must not allocate for them.
	if budget < 1 {
		noteRoleMapChainLimit(d)
		return s, false, false
	}
	next, ok := d.Resolve(roleMap.Get(s)).(object.Name)
	if !ok || next == "" || next == s {
		return s, false, true
	}
	if standardStructTypes[next] {
		return next, true, true
	}
	// A genuine chain: now a seen-set earns its allocation.
	seen := map[object.Name]bool{s: true, next: true}
	cur := next
	for steps := 1; steps < budget; steps++ {
		next, ok := d.Resolve(roleMap.Get(cur)).(object.Name)
		if !ok || next == "" || seen[next] {
			return s, false, true // the chain ends, or closes on itself
		}
		if standardStructTypes[next] {
			return next, true, true
		}
		seen[next] = true
		cur = next
	}
	noteRoleMapChainLimit(d)
	return s, false, false
}

func noteRoleMapChainLimit(d core.View) {
	d.Note(core.GuardRoleMapWork, fmt.Sprintf(
		"following one /RoleMap chain to a standard structure type cost more than %s steps; the type could not be resolved",
		core.LimitBound(int64(d.Limits.RoleMapSteps), core.DefaultMaxRoleMapSteps)), 0)
}

// checkUAStructNesting enforces the structure-element parent/child constraints
// (tables, lists, table of contents) from the PDF/UA profile.
func checkUAStructNesting(d core.View, cat *object.Dictionary) []UAViolation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))

	var v []UAViolation
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
			v = append(v, UAViolation{"7.2", "<" + string(t) + "> element must be contained in a " + orList(parents) + " element, not <" + string(parentType) + ">", 0})
		}

		// Child constraint: check each structure-element child's type.
		if allowed, ok := uaAllowedChildren[t]; ok {
			for _, ct := range childStructTypes(d, elem, roleMap) {
				if !allowed[ct] {
					v = append(v, UAViolation{"7.2", "<" + string(t) + "> element must not contain a <" + string(ct) + "> element", 0})
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

// structKids returns the /K children of an element as a slice of objects.
func structKids(d core.View, elem *object.Dictionary) []object.Object {
	k := elem.Get("K")
	if k == nil {
		return nil
	}
	if arr, ok := d.Resolve(k).(object.Array); ok {
		return []object.Object(arr)
	}
	return []object.Object{k}
}

// checkUATableListStructure enforces the well-formedness rules for Table, List
// (L) and table-of-contents (TOC) containers that go beyond simple parent/child
// typing (UA profile / ISO 32000-1 14.8.4.3): at most one Caption/THead/TFoot,
// a THead or TFoot requires a TBody, and a Caption must sit in the permitted
// position (first-or-last for a Table, first for a List or TOC).
func checkUATableListStructure(d core.View, cat *object.Dictionary) []UAViolation {
	var v []UAViolation
	for _, n := range structTree(d, cat) {
		kids := n.childTypes
		switch n.stdType {
		case "Table":
			v = append(v, tableStructErrors(kids)...)
		case "L":
			if c := countName(kids, "Caption"); c > 1 {
				v = append(v, UAViolation{"7.2", "list (L) has more than one Caption", 0})
			} else if c == 1 && firstIndexName(kids, "Caption") != 0 {
				v = append(v, UAViolation{"7.2", "list (L) Caption must be the first child", 0})
			}
		case "TOC":
			if c := countName(kids, "Caption"); c > 1 {
				v = append(v, UAViolation{"7.2", "table of contents (TOC) has more than one Caption", 0})
			} else if c == 1 && firstIndexName(kids, "Caption") != 0 {
				v = append(v, UAViolation{"7.2", "table of contents (TOC) Caption must be the first child", 0})
			}
		}
	}
	return v
}

// tableStructErrors reports the Table-container well-formedness violations for a
// table's ordered child-type list.
func tableStructErrors(kids []object.Name) []UAViolation {
	var v []UAViolation
	captions := countName(kids, "Caption")
	theads := countName(kids, "THead")
	tfoots := countName(kids, "TFoot")
	tbodies := countName(kids, "TBody")
	if captions > 1 {
		v = append(v, UAViolation{"7.2", "table has more than one Caption", 0})
	}
	if theads > 1 {
		v = append(v, UAViolation{"7.2", "table has more than one THead", 0})
	}
	if tfoots > 1 {
		v = append(v, UAViolation{"7.2", "table has more than one TFoot", 0})
	}
	if (theads > 0 || tfoots > 0) && tbodies == 0 {
		v = append(v, UAViolation{"7.2", "table has a THead or TFoot but no TBody", 0})
	}
	if captions == 1 {
		i := firstIndexName(kids, "Caption")
		if i != 0 && i != len(kids)-1 {
			v = append(v, UAViolation{"7.2", "table Caption must be the first or last child", 0})
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

// structNode is one structure-tree dict node in the flattened pre-order model
// built by structTree. It carries the fields the per-check walks need so they
// can iterate a cached list instead of each re-descending the tree — a large
// win on documents with hundreds of thousands of structure elements.
type structNode struct {
	elem       *object.Dictionary // resolved element dictionary
	objNum     int                // object number if reached via an indirect ref, else -1
	rawS       object.Name        // elem's /S as written (before /RoleMap resolution)
	hasS       bool               // whether /S is present and a name
	stdType    object.Name        // /RoleMap-resolved standard type
	childTypes []object.Name      // resolved standard types of the /S children, in order
}

// structTree returns the document's structure tree flattened into a pre-order
// list of dict nodes, computed once per validation run and memoized in the
// validation cache. The traversal matches the historical per-check walk: every
// dict reachable through /K is visited (indirect refs deduped for cycle safety),
// arrays are descended transparently, and both /S and non-/S dicts are recorded.
func structTree(d core.View, cat *object.Dictionary) []structNode {
	if c := uaMemo(d); true && c.structTreeValid {
		return c.structTree
	}
	nodes := buildStructTree(d, cat)
	if c := uaMemo(d); true {
		c.structTree = nodes
		c.structTreeValid = true
	}
	return nodes
}

func buildStructTree(d core.View, cat *object.Dictionary) []structNode {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	var nodes []structNode
	seen := map[int]bool{}
	var walk func(node object.Object)
	walk = func(node object.Object) {
		objNum := -1
		if ref, ok := node.(object.IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
			objNum = ref.Number
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(object.Array); ok {
				for _, kid := range arr {
					walk(kid)
				}
			}
			return
		}
		rawS, hasS := elem.Get("S").(object.Name)
		kids := structKids(d, elem)
		var childTypes []object.Name
		for _, kid := range kids {
			child := d.ResolveDict(kid)
			if child == nil {
				continue
			}
			if _, ok := child.Get("S").(object.Name); !ok {
				continue
			}
			childTypes = append(childTypes, standardStructType(d, child, roleMap))
		}
		nodes = append(nodes, structNode{
			elem:       elem,
			objNum:     objNum,
			rawS:       rawS,
			hasS:       hasS,
			stdType:    standardStructType(d, elem, roleMap),
			childTypes: childTypes,
		})
		for _, kid := range kids {
			walk(kid)
		}
	}
	walk(root.Get("K"))
	return nodes
}

// walkStructElems invokes fn for every structure element (with an /S type) in
// the tree, passing its role-map-resolved standard type.
func walkStructElems(d core.View, cat *object.Dictionary, fn func(elem *object.Dictionary, stdType object.Name)) {
	for _, n := range structTree(d, cat) {
		if n.hasS {
			fn(n.elem, n.stdType)
		}
	}
}

// checkUAHeaderVersion: PDF/UA-1 is defined against PDF 1.7, so the header must
// declare a 1.n version.
func checkUAHeaderVersion(d core.View) []UAViolation {
	if len(d.Version) >= 2 && d.Version[0] == '1' && d.Version[1] == '.' {
		return nil
	}
	return []UAViolation{{"6.1", "PDF/UA-1 requires a PDF 1.x header, got " + d.Version, 0}}
}

// checkUASuspects: a MarkInfo /Suspects value of true means the tagging may be
// unreliable and is not permitted.
func checkUASuspects(d core.View, cat *object.Dictionary) []UAViolation {
	if mark := d.ResolveDict(cat.Get("MarkInfo")); mark != nil && d.IsTrue(mark.Get("Suspects")) {
		return []UAViolation{{"7.1", "/MarkInfo /Suspects must not be true", 0}}
	}
	return nil
}

// checkUAStrongWeak: a document must be either strongly structured (H1–H6) or
// weakly structured (H), not both (7.4.4).
func checkUAStrongWeak(d core.View, cat *object.Dictionary) []UAViolation {
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
		return []UAViolation{{"7.4.4", "document mixes <H> and <H1>–<H6> headings; it must be either strongly or weakly structured", 0}}
	}
	return nil
}

// checkUANotes: every Note structure element must carry a unique /ID (7.9).
func checkUANotes(d core.View, cat *object.Dictionary) []UAViolation {
	var v []UAViolation
	ids := map[string]bool{}
	walkStructElems(d, cat, func(elem *object.Dictionary, t object.Name) {
		if t != "Note" {
			return
		}
		id, _ := d.Resolve(elem.Get("ID")).(object.String)
		if len(id.Value) == 0 {
			v = append(v, UAViolation{"7.9", "<Note> structure element has no /ID", 0})
			return
		}
		if ids[string(id.Value)] {
			v = append(v, UAViolation{"7.9", "<Note> structure elements share a non-unique /ID", 0})
		}
		ids[string(id.Value)] = true
	})
	return v
}
