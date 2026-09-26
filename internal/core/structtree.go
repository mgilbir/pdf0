package core

import (
	"fmt"

	"github.com/mgilbir/pdf0/internal/checked"
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

// The standard structure namespaces (ISO 32000-2 14.8.6.1) and the one other
// namespace PDF 2.0 names (14.8.6.3).
const (
	// NSPDF17 is the standard structure namespace for PDF 1.7, and the default
	// one: an element with no /NS is in it, in a PDF 2.0 file as in any other.
	NSPDF17 = "http://iso.org/pdf/ssn"
	// NSPDF20 is the standard structure namespace for PDF 2.0.
	NSPDF20 = "http://iso.org/pdf2/ssn"
	// NSMathML is MathML 3.0, whose element types need no role map.
	NSMathML = "http://www.w3.org/1998/Math/MathML"
)

// pdf20OnlyStructTypes are the types only the PDF 2.0 standard structure
// namespace defines (ISO 32000-2 Annex M), apart from Hn for n > 6.
var pdf20OnlyStructTypes = map[object.Name]bool{
	"DocumentFragment": true, "Aside": true, "Title": true, "FENote": true,
	"Sub": true, "Em": true, "Strong": true, "Artifact": true,
}

// pdf17OnlyStructTypes are the PDF 1.7 standard types the PDF 2.0 namespace
// does not define (Annex M).
var pdf17OnlyStructTypes = map[object.Name]bool{
	"Art": true, "BlockQuote": true, "TOC": true, "TOCI": true, "Index": true,
	"Private": true, "Quote": true, "Note": true, "Reference": true,
	"BibEntry": true, "Code": true,
}

// IsPDF20OnlyStructType reports whether t exists only in the PDF 2.0 standard
// structure namespace: an Annex M type, or Hn with n > 6.
func IsPDF20OnlyStructType(t object.Name) bool {
	if pdf20OnlyStructTypes[t] {
		return true
	}
	n, ok := headingLevel(t)
	return ok && n > 6
}

// headingLevel reads Hn: an H followed by a decimal number with no leading
// zero.
func headingLevel(t object.Name) (int64, bool) {
	if len(t) < 2 || t[0] != 'H' || t[1] == '0' {
		return 0, false
	}
	n, digits, fits := checked.Decimal(t[1:])
	return n, fits && digits == len(t)-1
}

// isPDF20StandardStructType reports whether t is a standard type of the PDF
// 2.0 namespace: the PDF 1.7 set less the types Annex M removes, plus those it
// adds.
func isPDF20StandardStructType(t object.Name) bool {
	return (StandardStructTypes[t] && !pdf17OnlyStructTypes[t]) || IsPDF20OnlyStructType(t)
}

// StructType is a structure element's type once resolved: the standard type
// it is, or is role-mapped to, and the namespace that type belongs to.
type StructType struct {
	// Std is the standard type reached, or the element's own /S when none
	// was.
	Std object.Name
	// NS is the namespace Std belongs to: NSPDF17, NSPDF20 or NSMathML when
	// Mapped, otherwise the namespace the walk stopped in ("" when it had
	// no name).
	NS string
	// Mapped reports that a standard type (or a MathML one) was reached.
	Mapped bool
	// Complete reports that the walk ran to its end. A walk stopped by the
	// role-map budget leaves Mapped unknown, and no check may report "not
	// mapped" on its strength.
	Complete bool
	// SameNamespaceMap reports that an element naming its namespace
	// explicitly is role-mapped, directly or along the chain, to a type in
	// that same namespace — which PDF/UA-2 forbids (ISO 14289-2 8.2.4).
	SameNamespaceMap bool
}

// StandardStructType resolves a structure element's type to a standard type
// through its namespace and the role maps (see ResolveStructType), or returns
// the element's own /S, which the role-map checks flag if non-standard.
func StandardStructType(d View, elem *object.Dictionary, roleMap *object.Dictionary) object.Name {
	return ResolveStructType(d, elem, roleMap).Std
}

// IsStructElem reports whether dict is a structure element: it has a type
// (/S). An MCR or OBJR dictionary in /K has none.
func IsStructElem(d View, dict *object.Dictionary) bool {
	_, ok := d.ResolveName(dict.Get("S"))
	return ok
}

// ResolveStructType resolves the type of a structure element (ISO 32000-2
// 14.7.3, 14.7.4, 14.8.6.2).
//
// An element with no /NS is in the default namespace, the PDF 1.7 one, and a
// non-standard type there is mapped by roleMap, the structure tree root's
// /RoleMap, whose values are types in that same namespace. An element whose /NS
// names a namespace dictionary is mapped by that dictionary's /RoleMapNS,
// whose values are either a type in the default namespace (a name) or a type
// in another namespace ([name nsdict]). A type is reached when it is standard
// in the namespace it is in — the PDF 1.7 set, or the PDF 2.0 set, which
// drops eleven 1.7 types and adds nine of its own and Hn for every n — or when
// it is in the MathML namespace, which needs no role map (14.8.6.3).
//
// Before this, only the PDF 1.7 vocabulary and the root /RoleMap were known,
// so a PDF/UA-2 file tagging its title /Title in the PDF 2.0 namespace, as that
// standard expects, was told the type was "neither standard nor mapped".
//
// A standard type is not followed further even when a role map names it:
// the validators have always read the standard type as final, and the role-map
// integrity checks report a map that remaps one.
//
// The walk is bounded like ResolveRoleMapChain: a seen-set ends a cycle, and
// the hops are capped by the /RoleMap step budget, a trip on which leaves the
// answer incomplete.
func ResolveStructType(d View, elem *object.Dictionary, roleMap *object.Dictionary) StructType {
	s, _ := d.ResolveName(elem.Get("S"))
	return resolveStructType(d, s, d.ResolveDict(elem.Get("NS")), roleMap)
}

// structNSKey identifies a type within a namespace for the cycle check. A nil
// namespace dictionary is the default namespace.
type structNSKey struct {
	ns *object.Dictionary
	t  object.Name
}

func resolveStructType(d View, s object.Name, ns *object.Dictionary, roleMap *object.Dictionary) StructType {
	orig := s
	if s == "" {
		return StructType{Complete: true}
	}
	// own is the namespace an element names explicitly, for SameNamespaceMap.
	own := ""
	if ns != nil {
		own = structNamespaceName(d, ns)
	}
	sameNS := false
	budget := d.Limits.RoleMapSteps
	var seen map[structNSKey]bool // allocated only for a chain of two hops or more
	for hops := 0; ; hops++ {
		uri := structNamespaceName(d, ns)
		switch {
		case uri == NSPDF17 && StandardStructTypes[s],
			uri == NSPDF20 && isPDF20StandardStructType(s),
			uri == NSMathML:
			return StructType{Std: s, NS: uri, Mapped: true, Complete: true, SameNamespaceMap: sameNS}
		}
		unmapped := StructType{Std: orig, NS: uri, Complete: true, SameNamespaceMap: sameNS}

		// The role map this namespace's types are mapped by.
		m := roleMap
		if ns != nil {
			m = d.ResolveDict(ns.Get("RoleMapNS"))
		}
		if m == nil {
			return unmapped
		}
		if hops >= budget {
			noteRoleMapChainLimit(d)
			unmapped.Complete = false
			return unmapped
		}
		var next object.Name
		var nextNS *object.Dictionary
		switch v := d.Resolve(m.Get(s)).(type) {
		case object.Name:
			next = v // a type in the default namespace
		case object.Array:
			// Only a /RoleMapNS maps into another namespace; the root
			// /RoleMap's values are names in the default namespace.
			if ns == nil || len(v) != 2 {
				return unmapped
			}
			n, ok := d.Resolve(v[0]).(object.Name)
			target := d.ResolveDict(v[1])
			if !ok || target == nil {
				return unmapped
			}
			next, nextNS = n, target
		default:
			return unmapped
		}
		if own != "" && structNamespaceName(d, nextNS) == own {
			sameNS = true
			unmapped.SameNamespaceMap = true
		}
		if next == "" || (next == s && nextNS == ns) {
			return unmapped
		}
		if hops > 0 {
			if seen == nil {
				seen = map[structNSKey]bool{{ns, s}: true}
			}
			k := structNSKey{nextNS, next}
			if seen[k] {
				return unmapped // the chain closes on itself
			}
			seen[k] = true
		}
		s, ns = next, nextNS
	}
}

// structNamespaceName is the name (/NS) of a namespace dictionary; a nil
// dictionary is the default namespace, the PDF 1.7 one. A namespace
// dictionary with no name is none of the known ones and answers "".
func structNamespaceName(d View, ns *object.Dictionary) string {
	if ns == nil {
		return NSPDF17
	}
	if str, ok := d.Resolve(ns.Get("NS")).(object.String); ok {
		return DecodePDFTextString(str.Value)
	}
	return ""
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
//
// It is ResolveStructType for a type in the default namespace, which is what
// every element without /NS is in.
func ResolveRoleMapChain(d View, s object.Name, roleMap *object.Dictionary) (std object.Name, mapped, complete bool) {
	r := resolveStructType(d, s, nil, roleMap)
	return r.Std, r.Mapped, r.Complete
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
	Elem   *object.Dictionary // resolved element dictionary
	ObjNum int                // object number if reached via an indirect ref, else -1
	// RawS is elem's /S as written, before any role mapping. Only a check
	// about the role map itself — is this written type mapped at all? — has
	// a reason to read it; every other check compares StdType, or a custom
	// type mapped to a standard one escapes it (audit 2026-09-22 C80, C81).
	// internal/lint's TestStructureChecksReadTheResolvedType holds that.
	RawS       object.Name
	HasS       bool          // whether /S is present and a name
	StdType    object.Name   // the resolved standard type (ResolveStructType)
	NS         string        // the namespace StdType is in; see StructType.NS
	Mapped     bool          // a standard type was reached; see StructType
	Complete   bool          // the resolution ran to its end; see StructType
	SameNSMap  bool          // mapped into its own explicit namespace; see StructType
	ChildTypes []object.Name // resolved standard types of the /S children, in order
	Parent     int           // index of the parent node in the list, or -1 at the root
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
		rawS, hasS := d.ResolveName(elem.Get("S"))
		kids := StructKids(d, elem)
		var childTypes []object.Name
		for _, kid := range kids {
			child := d.ResolveDict(kid)
			if child == nil {
				continue
			}
			if _, ok := d.ResolveName(child.Get("S")); !ok {
				continue
			}
			childTypes = append(childTypes, StandardStructType(d, child, roleMap))
		}
		self := len(nodes)
		rt := ResolveStructType(d, elem, roleMap)
		nodes = append(nodes, StructNode{
			Elem:       elem,
			ObjNum:     objNum,
			RawS:       rawS,
			HasS:       hasS,
			StdType:    rt.Std,
			NS:         rt.NS,
			Mapped:     rt.Mapped,
			Complete:   rt.Complete,
			SameNSMap:  rt.SameNamespaceMap,
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
