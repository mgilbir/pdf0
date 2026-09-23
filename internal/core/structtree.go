package core

import (
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
	// Complete reports that the walk ran to its end, and no check may report
	// "not mapped" when it did not. Every walk now does: a run that cannot
	// afford one is stopped by its work meter rather than answered in part
	// (ResolveStructType).
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
// A seen-set ends a cycle, and every hop is charged to the run's work meter.
// Answers are memoised for the run (a Slot keyed by structTypeKey): a type is resolved once
// however many elements carry it, and a walk in the default namespace answers
// for every type it crosses, so the whole role map costs one walk. The per-call
// step budget this replaces bounded one chain and not the elements that each
// walked it: 2,000 elements tagged with the first type of a 100,000-long chain
// took ninety seconds without a trip (audit 2026-09-22 C41). The answer is
// therefore always Complete; a run that cannot afford the walk is stopped by
// the meter rather than handed a partial one.
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

// structTypeKey is a type to resolve: its name, the namespace it is in (nil
// for the default one) and the structure tree root's /RoleMap it is resolved
// against.
type structTypeKey struct {
	roleMap *object.Dictionary
	ns      *object.Dictionary
	t       object.Name
}

type structTypeSlot struct{}

func resolveStructType(d View, s object.Name, ns *object.Dictionary, roleMap *object.Dictionary) StructType {
	if s == "" {
		return StructType{Complete: true}
	}
	// A standard type, or one hop from one, is what essentially every element
	// is, and this runs once per element: those answer without the memo.
	if r, done := resolveStructTypeShort(d, s, ns, roleMap); done {
		return r
	}
	memo := Slot[map[structTypeKey]StructType](d.Run, structTypeSlot{})
	if *memo == nil {
		*memo = map[structTypeKey]StructType{}
	}
	key := structTypeKey{roleMap, ns, s}
	if r, ok := (*memo)[key]; ok {
		d.Charge(1)
		return r
	}
	r, path := resolveStructTypeWalk(d, s, ns, roleMap, *memo)
	(*memo)[key] = r
	// Every type a walk in the default namespace crossed has the same answer
	// — the same standard type, or unmapped as itself — since the walk from
	// it follows the same links. (SameNamespaceMap is about a namespace an
	// element names, which the default namespace is not.)
	for _, t := range path {
		pr := r
		if !r.Mapped {
			pr.Std = t
		}
		(*memo)[structTypeKey{roleMap, nil, t}] = pr
	}
	return r
}

// resolveStructTypeShort answers a type that is standard in its namespace, or
// one role-map hop in the default namespace from a standard type, or that no
// role map names, reporting false for anything longer.
func resolveStructTypeShort(d View, s object.Name, ns *object.Dictionary, roleMap *object.Dictionary) (StructType, bool) {
	uri := structNamespaceName(d, ns)
	switch {
	case uri == NSPDF17 && StandardStructTypes[s],
		uri == NSPDF20 && isPDF20StandardStructType(s),
		uri == NSMathML:
		return StructType{Std: s, NS: uri, Mapped: true, Complete: true}, true
	}
	if ns != nil {
		return StructType{}, false
	}
	if roleMap == nil {
		return StructType{Std: s, NS: uri, Complete: true}, true
	}
	next, ok := d.Resolve(roleMap.Get(s)).(object.Name)
	if !ok || next == "" || next == s {
		return StructType{Std: s, NS: uri, Complete: true}, true
	}
	if StandardStructTypes[next] {
		return StructType{Std: next, NS: NSPDF17, Mapped: true, Complete: true}, true
	}
	return StructType{}, false
}

// resolveStructTypeWalk is the walk behind resolveStructType, returning the
// answer and, for a walk that starts in the default namespace, the types it
// crossed there. An answer memoised for a type the walk reaches in the
// default namespace ends the walk.
func resolveStructTypeWalk(d View, s object.Name, ns *object.Dictionary, roleMap *object.Dictionary, memo map[structTypeKey]StructType) (StructType, []object.Name) {
	orig := s
	// own is the namespace an element names explicitly, for SameNamespaceMap.
	own := ""
	if ns != nil {
		own = structNamespaceName(d, ns)
	}
	defaultNS := ns == nil
	var path []object.Name
	sameNS := false
	var seen map[structNSKey]bool // allocated only for a chain of two hops or more
	for hops := 0; ; hops++ {
		d.Charge(1)
		if defaultNS && hops > 0 {
			if r, ok := memo[structTypeKey{roleMap, nil, s}]; ok {
				if !r.Mapped {
					r.Std = orig
				}
				return r, path
			}
			path = append(path, s)
		}
		uri := structNamespaceName(d, ns)
		switch {
		case uri == NSPDF17 && StandardStructTypes[s],
			uri == NSPDF20 && isPDF20StandardStructType(s),
			uri == NSMathML:
			if len(path) > 0 && path[len(path)-1] == s {
				path = path[:len(path)-1] // a standard type answers for itself
			}
			return StructType{Std: s, NS: uri, Mapped: true, Complete: true, SameNamespaceMap: sameNS}, path
		}
		unmapped := StructType{Std: orig, NS: uri, Complete: true, SameNamespaceMap: sameNS}

		// The role map this namespace's types are mapped by.
		m := roleMap
		if ns != nil {
			m = d.ResolveDict(ns.Get("RoleMapNS"))
		}
		if m == nil {
			return unmapped, path
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
				return unmapped, path
			}
			n, ok := d.Resolve(v[0]).(object.Name)
			target := d.ResolveDict(v[1])
			if !ok || target == nil {
				return unmapped, path
			}
			next, nextNS = n, target
		default:
			return unmapped, path
		}
		if own != "" && structNamespaceName(d, nextNS) == own {
			sameNS = true
			unmapped.SameNamespaceMap = true
		}
		if next == "" || (next == s && nextNS == ns) {
			return unmapped, path
		}
		if hops > 0 {
			if seen == nil {
				seen = map[structNSKey]bool{{ns, s}: true}
			}
			k := structNSKey{nextNS, next}
			if seen[k] {
				return unmapped, path // the chain closes on itself
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
// A seen-set ends a cyclic map (which the role-map integrity checks report
// separately) rather than looping.
//
// The answer for each type is resolved once per run and memoised per role map
// (the same run Slot as ResolveStructType), and every type on a chain is answered by the walk that
// crossed it. A per-call step budget used to bound one chain, and a file with
// a 100,000-long chain and a few thousand elements tagged with its first type
// then paid for the whole chain once per element — minutes of work with no
// budget ever tripping (audit 2026-09-22 C41). Now the whole role map costs
// one walk per run, charged to the run's work meter.
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
// type before reaching a standard one, and whether the answer is complete
// (always; see ResolveRoleMapChain).
//
// It is separate from ResolveRoleMapChain because the two questions have
// different answers on the same input: a chain that closes on itself resolves to
// "no standard type" (ResolveRoleMapChain returns mapped=false) and *also* is a
// cycle, and the PDF/A structure-type rules report those as two distinct
// findings against two distinct clauses.
//
// Answers are memoised per run like ResolveRoleMapChain's. The types a walk
// crosses after the first share its answer — each is non-standard, so a walk
// started from it follows the same links — but the first may be a standard
// type, which this walk (unlike the resolution) leaves without asking, and a
// walk started elsewhere stops on it. So a standard start answers only for
// itself.
// roleMapCycles is the run's memo of RoleMapChainCycles, per role map.
type roleMapCycles map[*object.Dictionary]map[object.Name]bool

type roleMapCyclesSlot struct{}

func RoleMapChainCycles(d View, s object.Name, roleMap *object.Dictionary) (cyclic, complete bool) {
	if roleMap == nil || s == "" {
		return false, true
	}
	memo := Slot[roleMapCycles](d.Run, roleMapCyclesSlot{})
	if *memo == nil {
		*memo = roleMapCycles{}
	}
	cyc := (*memo)[roleMap]
	if cyc == nil {
		cyc = map[object.Name]bool{}
		(*memo)[roleMap] = cyc
	}
	if r, ok := cyc[s]; ok {
		d.Charge(1)
		return r, true
	}
	path := []object.Name{s}
	seen := map[object.Name]bool{s: true}
	cur := s
	var res bool
	for {
		d.Charge(1)
		if cur != s {
			if r, ok := cyc[cur]; ok {
				res = r
				break
			}
		}
		next, ok := d.Resolve(roleMap.Get(cur)).(object.Name)
		if !ok || next == "" {
			res = false // the chain simply ends
			break
		}
		if seen[next] {
			res = true
			break
		}
		if StandardStructTypes[next] {
			res = false // a standard type is the end of the chain
			break
		}
		seen[next] = true
		path = append(path, next)
		cur = next
	}
	if StandardStructTypes[s] {
		path = path[:1]
	}
	for _, t := range path {
		cyc[t] = res
	}
	return res, true
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

// ChildTypesKey identifies n's ChildTypes slice: nodes whose /K is the same
// array share one slice, and a consumer that derives something from the
// child types can compute it once per key. It is nil when there are no child
// types (and then there is nothing to derive).
func (n StructNode) ChildTypesKey() *object.Name {
	if len(n.ChildTypes) == 0 {
		return nil
	}
	return &n.ChildTypes[0]
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

// buildStructTree flattens the tree in pre-order. It is iterative — an
// explicit stack of (node, parent) pairs, pushed in reverse so they pop in
// order — because a structure tree is as deep as the file says: a chain of a
// hundred thousand elements, each the only kid of the last, overflowed the
// stack of the recursive walk this replaces (audit 2026-09-22 C84).
//
// An element's ChildTypes are computed once per /K array, not once per
// element: /K is usually a direct array, but when it is a reference, every
// element that names the same array has the same children. A file whose N
// elements all name one array of those N elements asked the recursive walk
// for N² child types, which ran out of memory at N = 10,000 in a 1 MB file
// (audit 2026-09-22 C18). The elements share one slice, which consumers only
// read. The shared array's kids are also descended once, under the first
// element that names it: a tree never shares a /K array, so for a real file
// nothing changes, and for a DAG the walk stays linear in the file rather
// than quadratic. Every element visited, and every child typed, is charged
// to the run's work meter.
func buildStructTree(d View, cat *object.Dictionary) []StructNode {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	var nodes []StructNode
	seen := map[int]bool{}
	sharedKids := map[int][]object.Name{}
	type item struct {
		node   object.Object
		parent int
	}
	stack := []item{{root.Get("K"), -1}}
	push := func(kids []object.Object, parent int) {
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, item{kids[i], parent})
		}
	}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		d.Charge(1)
		node, parent := it.node, it.parent
		objNum := -1
		if ref, ok := node.(object.IndirectRef); ok {
			if seen[ref.Number] {
				continue
			}
			seen[ref.Number] = true
			objNum = ref.Number
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(object.Array); ok {
				push(arr, parent)
			}
			continue
		}
		rawS, hasS := d.ResolveName(elem.Get("S"))
		kids := StructKids(d, elem)
		kref, shared := elem.Get("K").(object.IndirectRef)
		childTypes, known := sharedKids[kref.Number]
		if shared && known {
			// Typed and descended under the first element that named it.
			kids = nil
		}
		if !shared || !known {
			childTypes = nil
			for _, kid := range kids {
				d.Charge(1)
				child := d.ResolveDict(kid)
				if child == nil {
					continue
				}
				if _, ok := d.ResolveName(child.Get("S")); !ok {
					continue
				}
				childTypes = append(childTypes, StandardStructType(d, child, roleMap))
			}
			if shared {
				sharedKids[kref.Number] = childTypes
			}
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
		push(kids, self)
	}
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
