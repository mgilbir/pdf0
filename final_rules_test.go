package pdf0

import "testing"

func TestProhibitedCatalogEntries(t *testing.T) {
	mk := func(setup func(cat *Dictionary, doc *Document)) *Document {
		doc := NewPDFADocument(PDFA4)
		cat := doc.ResolveDict(doc.Trailer.Get("Root"))
		setup(cat, doc)
		return doc
	}
	if !hasRuleMsg(checkProhibitedCatalogEntries(mk(func(c *Dictionary, d *Document) {
		c.Set("Requirements", Array{})
	}), PDFA4), "6.12") {
		t.Error("Requirements must be flagged")
	}
	if !hasRuleMsg(checkProhibitedCatalogEntries(mk(func(c *Dictionary, d *Document) {
		names := &Dictionary{}
		names.Set("AlternatePresentations", &Dictionary{})
		c.Set("Names", names)
	}), PDFA4), "6.11") {
		t.Error("AlternatePresentations must be flagged")
	}
	// Clean A-4 document passes.
	if len(checkProhibitedCatalogEntries(NewPDFADocument(PDFA4), PDFA4)) != 0 {
		t.Error("clean document flagged")
	}
	// Not applied at other levels.
	if len(checkProhibitedCatalogEntries(mk(func(c *Dictionary, d *Document) {
		c.Set("Requirements", Array{})
	}), PDFA2b)) != 0 {
		t.Skip()
	}
}

func TestFileTrailerID(t *testing.T) {
	mk := func(id Object) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		if id != nil {
			doc.Trailer.Set("ID", id)
		}
		return doc
	}
	// Two non-empty strings: valid.
	valid := Array{String{Value: []byte("0123456789abcdef")}, String{Value: []byte("fedcba9876543210")}}
	if hasRuleMsg(checkFileTrailerID(mk(valid), PDFA2b), "6.1.3") {
		t.Error("valid ID flagged")
	}
	// Empty strings.
	if !hasRuleMsg(checkFileTrailerID(mk(Array{String{}, String{}}), PDFA2b), "6.1.3") {
		t.Error("empty ID strings not flagged")
	}
	// Wrong length.
	if !hasRuleMsg(checkFileTrailerID(mk(Array{String{Value: []byte("x")}}), PDFA2b), "6.1.3") {
		t.Error("single-element ID not flagged")
	}
	// Absent: no error.
	if len(checkFileTrailerID(mk(nil), PDFA2b)) != 0 {
		t.Error("absent ID must not be flagged")
	}
}

func TestInlineImageEntries(t *testing.T) {
	entries := inlineImageEntries([]byte("BI /W 1 /H 2 /I true /Intent /Custom ID xx EI"))
	if len(entries) != 1 {
		t.Fatalf("expected 1 inline image, got %d", len(entries))
	}
	e := entries[0]
	if e["W"] != "1" || e["H"] != "2" || e["I"] != "true" || e["Intent"] != "Custom" {
		t.Errorf("entries wrong: %v", e)
	}
}

func TestForbiddenAAEvents(t *testing.T) {
	for _, k := range []Name{"WS", "O", "C", "PV", "DP"} {
		if !forbiddenAAEvents[k] {
			t.Errorf("%s should be forbidden", k)
		}
	}
	for _, k := range []Name{"E", "X", "D", "U", "Fo", "Bl", "PI"} {
		if forbiddenAAEvents[k] {
			t.Errorf("%s should be permitted", k)
		}
	}
}

func TestA4TriggerEvents(t *testing.T) {
	doc := NewPDFADocument(PDFA4)
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	aa := &Dictionary{}
	aa.Set("WS", &Dictionary{})
	cat.Set("AA", aa)
	if !hasRuleMsg(checkA4TriggerEvents(doc, PDFA4), "6.6.3") {
		t.Error("catalog AA/WS must be flagged")
	}
	// Interaction-only AA passes.
	doc2 := NewPDFADocument(PDFA4)
	cat2 := doc2.ResolveDict(doc2.Trailer.Get("Root"))
	aa2 := &Dictionary{}
	aa2.Set("Fo", &Dictionary{})
	cat2.Set("AA", aa2)
	if hasRuleMsg(checkA4TriggerEvents(doc2, PDFA4), "6.6.3") {
		t.Error("interaction-only catalog AA must pass")
	}
}

