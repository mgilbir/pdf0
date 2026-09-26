package pdf0

import (
	"fmt"
	"time"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/xmp"
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

	id := core.RandomFileID()
	return &Document{
		Version: "2.0",
		Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Value: catalog},
			2: {Number: 2, Value: pages},
		},
		Trailer: *object.NewDictionary(
			object.Entry{Key: "Root", Value: object.IndirectRef{Number: 1}},
			object.Entry{Key: "ID", Value: object.Array{id, id}},
		),
	}
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
// Both are edited, not replaced. The fields info sets are set; everything else
// either one already holds is kept — another writer's entries in the
// information dictionary (a PDF/X-1a /GTS_PDFXVersion, a /Trapped), and in the
// XMP packet every property, namespace, extension schema and comment it had:
// the PDF/A, PDF/UA, PDF/X and PDF/VT identification, the Factur-X
// properties. Describing a document must not quietly stop it being what it
// says it is. A field left empty in info is left as the document has it.
//
// The one exception to writing both is PDF/A-4, which does not merely deprecate
// the information dictionary but restricts it: a conforming file that has one
// must also carry a /PieceInfo in its catalog, and the dictionary may then hold
// nothing but /ModDate (ISO 19005-4 6.1.3). So a document claiming PDF/A-4 is
// described in XMP alone. Nothing is lost by it — every field has an XMP
// property, and the dates are xmp:CreateDate and xmp:ModifyDate — and the
// alternative is a file that says it is PDF/A-4 and is not.
//
// A nil document, and a Locked one — encrypted and not decrypted, whose
// content Write passes through as ciphertext — are refused: the Info
// dictionary and the packet would be written in the clear under its /Encrypt.
//
// It returns an error, and changes nothing, when a field cannot be written as
// XMP text (invalid UTF-8, or a character XML does not allow; the error wraps
// xmp.ErrInvalidText), and when the document's existing metadata cannot be
// edited: a packet that is not well-formed XML, has no rdf:RDF, or is larger
// than the XMP packet limit. Replacing such a packet would destroy whatever it
// says, so the caller decides — removing the catalog's /Metadata first is how
// to ask for a fresh one.
func (d *Document) SetDocumentInfo(info DocumentInfo) error {
	if d == nil {
		return errNilDocument
	}
	if d.Locked() {
		return errLockedTarget("describing the document")
	}
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return fmt.Errorf("pdf0: the document has no catalog to describe")
	}
	if info.Producer == "" {
		info.Producer = "pdf0"
	}
	for _, f := range []struct{ name, value string }{
		{"Title", info.Title}, {"Author", info.Author}, {"Subject", info.Subject},
		{"Keywords", info.Keywords}, {"Creator", info.Creator}, {"Producer", info.Producer},
	} {
		if err := xmp.CheckText(f.value); err != nil {
			return fmt.Errorf("pdf0: document info %s: %w", f.name, err)
		}
	}

	packet, err := d.editableMetadata(catalog)
	if err != nil {
		return err
	}
	if err := describeInXMP(packet, info); err != nil {
		return err
	}
	data, err := packet.Bytes()
	if err != nil {
		return err
	}

	// Everything that can fail has; from here the document changes.
	if part, _ := packet.Text(xmp.NSPDFAID, "part"); part != "4" {
		d.setInfoDictionary(info)
	}
	d.setMetadataStream(catalog, data)
	return nil
}

// setInfoDictionary sets info's fields in the information dictionary, keeping
// every entry it does not set.
func (d *Document) setInfoDictionary(info DocumentInfo) {
	dict := &object.Dictionary{}
	if existing := d.ResolveDict(d.Trailer.Get("Info")); existing != nil {
		dict = existing.Clone()
	}
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
	// The same object when there is one, so nothing that points at it is
	// left pointing at a stale copy.
	if n := object.RefNum(d.Trailer.Get("Info")); n != 0 && d.Objects[n] != nil {
		d.Objects[n].Value = dict
		return
	}
	d.Trailer.Set("Info", d.Add(dict))
}

