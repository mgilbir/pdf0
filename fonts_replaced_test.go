package pdf0

import (
	"strings"
	"testing"

	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// TestDrawReplacedSaysItsReplacement: DrawReplaced and DrawUprightReplaced
// draw a run's glyphs as Draw and DrawUpright do and say what they stand for
// in the page's text with one /ActualText: nothing, for glyphs that are no text
// (a text shadow, an emphasis mark), or another string. In every kind of face
// and both forms, the page extracts as the replacement, holds exactly one
// marked-content sequence however the run shaped (a ligature, a conjunct, a
// right-to-left word would each have one of their own under Draw), and passes
// the PDF/A font rules at every level — the glyphs are the face's and the
// ToUnicode CMap still says what each one means.
func TestDrawReplacedSaysItsReplacement(t *testing.T) {
	levels := []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4}
	for _, fc := range drawPathFaces() {
		t.Run(fc.name, func(t *testing.T) {
			base := fc.load(t)
			for _, upright := range []bool{false, true} {
				for _, actual := range []string{"", "replacement"} {
					text := fc.texts[0]
					face := base.Clone()
					path := drawPath{name: "DrawReplaced", draw: func(t *testing.T, f *fonts.Face, b *content.Builder, s string, size float64) {
						glyphs, _ := f.ShapeGlyphs(s)
						f.DrawReplaced(b, s, glyphs, size, actual)
					}}
					if upright {
						path = drawPath{name: "DrawUprightReplaced", vertical: true, draw: func(t *testing.T, f *fonts.Face, b *content.Builder, s string, size float64) {
							glyphs, _ := f.ShapeGlyphsInContext(s, "", "", shape.Features{Vertical: true})
							f.DrawUprightReplaced(b, s, glyphs, size, actual)
						}}
						var ok bool
						if face, ok = formFor(t, face, path); !ok {
							continue
						}
					}
					for _, level := range levels {
						if !fc.embedded && level != pdfa.PDFA4 {
							continue
						}
						back, _ := drawnDocument(t, face, path, text, level, fc.embedded)
						if got := strings.TrimSpace(mustExtractText(t, back)); got != actual {
							t.Errorf("%s %q, upright %v: extracted %q, want %q", path.name, text, upright, got, actual)
						}
						st, _ := back.Resolve(back.PageList()[0].Get("Contents")).(*object.Stream)
						data, _ := back.StreamData(st)
						if n := strings.Count(string(data), "BDC"); n != 1 {
							t.Errorf("%s %q: %d marked-content sequences; want the one\n%s", path.name, text, n, data)
						}
						if fc.embedded {
							for _, v := range ValidatePDFA(back, level) {
								t.Errorf("%s %s %q: %s", path.name, level, text, v.Error())
							}
						}
						face = face.Clone()
					}
				}
			}
		})
	}
}

// TestAGlyphDrawnByIndexIsEmbedded: a glyph no shaping reaches — the extender
// of a stretched parenthesis, drawn from a MATH table by its index — is
// recorded in forme's record of use as it is drawn (shape.Face.Use), so the
// face is still subset, and the page is valid at every level: the glyph is in
// the subset program, in /W, in /CIDSet and in the ToUnicode CMap, as the
// parenthesis it is a piece of.
func TestAGlyphDrawnByIndexIsEmbedded(t *testing.T) {
	data := formeFile(t, "testdata/harfbuzz/fonts/MathTable.ttf")
	base, err := fonts.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	paren, _ := base.GlyphID('(')
	// Alone, and after shaped text on the same face: a subset that holds the
	// shaped glyphs must still hold the pieces.
	for _, shaped := range []string{"", "("} {
		t.Run("shaped="+shaped, func(t *testing.T) { glyphsDrawnByIndex(t, base, paren, shaped) })
	}
}

// glyphsDrawnByIndex is one case of TestAGlyphDrawnByIndexIsEmbedded: the
// parenthesis's pieces drawn by index, after the text shaped, if any.
func glyphsDrawnByIndex(t *testing.T, base *fonts.Face, paren int, shaped string) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		face := base.Clone()
		// The parenthesis's assembly: its bottom, an extender and its top,
		// glyphs 9, 10 and 11, stacked by their offsets.
		pieces := []fonts.Glyph{{GID: 9}, {GID: 10, YOffset: 600}, {GID: 11, YOffset: 1200}}
		for _, g := range pieces {
			if g.GID == paren {
				t.Fatal("a piece is the parenthesis's own glyph; the case draws nothing unshaped")
			}
		}
		path := drawPath{name: "DrawReplaced", draw: func(t *testing.T, f *fonts.Face, b *content.Builder, s string, size float64) {
			if shaped != "" {
				f.DrawShaped(b, shaped, size)
			}
			f.DrawReplaced(b, s, pieces, size, s)
		}}
		back, _ := drawnDocument(t, face, path, "(", level, true)
		for _, v := range ValidatePDFA(back, level) {
			t.Errorf("%s shaped %q: %s", level, shaped, v.Error())
		}
		if got, want := strings.TrimSpace(mustExtractText(t, back)), shaped+"("; got != want {
			t.Errorf("%s shaped %q: extracted %q, want %q", level, shaped, got, want)
		}
		res := back.ResolveDict(back.PageList()[0].Get("Resources"))
		cid := descendantOf(t, back, back.ResolveDict(res.Get("Font")).Get("F1"))
		// Subset, not whole: a subset's name carries its six-letter tag
		// (ISO 32000-2 9.9.2), and a font embedded whole has none.
		if name, _ := back.Resolve(cid.Get("BaseFont")).(object.Name); !subsetTagged(string(name)) {
			t.Errorf("%s shaped %q: the face is embedded as %q, not as a subset", level, shaped, name)
		}
		for _, g := range pieces {
			w, ok := widthOfCID(t, back, cid, g.GID)
			if !ok {
				w = 1000
				if dw, isInt := back.Resolve(cid.Get("DW")).(object.Integer); isInt {
					w = float64(dw)
				}
			}
			if want := face.GlyphAdvance(g.GID); w != want {
				t.Errorf("%s shaped %q: glyph %d is %v wide in /W; the face advances it %v", level, shaped, g.GID, w, want)
			}
		}
	}
}

// subsetTagged reports whether a font name begins with a subset tag: six
// uppercase letters and a plus sign (ISO 32000-2 9.9.2).
func subsetTagged(name string) bool {
	if len(name) < 7 || name[6] != '+' {
		return false
	}
	for _, c := range name[:6] {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
