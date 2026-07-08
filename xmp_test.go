package pdf0

import (
	"fmt"
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

func TestParseXMPPropertyForms(t *testing.T) {
	xmp := wrapXMP(`
		<rdf:Description rdf:about="" xmlns:xmp="http://ns.adobe.com/xap/1.0/" xmp:CreatorTool="tool">
			<xmp:Identifier><rdf:Bag>
				<rdf:li>plain</rdf:li>
				<rdf:li rdf:parseType="Resource">
					<rdf:value>qualified</rdf:value>
					<xmpidq:Scheme xmlns:xmpidq="http://ns.adobe.com/xmp/Identifier/qual/1.0/">uri</xmpidq:Scheme>
				</rdf:li>
			</rdf:Bag></xmp:Identifier>
		</rdf:Description>
		<rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/">
			<dc:title><rdf:Alt><rdf:li xml:lang="x-default">T</rdf:li></rdf:Alt></dc:title>
		</rdf:Description>
		<rdf:Description rdf:about="" xmlns:xmpMM="http://ns.adobe.com/xap/1.0/mm/">
			<xmpMM:DerivedFrom rdf:parseType="Resource">
				<stRef:instanceID xmlns:stRef="http://ns.adobe.com/xap/1.0/sType/ResourceRef#">uuid:x</stRef:instanceID>
			</xmpMM:DerivedFrom>
		</rdf:Description>`)

	props, err := parseXMPProperties([]byte(xmp))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]xmpValue{}
	for _, p := range props {
		byName[p.Name] = p.Value
	}
	if v := byName["CreatorTool"]; v.Kind != xmpSimple || v.Text != "tool" {
		t.Errorf("attribute property: %+v", v)
	}
	if v := byName["Identifier"]; v.Kind != xmpBag || len(v.Items) != 2 {
		t.Fatalf("Identifier: %+v", byName["Identifier"])
	} else {
		if v.Items[0].Kind != xmpSimple || v.Items[0].Text != "plain" {
			t.Errorf("plain item: %+v", v.Items[0])
		}
		// Qualified value (rdf:value) is a simple value, not a structure.
		if v.Items[1].Kind != xmpSimple || v.Items[1].Text != "qualified" {
			t.Errorf("qualified item: %+v", v.Items[1])
		}
	}
	if v := byName["title"]; v.Kind != xmpAlt || len(v.Items) != 1 || !v.Items[0].HasLang {
		t.Errorf("title: %+v", byName["title"])
	}
	if v := byName["DerivedFrom"]; v.Kind != xmpStruct || len(v.Fields) != 1 {
		t.Errorf("DerivedFrom: %+v", byName["DerivedFrom"])
	}
}

func TestXMPSyntaxValidators(t *testing.T) {
	cases := []struct {
		syn xmpSyntax
		ok  []string
		bad []string
	}{
		{synInteger, []string{"0", "42", "-7", "+3"}, []string{"", "1.0", "x", "1e3", "-"}},
		{synReal, []string{"1", "1.5", "-0.25", "+2."}, []string{"", "1/2", "x", "1.2.3"}},
		{synRational, []string{"152/1", "-3/4"}, []string{"", "1.5", "3/", "/4", "a/b"}},
		{synBoolean, []string{"True", "False"}, []string{"true", "false", "1", ""}},
		{synDate, []string{"2024", "2024-03", "2024-03-10", "2015-03-10T17:19:21+01:00",
			"2015-03-10T17:19Z", "2015-03-10T17:19:21.5Z"},
			[]string{"", "20240310", "2015-03-10T17", "2015-03-10T17:19:21", "2015-03-10 17:19:21Z"}},
	}
	for _, tc := range cases {
		for _, s := range tc.ok {
			if !validXMPSyntax(s, tc.syn) {
				t.Errorf("%v: %q should be valid", tc.syn, s)
			}
		}
		for _, s := range tc.bad {
			if validXMPSyntax(s, tc.syn) {
				t.Errorf("%v: %q should be invalid", tc.syn, s)
			}
		}
	}
}

// setDocXMP replaces a built document's metadata stream body.
func setDocXMP(doc *Document, xmp string) {
	meta := doc.Objects[3].Value.(*Stream)
	// Preserve the builder's pdfaid block by injecting extra descriptions
	// before the closing tag of the original packet instead of replacing it.
	meta.Data = []byte(xmp)
	meta.Dict.Set("Length", Integer(len(xmp)))
}

func xmpWithPDFAID(level PDFALevel, extra string) string {
	part := "1"
	if level == PDFA2b || level == PDFA3b {
		part = "2"
	}
	return wrapXMP(fmt.Sprintf(`
		<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" pdfaid:part="%s" pdfaid:conformance="B"/>
		%s`, part, extra))
}

