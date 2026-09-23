package pdfua

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/checked"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
	"math"
	"strings"
)

// Violation is a PDF/UA accessibility conformance failure.
type Violation struct {
	Clause  string // ISO 14289 clause
	Message string
	Object  int
	// Part is the PDF/UA part the finding is against: "1" (ISO 14289-1) or
	// "2" (ISO 14289-2). The validators set it on every finding; an empty
	// Part reads as "1", the part this type described before it had one.
	Part string
}

// RuleID returns the ISO 14289 clause identifier.
func (v Violation) RuleID() string { return v.Clause }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v Violation) ObjectNum() int { return v.Object }

// Error prints the finding under the part it is against, so a PDF/UA-2 finding
// is not reported as a PDF/UA-1 one (audit 2026-09-22 C145).
func (v Violation) Error() string {
	part := v.Part
	if part == "" {
		part = "1"
	}
	if v.Object != 0 {
		return fmt.Sprintf("[PDF/UA-%s %s] object %d: %s", part, v.Clause, v.Object, v.Message)
	}
	return fmt.Sprintf("[PDF/UA-%s %s] %s", part, v.Clause, v.Message)
}

// WithPart sets the part on every finding and returns them.
func WithPart(vs []Violation, part string) []Violation {
	for i := range vs {
		vs[i].Part = part
	}
	return vs
}

func validateView(doc core.View, part string) []Violation {
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		// Even here the guard trips are reported: "no catalog" is exactly the
		// kind of finding a read-time truncation can manufacture. This early
		// return is sorted like the normal one — every exit of every validator
		// returns findings in the same order.
		return []Violation{{Clause: "7.1", Message: "document has no catalog", Part: part}}
	}
	var v []Violation

	// Every check runs under a recover boundary, so a panic on hostile input
	// becomes an "internal" finding instead of crashing the caller, and one bad
	// check does not discard its siblings' findings (audit C27). The same wrapper
	// is the coarse cancellation boundary: a cancelled run skips every check it
	// has not started, and the traversals inside a check stop at their own finer
	// boundaries (cancel.go).
	//
	// The checks take the document as their first parameter rather than being
	// methods on it, so these two adapters bind it once here instead of at
	// every call site.
	run := func(check func(core.View) []Violation) {
		if doc.Cancel.Stopped() {
			return
		}
		v = append(v, RunCheck(func() []Violation { return check(doc) })...)
	}
	runCat := func(check func(core.View, *object.Dictionary) []Violation) {
		run(func(d core.View) []Violation { return check(d, cat) })
	}

	// 7.1 — tagged PDF, structure tree, shown title; 7.2 — default language.
	runCat(checkUACatalogBasics)

	// 5 — the file must declare PDF/UA conformance in its XMP metadata.
	run(func(d core.View) []Violation { return checkUAIdentifier(d, cat, part) })

	// Matterhorn checkpoint 06: the document must have an XMP dc:title.
	runCat(checkUATitle)

	// 7.21 — every font used for rendering must be embedded.
	run(checkUAFonts)
	run(checkUAFontDicts)
	run(checkUACMaps)
	run(checkUACMapWMode)
	run(checkUACIDSystemInfo)
	run(checkUAToUnicodeValues)
	run(checkUAFontSubsetGlyphs)
	run(checkUANotdefCID)

	// 7.2 — text must map to Unicode (Matterhorn 10-001).
	run(checkUACharMapping)

	// 7.18.3 — pages with annotations must use structure tab order.
	run(checkUATabOrder)

	// 7.1 — structure types must be standard or mapped via /RoleMap.
	runCat(checkUARoleMap)
	runCat(checkUARoleMapIntegrity)
	runCat(checkUAStructParent)

	// 7.2 — structure-element nesting (tables, lists, TOC) per the UA profile.
	runCat(checkUAStructNesting)
	runCat(checkUATableListStructure)
	runCat(checkUATableGrid)
	runCat(checkUATableTHScope)

	// 7.4 — heading levels must not be skipped; start at H1; one <H> per node.
	runCat(checkUAHeadings)
	runCat(checkUAOneHPerNode)

	// 7.16 — encryption must not disable accessibility (Matterhorn 26).
	run(checkUASecurity)

	// 7.18.2 — forbidden annotation subtypes (Matterhorn 28-007).
	run(checkUAAnnotations)

	// 7.15 — dynamic XFA is forbidden (Matterhorn 25-001).
	runCat(checkUAXFA)

	// 7.18.1 — a form field description belongs on the field, not its widgets.
	runCat(checkUAFieldDescription)

	// 7.18.6.2 — media clip data dictionaries need /CT and /Alt.
	run(checkUAMediaClips)

	// 7.20 — reference XObjects are forbidden; tagged forms painted once.
	run(checkUAReferenceXObjects)
	run(checkUAFormXObjectMCID)

	// 7.2 — any present /Lang must be a valid BCP 47 tag.
	runCat(checkUALang)

	// 7.10 — optional-content config; 7.11 — embedded-file specifications.
	runCat(checkUAOptionalContent)
	run(checkUAEmbeddedFiles)

	// 6.1 — PDF/UA-1 requires a 1.x header. PDF/UA-2 is defined against
	// PDF 2.0 instead; ValidatePDFUA2 checks that itself.
	if part == "1" {
		run(checkUAHeaderVersion)
	}

	// PDF/UA-2 rules about the PDF 2.0 namespaced structure model, which a
	// PDF/UA-1 (PDF 1.x) file does not have.
	if part == "2" {
		runCat(checkUA2NamespaceRoleMaps)
		runCat(checkUA2MathParent)
	}

	// 7.1 — Suspects must not be true; 7.4.4 strong/weak; 7.9 Note IDs.
	runCat(checkUASuspects)
	runCat(checkUAStrongWeak)
	runCat(checkUANotes)

	// 7.1 — real content must be tagged or marked as an artifact (Matterhorn 01).
	runCat(checkUARealContent)

	// 7.18 — annotation must sit under the right structure element (28-010/011).
	runCat(checkUAAnnotStructType)

	// 7.3 — every figure needs alternate text.
	runCat(checkFigureAlt)

	// The caller sorts.
	return WithPart(v, part)
}

