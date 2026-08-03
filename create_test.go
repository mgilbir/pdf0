package pdf0

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Creating a plain document, and describing one.

// TestNewDocumentWritesAndReparses is the whole claim: what NewDocument builds
// is a file, not a structure that happens to have the right keys.
func TestNewDocumentWritesAndReparses(t *testing.T) {
	doc := NewDocument()
	var b content.Builder
	b.Save().SetRGB(0, 0, 0).Rect(10, 10, 100, 50).Fill().Restore()
	if _, err := doc.AddPage(Page{Width: 200, Height: 100, Content: &b}); err != nil {
		t.Fatalf("adding a page: %v", err)
	}

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := rd.PageCount(); got != 1 {
		t.Errorf("the reparsed document has %d pages, want 1", got)
	}
	if !strings.HasPrefix(buf.String(), "%PDF-2.0") {
		t.Errorf("the file starts %q, want a PDF 2.0 header", buf.String()[:8])
	}
}

// TestNewDocumentIsNotAPDFA pins that a plain document does not pretend to be an
// archival one. Claiming conformance a file does not have is worse than not
// claiming it: a checker downstream believes the claim.
func TestNewDocumentIsNotAPDFA(t *testing.T) {
	doc := NewDocument()
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	if catalog.Get("OutputIntents") != nil {
		t.Error("a plain document was given an output intent")
	}
	if catalog.Get("Metadata") != nil {
		t.Error("a plain document was given a metadata stream it did not ask for")
	}
}

// TestEveryDocumentGetsItsOwnFileIdentifier pins that the identifier
// distinguishes files. Deriving it from the moment of creation cannot: two
// documents made in the same microsecond would share one, and the identifier's
// only job is to tell files apart.
func TestEveryDocumentGetsItsOwnFileIdentifier(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, ok := NewDocument().Trailer.Get("ID").(object.Array)
		if !ok || len(id) != 2 {
			t.Fatal("the trailer has no two-part file identifier")
		}
		first, ok := id[0].(object.String)
		if !ok || len(first.Value) < 16 {
			t.Fatalf("the identifier is %v, want at least 16 bytes", id[0])
		}
		key := string(first.Value)
		if seen[key] {
			t.Fatal("two documents were given the same file identifier")
		}
		seen[key] = true
	}
}

// TestDocumentInfoReachesBothPlaces is the point of writing it at all. A reader's
// properties panel shows the information dictionary; a metadata tool reads the
// XMP. A file that fills in only one displays as blank in the other.
func TestDocumentInfoReachesBothPlaces(t *testing.T) {
	doc := NewDocument()
	created := time.Date(2026, 8, 3, 14, 30, 0, 0, time.UTC)
	err := doc.SetDocumentInfo(DocumentInfo{
		Title:    "A Title",
		Author:   "An Author",
		Subject:  "A Subject",
		Keywords: "one two",
		Creator:  "A Creator",
		Created:  created,
		Modified: created,
	})
	if err != nil {
		t.Fatalf("describing: %v", err)
	}

	info := doc.ResolveDict(doc.Trailer.Get("Info"))
	if info == nil {
		t.Fatal("no information dictionary was written")
	}
	for key, want := range map[object.Name]string{
		"Title":    "A Title",
		"Author":   "An Author",
		"Subject":  "A Subject",
		"Keywords": "one two",
		"Creator":  "A Creator",
		"Producer": "pdf0",
	} {
		s, ok := info.Get(key).(object.String)
		if !ok || string(s.Value) != want {
			t.Errorf("/%s = %v, want %q", key, info.Get(key), want)
		}
	}
	if s, _ := info.Get("CreationDate").(object.String); string(s.Value) != "D:20260803143000Z00'00" {
		t.Errorf("/CreationDate = %q, want a PDF date string", s.Value)
	}

	packet := metadataPacket(t, doc)
	for _, want := range []string{
		"<rdf:li xml:lang=\"x-default\">A Title</rdf:li>",
		"<rdf:li>An Author</rdf:li>",
		"<pdf:Keywords>one two</pdf:Keywords>",
		"<xmp:CreatorTool>A Creator</xmp:CreatorTool>",
		"<pdf:Producer>pdf0</pdf:Producer>",
		"<xmp:CreateDate>2026-08-03T14:30:00Z</xmp:CreateDate>",
	} {
		if !strings.Contains(packet, want) {
			t.Errorf("the XMP packet does not contain %s", want)
		}
	}
}

