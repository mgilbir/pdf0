package pdf0

import (
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"strings"
	"testing"
)

// pageWithContent wires a page with the given content stream and resources
// into a NewPDFADocument.
func pageWithContent(doc *Document, content string, resources *object.Dictionary) *object.Dictionary {
	page := addTestPage(doc)
	stream := &object.Stream{Dict: object.Dictionary{}, Data: []byte(content)}
	stream.Dict.Set("Length", object.Integer(len(content)))
	doc.Objects[21] = &object.IndirectObject{Number: 21, Value: stream}
	page.Set("Contents", object.IndirectRef{Number: 21})
	if resources != nil {
		page.Set("Resources", resources)
	}
	return page
}

// Device colour inside an INVOKED form XObject body must be detected; the
// same form merely referenced but never drawn must not be.
func TestDeviceColorInFormBody(t *testing.T) {
	build := func(content string) *Document {
		doc := NewPDFADocument(pdfa.PDFA2b)
		form := &object.Stream{Dict: object.Dictionary{}, Data: []byte("0 0.7 0.7 0 k 0 0 9 9 re f")}
		form.Dict.Set("Type", object.Name("XObject"))
		form.Dict.Set("Subtype", object.Name("Form"))
		form.Dict.Set("BBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)})
		form.Dict.Set("Length", object.Integer(len(form.Data)))
		doc.Objects[22] = &object.IndirectObject{Number: 22, Value: form}
		xobj := &object.Dictionary{}
		xobj.Set("X0", object.IndirectRef{Number: 22})
		res := &object.Dictionary{}
		res.Set("XObject", xobj)
		pageWithContent(doc, content, res)
		return doc
	}

	// Invoked: DeviceCMYK without CMYK intent coverage must be flagged
	// (NewPDFADocument embeds an RGB output intent).
	if !hasRule(ValidatePDFA(build("q /X0 Do Q"), pdfa.PDFA2b), "6.2.4.3") {
		t.Error("DeviceCMYK inside an invoked form must be flagged")
	}
	// Referenced but never invoked: executed-content model, no violation.
	if hasRule(ValidatePDFA(build("q 0.1 0.2 0.3 rg 0 0 5 5 re f Q"), pdfa.PDFA2b), "6.2.4.3") {
		t.Error("a form that is never invoked must not be flagged")
	}
}

// /DeviceCMYK cs selection (as opposed to the k operator) must be detected.
func TestDeviceColorViaCSOperator(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	pageWithContent(doc, "/DeviceCMYK cs 0 0 0 1 sc 0 0 5 5 re f", nil)
	if !hasRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.2.4.3") {
		t.Error("device colour selected via cs operator must be detected")
	}
}

// A pattern's own DefaultCMYK covers its device usage; a page-level
// DefaultCMYK does not reach inside the pattern's resource scope.
func TestDefaultColorSpaceScope(t *testing.T) {
	build := func(patternDefaults bool) *Document {
		doc := NewPDFADocument(pdfa.PDFA2b)
		pat := &object.Stream{Dict: object.Dictionary{}, Data: []byte("0 0 0 1 k 0 0 5 5 re f")}
		pat.Dict.Set("PatternType", object.Integer(1))
		pat.Dict.Set("PaintType", object.Integer(1))
		pat.Dict.Set("TilingType", object.Integer(1))
		pat.Dict.Set("BBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)})
		pat.Dict.Set("XStep", object.Integer(10))
		pat.Dict.Set("YStep", object.Integer(10))
		pat.Dict.Set("Length", object.Integer(len(pat.Data)))
		patRes := &object.Dictionary{}
		if patternDefaults {
			csDict := &object.Dictionary{}
			csDict.Set("DefaultCMYK", object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 5}})
			patRes.Set("ColorSpace", csDict)
		}
		pat.Dict.Set("Resources", patRes)
		doc.Objects[22] = &object.IndirectObject{Number: 22, Value: pat}

		patterns := &object.Dictionary{}
		patterns.Set("P0", object.IndirectRef{Number: 22})
		res := &object.Dictionary{}
		res.Set("Pattern", patterns)
		// Page-level DefaultCMYK, which must NOT cover the pattern.
		pageCS := &object.Dictionary{}
		pageCS.Set("DefaultCMYK", object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 5}})
		res.Set("ColorSpace", pageCS)
		pageWithContent(doc, "/Pattern cs /P0 scn 0 0 50 50 re f", res)
		return doc
	}

	if !hasRule(ValidatePDFA(build(false), pdfa.PDFA2b), "6.2.4.3") {
		t.Error("page-level DefaultCMYK must not cover device colour inside a pattern")
	}
	if hasRule(ValidatePDFA(build(true), pdfa.PDFA2b), "6.2.4.3") {
		t.Error("the pattern's own DefaultCMYK must cover its device colour")
	}
}

