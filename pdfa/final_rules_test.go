package pdfa

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

func TestProhibitedCatalogEntries(t *testing.T) {
	mk := func(setup func(cat *object.Dictionary, doc core.View)) core.View {
		doc := mkPDFAView(PDFA4)
		cat := doc.ResolveDict(doc.Trailer.Get("Root"))
		setup(cat, doc)
		return doc
	}
	if !hasRuleMsg(checkProhibitedCatalogEntries(mk(func(c *object.Dictionary, d core.View) {
		c.Set("Requirements", object.Array{})
	}), PDFA4), "6.12") {
		t.Error("Requirements must be flagged")
	}
	if !hasRuleMsg(checkProhibitedCatalogEntries(mk(func(c *object.Dictionary, d core.View) {
		names := &object.Dictionary{}
		names.Set("AlternatePresentations", &object.Dictionary{})
		c.Set("Names", names)
	}), PDFA4), "6.11") {
		t.Error("AlternatePresentations must be flagged")
	}
	// Clean A-4 document passes.
	if len(checkProhibitedCatalogEntries(mkPDFAView(PDFA4), PDFA4)) != 0 {
		t.Error("clean document flagged")
	}
	// The /Requirements (6.12) prohibition is PDF/A-4 only; it must not fire at
	// 2b. A t.Skip here would let a level-gating regression pass silently.
	if got := len(checkProhibitedCatalogEntries(mk(func(c *object.Dictionary, d core.View) {
		c.Set("Requirements", object.Array{})
	}), PDFA2b)); got != 0 {
		t.Errorf("6.12 /Requirements must not be flagged at PDF/A-2b, got %d errors", got)
	}
	// 6.11 (AlternatePresentations / PresSteps) DOES apply at 2b and 3b.
	for _, lvl := range []Level{PDFA2b, PDFA3b} {
		altDoc := mk(func(c *object.Dictionary, d core.View) {
			names := &object.Dictionary{}
			names.Set("AlternatePresentations", &object.Dictionary{})
			c.Set("Names", names)
		})
		if !hasRuleMsg(checkProhibitedCatalogEntries(altDoc, lvl), "6.11") {
			t.Errorf("AlternatePresentations must be flagged at %s", lvl)
		}
	}
	// 1b does not use these clauses.
	if got := len(checkProhibitedCatalogEntries(mk(func(c *object.Dictionary, d core.View) {
		names := &object.Dictionary{}
		names.Set("AlternatePresentations", &object.Dictionary{})
		c.Set("Names", names)
	}), PDFA1b)); got != 0 {
		t.Errorf("6.11 must not be applied at PDF/A-1b, got %d errors", got)
	}
}

func TestFileTrailerID(t *testing.T) {
	mk := func(id object.Object) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, nil)
		if id != nil {
			doc.Trailer.Set("ID", id)
		}
		return doc
	}
	// Two non-empty strings: valid.
	valid := object.Array{object.String{Value: []byte("0123456789abcdef")}, object.String{Value: []byte("fedcba9876543210")}}
	if hasRuleMsg(checkFileTrailerID(mk(valid), PDFA2b), "6.1.3") {
		t.Error("valid ID flagged")
	}
	// Empty strings.
	if !hasRuleMsg(checkFileTrailerID(mk(object.Array{object.String{}, object.String{}}), PDFA2b), "6.1.3") {
		t.Error("empty ID strings not flagged")
	}
	// Wrong length.
	if !hasRuleMsg(checkFileTrailerID(mk(object.Array{object.String{Value: []byte("x")}}), PDFA2b), "6.1.3") {
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
	for _, k := range []object.Name{"WS", "O", "C", "PV", "DP"} {
		if !forbiddenAAEvents[k] {
			t.Errorf("%s should be forbidden", k)
		}
	}
	for _, k := range []object.Name{"E", "X", "D", "U", "Fo", "Bl", "PI"} {
		if forbiddenAAEvents[k] {
			t.Errorf("%s should be permitted", k)
		}
	}
}

