package fonts

import (
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/object"
)

// TestAnEmTheFormatDoesNotAllowIsRefused pins what forme 0.4.0 changed about
// the fonts this package loads: head's unitsPerEm must lie in 16..16384
// (OpenType's head table), and a program stating another is refused by Load and
// LoadSimple rather than embedded.
//
// It used to be embedded. The widths were written on the em as stated, while
// HarfBuzz — and so every browser — measures such a font on a thousand units,
// and FreeType, and every reader built on it, refuses to draw it at all: the
// document's /W and the drawing agreed with no reader. The two ends of the
// range still load, and the widths /W states are the advances scaled by that
// em, which is the whole of what the em is used for here.
func TestAnEmTheFormatDoesNotAllowIsRefused(t *testing.T) {
	program := func(upem int) []byte {
		return fonttest.SFNT(fonttest.SFNTOptions{
			Name:       "Em",
			UnitsPerEm: upem,
			Ascent:     min(upem*4/5, 16000),
			Descent:    -min(upem/5, 16000),
			Glyphs: []fonttest.Glyph{
				{Rune: 'a', Advance: upem / 2, HasShape: true},
				{Rune: 'b', Advance: upem / 4, HasShape: true},
			},
		})
	}
	for _, upem := range []int{1, 15, 16385, 65535} {
		if _, err := Load(program(upem)); err == nil {
			t.Errorf("Load took a font whose em is %d units", upem)
		}
		if _, err := LoadSimple(program(upem)); err == nil {
			t.Errorf("LoadSimple took a font whose em is %d units", upem)
		}
	}
	for _, upem := range []int{16, 16384} {
		f, err := Load(program(upem))
		if err != nil {
			t.Errorf("Load refused a font whose em is %d units: %v", upem, err)
			continue
		}
		if f.UnitsPerEm() != upem {
			t.Errorf("the face reports an em of %d; the program states %d", f.UnitsPerEm(), upem)
		}
		f.ShapeGlyphs("ab")
		doc := &allocator{}
		ref, err := f.Embed(doc)
		if err != nil {
			t.Errorf("em %d: embedding: %v", upem, err)
			continue
		}
		cid := doc.dict(t, doc.dict(t, ref).Get("DescendantFonts").(object.Array)[0])
		for _, c := range []struct {
			r    rune
			want float64
		}{{'a', 500}, {'b', 250}} {
			gid, _ := f.GlyphID(c.r)
			if got := widthIn(t, cid, gid); got != c.want {
				t.Errorf("em %d: %q is %v wide in /W, want %v", upem, c.r, got, c.want)
			}
		}
	}
}

// widthIn is a CID's width as a CIDFont dictionary states it, /DW where /W
// does not list it.
func widthIn(t *testing.T, cid *object.Dictionary, code int) float64 {
	t.Helper()
	num := func(o object.Object) float64 {
		switch v := o.(type) {
		case object.Integer:
			return float64(v)
		case object.Real:
			return float64(v)
		}
		t.Fatalf("a width is %v, not a number", o)
		return 0
	}
	if w, ok := cid.Get("W").(object.Array); ok {
		for i := 0; i+1 < len(w); {
			first := int(num(w[i]))
			if run, ok := w[i+1].(object.Array); ok {
				if code >= first && code < first+len(run) {
					return num(run[code-first])
				}
				i += 2
				continue
			}
			if i+2 < len(w) && code >= first && code <= int(num(w[i+1])) {
				return num(w[i+2])
			}
			i += 3
		}
	}
	return num(cid.Get("DW"))
}

// TestDWIsAnIntegerWhateverTheEm pins that /DW is written as an integer, and
// that the rounding does not move any glyph's width: a program whose em is not
// a thousand units has advances that are fractional in the thousandths PDF
// states them in (1139 of 2048 is 556.15234375), and /DW is typed as an integer
// (ISO 32000-2 9.7.4.3). poppler accepts /DW only as an integer and otherwise
// uses its own default, so a fractional one draws the text with the wrong
// advances in pdftoppm.
//
// The default is rounded, and a glyph is left out of /W only when its exact
// advance is the default, so every glyph the rounding moved is listed in /W
// with its exact width, which is what PDF/A compares to the program. The test
// reads each kept glyph's width as a reader would, /W where listed and /DW
// otherwise, and requires the program's own advance.
func TestDWIsAnIntegerWhateverTheEm(t *testing.T) {
	const upem = 2048
	// Three glyphs share 1139 (556.15...), which makes it the default; the
	// others are one that is exactly a width of its own, one that rounds to the
	// same integer as the default but is not it, and one that is far from it.
	advance := map[rune]int{'a': 1139, 'b': 1139, 'c': 1139, 'd': 1024, 'e': 1140, 'f': 1138}
	var glyphs []fonttest.Glyph
	for _, r := range "abcdef" {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: advance[r], HasShape: true})
	}
	f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{
		Name:       "Dw",
		UnitsPerEm: upem,
		Ascent:     upem * 4 / 5,
		Descent:    -upem / 5,
		Glyphs:     glyphs,
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.ShapeGlyphs("abcdef")
	doc := &allocator{}
	ref, err := f.Embed(doc)
	if err != nil {
		t.Fatal(err)
	}
	cid := doc.dict(t, doc.dict(t, ref).Get("DescendantFonts").(object.Array)[0])
	if dw, ok := cid.Get("DW").(object.Integer); !ok {
		t.Errorf("/DW is %v (%T), want an integer", cid.Get("DW"), cid.Get("DW"))
	} else if dw != 556 {
		t.Errorf("/DW is %d, want 556", dw)
	}
	advances := f.GlyphAdvances()
	for r, units := range advance {
		gid, ok := f.GlyphID(r)
		if !ok {
			t.Fatalf("%q has no glyph", r)
		}
		want := float64(units) * 1000 / upem
		if advances[gid] != want {
			t.Fatalf("%q: the program's advance is %v, the fixture means %v", r, advances[gid], want)
		}
		if got := widthIn(t, cid, gid); got != want {
			t.Errorf("%q is %v wide as the font dictionary states it, want %v", r, got, want)
		}
	}
}
