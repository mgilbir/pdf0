package facturx

import (
	"fmt"
	"github.com/mgilbir/formalis"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"time"
)

// This file produces Factur-X invoices. EmbedFacturX turns a PDF/A-3 document
// into a Factur-X hybrid invoice by embedding the Cross Industry Invoice XML as
// an associated file and writing the Factur-X XMP metadata that identifies it.
// The XMP includes the PDF/A extension schema that declares the fx: namespace,
// as PDF/A requires for a custom metadata schema.

// Embed embeds the CII invoice XML into doc as the associated file
// factur-x.xml and writes the Factur-X metadata for the given profile. doc must
// be a valid PDF/A-3 document (for example from NewPDFADocument(PDFA3b)); the
// result is a Factur-X container that ValidateFacturX accepts after a round
// trip. title, when non-empty, is recorded as the document title in the XMP.
func Embed(doc core.View, invoiceXML []byte, profile formalis.Profile, title string) error {
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		return fmt.Errorf("document has no catalog")
	}
	if _, ok := formalis.ProfileFor(string(profile)); !ok {
		return fmt.Errorf("unknown Factur-X profile %q", profile)
	}

	next := 1
	for n := range doc.Objects {
		if n >= next {
			next = n + 1
		}
	}
	newObj := func(v object.Object) int {
		n := next
		next++
		doc.Objects[n] = &object.IndirectObject{Number: n, Value: v}
		return n
	}

	modDate := "D:" + time.Now().UTC().Format("20060102150405") + "+00'00'"

	// Embedded file stream holding the invoice XML.
	ef := &object.Stream{Dict: object.Dictionary{}, Data: append([]byte(nil), invoiceXML...)}
	ef.Dict.Set("Type", object.Name("EmbeddedFile"))
	ef.Dict.Set("Subtype", object.Name("text/xml"))
	params := &object.Dictionary{}
	params.Set("ModDate", object.String{Value: []byte(modDate)})
	params.Set("Size", object.Integer(len(invoiceXML)))
	ef.Dict.Set("Params", params)
	efNum := newObj(ef)

	// File specification associating the embedded XML with the document.
	efEntry := &object.Dictionary{}
	efEntry.Set("F", object.IndirectRef{Number: efNum})
	efEntry.Set("UF", object.IndirectRef{Number: efNum})
	fs := &object.Dictionary{}
	fs.Set("Type", object.Name("Filespec"))
	fs.Set("F", object.String{Value: []byte(facturxFileName)})
	fs.Set("UF", object.String{Value: EncodeUTF16BE(facturxFileName)})
	fs.Set("AFRelationship", object.Name("Data"))
	fs.Set("Desc", object.String{Value: []byte("Factur-X XML invoice")})
	fs.Set("EF", efEntry)
	fsNum := newObj(fs)

	// Catalog /AF associated-files array.
	af, _ := doc.Resolve(cat.Get("AF")).(object.Array)
	cat.Set("AF", append(af, object.IndirectRef{Number: fsNum}))

	// EmbeddedFiles name tree.
	names := doc.ResolveDict(cat.Get("Names"))
	if names == nil {
		names = &object.Dictionary{}
		cat.Set("Names", object.IndirectRef{Number: newObj(names)})
	}
	efTree := &object.Dictionary{}
	efTree.Set("Names", object.Array{object.String{Value: []byte(facturxFileName)}, object.IndirectRef{Number: fsNum}})
	names.Set("EmbeddedFiles", object.IndirectRef{Number: newObj(efTree)})

	// Factur-X XMP metadata, reusing the existing metadata object number when
	// present so no orphan stream is left behind.
	md := &object.Stream{Dict: object.Dictionary{}, Data: XMPPacket(profile, "INVOICE", title)}
	md.Dict.Set("Type", object.Name("Metadata"))
	md.Dict.Set("Subtype", object.Name("XML"))
	if n := object.RefNum(cat.Get("Metadata")); n != 0 {
		doc.Objects[n] = &object.IndirectObject{Number: n, Value: md}
	} else {
		cat.Set("Metadata", object.IndirectRef{Number: newObj(md)})
	}

	return nil
}

const facturxFileName = "factur-x.xml"

