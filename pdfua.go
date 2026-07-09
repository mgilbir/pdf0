package pdf0

import (
	"fmt"
	"strings"
)

// UAViolation is a PDF/UA-1 (ISO 14289-1) accessibility conformance failure.
type UAViolation struct {
	Clause  string // ISO 14289-1 clause
	Message string
	Object  int
}

func (v UAViolation) Error() string {
	if v.Object != 0 {
		return fmt.Sprintf("[PDF/UA-1 %s] object %d: %s", v.Clause, v.Object, v.Message)
	}
	return fmt.Sprintf("[PDF/UA-1 %s] %s", v.Clause, v.Message)
}

// ValidatePDFUA checks a document against the foundational PDF/UA-1 (ISO
// 14289-1) requirements: the document must be tagged, carry a structure tree
// and a default language, be configured to display its title, and give every
// figure alternate text. It is a partial validator — a clean result means the
// implemented checks passed, not full PDF/UA conformance.
func ValidatePDFUA(doc *Document) []UAViolation {
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		return []UAViolation{{"7.1", "document has no catalog", 0}}
	}
	var v []UAViolation

	// 7.1 — the file must be a tagged PDF.
	mark := doc.ResolveDict(cat.Get("MarkInfo"))
	if mark == nil || !doc.isTrue(mark.Get("Marked")) {
		v = append(v, UAViolation{"7.1", "document is not marked as tagged (/MarkInfo << /Marked true >>)", 0})
	}
	if cat.Get("StructTreeRoot") == nil {
		v = append(v, UAViolation{"7.1", "document has no structure tree (/StructTreeRoot)", 0})
	}

	// 7.2 — a default natural language must be set.
	if s, _ := doc.Resolve(cat.Get("Lang")).(String); len(s.Value) == 0 {
		v = append(v, UAViolation{"7.2", "document does not specify a default language (catalog /Lang)", 0})
	}

	// 7.1 — the document title must be shown in the window title bar.
	vp := doc.ResolveDict(cat.Get("ViewerPreferences"))
	if vp == nil || !doc.isTrue(vp.Get("DisplayDocTitle")) {
		v = append(v, UAViolation{"7.1", "/ViewerPreferences /DisplayDocTitle must be true", 0})
	}

	// 5 — the file must declare PDF/UA conformance in its XMP metadata.
	v = append(v, doc.checkUAIdentifier(cat)...)

	// Matterhorn checkpoint 06: the document must have an XMP dc:title.
	v = append(v, doc.checkUATitle(cat)...)

	// 7.21 — every font used for rendering must be embedded.
	v = append(v, doc.checkUAFonts()...)
	v = append(v, doc.checkUAFontDicts()...)
	v = append(v, doc.checkUACMaps()...)
	v = append(v, doc.checkUACMapWMode()...)
	v = append(v, doc.checkUACIDSystemInfo()...)
	v = append(v, doc.checkUAToUnicodeValues()...)
	v = append(v, doc.checkUAFontSubsetGlyphs()...)
	v = append(v, doc.checkUANotdefCID()...)

	// 7.2 — text must map to Unicode (Matterhorn 10-001).
	v = append(v, doc.checkUACharMapping()...)

	// 7.18.3 — pages with annotations must use structure tab order.
	v = append(v, doc.checkUATabOrder()...)

	// 7.1 — structure types must be standard or mapped via /RoleMap.
	v = append(v, doc.checkUARoleMap(cat)...)
	v = append(v, doc.checkUARoleMapIntegrity(cat)...)
	v = append(v, doc.checkUAStructParent(cat)...)

	// 7.2 — structure-element nesting (tables, lists, TOC) per the UA profile.
	v = append(v, doc.checkUAStructNesting(cat)...)
	v = append(v, doc.checkUATableListStructure(cat)...)
	v = append(v, doc.checkUATableGrid(cat)...)

	// 7.4 — heading levels must not be skipped; start at H1; one <H> per node.
	v = append(v, doc.checkUAHeadings(cat)...)
	v = append(v, doc.checkUAOneHPerNode(cat)...)

	// 7.16 — encryption must not disable accessibility (Matterhorn 26).
	v = append(v, doc.checkUASecurity()...)

	// 7.18.2 — forbidden annotation subtypes (Matterhorn 28-007).
	v = append(v, doc.checkUAAnnotations()...)

	// 7.15 — dynamic XFA is forbidden (Matterhorn 25-001).
	v = append(v, doc.checkUAXFA(cat)...)

	// 7.18.6.2 — media clip data dictionaries need /CT and /Alt.
	v = append(v, doc.checkUAMediaClips()...)

	// 7.20 — reference XObjects are forbidden.
	v = append(v, doc.checkUAReferenceXObjects()...)

	// 7.2 — any present /Lang must be a valid BCP 47 tag.
	v = append(v, doc.checkUALang(cat)...)

	// 7.10 — optional-content config; 7.11 — embedded-file specifications.
	v = append(v, doc.checkUAOptionalContent(cat)...)
	v = append(v, doc.checkUAEmbeddedFiles()...)

	// 6.1 — PDF/UA-1 requires a 1.x header.
	v = append(v, doc.checkUAHeaderVersion()...)

	// 7.1 — Suspects must not be true; 7.4.4 strong/weak; 7.9 Note IDs.
	v = append(v, doc.checkUASuspects(cat)...)
	v = append(v, doc.checkUAStrongWeak(cat)...)
	v = append(v, doc.checkUANotes(cat)...)

	// 7.1 — real content must be tagged or marked as an artifact (Matterhorn 01).
	v = append(v, doc.checkUARealContent(cat)...)

	// 7.18 — annotation must sit under the right structure element (28-010/011).
	v = append(v, doc.checkUAAnnotStructType(cat)...)

	// 7.3 — every figure needs alternate text.
	v = append(v, doc.checkFigureAlt(cat)...)
	return v
}