// checkUACatalogBasics covers the catalog-level PDF/UA requirements: the file
// must be tagged (7.1) with a structure tree, specify a default natural
// language (7.2), and display its title in the window title bar (7.1).
func checkUACatalogBasics(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation

	// 7.1 — the file must be a tagged PDF.
	mark := d.ResolveDict(cat.Get("MarkInfo"))
	if mark == nil || !d.IsTrue(mark.Get("Marked")) {
		v = append(v, Violation{Clause: "7.1", Message: "document is not marked as tagged (/MarkInfo << /Marked true >>)", Object: 0})
	}
	if cat.Get("StructTreeRoot") == nil {
		v = append(v, Violation{Clause: "7.1", Message: "document has no structure tree (/StructTreeRoot)", Object: 0})
	}

	// 7.2 — a default natural language must be set.
	if !d.NonEmptyStringOrLocked(cat.Get("Lang")) {
		v = append(v, Violation{Clause: "7.2", Message: "document does not specify a default language (catalog /Lang)", Object: 0})
	}

	// 7.1 — the document title must be shown in the window title bar.
	vp := d.ResolveDict(cat.Get("ViewerPreferences"))
	if vp == nil || !d.IsTrue(vp.Get("DisplayDocTitle")) {
		v = append(v, Violation{Clause: "7.1", Message: "/ViewerPreferences /DisplayDocTitle must be true", Object: 0})
	}
	return v
}

// checkUAHeadings flags a skipped numbered-heading level (e.g. H1 followed by H3
// without an intervening H2), walking the structure tree in document order. It
// keys off the /RoleMap-resolved type, like the sibling heading checks, so a
// custom type mapped to a numbered heading counts as one (audit C29).
func checkUAHeadings(d core.View, cat *object.Dictionary) []Violation {
	var levels []int
	for _, n := range structTree(d, cat) {
		if st := n.StdType; len(st) == 2 && st[0] == 'H' && st[1] >= '1' && st[1] <= '6' {
			levels = append(levels, int(st[1]-'0'))
		}
	}

	var v []Violation
	// 7.4.2: a strongly structured document's first numbered heading must be H1.
	if len(levels) > 0 && levels[0] != 1 {
		v = append(v, Violation{Clause: "7.4.2", Message: fmt.Sprintf("first heading is H%d; a strongly structured document must start at H1", levels[0]), Object: 0})
	}
	prev := 0
	for _, lvl := range levels {
		if prev != 0 && lvl > prev+1 {
			v = append(v, Violation{Clause: "7.4", Message: fmt.Sprintf("heading level H%d follows H%d, skipping a level", lvl, prev), Object: 0})
		}
		prev = lvl
	}
	return v
}

// checkUAOneHPerNode enforces 7.4.4: in a weakly structured document each
// structure node may contain at most one child <H> heading.
func checkUAOneHPerNode(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	for _, n := range structTree(d, cat) {
		if countName(n.ChildTypes, "H") > 1 {
			v = append(v, Violation{Clause: "7.4.4", Message: "a structure node contains more than one child <H> heading", Object: 0})
		}
	}
	return v
}

// checkUATabOrder requires structure tab order on pages that carry annotations.
func checkUATabOrder(d core.View) []Violation {
	var v []Violation
	for _, pg := range d.Pages(d.CatalogPages()) {
		annots, _ := d.Resolve(pg.Dict.Get("Annots")).(object.Array)
		if len(annots) == 0 {
			continue
		}
		if tabs, _ := d.Resolve(pg.Dict.Get("Tabs")).(object.Name); tabs != "S" {
			v = append(v, Violation{Clause: "7.18.3", Message: "page with annotations must set /Tabs /S (structure tab order)", Object: pg.ObjNum})
		}
	}
	return v
}

// checkUARoleMap flags structure element types that are neither standard nor
// mapped to a standard type — through the structure tree's /RoleMap for an
// element in the default namespace, through its namespace's /RoleMapNS for
// one that names a namespace (ISO 32000-2 14.8.6.2). A type standard in the
// namespace the element names is standard: /Title in the PDF 2.0 namespace
// needs no mapping, though the PDF 1.7 set has no /Title.
//
// It reads RawS because its question is about the type as written.
func checkUARoleMap(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	// One verdict per distinct written type in each namespace.
	type key struct {
		t  object.Name
		ns *object.Dictionary
	}
	decided := map[key]bool{}
	for _, n := range structTree(d, cat) {
		k := key{n.RawS, d.ResolveDict(n.Elem.Get("NS"))}
		if k.t == "" || decided[k] {
			continue
		}
		decided[k] = true
		// A budget trip leaves the mapping unknown; only a completed walk that
		// found no standard type is evidence of a violation.
		if !n.Mapped && n.Complete {
			where := "/RoleMap"
			if k.ns != nil {
				where = "its namespace's /RoleMapNS"
			}
			v = append(v, Violation{Clause: "7.1", Message: "structure type /" + string(k.t) + " is neither standard nor mapped in " + where, Object: 0})
		}
	}
	return v
}

// checkUAIdentifier requires an XMP metadata stream declaring the given
// PDF/UA part.
//
// The packet is read through the XMP model, by namespace URI: the value is
// pdfuaid:part whatever prefix it is written with, found in element or
// attribute form, and unaffected by a comment or an escaped character — none
// of which the substring reader this replaced could say (audit C141).
func checkUAIdentifier(d core.View, cat *object.Dictionary, want string) []Violation {
	stream, ok := d.Resolve(cat.Get("Metadata")).(*object.Stream)
	if !ok {
		return []Violation{{Clause: "5", Message: "document has no XMP metadata (a PDF/UA identifier is required)", Object: 0}}
	}
	packet, status := d.XMPPacketOf(stream, object.RefNum(cat.Get("Metadata")))
	switch status {
	case core.XMPLimit:
		// Not read: the trip is on the run and reaches the report as a
		// "limit" finding. Saying the part is missing would be a guess.
		return nil
	case core.XMPMalformed:
		return []Violation{{Clause: "5", Message: "XMP metadata is not well-formed XML, so it declares no PDF/UA part (pdfuaid:part)", Object: 0}}
	case core.XMPAbsent:
		return []Violation{{Clause: "5", Message: "XMP metadata does not declare the PDF/UA part (pdfuaid:part)", Object: 0}}
	}
	part, has := packet.Text(xmp.NSPDFUAID, "part")
	if !has {
		return []Violation{{Clause: "5", Message: "XMP metadata does not declare the PDF/UA part (pdfuaid:part)", Object: 0}}
	}
	if part != want {
		got := part
		if got == "" {
			got = `""` // present and empty, which is not the part either
		}
		return []Violation{{Clause: "5", Message: "pdfuaid:part must be " + want + " for PDF/UA-" + want + ", got " + got, Object: 0}}
	}
	return checkUAIdentifierPrefix(packet)
}

// checkUAIdentifierPrefix flags a PDF/UA Identification Schema property (part,
// amd, corr) that is written with a namespace prefix other than the required
// "pdfuaid" (clause 5). Only properties in the PDF/UA-id namespace are
// considered, so an unrelated property named amd or corr is ignored; a property
// in the default namespace has no prefix to judge.
func checkUAIdentifierPrefix(packet *xmp.Packet) []Violation {
	var v []Violation
	seen := map[string]bool{}
	for _, p := range packet.Properties() {
		if p.NS != xmp.NSPDFUAID || p.Prefix == "pdfuaid" || p.Prefix == "" {
			continue
		}
		switch p.Name {
		case "part", "amd", "corr":
		default:
			continue
		}
		if key := p.Name + "\x00" + p.Prefix; !seen[key] {
			seen[key] = true
			v = append(v, Violation{Clause: "5", Message: "PDF/UA identification property '" + p.Name + "' uses namespace prefix '" + p.Prefix + "', must be 'pdfuaid'", Object: 0})
		}
	}
	return v
}

