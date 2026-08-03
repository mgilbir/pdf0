package core

import (
	"fmt"

	"github.com/mgilbir/pdf0/object"
)

// The logical structure tree (ISO 32000-1 14.7): the flattened element list,
// /RoleMap resolution and the standard type vocabulary.
//
// It lives here rather than in a validator because two validators need exactly
// the same walk. PDF/UA asks the tree about nesting, headings and Note
// identifiers; PDF/A Level A asks it about role-map validity and per-element
// language. A document with hundreds of thousands of structure elements should
// pay for that descent once, not once per validator, so the flattened form is
// memoized on the run.

// StandardStructTypes are the ISO 32000 standard structure types (Table 333/337).
//
// ISO 19005-1 is written against PDF Reference 1.4, whose table is a subset of
// this one (it predates THead/TBody/TFoot and the Ruby/Warichu family). The
// wider set is used at every level deliberately: treating a later standard type
// as standard can only withhold a "non-standard type" finding, never invent one,
// and a PDF/A-1 file using /TBody is not the violation the rule is about.
var StandardStructTypes = map[object.Name]bool{
	"Document": true, "Part": true, "Art": true, "Sect": true, "Div": true,
	"BlockQuote": true, "Caption": true, "TOC": true, "TOCI": true, "Index": true,
	"NonStruct": true, "Private": true, "P": true, "H": true, "H1": true, "H2": true,
	"H3": true, "H4": true, "H5": true, "H6": true, "L": true, "LI": true, "Lbl": true,
	"LBody": true, "Table": true, "TR": true, "TH": true, "TD": true, "THead": true,
	"TBody": true, "TFoot": true, "Span": true, "Quote": true, "Note": true,
	"Reference": true, "BibEntry": true, "Code": true, "Link": true, "Annot": true,
	"Ruby": true, "RB": true, "RT": true, "RP": true, "Warichu": true, "WT": true,
	"WP": true, "Figure": true, "Formula": true, "Form": true,
}

// StandardStructType resolves a structure element's type through /RoleMap to a
// standard type, or returns the element's own /S (which the role-map checks
// flag if non-standard).
func StandardStructType(d View, elem *object.Dictionary, roleMap *object.Dictionary) object.Name {
	s, _ := elem.Get("S").(object.Name)
	t, _, _ := ResolveRoleMapChain(d, s, roleMap)
	return t
}

