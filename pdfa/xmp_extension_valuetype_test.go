package pdfa

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// An XMP value has to match the shape its own extension schema declares for it.
//
// Clause 6.7.8 (ISO 19005-1; 6.6.2.3.x in -2/-3) makes a document describe any
// property outside the predefined schemas, so that a reader meeting an unknown
// property can find out what it holds. Everything around this was already
// checked — that the description is well formed, that its fields are the ones
// the specification names, that a field's value type is a type that exists.
// What was not checked is whether the document then honours it.
//
// A declaration nothing has to honour is not a declaration. This is the file
// that proves it: Isartor 6-7-8-t02-fail-k declares a custom `mailaddress`
// type with `name` and `mailto` fields, then writes the property as
// `John Doe, john@acme.com`. It was the last undetected file in that suite.

// extXMP wraps an extension schema and some instance data into a packet. The
// two arguments are the pdfaSchema:valueType Seq body and the rdf:Description
// carrying the values.
func extXMP(valueTypes, instance string) string {
	return `<?xpacket begin=""?><x:xmpmeta xmlns:x="adobe:ns:meta/">
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
 <rdf:Description rdf:about=""
   xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/"
   xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#"
   xmlns:pdfaProperty="http://www.aiim.org/pdfa/ns/property#"
   xmlns:pdfaType="http://www.aiim.org/pdfa/ns/type#"
   xmlns:pdfaField="http://www.aiim.org/pdfa/ns/field#">
  <pdfaExtension:schemas><rdf:Bag><rdf:li rdf:parseType="Resource">
   <pdfaSchema:schema>ACME</pdfaSchema:schema>
   <pdfaSchema:namespaceURI>http://acme.example/ns/1/</pdfaSchema:namespaceURI>
   <pdfaSchema:prefix>acme</pdfaSchema:prefix>
   <pdfaSchema:property><rdf:Seq><rdf:li rdf:parseType="Resource">
     <pdfaProperty:name>From</pdfaProperty:name>
     <pdfaProperty:valueType>mailaddress</pdfaProperty:valueType>
     <pdfaProperty:category>internal</pdfaProperty:category>
     <pdfaProperty:description>sender</pdfaProperty:description>
   </rdf:li></rdf:Seq></pdfaSchema:property>
   <pdfaSchema:valueType><rdf:Seq>` + valueTypes + `</rdf:Seq></pdfaSchema:valueType>
  </rdf:li></rdf:Bag></pdfaExtension:schemas>
 </rdf:Description>
 <rdf:Description rdf:about="" xmlns:acme="http://acme.example/ns/1/"
   xmlns:add="http://acme.example/ns/1/mailaddress/">` + instance + `</rdf:Description>
</rdf:RDF></x:xmpmeta><?xpacket end="r"?>`
}

// structuredType declares mailaddress with two fields; simpleType declares it
// with none, which makes it a simple type under another name.
const structuredType = `<rdf:li rdf:parseType="Resource">
 <pdfaType:type>mailaddress</pdfaType:type>
 <pdfaType:namespaceURI>http://acme.example/ns/1/mailaddress/</pdfaType:namespaceURI>
 <pdfaType:prefix>add</pdfaType:prefix>
 <pdfaType:description>an address</pdfaType:description>
 <pdfaType:field><rdf:Seq>
  <rdf:li rdf:parseType="Resource">
   <pdfaField:name>name</pdfaField:name>
   <pdfaField:valueType>Text</pdfaField:valueType>
   <pdfaField:description>the name</pdfaField:description></rdf:li>
  <rdf:li rdf:parseType="Resource">
   <pdfaField:name>mailto</pdfaField:name>
   <pdfaField:valueType>Text</pdfaField:valueType>
   <pdfaField:description>the address</pdfaField:description></rdf:li>
 </rdf:Seq></pdfaType:field>
</rdf:li>`