// describeInXMP sets info's fields as XMP properties.
func describeInXMP(p *xmp.Packet, info DocumentInfo) error {
	set := func(err error) error {
		if err != nil {
			return fmt.Errorf("pdf0: document metadata: %w", err)
		}
		return nil
	}
	if info.Title != "" {
		// dc:title is a language alternative, not a string: the same document
		// may carry a title in several languages, and x-default is the one to
		// show when none matches. The others are kept.
		if err := set(p.SetAltText(xmp.NSDC, "dc", "title", "x-default", info.Title)); err != nil {
			return err
		}
	}
	if info.Author != "" {
		// dc:creator is an ordered sequence; the information dictionary has one
		// author, and PDF/A-1 requires the sequence to hold exactly that one
		// when the dictionary names it.
		if err := set(p.SetSeq(xmp.NSDC, "dc", "creator", []string{info.Author})); err != nil {
			return err
		}
	}
	if info.Subject != "" {
		if err := set(p.SetAltText(xmp.NSDC, "dc", "description", "x-default", info.Subject)); err != nil {
			return err
		}
	}
	for _, e := range []struct{ ns, prefix, name, value string }{
		{xmp.NSPDF, "pdf", "Keywords", info.Keywords},
		{xmp.NSXMP, "xmp", "CreatorTool", info.Creator},
		{xmp.NSPDF, "pdf", "Producer", info.Producer},
	} {
		if e.value == "" {
			continue
		}
		if err := set(p.SetText(e.ns, e.prefix, e.name, e.value)); err != nil {
			return err
		}
	}
	if !info.Created.IsZero() {
		if err := set(p.SetText(xmp.NSXMP, "xmp", "CreateDate", info.Created.Format(time.RFC3339))); err != nil {
			return err
		}
	}
	if !info.Modified.IsZero() {
		if err := set(p.SetText(xmp.NSXMP, "xmp", "ModifyDate", info.Modified.Format(time.RFC3339))); err != nil {
			return err
		}
	}
	return nil
}

// editableMetadata returns the document's XMP packet parsed for editing, or a
// new empty packet when the document has none (core.EditableXMP).
func (d *Document) editableMetadata(catalog *object.Dictionary) (*xmp.Packet, error) {
	stream, _ := d.Resolve(catalog.Get("Metadata")).(*object.Stream)
	p, err := core.EditableXMP(d.canceler(), stream, d.lim())
	if err != nil {
		return nil, fmt.Errorf("pdf0: %w", err)
	}
	return p, nil
}

// setMetadataStream stores an XMP packet as the catalog's /Metadata stream,
// in the existing stream object when there is one.
func (d *Document) setMetadataStream(catalog *object.Dictionary, packet []byte) {
	stream := core.MetadataStream(packet)
	if n := object.RefNum(catalog.Get("Metadata")); n != 0 && d.Objects[n] != nil {
		if _, ok := d.Objects[n].Value.(*object.Stream); ok {
			d.Objects[n].Value = stream
			return
		}
	}
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
// PDF/A level, read through the XMP model like every other reader of it.
type pdfaIdentification struct {
	part, conformance string
	// status says whether the packet was read at all. A packet that is not
	// well-formed, or over the XMP packet limit, claims something pdf0 cannot
	// see — which is not the same as claiming nothing.
	status core.XMPStatus
}

// existingPDFAIdentification reads the pdfaid properties out of the document's
// current metadata, if it has any.
func (d *Document) existingPDFAIdentification() pdfaIdentification {
	packet, status := d.view().DocumentXMPPacket()
	id := pdfaIdentification{status: status}
	if status == core.XMPParsed {
		id.part, _ = packet.Text(xmp.NSPDFAID, "part")
		id.conformance, _ = packet.Text(xmp.NSPDFAID, "conformance")
	}
	return id
}
