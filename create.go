package pdf0

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/mgilbir/pdf0/object"
)

// Creating a document from nothing, and describing it.
//
// Until this existed the only way to start a document was NewPDFADocument,
// which is the right default for an archival file and the wrong one for
// everything else: it forces an output intent, an embedded ICC profile and a
// pdfaid identification onto a caller who wanted an ordinary PDF. A conformance
// level is a decision, and taking it silently on the caller's behalf is not the
// same as offering it.

// NewDocument creates an empty PDF 2.0 document: a catalog, an empty page tree
// and a file identifier. Pages go in with AddPage.
//
// It is not a PDF/A document and makes no claim to be one. NewPDFADocument is
// for that, and adds what conformance requires.
func NewDocument() *Document {
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Pages", object.IndirectRef{Number: 2})

	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{})
	pages.Set("Count", object.Integer(0))

	id := fileID()
	return &Document{
		Version: "2.0",
		Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Value: catalog},
			2: {Number: 2, Value: pages},
		},
		Trailer: object.Dictionary{
			Keys:   []object.Name{"Root", "ID"},
			Values: []object.Object{object.IndirectRef{Number: 1}, object.Array{id, id}},
		},
	}
}

// fileID makes the two-part file identifier of ISO 32000-2 14.4.
//
// It is random rather than a hash of the moment of creation. The identifier's
// job is to distinguish this file from every other, including one made a
// microsecond later by the same program, and a clock cannot promise that. Since
// Go 1.24 the system random source cannot fail without the runtime ending, so
// there is no error to handle.
func fileID() object.String {
	var b [16]byte
	rand.Read(b[:])
	return object.String{Value: b[:], IsHex: true}
}

// DocumentInfo is what a document says about itself.
//
// The zero value writes nothing: a field left empty is a field the document
// does not claim, which is different from claiming it is empty.
type DocumentInfo struct {
	Title    string
	Author   string
	Subject  string
	Keywords string

	// Creator is the application the content came from — for a converted
	// document, the thing that made the original.
	Creator string
	// Producer is the software that wrote the PDF. Left empty it becomes
	// "pdf0", which is true and is what a reader's document-properties panel
	// expects to find.
	Producer string

	// Created and Modified are written when non-zero. In PDF 2.0 these are the
	// only entries of the information dictionary that are not deprecated.
	Created  time.Time
	Modified time.Time
}

// SetDocumentInfo describes the document, writing both the information
// dictionary and the XMP metadata stream.
//
// Both, because the two are read by different things. ISO 32000-2 14.3.3
// deprecates the information dictionary for everything but the two dates and
// points at XMP instead — but a reader's document-properties panel still shows
// the dictionary, and a file that fills in only one of them displays as blank
// somewhere. Writing both is what a producer does, and PDF/A *requires* them to
// agree, so writing them together from one source is also the only way to be
// sure they do.
//
// An existing XMP packet's PDF/A identification is carried across. Describing a
// PDF/A document must not quietly stop it being one, and the identification is
// the part of the packet that says what it is.
//
// The one exception to writing both is PDF/A-4, which does not merely deprecate
// the information dictionary but restricts it: a conforming file that has one
// must also carry a /PieceInfo in its catalog, and the dictionary may then hold
// nothing but /ModDate (ISO 19005-4 6.1.3). So a document claiming PDF/A-4 is
// described in XMP alone. Nothing is lost by it — every field has an XMP
// property, and the dates are xmp:CreateDate and xmp:ModifyDate — and the
// alternative is a file that says it is PDF/A-4 and is not.
func (d *Document) SetDocumentInfo(info DocumentInfo) error {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return fmt.Errorf("pdf0: the document has no catalog to describe")
	}
	if info.Producer == "" {
		info.Producer = "pdf0"
	}
	identification := d.existingPDFAIdentification()

	if identification.part == "4" {
		d.writeMetadata(catalog, info, identification)
		return nil
	}

	dict := &object.Dictionary{}
	for _, e := range []struct {
		key   object.Name
		value string
	}{
		{"Title", info.Title},
		{"Author", info.Author},
		{"Subject", info.Subject},
		{"Keywords", info.Keywords},
		{"Creator", info.Creator},
		{"Producer", info.Producer},
	} {
		if e.value != "" {
			dict.Set(e.key, object.String{Value: encodePDFText(e.value)})
		}
	}
	if !info.Created.IsZero() {
		dict.Set("CreationDate", object.String{Value: []byte(pdfDate(info.Created))})
	}
	if !info.Modified.IsZero() {
		dict.Set("ModDate", object.String{Value: []byte(pdfDate(info.Modified))})
	}
	d.Trailer.Set("Info", d.Add(dict))
	d.writeMetadata(catalog, info, identification)
	return nil
}