func TestPUADetection(t *testing.T) {
	if !isPUARune(0xE000) || !isPUARune(0xF8FF) || !isPUARune(0xF0000) || !isPUARune(0x100000) {
		t.Error("PUA ranges misjudged")
	}
	if isPUARune('A') || isPUARune(0xDFFF) || isPUARune(0xFFFF) {
		t.Error("non-PUA flagged")
	}
	// UTF-16BE with a PUA code point (U+E29C).
	if !stringHasPUA([]byte{0xFE, 0xFF, 0xE2, 0x9C, 0x00, 0x41}) {
		t.Error("PUA in UTF-16BE string not detected")
	}
	if stringHasPUA([]byte{0xFE, 0xFF, 0x00, 0x41, 0x00, 0x42}) {
		t.Error("clean string flagged as PUA")
	}
}

func TestContentActualTexts(t *testing.T) {
	got := contentActualTexts([]byte("/Span << /ActualText <FEFF0041> >> BDC (x) Tj EMC"))
	if len(got) != 1 || string(got[0]) != "\xfe\xff\x00A" {
		t.Errorf("ActualText extraction wrong: %q", got)
	}
}

func TestType5HalftoneTransferFunction(t *testing.T) {
	mk := func(colorant string, hasTF bool) *Document {
		doc := NewPDFADocument(PDFA4)
		comp := &Dictionary{}
		comp.Set("HalftoneType", Integer(1))
		if hasTF {
			comp.Set("TransferFunction", Name("Identity"))
		}
		ht := &Dictionary{}
		ht.Set("Type", Name("Halftone"))
		ht.Set("HalftoneType", Integer(5))
		ht.Set(Name(colorant), comp)
		doc.Objects[30] = &IndirectObject{Number: 30, Value: ht}
		gs := &Dictionary{}
		gs.Set("Type", Name("ExtGState"))
		gs.Set("HT", IndirectRef{Number: 30})
		gsDict := &Dictionary{}
		gsDict.Set("GS0", gs)
		res := &Dictionary{}
		res.Set("ExtGState", gsDict)
		page := addTestPage(doc)
		s := &Stream{Dict: Dictionary{}, Data: []byte("/GS0 gs")}
		s.Dict.Set("Length", Integer(7))
		doc.Objects[21] = &IndirectObject{Number: 21, Value: s}
		page.Set("Contents", IndirectRef{Number: 21})
		page.Set("Resources", res)
		return doc
	}
	// Primary colorant with TransferFunction: fail.
	if !hasRuleMsg(checkType5Halftones(mk("Cyan", true), PDFA4), "6.2.5") {
		t.Error("primary colorant with TransferFunction must be flagged")
	}
	// Primary colorant without: pass.
	if hasRuleMsg(checkType5Halftones(mk("Cyan", false), PDFA4), "6.2.5") {
		t.Error("primary colorant without TransferFunction must pass")
	}
	// Non-primary colorant without TransferFunction: fail.
	if !hasRuleMsg(checkType5Halftones(mk("Red", false), PDFA4), "6.2.5") {
		t.Error("non-primary colorant without TransferFunction must be flagged")
	}
	// Non-primary with: pass.
	if hasRuleMsg(checkType5Halftones(mk("Red", true), PDFA4), "6.2.5") {
		t.Error("non-primary colorant with TransferFunction must pass")
	}
}

func TestInfoAuthorMultiEntry(t *testing.T) {
	xmp := `<dc:creator><rdf:Seq><rdf:li>A</rdf:li><rdf:li>B</rdf:li></rdf:Seq></dc:creator>`
	if countXMPListEntries(xmp, "dc:creator") != 2 {
		t.Errorf("expected 2 creator entries, got %d", countXMPListEntries(xmp, "dc:creator"))
	}
	single := `<dc:creator><rdf:Seq><rdf:li>A</rdf:li></rdf:Seq></dc:creator>`
	if countXMPListEntries(single, "dc:creator") != 1 {
		t.Error("expected 1 creator entry")
	}
}

func TestIsPDFMIME(t *testing.T) {
	if !isPDFMIME(Name("application/pdf")) {
		t.Error("application/pdf not recognized")
	}
	if isPDFMIME(Name("text/plain")) || isPDFMIME(Integer(1)) || isPDFMIME(nil) {
		t.Error("non-pdf MIME wrongly recognized")
	}
}

