package pdf0

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Saving against the conformance a document claims.

// aRedSquare draws something conforming at every level.
func aRedSquare() *content.Builder {
	var b content.Builder
	b.Save().SetRGB(1, 0, 0).Rect(10, 10, 100, 100).Fill().Restore()
	return &b
}

// TestConformanceComesFromTheDocumentsOwnClaim pins where the level is read
// from. Not from an argument — the document already carries its claim in its
// metadata, and checking that one is what makes the thing enforced and the
// thing a reader believes the same thing.
func TestConformanceComesFromTheDocumentsOwnClaim(t *testing.T) {
	if _, ok := NewDocument().Conformance(); ok {
		t.Error("a plain document claims a conformance level")
	}
	for _, level := range []pdfa.Level{
		pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4,
		pdfa.PDFA1a, pdfa.PDFA2a, pdfa.PDFA3a,
	} {
		got, ok := NewPDFADocument(level).Conformance()
		if !ok {
			t.Errorf("a %s document claims nothing", level)
			continue
		}
		if got != level {
			t.Errorf("a %s document reports %s", level, got)
		}
	}
}

// TestConformanceSurvivesAWriteAndRead pins that the claim is in the file, not
// in a Go field. A document read back from bytes has to report the same thing,
// or read-modify-write silently loses the guarantee.
func TestConformanceSurvivesAWriteAndRead(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	got, ok := rd.Conformance()
	if !ok || got != pdfa.PDFA2b {
		t.Errorf("the reparsed document reports (%v, %v), want PDF/A-2b", got, ok)
	}
}

// TestSaveRefusesADocumentThatContradictsItsClaim is the whole point.
//
// Before this existed, a caller asked for PDF/A-1b, drew something that level
// forbids, and got a file whose metadata says PDF/A-1b and whose content does
// not — with nothing anywhere saying so. The claim and the content had drifted
// apart with no step in between to notice.
func TestSaveRefusesADocumentThatContradictsItsClaim(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA1b)
	// Transparency, which PDF/A-1 forbids outright.
	gs, err := Opacity(0.5, 1)
	if err != nil {
		t.Fatalf("building the graphics state: %v", err)
	}
	var b content.Builder
	b.Save().SetExtGState("GS0").SetRGB(1, 0, 0).Rect(10, 10, 100, 100).Fill().Restore()
	if _, err := doc.AddPage(Page{
		Width: 200, Height: 200, Content: &b,
		ExtGStates: map[object.Name]object.Object{"GS0": gs},
	}); err != nil {
		t.Fatalf("adding the page: %v", err)
	}

	var buf bytes.Buffer
	err = doc.Save(&buf)
	if err == nil {
		t.Fatal("a document that contradicts its own conformance claim was written")
	}
	// Nothing may reach the writer: a failed Save leaves no partial file.
	if buf.Len() != 0 {
		t.Errorf("%d bytes were written despite the failure", buf.Len())
	}

	var ce *ConformanceError
	if !errors.As(err, &ce) {
		t.Fatalf("the error is %T, want a *ConformanceError a caller can act on", err)
	}
	if ce.Level != pdfa.PDFA1b {
		t.Errorf("the error names %s, want PDF/A-1b", ce.Level)
	}
	if len(ce.Violations) == 0 {
		t.Error("the error carries no violations")
	}
	if !strings.Contains(err.Error(), "PDF/A-1b") {
		t.Errorf("the message does not name the level: %q", err)
	}

	// And Write is still the escape hatch: it writes exactly what is there.
	var raw bytes.Buffer
	if err := doc.Write(&raw); err != nil {
		t.Errorf("Write refused a document Save rejected: %v", err)
	}
	if raw.Len() == 0 {
		t.Error("Write produced nothing")
	}
}

// TestSaveWritesAConformingDocument is the other half: the check must not be a
// refusal to write anything.
func TestSaveWritesAConformingDocument(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)
			if _, err := doc.AddPage(Page{Width: 200, Height: 200, Content: aRedSquare()}); err != nil {
				t.Fatalf("adding the page: %v", err)
			}
			var buf bytes.Buffer
			if err := doc.Save(&buf); err != nil {
				t.Fatalf("Save: %v", err)
			}
			if buf.Len() == 0 {
				t.Fatal("Save wrote nothing")
			}
			// What it wrote is what it promised.
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if v := ValidatePDFABytes(rd, level, buf.Bytes()); len(v) != 0 {
				t.Errorf("Save wrote a non-conforming file: %v", v)
			}
		})
	}
}