// checkUAStructParent flags a structure element that lacks the required /P
// (parent) entry (7.1, ISO 32000-1 14.7.2 Table 323).
func checkUAStructParent(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	walkStructElems(d, cat, func(elem *object.Dictionary, t object.Name) {
		if elem.Get("P") == nil {
			v = append(v, Violation{Clause: "7.1", Message: "structure element <" + string(t) + "> has no /P (parent) entry", Object: 0})
		}
	})
	return v
}

// checkUARoleMapIntegrity flags a /RoleMap that remaps a standard structure type
// or contains a circular mapping (7.1).
func checkUARoleMapIntegrity(d core.View, cat *object.Dictionary) []Violation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	if roleMap == nil {
		return nil
	}
	var v []Violation
	// Bound the total chain-following work. Get is O(1) once the dictionary is
	// indexed, so this loop is at worst O(n^2) over a crafted /RoleMap; the cap
	// keeps that from being a CPU DoS on a large map while never triggering on a
	// real one (audit C20).
	work := 0
	for key := range roleMap.Keys() {
		if standardStructTypes[key] {
			v = append(v, Violation{Clause: "7.1", Message: "/RoleMap remaps standard structure type <" + string(key) + ">", Object: 0})
		}
		// Follow the mapping chain from key; a repeat is a cycle.
		seen := map[object.Name]bool{key: true}
		cur := key
		for {
			work++
			if work > d.Limits.RoleMapSteps {
				// Every key not yet examined goes unchecked, including the
				// cheap standard-type test, so the remaining keys are
				// unknown rather than clean.
				d.Note(core.GuardRoleMapWork, fmt.Sprintf("following the /RoleMap chains cost more than %s steps; the remaining keys were not checked for standard-type remapping or cycles", core.LimitBound(int64(d.Limits.RoleMapSteps), core.DefaultMaxRoleMapSteps)), 0)
				return v
			}
			next, ok := d.Resolve(roleMap.Get(cur)).(object.Name)
			if !ok || next == "" {
				break
			}
			if seen[next] {
				v = append(v, Violation{Clause: "7.1", Message: "/RoleMap contains a circular mapping involving <" + string(key) + ">", Object: 0})
				break
			}
			seen[next] = true
			cur = next
		}
	}
	return v
}

// The total number of /RoleMap chain-follow steps across all keys defaults to
// defaultMaxRoleMapSteps; a caller can change it with WithMaxRoleMapSteps. The
// bound stops a crafted role map driving super-linear validation work.

// checkUASecurity flags an encrypted document that lacks a /P entry or whose
// permissions disable text extraction for accessibility (Matterhorn 26-001/002).
func checkUASecurity(d core.View) []Violation {
	enc := d.ResolveDict(d.Trailer.Get("Encrypt"))
	if enc == nil {
		return nil
	}
	p, ok := d.Resolve(enc.Get("P")).(object.Integer)
	if !ok {
		return []Violation{{Clause: "7.16", Message: "encrypted document has no /P permissions entry", Object: 0}}
	}
	if uint32(int32(p))&0x200 == 0 { // bit position 10: extract for accessibility
		return []Violation{{Clause: "7.16", Message: "encryption disables text extraction for accessibility (permission bit 10)", Object: 0}}
	}
	return nil
}

// checkUAAnnotations flags forbidden annotation subtypes. Hidden and Popup
// annotations are exempt from checkpoint 28.
//
// It judges the annotations the document reaches, direct ones included — an
// annotation written inline in a page's /Annots is as much the page's as one
// written as its own object — and not an orphan nothing refers to (audit
// 2026-09-22 C83). A direct annotation is reported against the object it is
// written in.
func checkUAAnnotations(d core.View) []Violation {
	var v []Violation
	for _, r := range d.ReachableDicts() {
		a, num := r.Dict, r.ObjNum
		if r.Stream != nil || !d.IsAnnotation(a) {
			continue
		}
		st, _ := d.ResolveName(a.Get("Subtype"))
		if st == "Popup" {
			continue
		}
		if f, _ := d.Resolve(a.Get("F")).(object.Integer); int(f)&0x2 != 0 {
			continue // hidden
		}
		if st == "TrapNet" {
			v = append(v, Violation{Clause: "7.18.2", Message: "TrapNet annotations are not permitted", Object: num})
		}
		// 28-012: a Link annotation needs an alternate description in /Contents.
		if st == "Link" {
			if !d.NonEmptyStringOrLocked(a.Get("Contents")) {
				v = append(v, Violation{Clause: "7.18.5", Message: "Link annotation has no alternate description (/Contents)", Object: num})
			}
		}
		// 7.18.1: a form-field Widget must have a non-empty field description /TU
		// (own or inherited from its parent field) or an /Alt on the widget.
		if st == "Widget" {
			if !effectiveFieldTU(d, a) && !d.NonEmptyStringOrLocked(a.Get("Alt")) {
				v = append(v, Violation{Clause: "7.18.1", Message: "form-field Widget has neither a field description (/TU) nor an /Alt", Object: num})
			}
		}
		// 7.18.1: every other visible annotation (not a Widget, which has its own
		// TU/Alt rule, and not a PrinterMark artifact) must carry an alternate
		// description in /Contents or /Alt.
		if st != "Widget" && st != "Link" && st != "PrinterMark" {
			if !d.NonEmptyStringOrLocked(a.Get("Contents")) && !d.NonEmptyStringOrLocked(a.Get("Alt")) {
				v = append(v, Violation{Clause: "7.18.1", Message: "annotation of subtype /" + string(st) + " has no alternate description (/Contents or /Alt)", Object: num})
			}
		}
		// 7.18.8: a PrinterMark is an incidental artifact and must NOT be tagged;
		// a visible one carrying a /StructParent is a violation.
		if st == "PrinterMark" {
			if a.Get("StructParent") != nil {
				v = append(v, Violation{Clause: "7.18.8", Message: "PrinterMark annotation must be an artifact, not tagged (has /StructParent)", Object: num})
			}
			continue
		}
		// 28-002/010/011: every other visible annotation must be represented in the
		// structure tree — it carries a /StructParent linking it to a structure
		// element. (Hidden and Popup annotations were already skipped above.)
		if a.Get("StructParent") == nil {
			v = append(v, Violation{Clause: "7.18.1", Message: "annotation is not tagged (no /StructParent linking it to the structure tree)", Object: num})
		}
	}
	return v
}