func TestValidatePDFA_XMPUnknownProperty(t *testing.T) {
	doc := NewPDFADocument(PDFA1b)
	setDocXMP(doc, xmpWithPDFAID(PDFA1b, `
		<rdf:Description rdf:about="" xmlns:xmp="http://ns.adobe.com/xap/1.0/" xmp:Author="A"/>`))
	if !hasRule(ValidatePDFA(doc, PDFA1b), "6.7.2") {
		t.Error("expected 6.7.2 error for xmp:Author (not in XMP Basic)")
	}
}

func TestValidatePDFA_XMPWrongForm(t *testing.T) {
	doc := NewPDFADocument(PDFA1b)
	setDocXMP(doc, xmpWithPDFAID(PDFA1b, `
		<rdf:Description rdf:about="" xmlns:pdf="http://ns.adobe.com/pdf/1.3/">
			<pdf:Keywords><rdf:Seq><rdf:li>k</rdf:li></rdf:Seq></pdf:Keywords>
		</rdf:Description>`))
	if !hasRule(ValidatePDFA(doc, PDFA1b), "6.7.2") {
		t.Error("expected 6.7.2 error for pdf:Keywords as Seq (must be Text)")
	}
}

func TestValidatePDFA_XMPSyntaxChecked(t *testing.T) {
	doc := NewPDFADocument(PDFA2b)
	setDocXMP(doc, xmpWithPDFAID(PDFA2b, `
		<rdf:Description rdf:about="" xmlns:exif="http://ns.adobe.com/exif/1.0/">
			<exif:ColorSpace>1.3</exif:ColorSpace>
		</rdf:Description>`))
	if !hasRule(ValidatePDFA(doc, PDFA2b), "6.6.2.3") {
		t.Error("expected 6.6.2.3 error for non-integer exif:ColorSpace")
	}
}