// Overprint mode 1 with an ICCBased CMYK space and overprinting on.
func TestICCCMYKOverprint(t *testing.T) {
	build := func(op bool, paint string) *Document {
		doc := NewPDFADocument(pdfa.PDFA2b)
		icc := &object.Stream{Dict: object.Dictionary{}, Data: DefaultSRGBProfile()}
		icc.Dict.Set("N", object.Integer(4))
		icc.Dict.Set("Length", object.Integer(len(icc.Data)))
		doc.Objects[22] = &object.IndirectObject{Number: 22, Value: icc}
		gs := &object.Dictionary{}
		gs.Set("Type", object.Name("ExtGState"))
		gs.Set("OPM", object.Integer(1))
		gs.Set("OP", object.Boolean(op))
		gsDict := &object.Dictionary{}
		gsDict.Set("GS0", gs)
		csDict := &object.Dictionary{}
		csDict.Set("CS0", object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 22}})
		res := &object.Dictionary{}
		res.Set("ExtGState", gsDict)
		res.Set("ColorSpace", csDict)
		pageWithContent(doc, "/GS0 gs /CS0 CS 0 0 0 1 SCN 0 0 5 5 re "+paint, res)
		return doc
	}
	// ICC-profile validity and overprint share clause 6.2.4.2 (different tests),
	// so match the overprint rule by its message to isolate it.
	hasOverprint := func(errs []pdfa.ValidationError) bool {
		for _, e := range errs {
			if e.Rule == "6.2.4.2" && strings.Contains(e.Message, "overprint") {
				return true
			}
		}
		return false
	}
	if !hasOverprint(ValidatePDFA(build(true, "S"), pdfa.PDFA2b)) {
		t.Error("OPM=1 + OP + stroked ICC CMYK must be flagged")
	}
	if hasOverprint(ValidatePDFA(build(false, "S"), pdfa.PDFA2b)) {
		t.Error("without overprinting there is no violation")
	}
	if hasOverprint(ValidatePDFA(build(true, "f"), pdfa.PDFA2b)) {
		t.Error("stroke CS that never strokes must not be flagged")
	}
}

// JP2 header parsing and channel/bit-depth/METH/EnumCS restrictions.
func TestJPXValidation(t *testing.T) {
	jp2 := func(nc int, bpc byte, meth byte, enumCS uint32) []byte {
		var ihdr []byte
		ihdr = append(ihdr, 0, 0, 0, 1, 0, 0, 0, 1) // height, width
		ihdr = append(ihdr, byte(nc>>8), byte(nc), bpc, 7, 0, 0)
		colr := []byte{meth, 0, 0}
		if meth == 1 {
			colr = append(colr, byte(enumCS>>24), byte(enumCS>>16), byte(enumCS>>8), byte(enumCS))
		}
		box := func(t string, payload []byte) []byte {
			n := uint32(8 + len(payload))
			out := []byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
			out = append(out, t...)
			return append(out, payload...)
		}
		jp2h := append(box("ihdr", ihdr), box("colr", colr)...)
		var data []byte
		data = append(data, box("jP  ", []byte{0x0D, 0x0A, 0x87, 0x0A})...)
		return append(data, box("jp2h", jp2h)...)
	}
	build := func(data []byte) *Document {
		doc := NewPDFADocument(pdfa.PDFA2b)
		img := &object.Stream{Dict: object.Dictionary{}, Data: data}
		img.Dict.Set("Type", object.Name("XObject"))
		img.Dict.Set("Subtype", object.Name("Image"))
		img.Dict.Set("Filter", object.Name("JPXDecode"))
		img.Dict.Set("Length", object.Integer(len(data)))
		doc.Objects[22] = &object.IndirectObject{Number: 22, Value: img}
		return doc
	}

	if !hasRule(ValidatePDFA(build(jp2(5, 7, 1, 16)), pdfa.PDFA2b), "6.2.8.3") {
		t.Error("5 colour channels must be flagged")
	}
	if !hasRule(ValidatePDFA(build(jp2(3, 40, 1, 16)), pdfa.PDFA2b), "6.2.8.3") {
		t.Error("bit depth 41 must be flagged")
	}
	if !hasRule(ValidatePDFA(build(jp2(3, 7, 4, 0)), pdfa.PDFA2b), "6.2.8.3") {
		t.Error("METH 4 must be flagged")
	}
	if !hasRule(ValidatePDFA(build(jp2(3, 7, 1, 19)), pdfa.PDFA2b), "6.2.8.3") {
		t.Error("enumerated colour space 19 (CIEJab) must be flagged")
	}
	if hasRule(ValidatePDFA(build(jp2(3, 7, 1, 16)), pdfa.PDFA2b), "6.2.8.3") {
		t.Error("valid sRGB JP2 must pass")
	}
}

// Separation/DeviceN device alternates need intent coverage at 2b+.
func TestDeviceAlternateNeedsCoverage(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b) // sRGB intent: no CMYK coverage
	csDict := &object.Dictionary{}
	csDict.Set("CS0", object.Array{object.Name("Separation"), object.Name("Spot"), object.Name("DeviceCMYK"), object.IndirectRef{Number: 5}})
	res := &object.Dictionary{}
	res.Set("ColorSpace", csDict)
	pageWithContent(doc, "/CS0 cs 1 sc 0 0 5 5 re f", res)
	if !hasRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.2.4.3") {
		t.Error("DeviceCMYK alternate without CMYK intent must be flagged")
	}
}