// isPredefinedCMap reports whether name is a predefined CMap from ISO 32000-1,
// 9.7.5.2, Table 118 (the core.PredefinedCMaps table, which includes Identity-H/V).
func isPredefinedCMap(name object.Name) bool {
	_, ok := core.PredefinedCMaps[string(name)]
	return ok
}

// checkUACMaps enforces that a Type 0 font's CMap is either predefined (Table
// 118) or embedded, and that an embedded CMap's /UseCMap references only a
// predefined CMap (7.21.3.3). Only fonts used for rendering are considered.
func checkUACMaps(d core.View) []Violation {
	var v []Violation
	for fontDict := range core.CollectFontTextUsage(d) {
		v = append(v, checkOneUACMap(d, fontDict)...)
	}
	return v
}

// checkUACIDSystemInfo enforces that a composite font's CIDFont CIDSystemInfo
// matches the Registry and Ordering implied by its CMap encoding (7.21.3.1).
// Identity encodings are exempt; the check runs only when both sides declare a
// Registry and Ordering.
func checkUACIDSystemInfo(d core.View) []Violation {
	var v []Violation
	for fontDict := range core.CollectFontTextUsage(d) {
		v = append(v, checkOneUACIDSystemInfo(d, fontDict)...)
	}
	return v
}

func checkOneUACIDSystemInfo(d core.View, fontDict *object.Dictionary) []Violation {
	if st, _ := d.ResolveName(fontDict.Get("Subtype")); st != "Type0" {
		return nil
	}
	var wantReg, wantOrd string
	wantSup, haveWantSup := 0, false
	switch enc := d.Resolve(fontDict.Get("Encoding")).(type) {
	case object.Name:
		if enc == "Identity-H" || enc == "Identity-V" {
			return nil
		}
		info, ok := core.PredefinedCMaps[string(enc)]
		if !ok {
			return nil
		}
		wantReg, wantOrd = info.Registry, info.Ordering
	case *object.Stream:
		wantReg, wantOrd = cidSystemInfo(d, &enc.Dict)
		wantSup, haveWantSup = cidSupplement(d, &enc.Dict)
	default:
		return nil
	}
	if wantReg == "" && wantOrd == "" {
		return nil
	}
	df, _ := d.Resolve(fontDict.Get("DescendantFonts")).(object.Array)
	if len(df) == 0 {
		return nil
	}
	cid := d.ResolveDict(df[0])
	if cid == nil {
		return nil
	}
	gotReg, gotOrd := cidSystemInfo(d, cid)
	if gotReg == "" && gotOrd == "" {
		return nil
	}
	if gotReg != wantReg || gotOrd != wantOrd {
		return []Violation{{Clause: "7.21.3.1", Message: "CIDFont CIDSystemInfo (" + gotReg + "-" + gotOrd + ") does not match the CMap (" + wantReg + "-" + wantOrd + ")", Object: d.ObjNumOf(fontDict)}}
	}
	// The CIDFont's Supplement must not exceed the CMap's (a CMap of a lower
	// supplement cannot address CIDs introduced by a higher one).
	if gotSup, ok := cidSupplement(d, cid); ok && haveWantSup && gotSup > wantSup {
		return []Violation{{Clause: "7.21.3.1", Message: fmt.Sprintf("CIDFont CIDSystemInfo Supplement %d exceeds the CMap Supplement %d", gotSup, wantSup), Object: d.ObjNumOf(fontDict)}}
	}
	return nil
}

// cidSupplement returns a dictionary's /CIDSystemInfo /Supplement value.
func cidSupplement(d core.View, dict *object.Dictionary) (int, bool) {
	si := d.ResolveDict(dict.Get("CIDSystemInfo"))
	if si == nil {
		return 0, false
	}
	n, ok := d.Resolve(si.Get("Supplement")).(object.Integer)
	return int(n), ok
}

// cidSystemInfo returns the Registry and Ordering strings of a dictionary's
// /CIDSystemInfo, or empty strings if absent — or ciphertext, which the caller
// must not compare either.
func cidSystemInfo(d core.View, dict *object.Dictionary) (string, string) {
	si := d.ResolveDict(dict.Get("CIDSystemInfo"))
	if si == nil {
		return "", ""
	}
	r, rr := d.StringValue(si.Get("Registry"))
	o, ro := d.StringValue(si.Get("Ordering"))
	if rr == core.ReasonLocked || ro == core.ReasonLocked {
		return "", ""
	}
	return string(r.Value), string(o.Value)
}

// checkUACMapWMode enforces that an embedded CMap's /WMode dictionary entry
// matches the WMode declared inside the CMap stream itself (7.21.3.3, CMapFile
// rule). Only Type 0 fonts used for rendering are considered.
func checkUACMapWMode(d core.View) []Violation {
	var v []Violation
	for fontDict := range core.CollectFontTextUsage(d) {
		if st, _ := d.ResolveName(fontDict.Get("Subtype")); st != "Type0" {
			continue
		}
		s, ok := d.Resolve(fontDict.Get("Encoding")).(*object.Stream)
		if !ok {
			continue
		}
		dictWM := 0
		if w, ok := d.Resolve(s.Dict.Get("WMode")).(object.Integer); ok {
			dictWM = int(w)
		}
		data, _ := d.Content(s) // reason: presence-only; an unread CMap declares no /WMode and the producer recorded any declined trip
		if inner, found := cmapInnerWMode(data); found && inner != dictWM {
			v = append(v, Violation{Clause: "7.21.3.3", Message: fmt.Sprintf("embedded CMap /WMode %d does not match the WMode %d declared in the CMap stream", dictWM, inner), Object: d.ObjNumOf(fontDict)})
		}
	}
	return v
}

// cmapInnerWMode extracts the integer following the first "/WMode" token in a
// decoded CMap stream (e.g. "/WMode 1 def"), returning it and whether it was
// found.
func cmapInnerWMode(data []byte) (int, bool) {
	i := strings.Index(string(data), "/WMode")
	if i < 0 {
		return 0, false
	}
	j := i + len("/WMode")
	for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\r' || data[j] == '\n') {
		j++
	}
	n, digits, _ := checked.Decimal(data[j:])
	if digits == 0 {
		return 0, false
	}
	// A value too large for an int saturates; it is compared with 0 and 1.
	return int(min(n, math.MaxInt)), true
}

func checkOneUACMap(d core.View, fontDict *object.Dictionary) []Violation {
	if st, _ := d.ResolveName(fontDict.Get("Subtype")); st != "Type0" {
		return nil
	}
	num := d.ObjNumOf(fontDict)
	switch enc := d.Resolve(fontDict.Get("Encoding")).(type) {
	case object.Name:
		if !isPredefinedCMap(enc) {
			return []Violation{{Clause: "7.21.3.3", Message: "Type 0 font uses CMap /" + string(enc) + ", which is neither predefined nor embedded", Object: num}}
		}
	case *object.Stream:
		if use, ok := d.ResolveName(enc.Dict.Get("UseCMap")); ok && !isPredefinedCMap(use) {
			return []Violation{{Clause: "7.21.3.3", Message: "embedded CMap references non-predefined CMap /" + string(use) + " via /UseCMap", Object: num}}
		}
	}
	return nil
}