// ResolveRoleMapChain follows the /RoleMap mapping from a structure type until
// it reaches a standard type. ISO 32000-1 14.7.3 (Table 323, /RoleMap) maps a
// type to "the standard structure type" it is equivalent to, and a role map may
// reach one through intermediate custom types: MyPara -> Para -> P is a legal
// two-step chain, and stopping after a single hop declared MyPara unmapped —
// which fired "structure type /MyPara is neither standard nor mapped in
// /RoleMap" and then, because every dependent check saw the raw type instead of
// P, a spray of 7.2 nesting findings on a conformant file.
//
// The walk is bounded twice over. A seen-set ends a cyclic map (which the
// role-map integrity checks report separately) rather than looping, and the
// total hops are capped by the same /RoleMap step budget those checks use
// (WithMaxRoleMapSteps) rather than a second knob of its own.
//
// It returns the standard type reached (or the input type when none is), whether
// one was reached, and whether the walk ran to completion. A budget trip leaves
// the answer unknown, so a caller must not report "neither standard nor mapped"
// on that basis — the rule every structure check follows for a truncated walk.
func ResolveRoleMapChain(d View, s object.Name, roleMap *object.Dictionary) (std object.Name, mapped, complete bool) {
	if StandardStructTypes[s] || roleMap == nil || s == "" {
		return s, StandardStructTypes[s], true
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
	if StandardStructTypes[next] {
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
		if StandardStructTypes[next] {
			return next, true, true
		}
		seen[next] = true
		cur = next
	}
	noteRoleMapChainLimit(d)
	return s, false, false
}

// RoleMapChainCycles reports whether following the /RoleMap from s revisits a
// type before reaching a standard one, and whether the walk ran to completion.
//
// It is separate from ResolveRoleMapChain because the two questions have
// different answers on the same input: a chain that closes on itself resolves to
// "no standard type" (ResolveRoleMapChain returns mapped=false) and *also* is a
// cycle, and the PDF/A structure-type rules report those as two distinct
// findings against two distinct clauses.
func RoleMapChainCycles(d View, s object.Name, roleMap *object.Dictionary) (cyclic, complete bool) {
	if roleMap == nil || s == "" {
		return false, true
	}
	budget := d.Limits.RoleMapSteps
	seen := map[object.Name]bool{s: true}
	cur := s
	for steps := 0; steps < budget; steps++ {
		next, ok := d.Resolve(roleMap.Get(cur)).(object.Name)
		if !ok || next == "" {
			return false, true // the chain simply ends
		}
		if seen[next] {
			return true, true
		}
		if StandardStructTypes[next] {
			return false, true // a standard type is the end of the chain
		}
		seen[next] = true
		cur = next
	}
	noteRoleMapChainLimit(d)
	return false, false
}

func noteRoleMapChainLimit(d View) {
	d.Note(GuardRoleMapWork, fmt.Sprintf(
		"following one /RoleMap chain to a standard structure type cost more than %s steps; the type could not be resolved",
		LimitBound(int64(d.Limits.RoleMapSteps), DefaultMaxRoleMapSteps)), 0)
}

// StructKids returns the /K children of an element as a slice of objects.
func StructKids(d View, elem *object.Dictionary) []object.Object {
	k := elem.Get("K")
	if k == nil {
		return nil
	}
	if arr, ok := d.Resolve(k).(object.Array); ok {
		return []object.Object(arr)
	}
	return []object.Object{k}
}

// StructNode is one structure-tree dict node in the flattened pre-order model
// built by StructTree. It carries the fields the per-check walks need so they
// can iterate a cached list instead of each re-descending the tree — a large
// win on documents with hundreds of thousands of structure elements.
type StructNode struct {
	Elem       *object.Dictionary // resolved element dictionary
	ObjNum     int                // object number if reached via an indirect ref, else -1
	RawS       object.Name        // elem's /S as written (before /RoleMap resolution)
	HasS       bool               // whether /S is present and a name
	StdType    object.Name        // /RoleMap-resolved standard type
	ChildTypes []object.Name      // resolved standard types of the /S children, in order
	Parent     int                // index of the parent node in the list, or -1 at the root
}

// structTreeSlot keys the flattened structure tree on the run.
type structTreeSlot struct{}

type structTreeMemo struct {
	nodes []StructNode
	valid bool
}

// StructTree returns the document's structure tree flattened into a pre-order
// list of dict nodes, computed once per validation run and memoized in the run
// state. Every dict reachable through /K is visited (indirect refs deduped for
// cycle safety), arrays are descended transparently, and both /S and non-/S
// dicts are recorded.
func StructTree(d View, cat *object.Dictionary) []StructNode {
	c := Slot[structTreeMemo](d.Run, structTreeSlot{})
	if c.valid {
		return c.nodes
	}
	nodes := buildStructTree(d, cat)
	c.nodes = nodes
	c.valid = true
	return nodes
}

func buildStructTree(d View, cat *object.Dictionary) []StructNode {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	var nodes []StructNode
	seen := map[int]bool{}
	var walk func(node object.Object, parent int)
	walk = func(node object.Object, parent int) {
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
					walk(kid, parent)
				}
			}
			return
		}
		rawS, hasS := elem.Get("S").(object.Name)
		kids := StructKids(d, elem)
		var childTypes []object.Name
		for _, kid := range kids {
			child := d.ResolveDict(kid)
			if child == nil {
				continue
			}
			if _, ok := child.Get("S").(object.Name); !ok {
				continue
			}
			childTypes = append(childTypes, StandardStructType(d, child, roleMap))
		}
		self := len(nodes)
		nodes = append(nodes, StructNode{
			Elem:       elem,
			ObjNum:     objNum,
			RawS:       rawS,
			HasS:       hasS,
			StdType:    StandardStructType(d, elem, roleMap),
			ChildTypes: childTypes,
			Parent:     parent,
		})
		for _, kid := range kids {
			walk(kid, self)
		}
	}
	walk(root.Get("K"), -1)
	return nodes
}

// WalkStructElems invokes fn for every structure element (with an /S type) in
// the tree, passing its role-map-resolved standard type.
func WalkStructElems(d View, cat *object.Dictionary, fn func(elem *object.Dictionary, stdType object.Name)) {
	for _, n := range StructTree(d, cat) {
		if n.HasS {
			fn(n.Elem, n.StdType)
		}
	}
}