// TestUnsetFieldsAreNotWritten pins that an empty field is a field the document
// does not claim. Writing an empty title is a claim that the title is empty.
func TestUnsetFieldsAreNotWritten(t *testing.T) {
	doc := NewDocument()
	if err := doc.SetDocumentInfo(DocumentInfo{Title: "Only This"}); err != nil {
		t.Fatalf("describing: %v", err)
	}
	info := doc.ResolveDict(doc.Trailer.Get("Info"))
	for _, key := range []object.Name{"Author", "Subject", "Keywords", "Creator", "CreationDate", "ModDate"} {
		if info.Get(key) != nil {
			t.Errorf("/%s was written for a field that was never set", key)
		}
	}
	packet := metadataPacket(t, doc)
	for _, absent := range []string{"dc:creator", "dc:description", "pdf:Keywords", "xmp:CreateDate"} {
		if strings.Contains(packet, absent) {
			t.Errorf("the XMP packet contains %s for a field that was never set", absent)
		}
	}
}

// TestDescribingAPDFADocumentKeepsItOne is the case that makes this more than a
// string-formatting exercise.
//
// Rewriting the metadata of a PDF/A document throws away the packet that said it
// was one. Carrying the identification across is what keeps the file conformant
// — and the validator, not a substring check, is what says whether it worked.
func TestDescribingAPDFADocumentKeepsItOne(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)
			if err := doc.SetDocumentInfo(DocumentInfo{Title: "Described", Author: "An Author"}); err != nil {
				t.Fatalf("describing: %v", err)
			}
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("write: %v", err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if v := ValidatePDFABytes(rd, level, buf.Bytes()); len(v) != 0 {
				t.Errorf("describing the document stopped it being %s: %v", level, v)
			}
		})
	}
}

// TestPDFA4IsDescribedInMetadataAlone pins the exception, and why it exists.
//
// PDF/A-4 does not merely deprecate the information dictionary, it restricts it:
// a file that has one must carry a /PieceInfo, and the dictionary may then hold
// nothing but /ModDate. Writing the usual pair would produce a file that claims
// PDF/A-4 and is not one — which the validator says, and which is how this was
// found.
func TestPDFA4IsDescribedInMetadataAlone(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA4)
	if err := doc.SetDocumentInfo(DocumentInfo{Title: "Described", Author: "An Author"}); err != nil {
		t.Fatalf("describing: %v", err)
	}
	if doc.Trailer.Get("Info") != nil {
		t.Error("a PDF/A-4 document was given an information dictionary, which the level restricts")
	}
	// Nothing is lost: every field has an XMP property.
	packet := metadataPacket(t, doc)
	for _, want := range []string{"Described", "An Author", "<pdfaid:part>4</pdfaid:part>"} {
		if !strings.Contains(packet, want) {
			t.Errorf("the XMP packet does not carry %q", want)
		}
	}
}

// TestPDFAIdentificationIsCarriedNotInvented is the other half. The claim must
// survive a rewrite, and must not appear on a document that never made it.
func TestPDFAIdentificationIsCarriedNotInvented(t *testing.T) {
	described := NewPDFADocument(pdfa.PDFA2b)
	if err := described.SetDocumentInfo(DocumentInfo{Title: "x"}); err != nil {
		t.Fatalf("describing: %v", err)
	}
	if packet := metadataPacket(t, described); !strings.Contains(packet, "<pdfaid:part>2</pdfaid:part>") {
		t.Error("the PDF/A identification was lost when the metadata was rewritten")
	}

	plain := NewDocument()
	if err := plain.SetDocumentInfo(DocumentInfo{Title: "x"}); err != nil {
		t.Fatalf("describing: %v", err)
	}
	if packet := metadataPacket(t, plain); strings.Contains(packet, "pdfaid") {
		t.Error("a plain document was given a PDF/A identification it never claimed")
	}
}