// checkUAToUnicodeValues flags a rendered font whose ToUnicode CMap maps any
// character code to a forbidden Unicode value (U+0000, U+FEFF, or U+FFFE), which
// carry no usable text meaning (7.21.7).
func checkUAToUnicodeValues(d core.View) []Violation {
	var v []Violation
	for fontDict := range core.CollectFontTextUsage(d) {
		if tu, ok := d.Resolve(fontDict.Get("ToUnicode")).(*object.Stream); ok {
			if core.HasForbiddenUnicodeTargets(d, tu) {
				v = append(v, Violation{Clause: "7.21.7", Message: "ToUnicode CMap maps to a forbidden Unicode value (U+0000, U+FEFF or U+FFFE)", Object: d.ObjNumOf(fontDict)})
			}
		}
	}
	return v
}

// checkUAFontSubsetGlyphs enforces 7.21.4.2 for Type 1 subset fonts: when the
// FontDescriptor carries a /CharSet string, it must list the name of every glyph
// actually present in the embedded font program — not merely the glyphs used for
// rendering. The .notdef glyph is never required to be listed. Only subset fonts
// (ABCDEF+ BaseFont prefix) are in scope, matching the profile.
//
// The CIDFont /CIDSet variant of this rule is deliberately not implemented: a
// subset CIDFont's embedded program routinely contains padded/present glyphs
// that a conformant /CIDSet does not enumerate, so a program-vs-CIDSet
// comparison raises false positives on well-formed files.
func checkUAFontSubsetGlyphs(d core.View) []Violation {
	var v []Violation
	for fontDict := range core.CollectFontTextUsage(d) {
		if !isSubsetFont(d, fontDict) {
			continue
		}
		switch st, _ := d.ResolveName(fontDict.Get("Subtype")); st {
		case "Type1", "MMType1":
			v = append(v, checkType1CharSet(d, fontDict)...)
		case "Type0":
			v = append(v, checkCIDFontCIDSet(d, fontDict)...)
		}
	}
	return v
}

// checkType1CharSet verifies a subset Type 1 font's /CharSet lists exactly the
// glyph names present in the embedded program.
func checkType1CharSet(d core.View, fontDict *object.Dictionary) []Violation {
	fd := d.ResolveDict(fontDict.Get("FontDescriptor"))
	if fd == nil {
		return nil
	}
	cs, r := d.StringValue(fd.Get("CharSet"))
	if r != core.ReasonOK {
		return nil // absent, or ciphertext that lists nothing readable
	}
	fp, _ := core.LoadFontProgram(d, fd) // reason: a nil program declines below; the producer recorded any declined trip
	if fp == nil || fp.GlyphNames == nil {
		return nil
	}
	listed := core.ParseCharSet(string(cs.Value))
	num := d.ObjNumOf(fontDict)
	var v []Violation
	// Both directions report ONE glyph as the example, not the whole set. The
	// glyph named must be the lexicographically smallest offender rather than
	// whichever the loop meets first: fp.glyphNames and listed are Go maps, and
	// Go randomises map iteration order on every run, so "first" named a
	// different glyph each time the same font was validated and the message
	// churned between otherwise identical runs. object.String order is a total order
	// over glyph names, so the choice below is reproducible — that is
	// load-bearing (reports are diffed run against run), not incidental.
	//
	// The minimum is tracked in one pass rather than by sorting: these maps hold
	// one entry per glyph in an untrusted, attacker-sized embedded font program,
	// and sorting them would allocate a slice as large as the font.

	// Forward: every glyph in the program must be listed in /CharSet.
	unlisted := ""
	for name := range fp.GlyphNames {
		if name == ".notdef" || name == "" {
			continue
		}
		if !listed[name] && (unlisted == "" || name < unlisted) {
			unlisted = name
		}
	}
	if unlisted != "" {
		v = append(v, Violation{Clause: "7.21.4.2", Message: "FontDescriptor /CharSet does not list glyph " + unlisted + " present in the embedded font program", Object: num})
	}
	// Reverse: /CharSet must not list a glyph absent from the program.
	absent := ""
	for name := range listed {
		if name == ".notdef" || name == "" {
			continue
		}
		if !fp.GlyphNames[name] && (absent == "" || name < absent) {
			absent = name
		}
	}
	if absent != "" {
		v = append(v, Violation{Clause: "7.21.4.2", Message: "FontDescriptor /CharSet lists glyph " + absent + " that is not present in the embedded font program", Object: num})
	}
	return v
}

// checkCIDFontCIDSet verifies a subset CIDFontType2 font's /CIDSet identifies
// every CID present in the embedded program. A CID is "present" when its glyph
// carries an outline, EXCEPT glyphs that appear only as composite-glyph
// components (e.g. an accent reused across letters): such a glyph carries an
// outline as a building block but is not a directly mapped CID, so a conformant
// /CIDSet does not list it. Only the Identity CIDToGIDMap case (CID == GID) is
// handled; a mapping stream would need inversion and is left alone.
func checkCIDFontCIDSet(d core.View, fontDict *object.Dictionary) []Violation {
	desc := core.Type0Descendant(d, fontDict)
	if desc == nil {
		return nil
	}
	if cs, _ := d.ResolveName(desc.Get("Subtype")); cs != "CIDFontType2" {
		return nil
	}
	if m := d.Resolve(desc.Get("CIDToGIDMap")); m != nil {
		if n, ok := m.(object.Name); !ok || n != "Identity" {
			return nil
		}
	}
	fd := d.ResolveDict(desc.Get("FontDescriptor"))
	if fd == nil {
		return nil
	}
	cidSetStream, ok := d.Resolve(fd.Get("CIDSet")).(*object.Stream)
	if !ok {
		return nil
	}
	fp, _ := core.LoadFontProgram(d, fd) // reason: a nil program declines below; the producer recorded any declined trip
	if fp == nil || fp.GlyphNonEmpty == nil {
		return nil
	}
	present, r := core.DecodeCIDSet(d, cidSetStream)
	if r.Declined() {
		// Not read — a limit, a filter, ciphertext. An unread CIDSet lists
		// nothing, and saying so would be a finding about pdf0 (audit
		// 2026-09-22 C47); the producer recorded the trip. A CIDSet whose data
		// is malformed does not list the glyphs, and the rule says so below.
		return nil
	}
	for gid, nonEmpty := range fp.GlyphNonEmpty {
		if !nonEmpty || gid == 0 {
			continue
		}
		if gid < len(fp.ComponentGID) && fp.ComponentGID[gid] {
			continue // outline serves only as a composite component
		}
		if !present.Has(gid) {
			return []Violation{{Clause: "7.21.4.2", Message: "FontDescriptor /CIDSet does not list all CIDs present in the embedded font program", Object: d.ObjNumOf(fontDict)}}
		}
	}
	return nil
}

