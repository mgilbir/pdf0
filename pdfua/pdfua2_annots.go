package pdfua

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// PDF/UA-2's annotation, form and structure-type rules that PDF/UA-1 does not
// have, or words differently (ISO 14289-2 8.2.5, 8.9, 8.10). Each follows the
// veraPDF PDF/UA-2 profile's test for it, and the single-defect fixtures
// (pdfua_fixtures_verapdf_test.go) hold them to veraPDF.

// markupAnnots are the markup annotations (ISO 32000-2 12.5.6.2, Table 171).
var markupAnnots = map[object.Name]bool{
	"Text": true, "FreeText": true, "Line": true, "Square": true, "Circle": true,
	"Polygon": true, "PolyLine": true, "Highlight": true, "Underline": true,
	"Squiggly": true, "StrikeOut": true, "Caret": true, "Stamp": true, "Ink": true,
	"FileAttachment": true, "Sound": true, "Redact": true, "Projection": true,
}

// checkUA2Annotations is PDF/UA-2's per-annotation rules. An annotation is
// "enclosed" when a structure element holds an object reference to it, and
// an artifact when that element is, or is inside, an Artifact element, as
// veraPDF's PDAnnot reads the two.
//
//   - 8.2.5.20: a link is enclosed in a Link or Reference element.
//   - 8.9.2.2: one whose NoView flag is set, and ToggleNoView not, is an
//     artifact if enclosed (the Invisible flag is checkUA2InvisibleAnnots').
//   - 8.9.2.3: a markup annotation is enclosed in an Annot element, and its
//     /RC, when it has one and /Contents, is the same text.
//   - 8.9.2.4.7 to .19: a stamp has /Name or /Contents; ink, screen, 3D and
//     rich media annotations have /Contents; a popup is not in the structure
//     tree; sound, movie and trap network annotations are not used; a widget
//     of zero size, a printer's mark and a watermark not in an Annot element
//     are artifacts if enclosed.
//   - 8.9.4.2: an annotation's /Contents and its enclosing element's /Alt,
//     when it has both, are the same text.
//   - 8.10.1: a field's widget is enclosed in a Form element.
func checkUA2Annotations(d core.View, cat *object.Dictionary) []Violation {
	ctx := newAnnotContext(d, cat)
	nodes := structTree(d, cat)
	at := annotStructIndex(d, cat, ctx)
	artifact := inArtifacts(d, nodes)
	var v []Violation
	for _, a := range ctx.annots {
		d.Charge(1)
		st, _ := d.ResolveName(a.Get("Subtype"))
		num := anchorOf(d, a)
		report := func(clause, msg string) {
			v = append(v, Violation{Clause: clause, Message: msg, Object: num})
		}
		i, tagged := at[a]
		enclosed := tagged && !artifact[i]
		var parentType object.Name
		if tagged {
			parentType = nodes[i].StdType
		}
		hasContents := present(d, a.Get("Contents"))
		f, _ := d.Resolve(a.Get("F")).(object.Integer)

		if enclosed && f&32 != 0 && f&256 == 0 {
			report("8.9.2.2", "an annotation with the NoView flag is in the logical structure and is not an artifact")
		}
		switch {
		case st == "Link":
			if enclosed && parentType != "Link" && parentType != "Reference" {
				report("8.2.5.20", "a Link annotation is enclosed in a <"+string(parentType)+"> element, not a Link or Reference element")
			}
		case st == "Widget":
			if enclosed && parentType != "Form" && (a.Has("T") || a.Has("Parent")) {
				report("8.10.1", "a field's widget annotation is enclosed in a <"+string(parentType)+"> element, not a Form element")
			}
			if enclosed && zeroSize(d, a) {
				report("8.9.2.4.13", "a widget annotation of zero width and height is in the logical structure and is not an artifact")
			}
		case st == "Watermark":
			if enclosed && parentType != "Annot" {
				report("8.9.2.4.16", "a Watermark annotation is enclosed in a <"+string(parentType)+"> element, not an Annot element")
			}
		case markupAnnots[st]:
			if enclosed && parentType != "Annot" {
				report("8.9.2.3", "a markup annotation of subtype /"+string(st)+" is enclosed in a <"+string(parentType)+"> element, not an Annot element")
			}
			if rc, ok := richText(d, a.Get("RC")); ok {
				if c, ok := textOf(d, a.Get("Contents")); ok && rc != strings.TrimSpace(c) {
					report("8.9.2.3", "a markup annotation's /RC and /Contents are not the same text")
				}
			}
		}
		switch st {
		case "Stamp":
			if !hasContents && !present(d, a.Get("Name")) {
				report("8.9.2.4.7", "a stamp annotation has neither /Name nor /Contents to describe it")
			}
		case "Ink":
			if !hasContents {
				report("8.9.2.4.8", "an ink annotation has no /Contents describing it")
			}
		case "Popup":
			if tagged {
				report("8.9.2.4.9", "a Popup annotation is in the structure tree")
			}
		case "Sound", "Movie":
			report("8.9.2.4.11", "a /"+string(st)+" annotation is not permitted in PDF/UA-2")
		case "Screen":
			if !hasContents {
				report("8.9.2.4.12", "a screen annotation has no /Contents describing it")
			}
		case "PrinterMark":
			if enclosed {
				report("8.9.2.4.14", "a printer's mark annotation is in the logical structure and is not an artifact")
			}
		case "TrapNet":
			report("8.9.2.4.15", "a trap network annotation is not permitted in PDF/UA-2")
		case "3D", "RichMedia":
			if !hasContents {
				report("8.9.2.4.19", "a /"+string(st)+" annotation has no /Contents describing it")
			}
		}
		if tagged {
			if c, ok := textOf(d, a.Get("Contents")); ok {
				if alt, ok := textOf(d, nodes[i].Elem.Get("Alt")); ok && alt != c {
					report("8.9.4.2", "an annotation's /Contents is not the same text as its enclosing element's /Alt")
				}
			}
		}
	}
	return v
}

