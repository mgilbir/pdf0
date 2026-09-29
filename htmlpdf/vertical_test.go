package htmlpdf

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/paragraph"
	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/forme/style"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Text set down the page.
//
// A sideways run (vertical-rl, vertical-lr and sideways-rl turn it
// clockwise, sideways-lr anticlockwise) is a horizontal run turned a quarter,
// and is drawn by turning the text matrix. The tests hold the drawing to two
// oracles: forme's own DrawText, whose At is where layout put the pen, and
// Ghostscript's ink bounding box, which knows nothing about either and says
// where the glyphs landed and which way they lie.

// pageSpace is the page transform of a composed document, as a function.
type pageSpace struct{ k, tx, ty float64 }

func pageSpaceOf(c layout.Composed) pageSpace {
	return pageSpace{
		k:  72.0 / 96.0 * c.Scale,
		tx: c.Page.Margin.Left.Pt(),
		ty: c.Page.Height.Pt() - c.Page.Margin.Top.Pt(),
	}
}

func (m pageSpace) point(x, y float64) [2]float64 {
	return [2]float64{m.tx + m.k*x, m.ty - m.k*y}
}

// textMatrices is every Tm a content stream sets, in order.
func textMatrices(t *testing.T, stream []byte) [][6]float64 {
	t.Helper()
	var (
		out      [][6]float64
		operands []core.ContentToken
	)
	for tk := range core.TokenizeContent(core.Canceler{}, stream) {
		if tk.Kind != core.KindOp {
			operands = append(operands, tk)
			continue
		}
		if tk.Op == "Tm" && len(operands) >= 6 {
			var m [6]float64
			for i, o := range operands[len(operands)-6:] {
				m[i] = o.Number()
			}
			out = append(out, m)
		}
		operands = operands[:0]
	}
	return out
}

