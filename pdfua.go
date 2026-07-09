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

	// 7.21 — every font used for rendering must be embedded.
	v = append(v, doc.checkUAFonts()...)

	// 7.18.3 — pages with annotations must use structure tab order.
	v = append(v, doc.checkUATabOrder()...)

	// 7.1 — structure types must be standard or mapped via /RoleMap.
	v = append(v, doc.checkUARoleMap(cat)...)

	// 7.4 — numbered heading levels must not be skipped.
	v = append(v, doc.checkUAHeadings(cat)...)

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