// writeMetadata replaces the document's XMP packet.
func (d *Document) writeMetadata(catalog *object.Dictionary, info DocumentInfo, id pdfaIdentification) {
	packet := buildXMP(info, id)
	stream := &object.Stream{Dict: object.Dictionary{}, Data: packet}
	stream.Dict.Set("Type", object.Name("Metadata"))
	stream.Dict.Set("Subtype", object.Name("XML"))
	// Deliberately unfiltered: a metadata stream is meant to be findable by a
	// tool scanning the bytes without parsing the file (ISO 32000-2 14.3.2).
	stream.Dict.Set("Length", object.Integer(len(packet)))
	catalog.Set("Metadata", d.Add(stream))
}

// pdfDate writes a time in the form of ISO 32000-2 7.9.4: D:YYYYMMDDHHmmSSOHH'mm.
func pdfDate(t time.Time) string {
	base := t.Format("D:20060102150405")
	_, offset := t.Zone()
	if offset == 0 {
		return base + "Z00'00"
	}
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	return fmt.Sprintf("%s%s%02d'%02d", base, sign, offset/3600, (offset%3600)/60)
}

// pdfaIdentification is the part of an XMP packet that says a document claims a
// PDF/A level. It is carried across when the metadata is rewritten.
type pdfaIdentification struct {
	part        string
	conformance string
	rev         string
}

func (p pdfaIdentification) empty() bool { return p.part == "" }

// existingPDFAIdentification reads the pdfaid properties out of the document's
// current metadata, if it has any.
//
// It reads the raw packet rather than going through the XMP parser because the
// question is narrow — three properties whose values are short literals — and
// because this must not fail on a packet the parser would reject. A document
// whose metadata cannot be understood keeps whatever claim it had, or none.
func (d *Document) existingPDFAIdentification() pdfaIdentification {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return pdfaIdentification{}
	}
	stream, ok := d.Resolve(catalog.Get("Metadata")).(*object.Stream)
	if !ok {
		return pdfaIdentification{}
	}
	data, err := d.StreamData(stream)
	if err != nil {
		return pdfaIdentification{}
	}
	text := string(data)
	return pdfaIdentification{
		part:        xmlElementText(text, "pdfaid:part"),
		conformance: xmlElementText(text, "pdfaid:conformance"),
		rev:         xmlElementText(text, "pdfaid:rev"),
	}
}

// xmlElementText pulls the text of the first <tag>…</tag> out of a packet.
//
// The values it is used for are short literals — a part number, a single
// letter, a year — so there is nothing to unescape and nothing nested. Anything
// longer than that is not an identification and is ignored.
func xmlElementText(doc, tag string) string {
	open := "<" + tag + ">"
	i := strings.Index(doc, open)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(open):]
	j := strings.Index(rest, "</"+tag+">")
	if j < 0 || j > 64 {
		return ""
	}
	value := strings.TrimSpace(rest[:j])
	for _, r := range value {
		// A value carrying markup is not one of the literals this reads.
		if r == '<' || r == '&' || r == '>' {
			return ""
		}
	}
	return value
}