// TestSaveOnAPlainDocumentJustWrites pins that a document claiming nothing is
// not held to a standard it never claimed.
func TestSaveOnAPlainDocumentJustWrites(t *testing.T) {
	doc := NewDocument()
	if _, err := doc.AddPage(Page{Width: 200, Height: 200, Content: aRedSquare()}); err != nil {
		t.Fatalf("adding the page: %v", err)
	}
	var saved, written bytes.Buffer
	if err := doc.Save(&saved); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := doc.Write(&written); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if saved.Len() == 0 {
		t.Fatal("Save wrote nothing")
	}
	// The two differ only in the file identifier, which is random per document
	// — so compare lengths rather than bytes.
	if saved.Len() != written.Len() {
		t.Errorf("Save wrote %d bytes and Write %d; a document claiming nothing should take the same path",
			saved.Len(), written.Len())
	}
}

// TestSaveRefusesAClaimItCannotCheck pins the honest failure. A document
// asserting a part of ISO 19005 this package does not implement must not be
// written with that assertion standing unchecked — Save's promise is that it
// verified the claim, and it cannot make that promise here.
func TestSaveRefusesAClaimItCannotCheck(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	stream, ok := doc.Resolve(catalog.Get("Metadata")).(*object.Stream)
	if !ok {
		t.Fatal("no metadata stream")
	}
	stream.Data = bytes.ReplaceAll(stream.Data,
		[]byte("<pdfaid:part>2</pdfaid:part>"),
		[]byte("<pdfaid:part>9</pdfaid:part>"))
	stream.Dict.Set("Length", object.Integer(len(stream.Data)))

	var buf bytes.Buffer
	err := doc.Save(&buf)
	if err == nil {
		t.Fatal("a claim to an unknown PDF/A part was written unchecked")
	}
	if !strings.Contains(err.Error(), "9") {
		t.Errorf("the error does not name the part it could not check: %q", err)
	}
	if buf.Len() != 0 {
		t.Errorf("%d bytes were written", buf.Len())
	}
}

// TestLevelForIsTheInverseOfWhatIsWritten pins the two directions against each
// other. Skeleton writes a part and a conformance; LevelFor reads them back. If
// they ever disagree, a document is checked against a level other than the one
// it claims — which is worse than not checking, because it looks checked.
func TestLevelForIsTheInverseOfWhatIsWritten(t *testing.T) {
	for _, level := range []pdfa.Level{
		pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4,
		pdfa.PDFA1a, pdfa.PDFA2a, pdfa.PDFA3a,
	} {
		doc := NewPDFADocument(level)
		got, ok := doc.Conformance()
		if !ok || got != level {
			t.Errorf("%s round-trips through its metadata as (%v, %v)", level, got, ok)
		}
	}
	if _, ok := pdfa.LevelFor("9", ""); ok {
		t.Error("an unknown part was mapped to a level")
	}
	if _, ok := pdfa.LevelFor("", ""); ok {
		t.Error("an absent part was mapped to a level")
	}
	// Conformance U is checked as the B level it extends, which is what this
	// package implements of it.
	if got, ok := pdfa.LevelFor("2", "U"); !ok || got != pdfa.PDFA2b {
		t.Errorf("PDF/A-2u mapped to (%v, %v), want PDF/A-2b", got, ok)
	}
}

// TestValidatingNothingIsAFindingNotAPanic pins the shape of a caller mistake
// that the natural shape of the code invites: Read fails, doc is nil, the error
// goes unchecked, and the next line validates.
func TestValidatingNothingIsAFindingNotAPanic(t *testing.T) {
	v := ValidatePDFABytes(nil, pdfa.PDFA2b, nil)
	if len(v) == 0 {
		t.Fatal("validating a nil document reported nothing, which reads as a clean bill of health")
	}
	if !IsCheckerFinding(v[0]) {
		t.Errorf("the finding is %v; it must be a checker finding so it cannot pass for conformance", v[0])
	}
}