// EncodeUTF16BE encodes s as a PDF text string: a UTF-16BE byte-order mark
// followed by big-endian code units (used for Unicode file-spec /UF names).
func EncodeUTF16BE(s string) []byte {
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		if r > 0xFFFF {
			r = 0xFFFD
		}
		out = append(out, byte(r>>8), byte(r))
	}
	return out
}

// XMPPacket builds the Factur-X XMP metadata packet: PDF/A-3
// identification, an optional document title, the PDF/A extension schema that
// declares the fx: namespace, and the Factur-X properties.
func XMPPacket(profile formalis.Profile, docType, title string) []byte {
	titleBlock := ""
	if title != "" {
		titleBlock = fmt.Sprintf(`
    <rdf:Description xmlns:dc="http://purl.org/dc/elements/1.1/" rdf:about="">
      <dc:title><rdf:Alt><rdf:li xml:lang="x-default">%s</rdf:li></rdf:Alt></dc:title>
    </rdf:Description>`, pdfa.XMLEscape(title))
	}
	return []byte(fmt.Sprintf(`<?xpacket begin="%s" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" rdf:about="">
      <pdfaid:part>3</pdfaid:part>
      <pdfaid:conformance>B</pdfaid:conformance>
    </rdf:Description>%s
    <rdf:Description xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/" xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#" xmlns:pdfaProperty="http://www.aiim.org/pdfa/ns/property#" rdf:about="">
      <pdfaExtension:schemas>
        <rdf:Bag>
          <rdf:li rdf:parseType="Resource">
            <pdfaSchema:schema>Factur-X PDFA Extension Schema</pdfaSchema:schema>
            <pdfaSchema:namespaceURI>urn:factur-x:pdfa:CrossIndustryDocument:invoice:1p0#</pdfaSchema:namespaceURI>
            <pdfaSchema:prefix>fx</pdfaSchema:prefix>
            <pdfaSchema:property>
              <rdf:Seq>
                <rdf:li rdf:parseType="Resource">
                  <pdfaProperty:name>DocumentFileName</pdfaProperty:name>
                  <pdfaProperty:valueType>Text</pdfaProperty:valueType>
                  <pdfaProperty:category>external</pdfaProperty:category>
                  <pdfaProperty:description>The name of the embedded XML document</pdfaProperty:description>
                </rdf:li>
                <rdf:li rdf:parseType="Resource">
                  <pdfaProperty:name>DocumentType</pdfaProperty:name>
                  <pdfaProperty:valueType>Text</pdfaProperty:valueType>
                  <pdfaProperty:category>external</pdfaProperty:category>
                  <pdfaProperty:description>The type of the hybrid document in capital letters, e.g. INVOICE or ORDER</pdfaProperty:description>
                </rdf:li>
                <rdf:li rdf:parseType="Resource">
                  <pdfaProperty:name>Version</pdfaProperty:name>
                  <pdfaProperty:valueType>Text</pdfaProperty:valueType>
                  <pdfaProperty:category>external</pdfaProperty:category>
                  <pdfaProperty:description>The actual version of the standard applying to the embedded XML document</pdfaProperty:description>
                </rdf:li>
                <rdf:li rdf:parseType="Resource">
                  <pdfaProperty:name>ConformanceLevel</pdfaProperty:name>
                  <pdfaProperty:valueType>Text</pdfaProperty:valueType>
                  <pdfaProperty:category>external</pdfaProperty:category>
                  <pdfaProperty:description>The conformance level of the embedded XML document</pdfaProperty:description>
                </rdf:li>
              </rdf:Seq>
            </pdfaSchema:property>
          </rdf:li>
        </rdf:Bag>
      </pdfaExtension:schemas>
    </rdf:Description>
    <rdf:Description xmlns:fx="urn:factur-x:pdfa:CrossIndustryDocument:invoice:1p0#" rdf:about="">
      <fx:DocumentType>%s</fx:DocumentType>
      <fx:DocumentFileName>%s</fx:DocumentFileName>
      <fx:Version>1.0</fx:Version>
      <fx:ConformanceLevel>%s</fx:ConformanceLevel>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`, "\uFEFF", titleBlock, pdfa.XMLEscape(docType), facturxFileName, string(profile)))
}