// checkUANotdefCID enforces 7.21.8 for composite fonts: a text-showing operator
// must not reference the .notdef glyph. For an Identity-encoded Type 0 font the
// two-byte codes are CIDs, and CID 0 is unambiguously .notdef — a definite
// signal that needs no font-program lookup. The simple-font .notdef case is not
// handled here because it can only be resolved through the font program, where a
// lookup failure is indistinguishable from a genuine .notdef reference.
func checkUANotdefCID(d core.View) []Violation {
	var v []Violation
	for fontDict, u := range core.CollectFontTextUsage(d) {
		if st, _ := d.ResolveName(fontDict.Get("Subtype")); st != "Type0" {
			continue
		}
		// The CMap says how the codes are cut and what CID each names. Identity
		// is one answer and a CMap the document carries is another; a
		// predefined name is data this module does not have, and the check is
		// skipped rather than run against a guess — reading UniJIS-UCS2-H as
		// Identity would find CID 0 wherever the file happens to hold two zero
		// bytes, which is a report about nothing. LoadCMap records that skip
		// itself, so it is asked only for a font that shows text.
		if u == nil {
			continue
		}
		cmap, r := core.LoadCMap(d, fontDict)
		if r != core.ReasonOK {
			continue
		}
		found := false
		for _, s := range u.Strings {
			for _, code := range cmap.Decode(s) {
				if code.Mapped && code.CID == 0 {
					found = true
				}
			}
		}
		if found {
			v = append(v, Violation{Clause: "7.21.8", Message: "a text-showing operator references the .notdef glyph (CID 0)", Object: d.ObjNumOf(fontDict)})
		}
	}
	return v
}

// isSubsetFont reports whether a font dictionary's BaseFont carries the six
// uppercase letters + '+' subset tag (e.g. ABCDEF+Arial).
func isSubsetFont(d core.View, fontDict *object.Dictionary) bool {
	bf, _ := d.ResolveName(fontDict.Get("BaseFont"))
	if len(bf) < 7 || bf[6] != '+' {
		return false
	}
	for i := 0; i < 6; i++ {
		if bf[i] < 'A' || bf[i] > 'Z' {
			return false
		}
	}
	return true
}

// checkUAReferenceXObjects flags reference XObjects — Form XObjects carrying a
// /Ref entry, which import content from an external file — which PDF/UA forbids
// (7.20).
func checkUAReferenceXObjects(d core.View) []Violation {
	var v []Violation
	for _, r := range d.ReachableDicts() {
		dict, num := r.Dict, r.ObjNum
		if st, _ := d.ResolveName(dict.Get("Subtype")); st != "Form" {
			continue
		}
		if ty, _ := d.ResolveName(dict.Get("Type")); ty != "" && ty != "XObject" {
			continue
		}
		if dict.Get("Ref") != nil {
			v = append(v, Violation{Clause: "7.20", Message: "reference XObject (Form XObject with /Ref) is not permitted", Object: num})
		}
	}
	return v
}

// checkUAMediaClips requires every media clip data dictionary (Type /MediaClip)
// to carry both the /CT (content type) and /Alt (alternate text) keys
// (7.18.6.2). Media clips are typically inline dictionaries nested inside a
// Screen annotation's Rendition action, so every dictionary the document
// reaches, direct ones included, is examined.
func checkUAMediaClips(d core.View) []Violation {
	var v []Violation
	for _, r := range d.ReachableDicts() {
		mc, num := r.Dict, r.ObjNum
		if t, _ := d.ResolveName(mc.Get("Type")); t != "MediaClip" {
			continue
		}
		if mc.Get("CT") == nil {
			v = append(v, Violation{Clause: "7.18.6.2", Message: "media clip data dictionary has no /CT (content type)", Object: num})
		}
		if mc.Get("Alt") == nil {
			v = append(v, Violation{Clause: "7.18.6.2", Message: "media clip data dictionary has no /Alt (alternate text)", Object: num})
		} else if !altArrayHasText(d, mc.Get("Alt")) {
			v = append(v, Violation{Clause: "7.18.6.2", Message: "media clip data dictionary /Alt is empty", Object: num})
		}
	}
	return v
}

// altArrayHasText reports whether an /Alt value carries at least one non-empty
// text string. Media-clip /Alt is an array of alternating culture/text strings;
// a plain string is also accepted.
func altArrayHasText(d core.View, o object.Object) bool {
	if a, ok := d.Resolve(o).(object.Array); ok {
		for _, e := range a {
			if d.NonEmptyStringOrLocked(e) {
				return true
			}
		}
		return false
	}
	return d.NonEmptyStringOrLocked(o)
}

// checkUALang enforces that any /Lang value present — in the catalog or on a
// structure element — is a syntactically valid BCP 47 language tag (7.2, CosLang
// rule of the UA profile). An empty /Lang is permitted (it defers to an
// ancestor); a present but malformed tag is not.
func checkUALang(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	// /Lang is a text string, and a UTF-16 "en-US" is as valid a language tag
	// as a PDFDocEncoded one: decode before judging it.
	// A Locked document's /Lang is ciphertext, and judging it was how every
	// encrypted file got a "not a valid language identifier" (audit 2026-09-22
	// C63): only a value that was read is judged.
	if s, r := d.StringValue(cat.Get("Lang")); r == core.ReasonOK && len(s.Value) > 0 && !core.ValidBCP47(core.DecodePDFTextString(s.Value)) {
		v = append(v, Violation{Clause: "7.2", Message: "catalog /Lang " + quote(core.DecodePDFTextString(s.Value)) + " is not a valid language identifier", Object: 0})
	}
	walkStructElems(d, cat, func(elem *object.Dictionary, _ object.Name) {
		if s, r := d.StringValue(elem.Get("Lang")); r == core.ReasonOK && len(s.Value) > 0 && !core.ValidBCP47(core.DecodePDFTextString(s.Value)) {
			v = append(v, Violation{Clause: "7.2", Message: "structure element /Lang " + quote(core.DecodePDFTextString(s.Value)) + " is not a valid language identifier", Object: 0})
		}
	})
	return v
}

func quote(s string) string { return "\"" + s + "\"" }

