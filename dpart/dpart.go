package dpart

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
)

// This file implements the document part (DPart) hierarchy defined in
// ISO 32000-2:2020 clause 14.12. The DPart tree partitions a document's pages
// into a hierarchy of parts, each optionally carrying document part metadata
// (DPM). It is a core PDF 2.0 structure and the structural foundation of PDF/VT
// (ISO 16612-2): a PDF/VT file expresses its record boundaries through this
// tree. The checks here are grounded in Tables 408 and 409 and the connectivity
// rules of 14.12.2 and 14.12.3; they operate on the parsed object model.

// Violation reports a way in which a document's DPart hierarchy departs
// from ISO 32000-2 clause 14.12.
type Violation struct {
	Rule    string // ISO 32000-2 subclause, e.g. "14.12.2"
	Message string
	Object  int // object number the violation is anchored to, 0 if N/A
}

// RuleID returns the ISO 32000-2 DPart subclause.
func (v Violation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v Violation) ObjectNum() int { return v.Object }

func (v Violation) Error() string {
	if v.Object != 0 {
		return fmt.Sprintf("DPart %s: %s (object %d)", v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("DPart %s: %s", v.Rule, v.Message)
}

// validateHierarchy runs the ISO 32000-2 14.12 checks, reporting through
// add. It is the body of ValidateDParts, split out so the panic boundary and
// the result ordering live in one place.
func validateHierarchy(doc core.View, add func(rule, msg string, obj int)) {
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		return
	}
	rootRef := cat.Get("DPartRoot")
	if rootRef == nil {
		return // no document part hierarchy; the feature is optional
	}

	// 14.12.2: the catalog /DPartRoot shall be an indirect reference to a
	// DPartRoot dictionary.
	rootDict := doc.ResolveDict(rootRef)
	if rootDict == nil {
		add("14.12.2", "catalog /DPartRoot does not resolve to a dictionary", object.RefNum(rootRef))
		return
	}
	rootDictNum := object.RefNum(rootRef)
	if t, ok := doc.ResolveName(rootDict.Get("Type")); ok && t != "DPartRoot" {
		add("14.12.4.1", fmt.Sprintf("DPartRoot /Type shall be /DPartRoot, got %s", t), rootDictNum)
	}

	// Table 408: /DPartRootNode is required and is the root of the tree.
	nodeRef := rootDict.Get("DPartRootNode")
	if nodeRef == nil {
		add("14.12.4.1", "DPartRoot is missing the required /DPartRootNode entry", rootDictNum)
		return
	}
	if doc.ResolveDict(nodeRef) == nil {
		add("14.12.4.1", "DPartRoot /DPartRootNode does not resolve to a dictionary", rootDictNum)
		return
	}

	// Map each page object number to its reading-order index so leaf ranges can
	// be checked against the page tree.
	pages := doc.Pages(doc.CatalogPages())
	pageIndex := make(map[int]int, len(pages))
	for i, pg := range pages {
		pageIndex[pg.ObjNum] = i
	}

	type leaf struct {
		objNum   int
		startIdx int
		endIdx   int // inclusive; == startIdx for a single-page range
		ok       bool
	}
	var leaves []leaf
	visited := map[int]bool{}
	maxDepth := 0

	var walk func(ref object.Object, expectedParent, depth int)
	walk = func(ref object.Object, expectedParent, depth int) {
		if !doc.Descend(depth) {
			return
		}
		doc.Charge(1)
		num := object.RefNum(ref)
		node := doc.ResolveDict(ref)
		if node == nil {
			add("14.12.2", "a DPart reference does not resolve to a dictionary", num)
			return
		}
		// 14.12.2: a DPart shall not be referenced by more than one parent, and
		// the tree shall be acyclic. Revisiting a node signals either.
		if num != 0 {
			if visited[num] {
				add("14.12.2", "DPart is referenced by more than one parent (or forms a cycle)", num)
				return
			}
			visited[num] = true
		}
		if depth+1 > maxDepth {
			maxDepth = depth + 1
		}
		if t, ok := doc.ResolveName(node.Get("Type")); ok && t != "DPart" {
			add("14.12.4.1", fmt.Sprintf("DPart /Type shall be /DPart, got %s", t), num)
		}

		// Table 409: /Parent is required. For the root node it references the
		// DPartRoot dictionary; otherwise the immediate ancestor DPart.
		parent := node.Get("Parent")
		if parent == nil {
			add("14.12.4.1", "DPart is missing the required /Parent entry", num)
		} else if pn := object.RefNum(parent); pn != expectedParent {
			add("14.12.2", "DPart /Parent does not reference its actual parent node", num)
		}

		// Table 409: /DParts (internal node) and /Start (leaf) are exclusive.
		dparts := node.Get("DParts")
		start := node.Get("Start")
		switch {
		case dparts != nil && start != nil:
			add("14.12.4.1", "DPart has both /DParts and /Start; they are exclusive", num)
		case dparts == nil && start == nil:
			add("14.12.4.1", "DPart has neither /DParts (internal node) nor /Start (leaf node)", num)
		case dparts != nil:
			// Internal node: /DParts is a non-empty array of arrays of DPart refs.
			arr, ok := doc.Resolve(dparts).(object.Array)
			if !ok || len(arr) == 0 {
				add("14.12.4.1", "DPart /DParts shall be a non-empty array", num)
				break
			}
			for _, elem := range arr {
				inner, ok := doc.Resolve(elem).(object.Array)
				if !ok {
					add("14.12.4.1", "DPart /DParts elements shall be arrays of DPart references", num)
					continue
				}
				for _, child := range inner {
					doc.Charge(1)
					walk(child, num, depth+1)
				}
			}
		default:
			// Leaf node: /Start (and optional /End) delimit a page range.
			lf := leaf{objNum: num}
			si, ok := pageIndex[object.RefNum(start)]
			if !ok {
				add("14.12.3", "DPart /Start does not reference a page object", num)
			} else {
				lf.startIdx, lf.endIdx, lf.ok = si, si, true
				if endRef := node.Get("End"); endRef != nil {
					ei, ok := pageIndex[object.RefNum(endRef)]
					if !ok {
						add("14.12.3", "DPart /End does not reference a page object", num)
						lf.ok = false
					} else if ei < si {
						add("14.12.4.1", "DPart /End page precedes /Start page", num)
						lf.ok = false
					} else {
						lf.endIdx = ei
					}
				}
			}
			leaves = append(leaves, lf)
		}

		// 14.12.4.2: validate the document part metadata dictionary if present.
		if dpm := doc.ResolveDict(node.Get("DPM")); dpm != nil {
			validateDPM(doc, dpm, num, add)
		}
	}
	walk(nodeRef, rootDictNum, 0)

	// 14.12.2 / 14.12.3: leaf ranges, in depth-first order, shall cover every
	// page exactly once and in page-tree order.
	//
	// How many leaves cover each page is a sweep over the ranges' endpoints —
	// +1 where a range starts, -1 past where it ends, summed in page order —
	// which is O(leaves + pages). Filling each range page by page was
	// O(leaves × pages): 40,000 leaves each spanning 40,000 pages took 38
	// seconds against a one-second deadline (audit 2026-09-22 C42).
	delta := make([]int, len(pages)+1)
	leafAt := map[int]leaf{} // by object number, for the back-reference check
	expectedNext := 0
	for _, lf := range leaves {
		doc.Charge(1)
		if !lf.ok {
			continue
		}
		if lf.startIdx != expectedNext {
			add("14.12.3", "DPart leaf page range is not contiguous with the preceding part in page-tree order", lf.objNum)
		}
		if lf.startIdx < len(pages) {
			delta[lf.startIdx]++
			delta[min(lf.endIdx, len(pages)-1)+1]--
		}
		if lf.endIdx+1 > expectedNext {
			expectedNext = lf.endIdx + 1
		}
		if lf.objNum != 0 {
			leafAt[lf.objNum] = lf
		}
	}
	covered := make([]int, len(pages))
	c := 0
	for i := range pages {
		doc.Charge(1)
		c += delta[i]
		covered[i] = c
		switch {
		case c == 0:
			add("14.12.2", "page is not included in any DPart leaf range", pages[i].ObjNum)
		case c > 1:
			add("14.12.2", "page is included in more than one DPart leaf range", pages[i].ObjNum)
		}
	}

	// 14.12.3: each page in a leaf's range shall have a /DPart back-reference to
	// that leaf, when present. A page whose back-reference names a leaf that
	// covers it is in order only if no other leaf covers it too; the finding
	// is the page's, so it is reported once per page however many leaves
	// disagree with it.
	for i, pg := range pages {
		bp := pg.Dict.Get("DPart")
		if bp == nil || covered[i] == 0 {
			continue
		}
		mine := 0
		if lf, ok := leafAt[object.RefNum(bp)]; ok && lf.startIdx <= i && i <= lf.endIdx {
			mine = 1
		}
		if covered[i] > mine {
			add("14.12.3", "page /DPart does not reference the DPart leaf whose range contains it", pg.ObjNum)
		}
	}

	// Table 408: if /NodeNameList is present its length equals the number of
	// levels in the tree, and each entry is a valid XML name token.
	if nnl := doc.Resolve(rootDict.Get("NodeNameList")); nnl != nil {
		arr, ok := nnl.(object.Array)
		if !ok {
			add("14.12.4.1", "DPartRoot /NodeNameList shall be an array", rootDictNum)
		} else {
			if len(arr) != maxDepth {
				add("14.12.4.1", fmt.Sprintf("DPartRoot /NodeNameList has %d entries but the hierarchy has %d levels", len(arr), maxDepth), rootDictNum)
			}
			for _, n := range arr {
				name, ok := doc.ResolveName(n)
				if !ok {
					add("14.12.4.1", "DPartRoot /NodeNameList entries shall be names", rootDictNum)
				} else if !isXMLNameToken(string(name)) {
					add("14.12.4.1", fmt.Sprintf("DPartRoot /NodeNameList entry %s is not a valid XML name token", name), rootDictNum)
				}
			}
		}
	}
}

// validateDPM checks a document part metadata dictionary against ISO 32000-2
// 14.12.4.2: every value (recursively) shall be only a text/date string, array,
// dictionary, boolean, integer or real. Names, streams and null are not
// permitted as values.
//
// The clause also requires DPM key names to be XML name tokens, but that rule is
// deliberately not enforced on the raw PDF name. ISO 16612-2 (the PDF/VT
// standard this structure comes from, and the source of real DPM in the wild)
// encodes metadata field names — whose original text may contain spaces or
// non-ASCII characters — into a reversible base64 form used as the PDF name key.
// The Cal Poly PDF/VT-1 test suite, a set of valid files, carries such keys
// (e.g. "77u-R2VuZGVy75i2" for a field named "Gender"); they are not literal XML
// name tokens and validating the raw name would flag conforming files. A
// decoded check belongs with dedicated PDF/VT-1 (ISO 16612-2) validation.
//
// The values are walked iteratively, and every object reached through a
// reference is visited once, arrays included. The recursive walk this
// replaces kept its visited set on dictionaries only, so `6 0 obj [6 0 R]`
// recursed until the stack overflowed, and a DAG of arrays each naming the
// next twice cost 2^depth (audit 2026-09-22 C17). Each value is charged to the
// run's work meter; a value that occurs several times in the DAG is judged
// once, which is all its finding could say.
func validateDPM(doc core.View, dpm *object.Dictionary, objNum int, add func(rule, msg string, obj int)) {
	seenRef := map[int]bool{}
	seenDict := map[*object.Dictionary]bool{dpm: true}
	var stack []object.Object
	for _, dval := range dpm.All() {
		stack = append(stack, dval)
	}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		doc.Charge(1)
		if ref, ok := v.(object.IndirectRef); ok {
			if seenRef[ref.Number] {
				continue
			}
			seenRef[ref.Number] = true
		}
		switch val := doc.Resolve(v).(type) {
		case object.String, object.Boolean, object.Integer, object.Real:
			// Permitted scalar value types (text string / date string /
			// boolean / integer / real).
		case object.Array:
			stack = append(stack, val...)
		case *object.Dictionary:
			if seenDict[val] {
				continue
			}
			seenDict[val] = true
			for _, dval := range val.All() {
				stack = append(stack, dval)
			}
		default:
			add("14.12.4.2", fmt.Sprintf("DPM value of type %T is not permitted (only string, array, dictionary, boolean, integer, real)", val), objNum)
		}
	}
}

// isXMLNameToken reports whether s is a valid XML Name: a first character that
// is a letter, underscore or colon, followed by letters, digits, or one of
// '-', '_', '.', ':'. This is the constraint 14.12.4.2 places on DPM keys and
// Table 408 on /NodeNameList entries.
func isXMLNameToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isLetter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == ':'
		if i == 0 {
			if !isLetter {
				return false
			}
			continue
		}
		if !isLetter && !(c >= '0' && c <= '9') && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

// validateView runs the hierarchy checks over a view and returns the findings.
// It exists for PDF/VT, which requires a conforming document-part hierarchy and
// adopts these findings under its own prefix; the root package's ValidateDParts
// adds the read-time guard trips on top.
func validateView(v core.View) []Violation {
	var out []Violation
	add := func(rule, msg string, obj int) {
		out = append(out, Violation{Rule: rule, Message: msg, Object: obj})
	}
	if !v.Cancel.Stopped() {
		finding.Guarded(add, func() { validateHierarchy(v, add) })
	}
	finding.Sort(out)
	return out
}