// checkUA2Forms is PDF/UA-2's form rules over the interactive form: no XFA
// (8.10.1), and a text field's rich-text value /RV only beside a /V that is
// the same text (8.10.3.3).
func checkUA2Forms(d core.View, cat *object.Dictionary) []Violation {
	form := d.ResolveDict(cat.Get("AcroForm"))
	if form == nil {
		return nil
	}
	var v []Violation
	if form.Get("XFA") != nil {
		v = append(v, Violation{Clause: "8.10.1", Message: "the interactive form has an /XFA entry", Object: anchorOf(d, form)})
	}
	fields, _ := d.Resolve(form.Get("Fields")).(object.Array)
	seen := map[*object.Dictionary]bool{}
	var walk func(kids object.Array, ft object.Name, depth int)
	walk = func(kids object.Array, ft object.Name, depth int) {
		if depth > maxFieldDepth {
			return
		}
		for _, k := range kids {
			d.Charge(1)
			f := d.ResolveDict(k)
			if f == nil || seen[f] {
				continue
			}
			seen[f] = true
			typ := ft
			if t, ok := d.ResolveName(f.Get("FT")); ok {
				typ = t
			}
			if typ == "Tx" && f.Get("RV") != nil {
				rv, rvOK := richText(d, f.Get("RV"))
				val, vOK := textOf(d, f.Get("V"))
				if !present(d, f.Get("V")) || rvOK && vOK && rv != strings.TrimSpace(val) {
					v = append(v, Violation{Clause: "8.10.3.3", Message: "a text field's /RV is not accompanied by a /V that is the same text", Object: anchorOf(d, f)})
				}
			}
			if sub, ok := d.Resolve(f.Get("Kids")).(object.Array); ok {
				walk(sub, typ, depth+1)
			}
		}
	}
	walk(fields, "", 0)
	return v
}

// maxFieldDepth bounds the walk down a form's field hierarchy.
const maxFieldDepth = 64

