package htmlpdf

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/shape"
)

// Glyphs that are not the document's text — a text shadow, an emphasis mark —
// and glyphs that are text and stand for no character — a formula's stretched
// operator, drawn by glyph index.

// opsOf is every operation of a kind in a composed page, in order.
func opsOf[T layout.Op](c layout.Composed) []T {
	var out []T
	for _, op := range c.Ops {
		if v, ok := op.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

// TestATextShadowIsDrawnAndIsNotText: a sharp text shadow is the run's glyphs,
// moved by the offset, in the shadow's colour, under the run — and not text.
// The page extracts as the words once, by pdf0 and by Poppler; the shadow is
// an artifact inside an /ActualText that says nothing; and every glyph of it
// is where forme put the shadow's run.
func TestATextShadowIsDrawnAndIsNotText(t *testing.T) {
	in := Input{HTML: `<p style="text-shadow: 2px 3px rgb(255,0,0)">shadowed words</p>`, Fonts: notoSansSet(t)}
	c := layout.Compose(in, Options{})
	shadows := opsOf[layout.DrawTextShadow](c)
	runs := opsOf[layout.DrawText](c)
	if len(shadows) == 0 || len(runs) == 0 {
		t.Fatalf("layout drew %d shadows and %d runs", len(shadows), len(runs))
	}
	doc, raw := roundTrip(t, in, Options{})
	if got := strings.Join(strings.Fields(mustExtractText(t, doc)), " "); got != "shadowed words" {
		t.Errorf("extracted %q, want the words once", got)
	}
	if got := pdftotextOf(t, doc); got != "shadowedwords" {
		t.Errorf("pdftotext reads %q, want the words once", got)
	}
	stream := string(contentOf(t, doc))
	if n := strings.Count(stream, "/Artifact BMC"); n != len(shadows) {
		t.Errorf("%d artifacts for %d shadows:\n%s", n, len(shadows), stream)
	}
	if n := strings.Count(stream, "/ActualText <FEFF>"); n != len(shadows) {
		t.Errorf("%d empty /ActualText for %d shadows:\n%s", n, len(shadows), stream)
	}
	for _, f := range fontFindings(doc, raw) {
		t.Error(f)
	}
	// Where the glyphs land: each shadow's glyphs where forme placed its
	// run — the text's glyphs, moved by the offset — and drawn before the
	// run it shadows, so that the run is over it.
	placed, _ := readPage(t, doc, nil)
	indexOf := func(v layout.DrawText) int {
		glyphs, _ := layout.ShapedGlyphs(v)
		s := v.Size.Px() / 1000
		pen, first := v.At.X.Px(), -1
		for _, g := range glyphs {
			want := [2]float64{pen + g.XOffset*s, v.At.Y.Px() - g.YOffset*s}
			pen += g.XAdvance * s
			at := -1
			for i, p := range placed {
				if p.code == v.Face.GlyphCode(g.GID) && math.Abs(p.at[0]-want[0]) < 1e-3 && math.Abs(p.at[1]-want[1]) < 1e-3 {
					at = i
					break
				}
			}
			if at < 0 {
				t.Errorf("%q: glyph %d is not drawn at %v", v.Text, g.GID, want)
			}
			if first < 0 {
				first = at
			}
		}
		return first
	}
	if len(shadows) != len(runs) {
		t.Fatalf("%d shadows for %d runs", len(shadows), len(runs))
	}
	for i := range runs {
		if sh := shadows[i].Run; sh.Text != runs[i].Text || sh.At.X-runs[i].At.X != 128 || sh.At.Y-runs[i].At.Y != 192 {
			t.Errorf("shadow %d is %q at %v; the run is %q at %v, 2px across and 3 down from it", i, sh.Text, sh.At, runs[i].Text, runs[i].At)
		}
		if s, r := indexOf(shadows[i].Run), indexOf(runs[i]); s >= r {
			t.Errorf("%q: the shadow is drawn at %d, after the run at %d", runs[i].Text, s, r)
		}
	}
}

// TestAnEmphasisMarkIsDrawnAndIsNotText: "text-emphasis: dot" puts a dot over
// every character, each a DrawEmphasisMark. The dots are drawn where forme put
// them, in the mark's colour, and are not text: the page extracts as the
// words, with no dot among them.
func TestAnEmphasisMarkIsDrawnAndIsNotText(t *testing.T) {
	in := Input{HTML: `<p style="text-emphasis: dot rgb(255,0,0)">emphasis</p>`, Fonts: notoSansSet(t)}
	c := layout.Compose(in, Options{})
	marks := opsOf[layout.DrawEmphasisMark](c)
	if len(marks) != len("emphasis") {
		t.Fatalf("layout drew %d emphasis marks for %d characters", len(marks), len("emphasis"))
	}
	doc, raw := roundTrip(t, in, Options{})
	if got := strings.TrimSpace(mustExtractText(t, doc)); got != "emphasis" {
		t.Errorf("extracted %q, want the word alone", got)
	}
	if got := pdftotextOf(t, doc); got != "emphasis" {
		t.Errorf("pdftotext reads %q, want the word alone", got)
	}
	for _, f := range fontFindings(doc, raw) {
		t.Error(f)
	}
	placed, _ := readPage(t, doc, nil)
	dot, _ := marks[0].Mark.Face.GlyphID('•')
	var at [][2]float64
	for _, g := range placed {
		if g.code == dot {
			at = append(at, g.at)
		}
	}
	if len(at) != len(marks) {
		t.Fatalf("the page shows %d dots; forme drew %d marks", len(at), len(marks))
	}
	for i, m := range marks {
		glyphs, _ := layout.ShapedGlyphs(m.Mark)
		want := [2]float64{m.Mark.At.X.Px() + glyphs[0].XOffset*m.Mark.Size.Px()/1000, m.Mark.At.Y.Px()}
		if math.Abs(at[i][0]-want[0]) > 1e-3 || math.Abs(at[i][1]-want[1]) > 1e-3 {
			t.Errorf("dot %d is drawn at %v; forme's mark is at %v", i, at[i], want)
		}
	}
}

// TestABlurredTextShadowIsRefused: PDF has no blur.
func TestABlurredTextShadowIsRefused(t *testing.T) {
	_, err := Render(Input{HTML: `<p style="text-shadow: 2px 2px 3px red">blurred</p>`, Fonts: notoSansSet(t)}, Options{})
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "text shadow blurred") {
		t.Errorf("rendered a blurred text shadow: %v", err)
	}
}

// mathFace is forme's MATH table fixture: a face with a parenthesis, a radical
// and a sum, their size variants and the pieces of their assemblies, and 'x'.
var mathFace = sync.OnceValues(func() ([]byte, error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/mgilbir/forme").Output()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "testdata", "harfbuzz", "fonts", "MathTable.ttf"))
})

func mathSet(t *testing.T) oneFace {
	t.Helper()
	data, err := mathFace()
	if err != nil {
		t.Fatalf("reading forme's MATH fixture: %v", err)
	}
	f, err := shape.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return oneFace{f}
}

// mathX is an upright x, which the fixture has; an italic one it has not.
const mathX = `<mi mathvariant="normal">x</mi>`

// TestAStretchedOperatorIsItsGlyphsAndItsCharacterOnce: MathML draws a
// stretched radical as the pieces of its assembly, a tall parenthesis the same
// way, and a display sum as its size variant — glyphs no character maps to,
// each a DrawGlyphs. Every piece is drawn where forme placed it, from the
// face's one glyph-code path; the operator extracts as its character once,
// however many pieces draw it, by pdf0 and by Poppler; and the fonts pass the
// PDF/A font rules.
func TestAStretchedOperatorIsItsGlyphsAndItsCharacterOnce(t *testing.T) {
	for _, tc := range []struct{ name, html, text string }{
		{"radical", `<math display="block"><msqrt><mfrac><mfrac>` + mathX + mathX + `</mfrac>` + mathX + `</mfrac></msqrt></math>`, "√"},
		{"parenthesis", `<math display="block"><mrow><mo>(</mo><mfrac><mfrac>` + mathX + mathX + `</mfrac><mfrac>` + mathX + mathX + `</mfrac></mfrac></mrow></math>`, "("},
		{"sum", `<math display="block"><mo>∑</mo>` + mathX + `</math>`, "∑"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := mathSet(t)
			in := Input{HTML: tc.html, Fonts: set}
			c := layout.Compose(in, Options{})
			drawn := opsOf[layout.DrawGlyphs](c)
			if len(drawn) != 1 || drawn[0].Text != tc.text {
				t.Fatalf("layout drew %d glyph operations (%+v); the case needs one for %q", len(drawn), drawn, tc.text)
			}
			v := drawn[0]
			for _, g := range v.Glyphs {
				if _, mapped := set.face.Cmap()[[]rune(tc.text)[0]]; mapped && tc.name != "sum" {
					if gid, _ := set.face.GlyphID([]rune(tc.text)[0]); gid == g.GID {
						t.Fatalf("glyph %d is the character's own; the case needs a variant or pieces", g.GID)
					}
				}
			}
			doc, raw := roundTrip(t, in, Options{})
			got := mustExtractText(t, doc)
			if n := strings.Count(got, tc.text); n != 1 {
				t.Errorf("%q extracts %d times from %q", tc.text, n, got)
			}
			if p := pdftotextOf(t, doc); strings.Count(p, tc.text) != 1 {
				t.Errorf("pdftotext reads %q; want %q once", p, tc.text)
			}
			for _, f := range fontFindings(doc, raw) {
				t.Error(f)
			}
			// The pieces, where forme put them: each at the pen displaced by
			// its offsets, the pen moving by its advances.
			placed, _ := readPage(t, doc, nil)
			s := v.Size.Px() / 1000
			pen := 0.0
			found := 0
			for _, g := range v.Glyphs {
				want := [2]float64{v.At.X.Px() + (pen+g.XOffset)*s, v.At.Y.Px() - g.YOffset*s}
				pen += g.XAdvance
				for _, p := range placed {
					if p.code == set.face.GlyphCode(g.GID) && math.Abs(p.at[0]-want[0]) < 1e-3 && math.Abs(p.at[1]-want[1]) < 1e-3 {
						found++
						break
					}
				}
			}
			if found != len(v.Glyphs) {
				t.Errorf("%d of forme's %d glyphs are drawn where it placed them; the page shows %+v", found, len(v.Glyphs), placed)
			}
		})
	}
}

// TestGlyphsAFaceCannotDrawByIndexAreRefused: a simple or standard face's
// codes are characters, so it cannot draw a glyph by its index, and a glyph
// past a face's last would be written as .notdef.
func TestGlyphsAFaceCannotDrawByIndexAreRefused(t *testing.T) {
	helvetica, err := shape.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	set := mathSet(t)
	for _, tc := range []struct {
		v    layout.DrawGlyphs
		want string
	}{
		{layout.DrawGlyphs{Face: helvetica, Glyphs: []shape.Glyph{{GID: 36}}}, "codes are characters"},
		{layout.DrawGlyphs{Face: set.face, Glyphs: []shape.Glyph{{GID: set.face.NumGlyphs()}}}, "which has"},
		{layout.DrawGlyphs{Face: set.face, Glyphs: []shape.Glyph{{GID: 7}}}, ""},
	} {
		got := undrawable(tc.v)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%+v: refused %q, want %q", tc.v.Glyphs, got, tc.want)
		}
	}
}
