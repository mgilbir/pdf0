package pdf0

import (
	"fmt"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"strings"
	"testing"
)

func wrapXMP(body string) string {
	return `<?xpacket begin='' id='W5M0MpCehiHzreSzNTczkc9d'?>` +
		`<x:xmpmeta xmlns:x="adobe:ns:meta/">` +
		`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		body +
		`</rdf:RDF></x:xmpmeta><?xpacket end='w'?>`
}

// setDocXMP replaces a built document's metadata stream body.
func setDocXMP(doc *Document, xmp string) {
	meta := doc.Objects[3].Value.(*object.Stream)
	// Preserve the builder's pdfaid block by injecting extra descriptions
	// before the closing tag of the original packet instead of replacing it.
	meta.Data = []byte(xmp)
	meta.Dict.Set("Length", object.Integer(len(xmp)))
}

func xmpWithPDFAID(level pdfa.Level, extra string) string {
	part := "1"
	if level == pdfa.PDFA2b || level == pdfa.PDFA3b {
		part = "2"
	}
	return wrapXMP(fmt.Sprintf(`
		<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" pdfaid:part="%s" pdfaid:conformance="B"/>
		%s`, part, extra))
}

func TestValidatePDFA_XMPUnknownProperty(t *testing.T) {
	doc := mustPDFADoc(t, pdfa.PDFA1b)
	setDocXMP(doc, xmpWithPDFAID(pdfa.PDFA1b, `
		<rdf:Description rdf:about="" xmlns:xmp="http://ns.adobe.com/xap/1.0/" xmp:Author="A"/>`))
	// PDF/A-1 numbers the XMP schema rule 6.7.9 (veraPDF 6.7.9-2).
	if !hasRule(ValidatePDFA(doc, pdfa.PDFA1b), "6.7.9") {
		t.Error("expected 6.7.9 error for xmp:Author (not in XMP Basic)")
	}
}

func TestValidatePDFA_XMPWrongForm(t *testing.T) {
	doc := mustPDFADoc(t, pdfa.PDFA1b)
	setDocXMP(doc, xmpWithPDFAID(pdfa.PDFA1b, `
		<rdf:Description rdf:about="" xmlns:pdf="http://ns.adobe.com/pdf/1.3/">
			<pdf:Keywords><rdf:Seq><rdf:li>k</rdf:li></rdf:Seq></pdf:Keywords>
		</rdf:Description>`))
	// A value of the wrong type is veraPDF 6.7.9-3.
	if !hasRule(ValidatePDFA(doc, pdfa.PDFA1b), "6.7.9") {
		t.Error("expected 6.7.9 error for pdf:Keywords as Seq (must be Text)")
	}
}

func TestValidatePDFA_XMPSyntaxChecked(t *testing.T) {
	doc := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(doc, xmpWithPDFAID(pdfa.PDFA2b, `
		<rdf:Description rdf:about="" xmlns:exif="http://ns.adobe.com/exif/1.0/">
			<exif:ColorSpace>1.3</exif:ColorSpace>
		</rdf:Description>`))
	if !hasRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.6.2.3.1") {
		t.Error("expected 6.6.2.3.1 error for non-integer exif:ColorSpace")
	}
}

// TestMalformedXMPNamesItsCause: a metadata packet that is not well-formed
// XML is reported, by the PDF/A well-formedness rule and the PDF/UA
// identification rule alike, with the XML parser's reason and the line it
// stopped on — the same words from both, since both judge the packet with the
// one XMP model (xmp.CheckWellFormed is xmp.Parse without the tree). The
// U+0001 case is the WTPDF example tb139mitt-deims24's: XML 1.0's Char
// production excludes it, so the packet is not XML.
func TestMalformedXMPNamesItsCause(t *testing.T) {
	for _, tc := range []struct{ name, packet, reason string }{
		{"control character", wrapXMP("\n<rdf:Description rdf:about=\"\">\x01</rdf:Description>"),
			"(XML syntax error on line 2: illegal character code U+0001)"},
		{"mismatched tags", wrapXMP("\n\n<rdf:Description rdf:about=\"\"></rdf:Seq>"),
			"(XML syntax error on line 3: element <rdf:Description> closed by </rdf:Seq>)"},
		{"unclosed element", wrapXMP("")[:len(wrapXMP(""))-len("</rdf:RDF></x:xmpmeta><?xpacket end='w'?>")] + "\n",
			"(XML syntax error on line 2: unexpected end of packet inside <rdf:RDF>)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustPDFADoc(t, pdfa.PDFA2b)
			setDocXMP(doc, tc.packet)
			var a []string
			for _, v := range ValidatePDFA(doc, pdfa.PDFA2b) {
				if v.Rule == "6.6.2.1" && strings.Contains(v.Message, "not well-formed") {
					a = append(a, v.Message)
				}
			}
			if len(a) != 1 || !strings.Contains(a[0], tc.reason) {
				t.Errorf("PDF/A 6.6.2.1: want one finding naming %s, got %q", tc.reason, a)
			}
			var ua []string
			for _, v := range ValidatePDFUA(doc) {
				if v.Clause == "5" && strings.Contains(v.Message, "not well-formed") {
					ua = append(ua, v.Message)
				}
			}
			if len(ua) != 1 || !strings.Contains(ua[0], tc.reason) {
				t.Errorf("PDF/UA 5: want one finding naming %s, got %q", tc.reason, ua)
			}
		})
	}
}

func TestValidatePDFA_XMPLevelDependentSchemas(t *testing.T) {
	// crs is predefined at 2b (XMP 2005) but not at 1b (XMP 2004).
	body := `<rdf:Description rdf:about="" xmlns:crs="http://ns.adobe.com/camera-raw-settings/1.0/" crs:AutoBrightness="True"/>`

	doc1 := mustPDFADoc(t, pdfa.PDFA1b)
	setDocXMP(doc1, xmpWithPDFAID(pdfa.PDFA1b, body))
	if !hasRule(ValidatePDFA(doc1, pdfa.PDFA1b), "6.7.9") {
		t.Error("crs property must fail at 1b (schema not predefined, no extension schema)")
	}

	doc2 := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(doc2, xmpWithPDFAID(pdfa.PDFA2b, body))
	if hasRule(ValidatePDFA(doc2, pdfa.PDFA2b), "6.6.2.3.3") {
		t.Error("crs property must pass at 2b (predefined in XMP 2005)")
	}
}

const extSchemaOK = `
	<rdf:Description rdf:about=""
		xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/"
		xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#"
		xmlns:pdfaProperty="http://www.aiim.org/pdfa/ns/property#">
		<pdfaExtension:schemas><rdf:Bag><rdf:li rdf:parseType="Resource">
			<pdfaSchema:schema>Custom</pdfaSchema:schema>
			<pdfaSchema:namespaceURI>http://example.org/ns/</pdfaSchema:namespaceURI>
			<pdfaSchema:prefix>ex</pdfaSchema:prefix>
			<pdfaSchema:property><rdf:Seq><rdf:li rdf:parseType="Resource">
				<pdfaProperty:name>thing</pdfaProperty:name>
				<pdfaProperty:valueType>Integer</pdfaProperty:valueType>
				<pdfaProperty:category>external</pdfaProperty:category>
				<pdfaProperty:description>d</pdfaProperty:description>
			</rdf:li></rdf:Seq></pdfaSchema:property>
		</rdf:li></rdf:Bag></pdfaExtension:schemas>
	</rdf:Description>`

func TestValidatePDFA_XMPExtensionSchema(t *testing.T) {
	use := `<rdf:Description rdf:about="" xmlns:ex="http://example.org/ns/"><ex:thing>5</ex:thing></rdf:Description>`

	// Declared and well-typed: passes.
	doc := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(doc, xmpWithPDFAID(pdfa.PDFA2b, extSchemaOK+use))
	if errs := filterRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.6.2.3.3"); len(errs) > 0 {
		t.Errorf("declared extension property should pass: %v", errs)
	}

	// Declared but value violates the declared Integer type.
	badUse := `<rdf:Description rdf:about="" xmlns:ex="http://example.org/ns/"><ex:thing>x</ex:thing></rdf:Description>`
	doc2 := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(doc2, xmpWithPDFAID(pdfa.PDFA2b, extSchemaOK+badUse))
	if !hasRule(ValidatePDFA(doc2, pdfa.PDFA2b), "6.6.2.3.1") {
		t.Error("declared-type violation must be flagged")
	}

	// Undeclared property in the declared namespace.
	otherUse := `<rdf:Description rdf:about="" xmlns:ex="http://example.org/ns/"><ex:other>5</ex:other></rdf:Description>`
	doc3 := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(doc3, xmpWithPDFAID(pdfa.PDFA2b, extSchemaOK+otherUse))
	if !hasRule(ValidatePDFA(doc3, pdfa.PDFA2b), "6.6.2.3.1") {
		t.Error("undeclared property in extension namespace must be flagged")
	}

	// Unknown namespace with no extension schema at all.
	doc4 := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(doc4, xmpWithPDFAID(pdfa.PDFA2b, use))
	if !hasRule(ValidatePDFA(doc4, pdfa.PDFA2b), "6.6.2.3.1") {
		t.Error("unknown schema without declaration must be flagged")
	}
}

// 6.6.2.3.2: an extension schema object must not carry a field the spec does
// not define.
func TestValidatePDFA_XMPUndefinedField(t *testing.T) {
	withExtra := strings.Replace(extSchemaOK, "<pdfaSchema:prefix>ex</pdfaSchema:prefix>",
		"<pdfaSchema:prefix>ex</pdfaSchema:prefix><pdfaSchema:bogus>x</pdfaSchema:bogus>", 1)
	doc := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(doc, xmpWithPDFAID(pdfa.PDFA2b, withExtra))
	if !hasRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.6.2.3.2") {
		t.Error("undefined pdfaSchema field must be flagged as 6.6.2.3.2")
	}
	// The unmodified schema must stay clean.
	clean := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(clean, xmpWithPDFAID(pdfa.PDFA2b, extSchemaOK))
	if hasRule(ValidatePDFA(clean, pdfa.PDFA2b), "6.6.2.3.2") {
		t.Error("a well-formed extension schema must not be flagged")
	}
}

func TestValidatePDFA_XMPExtensionContainerRules(t *testing.T) {
	// Missing pdfaSchema:prefix.
	missingPrefix := strings.Replace(extSchemaOK, "<pdfaSchema:prefix>ex</pdfaSchema:prefix>", "", 1)
	doc := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(doc, xmpWithPDFAID(pdfa.PDFA2b, missingPrefix))
	if !hasRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.6.2.3.3") {
		t.Error("missing pdfaSchema:prefix must be flagged")
	}

	// Non-canonical prefix bound to the pdfaSchema namespace.
	badPrefix := strings.ReplaceAll(extSchemaOK, "pdfaSchema:", "wrongPrefix:")
	badPrefix = strings.Replace(badPrefix, `xmlns:wrongPrefix="http://www.aiim.org/pdfa/ns/schema#"`,
		`xmlns:wrongPrefix="http://www.aiim.org/pdfa/ns/schema#"`, 1)
	doc2 := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(doc2, xmpWithPDFAID(pdfa.PDFA2b, badPrefix))
	if !hasRule(ValidatePDFA(doc2, pdfa.PDFA2b), "6.6.2.3.3") {
		t.Error("non-canonical extension prefix must be flagged")
	}

	// Missing pdfaProperty:category.
	missingCat := strings.Replace(extSchemaOK, "<pdfaProperty:category>external</pdfaProperty:category>", "", 1)
	doc3 := mustPDFADoc(t, pdfa.PDFA2b)
	setDocXMP(doc3, xmpWithPDFAID(pdfa.PDFA2b, missingCat))
	if !hasRule(ValidatePDFA(doc3, pdfa.PDFA2b), "6.6.2.3.3") {
		t.Error("missing pdfaProperty:category must be flagged")
	}
}
