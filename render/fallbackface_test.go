package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// The fallback face, and what it is actually worth.
//
// The fourteen standard PDF faces cover Latin. A document with a Hebrew word in
// it gets one of them, which cannot encode the letters — and the encoder
// substitutes a space for anything it cannot represent, so the word does not
// appear as a row of boxes a reader would query. It is simply absent, from the
// page and from the text extracted out of it.
//
// That is what this fixes, and the fix is worth stating precisely because the
// reftest count does not move for it: a document that needed the substitution
// reported a missing glyph before and reports a font substitution now, and both
// keep it out of the clean-pass bucket. The gain is that the text is on the page.

// notoDir returns the fetched fallback fonts, or skips.
func notoDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("NOTO_FONTS")
	if dir == "" {
		t.Skip("set NOTO_FONTS (or run `make noto-fonts`) to check the fallback face")
	}
	return dir
}

// oneFaceSet is a FontSet with a single fallback face, which is enough to pin
// the mechanism without depending on which scripts the fetched set covers.
type oneFaceSet struct {
	fallback *fonts.Face
	standard FontSet
}

func (s oneFaceSet) Face(family string, bold, italic bool) (*fonts.Face, bool) {
	return s.standard.Face(family, bold, italic)
}

func (s oneFaceSet) FaceFor(text string, bold, italic bool) (*fonts.Face, bool) {
	if _, missing := s.fallback.Shape(text); missing == 0 {
		return s.fallback, true
	}
	return nil, false
}

func loadNoto(t *testing.T, name string) *fonts.Face {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(notoDir(t), name))
	if err != nil {
		t.Skipf("no %s: %v", name, err)
	}
	face, err := fonts.Load(data)
	if err != nil {
		t.Fatalf("loading %s: %v", name, err)
	}
	return face
}

func loadHebrew(t *testing.T) *fonts.Face {
	return loadNoto(t, "NotoSansHebrew-Regular.ttf")
}

// layoutWith lays a document out against a given font set.
func layoutWith(t *testing.T, set FontSet, htmlSrc, cssSrc string) (*Fragment, []Finding) {
	t.Helper()
	built := Build(Input{HTML: htmlSrc, CSS: []Stylesheet{{Source: cssSrc}}})
	rec := NewRecorder(nil)
	w, _ := style.FromPx(600)
	h, _ := style.FromPx(10000)
	frag := Layout(built.Root, Size{W: w, H: h}, set, rec)
	return frag, rec.Findings()
}

func TestTextWithNoGlyphIsSetInAFallbackFace(t *testing.T) {
	hebrew := loadHebrew(t)
	const doc = `<p id="p">שלום</p>`
	const css = `#p { font-family: Helvetica; font-size: 20px }`

	// Without a fallback the letters are unencodable, so they are reported and
	// the run is set in a face that will put spaces where they were.
	_, plain := layoutWith(t, StandardFonts(), doc, css)
	var missing int
	for _, f := range plain {
		if f.Rule == RuleGlyphMissing {
			missing++
		}
	}
	if missing == 0 {
		t.Fatal("Hebrew in a Latin-only face was not reported at all; this test is " +
			"not exercising what it claims to")
	}

	// With one, the letters are set in the face that has them.
	set := oneFaceSet{fallback: hebrew, standard: StandardFonts()}
	frag, withFallback := layoutWith(t, set, doc, css)
	for _, f := range withFallback {
		if f.Rule == RuleGlyphMissing {
			t.Errorf("a missing glyph was still reported with a fallback face "+
				"available: %s", f.Message)
		}
	}
	// And the substitution is reported, because it changes the metrics and so
	// changes where every line breaks. Silently swapping a font is the thing
	// this design is against.
	var substituted bool
	for _, f := range withFallback {
		if f.Rule == RuleFontFallback {
			substituted = true
		}
	}
	if !substituted {
		t.Error("the face was substituted without saying so")
	}

	// The text is on the page, in the fallback face rather than the family's.
	var runs int
	for _, line := range find(t, frag, "p").Lines {
		for _, r := range line.Runs {
			runs++
			if r.Face == nil || r.Face.Name() != hebrew.Name() {
				t.Errorf("the run %q was set in %v, want the fallback face %q",
					r.Text, r.Face, hebrew.Name())
			}
		}
	}
	if runs == 0 {
		t.Error("no text was laid out at all")
	}
}

func TestLatinIsNotSubstituted(t *testing.T) {
	// The fallback must not fire for text the family can already set. An earlier
	// version triggered on any character the face could not encode, which
	// included the no-break space — so a paragraph whose only unusual character
	// was one of those had every metric on the page changed to fix nothing. It
	// cost 38 clean reftest passes and was caught by the ratchet.
	//
	// The fallback here has to be a face that *could* set this text, or the test
	// proves nothing: with a Hebrew-only face the substitution is refused for
	// want of Latin glyphs whatever the trigger decides, and a planted defect
	// that made the trigger always fire went unnoticed. Noto Sans covers Latin
	// and the no-break space, so a spurious trigger really does substitute.
	latin := loadNoto(t, "NotoSans-Regular.ttf")
	set := oneFaceSet{fallback: latin, standard: StandardFonts()}
	_, findings := layoutWith(t, set,
		"<p id=\"p\">a\u00a0b c</p>",
		`#p { font-family: Helvetica; font-size: 20px }`)
	for _, f := range findings {
		if f.Rule == RuleFontFallback {
			t.Errorf("Latin text with a no-break space was substituted: %s", f.Message)
		}
	}
}
