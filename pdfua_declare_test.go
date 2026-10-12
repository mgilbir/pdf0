package pdf0

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfua"
)

// Declaring PDF/UA: the document-level parts a structure tree does not supply.

// accessiblePage draws a heading and a paragraph in an embedded font, each
// tagged, and describes them with a structure tree: everything PDF/UA asks of
// the content, and nothing of the document level.
func accessiblePage(t *testing.T, d *Document) {
	t.Helper()
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	var b content.Builder
	b.BeginTagged("H1", 0).BeginText().SetFont("F1", 20).SetRGB(0, 0, 0).MoveText(72, 760)
	face.DrawShaped(&b, "Annual report", 20)
	b.EndText().EndMarked()
	b.BeginTagged("P", 1).BeginText().SetFont("F1", 11).MoveText(72, 730)
	face.DrawShaped(&b, "Revenue grew in every quarter.", 11)
	b.EndText().EndMarked()
	page, err := d.AddPage(Page{
		Width: 595, Height: 842, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face},
	})
	if err != nil {
		t.Fatalf("adding the page: %v", err)
	}
	if err := d.SetStructureTree([]StructElem{{
		Tag: "Document", Page: &page,
		Children: []StructElem{{Tag: "H1", Content: []int{0}}, {Tag: "P", Content: []int{1}}},
	}}, nil); err != nil {
		t.Fatalf("setting the structure tree: %v", err)
	}
}

// writeAndRead round-trips a document through its bytes, which is what a
// validator's byte-level rules and any reader see.
func writtenAndRead(t *testing.T, d *Document) *Document {
	t.Helper()
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	return rd
}

// uaViolations drops checker findings: what is left says the document is wrong.
func uaViolations(vs []pdfua.Violation) []pdfua.Violation {
	var out []pdfua.Violation
	for _, v := range vs {
		if !IsCheckerFinding(v) {
			out = append(out, v)
		}
	}
	return out
}

// describeAndDeclare is the recipe docs/building.md shows.
func describeAndDeclare(t *testing.T, d *Document, part int) {
	t.Helper()
	if err := d.SetDocumentInfo(DocumentInfo{Title: "Annual report", Language: "en-GB"}); err != nil {
		t.Fatalf("SetDocumentInfo: %v", err)
	}
	if err := d.DeclarePDFUA(part); err != nil {
		t.Fatalf("DeclarePDFUA(%d): %v", part, err)
	}
}

// TestDeclaredPDFUA1Conforms: a tagged document, described and declared, passes
// ValidatePDFUA once written — the catalog /Lang, /DisplayDocTitle, dc:title and
// pdfuaid:part are all there. Without the declaration it fails exactly on those.
func TestDeclaredPDFUA1Conforms(t *testing.T) {
	d := NewDocument()
	d.Version = "1.7" // PDF/UA-1 is defined against PDF 1.7
	accessiblePage(t, d)
	describeAndDeclare(t, d, 1)

	rd := writtenAndRead(t, d)
	if v := uaViolations(ValidatePDFUA(rd)); len(v) != 0 {
		t.Errorf("a declared PDF/UA-1 document fails ValidatePDFUA: %v", v)
	}
	cat := rd.ResolveDict(rd.Trailer.Get("Root"))
	if s, _ := rd.Resolve(cat.Get("Lang")).(object.String); string(s.Value) != "en-GB" {
		t.Errorf("catalog /Lang = %v, want (en-GB)", cat.Get("Lang"))
	}
	if vp := rd.ResolveDict(cat.Get("ViewerPreferences")); vp == nil || vp.Get("DisplayDocTitle") != object.Boolean(true) {
		t.Errorf("/ViewerPreferences = %v, want /DisplayDocTitle true", cat.Get("ViewerPreferences"))
	}
	p := packetOf(t, rd)
	if part, _ := p.Text(xmp.NSPDFUAID, "part"); part != "1" {
		t.Errorf("pdfuaid:part = %q, want 1", part)
	}
	if _, has := p.Get(xmp.NSPDFUAID, "rev"); has {
		t.Error("a PDF/UA-1 declaration wrote pdfuaid:rev, which only PDF/UA-2 defines")
	}
}