// checkUAHeadings flags a skipped numbered-heading level (e.g. H1 followed by H3
// without an intervening H2), walking the structure tree in document order.
func (d *Document) checkUAHeadings(cat *Dictionary) []UAViolation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	var levels []int
	seen := map[int]bool{}
	var walk func(node Object)
	walk = func(node Object) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, kid := range arr {
					walk(kid)
				}
			}
			return
		}
		if st, ok := elem.Get("S").(Name); ok && len(st) == 2 && st[0] == 'H' && st[1] >= '1' && st[1] <= '6' {
			levels = append(levels, int(st[1]-'0'))
		}
		if k := elem.Get("K"); k != nil {
			switch kids := d.Resolve(k).(type) {
			case Array:
				for _, kid := range kids {
					walk(kid)
				}
			default:
				walk(k)
			}
		}
	}
	walk(root.Get("K"))

	var v []UAViolation
	// 7.4.2: a strongly structured document's first numbered heading must be H1.
	if len(levels) > 0 && levels[0] != 1 {
		v = append(v, UAViolation{"7.4.2", fmt.Sprintf("first heading is H%d; a strongly structured document must start at H1", levels[0]), 0})
	}
	prev := 0
	for _, lvl := range levels {
		if prev != 0 && lvl > prev+1 {
			v = append(v, UAViolation{"7.4", fmt.Sprintf("heading level H%d follows H%d, skipping a level", lvl, prev), 0})
		}
		prev = lvl
	}
	return v
}

// checkUAOneHPerNode enforces 7.4.4: in a weakly structured document each
// structure node may contain at most one child <H> heading.
func (d *Document) checkUAOneHPerNode(cat *Dictionary) []UAViolation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	var v []UAViolation
	seen := map[int]bool{}
	var walk func(node Object)
	walk = func(node Object) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, kid := range arr {
					walk(kid)
				}
			}
			return
		}
		hCount := 0
		for _, ct := range d.childStructTypes(elem, roleMap) {
			if ct == "H" {
				hCount++
			}
		}
		if hCount > 1 {
			v = append(v, UAViolation{"7.4.4", "a structure node contains more than one child <H> heading", 0})
		}
		for _, kid := range d.structKids(elem) {
			walk(kid)
		}
	}
	walk(root.Get("K"))
	return v
}

// standardStructTypes are the ISO 32000 standard structure types (Table 333/337).
var standardStructTypes = map[Name]bool{
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

// checkUATabOrder requires structure tab order on pages that carry annotations.
func (d *Document) checkUATabOrder() []UAViolation {
	var v []UAViolation
	for _, pg := range collectPages(d, d.catalogPages()) {
		annots, _ := d.Resolve(pg.dict.Get("Annots")).(Array)
		if len(annots) == 0 {
			continue
		}
		if tabs, _ := d.Resolve(pg.dict.Get("Tabs")).(Name); tabs != "S" {
			v = append(v, UAViolation{"7.18.3", "page with annotations must set /Tabs /S (structure tab order)", pg.objNum})
		}
	}
	return v
}

// checkUARoleMap flags structure element types that are neither standard nor
// mapped to a standard type through the structure tree's /RoleMap.
func (d *Document) checkUARoleMap(cat *Dictionary) []UAViolation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	mapped := func(t Name) bool {
		if standardStructTypes[t] {
			return true
		}
		if roleMap == nil {
			return false
		}
		to, _ := d.Resolve(roleMap.Get(t)).(Name)
		return standardStructTypes[to]
	}
	var v []UAViolation
	seen := map[int]bool{}
	reported := map[Name]bool{}
	var walk func(node Object)
	walk = func(node Object) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, kid := range arr {
					walk(kid)
				}
			}
			return
		}
		if st, ok := elem.Get("S").(Name); ok && st != "" && !mapped(st) && !reported[st] {
			reported[st] = true
			v = append(v, UAViolation{"7.1", "structure type /" + string(st) + " is neither standard nor mapped in /RoleMap", 0})
		}
		if k := elem.Get("K"); k != nil {
			switch kids := d.Resolve(k).(type) {
			case Array:
				for _, kid := range kids {
					walk(kid)
				}
			default:
				walk(k)
			}
		}
	}
	walk(root.Get("K"))
	return v
}

