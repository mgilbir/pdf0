package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/fonttest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// TestAddPageProducesAConformingPage is the whole point: drawing, embedding and
// one call produce a document that validates, with no page dictionary assembled
// by hand.
func TestAddPageProducesAConformingPage(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)

	face, err := fonts.Load(fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Page-Regular",
		Glyphs: []fonttest.Glyph{
			{Rune: 'H', Advance: 722, HasShape: true},
			{Rune: 'i', Advance: 250, HasShape: true},
		},
	}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	codes, _ := face.Encode("Hi")

	var b content.Builder
	b.Save().SetRGB(0.1, 0.2, 0.9).Rect(72, 72, 200, 100).Fill().Restore()
	b.BeginText().SetFont("F1", 24).MoveText(72, 700).ShowText(codes).EndText()

	fontRef, err := face.Embed(doc)
	if err != nil {
		t.Fatalf("embedding: %v", err)
	}
	if _, err := doc.AddPage(Page{
		Width: 612, Height: 792,
		Content: &b,
		Fonts:   map[object.Name]object.Object{"F1": fontRef},
	}); err != nil {
		t.Fatalf("AddPage: %v", err)
	}
	if got := doc.PageCount(); got != 1 {
		t.Errorf("PageCount = %d, want 1", got)
	}

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	for _, e := range ValidatePDFABytes(rd, pdfa.PDFA2b, buf.Bytes()) {
		t.Errorf("violation: %s", e.Error())
	}
	if got := rd.ExtractText(); !strings.Contains(got, "Hi") {
		t.Errorf("extracted %q, want it to contain %q", got, "Hi")
	}
}

// TestAddPageRefusesAnUndefinedResource pins the check the resource bookkeeping
// was built for. A content stream naming a font that /Resources does not define
// is a broken page: a reader draws nothing where the text should be, and
// nothing in the file says why. It is caught here, where the cause is still to
// hand.
func TestAddPageRefusesAnUndefinedResource(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	var b content.Builder
	b.BeginText().SetFont("F1", 12).ShowText([]byte{0, 1}).EndText()

	_, err := doc.AddPage(Page{Width: 612, Height: 792, Content: &b})
	if err == nil {
		t.Fatal("a page using an undefined font was accepted")
	}
	if !strings.Contains(err.Error(), "F1") || !strings.Contains(err.Error(), "Font") {
		t.Errorf("error %q does not name the missing resource and its group", err)
	}
}

// TestAddPageSurfacesDrawingErrors pins that a builder left in a broken state
// cannot become a page. Drawing calls do not return errors so that a page can
// be written as a sequence; this is where that debt is settled.
func TestAddPageSurfacesDrawingErrors(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	var b content.Builder
	b.Save().Rect(0, 0, 10, 10).Fill() // no Restore: unbalanced
	if _, err := doc.AddPage(Page{Width: 612, Height: 792, Content: &b}); err == nil {
		t.Error("a page with an unbalanced q was accepted")
	}
}

// TestAddPageAppendsRatherThanReplaces pins that a second page joins the first.
func TestAddPageAppendsRatherThanReplaces(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	for i := 0; i < 3; i++ {
		var b content.Builder
		b.Rect(0, 0, 10, 10).Fill()
		if _, err := doc.AddPage(Page{Width: 595, Height: 842, Content: &b}); err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
	}
	if got := doc.PageCount(); got != 3 {
		t.Errorf("PageCount = %d, want 3", got)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := rd.PageCount(); got != 3 {
		t.Errorf("after a round trip PageCount = %d, want 3", got)
	}
	for _, e := range ValidatePDFABytes(rd, pdfa.PDFA2b, buf.Bytes()) {
		t.Errorf("violation: %s", e.Error())
	}
}

// TestAddPageCompressesTheContentStream pins that a produced file is not
// needlessly large, and that the bytes come back through the public reader.
func TestAddPageCompressesTheContentStream(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	var b content.Builder
	for i := 0; i < 200; i++ {
		b.Rect(float64(i), float64(i), 10, 10).Fill()
	}
	drawn, err := b.Bytes()
	if err != nil {
		t.Fatalf("drawing: %v", err)
	}
	ref, err := doc.AddPage(Page{Width: 612, Height: 792, Content: &b})
	if err != nil {
		t.Fatalf("AddPage: %v", err)
	}
	page := doc.ResolveDict(ref)
	stream, ok := doc.Resolve(page.Get("Contents")).(*object.Stream)
	if !ok {
		t.Fatal("the page's /Contents is not a stream")
	}
	if len(stream.Data) >= len(drawn) {
		t.Errorf("the stored stream is %d bytes against %d drawn; it was not compressed",
			len(stream.Data), len(drawn))
	}
	got, err := doc.StreamData(stream)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !bytes.Equal(got, drawn) {
		t.Error("the compressed stream does not decode to what was drawn")
	}
}

// TestOpacityIsAUsableExtGState pins the other half of §3.2's graphics state:
// translucency needs a dictionary the content stream names, and this is it.
func TestOpacityIsAUsableExtGState(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	gs, err := Opacity(0.5, 1)
	if err != nil {
		t.Fatalf("Opacity: %v", err)
	}
	var b content.Builder
	b.Save().SetExtGState("GS0").SetRGB(1, 0, 0).Rect(72, 72, 100, 100).Fill().Restore()
	if _, err := doc.AddPage(Page{
		Width: 612, Height: 792, Content: &b,
		ExtGStates: map[object.Name]object.Object{"GS0": doc.Add(gs)},
	}); err != nil {
		t.Fatalf("AddPage: %v", err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	// PDF/A-2 permits transparency; a translucent page must not be a finding.
	for _, e := range ValidatePDFABytes(rd, pdfa.PDFA2b, buf.Bytes()) {
		t.Errorf("violation on a translucent page: %s", e.Error())
	}
	if _, err := Opacity(1.5, 1); err == nil {
		t.Error("an alpha outside [0,1] was accepted")
	}
}