// TestDeclaredPDFUA2Conforms: the same for PDF/UA-2, which adds pdfuaid:rev,
// requires PDF 2.0 and the catalog /Lang, and puts the tree's Document element
// in the PDF 2.0 structure namespace (ISO 14289-2 8.2.5.2; veraPDF 8.2.5.2-2).
// The declaration may come before or after the tree.
func TestDeclaredPDFUA2Conforms(t *testing.T) {
	for _, order := range []string{"tree-then-declare", "declare-then-tree"} {
		t.Run(order, func(t *testing.T) {
			d := NewDocument() // PDF 2.0
			if order == "tree-then-declare" {
				accessiblePage(t, d)
				describeAndDeclare(t, d, 2)
			} else {
				describeAndDeclare(t, d, 2)
				accessiblePage(t, d)
			}
			rd := writtenAndRead(t, d)
			if v := uaViolations(ValidatePDFUA2(rd)); len(v) != 0 {
				t.Errorf("a declared PDF/UA-2 document fails ValidatePDFUA2: %v", v)
			}
			p := packetOf(t, rd)
			if part, _ := p.Text(xmp.NSPDFUAID, "part"); part != "2" {
				t.Errorf("pdfuaid:part = %q, want 2", part)
			}
			if rev, _ := p.Text(xmp.NSPDFUAID, "rev"); rev != "2024" {
				t.Errorf("pdfuaid:rev = %q, want 2024", rev)
			}
			cat := rd.ResolveDict(rd.Trailer.Get("Root"))
			root := rd.ResolveDict(cat.Get("StructTreeRoot"))
			doc := rd.ResolveDict(root.Get("K"))
			if doc == nil || doc.Get("S") != object.Name("Document") {
				t.Fatalf("the tree root's /K is %v, want the Document element", root.Get("K"))
			}
			ns := rd.ResolveDict(doc.Get("NS"))
			if s, _ := rd.Resolve(ns.Get("NS")).(object.String); string(s.Value) != core.NSPDF20 {
				t.Errorf("Document is in namespace %v, want %s", doc.Get("NS"), core.NSPDF20)
			}
			spaces, _ := rd.Resolve(root.Get("Namespaces")).(object.Array)
			if len(spaces) != 1 {
				t.Errorf("/Namespaces = %v, want the one PDF 2.0 namespace", root.Get("Namespaces"))
			}
		})
	}
}