func TestA4TriggerEvents(t *testing.T) {
	doc := mkPDFAView(PDFA4)
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	aa := &object.Dictionary{}
	aa.Set("WS", &object.Dictionary{})
	cat.Set("AA", aa)
	if !hasRuleMsg(checkA4TriggerEvents(doc, PDFA4), "6.6.3") {
		t.Error("catalog AA/WS must be flagged")
	}
	// Interaction-only AA passes.
	doc2 := mkPDFAView(PDFA4)
	cat2 := doc2.ResolveDict(doc2.Trailer.Get("Root"))
	aa2 := &object.Dictionary{}
	aa2.Set("Fo", &object.Dictionary{})
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
	mk := func(colorant string, hasTF bool) core.View {
		doc := mkPDFAView(PDFA4)
		comp := &object.Dictionary{}
		comp.Set("HalftoneType", object.Integer(1))
		if hasTF {
			comp.Set("TransferFunction", object.Name("Identity"))
		}
		ht := &object.Dictionary{}
		ht.Set("Type", object.Name("Halftone"))
		ht.Set("HalftoneType", object.Integer(5))
		ht.Set(object.Name(colorant), comp)
		doc.Objects[30] = &object.IndirectObject{Number: 30, Value: ht}
		gs := &object.Dictionary{}
		gs.Set("Type", object.Name("ExtGState"))
		gs.Set("HT", object.IndirectRef{Number: 30})
		gsDict := &object.Dictionary{}
		gsDict.Set("GS0", gs)
		res := &object.Dictionary{}
		res.Set("ExtGState", gsDict)
		page := addTestPage(doc)
		s := &object.Stream{Dict: object.Dictionary{}, Data: []byte("/GS0 gs")}
		s.Dict.Set("Length", object.Integer(7))
		doc.Objects[21] = &object.IndirectObject{Number: 21, Value: s}
		page.Set("Contents", object.IndirectRef{Number: 21})
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

// docWithInfoAndXMP is a PDF/A-1b-shaped document carrying an Info dictionary
// and an XMP packet.
func docWithInfoAndXMP(info *object.Dictionary, packet string) core.View {
	doc := docWithXMP([]byte(packet))
	doc.Objects[9] = &object.IndirectObject{Number: 9, Value: info}
	doc.Trailer.Set("Info", object.IndirectRef{Number: 9})
	return doc
}

func TestInfoAuthorMultiEntry(t *testing.T) {
	info := object.NewDictionary(object.Entry{Key: "Author", Value: object.String{Value: []byte("A")}})
	two := docWithInfoAndXMP(info, validXMP(`<dc:creator><rdf:Seq><rdf:li>A</rdf:li><rdf:li>B</rdf:li></rdf:Seq></dc:creator>`))
	if !hasMsg(checkInfoXMPConsistency(two, PDFA1b), "more than one entry") {
		t.Error("two dc:creator entries with Info /Author present must be flagged")
	}
	one := docWithInfoAndXMP(info, validXMP(`<dc:creator><rdf:Seq><rdf:li>A</rdf:li></rdf:Seq></dc:creator>`))
	if errs := checkInfoXMPConsistency(one, PDFA1b); len(errs) != 0 {
		t.Errorf("one matching dc:creator entry must pass, got %v", errs)
	}
}

// TestInfoXMPComparedUnescaped is the C35 regression: the XMP side of the 1b
// Info↔XMP comparison is the value the packet means, not its escaped spelling,
// and a language alternative is read at its x-default item.
func TestInfoXMPComparedUnescaped(t *testing.T) {
	for _, title := range []string{"Smith & Sons", "a < b > c", "Café é"} {
		info := object.NewDictionary(object.Entry{Key: "Title", Value: object.String{Value: []byte(title)}})
		var esc strings.Builder
		for _, r := range title {
			// Every non-ASCII character as a numeric reference, markup escaped.
			switch {
			case r == '&':
				esc.WriteString("&amp;")
			case r == '<':
				esc.WriteString("&lt;")
			case r == '>':
				esc.WriteString("&#62;")
			case r > 0x7E:
				fmt.Fprintf(&esc, "&#x%X;", r)
			default:
				esc.WriteRune(r)
			}
		}
		packet := validXMP(`<dc:title><rdf:Alt><rdf:li xml:lang="de">Anders</rdf:li><rdf:li xml:lang="x-default">` + esc.String() + `</rdf:li></rdf:Alt></dc:title>`)
		// Café in the Info dictionary is PDFDocEncoded, where é is the byte
		// 0xE9 (audit C77); build that rather than UTF-8.
		if pd, ok := pdfdocBytes(title); ok {
			info.Set("Title", object.String{Value: pd})
		}
		if errs := checkInfoXMPConsistency(docWithInfoAndXMP(info, packet), PDFA1b); len(errs) != 0 {
			t.Errorf("title %q: %v", title, errs)
		}
	}
}

func pdfdocBytes(s string) ([]byte, bool) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			return nil, false
		}
		out = append(out, byte(r))
	}
	return out, true
}

func TestIsPDFMIME(t *testing.T) {
	if !isPDFMIME(object.Name("application/pdf")) {
		t.Error("application/pdf not recognized")
	}
	if isPDFMIME(object.Name("text/plain")) || isPDFMIME(object.Integer(1)) || isPDFMIME(nil) {
		t.Error("non-pdf MIME wrongly recognized")
	}
}

