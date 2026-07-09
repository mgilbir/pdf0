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

	// 7.3 — every figure needs alternate text.
	v = append(v, doc.checkFigureAlt(cat)...)
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