// TestPDFAAndPDFUATogether: a PDF/A document declared PDF/UA keeps both
// identifications in one packet, and describes pdfuaid in an extension schema,
// which PDF/A requires of a namespace it does not predefine (ISO 19005-2
// 6.6.2.3.1). Save checks the PDF/A level; ValidatePDFUA the rest.
func TestPDFAAndPDFUATogether(t *testing.T) {
	for _, tc := range []struct {
		level pdfa.Level
		part  int
	}{{pdfa.PDFA2b, 1}, {pdfa.PDFA4, 2}} {
		t.Run(tc.level.String(), func(t *testing.T) {
			d := mustPDFADoc(t, tc.level)
			accessiblePage(t, d)
			describeAndDeclare(t, d, tc.part)

			var buf bytes.Buffer
			if err := d.Save(&buf); err != nil {
				t.Fatalf("Save at %s: %v", tc.level, err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatal(err)
			}
			validate := ValidatePDFUA
			if tc.part == 2 {
				validate = ValidatePDFUA2
			}
			if v := uaViolations(validate(rd)); len(v) != 0 {
				t.Errorf("PDF/UA-%d findings: %v", tc.part, v)
			}
			if v := ValidatePDFA(rd, tc.level); len(v) != 0 {
				t.Errorf("%s findings: %v", tc.level, v)
			}
			if lvl, ok := rd.Conformance(); !ok || lvl != tc.level {
				t.Errorf("Conformance() = %v, %v; the PDF/A identification was lost", lvl, ok)
			}
			if got := extensionSchemaCount(t, packetOf(t, rd), xmp.NSPDFUAID); got != 1 {
				t.Errorf("%d extension schemas describe pdfuaid, want 1", got)
			}
		})
	}
}

// TestDeclarePDFUARefuses: every refusal leaves the document as it was.
func TestDeclarePDFUARefuses(t *testing.T) {
	described := func(version string, info DocumentInfo) *Document {
		d := NewDocument()
		d.Version = version
		if err := d.SetDocumentInfo(info); err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, tc := range []struct {
		name string
		doc  func() *Document
		part int
		want string
	}{
		{"no title", func() *Document { return described("1.7", DocumentInfo{Author: "A"}) }, 1, "title"},
		{"no metadata at all", func() *Document { d := NewDocument(); d.Version = "1.7"; return d }, 1, "title"},
		{"part 1 in PDF 2.0", func() *Document { return described("2.0", DocumentInfo{Title: "T"}) }, 1, "PDF 1"},
		{"part 2 below PDF 2.0", func() *Document { return described("1.7", DocumentInfo{Title: "T", Language: "en"}) }, 2, "PDF 2.0"},
		{"part 2 without a language", func() *Document { return described("2.0", DocumentInfo{Title: "T"}) }, 2, "Language"},
		{"part 3", func() *Document { return described("2.0", DocumentInfo{Title: "T", Language: "en"}) }, 3, "part 3"},
		{"part 0", func() *Document { return described("1.7", DocumentInfo{Title: "T"}) }, 0, "part 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.doc()
			before := snapshot(t, d)
			err := d.DeclarePDFUA(tc.part)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("DeclarePDFUA(%d) = %v, want a refusal mentioning %q", tc.part, err, tc.want)
			}
			if after := snapshot(t, d); !bytes.Equal(before, after) {
				t.Error("a refused declaration changed the document")
			}
		})
	}
}

// TestDocumentLanguageIsALanguageTag: Language is held to the grammar the
// validators judge /Lang by (core.ValidLanguageTag), and refused otherwise —
// before anything is written.
func TestDocumentLanguageIsALanguageTag(t *testing.T) {
	for _, bad := range []string{"en_GB", "englishlanguage", "-en", "en-", "e n", "en-GB-x-toolongsubtag", "日本語"} {
		d := NewDocument()
		before := snapshot(t, d)
		if err := d.SetDocumentInfo(DocumentInfo{Title: "T", Language: bad}); err == nil {
			t.Errorf("Language %q was accepted", bad)
		}
		if after := snapshot(t, d); !bytes.Equal(before, after) {
			t.Errorf("a refused Language %q changed the document", bad)
		}
	}
	for _, good := range []string{"en", "en-GB", "zh-Hant-TW", "de-CH-1996", "x-klingon", "i"} {
		d := NewDocument()
		if err := d.SetDocumentInfo(DocumentInfo{Language: good}); err != nil {
			t.Errorf("Language %q was refused: %v", good, err)
			continue
		}
		cat := d.ResolveDict(d.Trailer.Get("Root"))
		if s, _ := cat.Get("Lang").(object.String); string(s.Value) != good {
			t.Errorf("Language %q wrote /Lang %v", good, cat.Get("Lang"))
		}
	}

	// PDF/A-1 is written against RFC 1766, where a subtag is letters only
	// (core.ValidLanguageTag); the later parts allow digits.
	d := mustPDFADoc(t, pdfa.PDFA1b)
	if err := d.SetDocumentInfo(DocumentInfo{Language: "de-CH-1996"}); err == nil {
		t.Error("a subtag with digits was accepted in a PDF/A-1 document")
	}
	d = mustPDFADoc(t, pdfa.PDFA2b)
	if err := d.SetDocumentInfo(DocumentInfo{Language: "de-CH-1996"}); err != nil {
		t.Errorf("a subtag with digits was refused in a PDF/A-2 document: %v", err)
	}
}