// checkUAIdentifier requires an XMP metadata stream declaring the PDF/UA part.
func (d *Document) checkUAIdentifier(cat *Dictionary) []UAViolation {
	stream, ok := d.Resolve(cat.Get("Metadata")).(*Stream)
	if !ok {
		return []UAViolation{{"5", "document has no XMP metadata (a PDF/UA identifier is required)", 0}}
	}
	xmp := decodeXMPToUTF8(stream.Data)
	if !strings.Contains(xmp, "pdfuaid:part") {
		return []UAViolation{{"5", "XMP metadata does not declare the PDF/UA part (pdfuaid:part)", 0}}
	}
	if part := xmpPDFUAPart(xmp); part != "" && part != "1" {
		return []UAViolation{{"5", "pdfuaid:part must be 1 for PDF/UA-1, got " + part, 0}}
	}
	return nil
}

// xmpPDFUAPart extracts the pdfuaid:part value from an XMP packet, handling both
// the attribute form (pdfuaid:part="1") and the element form
// (<pdfuaid:part>1</pdfuaid:part>). It returns "" if no value is found.
func xmpPDFUAPart(xmp string) string {
	i := strings.Index(xmp, "pdfuaid:part")
	if i < 0 {
		return ""
	}
	rest := xmp[i+len("pdfuaid:part"):]
	// Attribute form: ="N"
	if j := strings.IndexAny(rest, "=>"); j >= 0 && rest[j] == '=' {
		k := strings.IndexAny(rest[j:], "\"'")
		if k >= 0 {
			q := rest[j+k+1:]
			if e := strings.IndexAny(q, "\"'"); e >= 0 {
				return strings.TrimSpace(q[:e])
			}
		}
		return ""
	}
	// Element form: >N<
	if j := strings.IndexByte(rest, '>'); j >= 0 {
		q := rest[j+1:]
		if e := strings.IndexByte(q, '<'); e >= 0 {
			return strings.TrimSpace(q[:e])
		}
	}
	return ""
}

// checkUAStructParent flags a structure element that lacks the required /P
// (parent) entry (7.1, ISO 32000-1 14.7.2 Table 323).
func (d *Document) checkUAStructParent(cat *Dictionary) []UAViolation {
	var v []UAViolation
	d.walkStructElems(cat, func(elem *Dictionary, t Name) {
		if elem.Get("P") == nil {
			v = append(v, UAViolation{"7.1", "structure element <" + string(t) + "> has no /P (parent) entry", 0})
		}
	})
	return v
}

// checkUARoleMapIntegrity flags a /RoleMap that remaps a standard structure type
// or contains a circular mapping (7.1).
func (d *Document) checkUARoleMapIntegrity(cat *Dictionary) []UAViolation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	if roleMap == nil {
		return nil
	}
	var v []UAViolation
	for _, key := range roleMap.Keys {
		if standardStructTypes[key] {
			v = append(v, UAViolation{"7.1", "/RoleMap remaps standard structure type <" + string(key) + ">", 0})
		}
		// Follow the mapping chain from key; a repeat is a cycle.
		seen := map[Name]bool{key: true}
		cur := key
		for {
			next, ok := d.Resolve(roleMap.Get(cur)).(Name)
			if !ok || next == "" {
				break
			}
			if seen[next] {
				v = append(v, UAViolation{"7.1", "/RoleMap contains a circular mapping involving <" + string(key) + ">", 0})
				break
			}
			seen[next] = true
			cur = next
		}
	}
	return v
}

// checkUASecurity flags an encrypted document that lacks a /P entry or whose
// permissions disable text extraction for accessibility (Matterhorn 26-001/002).
func (d *Document) checkUASecurity() []UAViolation {
	enc := d.ResolveDict(d.Trailer.Get("Encrypt"))
	if enc == nil {
		return nil
	}
	p, ok := d.Resolve(enc.Get("P")).(Integer)
	if !ok {
		return []UAViolation{{"7.16", "encrypted document has no /P permissions entry", 0}}
	}
	if uint32(int32(p))&0x200 == 0 { // bit position 10: extract for accessibility
		return []UAViolation{{"7.16", "encryption disables text extraction for accessibility (permission bit 10)", 0}}
	}
	return nil
}