// inkBox is Ghostscript's bounding box of everything a document paints, in
// points. The test that asks skips when there is no Ghostscript.
func inkBox(t *testing.T, doc *pdf0.Document) [4]float64 {
	t.Helper()
	gs, err := exec.LookPath("gs")
	if err != nil {
		t.Skip("no Ghostscript on this machine; the ink is not checked")
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "page.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(gs, "-q", "-dNOPAUSE", "-dBATCH", "-dSAFER", "-sDEVICE=bbox", path).CombinedOutput()
	if err != nil {
		t.Fatalf("gs: %v\n%s", err, out)
	}
	m := regexp.MustCompile(`%%HiResBoundingBox: ([-0-9.]+) ([-0-9.]+) ([-0-9.]+) ([-0-9.]+)`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("gs printed no bounding box:\n%s", out)
	}
	var box [4]float64
	for i := range box {
		box[i], _ = strconv.ParseFloat(string(m[i+1]), 64)
	}
	return box
}

// TestSidewaysTextIsTurned: in each writing mode that sets Latin text
// sideways, every run is drawn with its text matrix turned the way layout
// turned it, at the pen position layout placed it at, and the ink lies down
// the page from there — on the side the glyphs' tops point to — and the text
// extracts as written.
func TestSidewaysTextIsTurned(t *testing.T) {
	const text = "HHHH HHH"
	set := notoSansSet(t)
	for _, tc := range []struct {
		mode          string
		anticlockwise bool
	}{
		{"vertical-rl", false},
		{"vertical-lr", false},
		{"sideways-rl", false},
		{"sideways-lr", true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			in := Input{
				HTML:  `<p>` + text + `</p>`,
				CSS:   []Stylesheet{{Source: `html { writing-mode: ` + tc.mode + ` } p { margin: 0 }`}},
				Fonts: set,
			}
			c := layout.Compose(in, Options{})
			var runs []layout.DrawText
			for _, op := range c.Ops {
				if d, ok := op.(layout.DrawText); ok && d.Text != "" {
					runs = append(runs, d)
				}
			}
			if len(runs) == 0 {
				t.Fatal("forme drew no text")
			}
			for _, r := range runs {
				if !r.Sideways || r.Anticlockwise != tc.anticlockwise || r.Upright {
					t.Fatalf("forme set %q as Sideways=%v Anticlockwise=%v Upright=%v; the fixture "+
						"expects a sideways run", r.Text, r.Sideways, r.Anticlockwise, r.Upright)
				}
			}

			doc, _ := roundTrip(t, in, Options{})
			if got := strings.Join(strings.Fields(mustExtractText(t, doc)), " "); got != text {
				t.Errorf("extracted %q, want %q", got, text)
			}

			// Each run's Tm: the quarter turn, and the pen where layout put it.
			want := [4]float64{0, 1, 1, 0}
			if tc.anticlockwise {
				want = [4]float64{0, -1, -1, 0}
			}
			tms := textMatrices(t, contentOf(t, doc))
			if len(tms) != len(runs) {
				t.Fatalf("the page sets %d text matrices for %d runs", len(tms), len(runs))
			}
			for i, m := range tms {
				if [4]float64{m[0], m[1], m[2], m[3]} != want {
					t.Errorf("run %d is drawn with the matrix %v, want %v", i, m[:4], want)
				}
				if math.Abs(m[4]-runs[i].At.X.Px()) > 1e-3 || math.Abs(m[5]-runs[i].At.Y.Px()) > 1e-3 {
					t.Errorf("run %d starts at (%v, %v); layout put its pen at (%v, %v)",
						i, m[4], m[5], runs[i].At.X.Px(), runs[i].At.Y.Px())
				}
			}

			// The ink, from a renderer: a column, not a row, starting at the
			// first pen position and running the way the advance goes, on the
			// side the capitals' tops point to.
			ps := pageSpaceOf(c)
			first := runs[0]
			at := ps.point(first.At.X.Px(), first.At.Y.Px())
			size := first.Size.Px() * ps.k
			length := first.Face.Measure(text, first.Size.Px()) * ps.k
			box := inkBox(t, doc)
			w, h := box[2]-box[0], box[3]-box[1]
			if !(h > 3*w) {
				t.Fatalf("the ink is %.1f wide and %.1f tall; a sideways line is a column", w, h)
			}
			const slack = 1.0 // a point: side bearings and the rasteriser's rounding
			capTop := 0.8 * size
			if tc.anticlockwise {
				// Up the page from the pen, the tops to the left.
				if box[2] > at[0]+slack || box[0] < at[0]-capTop || box[1] < at[1]-slack || box[3] > at[1]+length+slack {
					t.Errorf("the ink %v is not up the page and left of the pen %v (length %.1f)", box, at, length)
				}
			} else {
				// Down the page from the pen, the tops to the right.
				if box[0] < at[0]-slack || box[2] > at[0]+capTop || box[3] > at[1]+slack || box[1] < at[1]-length-slack {
					t.Errorf("the ink %v is not down the page and right of the pen %v (length %.1f)", box, at, length)
				}
			}
			if h < 0.8*length {
				t.Errorf("the ink runs %.1f of the %.1f the run advances", h, length)
			}
		})
	}
}

// uprightOps is the upright runs of a composed page, which the fixtures
// expect every run to be.
func uprightOps(t *testing.T, c layout.Composed) []layout.DrawText {
	t.Helper()
	var runs []layout.DrawText
	for _, op := range c.Ops {
		if d, ok := op.(layout.DrawText); ok && d.Text != "" {
			if !d.Upright || !d.Sideways || d.Anticlockwise {
				t.Fatalf("forme set %q as Sideways=%v Anticlockwise=%v Upright=%v; the "+
					"fixture expects upright runs", d.Text, d.Sideways, d.Anticlockwise, d.Upright)
			}
			runs = append(runs, d)
		}
	}
	if len(runs) == 0 {
		t.Fatal("forme drew no text")
	}
	return runs
}

// readPage is the placed glyphs of a written page and where each run left the
// pen, read from the file by the reader's model in glyphreader_test.go. A
// standard font's widths, which the file does not carry, are the face's.
func readPage(t *testing.T, doc *pdf0.Document, standard *shape.Face) ([]placedGlyph, [][2]float64) {
	t.Helper()
	models := fontModels(t, doc, doc.PageList()[0], func(font string, code int) float64 {
		if standard == nil {
			t.Fatalf("font %s is a simple font and the test has no face for its widths", font)
		}
		w, _ := standard.Advance(rune(code)) // ASCII, which WinAnsi agrees with
		return w
	})
	return placedGlyphs(t, contentOf(t, doc), models)
}

