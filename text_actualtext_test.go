package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

// Extraction honours /ActualText (ISO 32000-2 14.9.4): inside a marked-content
// sequence that carries one, the sequence's text replaces what its glyphs map
// to. It is what lets a page say what a right-to-left word or a reordered
// cluster reads as, when the glyphs are in the order they are drawn.
func TestExtractTextHonoursActualText(t *testing.T) {
	face, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	props := &object.Dictionary{}
	props.Set("ActualText", object.String{Value: []byte("named")})

	var b content.Builder
	b.BeginText().SetFont("F1", 12).MoveText(10, 10)
	b.ShowText([]byte("A"))
	// Inline, with a nested sequence inside that must not add its own text.
	b.BeginActualText("inline")
	b.ShowText([]byte("XY"))
	b.BeginActualText("nested")
	b.ShowText([]byte("Z"))
	b.EndMarked()
	b.EndMarked()
	// Named, through /Resources /Properties.
	b.BeginMarkedProperties("Span", "P0")
	b.ShowText([]byte("QQ"))
	b.EndMarked()
	// A sequence with no /ActualText changes nothing.
	b.BeginMarked("Span")
	b.ShowText([]byte("B"))
	b.EndMarked()
	b.EndText()

	doc := NewDocument()
	if _, err := doc.AddPage(Page{
		Width: 200, Height: 100, Content: &b,
		Faces:      map[object.Name]*fonts.Face{"F1": face},
		Properties: map[object.Name]object.Object{"P0": props},
	}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(back.ExtractText()); got != "AinlinenamedB" {
		t.Errorf("extracted %q, want %q", got, "AinlinenamedB")
	}
}

// TestAFontSelectedInsideActualTextOutlivesIt: /ActualText replaces what the
// sequence shows, not the state it sets. A font selected inside it is the font
// the text after it is decoded with.
func TestAFontSelectedInsideActualTextOutlivesIt(t *testing.T) {
	simple, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	composite, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	var b content.Builder
	b.BeginText().SetFont("F1", 12).MoveText(10, 10)
	b.BeginActualText("x")
	b.SetFont("F2", 12)
	b.EndMarked()
	codes, _ := composite.Encode("Hi")
	b.ShowText(codes)
	b.EndText()
	doc := NewDocument()
	if _, err := doc.AddPage(Page{Width: 100, Height: 100, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": simple, "F2": composite}}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(back.ExtractText()); got != "xHi" {
		t.Errorf("extracted %q, want %q", got, "xHi")
	}
}

// TestExtractTextSurvivesAStrayEMC: an EMC with nothing open is malformed and
// must neither panic nor start suppressing text.
func TestExtractTextSurvivesAStrayEMC(t *testing.T) {
	doc := NewDocument()
	face, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	var b content.Builder
	b.BeginText().SetFont("F1", 12).ShowText([]byte("A")).EndText()
	if _, err := doc.AddPage(Page{Width: 100, Height: 100, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
		t.Fatal(err)
	}
	pg := doc.PageList()[0]
	// Put a stray EMC in front of the text by hand; the builder would not.
	stream := object.NewStream(nil, []byte("EMC EMC BT /F1 12 Tf (A) Tj ET"))
	stream.Dict.Set("Length", object.Integer(len(stream.Data)))
	pg.Set("Contents", doc.Add(stream))
	if got := strings.TrimSpace(doc.ExtractText()); got != "A" {
		t.Errorf("extracted %q, want %q", got, "A")
	}
}