// buildXMP writes the metadata packet.
//
// It is assembled as text rather than through an XML encoder because an XMP
// packet is not free-form XML: it opens and closes with processing
// instructions a scanner looks for, and the padding and the exact packet
// wrapper are part of the format. An encoder would produce valid XML that is
// not a valid packet.
func buildXMP(info DocumentInfo, id pdfaIdentification) []byte {
	var b strings.Builder
	// The packet header carries a byte-order mark as its "begin" value, and the
	// identifier is a fixed magic string: both are how a scanner recognises a
	// packet in a file it is not parsing. Written as an escape because Go does
	// not allow a byte-order mark in the middle of a source file.
	b.WriteString("<?xpacket begin=\"\uFEFF\" id=\"W5M0MpCehiHzreSzNTczkc9d\"?>\n")
	b.WriteString(`<x:xmpmeta xmlns:x="adobe:ns:meta/">` + "\n")
	b.WriteString(`  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` + "\n")
	b.WriteString(`    <rdf:Description rdf:about=""` + "\n")
	b.WriteString(`      xmlns:dc="http://purl.org/dc/elements/1.1/"` + "\n")
	b.WriteString(`      xmlns:pdf="http://ns.adobe.com/pdf/1.3/"` + "\n")
	if !id.empty() {
		b.WriteString(`      xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"` + "\n")
	}
	b.WriteString(`      xmlns:xmp="http://ns.adobe.com/xap/1.0/">` + "\n")

	if !id.empty() {
		fmt.Fprintf(&b, "      <pdfaid:part>%s</pdfaid:part>\n", xmlEscape(id.part))
		if id.conformance != "" {
			fmt.Fprintf(&b, "      <pdfaid:conformance>%s</pdfaid:conformance>\n", xmlEscape(id.conformance))
		}
		if id.rev != "" {
			fmt.Fprintf(&b, "      <pdfaid:rev>%s</pdfaid:rev>\n", xmlEscape(id.rev))
		}
	}
	if info.Title != "" {
		// dc:title is a language alternative, not a string: the same document
		// may carry a title in several languages, and x-default is the one to
		// show when none matches.
		fmt.Fprintf(&b, "      <dc:title>\n        <rdf:Alt>\n          <rdf:li xml:lang=\"x-default\">%s</rdf:li>\n        </rdf:Alt>\n      </dc:title>\n", xmlEscape(info.Title))
	}
	if info.Author != "" {
		// dc:creator is an ordered sequence, because a document may have
		// several authors and the order is meaningful.
		fmt.Fprintf(&b, "      <dc:creator>\n        <rdf:Seq>\n          <rdf:li>%s</rdf:li>\n        </rdf:Seq>\n      </dc:creator>\n", xmlEscape(info.Author))
	}
	if info.Subject != "" {
		fmt.Fprintf(&b, "      <dc:description>\n        <rdf:Alt>\n          <rdf:li xml:lang=\"x-default\">%s</rdf:li>\n        </rdf:Alt>\n      </dc:description>\n", xmlEscape(info.Subject))
	}
	if info.Keywords != "" {
		fmt.Fprintf(&b, "      <pdf:Keywords>%s</pdf:Keywords>\n", xmlEscape(info.Keywords))
	}
	if info.Creator != "" {
		fmt.Fprintf(&b, "      <xmp:CreatorTool>%s</xmp:CreatorTool>\n", xmlEscape(info.Creator))
	}
	fmt.Fprintf(&b, "      <pdf:Producer>%s</pdf:Producer>\n", xmlEscape(info.Producer))
	if !info.Created.IsZero() {
		fmt.Fprintf(&b, "      <xmp:CreateDate>%s</xmp:CreateDate>\n", info.Created.Format(time.RFC3339))
	}
	if !info.Modified.IsZero() {
		fmt.Fprintf(&b, "      <xmp:ModifyDate>%s</xmp:ModifyDate>\n", info.Modified.Format(time.RFC3339))
	}

	b.WriteString("    </rdf:Description>\n  </rdf:RDF>\n</x:xmpmeta>\n<?xpacket end=\"w\"?>")
	return []byte(b.String())
}

// xmlEscape makes a string safe to place in element content.
//
// Control characters are dropped rather than escaped: they are illegal in XML
// 1.0 even as character references, so escaping one produces a packet no parser
// will read.
func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}