// pdftotextOf is poppler's pdftotext's reading of a document, with the white
// space it lays the page out with taken away, or the test is skipped where
// there is no pdftotext.
func pdftotextOf(t *testing.T, doc *pdf0.Document) string {
	t.Helper()
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("no pdftotext on this machine; poppler's reading is not checked")
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "page.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, path, "-").CombinedOutput()
	if err != nil {
		t.Fatalf("pdftotext: %v\n%s", err, out)
	}
	return strings.Join(strings.Fields(string(out)), "")
}

// TestUprightTextIsDrawnByItsVerticalMetrics: CJK text in a vertical writing
// mode stands upright, one character below the other, written in the face's
// vertical form: an Identity-V font whose /W2 states each glyph's own
// vertical metrics. Read back from the file by a reader's model of the font
// dictionaries and the text operators, each glyph is where forme's shaping
// hangs it — the pen moving down by each glyph's vertical advance, the glyph
// hung from its vertical origin and moved by its offsets — which for Noto
// Sans JP puts every glyph centred on the line and inside the em box layout
// measured its character at, and each run ends where layout starts the next.
// The text extracts in the order it was written, through pdf0 and poppler,
// the fonts pass the PDF/A font rules, and Ghostscript finds the ink in that
// column.
func TestUprightTextIsDrawnByItsVerticalMetrics(t *testing.T) {
	set := cjkSet(t)
	for _, tc := range []struct {
		name, text, css string
		spacing         float64 // px of letter-spacing after each character
	}{
		{"vertical-rl", "日本語のテキスト", `html { writing-mode: vertical-rl } p { margin: 0 }`, 0},
		{"vertical-lr", "日本語のテキスト", `html { writing-mode: vertical-lr } p { margin: 0 }`, 0},
		{"letter-spacing", "日本語のテキスト", `html { writing-mode: vertical-rl } p { margin: 0; letter-spacing: 4px }`, 4},
		// Latin set upright is a word to a run, several glyphs each, and
		// the spacing falls between them.
		{"upright-latin", "ABC",
			`html { writing-mode: vertical-rl; text-orientation: upright } p { margin: 0; letter-spacing: 4px }`, 4},
		// 'vpal' sets the kana and the punctuation on their own advances
		// down the line, so the pen moves by less than /W2 says, and the
		// difference is written as displacements.
		{"vpal", "テスト、です。",
			`html { writing-mode: vertical-rl } p { margin: 0; font-feature-settings: "vpal" 1 }`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := tc.text
			in := Input{HTML: `<p>` + text + `</p>`, CSS: []Stylesheet{{Source: tc.css}}, Fonts: set}
			c := layout.Compose(in, Options{})
			runs := uprightOps(t, c)

			doc, raw := roundTrip(t, in, Options{})
			if got := strings.TrimSpace(mustExtractText(t, doc)); got != text {
				t.Errorf("extracted %q, want %q", got, text)
			}
			for _, f := range fontFindings(doc, raw) {
				t.Error(f)
			}

			placed, ends := readPage(t, doc, nil)
			if len(ends) != len(runs) {
				t.Fatalf("the page draws %d runs; layout placed %d", len(ends), len(runs))
			}
			k := 0 // the next placed glyph
			vpalMoved := false
			for r, run := range runs {
				// Where forme hangs each glyph, worked out here from its
				// shaping of the run.
				v := run
				v.Features.Vertical = true
				glyphs, _ := layout.ShapedGlyphs(v)
				size := run.Size.Px()
				s := size / 1000
				pen := 0.0
				unit := 0 // the character, along the run, the glyph belongs to
				for j, g := range glyphs {
					if k >= len(placed) {
						t.Fatalf("the page shows %d glyphs; forme shaped more", len(placed))
					}
					got := placed[k]
					k++
					if !got.vertical {
						t.Errorf("%q is shown in %s, which is not a vertical font", run.Text, got.font)
					}
					if want := set.face.GlyphCode(g.GID); got.code != want {
						t.Errorf("%q: code %d, want %d", run.Text, got.code, want)
					}
					if adv, _, _ := set.face.GlyphVerticalMetrics(g.GID); g.YAdvance != adv {
						vpalMoved = true
					}
					// The text matrix is [1 0 0 -1 At]: up the glyph is up
					// the page.
					want := [2]float64{
						run.At.X.Px() + (g.XOffset-g.VOriginX)*s,
						run.At.Y.Px() - (pen+g.YOffset-g.VOriginY)*s,
					}
					if math.Abs(got.at[0]-want[0]) > 1e-3 || math.Abs(got.at[1]-want[1]) > 1e-3 {
						t.Errorf("%q glyph %d is drawn at %v; forme hangs it at %v", run.Text, j, got.at, want)
					}
					pen += g.YAdvance

					if tc.name != "vpal" {
						// The model layout placed the run by: each
						// character's em box follows the last and its
						// spacing, and is centred across the line.
						top := run.At.Y.Px() + float64(unit)*(size+tc.spacing)
						if y := got.at[1]; !(y > top && y < top+size) {
							t.Errorf("%q's baseline is at %v, outside its em box [%v, %v]", run.Text, y, top, top+size)
						}
					}
					// Within half a font unit: the origin is half the
					// advance halved as a whole number of units, as HarfBuzz
					// halves it, so an odd advance is half a unit off centre.
					halfUnit := 0.5 * 1000 / float64(set.face.UnitsPerEm()) * s
					if mid := got.at[0] + set.face.GlyphAdvance(g.GID)*s/2; math.Abs(mid-run.At.X.Px()) > halfUnit+1e-9 {
						t.Errorf("%q is centred at %v, off the line's middle %v", run.Text, mid, run.At.X.Px())
					}
					// The spacing after each character, down the run.
					if j+1 == len(glyphs) || glyphs[j+1].Cluster != g.Cluster {
						pen -= tc.spacing / s
						unit++
					}
				}
				// The pen ends where layout starts the next run on the
				// line, to within one of layout's units (1/64 px), which
				// is how finely it placed that run.
				if r+1 < len(runs) && runs[r+1].At.X == run.At.X {
					next := runs[r+1].At
					if math.Abs(ends[r][0]-next.X.Px()) > 1e-6 || math.Abs(ends[r][1]-next.Y.Px()) > 1.0/64 {
						t.Errorf("%q leaves the pen at %v; layout starts %q at %v",
							run.Text, ends[r], runs[r+1].Text, [2]float64{next.X.Px(), next.Y.Px()})
					}
				}
			}
			if k != len(placed) {
				t.Errorf("the page shows %d glyphs; forme shaped %d", len(placed), k)
			}
			if tc.name == "vpal" && !vpalMoved {
				t.Error("'vpal' moved no glyph's advance; the case tests nothing")
			}

			if got := pdftotextOf(t, doc); got != text {
				t.Errorf("pdftotext reads %q, want %q", got, text)
			}

			// The ink, from a renderer: a column one em wide, centred on the
			// line, running down the page from the first pen position.
			ps := pageSpaceOf(c)
			first := runs[0]
			at := ps.point(first.At.X.Px(), first.At.Y.Px())
			em := first.Size.Px() * ps.k
			n := float64(len([]rune(text)))
			length := n*em + (n-1)*tc.spacing*ps.k
			box := inkBox(t, doc)
			const slack = 1.0
			if box[0] < at[0]-em/2-slack || box[2] > at[0]+em/2+slack ||
				box[3] > at[1]+slack || box[1] < at[1]-length-slack {
				t.Errorf("the ink %v is not in the column %.1f wide centred on %v and %.1f long", box, em, at, length)
			}
			if h := box[3] - box[1]; h < length-2*em {
				t.Errorf("the ink runs %.1f down the page; %d characters of %.1f take %.1f", h, int(n), em, length)
			}
		})
	}
}

