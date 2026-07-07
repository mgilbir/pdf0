package pdf0

import (
	"testing"
)

// pageWithContent wires a page with the given content stream and resources
// into a NewPDFADocument.
func pageWithContent(doc *Document, content string, resources *Dictionary) *Dictionary {
	page := addTestPage(doc)
	stream := &Stream{Dict: Dictionary{}, Data: []byte(content)}
	stream.Dict.Set("Length", Integer(len(content)))
	doc.Objects[21] = &IndirectObject{Number: 21, Value: stream}
	page.Set("Contents", IndirectRef{Number: 21})
	if resources != nil {
		page.Set("Resources", resources)
	}
	return page
}

// Device colour inside an INVOKED form XObject body must be detected; the
// same form merely referenced but never drawn must not be.
func TestDeviceColorInFormBody(t *testing.T) {
	build := func(content string) *Document {
		doc := NewPDFADocument(PDFA2b)
		form := &Stream{Dict: Dictionary{}, Data: []byte("0 0.7 0.7 0 k 0 0 9 9 re f")}
		form.Dict.Set("Type", Name("XObject"))
		form.Dict.Set("Subtype", Name("Form"))
		form.Dict.Set("BBox", Array{Integer(0), Integer(0), Integer(10), Integer(10)})
		form.Dict.Set("Length", Integer(len(form.Data)))
		doc.Objects[22] = &IndirectObject{Number: 22, Value: form}
		xobj := &Dictionary{}
		xobj.Set("X0", IndirectRef{Number: 22})
		res := &Dictionary{}
		res.Set("XObject", xobj)
		pageWithContent(doc, content, res)
		return doc
	}

	// Invoked: DeviceCMYK without CMYK intent coverage must be flagged
	// (NewPDFADocument embeds an RGB output intent).
	if !hasRule(ValidatePDFA(build("q /X0 Do Q"), PDFA2b), "6.2.4") {
		t.Error("DeviceCMYK inside an invoked form must be flagged")
	}
	// Referenced but never invoked: executed-content model, no violation.
	if hasRule(ValidatePDFA(build("q 0.1 0.2 0.3 rg 0 0 5 5 re f Q"), PDFA2b), "6.2.4") {
		t.Error("a form that is never invoked must not be flagged")
	}
}

// /DeviceCMYK cs selection (as opposed to the k operator) must be detected.
func TestDeviceColorViaCSOperator(t *testing.T) {
	doc := NewPDFADocument(PDFA2b)
	pageWithContent(doc, "/DeviceCMYK cs 0 0 0 1 sc 0 0 5 5 re f", nil)
	if !hasRule(ValidatePDFA(doc, PDFA2b), "6.2.4") {
		t.Error("device colour selected via cs operator must be detected")
	}
}

// A pattern's own DefaultCMYK covers its device usage; a page-level
// DefaultCMYK does not reach inside the pattern's resource scope.
func TestDefaultColorSpaceScope(t *testing.T) {
	build := func(patternDefaults bool) *Document {
		doc := NewPDFADocument(PDFA2b)
		pat := &Stream{Dict: Dictionary{}, Data: []byte("0 0 0 1 k 0 0 5 5 re f")}
		pat.Dict.Set("PatternType", Integer(1))
		pat.Dict.Set("PaintType", Integer(1))
		pat.Dict.Set("TilingType", Integer(1))
		pat.Dict.Set("BBox", Array{Integer(0), Integer(0), Integer(10), Integer(10)})
		pat.Dict.Set("XStep", Integer(10))
		pat.Dict.Set("YStep", Integer(10))
		pat.Dict.Set("Length", Integer(len(pat.Data)))
		patRes := &Dictionary{}
		if patternDefaults {
			csDict := &Dictionary{}
			csDict.Set("DefaultCMYK", Array{Name("ICCBased"), IndirectRef{Number: 5}})
			patRes.Set("ColorSpace", csDict)
		}
		pat.Dict.Set("Resources", patRes)
		doc.Objects[22] = &IndirectObject{Number: 22, Value: pat}

		patterns := &Dictionary{}
		patterns.Set("P0", IndirectRef{Number: 22})
		res := &Dictionary{}
		res.Set("Pattern", patterns)
		// Page-level DefaultCMYK, which must NOT cover the pattern.
		pageCS := &Dictionary{}
		pageCS.Set("DefaultCMYK", Array{Name("ICCBased"), IndirectRef{Number: 5}})
		res.Set("ColorSpace", pageCS)
		pageWithContent(doc, "/Pattern cs /P0 scn 0 0 50 50 re f", res)
		return doc
	}

	if !hasRule(ValidatePDFA(build(false), PDFA2b), "6.2.4") {
		t.Error("page-level DefaultCMYK must not cover device colour inside a pattern")
	}
	if hasRule(ValidatePDFA(build(true), PDFA2b), "6.2.4") {
		t.Error("the pattern's own DefaultCMYK must cover its device colour")
	}
}