// checkUAOptionalContent enforces the optional-content configuration
// requirements (7.10): every OC configuration dictionary — the /D default and
// each entry of /Configs — must carry a non-empty /Name and must not contain an
// /AS key (which would make visibility depend on usage/state).
func checkUAOptionalContent(d core.View, cat *object.Dictionary) []Violation {
	ocp := d.ResolveDict(cat.Get("OCProperties"))
	if ocp == nil {
		return nil
	}
	var v []Violation
	check := func(cfg *object.Dictionary) {
		if cfg == nil {
			return
		}
		if !d.NonEmptyStringOrLocked(cfg.Get("Name")) {
			v = append(v, Violation{Clause: "7.10", Message: "optional-content configuration dictionary has no non-empty /Name", Object: 0})
		}
		if cfg.Get("AS") != nil {
			v = append(v, Violation{Clause: "7.10", Message: "optional-content configuration dictionary must not contain an /AS key", Object: 0})
		}
	}
	check(d.ResolveDict(ocp.Get("D")))
	if cfgs, ok := d.Resolve(ocp.Get("Configs")).(object.Array); ok {
		for _, c := range cfgs {
			check(d.ResolveDict(c))
		}
	}
	return v
}

// checkUAEmbeddedFiles requires every embedded-file specification (a file spec
// with an /EF entry) to carry non-empty /F and /UF file names (7.11).
//
// A file specification is often written directly — inside the EmbeddedFiles
// name tree's /Names array or an annotation's /FS — so every dictionary the
// document reaches is examined, not only those that are objects of their own
// (audit 2026-09-22 C83).
func checkUAEmbeddedFiles(d core.View) []Violation {
	var v []Violation
	for _, r := range d.ReachableDicts() {
		fs, num := r.Dict, r.ObjNum
		if r.Stream != nil || fs.Get("EF") == nil {
			continue
		}
		if t, _ := d.ResolveName(fs.Get("Type")); t != "" && t != "Filespec" {
			continue
		}
		if !d.NonEmptyStringOrLocked(fs.Get("F")) || !d.NonEmptyStringOrLocked(fs.Get("UF")) {
			v = append(v, Violation{Clause: "7.11", Message: "embedded-file specification must have non-empty /F and /UF keys", Object: num})
		}
	}
	return v
}

// effectiveFieldTU reports whether a Widget/field has a user-facing
// description (/TU), following the /Parent field chain (bounded and
// cycle-guarded) since a terminal Widget may inherit /TU from its parent
// field. A /TU that is ciphertext counts as one (NonEmptyStringOrLocked).
func effectiveFieldTU(d core.View, a *object.Dictionary) bool {
	seen := map[*object.Dictionary]bool{}
	cur := a
	for i := 0; i < 32 && cur != nil && !seen[cur]; i++ {
		seen[cur] = true
		if d.NonEmptyStringOrLocked(cur.Get("TU")) {
			return true
		}
		cur = d.ResolveDict(cur.Get("Parent"))
	}
	return false
}