// TestUprightTextInAFaceWithoutVerticalMetricsIsSetOnEmBoxes: Noto Sans and
// the standard faces state no vertical metrics. Layout measures an upright
// run in them at an em a character, CSS Writing Modes 4.4's synthesis, and
// the backend draws them by it: each character's glyphs are hung from the
// top of its em box, where layout.ShapedGlyphs puts them, each character's
// ink is centred in its box (forme #858), and the run is as long as layout
// made it. Noto Sans
// is composite and is written in its vertical form; a standard face has
// none, and its glyphs are placed one by one in the horizontal font.
func TestUprightTextInAFaceWithoutVerticalMetricsIsSetOnEmBoxes(t *testing.T) {
	helvetica, err := shape.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		fonts    layout.FontSet
		face     *shape.Face
		text     string
		spacing  float64
		vertical bool // written in a vertical font
	}{
		{"noto-sans", notoSansSet(t), notoSansSet(t).face, "ABC", 0, true},
		{"noto-sans-spaced", notoSansSet(t), notoSansSet(t).face, "Wiq̇", 3, true},
		{"standard", nil, helvetica, "ABC", 0, false},
		{"standard-spaced", nil, helvetica, "Wiq", 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.face.StatesVerticalMetrics() {
				t.Fatalf("%s states vertical metrics; the case is for a face that states none", tc.name)
			}
			css := `html { writing-mode: vertical-rl; text-orientation: upright } p { margin: 0 }`
			if tc.spacing != 0 {
				css += fmt.Sprintf(` p { letter-spacing: %gpx }`, tc.spacing)
			}
			in := Input{HTML: `<p>` + tc.text + `</p>`, CSS: []Stylesheet{{Source: css}}, Fonts: tc.fonts}
			c := layout.Compose(in, Options{})
			runs := uprightOps(t, c)
			if len(runs) != 1 {
				t.Fatalf("layout set %d runs; the case expects one", len(runs))
			}
			run := runs[0]

			doc, raw := roundTrip(t, in, Options{})
			if got := strings.TrimSpace(mustExtractText(t, doc)); got != tc.text {
				t.Errorf("extracted %q, want %q", got, tc.text)
			}
			if tc.fonts != nil {
				for _, f := range fontFindings(doc, raw) {
					t.Error(f)
				}
			}
			if run.Face.StatesVerticalMetrics() {
				t.Fatalf("%s states vertical metrics; the case is for a face that states none", run.Face.Name())
			}
			var std *shape.Face
			if !tc.vertical {
				std = run.Face
			}
			placed, ends := readPage(t, doc, std)

			// The em box model, from layout's side: the k-th character's
			// box is an em down the line from the last, after its
			// spacing. A character's glyphs — its cluster, and a mark
			// shaping left in a cluster of its own after it — keep their
			// places relative to each other as shaped, hung from the top
			// of the character's boxes.
			v := run
			v.Features.Vertical = true
			glyphs, _ := layout.ShapedGlyphs(v)
			size := run.Size.Px()
			s := size / 1000
			if len(placed) != len(glyphs) {
				t.Fatalf("the page shows %d glyphs; forme shaped %d", len(placed), len(glyphs))
			}
			unitsOf := func(i int) int { // the characters glyph i's cluster holds
				end := len(tc.text)
				for _, g := range glyphs {
					if g.Cluster > glyphs[i].Cluster && g.Cluster < end {
						end = g.Cluster
					}
				}
				return paragraph.UprightUnits(tc.text[glyphs[i].Cluster:end])
			}
			before := 0 // characters before this one
			for lo := 0; lo < len(glyphs); {
				hi := lo + 1
				for hi < len(glyphs) && (glyphs[hi].Cluster == glyphs[lo].Cluster || unitsOf(hi) == 0) {
					hi++
				}
				chars := unitsOf(lo)
				cell := 0.0
				for _, g := range glyphs[lo:hi] {
					cell -= g.YAdvance
				}
				// forme gives the character an em (layout.ShapedGlyphs,
				// forme 5a6c5b6), and each glyph hangs where shaping hung it
				// from the pen at the top of the character's box.
				if cell != 1000*float64(chars) {
					t.Errorf("glyphs %d to %d advance %v down the line for %d character(s); layout measured an em each",
						lo, hi, cell, chars)
				}
				top := run.At.Y.Px() + float64(before)*(size+tc.spacing)
				p := 0.0
				for j := lo; j < hi; j++ {
					g := glyphs[j]
					want := [2]float64{
						run.At.X.Px() + (g.XOffset-g.VOriginX)*s,
						top + (p+g.YOffset-g.VOriginY)*s*-1,
					}
					got := placed[j]
					if got.vertical != tc.vertical {
						t.Errorf("glyph %d is in a vertical font: %v; want %v", j, got.vertical, tc.vertical)
					}
					if math.Abs(got.at[0]-want[0]) > 1e-3 || math.Abs(got.at[1]-want[1]) > 1e-3 {
						t.Errorf("glyph %d of %q is drawn at %v; its em box puts it at %v", j, tc.text, got.at, want)
					}
					// And the character's own glyph is centred in its box,
					// measured from where the file draws it and the ink the
					// font gives the glyph, not from the origin shaping
					// states: an origin synthesized for another advance
					// (HarfBuzz's, the line's height) with the em's advance
					// puts every glyph low in its box (forme #858).
					// A standard face has no glyph program, and states its
					// ink per character (Adobe's AFM boxes, InkExtent).
					if j == lo && chars == 1 {
						var above, below float64 // the ink, from the baseline, in px
						if _, yb, _, h, ok := run.Face.GlyphExtents(g.GID); ok {
							u := size / float64(run.Face.UnitsPerEm())
							above, below = float64(yb)*u, -float64(yb+h)*u
						} else {
							r, _ := utf8.DecodeRuneInString(tc.text[g.Cluster:])
							var ok bool
							if above, below, ok = run.Face.InkExtent(string(r), size); !ok {
								t.Fatalf("%s states no ink for %q; the centring is not measured", run.Face.Name(), r)
							}
						}
						ink := got.at[1] - (above-below)/2
						if centre := top + size/2; math.Abs(ink-centre) > size/100 {
							t.Errorf("glyph %d of %q has its ink centred %+.3f em from its em box's centre (+ is down the line)",
								j, tc.text, (ink-centre)/size)
						}
					}
					p += g.YAdvance
				}
				before += chars
				lo = hi
			}
			// The run is as long as layout made it: an em a character and
			// the spacing after each.
			n := float64(paragraph.UprightUnits(tc.text))
			length := n*size + n*tc.spacing
			// A vertical font moves the pen down the run; the horizontal
			// one places each glyph from the run's origin and leaves it
			// there.
			if tc.vertical && (math.Abs(ends[0][1]-(run.At.Y.Px()+length)) > 1e-6 || math.Abs(ends[0][0]-run.At.X.Px()) > 1e-6) {
				t.Errorf("the run leaves the pen at %v; layout measured it %v long from %v",
					ends[0], length, [2]float64{run.At.X.Px(), run.At.Y.Px()})
			}

			if got := pdftotextOf(t, doc); got != tc.text {
				t.Errorf("pdftotext reads %q, want %q", got, tc.text)
			}
			// The ink: inside the one-em column centred on the line, and
			// inside the run's length.
			ps := pageSpaceOf(c)
			at := ps.point(run.At.X.Px(), run.At.Y.Px())
			em := size * ps.k
			box := inkBox(t, doc)
			const slack = 1.0
			if box[0] < at[0]-em/2-slack || box[2] > at[0]+em/2+slack ||
				box[3] > at[1]+slack || box[1] < at[1]-length*ps.k-slack {
				t.Errorf("the ink %v is not in the column %.1f wide centred on %v and %.1f long", box, em, at, length*ps.k)
			}
		})
	}
}