// checkUAAnnotations flags forbidden annotation subtypes. Hidden and Popup
// annotations are exempt from checkpoint 28.
func (d *Document) checkUAAnnotations() []UAViolation {
	var v []UAViolation
	for num, iobj := range d.Objects {
		a, ok := iobj.Value.(*Dictionary)
		if !ok || !isAnnotation(a) {
			continue
		}
		st, _ := a.Get("Subtype").(Name)
		if st == "Popup" {
			continue
		}
		if f, _ := d.Resolve(a.Get("F")).(Integer); int(f)&0x2 != 0 {
			continue // hidden
		}
		if st == "TrapNet" {
			v = append(v, UAViolation{"7.18.2", "TrapNet annotations are not permitted", num})
		}
		// 28-012: a Link annotation needs an alternate description in /Contents.
		if st == "Link" {
			if c, _ := d.Resolve(a.Get("Contents")).(String); len(c.Value) == 0 {
				v = append(v, UAViolation{"7.18.5", "Link annotation has no alternate description (/Contents)", num})
			}
		}
		// 7.18.1: a form-field Widget must have a non-empty field description /TU
		// (own or inherited from its parent field) or an /Alt on the widget.
		if st == "Widget" {
			alt, _ := d.Resolve(a.Get("Alt")).(String)
			if len(d.effectiveFieldTU(a)) == 0 && len(alt.Value) == 0 {
				v = append(v, UAViolation{"7.18.1", "form-field Widget has neither a field description (/TU) nor an /Alt", num})
			}
		}
		// 7.18.1: every other visible annotation (not a Widget, which has its own
		// TU/Alt rule, and not a PrinterMark artifact) must carry an alternate
		// description in /Contents or /Alt.
		if st != "Widget" && st != "Link" && st != "PrinterMark" {
			c, _ := d.Resolve(a.Get("Contents")).(String)
			alt, _ := d.Resolve(a.Get("Alt")).(String)
			if len(c.Value) == 0 && len(alt.Value) == 0 {
				v = append(v, UAViolation{"7.18.1", "annotation of subtype /" + string(st) + " has no alternate description (/Contents or /Alt)", num})
			}
		}
		// 7.18.8: a PrinterMark is an incidental artifact and must NOT be tagged;
		// a visible one carrying a /StructParent is a violation.
		if st == "PrinterMark" {
			if a.Get("StructParent") != nil {
				v = append(v, UAViolation{"7.18.8", "PrinterMark annotation must be an artifact, not tagged (has /StructParent)", num})
			}
			continue
		}
		// 28-002/010/011: every other visible annotation must be represented in the
		// structure tree — it carries a /StructParent linking it to a structure
		// element. (Hidden and Popup annotations were already skipped above.)
		if a.Get("StructParent") == nil {
			v = append(v, UAViolation{"7.18.1", "annotation is not tagged (no /StructParent linking it to the structure tree)", num})
		}
	}
	return v
}

// isPredefinedCMap reports whether name is a predefined CMap from ISO 32000-1,
// 9.7.5.2, Table 118 (the predefinedCMaps table, which includes Identity-H/V).
func isPredefinedCMap(name Name) bool {
	_, ok := predefinedCMaps[string(name)]
	return ok
}

// checkUACMaps enforces that a Type 0 font's CMap is either predefined (Table
// 118) or embedded, and that an embedded CMap's /UseCMap references only a
// predefined CMap (7.21.3.3). Only fonts used for rendering are considered.
func (d *Document) checkUACMaps() []UAViolation {
	var v []UAViolation
	for fontDict := range collectFontTextUsage(d) {
		v = append(v, d.checkOneUACMap(fontDict)...)
	}
	return v
}

// checkUACIDSystemInfo enforces that a composite font's CIDFont CIDSystemInfo
// matches the Registry and Ordering implied by its CMap encoding (7.21.3.1).
// Identity encodings are exempt; the check runs only when both sides declare a
// Registry and Ordering.
func (d *Document) checkUACIDSystemInfo() []UAViolation {
	var v []UAViolation
	for fontDict := range collectFontTextUsage(d) {
		v = append(v, d.checkOneUACIDSystemInfo(fontDict)...)
	}
	return v
}

func (d *Document) checkOneUACIDSystemInfo(fontDict *Dictionary) []UAViolation {
	if st, _ := fontDict.Get("Subtype").(Name); st != "Type0" {
		return nil
	}
	var wantReg, wantOrd string
	switch enc := d.Resolve(fontDict.Get("Encoding")).(type) {
	case Name:
		if enc == "Identity-H" || enc == "Identity-V" {
			return nil
		}
		info, ok := predefinedCMaps[string(enc)]
		if !ok {
			return nil
		}
		wantReg, wantOrd = info.Registry, info.Ordering
	case *Stream:
		wantReg, wantOrd = d.cidSystemInfo(&enc.Dict)
	default:
		return nil
	}
	if wantReg == "" && wantOrd == "" {
		return nil
	}
	df, _ := d.Resolve(fontDict.Get("DescendantFonts")).(Array)
	if len(df) == 0 {
		return nil
	}
	cid := d.ResolveDict(df[0])
	if cid == nil {
		return nil
	}
	gotReg, gotOrd := d.cidSystemInfo(cid)
	if gotReg == "" && gotOrd == "" {
		return nil
	}
	if gotReg != wantReg || gotOrd != wantOrd {
		return []UAViolation{{"7.21.3.1", "CIDFont CIDSystemInfo (" + gotReg + "-" + gotOrd + ") does not match the CMap (" + wantReg + "-" + wantOrd + ")", d.dictObjNum(fontDict)}}
	}
	return nil
}

// cidSystemInfo returns the Registry and Ordering strings of a dictionary's
// /CIDSystemInfo, or empty strings if absent.
func (d *Document) cidSystemInfo(dict *Dictionary) (string, string) {
	si := d.ResolveDict(dict.Get("CIDSystemInfo"))
	if si == nil {
		return "", ""
	}
	r, _ := d.Resolve(si.Get("Registry")).(String)
	o, _ := d.Resolve(si.Get("Ordering")).(String)
	return string(r.Value), string(o.Value)
}