// TestDeclaringTwiceAndDescribingAfter: the declaration is idempotent, replaces
// what an earlier one said, and survives a later SetDocumentInfo, which edits
// the packet rather than replacing it.
func TestDeclaringTwiceAndDescribingAfter(t *testing.T) {
	d := NewDocument()
	accessiblePage(t, d)
	describeAndDeclare(t, d, 2)
	once := snapshot(t, d)
	if err := d.DeclarePDFUA(2); err != nil {
		t.Fatal(err)
	}
	if twice := snapshot(t, d); !bytes.Equal(once, twice) {
		t.Error("declaring the same part twice changed the document")
	}
	p := packetOf(t, d)
	if n := len(p.Lookup(xmp.NSPDFUAID, "part")); n != 1 {
		t.Errorf("%d pdfuaid:part properties, want 1", n)
	}
	if got := extensionSchemaCount(t, p, xmp.NSPDFUAID); got != 1 {
		t.Errorf("%d extension schemas describe pdfuaid, want 1", got)
	}

	if err := d.SetDocumentInfo(DocumentInfo{Title: "Annual report 2026", Author: "Finance"}); err != nil {
		t.Fatal(err)
	}
	p = packetOf(t, d)
	if part, _ := p.Text(xmp.NSPDFUAID, "part"); part != "2" {
		t.Errorf("after SetDocumentInfo pdfuaid:part = %q, want 2", part)
	}
	if v := uaViolations(ValidatePDFUA2(writtenAndRead(t, d))); len(v) != 0 {
		t.Errorf("described again, the document fails ValidatePDFUA2: %v", v)
	}

	// A part 1 declaration over metadata that carries a pdfuaid:rev — a file
	// once declared part 2 — removes it: only part 2 has a revision.
	d17 := NewDocument()
	d17.Version = "1.7"
	accessiblePage(t, d17)
	describeAndDeclare(t, d17, 1)
	cat := d17.ResolveDict(d17.Trailer.Get("Root"))
	stale, err := d17.editableMetadata(cat)
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.SetText(xmp.NSPDFUAID, "pdfuaid", "rev", "2024"); err != nil {
		t.Fatal(err)
	}
	data, err := stale.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d17.setMetadataStream(cat, data)
	if err := d17.DeclarePDFUA(1); err != nil {
		t.Fatal(err)
	}
	if _, has := packetOf(t, d17).Get(xmp.NSPDFUAID, "rev"); has {
		t.Error("PDF/UA-1 carries pdfuaid:rev")
	}
}

// TestDeclarePDFUARefusesALockedDocument: like every metadata writer, it would
// write plaintext under the document's /Encrypt.
func TestDeclarePDFUARefusesALockedDocument(t *testing.T) {
	checkRefused(t, "DeclarePDFUA", lockedPDFA3(t), func(d *Document) error { return d.DeclarePDFUA(1) })
	var nilDoc *Document
	if err := nilDoc.DeclarePDFUA(1); !errors.Is(err, errNilDocument) {
		t.Errorf("DeclarePDFUA on nil = %v, want errNilDocument", err)
	}
}

// packetOf parses the document's XMP metadata through the model.
func packetOf(t *testing.T, d *Document) *xmp.Packet {
	t.Helper()
	p, status := d.view().DocumentXMPPacket()
	if status != core.XMPParsed {
		t.Fatalf("the document's metadata is not readable (status %v)", status)
	}
	return p
}

// extensionSchemaCount counts the pdfaExtension:schemas entries for ns.
func extensionSchemaCount(t *testing.T, p *xmp.Packet, ns string) int {
	t.Helper()
	pr, ok := p.Get(xmp.NSPDFAExtension, "schemas")
	if !ok {
		return 0
	}
	n := 0
	for _, item := range pr.Value.Items {
		for _, f := range item.Fields {
			if f.NS == xmp.NSPDFASchema && f.Name == "namespaceURI" && strings.TrimSpace(f.Value.Text) == ns {
				n++
			}
		}
	}
	return n
}

// snapshot is the document as bytes, to tell whether a call changed it.
func snapshot(t *testing.T, d *Document) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	return buf.Bytes()
}
