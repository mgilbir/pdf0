package htmlpdf

import (
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/forme/shape"
)

// TestATaggedPageMarksRightToLeftText: a tagged page's marked content is in
// logical order (ISO 32000-2 14.8.2.5.1), so its right-to-left word carries
// an /ActualText; an untagged page leaves the order to the reader, as PDFium
// and poppler expect (#435). pdf0 extracts the line as written from both.
func TestATaggedPageMarksRightToLeftText(t *testing.T) {
	var glyphs []fonttest.Glyph
	for _, r := range "abcd שלום" {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 500, HasShape: r != ' '})
	}
	face, err := shape.Load(fonttest.SFNT(fonttest.SFNTOptions{Name: "Bidi", Glyphs: glyphs}))
	if err != nil {
		t.Fatal(err)
	}
	const text = "ab שלום cd"
	for _, tagged := range []bool{false, true} {
		doc, _ := roundTrip(t, Input{HTML: "<p>" + text + "</p>", Fonts: oneFace{face}},
			Options{Page: A4, Tagged: tagged, EmbedAllFonts: true})
		marked := strings.Contains(string(contentOf(t, doc)), "/ActualText")
		if marked != tagged {
			t.Errorf("tagged %v: the page carries an /ActualText: %v", tagged, marked)
		}
		if got := strings.Join(strings.Fields(mustExtractText(t, doc)), " "); got != text {
			t.Errorf("tagged %v: pdf0 extracts %q; want %q", tagged, got, text)
		}
	}
}
