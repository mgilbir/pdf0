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

	// 7.2 — text must map to Unicode (Matterhorn 10-001).
	v = append(v, doc.checkUACharMapping()...)

	// 7.18.3 — pages with annotations must use structure tab order.
	v = append(v, doc.checkUATabOrder()...)

	// 7.1 — structure types must be standard or mapped via /RoleMap.
	v = append(v, doc.checkUARoleMap(cat)...)

	// 7.2 — structure-element nesting (tables, lists, TOC) per the UA profile.
	v = append(v, doc.checkUAStructNesting(cat)...)

	// 7.4 — numbered heading levels must not be skipped.
	v = append(v, doc.checkUAHeadings(cat)...)

	// 7.16 — encryption must not disable accessibility (Matterhorn 26).
	v = append(v, doc.checkUASecurity()...)

	// 7.18.2 — forbidden annotation subtypes (Matterhorn 28-007).
	v = append(v, doc.checkUAAnnotations()...)

	// 7.15 — dynamic XFA is forbidden (Matterhorn 25-001).
	v = append(v, doc.checkUAXFA(cat)...)

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
	prev := 0
	for _, lvl := range levels {
		if prev != 0 && lvl > prev+1 {
			v = append(v, UAViolation{"7.4", fmt.Sprintf("heading level H%d follows H%d, skipping a level", lvl, prev), 0})
		}
		prev = lvl
	}
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
	if !strings.Contains(decodeXMPToUTF8(stream.Data), "pdfuaid:part") {
		return []UAViolation{{"5", "XMP metadata does not declare the PDF/UA part (pdfuaid:part)", 0}}
	}
	return nil
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
		// 28-002/010/011: a visible annotation must be represented in the
		// structure tree — it carries a /StructParent linking it to a structure
		// element. (Hidden and Popup annotations were already skipped above.)
		if a.Get("StructParent") == nil {
			v = append(v, UAViolation{"7.18.1", "annotation is not tagged (no /StructParent linking it to the structure tree)", num})
		}
	}
	return v
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
			_, hasAlt := d.Resolve(elem.Get("Alt")).(String)
			_, hasActual := d.Resolve(elem.Get("ActualText")).(String)
			if !hasAlt && !hasActual {
				v = append(v, UAViolation{"7.3", "figure structure element has no alternate text (/Alt)", 0})
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