// TestDrawUprightHangsEachGlyphBelowTheLast holds fonts.Face.DrawUpright to
// forme's metrics over runs of several glyphs, as a caller of the fonts
// package draws them (layout sets each CJK character as its own run), in
// both of the face's forms: the vertical one, and the horizontal one, which
// places each glyph explicitly. The pen moves down by each glyph's vertical
// advance and each glyph is hung from its vertical origin at the pen. The
// cases carry what the vertical form writes displacements for: a combining
// mark that rides on the glyph before it, a mark placed across the line
// (a Hebrew point on a character the face lacks, which forme places by the
// ink of the missing glyph, half an em to the right), and 'vpal', which
// moves the pen by less than the glyphs' own advances. The glyphs are read
// back from the written file.
func TestDrawUprightHangsEachGlyphBelowTheLast(t *testing.T) {
	set := cjkSet(t)
	for _, vertical := range []bool{true, false} {
		for _, tc := range []struct {
			text string
			tags string
		}{
			{"日本語のテキスト", ""},
			{"がき", ""},
			{"か\u3099き", ""},
			{"日\u05D0\u05B7", ""},
			{"テスト、です。", "vpal"},
		} {
			face := fonts.Adopt(set.face.Clone())
			draw := face
			if vertical {
				v, err := face.Vertical()
				if err != nil {
					t.Fatal(err)
				}
				draw = v
			}
			glyphs, _ := face.ShapeGlyphsInContext(tc.text, "", "", shape.Features{Vertical: true, Tags: tc.tags})
			const size, x0, y0 = 20.0, 100.0, 150.0
			var b content.Builder
			b.BeginText().SetFont("F1", size)
			b.SetTextMatrix(1, 0, 0, 1, x0, y0)
			draw.DrawUpright(&b, tc.text, glyphs, size)
			b.EndText()
			doc := pdf0.NewDocument()
			if _, err := doc.AddPage(pdf0.Page{Width: 200, Height: 200, Content: &b,
				Faces: map[object.Name]*fonts.Face{"F1": draw}}); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatal(err)
			}
			back, err := pdf0.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatal(err)
			}
			placed, ends := readPage(t, back, nil)
			if len(placed) != len(glyphs) {
				t.Fatalf("%q: %d glyphs shown, %d shaped", tc.text, len(placed), len(glyphs))
			}
			s, pen := size/1000, 0.0
			for i, g := range glyphs {
				if placed[i].vertical != vertical {
					t.Errorf("%q glyph %d: in a vertical font %v, want %v", tc.text, i, placed[i].vertical, vertical)
				}
				want := [2]float64{x0 + (g.XOffset-g.VOriginX)*s, y0 + (pen+g.YOffset-g.VOriginY)*s}
				if math.Abs(placed[i].at[0]-want[0]) > 1e-3 || math.Abs(placed[i].at[1]-want[1]) > 1e-3 {
					t.Errorf("vertical=%v %q glyph %d is drawn at %v; forme hangs it at %v", vertical, tc.text, i, placed[i].at, want)
				}
				pen += g.YAdvance
			}
			if vertical {
				// The run leaves the pen where shaping ends it, on the line
				// it began: a caller drawing on from here is on its line.
				if want := [2]float64{x0, y0 + pen*s}; math.Abs(ends[0][0]-want[0]) > 1e-3 || math.Abs(ends[0][1]-want[1]) > 1e-3 {
					t.Errorf("%q leaves the pen at %v; shaping ends it at %v", tc.text, ends[0], want)
				}
			}
			if got := strings.TrimSpace(mustExtractText(t, back)); got != tc.text {
				t.Errorf("vertical=%v %q extracts as %q", vertical, tc.text, got)
			}
		}
	}
}

// TestLetterSpacingGoesDownAnUprightRun: the spacing after a unit of an
// upright run is along the run, which is down the page, and a vertical
// advance states down as negative.
func TestLetterSpacingGoesDownAnUprightRun(t *testing.T) {
	set := cjkSet(t)
	v := layout.DrawText{Text: "日本", Face: set.face, Upright: true, Sideways: true}
	v.Size, _ = style.FromPx(16)
	v.CharSpacing, _ = style.FromPx(4)
	v.Features.Vertical = true
	glyphs, _ := layout.ShapedGlyphs(v)
	spaced := withLetterSpacing(glyphs, v.Text, v)
	for i := range glyphs {
		if spaced[i].XAdvance != glyphs[i].XAdvance {
			t.Errorf("glyph %d: the spacing went across the run (XAdvance %v -> %v)", i, glyphs[i].XAdvance, spaced[i].XAdvance)
		}
		// 4px at 16px is 250 thousandths of an em.
		if d := spaced[i].YAdvance - glyphs[i].YAdvance; math.Abs(d+250) > 1e-9 {
			t.Errorf("glyph %d: YAdvance moved by %v, want -250", i, d)
		}
	}
}
