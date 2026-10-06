package htmlpdf

import (
	"bytes"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonts/notosans"
	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// The backend draws what layout laid out, on the sheet layout laid it out on,
// and refuses what it cannot draw rather than drawing something else
// (audit 2026-09-22 C24, C26, C27, C28, C114, C168).

// oneFace is a font set with a single face for every family.
type oneFace struct{ face *shape.Face }

func (o oneFace) Face(string, bool, bool) (*shape.Face, bool) { return o.face, true }

func notoSansSet(t *testing.T) oneFace {
	t.Helper()
	f, err := notosans.Face()
	if err != nil {
		t.Fatal(err)
	}
	return oneFace{f}
}

// strikesSet is forme's EBDT fixture: A to Y as 1-bit and 2-bit strikes, and
// no outlines.
func strikesSet(t *testing.T) oneFace {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/mgilbir/forme").Output()
	if err != nil {
		t.Fatalf("locating forme: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "testdata", "freetype", "fonts", "Strikes.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := shape.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return oneFace{f}
}

// emojiSet is Noto Color Emoji's CBDT build: colour bitmaps, no outlines.
func emojiSet(t *testing.T) oneFace {
	t.Helper()
	data, err := os.ReadFile(testfiles.NotoEmoji.File(t, "NotoColorEmoji.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := shape.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return oneFace{f}
}

func cjkSet(t *testing.T) oneFace {
	t.Helper()
	data, err := os.ReadFile(testfiles.NotoCJK.File(t, "NotoSansJP-Regular.otf"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := shape.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return oneFace{f}
}

// roundTrip renders, writes and reads back.
func roundTrip(t *testing.T, in Input, opts Options) (*pdf0.Document, []byte) {
	t.Helper()
	out, err := Render(in, opts)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	var buf bytes.Buffer
	if err := out.Document.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	doc, err := pdf0.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	return doc, buf.Bytes()
}

// fontFindings is what this module's PDF/A-4 validator says about the fonts
// of a document, which is the part of it that applies to a page htmlpdf wrote:
// the document claims no PDF/A level, so everything else is about the claim.
func fontFindings(doc *pdf0.Document, raw []byte) []string {
	var out []string
	for _, v := range pdf0.ValidatePDFA(doc, pdfa.PDFA4) {
		if strings.HasPrefix(v.Rule, "6.2.10") {
			out = append(out, v.Error())
		}
	}
	return out
}

// TestRenderInEveryFaceKindRoundTrips is htmlpdf's row of the drawing-path
// matrix: a CID-keyed CFF, a TrueType face with ligatures and conjuncts, and
// the standard faces, drawn through Render, extracted back and checked by the
// font rules.
func TestRenderInEveryFaceKindRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fonts func(t *testing.T) layout.FontSet
		texts []string
	}{
		{"cid-keyed-cff", func(t *testing.T) layout.FontSet { return cjkSet(t) }, []string{"ｱ日本", "日本語のテキスト"}},
		{"truetype", func(t *testing.T) layout.FontSet { return notoSansSet(t) }, []string{"office", "affluent", "क्षत्रिय", "नमस्ते"}},
		{"standard", func(t *testing.T) layout.FontSet { return nil }, []string{"Hello office", "Café"}},
		// Faces whose glyphs are only bitmaps, written as Type 3 fonts: an
		// EBDT face with 1-bit and 2-bit strikes, and colour emoji.
		{"bitmap-ebdt", func(t *testing.T) layout.FontSet { return strikesSet(t) }, []string{"HELLO", "ABCXY"}},
		{"bitmap-cbdt", func(t *testing.T) layout.FontSet { return emojiSet(t) }, []string{"😀", "👍🏽🎉", "🇪🇸"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, text := range tc.texts {
				// In a colour, which a greyscale strike carries into its glyphs.
				doc, raw := roundTrip(t, Input{HTML: `<p style="color: #c00; font-size: 12pt">` + text + "</p>", Fonts: tc.fonts(t)}, Options{})
				if got := strings.TrimSpace(mustExtractText(t, doc)); got != text {
					t.Errorf("%q extracted as %q", text, got)
				}
				if tc.name == "standard" {
					continue // nothing is embedded, so there is nothing to check
				}
				for _, f := range fontFindings(doc, raw) {
					t.Errorf("%q: %s", text, f)
				}
			}
		})
	}
}

// TestTheSheetIsTheOneThePageRuleSet is C26: @page's size and margin decide
// the sheet layout used, so they decide the one written.
func TestTheSheetIsTheOneThePageRuleSet(t *testing.T) {
	doc, _ := roundTrip(t, Input{
		HTML: `<p>x</p>`,
		CSS:  []Stylesheet{{Source: `@page { size: 100mm 100mm; margin: 0 }`}},
	}, Options{})
	box := mediaBoxOf(t, doc, doc.PageList()[0])
	// Layout's lengths are a fixed-point number of 1/64 px, which is
	// 0.0117pt: the sheet is 100mm to within one of those.
	want := 100 / 25.4 * 72
	const unit = 0.75 / 64
	if math.Abs(box[2]-want) > unit || math.Abs(box[3]-want) > unit {
		t.Errorf("the page is %v x %v points, want the %.2f-point square @page asked for",
			box[2], box[3], want)
	}
	// And no margin: the page transform places the content at the corner.
	stream := contentOf(t, doc)
	if !bytes.Contains(stream, []byte(" 0 "+formatPt(box[3])+" cm")) {
		t.Errorf("the content is not placed at the sheet's own origin:\n%.200s", stream)
	}
}

// formatPt writes a number as the content builder does.
func formatPt(v float64) string {
	var b content.Builder
	b.Concat(v, 0, 0, 1, 0, 0)
	out, _ := b.Bytes()
	return strings.Fields(string(out))[0]
}

func contentOf(t *testing.T, doc *pdf0.Document) []byte {
	t.Helper()
	pg := doc.PageList()[0]
	st, ok := doc.Resolve(pg.Get("Contents")).(*object.Stream)
	if !ok {
		t.Fatal("the page has no content stream")
	}
	data, err := doc.StreamData(st)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestTheGlyphsDrawnAreTheOnesLaidOut is C27: the backend draws the glyphs
// layout measured, with the features the document turned off still off.
func TestTheGlyphsDrawnAreTheOnesLaidOut(t *testing.T) {
	set := notoSansSet(t)
	ffi, _ := set.face.ShapeGlyphs("ffi")
	if len(ffi) != 1 {
		t.Fatalf("the fixture face has no ffi ligature: %v", ffi)
	}
	lig := set.face.GlyphCode(ffi[0].GID)
	ligCode := []byte{byte(lig >> 8), byte(lig)}

	doc, _ := roundTrip(t, Input{
		HTML:  `<p>office</p>`,
		CSS:   []Stylesheet{{Source: `p { font-variant-ligatures: none }`}},
		Fonts: set,
	}, Options{})
	shown := shownCodes(contentOf(t, doc))
	if bytes.Contains(shown, ligCode) {
		t.Errorf("layout measured \"office\" without ligatures and the page draws the ffi ligature: % X", shown)
	}
	if len(shown) != 12 {
		t.Errorf("the page shows %d bytes for six letters; want 12", len(shown))
	}
	if got := strings.TrimSpace(mustExtractText(t, doc)); got != "office" {
		t.Errorf("extracted %q", got)
	}
}

// shownCodes is every byte a content stream shows, from its literal strings.
func shownCodes(stream []byte) []byte {
	var out []byte
	depth := 0
	for i := 0; i < len(stream); i++ {
		c := stream[i]
		switch {
		case depth == 0 && c == '(':
			depth = 1
		case depth > 0 && c == '\\' && i+1 < len(stream):
			i++
			switch stream[i] {
			case 'r':
				out = append(out, '\r')
			default:
				out = append(out, stream[i])
			}
		case depth > 0 && c == '(':
			depth++
			out = append(out, c)
		case depth > 0 && c == ')':
			depth--
			if depth > 0 {
				out = append(out, c)
			}
		case depth > 0:
			out = append(out, c)
		}
	}
	return out
}

// TestAlphaIsPainted is C28: a translucent colour is drawn translucent, with an
// ExtGState carrying its alpha, rather than as the opaque colour.
func TestAlphaIsPainted(t *testing.T) {
	doc, _ := roundTrip(t, Input{
		HTML: `<div style="background: rgba(0,0,0,0.25); height: 20px"></div>` +
			`<p style="color: rgba(255,0,0,0.5)">x</p>`,
	}, Options{})
	pg := doc.PageList()[0]
	res := doc.ResolveDict(pg.Get("Resources"))
	gs := doc.ResolveDict(res.Get("ExtGState"))
	if gs == nil {
		t.Fatal("the page has no /ExtGState, so nothing on it is translucent")
	}
	alphas := map[float64]bool{}
	for k := range gs.Keys() {
		d := doc.ResolveDict(gs.Get(k))
		ca, _ := doc.Resolve(d.Get("ca")).(object.Real)
		CA, _ := doc.Resolve(d.Get("CA")).(object.Real)
		if ca != CA {
			t.Errorf("/%s has /ca %v and /CA %v", k, ca, CA)
		}
		alphas[float64(ca)] = true
	}
	for _, want := range []float64{0.25, 0.5} {
		if !alphas[want] {
			t.Errorf("no ExtGState carries alpha %v; have %v", want, alphas)
		}
	}
	if !bytes.Contains(contentOf(t, doc), []byte(" gs\n")) {
		t.Error("the content stream never selects an ExtGState")
	}
	if doc.ResolveDict(pg.Get("Group")) == nil {
		t.Error("a page with translucent marks has no transparency group")
	}
}

// TestFullyTransparentMarksPaintNothing: alpha zero is no ink. A fill is left
// out, and text is drawn invisible, so it is still in the page's text.
func TestFullyTransparentMarksPaintNothing(t *testing.T) {
	doc, _ := roundTrip(t, Input{
		HTML: `<div style="background: rgba(0,0,0,0); height: 20px"></div>` +
			`<p style="color: transparent">hidden</p>`,
	}, Options{})
	stream := contentOf(t, doc)
	if bytes.Contains(stream, []byte(" re\nf\n")) {
		t.Errorf("a transparent background was filled:\n%s", stream)
	}
	if !bytes.Contains(stream, []byte("3 Tr")) {
		t.Errorf("transparent text was not drawn invisible:\n%s", stream)
	}
	if got := strings.TrimSpace(mustExtractText(t, doc)); got != "hidden" {
		t.Errorf("extracted %q", got)
	}
}

// TestUprightTextIsDrawnInTheDefaultFace is C114's last case: a run set
// upright in the standard faces, which state no vertical metrics, was
// refused, because layout measured it at an em a character and the glyphs
// advanced by the line's height. It is drawn on em boxes now, as layout
// measures it; TestUprightTextInAFaceWithoutVerticalMetricsIsSetOnEmBoxes
// holds where each glyph goes.
func TestUprightTextIsDrawnInTheDefaultFace(t *testing.T) {
	for _, css := range []string{
		`html { writing-mode: vertical-rl; text-orientation: upright }`,
		`html { writing-mode: vertical-lr; text-orientation: upright }`,
	} {
		out, err := Render(Input{
			HTML: `<p>vertical text</p>`,
			CSS:  []Stylesheet{{Source: css}},
		}, Options{})
		if err != nil {
			t.Errorf("%s: %v", css, err)
			continue
		}
		if hasRule(out.Findings, RuleVerticalText, layout.Warn) || hasRule(out.Findings, RuleVerticalText, layout.Error) {
			t.Errorf("%s: reported %s: %v", css, RuleVerticalText, out.Findings)
		}
		if got := strings.Join(strings.Fields(mustExtractText(t, reread(t, out.Document))), " "); got != "vertical text" {
			t.Errorf("%s: extracted %q", css, got)
		}
	}
}

// TestLinksAreNotDroppedSilently is C168: a link this backend does not write
// refuses the document unless the caller accepts losing it. A link it can
// write is written; see link_test.go. This one is relative to the HTML
// document, whose address the backend is not told.
func TestLinksAreNotDroppedSilently(t *testing.T) {
	in := Input{HTML: `<p>see <a href="other.html">this</a></p>`}
	_, err := Render(in, Options{})
	var refused *RefusedError
	if !errors.As(err, &refused) || !hasRule(refused.Findings, RuleLinkDropped, layout.Error) {
		t.Fatalf("a document with a link rendered as though it had none: %v", err)
	}
	in.Policy = layout.Policy{RuleLinkDropped: layout.Warn}
	out, err := Render(in, Options{})
	if err != nil {
		t.Fatalf("with the rule lowered: %v", err)
	}
	if !hasRule(out.Findings, RuleLinkDropped, layout.Warn) {
		t.Errorf("the dropped link was not reported: %v", out.Findings)
	}
	// An <a> with no href is not a link, and is not refused.
	if _, err := Render(Input{HTML: `<p><a name="x">anchor</a></p>`}, Options{}); err != nil {
		t.Errorf("an anchor without href was refused: %v", err)
	}
}

func hasRule(findings []layout.Finding, rule layout.Rule, sev layout.Severity) bool {
	for _, f := range findings {
		if f.Rule == rule && f.Severity == sev {
			return true
		}
	}
	return false
}

// TestLetterSpacingFallsAfterUnitsNotGlyphs: CSS Text §8.2's letter-spacing
// goes after each typographic character unit. A letter with a combining mark
// is one unit and two glyphs, so a run of two such letters is two spacings
// wide, not four — the Tc operator this backend used adds one after every
// glyph, and moved each mark off its letter.
func TestLetterSpacingFallsAfterUnitsNotGlyphs(t *testing.T) {
	set := notoSansSet(t)
	const text = "x\u0301x\u0301" // no precomposed form, so each stays two glyphs
	glyphs, _ := set.face.ShapeGlyphs(text)
	if len(glyphs) != 4 {
		t.Fatalf("the fixture shapes %q as %d glyphs; the test needs a letter and a mark each", text, len(glyphs))
	}
	doc, _ := roundTrip(t, Input{
		HTML:  `<p>` + text + `</p>`,
		CSS:   []Stylesheet{{Source: `p { letter-spacing: 10px }`}},
		Fonts: set,
	}, Options{})
	stream := contentOf(t, doc)
	const size = 16 // the default font size, in px, which is what Tf states
	got := penAdvance(t, stream, size, func(code int) float64 { return set.face.GlyphAdvance(code) })
	var want float64
	for _, g := range glyphs {
		want += g.XAdvance * size / 1000
	}
	want += 2 * 10 // two units, ten px each
	if math.Abs(got-want) > 0.01 {
		t.Errorf("%s\nthe run advances the pen %.3f px; two units of \"x\u0301\" with 10px of "+
			"letter-spacing each is %.3f", stream, got, want)
	}
}

// penAdvance is how far the text operators of a single run move the pen, in
// text space: each two-byte code by its width plus Tc, each TJ number by
// minus a thousandth of the size. It reads the stream with the module's own
// content tokenizer.
func penAdvance(t *testing.T, stream []byte, size float64, width func(code int) float64) float64 {
	t.Helper()
	var (
		pen, tc  float64
		operands []core.ContentToken
		inArray  bool
	)
	show := func(codes []byte) {
		for j := 0; j+1 < len(codes); j += 2 {
			pen += width(int(codes[j])<<8|int(codes[j+1]))*size/1000 + tc
		}
	}
	for tk := range core.TokenizeContent(core.Canceler{}, stream) {
		switch tk.Kind {
		case core.KindArrayStart:
			inArray = true
			continue
		case core.KindArrayEnd:
			inArray = false
			continue
		case core.KindString:
			if inArray {
				show(tk.Str)
				continue
			}
		case core.KindNumber:
			if inArray {
				pen -= tk.Number() * size / 1000
				continue
			}
		case core.KindOp:
			switch tk.Op {
			case "Tc":
				if len(operands) > 0 {
					tc = operands[len(operands)-1].Number()
				}
			case "Tj":
				if len(operands) > 0 {
					show(operands[len(operands)-1].Str)
				}
			}
			operands = operands[:0]
			continue
		}
		operands = append(operands, tk)
	}
	return pen
}

// TestTheTilingPatternIsCompressed: the one stream this backend writes itself
// is Flate-compressed like every other, and decodes to the cell that draws the
// tile.
func TestTheTilingPatternIsCompressed(t *testing.T) {
	got := renderWithImages(t, `<div id="a">x</div>`, noDefaults+
		`#a { width: 200px; height: 100px;
		      background-image: url(wide.png); background-repeat: repeat }`)
	doc := reread(t, got.Document)
	page, _ := pageContent(t, doc)
	patterns := doc.ResolveDict(doc.ResolveDict(page.Get("Resources")).Get("Pattern"))
	if patterns == nil || patterns.Len() != 1 {
		t.Fatalf("want one pattern, have %v", patterns)
	}
	st, _ := doc.Resolve(slices.Collect(patterns.Values())[0]).(*object.Stream)
	if f, _ := st.Dict.Get("Filter").(object.Name); f != "FlateDecode" {
		t.Errorf("the pattern stream's /Filter is %v", st.Dict.Get("Filter"))
	}
	cell, err := doc.StreamData(st)
	if err != nil || !bytes.Contains(cell, []byte(" Do")) {
		t.Errorf("the pattern cell decodes to %q (%v), which draws no image", cell, err)
	}
}

// TestEachVerticalFlagIsJudgedOnItsOwn: the check reads the flags as they
// are, not as layout happens to combine them today. A turn either way is
// drawn, and so is an upright run down a clockwise line; a combination that
// is not one (Anticlockwise or Upright without Sideways, Upright with
// Anticlockwise) is refused, and a caller who lowers the rule gets the page
// with the finding at the severity it chose.
func TestEachVerticalFlagIsJudgedOnItsOwn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		op      layout.DrawText
		refused bool
	}{
		{"across", layout.DrawText{}, false},
		{"Sideways", layout.DrawText{Sideways: true}, false},
		{"Sideways+Anticlockwise", layout.DrawText{Sideways: true, Anticlockwise: true}, false},
		{"Anticlockwise", layout.DrawText{Anticlockwise: true}, true},
		{"Upright", layout.DrawText{Upright: true}, true},
		{"Sideways+Upright", layout.DrawText{Sideways: true, Upright: true}, false},
		{"Anticlockwise+Upright", layout.DrawText{Anticlockwise: true, Upright: true}, true},
		{"all three", layout.DrawText{Sideways: true, Anticlockwise: true, Upright: true}, true},
	} {
		findings, refused := checkDrawable(layout.Composed{Ops: []layout.Op{tc.op}}, nil)
		if refused != tc.refused || hasRule(findings, RuleVerticalText, layout.Error) != tc.refused {
			t.Errorf("%s: refused=%v with %v; want refused=%v", tc.name, refused, findings, tc.refused)
		}
		lowered, refused := checkDrawable(layout.Composed{Ops: []layout.Op{tc.op}},
			layout.Policy{RuleVerticalText: layout.Warn})
		if refused || hasRule(lowered, RuleVerticalText, layout.Warn) != tc.refused {
			t.Errorf("%s lowered to Warn: refused=%v with %v", tc.name, refused, lowered)
		}
	}
}

// TestABitmapFacesStrikeIsTheOneItsCSSPixelSizePicks: a bitmap face's strike
// is chosen by the text size in CSS pixels, which is what htmlpdf's content
// stream is in. 9pt is 12px, which forme's EBDT fixture has a 1-bit strike
// for; 12pt is 16px, which it has a 2-bit strike for, drawn in the text colour.
func TestABitmapFacesStrikeIsTheOneItsCSSPixelSizePicks(t *testing.T) {
	set := strikesSet(t)
	gid, _ := set.face.GlyphID('A')
	for _, tc := range []struct {
		css     string
		ppem    int
		painter string // the procedure's operator: d1 a stencil, d0 a colour
	}{
		{"font-size: 9pt", 12, "d1"},
		{"font-size: 12pt", 16, "d0"},
	} {
		doc, _ := roundTrip(t, Input{HTML: `<p style="color: #c00; ` + tc.css + `">A</p>`, Fonts: set}, Options{})
		var want oneImageCapture
		if err := set.face.PaintGlyph(gid, shape.PaintOptions{PPEM: tc.ppem}, &want); err != nil || !want.got {
			t.Fatalf("A has no image at %d ppem: %v", tc.ppem, err)
		}
		res := doc.ResolveDict(doc.PageList()[0].Get("Resources"))
		found := false
		for _, ref := range doc.ResolveDict(res.Get("Font")).All() {
			fd := doc.ResolveDict(ref)
			proc, ok := doc.Resolve(doc.ResolveDict(fd.Get("CharProcs")).Get(object.Name("g" + strconv.Itoa(gid)))).(*object.Stream)
			if !ok {
				continue
			}
			found = true
			data, _ := doc.StreamData(proc)
			if !strings.Contains(string(data), " "+tc.painter+"\n") {
				t.Errorf("%s: A's procedure is not %s:\n%s", tc.css, tc.painter, data)
			}
			for _, im := range doc.ResolveDict(doc.ResolveDict(fd.Get("Resources")).Get("XObject")).All() {
				st := doc.Resolve(im).(*object.Stream)
				if sm := st.Dict.Get("SMask"); sm != nil {
					st = doc.Resolve(sm).(*object.Stream)
				}
				if st.Dict.Get("Width") != object.Integer(want.img.Width) || st.Dict.Get("Height") != object.Integer(want.img.Height) {
					t.Errorf("%s: A is drawn %v×%v, the %d ppem strike's image is %d×%d",
						tc.css, st.Dict.Get("Width"), st.Dict.Get("Height"), tc.ppem, want.img.Width, want.img.Height)
				}
			}
		}
		if !found {
			t.Errorf("%s: no font on the page draws A", tc.css)
		}
	}
}

// oneImageCapture keeps the one image a bitmap glyph is painted as.
type oneImageCapture struct {
	img shape.Image
	got bool
}

func (o *oneImageCapture) PushTransform(shape.Transform)       {}
func (o *oneImageCapture) PopTransform()                       {}
func (o *oneImageCapture) PushClipGlyph(int)                   {}
func (o *oneImageCapture) PushClipRect(shape.Rect)             {}
func (o *oneImageCapture) PopClip()                            {}
func (o *oneImageCapture) PushGroup()                          {}
func (o *oneImageCapture) PopGroup(shape.CompositeMode)        {}
func (o *oneImageCapture) Solid(shape.Color, bool)             {}
func (o *oneImageCapture) LinearGradient(shape.LinearGradient) {}
func (o *oneImageCapture) RadialGradient(shape.RadialGradient) {}
func (o *oneImageCapture) SweepGradient(shape.SweepGradient)   {}
func (o *oneImageCapture) Image(img shape.Image)               { o.img, o.got = img, true }
