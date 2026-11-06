package pdf0

import (
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

// Extraction honours /ReversedChars (ISO 32000-2 14.8.2.5.3): inside a
// sequence with that tag, "the sequence of the characters as found in the
// show string operator shall be reversed", string by string.

// extractedFrom is the text pdf0 extracts from one page drawn by draw, in the
// faces named.
func extractedFrom(t *testing.T, faces map[object.Name]*fonts.Face, props map[object.Name]object.Object, draw func(b *content.Builder)) string {
	t.Helper()
	var b content.Builder
	b.BeginText().SetFont("F1", 12).MoveText(10, 10)
	draw(&b)
	b.EndText()
	doc := NewDocument()
	if _, err := doc.AddPage(Page{Width: 200, Height: 100, Content: &b, Faces: faces, Properties: props}); err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndRead(t, doc)
	return mustExtractText(t, back)
}

func TestExtractTextHonoursReversedChars(t *testing.T) {
	helvetica, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	simple := map[object.Name]*fonts.Face{"F1": helvetica}

	t.Run("the standard's example", func(t *testing.T) {
		got := extractedFrom(t, simple, nil, func(b *content.Builder) {
			b.BeginMarked("ReversedChars")
			b.ShowText([]byte(" olleH"))
			b.MoveText(-200, 0)
			b.ShowText([]byte(" .dlrow"))
			b.EndMarked()
			b.ShowText([]byte("after"))
		})
		// "Hello world." — each string backwards, the strings in order;
		// pdf0 breaks the line at every Td. After the EMC, order is as drawn.
		if want := "Hello \nworld. after"; strings.TrimSpace(got) != want {
			t.Errorf("extracted %q; want %q", strings.TrimSpace(got), want)
		}
	})

	t.Run("BDC, TJ, and an /ActualText inside", func(t *testing.T) {
		props := &object.Dictionary{}
		props.Set("MCID", object.Integer(0))
		got := extractedFrom(t, simple, map[object.Name]object.Object{"P0": props}, func(b *content.Builder) {
			b.BeginMarkedProperties("ReversedChars", "P0")
			b.ShowTextAdjusted(content.TextSpan{Codes: []byte("cba")}, content.TextSpan{Adjust: -10}, content.TextSpan{Codes: []byte("fed")})
			// What an /ActualText says is the text, not a string to reverse.
			b.BeginActualText("xyz")
			b.ShowText([]byte("QQ"))
			b.EndMarked()
			b.EndMarked()
		})
		if want := "abcdefxyz"; strings.TrimSpace(got) != want {
			t.Errorf("extracted %q; want %q", strings.TrimSpace(got), want)
		}
	})

	t.Run("a code for several characters", func(t *testing.T) {
		// A glyph drawn for "fi", whose ToUnicode entry is the two letters:
		// reversing the string's codes keeps them in their order.
		var gs []fonttest.Glyph
		for _, r := range "fia" {
			gs = append(gs, fonttest.Glyph{Rune: r, Advance: 500, HasShape: true})
		}
		face, err := fonts.Load(fonttest.SFNT(fonttest.SFNTOptions{Name: "Lig", Glyphs: gs}))
		if err != nil {
			t.Fatal(err)
		}
		f, _ := face.GlyphID('f')
		a, _ := face.GlyphID('a')
		if _, err := face.Record("fia", []fonts.Glyph{{GID: f, Cluster: 0}, {GID: a, Cluster: 2}}); err != nil {
			t.Fatal(err)
		}
		got := extractedFrom(t, map[object.Name]*fonts.Face{"F1": face}, nil, func(b *content.Builder) {
			var codes []byte
			codes, _ = face.AppendCode(codes, a)
			codes, _ = face.AppendCode(codes, f)
			b.BeginMarked("ReversedChars")
			b.ShowText(codes)
			b.EndMarked()
		})
		if want := "fia"; strings.TrimSpace(got) != want {
			t.Errorf("extracted %q; want %q (reversing characters, not codes, gives \"aif\")", strings.TrimSpace(got), want)
		}
	})
}