// TestDescriptionsAreEscaped is the injection case. A title is caller-supplied
// text that lands inside XML, and a title containing a closing tag must not be
// able to end an element or add one.
func TestDescriptionsAreEscaped(t *testing.T) {
	doc := NewDocument()
	const nasty = `</rdf:li></rdf:Alt></dc:title><pdfaid:part>4</pdfaid:part><dc:title><rdf:Alt><rdf:li>`
	if err := doc.SetDocumentInfo(DocumentInfo{Title: nasty, Author: "a & b < c"}); err != nil {
		t.Fatalf("describing: %v", err)
	}
	packet := metadataPacket(t, doc)
	if strings.Contains(packet, "<pdfaid:part>4</pdfaid:part>") {
		t.Fatal("a title was able to inject a PDF/A conformance claim into the metadata")
	}
	if strings.Contains(packet, "a & b < c") {
		t.Error("an ampersand and a less-than reached the packet unescaped")
	}
	if !strings.Contains(packet, "a &amp; b &lt; c") {
		t.Error("the author was not escaped")
	}
	// And the packet is still well-formed XML after all that.
	assertWellFormedXML(t, packet)
}

// TestControlCharactersAreDroppedNotEscaped pins the one case where escaping is
// not enough. A control character is illegal in XML 1.0 even as a character
// reference, so a packet that escapes one is a packet no parser will read.
func TestControlCharactersAreDroppedNotEscaped(t *testing.T) {
	doc := NewDocument()
	if err := doc.SetDocumentInfo(DocumentInfo{Title: "a\x01b\x1fc"}); err != nil {
		t.Fatalf("describing: %v", err)
	}
	packet := metadataPacket(t, doc)
	if !strings.Contains(packet, ">abc<") {
		t.Errorf("control characters were not dropped from the title: %s", excerpt(packet, "dc:title"))
	}
	assertWellFormedXML(t, packet)
}

// TestPDFDateOffsets pins the timezone form, which is the part of a PDF date
// most often written wrongly.
func TestPDFDateOffsets(t *testing.T) {
	base := time.Date(2026, 8, 3, 14, 30, 0, 0, time.UTC)
	cases := []struct {
		zone *time.Location
		want string
	}{
		{time.UTC, "D:20260803143000Z00'00"},
		{time.FixedZone("", 2*3600), "D:20260803143000+02'00"},
		{time.FixedZone("", -5*3600-30*60), "D:20260803143000-05'30"},
	}
	for _, tc := range cases {
		got := pdfDate(base.In(tc.zone))
		// Compare the offset only; the wall time moves with the zone.
		if got[len(got)-6:] != tc.want[len(tc.want)-6:] {
			t.Errorf("zone offset of %q, want that of %q", got, tc.want)
		}
	}
}

// assertWellFormedXML parses the packet the way a metadata reader would. A
// substring check can say an escape happened; only a parser can say the result
// is still a document.
func assertWellFormedXML(t *testing.T, packet string) {
	t.Helper()
	// The packet is wrapped in processing instructions and a byte-order mark,
	// which are part of the packet format and which a plain XML decoder handles
	// as a directive — so the whole thing can go in as it stands.
	dec := xml.NewDecoder(strings.NewReader(packet))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("the metadata packet is not well-formed XML: %v\n%s", err, packet)
		}
	}
}

// metadataPacket reads the document's XMP stream back as text.
func metadataPacket(t *testing.T, d *Document) string {
	t.Helper()
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	stream, ok := d.Resolve(catalog.Get("Metadata")).(*object.Stream)
	if !ok {
		t.Fatal("the catalog names no metadata stream")
	}
	data, err := d.StreamData(stream)
	if err != nil {
		t.Fatalf("reading the metadata: %v", err)
	}
	return string(data)
}

func excerpt(s, around string) string {
	i := strings.Index(s, around)
	if i < 0 {
		return s
	}
	end := min(i+120, len(s))
	return s[i:end]
}