// checkUA2StructureTypes is PDF/UA-2's rules on particular structure types:
// no Note (8.2.5.14: PDF 2.0 has FENote); a TOCI, or an element inside it,
// carries /Ref (8.2.5.8); a Ruby's children are RB and RT, with or without
// RP around the RT (8.2.5.23); a Warichu's are WP, WT and WP (8.2.5.24).
func checkUA2StructureTypes(d core.View, cat *object.Dictionary) []Violation {
	nodes := structTree(d, cat)
	hasRef := make([]bool, len(nodes)) // the node or a descendant carries /Ref
	for i := len(nodes) - 1; i >= 0; i-- {
		d.Charge(1)
		if nodes[i].HasS && nodes[i].Elem.Get("Ref") != nil {
			hasRef[i] = true
		}
		if hasRef[i] && nodes[i].Parent >= 0 {
			hasRef[nodes[i].Parent] = true
		}
	}
	var v []Violation
	for i, n := range nodes {
		if !n.HasS {
			continue
		}
		report := func(clause, msg string) {
			v = append(v, Violation{Clause: clause, Message: msg, Object: anchorOf(d, n.Elem)})
		}
		switch {
		case isStdType(n, "Note"):
			report("8.2.5.14", "a Note structure element is present; PDF 2.0 marks notes with FENote")
		case isStdType(n, "TOCI"):
			if !hasRef[i] {
				report("8.2.5.8", "a TOCI structure element identifies its target with no /Ref, on itself or inside it")
			}
		case isStdType(n, "Ruby"):
			if k := kidTypes(n); k != "RB&RT" && k != "RB&RP&RT&RP" {
				report("8.2.5.23", "a Ruby structure element's children are "+k+", not RB and RT (with RP around the RT)")
			}
		case isStdType(n, "Warichu"):
			if k := kidTypes(n); k != "WP&WT&WP" {
				report("8.2.5.24", "a Warichu structure element's children are "+k+", not WP, WT and WP")
			}
		}
	}
	return v
}

// kidTypes is n's children's standard types joined by "&", the form the
// veraPDF profile compares them in.
func kidTypes(n structNode) string {
	parts := make([]string, len(n.ChildTypes))
	for i, t := range n.ChildTypes {
		parts[i] = string(t)
	}
	return strings.Join(parts, "&")
}

// present reports whether o is an entry with a value: absent and null are not.
func present(d core.View, o object.Object) bool {
	switch d.Resolve(o).(type) {
	case nil, object.Null:
		return false
	}
	return true
}

// textOf is o as decoded text, and whether it is a string pdf0 can read:
// ciphertext is not compared (core.View.StringValue).
func textOf(d core.View, o object.Object) (string, bool) {
	if o == nil {
		return "", false
	}
	s, why := d.StringValue(o)
	if why != core.ReasonOK {
		return "", false
	}
	return core.DecodePDFTextString(s.Value), true
}

// richText is the text a rich text string (ISO 32000-2 12.7.4.4) holds: the
// character data of its XHTML, trimmed, as veraPDF compares it with the
// plain text beside it — the same words in markup are the same text. One
// that is not XML, or is text outside any element, holds none. The second result is false when o is not a
// string or stream pdf0 can read.
func richText(d core.View, o object.Object) (string, bool) {
	var raw []byte
	switch v := d.Resolve(o).(type) {
	case object.String:
		s, why := d.StringValue(v)
		if why != core.ReasonOK {
			return "", false
		}
		raw = []byte(core.DecodePDFTextString(s.Value))
	case *object.Stream:
		data, why := d.Content(v)
		if why != core.ReasonOK {
			return "", false
		}
		raw = data
	default:
		return "", false
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = true
	var text strings.Builder
	depth := 0
	for {
		d.Charge(1)
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", true
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			// Text outside every element is not markup's text.
			if depth > 0 {
				text.Write(t)
			}
		}
	}
	return strings.TrimSpace(text.String()), true
}

// zeroSize reports whether an annotation's /Rect has no width and no height.
func zeroSize(d core.View, a *object.Dictionary) bool {
	r, ok := rect4(d, a.Get("Rect"))
	return ok && r[0] == r[2] && r[1] == r[3]
}