const simpleType = `<rdf:li rdf:parseType="Resource">
 <pdfaType:type>mailaddress</pdfaType:type>
 <pdfaType:namespaceURI>http://acme.example/ns/1/mailaddress/</pdfaType:namespaceURI>
 <pdfaType:prefix>add</pdfaType:prefix>
 <pdfaType:description>an address</pdfaType:description>
</rdf:li>`

func xmpView(xmp string) core.View {
	meta := &object.Stream{Dict: object.Dictionary{}, Data: []byte(xmp)}
	meta.Dict.Set("Type", object.Name("Metadata"))
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Metadata", object.IndirectRef{Number: 2})
	return mkV(core.View{Version: "1.7", Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: cat},
		2: {Number: 2, Value: meta},
	}, Trailer: ptrDict(dictWith("Root", object.IndirectRef{Number: 1}))})
}

func declaredStructure(errs []Violation) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, "defines as a structure") {
			return true
		}
	}
	return false
}

func TestAValueMustMatchTheStructureItsSchemaDeclares(t *testing.T) {
	// The violation: declared a structure, written as text.
	errs := checkXMPProperties(xmpView(extXMP(structuredType,
		`<acme:From>John Doe, john@acme.com</acme:From>`)), PDFA1b)
	if !declaredStructure(errs) {
		t.Errorf("a text value for a property declared as a structured type was "+
			"not reported: %v", errs)
	}

	// Written as the structure it promised, which must stay clean. This is the
	// same document the corpus's sibling file writes, so a rule that reported
	// it would be a false positive on a conforming file.
	errs = checkXMPProperties(xmpView(extXMP(structuredType,
		`<acme:From rdf:parseType="Resource">
		   <add:name>John Doe</add:name>
		   <add:mailto>john@acme.com</add:mailto>
		 </acme:From>`)), PDFA1b)
	if len(errs) != 0 {
		t.Errorf("a conforming structured value was reported: %v", errs)
	}

	// A custom type that declares no fields is a simple type under another
	// name, and a text value is exactly what it should have. Requiring a
	// structure here would condemn a conforming document, and no corpus file
	// covers it.
	errs = checkXMPProperties(xmpView(extXMP(simpleType,
		`<acme:From>John Doe, john@acme.com</acme:From>`)), PDFA1b)
	if declaredStructure(errs) {
		t.Errorf("a text value for a custom type that declares no fields was "+
			"reported: %v", errs)
	}
}

// TestTheStructureRuleHoldsAtEveryLevelThatHasTheClause.
func TestTheStructureRuleHoldsAtEveryLevelThatHasTheClause(t *testing.T) {
	bad := extXMP(structuredType, `<acme:From>John Doe, john@acme.com</acme:From>`)
	for _, level := range []Level{PDFA1b, PDFA2b, PDFA3b} {
		if !declaredStructure(checkXMPProperties(xmpView(bad), level)) {
			t.Errorf("%s: not reported", level)
		}
	}
	// PDF/A-4 tolerates non-conforming XMP property values by design; see the
	// note on checkXMPProperties. Asserted so that a change there is a
	// decision rather than an accident.
	if declaredStructure(checkXMPProperties(xmpView(bad), PDFA4)) {
		t.Error("PDF/A-4 reported an XMP property value, which it deliberately does not check")
	}
}

// TestTheReportNamesTheFieldsInAFixedOrder, because a violation message that
// reorders between runs makes a diff of two reports unreadable.
func TestTheReportNamesTheFieldsInAFixedOrder(t *testing.T) {
	bad := extXMP(structuredType, `<acme:From>x</acme:From>`)
	first := ""
	for i := 0; i < 8; i++ {
		for _, e := range checkXMPProperties(xmpView(bad), PDFA1b) {
			if !strings.Contains(e.Message, "defines as a structure") {
				continue
			}
			if first == "" {
				first = e.Message
			} else if e.Message != first {
				t.Fatalf("the message varies between runs:\n  %s\n  %s", first, e.Message)
			}
		}
	}
	if !strings.Contains(first, "(mailto, name)") {
		t.Errorf("the message does not name the declared fields in order: %q", first)
	}
}