// checkUAFieldDescription enforces 7.18.1 for form fields with multiple widgets:
// the accessible description /TU belongs on the field, not on its widget
// annotations. When a field carries no /TU of its own but a pure-widget child
// (a /Widget with no /T, i.e. not itself a named sub-field) carries a /TU, the
// description is misplaced. A widget child that is a named sub-field (has /T) is
// exempt, as is a field that supplies its own /TU.
func checkUAFieldDescription(d core.View, cat *object.Dictionary) []Violation {
	form := d.ResolveDict(cat.Get("AcroForm"))
	if form == nil {
		return nil
	}
	var v []Violation
	seen := map[int]bool{}
	var walk func(node object.Object)
	walk = func(node object.Object) {
		if ref, ok := node.(object.IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		fd := d.ResolveDict(node)
		if fd == nil {
			return
		}
		_, hasFT := d.ResolveName(fd.Get("FT"))
		kids, _ := d.Resolve(fd.Get("Kids")).(object.Array)
		if hasFT && !d.NonEmptyStringOrLocked(fd.Get("TU")) {
			for _, kr := range kids {
				kd := d.ResolveDict(kr)
				if kd == nil {
					continue
				}
				st, _ := d.ResolveName(kd.Get("Subtype"))
				kt, rt := d.StringValue(kd.Get("T"))
				ktu, rtu := d.StringValue(kd.Get("TU"))
				if rt == core.ReasonLocked || rtu == core.ReasonLocked {
					continue // ciphertext: which of the two is empty is unknown
				}
				if st == "Widget" && len(kt.Value) == 0 && len(ktu.Value) > 0 {
					v = append(v, Violation{Clause: "7.18.1", Message: "form field has no /TU; its accessible description is misplaced on a widget annotation", Object: d.ObjNumOf(fd)})
					break
				}
			}
		}
		for _, kr := range kids {
			walk(kr)
		}
	}
	if fields, ok := d.Resolve(form.Get("Fields")).(object.Array); ok {
		for _, fr := range fields {
			walk(fr)
		}
	}
	return v
}

// checkUAXFA flags a dynamic XFA form (dynamicRender = required), which PDF/UA
// forbids (Matterhorn 25-001).
func checkUAXFA(d core.View, cat *object.Dictionary) []Violation {
	form := d.ResolveDict(cat.Get("AcroForm"))
	if form == nil {
		return nil
	}
	var xfa []byte
	switch v := d.Resolve(form.Get("XFA")).(type) {
	case *object.Stream:
		xfa, _ = d.Content(v) // reason: presence-only; the producer recorded any declined trip
	case object.Array:
		for _, e := range v {
			if st, ok := d.Resolve(e).(*object.Stream); ok {
				data, _ := d.Content(st) // reason: presence-only; the producer recorded any declined trip
				xfa = append(xfa, data...)
			}
		}
	}
	if dynamicXFARequired(xfa) {
		return []Violation{{Clause: "7.15", Message: "dynamic XFA forms are not permitted (dynamicRender required)", Object: 0}}
	}
	return nil
}

// dynamicXFARequired reports whether an XFA config declares dynamic rendering.
func dynamicXFARequired(xfa []byte) bool {
	i := bytesIndexFold(xfa, "dynamicRender")
	if i < 0 {
		return false
	}
	return bytesIndexFold(xfa[i:min(i+64, len(xfa))], "required") >= 0
}

func bytesIndexFold(b []byte, sub string) int {
	return strings.Index(strings.ToLower(string(b)), strings.ToLower(sub))
}

// checkUATitle requires the XMP metadata to carry a document title (dc:title),
// which together with /DisplayDocTitle makes assistive tools announce the title
// rather than the file name (Matterhorn checkpoint 06).
func checkUATitle(d core.View, cat *object.Dictionary) []Violation {
	stream, ok := d.Resolve(cat.Get("Metadata")).(*object.Stream)
	if !ok {
		return nil // absence of metadata is already reported by the identifier check
	}
	// Read through the XMP model: a dc:title is a property in the Dublin Core
	// namespace, not the string "dc:title" somewhere in the packet — which a
	// comment, or a title that merely mentions it, would supply.
	packet, status := d.XMPPacketOf(stream, object.RefNum(cat.Get("Metadata")))
	switch status {
	case core.XMPLimit, core.XMPMalformed:
		// Not read; the limit finding or the identifier check says why.
		return nil
	case core.XMPParsed:
		if _, ok := packet.Get(xmp.NSDC, "title"); ok {
			return nil
		}
	}
	return []Violation{{Clause: "7.1", Message: "XMP metadata has no document title (dc:title)", Object: 0}}
}

// checkUAFonts flags fonts used for rendering but not embedded. It considers
// only fonts actually shown (the executed-content model), so unused or invisible
// font dictionaries are not false-flagged.
func checkUAFonts(d core.View) []Violation {
	var v []Violation
	for fontDict := range core.CollectFontTextUsage(d) {
		st, _ := d.ResolveName(fontDict.Get("Subtype"))
		if st == "Type3" {
			continue // procedural glyphs, no font program
		}
		embedded := fontProgramEmbedded(d, fontDict)
		if st == "Type0" {
			if df, _ := d.Resolve(fontDict.Get("DescendantFonts")).(object.Array); len(df) > 0 {
				if cid := d.ResolveDict(df[0]); cid != nil {
					embedded = fontProgramEmbedded(d, cid)
				}
			}
		}
		if !embedded {
			v = append(v, Violation{Clause: "7.21.4.1", Message: "font used for rendering is not embedded", Object: d.ObjNumOf(fontDict)})
		}
	}
	return v
}

// checkUACharMapping flags text shown with a font whose character codes cannot
// be mapped to Unicode. The clear, false-positive-free case: a composite
// (Type0) font with Identity encoding and no ToUnicode CMap — its codes are
// CIDs with no defined Unicode mapping (Matterhorn 10-001).
func checkUACharMapping(d core.View) []Violation {
	var v []Violation
	for fontDict := range core.CollectFontTextUsage(d) {
		if fontDict.Get("ToUnicode") != nil {
			continue
		}
		if st, _ := d.ResolveName(fontDict.Get("Subtype")); st != "Type0" {
			continue
		}
		if enc, _ := d.Resolve(fontDict.Get("Encoding")).(object.Name); enc == "Identity-H" || enc == "Identity-V" {
			v = append(v, Violation{Clause: "7.2", Message: "text uses a composite font with Identity encoding and no ToUnicode CMap; its character codes cannot be mapped to Unicode", Object: d.ObjNumOf(fontDict)})
		}
	}
	return v
}

// checkUAFontDicts enforces dictionary-level font requirements from clause 7.21
// on the fonts actually used for rendering (executed-content model):
//   - 7.21.3.2 an embedded CIDFontType2 must carry a /CIDToGIDMap;
//   - 7.21.6   a symbolic TrueType font must not have an /Encoding entry, and a
//     non-symbolic one must use MacRomanEncoding or WinAnsiEncoding.
func checkUAFontDicts(d core.View) []Violation {
	var v []Violation
	for fontDict := range core.CollectFontTextUsage(d) {
		v = append(v, checkOneUAFontDict(d, fontDict)...)
	}
	return v
}

// checkOneUAFontDict applies the dictionary-level clause 7.21 rules to a single
// font dictionary.
func checkOneUAFontDict(d core.View, fontDict *object.Dictionary) []Violation {
	var v []Violation
	st, _ := d.ResolveName(fontDict.Get("Subtype"))
	num := d.ObjNumOf(fontDict)
	switch st {
	case "Type0":
		df, _ := d.Resolve(fontDict.Get("DescendantFonts")).(object.Array)
		if len(df) == 0 {
			return nil
		}
		cid := d.ResolveDict(df[0])
		if cid == nil {
			return nil
		}
		cst, _ := d.ResolveName(cid.Get("Subtype"))
		if cst == "CIDFontType2" && fontProgramEmbedded(d, cid) && cid.Get("CIDToGIDMap") == nil {
			v = append(v, Violation{Clause: "7.21.3.2", Message: "embedded CIDFontType2 font has no /CIDToGIDMap", Object: num})
		}
		// /CIDToGIDMap, when it is a name, must be exactly "Identity"; any other
		// name (e.g. "NoIdentity" or empty) is invalid (ISO 32000-1 9.7.4.3).
		if m, ok := d.Resolve(cid.Get("CIDToGIDMap")).(object.Name); ok && m != "Identity" {
			v = append(v, Violation{Clause: "7.21.3.2", Message: "/CIDToGIDMap name value must be Identity, got /" + string(m), Object: num})
		}
	case "TrueType":
		symbolic := fontIsSymbolic(d, fontDict)
		enc := d.Resolve(fontDict.Get("Encoding"))
		if symbolic {
			if enc != nil {
				if _, isNull := enc.(object.Null); !isNull {
					v = append(v, Violation{Clause: "7.21.6", Message: "symbolic TrueType font must not contain an /Encoding entry", Object: num})
				}
			}
			return v
		}
		base, _ := enc.(object.Name)
		if ed := d.ResolveDict(fontDict.Get("Encoding")); ed != nil {
			base, _ = d.ResolveName(ed.Get("BaseEncoding"))
		}
		if base != "MacRomanEncoding" && base != "WinAnsiEncoding" {
			v = append(v, Violation{Clause: "7.21.6", Message: "non-symbolic TrueType font must use MacRomanEncoding or WinAnsiEncoding", Object: num})
		}
	}
	return v
}

// fontIsSymbolic reports whether a font's descriptor marks it symbolic (Flags
// bit 3, value 4).
func fontIsSymbolic(d core.View, font *object.Dictionary) bool {
	fd := d.ResolveDict(font.Get("FontDescriptor"))
	if fd == nil {
		return false
	}
	flags, _ := d.Resolve(fd.Get("Flags")).(object.Integer)
	return int(flags)&0x4 != 0
}

func fontProgramEmbedded(d core.View, font *object.Dictionary) bool {
	fd := d.ResolveDict(font.Get("FontDescriptor"))
	if fd == nil {
		return false
	}
	return fd.Get("FontFile") != nil || fd.Get("FontFile2") != nil || fd.Get("FontFile3") != nil
}

// checkFigureAlt walks the structure tree and flags Figure elements that carry
// neither /Alt nor /ActualText. A figure is an element whose resolved type is
// Figure: a custom /Img role-mapped to /Figure is one, and asking the written
// /S let it through without alternate text (audit 2026-09-22 C80).
func checkFigureAlt(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	for _, n := range structTree(d, cat) {
		if !n.HasS || n.StdType != "Figure" {
			continue
		}
		if !d.NonEmptyStringOrLocked(n.Elem.Get("Alt")) && !d.NonEmptyStringOrLocked(n.Elem.Get("ActualText")) {
			v = append(v, Violation{Clause: "7.3", Message: "figure structure element has no non-empty alternate text (/Alt or /ActualText)", Object: 0})
		}
	}
	return v
}

func RunCheck(check func() []Violation) (out []Violation) {
	defer func() {
		if r := recover(); r != nil {
			out = []Violation{{Clause: finding.InternalRule, Message: finding.InternalMessage(r)}}
		}
	}()
	return check()
}