// ISO 32000-1 Tables 63-65: CIE colour space parameter validation.
func TestCIEColorSpaceParams(t *testing.T) {
	check := func(family string, params *Dictionary) []ValidationError {
		doc := NewPDFADocument(PDFA2b)
		var errs []ValidationError
		checkColorSpaceValue(doc, Array{Name(family), params}, 0, PDFA2b, &errs)
		return errs
	}
	wp := func(x, y, z float64) Array { return Array{Real(x), Real(y), Real(z)} }

	missing := &Dictionary{}
	if len(check("CalRGB", missing)) == 0 {
		t.Error("missing WhitePoint must be flagged")
	}
	badY := &Dictionary{}
	badY.Set("WhitePoint", wp(0.95, 0.9, 1.09))
	if len(check("CalGray", badY)) == 0 {
		t.Error("WhitePoint Yw != 1.0 must be flagged")
	}
	negBP := &Dictionary{}
	negBP.Set("WhitePoint", wp(0.95, 1.0, 1.09))
	negBP.Set("BlackPoint", Array{Real(-0.1), Real(0), Real(0)})
	if len(check("Lab", negBP)) == 0 {
		t.Error("negative BlackPoint must be flagged")
	}
	badRange := &Dictionary{}
	badRange.Set("WhitePoint", wp(0.95, 1.0, 1.09))
	badRange.Set("Range", Array{Integer(100), Integer(-100), Integer(-100), Integer(100)})
	if len(check("Lab", badRange)) == 0 {
		t.Error("Lab Range with min > max must be flagged")
	}
	good := &Dictionary{}
	good.Set("WhitePoint", wp(0.9505, 1.0, 1.089))
	if errs := check("CalRGB", good); len(errs) != 0 {
		t.Errorf("valid CalRGB dict must pass, got %v", errs)
	}
}

// Overprint mode 1 with an ICCBased CMYK space and overprinting on.
func TestICCCMYKOverprint(t *testing.T) {
	build := func(op bool, paint string) *Document {
		doc := NewPDFADocument(PDFA2b)
		icc := &Stream{Dict: Dictionary{}, Data: DefaultSRGBProfile()}
		icc.Dict.Set("N", Integer(4))
		icc.Dict.Set("Length", Integer(len(icc.Data)))
		doc.Objects[22] = &IndirectObject{Number: 22, Value: icc}
		gs := &Dictionary{}
		gs.Set("Type", Name("ExtGState"))
		gs.Set("OPM", Integer(1))
		gs.Set("OP", Boolean(op))
		gsDict := &Dictionary{}
		gsDict.Set("GS0", gs)
		csDict := &Dictionary{}
		csDict.Set("CS0", Array{Name("ICCBased"), IndirectRef{Number: 22}})
		res := &Dictionary{}
		res.Set("ExtGState", gsDict)
		res.Set("ColorSpace", csDict)
		pageWithContent(doc, "/GS0 gs /CS0 CS 0 0 0 1 SCN 0 0 5 5 re "+paint, res)
		return doc
	}
	if !hasRule(ValidatePDFA(build(true, "S"), PDFA2b), "6.2.4.2") {
		t.Error("OPM=1 + OP + stroked ICC CMYK must be flagged")
	}
	if hasRule(ValidatePDFA(build(false, "S"), PDFA2b), "6.2.4.2") {
		t.Error("without overprinting there is no violation")
	}
	if hasRule(ValidatePDFA(build(true, "f"), PDFA2b), "6.2.4.2") {
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
		doc := NewPDFADocument(PDFA2b)
		img := &Stream{Dict: Dictionary{}, Data: data}
		img.Dict.Set("Type", Name("XObject"))
		img.Dict.Set("Subtype", Name("Image"))
		img.Dict.Set("Filter", Name("JPXDecode"))
		img.Dict.Set("Length", Integer(len(data)))
		doc.Objects[22] = &IndirectObject{Number: 22, Value: img}
		return doc
	}

	if !hasRule(ValidatePDFA(build(jp2(5, 7, 1, 16)), PDFA2b), "6.2.8.3") {
		t.Error("5 colour channels must be flagged")
	}
	if !hasRule(ValidatePDFA(build(jp2(3, 40, 1, 16)), PDFA2b), "6.2.8.3") {
		t.Error("bit depth 41 must be flagged")
	}
	if !hasRule(ValidatePDFA(build(jp2(3, 7, 4, 0)), PDFA2b), "6.2.8.3") {
		t.Error("METH 4 must be flagged")
	}
	if !hasRule(ValidatePDFA(build(jp2(3, 7, 1, 19)), PDFA2b), "6.2.8.3") {
		t.Error("enumerated colour space 19 (CIEJab) must be flagged")
	}
	if hasRule(ValidatePDFA(build(jp2(3, 7, 1, 16)), PDFA2b), "6.2.8.3") {
		t.Error("valid sRGB JP2 must pass")
	}
}

// Separation/DeviceN device alternates need intent coverage at 2b+.
func TestDeviceAlternateNeedsCoverage(t *testing.T) {
	doc := NewPDFADocument(PDFA2b) // sRGB intent: no CMYK coverage
	csDict := &Dictionary{}
	csDict.Set("CS0", Array{Name("Separation"), Name("Spot"), Name("DeviceCMYK"), IndirectRef{Number: 5}})
	res := &Dictionary{}
	res.Set("ColorSpace", csDict)
	pageWithContent(doc, "/CS0 cs 1 sc 0 0 5 5 re f", res)
	if !hasRule(ValidatePDFA(doc, PDFA2b), "6.2.4") {
		t.Error("DeviceCMYK alternate without CMYK intent must be flagged")
	}
}

// DeviceN with spot colorants requires a Colorants dictionary at 2b+.
func TestDeviceNSpotNeedsColorants(t *testing.T) {
	doc := NewPDFADocument(PDFA2b)
	var errs []ValidationError
	deviceN := Array{Name("DeviceN"), Array{Name("Spot1")}, Array{Name("ICCBased"), IndirectRef{Number: 5}}, IndirectRef{Number: 5}}
	checkColorSpaceValue(doc, deviceN, 0, PDFA2b, &errs)
	found := false
	for _, e := range errs {
		if e.Message == "DeviceN color space with spot colorants must have a Colorants dictionary" {
			found = true
		}
	}
	if !found {
		t.Errorf("spot DeviceN without Colorants dict must be flagged, got %v", errs)
	}
}