// checkUACMapWMode enforces that an embedded CMap's /WMode dictionary entry
// matches the WMode declared inside the CMap stream itself (7.21.3.3, CMapFile
// rule). Only Type 0 fonts used for rendering are considered.
func (d *Document) checkUACMapWMode() []UAViolation {
	var v []UAViolation
	for fontDict := range collectFontTextUsage(d) {
		if st, _ := fontDict.Get("Subtype").(Name); st != "Type0" {
			continue
		}
		s, ok := d.Resolve(fontDict.Get("Encoding")).(*Stream)
		if !ok {
			continue
		}
		dictWM := 0
		if w, ok := d.Resolve(s.Dict.Get("WMode")).(Integer); ok {
			dictWM = int(w)
		}
		if inner, found := cmapInnerWMode(decodeContentStream(d, s)); found && inner != dictWM {
			v = append(v, UAViolation{"7.21.3.3", fmt.Sprintf("embedded CMap /WMode %d does not match the WMode %d declared in the CMap stream", dictWM, inner), d.dictObjNum(fontDict)})
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
	start := j
	for j < len(data) && data[j] >= '0' && data[j] <= '9' {
		j++
	}
	if j == start {
		return 0, false
	}
	n := 0
	for _, c := range data[start:j] {
		n = n*10 + int(c-'0')
	}
	return n, true
}

func (d *Document) checkOneUACMap(fontDict *Dictionary) []UAViolation {
	if st, _ := fontDict.Get("Subtype").(Name); st != "Type0" {
		return nil
	}
	num := d.dictObjNum(fontDict)
	switch enc := d.Resolve(fontDict.Get("Encoding")).(type) {
	case Name:
		if !isPredefinedCMap(enc) {
			return []UAViolation{{"7.21.3.3", "Type 0 font uses CMap /" + string(enc) + ", which is neither predefined nor embedded", num}}
		}
	case *Stream:
		if use, ok := enc.Dict.Get("UseCMap").(Name); ok && !isPredefinedCMap(use) {
			return []UAViolation{{"7.21.3.3", "embedded CMap references non-predefined CMap /" + string(use) + " via /UseCMap", num}}
		}
	}
	return nil
}

// checkUAToUnicodeValues flags a rendered font whose ToUnicode CMap maps any
// character code to a forbidden Unicode value (U+0000, U+FEFF, or U+FFFE), which
// carry no usable text meaning (7.21.7).
func (d *Document) checkUAToUnicodeValues() []UAViolation {
	var v []UAViolation
	for fontDict := range collectFontTextUsage(d) {
		if tu, ok := d.Resolve(fontDict.Get("ToUnicode")).(*Stream); ok {
			if hasForbiddenUnicodeTargets(d, tu) {
				v = append(v, UAViolation{"7.21.7", "ToUnicode CMap maps to a forbidden Unicode value (U+0000, U+FEFF or U+FFFE)", d.dictObjNum(fontDict)})
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
func (d *Document) checkUAFontSubsetGlyphs() []UAViolation {
	var v []UAViolation
	for fontDict := range collectFontTextUsage(d) {
		if !isSubsetFont(fontDict) {
			continue
		}
		if st, _ := fontDict.Get("Subtype").(Name); st != "Type1" && st != "MMType1" {
			continue
		}
		fd := d.ResolveDict(fontDict.Get("FontDescriptor"))
		if fd == nil {
			continue
		}
		cs, ok := d.Resolve(fd.Get("CharSet")).(String)
		if !ok {
			continue
		}
		fp := loadFontProgram(d, fd)
		if fp == nil || fp.glyphNames == nil {
			continue
		}
		listed := parseCharSet(string(cs.Value))
		for name := range fp.glyphNames {
			if name == ".notdef" || name == "" {
				continue
			}
			if !listed[name] {
				v = append(v, UAViolation{"7.21.4.2", "FontDescriptor /CharSet does not list glyph " + name + " present in the embedded font program", d.dictObjNum(fontDict)})
				break
			}
		}
	}
	return v
}

// checkUANotdefCID enforces 7.21.8 for composite fonts: a text-showing operator
// must not reference the .notdef glyph. For an Identity-encoded Type 0 font the
// two-byte codes are CIDs, and CID 0 is unambiguously .notdef — a definite
// signal that needs no font-program lookup. The simple-font .notdef case is not
// handled here because it can only be resolved through the font program, where a
// lookup failure is indistinguishable from a genuine .notdef reference.
func (d *Document) checkUANotdefCID() []UAViolation {
	var v []UAViolation
	for fontDict, u := range collectFontTextUsage(d) {
		if st, _ := fontDict.Get("Subtype").(Name); st != "Type0" {
			continue
		}
		if !isIdentityEncoding(d, fontDict) {
			continue
		}
		if u == nil {
			continue
		}
		found := false
		for _, s := range u.strings {
			for i := 0; i+1 < len(s); i += 2 {
				if int(s[i])<<8|int(s[i+1]) == 0 {
					found = true
				}
			}
		}
		if found {
			v = append(v, UAViolation{"7.21.8", "a text-showing operator references the .notdef glyph (CID 0)", d.dictObjNum(fontDict)})
		}
	}
	return v
}

// isSubsetFont reports whether a font dictionary's BaseFont carries the six
// uppercase letters + '+' subset tag (e.g. ABCDEF+Arial).
func isSubsetFont(fontDict *Dictionary) bool {
	bf, _ := fontDict.Get("BaseFont").(Name)
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
func (d *Document) checkUAReferenceXObjects() []UAViolation {
	var v []UAViolation
	d.walkAllDicts(func(dict *Dictionary, num int) {
		if st, _ := dict.Get("Subtype").(Name); st != "Form" {
			return
		}
		if ty, _ := dict.Get("Type").(Name); ty != "" && ty != "XObject" {
			return
		}
		if dict.Get("Ref") != nil {
			v = append(v, UAViolation{"7.20", "reference XObject (Form XObject with /Ref) is not permitted", num})
		}
	})
	return v
}

// checkUAMediaClips requires every media clip data dictionary (Type /MediaClip)
// to carry both the /CT (content type) and /Alt (alternate text) keys
// (7.18.6.2). Media clips are typically inline dictionaries nested inside a
// Screen annotation's Rendition action, so the whole object graph is walked.
func (d *Document) checkUAMediaClips() []UAViolation {
	var v []UAViolation
	d.walkAllDicts(func(mc *Dictionary, num int) {
		if t, _ := mc.Get("Type").(Name); t != "MediaClip" {
			return
		}
		if mc.Get("CT") == nil {
			v = append(v, UAViolation{"7.18.6.2", "media clip data dictionary has no /CT (content type)", num})
		}
		if mc.Get("Alt") == nil {
			v = append(v, UAViolation{"7.18.6.2", "media clip data dictionary has no /Alt (alternate text)", num})
		} else if !d.altArrayHasText(mc.Get("Alt")) {
			v = append(v, UAViolation{"7.18.6.2", "media clip data dictionary /Alt is empty", num})
		}
	})
	return v
}

// altArrayHasText reports whether an /Alt value carries at least one non-empty
// text string. Media-clip /Alt is an array of alternating culture/text strings;
// a plain string is also accepted.
func (d *Document) altArrayHasText(o Object) bool {
	switch a := d.Resolve(o).(type) {
	case String:
		return len(a.Value) > 0
	case Array:
		for _, e := range a {
			if s, ok := d.Resolve(e).(String); ok && len(s.Value) > 0 {
				return true
			}
		}
	}
	return false
}

// walkAllDicts visits every dictionary reachable in the object graph — including
// dictionaries nested inline inside arrays, streams, and other dictionaries —
// exactly once. objNum is the number of the enclosing top-level object.
func (d *Document) walkAllDicts(fn func(dict *Dictionary, objNum int)) {
	seenRef := map[int]bool{}
	seenPtr := map[*Dictionary]bool{}
	var visit func(o Object, objNum int)
	visit = func(o Object, objNum int) {
		switch x := o.(type) {
		case IndirectRef:
			if seenRef[x.Number] {
				return
			}
			seenRef[x.Number] = true
			if io := d.Objects[x.Number]; io != nil {
				visit(io.Value, x.Number)
			}
		case *Dictionary:
			if seenPtr[x] {
				return
			}
			seenPtr[x] = true
			fn(x, objNum)
			for _, val := range x.Values {
				visit(val, objNum)
			}
		case *Stream:
			if !seenPtr[&x.Dict] {
				seenPtr[&x.Dict] = true
				fn(&x.Dict, objNum)
				for _, val := range x.Dict.Values {
					visit(val, objNum)
				}
			}
		case Array:
			for _, e := range x {
				visit(e, objNum)
			}
		}
	}
	for num, io := range d.Objects {
		visit(io.Value, num)
	}
}

// checkUALang enforces that any /Lang value present — in the catalog or on a
// structure element — is a syntactically valid BCP 47 language tag (7.2, CosLang
// rule of the UA profile). An empty /Lang is permitted (it defers to an
// ancestor); a present but malformed tag is not.
func (d *Document) checkUALang(cat *Dictionary) []UAViolation {
	var v []UAViolation
	if s, ok := d.Resolve(cat.Get("Lang")).(String); ok && len(s.Value) > 0 && !validBCP47(string(s.Value)) {
		v = append(v, UAViolation{"7.2", "catalog /Lang " + quote(string(s.Value)) + " is not a valid language identifier", 0})
	}
	d.walkStructElems(cat, func(elem *Dictionary, _ Name) {
		if s, ok := d.Resolve(elem.Get("Lang")).(String); ok && len(s.Value) > 0 && !validBCP47(string(s.Value)) {
			v = append(v, UAViolation{"7.2", "structure element /Lang " + quote(string(s.Value)) + " is not a valid language identifier", 0})
		}
	})
	return v
}

func quote(s string) string { return "\"" + s + "\"" }

// validBCP47 reports whether tag is a syntactically well-formed BCP 47 (RFC
// 5646) language tag. It validates the subtag structure rather than a registry:
// a non-empty primary language of 2–8 letters (or an x-/i- private/grandfathered
// singleton), followed by subtags of 1–8 alphanumerics each.
func validBCP47(tag string) bool {
	subs := strings.Split(tag, "-")
	first := subs[0]
	if len(first) == 1 {
		if first != "x" && first != "i" && first != "X" && first != "I" {
			return false // a lone singleton cannot be the primary language
		}
	} else if len(first) < 2 || len(first) > 8 || !allAlpha(first) {
		return false
	}
	for _, s := range subs[1:] {
		if len(s) < 1 || len(s) > 8 || !allAlnum(s) {
			return false
		}
	}
	return true
}

func allAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func allAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// checkUAOptionalContent enforces the optional-content configuration
// requirements (7.10): every OC configuration dictionary — the /D default and
// each entry of /Configs — must carry a non-empty /Name and must not contain an
// /AS key (which would make visibility depend on usage/state).
func (d *Document) checkUAOptionalContent(cat *Dictionary) []UAViolation {
	ocp := d.ResolveDict(cat.Get("OCProperties"))
	if ocp == nil {
		return nil
	}
	var v []UAViolation
	check := func(cfg *Dictionary) {
		if cfg == nil {
			return
		}
		if name, _ := d.Resolve(cfg.Get("Name")).(String); len(name.Value) == 0 {
			v = append(v, UAViolation{"7.10", "optional-content configuration dictionary has no non-empty /Name", 0})
		}
		if cfg.Get("AS") != nil {
			v = append(v, UAViolation{"7.10", "optional-content configuration dictionary must not contain an /AS key", 0})
		}
	}
	check(d.ResolveDict(ocp.Get("D")))
	if cfgs, ok := d.Resolve(ocp.Get("Configs")).(Array); ok {
		for _, c := range cfgs {
			check(d.ResolveDict(c))
		}
	}
	return v
}

// checkUAEmbeddedFiles requires every embedded-file specification (a file spec
// with an /EF entry) to carry non-empty /F and /UF file names (7.11).
func (d *Document) checkUAEmbeddedFiles() []UAViolation {
	var v []UAViolation
	for num, iobj := range d.Objects {
		fs, ok := iobj.Value.(*Dictionary)
		if !ok || fs.Get("EF") == nil {
			continue
		}
		if t, _ := fs.Get("Type").(Name); t != "" && t != "Filespec" {
			continue
		}
		f, _ := d.Resolve(fs.Get("F")).(String)
		uf, _ := d.Resolve(fs.Get("UF")).(String)
		if len(f.Value) == 0 || len(uf.Value) == 0 {
			v = append(v, UAViolation{"7.11", "embedded-file specification must have non-empty /F and /UF keys", num})
		}
	}
	return v
}

// effectiveFieldTU returns a Widget/field's user-facing description (/TU),
// following the /Parent field chain (bounded and cycle-guarded) since a terminal
// Widget may inherit /TU from its parent field.
func (d *Document) effectiveFieldTU(a *Dictionary) []byte {
	seen := map[*Dictionary]bool{}
	cur := a
	for i := 0; i < 32 && cur != nil && !seen[cur]; i++ {
		seen[cur] = true
		if tu, _ := d.Resolve(cur.Get("TU")).(String); len(tu.Value) > 0 {
			return tu.Value
		}
		cur = d.ResolveDict(cur.Get("Parent"))
	}
	return nil
}

// checkUAXFA flags a dynamic XFA form (dynamicRender = required), which PDF/UA
// forbids (Matterhorn 25-001).
func (d *Document) checkUAXFA(cat *Dictionary) []UAViolation {
	form := d.ResolveDict(cat.Get("AcroForm"))
	if form == nil {
		return nil
	}
	var xfa []byte
	switch v := d.Resolve(form.Get("XFA")).(type) {
	case *Stream:
		xfa = v.Data
	case Array:
		for _, e := range v {
			if st, ok := d.Resolve(e).(*Stream); ok {
				xfa = append(xfa, st.Data...)
			}
		}
	}
	if dynamicXFARequired(xfa) {
		return []UAViolation{{"7.15", "dynamic XFA forms are not permitted (dynamicRender required)", 0}}
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
func (d *Document) checkUATitle(cat *Dictionary) []UAViolation {
	stream, ok := d.Resolve(cat.Get("Metadata")).(*Stream)
	if !ok {
		return nil // absence of metadata is already reported by the identifier check
	}
	if !strings.Contains(decodeXMPToUTF8(stream.Data), "dc:title") {
		return []UAViolation{{"7.1", "XMP metadata has no document title (dc:title)", 0}}
	}
	return nil
}

// checkUAFonts flags fonts used for rendering but not embedded. It considers
// only fonts actually shown (the executed-content model), so unused or invisible
// font dictionaries are not false-flagged.
func (d *Document) checkUAFonts() []UAViolation {
	var v []UAViolation
	for fontDict := range collectFontTextUsage(d) {
		st, _ := fontDict.Get("Subtype").(Name)
		if st == "Type3" {
			continue // procedural glyphs, no font program
		}
		embedded := d.fontProgramEmbedded(fontDict)
		if st == "Type0" {
			if df, _ := d.Resolve(fontDict.Get("DescendantFonts")).(Array); len(df) > 0 {
				if cid := d.ResolveDict(df[0]); cid != nil {
					embedded = d.fontProgramEmbedded(cid)
				}
			}
		}
		if !embedded {
			v = append(v, UAViolation{"7.21.4.1", "font used for rendering is not embedded", d.dictObjNum(fontDict)})
		}
	}
	return v
}

// checkUACharMapping flags text shown with a font whose character codes cannot
// be mapped to Unicode. The clear, false-positive-free case: a composite
// (Type0) font with Identity encoding and no ToUnicode CMap — its codes are
// CIDs with no defined Unicode mapping (Matterhorn 10-001).
func (d *Document) checkUACharMapping() []UAViolation {
	var v []UAViolation
	for fontDict := range collectFontTextUsage(d) {
		if fontDict.Get("ToUnicode") != nil {
			continue
		}
		if st, _ := fontDict.Get("Subtype").(Name); st != "Type0" {
			continue
		}
		if enc, _ := d.Resolve(fontDict.Get("Encoding")).(Name); enc == "Identity-H" || enc == "Identity-V" {
			v = append(v, UAViolation{"7.2", "text uses a composite font with Identity encoding and no ToUnicode CMap; its character codes cannot be mapped to Unicode", d.dictObjNum(fontDict)})
		}
	}
	return v
}

// checkUAFontDicts enforces dictionary-level font requirements from clause 7.21
// on the fonts actually used for rendering (executed-content model):
//   - 7.21.3.2 an embedded CIDFontType2 must carry a /CIDToGIDMap;
//   - 7.21.6   a symbolic TrueType font must not have an /Encoding entry, and a
//     non-symbolic one must use MacRomanEncoding or WinAnsiEncoding.
func (d *Document) checkUAFontDicts() []UAViolation {
	var v []UAViolation
	for fontDict := range collectFontTextUsage(d) {
		v = append(v, d.checkOneUAFontDict(fontDict)...)
	}
	return v
}

// checkOneUAFontDict applies the dictionary-level clause 7.21 rules to a single
// font dictionary.
func (d *Document) checkOneUAFontDict(fontDict *Dictionary) []UAViolation {
	var v []UAViolation
	st, _ := fontDict.Get("Subtype").(Name)
	num := d.dictObjNum(fontDict)
	switch st {
	case "Type0":
		df, _ := d.Resolve(fontDict.Get("DescendantFonts")).(Array)
		if len(df) == 0 {
			return nil
		}
		cid := d.ResolveDict(df[0])
		if cid == nil {
			return nil
		}
		cst, _ := cid.Get("Subtype").(Name)
		if cst == "CIDFontType2" && d.fontProgramEmbedded(cid) && cid.Get("CIDToGIDMap") == nil {
			v = append(v, UAViolation{"7.21.3.2", "embedded CIDFontType2 font has no /CIDToGIDMap", num})
		}
	case "TrueType":
		symbolic := d.fontIsSymbolic(fontDict)
		enc := d.Resolve(fontDict.Get("Encoding"))
		if symbolic {
			if enc != nil {
				if _, isNull := enc.(Null); !isNull {
					v = append(v, UAViolation{"7.21.6", "symbolic TrueType font must not contain an /Encoding entry", num})
				}
			}
			return v
		}
		base, _ := enc.(Name)
		if ed := d.ResolveDict(fontDict.Get("Encoding")); ed != nil {
			base, _ = ed.Get("BaseEncoding").(Name)
		}
		if base != "MacRomanEncoding" && base != "WinAnsiEncoding" {
			v = append(v, UAViolation{"7.21.6", "non-symbolic TrueType font must use MacRomanEncoding or WinAnsiEncoding", num})
		}
	}
	return v
}

// fontIsSymbolic reports whether a font's descriptor marks it symbolic (Flags
// bit 3, value 4).
func (d *Document) fontIsSymbolic(font *Dictionary) bool {
	fd := d.ResolveDict(font.Get("FontDescriptor"))
	if fd == nil {
		return false
	}
	flags, _ := d.Resolve(fd.Get("Flags")).(Integer)
	return int(flags)&0x4 != 0
}

func (d *Document) fontProgramEmbedded(font *Dictionary) bool {
	fd := d.ResolveDict(font.Get("FontDescriptor"))
	if fd == nil {
		return false
	}
	return fd.Get("FontFile") != nil || fd.Get("FontFile2") != nil || fd.Get("FontFile3") != nil
}

func (d *Document) isTrue(o Object) bool {
	b, ok := d.Resolve(o).(Boolean)
	return ok && bool(b)
}

// checkFigureAlt walks the structure tree and flags Figure elements that carry
// neither /Alt nor /ActualText.
func (d *Document) checkFigureAlt(cat *Dictionary) []UAViolation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	var v []UAViolation
	seen := map[int]bool{}
	var walk func(node Object)
	walk = func(node Object) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			// A /K entry can also be an array of children or a marked-content id.
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, kid := range arr {
					walk(kid)
				}
			}
			return
		}
		if s, _ := elem.Get("S").(Name); s == "Figure" {
			alt, _ := d.Resolve(elem.Get("Alt")).(String)
			actual, _ := d.Resolve(elem.Get("ActualText")).(String)
			if len(alt.Value) == 0 && len(actual.Value) == 0 {
				v = append(v, UAViolation{"7.3", "figure structure element has no non-empty alternate text (/Alt or /ActualText)", 0})
			}
		}
		if k := elem.Get("K"); k != nil {
			switch kids := d.Resolve(k).(type) {
			case Array:
				for _, kid := range kids {
					walk(kid)
				}
			default:
				walk(k)
			}
		}
	}
	walk(root.Get("K"))
	return v
}