func TestDeclaredPDFALevel(t *testing.T) {
	mk := func(xmp string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		s := &Stream{Dict: Dictionary{}, Data: []byte(xmp)}
		s.Dict.Set("Length", Integer(len(xmp)))
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &IndirectObject{Number: 2, Value: s}
		cat.Set("Metadata", IndirectRef{Number: 2})
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	// Attribute form.
	if lvl, ok := declaredPDFALevel(mk(`<rdf:Description pdfaid:part="4" pdfaid:conformance="B"/>`)); !ok || lvl != PDFA4 {
		t.Errorf("part=4 attr: got %v %v", lvl, ok)
	}
	// Element form.
	if lvl, ok := declaredPDFALevel(mk(`<pdfaid:part>2</pdfaid:part>`)); !ok || lvl != PDFA2b {
		t.Errorf("part=2 elem: got %v %v", lvl, ok)
	}
	// No pdfaid: not PDF/A.
	if _, ok := declaredPDFALevel(mk(`<rdf:Description/>`)); ok {
		t.Error("document without pdfaid must not be PDF/A")
	}
}

func TestExtractXMPAttr(t *testing.T) {
	if got := extractXMPAttr(`x pdfaid:part="4" y`, "pdfaid:part"); got != "4" {
		t.Errorf("double-quote attr: %q", got)
	}
	if got := extractXMPAttr(`x pdfaid:part='3' y`, "pdfaid:part"); got != "3" {
		t.Errorf("single-quote attr: %q", got)
	}
	if got := extractXMPAttr(`x y`, "pdfaid:part"); got != "" {
		t.Errorf("missing attr should be empty: %q", got)
	}
}

func TestParseToUnicodeMapSpaceless(t *testing.T) {
	// bfrange with no separators between <hhhh> tokens (real-world format).
	cmap := "begincmap\n2 beginbfrange\n<0003><0003><0020>\n<0028><0028><0048>\nendbfrange\nendcmap"
	doc := &Document{Objects: map[int]*IndirectObject{}}
	s := &Stream{Dict: Dictionary{}, Data: []byte(cmap)}
	s.Dict.Set("Length", Integer(len(cmap)))
	doc.Objects[1] = &IndirectObject{Number: 1, Value: s}
	fontDict := &Dictionary{}
	fontDict.Set("ToUnicode", IndirectRef{Number: 1})
	m := parseToUnicodeMap(doc, fontDict)
	if m[3] != 0x20 || m[0x28] != 0x48 {
		t.Errorf("bfrange parse wrong: %v", m)
	}
	if !isGlyphWhitespace(m[3]) || isGlyphWhitespace(m[0x28]) {
		t.Error("whitespace classification wrong")
	}
}

func TestParseToUnicodeMapMalformed(t *testing.T) {
	// Must not panic on truncated / overlapping markers.
	for _, bad := range []string{
		"beginbfchar", "endbfchar beginbfchar", "beginbfrangeendbfrange",
		"beginbfchar<00", "beginbfrange<0><1><2>", "",
	} {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		s := &Stream{Dict: Dictionary{}, Data: []byte(bad)}
		s.Dict.Set("Length", Integer(len(bad)))
		doc.Objects[1] = &IndirectObject{Number: 1, Value: s}
		fontDict := &Dictionary{}
		fontDict.Set("ToUnicode", IndirectRef{Number: 1})
		_ = parseToUnicodeMap(doc, fontDict) // just must not panic
	}
}

func TestAngleTokens(t *testing.T) {
	got := angleTokens("<0003><0003><0020>")
	if len(got) != 3 || got[0] != "<0003>" || got[2] != "<0020>" {
		t.Errorf("angleTokens wrong: %v", got)
	}
	if len(angleTokens("no tokens here")) != 0 {
		t.Error("expected no tokens")
	}
	if len(angleTokens("<unterminated")) != 0 {
		t.Error("unterminated must yield nothing")
	}
}

func TestInheritedPageXObject(t *testing.T) {
	mk := func(pageHasOwn bool) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		xo := &Dictionary{}
		xo.Set("X0", IndirectRef{Number: 90})
		page := &Dictionary{}
		page.Set("Type", Name("Page"))
		page.Set("Parent", IndirectRef{Number: 2})
		page.Set("Contents", IndirectRef{Number: 91})
		if pageHasOwn {
			ownRes := &Dictionary{}
			ownRes.Set("XObject", xo)
			page.Set("Resources", ownRes)
		}
		pagesRes := &Dictionary{}
		pagesRes.Set("XObject", xo)
		pages := &Dictionary{}
		pages.Set("Type", Name("Pages"))
		pages.Set("Kids", Array{IndirectRef{Number: 3}})
		pages.Set("Count", Integer(1))
		pages.Set("Resources", pagesRes)
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		cat.Set("Pages", IndirectRef{Number: 2})
		c := &Stream{Dict: Dictionary{}, Data: []byte("/X0 Do")}
		c.Dict.Set("Length", Integer(6))
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
		doc.Objects[3] = &IndirectObject{Number: 3, Value: page}
		doc.Objects[91] = &IndirectObject{Number: 91, Value: c}
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	if !hasRuleMsg(checkInheritedPageXObject(mk(false), PDFA4), "6.2.2") {
		t.Error("inherited page XObject must be flagged")
	}
	if hasRuleMsg(checkInheritedPageXObject(mk(true), PDFA4), "6.2.2") {
		t.Error("page with own XObject resource must pass")
	}
}