func TestValidatePDFA_XMPLevelDependentSchemas(t *testing.T) {
	// crs is predefined at 2b (XMP 2005) but not at 1b (XMP 2004).
	body := `<rdf:Description rdf:about="" xmlns:crs="http://ns.adobe.com/camera-raw-settings/1.0/" crs:AutoBrightness="True"/>`

	doc1 := NewPDFADocument(PDFA1b)
	setDocXMP(doc1, xmpWithPDFAID(PDFA1b, body))
	if !hasRule(ValidatePDFA(doc1, PDFA1b), "6.7.2") {
		t.Error("crs property must fail at 1b (schema not predefined, no extension schema)")
	}

	doc2 := NewPDFADocument(PDFA2b)
	setDocXMP(doc2, xmpWithPDFAID(PDFA2b, body))
	if hasRule(ValidatePDFA(doc2, PDFA2b), "6.6.2.3") {
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
	doc := NewPDFADocument(PDFA2b)
	setDocXMP(doc, xmpWithPDFAID(PDFA2b, extSchemaOK+use))
	if errs := filterRule(ValidatePDFA(doc, PDFA2b), "6.6.2.3"); len(errs) > 0 {
		t.Errorf("declared extension property should pass: %v", errs)
	}

	// Declared but value violates the declared Integer type.
	badUse := `<rdf:Description rdf:about="" xmlns:ex="http://example.org/ns/"><ex:thing>x</ex:thing></rdf:Description>`
	doc2 := NewPDFADocument(PDFA2b)
	setDocXMP(doc2, xmpWithPDFAID(PDFA2b, extSchemaOK+badUse))
	if !hasRule(ValidatePDFA(doc2, PDFA2b), "6.6.2.3") {
		t.Error("declared-type violation must be flagged")
	}

	// Undeclared property in the declared namespace.
	otherUse := `<rdf:Description rdf:about="" xmlns:ex="http://example.org/ns/"><ex:other>5</ex:other></rdf:Description>`
	doc3 := NewPDFADocument(PDFA2b)
	setDocXMP(doc3, xmpWithPDFAID(PDFA2b, extSchemaOK+otherUse))
	if !hasRule(ValidatePDFA(doc3, PDFA2b), "6.6.2.3") {
		t.Error("undeclared property in extension namespace must be flagged")
	}

	// Unknown namespace with no extension schema at all.
	doc4 := NewPDFADocument(PDFA2b)
	setDocXMP(doc4, xmpWithPDFAID(PDFA2b, use))
	if !hasRule(ValidatePDFA(doc4, PDFA2b), "6.6.2.3") {
		t.Error("unknown schema without declaration must be flagged")
	}
}

func TestValidatePDFA_XMPExtensionContainerRules(t *testing.T) {
	// Missing pdfaSchema:prefix.
	missingPrefix := strings.Replace(extSchemaOK, "<pdfaSchema:prefix>ex</pdfaSchema:prefix>", "", 1)
	doc := NewPDFADocument(PDFA2b)
	setDocXMP(doc, xmpWithPDFAID(PDFA2b, missingPrefix))
	if !hasRule(ValidatePDFA(doc, PDFA2b), "6.6.2.3") {
		t.Error("missing pdfaSchema:prefix must be flagged")
	}

	// Non-canonical prefix bound to the pdfaSchema namespace.
	badPrefix := strings.ReplaceAll(extSchemaOK, "pdfaSchema:", "wrongPrefix:")
	badPrefix = strings.Replace(badPrefix, `xmlns:wrongPrefix="http://www.aiim.org/pdfa/ns/schema#"`,
		`xmlns:wrongPrefix="http://www.aiim.org/pdfa/ns/schema#"`, 1)
	doc2 := NewPDFADocument(PDFA2b)
	setDocXMP(doc2, xmpWithPDFAID(PDFA2b, badPrefix))
	if !hasRule(ValidatePDFA(doc2, PDFA2b), "6.6.2.3") {
		t.Error("non-canonical extension prefix must be flagged")
	}

	// Missing pdfaProperty:category.
	missingCat := strings.Replace(extSchemaOK, "<pdfaProperty:category>external</pdfaProperty:category>", "", 1)
	doc3 := NewPDFADocument(PDFA2b)
	setDocXMP(doc3, xmpWithPDFAID(PDFA2b, missingCat))
	if !hasRule(ValidatePDFA(doc3, PDFA2b), "6.6.2.3") {
		t.Error("missing pdfaProperty:category must be flagged")
	}
}

// TestExtensionFieldUndeclaredType ensures a field whose value type is neither
// a standard type nor declared by the extension schema is flagged
// (ISO 19005-1 6.7.8).
func TestExtensionFieldUndeclaredType(t *testing.T) {
	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/" xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#" xmlns:pdfaProperty="http://www.aiim.org/pdfa/ns/property#" xmlns:pdfaType="http://www.aiim.org/pdfa/ns/type#" xmlns:pdfaField="http://www.aiim.org/pdfa/ns/field#">
<pdfaExtension:schemas><rdf:Bag><rdf:li rdf:parseType="Resource">
<pdfaSchema:schema>S</pdfaSchema:schema><pdfaSchema:namespaceURI>http://x/</pdfaSchema:namespaceURI><pdfaSchema:prefix>x</pdfaSchema:prefix>
<pdfaSchema:valueType><rdf:Seq><rdf:li rdf:parseType="Resource">
<pdfaType:type>mailaddress</pdfaType:type><pdfaType:namespaceURI>http://x/m/</pdfaType:namespaceURI><pdfaType:prefix>m</pdfaType:prefix><pdfaType:description>d</pdfaType:description>
<pdfaType:field><rdf:Seq><rdf:li rdf:parseType="Resource">
<pdfaField:name>mailto</pdfaField:name><pdfaField:valueType>CT</pdfaField:valueType><pdfaField:description>e</pdfaField:description>
</rdf:li></rdf:Seq></pdfaType:field>
</rdf:li></rdf:Seq></pdfaSchema:valueType>
</rdf:li></rdf:Bag></pdfaExtension:schemas>
</rdf:Description></rdf:RDF></x:xmpmeta>`
	props, err := parseXMPProperties([]byte(xmp))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := checkXMPExtensionContainer(xmp, props, "6.7.8", PDFA1b)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, `"CT"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("undeclared field value type CT not flagged; errs=%v", errs)
	}
}

// TestXMPUndeclaredRDFPrefix ensures a packet whose rdf:RDF element uses an
// undeclared namespace prefix is flagged. encoding/xml resolves a declared
// prefix to its URI, so an undeclared <RDF:RDF> leaves the raw prefix as the
// namespace (ISO 19005-1; Isartor 6.7.2-t02-fail-a).
func TestXMPUndeclaredRDFPrefix(t *testing.T) {
	mk := func(body string) *Document {
		meta := &Stream{Dict: Dictionary{}, Data: []byte(body)}
		meta.Dict.Set("Type", Name("Metadata"))
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		cat.Set("Metadata", IndirectRef{Number: 2})
		return &Document{Version: "1.7", Objects: map[int]*IndirectObject{
			1: {Number: 1, Value: cat},
			2: {Number: 2, Value: meta},
		}, Trailer: dictWith("Root", IndirectRef{Number: 1})}
	}
	rdfNS := `xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"`
	bad := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><RDF:RDF ` + rdfNS + `><rdf:Description/></RDF:RDF></x:xmpmeta>`
	good := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF ` + rdfNS + `><rdf:Description/></rdf:RDF></x:xmpmeta>`
	if got := len(checkXMPWellFormed(mk(bad), PDFA1b)); got == 0 {
		t.Error("undeclared RDF prefix not flagged")
	}
	if got := len(checkXMPWellFormed(mk(good), PDFA1b)); got != 0 {
		t.Errorf("valid rdf:RDF wrongly flagged: %d", got)
	}
}