func TestDeclaredPDFALevel(t *testing.T) {
	const ns = `http://www.aiim.org/pdfa/ns/id/`
	attrForm := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description rdf:about="" xmlns:pdfaid="` + ns + `" pdfaid:part="4" pdfaid:conformance="B"/></rdf:RDF></x:xmpmeta>`
	if lvl, ok := DeclaredLevel(docWithXMP([]byte(attrForm))); !ok || lvl != PDFA4 {
		t.Errorf("part=4 attr: got %v %v", lvl, ok)
	}
	elemForm := validXMP(`<pdfaid:part xmlns:pdfaid="` + ns + `">2</pdfaid:part>`)
	if lvl, ok := DeclaredLevel(docWithXMP([]byte(elemForm))); !ok || lvl != PDFA2b {
		t.Errorf("part=2 elem: got %v %v", lvl, ok)
	}
	// A value inside a comment is not a declaration (audit C141).
	commented := validXMP(`<!-- <pdfaid:part xmlns:pdfaid="` + ns + `">2</pdfaid:part> -->`)
	if _, ok := DeclaredLevel(docWithXMP([]byte(commented))); ok {
		t.Error("a pdfaid:part inside a comment was read as a declaration")
	}
	if _, ok := DeclaredLevel(docWithXMP([]byte(validXMP(``)))); ok {
		t.Error("document without pdfaid must not be PDF/A")
	}
}
func TestParseToUnicodeMapSpaceless(t *testing.T) {
	// bfrange with no separators between <hhhh> tokens (real-world format).
	cmap := "begincmap\n2 beginbfrange\n<0003><0003><0020>\n<0028><0028><0048>\nendbfrange\nendcmap"
	doc := mkView(map[int]*object.IndirectObject{}, nil)
	s := &object.Stream{Dict: object.Dictionary{}, Data: []byte(cmap)}
	s.Dict.Set("Length", object.Integer(len(cmap)))
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: s}
	fontDict := &object.Dictionary{}
	fontDict.Set("ToUnicode", object.IndirectRef{Number: 1})
	m, r := doc.ParseToUnicodeMap(fontDict)
	if r != core.ReasonOK {
		t.Fatalf("ParseToUnicodeMap reason = %v, want ok", r)
	}
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
		doc := mkView(map[int]*object.IndirectObject{}, nil)
		s := &object.Stream{Dict: object.Dictionary{}, Data: []byte(bad)}
		s.Dict.Set("Length", object.Integer(len(bad)))
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: s}
		fontDict := &object.Dictionary{}
		fontDict.Set("ToUnicode", object.IndirectRef{Number: 1})
		_, _ = doc.ParseToUnicodeMap(fontDict) // just must not panic
	}
}

func TestInheritedPageXObject(t *testing.T) {
	mk := func(pageHasOwn bool) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, nil)
		xo := &object.Dictionary{}
		xo.Set("X0", object.IndirectRef{Number: 90})
		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Parent", object.IndirectRef{Number: 2})
		page.Set("Contents", object.IndirectRef{Number: 91})
		if pageHasOwn {
			ownRes := &object.Dictionary{}
			ownRes.Set("XObject", xo)
			page.Set("Resources", ownRes)
		}
		pagesRes := &object.Dictionary{}
		pagesRes.Set("XObject", xo)
		pages := &object.Dictionary{}
		pages.Set("Type", object.Name("Pages"))
		pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
		pages.Set("Count", object.Integer(1))
		pages.Set("Resources", pagesRes)
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		cat.Set("Pages", object.IndirectRef{Number: 2})
		c := &object.Stream{Dict: object.Dictionary{}, Data: []byte("/X0 Do")}
		c.Dict.Set("Length", object.Integer(6))
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
		doc.Objects[3] = &object.IndirectObject{Number: 3, Value: page}
		doc.Objects[91] = &object.IndirectObject{Number: 91, Value: c}
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	if !hasRuleMsg(checkInheritedPageXObject(mk(false), PDFA4), "6.2.2") {
		t.Error("inherited page XObject must be flagged")
	}
	if hasRuleMsg(checkInheritedPageXObject(mk(true), PDFA4), "6.2.2") {
		t.Error("page with own XObject resource must pass")
	}
}

// TestA4EConformanceRelaxations checks that PDF/A-4e permits the 3D/RichMedia
// annotations and 3D/multimedia actions that plain PDF/A-4 forbids.
func TestA4EConformanceRelaxations(t *testing.T) {
	// isForbiddenAction: SetOCGState/GoTo3DView allowed only at conformance E.
	for _, act := range []object.Name{"SetOCGState", "GoTo3DView"} {
		if !isForbiddenAction(act, PDFA4, "") {
			t.Errorf("plain PDF/A-4 should forbid /%s", act)
		}
		if isForbiddenAction(act, PDFA4, "E") {
			t.Errorf("PDF/A-4e should permit /%s", act)
		}
	}
	// SetState/NOP stay forbidden even at 4e.
	if !isForbiddenAction("SetState", PDFA4, "E") {
		t.Error("PDF/A-4e must still forbid /SetState")
	}
}
