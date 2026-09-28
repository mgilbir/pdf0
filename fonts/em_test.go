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
