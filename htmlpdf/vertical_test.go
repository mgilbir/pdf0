package htmlpdf

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/forme/style"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/core"
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

// shownGlyph is a code a content stream shows, and where: the horizontal
// origin of its glyph, in the coordinates the text matrix maps into.
type shownGlyph struct {
	code int
	at   [2]float64
}

// shownGlyphs follows a content stream's text state — Tm, Tf, Ts, the
// strings of Tj and TJ and TJ's displacements — and returns each two-byte
// code shown, with where its origin is (ISO 32000-2 9.4.4: the glyph is drawn
// at the text matrix applied to the pen's advance along the line and the
// rise). width is a code's width in thousandths of an em. Tc, Tw and Tz are
// not written by this backend and are not followed.
func shownGlyphs(t *testing.T, stream []byte, width func(code int) float64) []shownGlyph {
	t.Helper()
	var (
		out      []shownGlyph
		operands []core.ContentToken
		tm       [6]float64
		tx, rise float64
		size     float64
		inArray  bool
	)
	show := func(codes []byte) {
		for j := 0; j+1 < len(codes); j += 2 {
			code := int(codes[j])<<8 | int(codes[j+1])
			out = append(out, shownGlyph{code, [2]float64{
				tm[0]*tx + tm[2]*rise + tm[4],
				tm[1]*tx + tm[3]*rise + tm[5],
			}})
			tx += width(code) * size / 1000
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
				tx -= tk.Number() * size / 1000
				continue
			}
		case core.KindOp:
			n := len(operands)
			switch {
			case tk.Op == "Tm" && n >= 6:
				for i, o := range operands[n-6:] {
					tm[i] = o.Number()
				}
				tx = 0
			case tk.Op == "Tf" && n >= 1:
				size = operands[n-1].Number()
			case tk.Op == "Ts" && n >= 1:
				rise = operands[n-1].Number()
			case tk.Op == "Tj" && n >= 1:
				show(operands[n-1].Str)
			case tk.Op == "Td" || tk.Op == "TD" || tk.Op == "T*" || tk.Op == "'" || tk.Op == "\"":
				t.Fatalf("the stream moves the line with %s, which this reader does not follow", tk.Op)
			}
			operands = operands[:0]
			continue
		}
		operands = append(operands, tk)
	}
	return out
}

// TestUprightTextIsDrawnByItsVerticalMetrics: CJK text in a vertical writing
// mode stands upright, one character below the other. Each glyph is drawn
// where forme's vertical metrics hang it — the pen moving down by each
// glyph's vertical advance, the glyph hung from its vertical origin and moved
// by its offsets — which for Noto Sans JP puts every glyph centred on the
// line and inside the em box layout measured its character at. The text
// extracts in the order it was written, the fonts pass the PDF/A font rules,
// and Ghostscript finds the ink in that column.
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := tc.text
			in := Input{HTML: `<p>` + text + `</p>`, CSS: []Stylesheet{{Source: tc.css}}, Fonts: set}
			c := layout.Compose(in, Options{})
			// Layout sets each upright character as its own run, in its own
			// em box: an em per character (CSS Writing Modes 4.4).
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

			doc, raw := roundTrip(t, in, Options{})
			if got := strings.TrimSpace(mustExtractText(t, doc)); got != text {
				t.Errorf("extracted %q, want %q", got, text)
			}
			for _, f := range fontFindings(doc, raw) {
				t.Error(f)
			}

			shown := shownGlyphs(t, contentOf(t, doc), func(code int) float64 {
				return set.face.GlyphAdvance(glyphOfCode(t, set, code))
			})
			k := 0 // the next shown glyph
			for _, run := range runs {
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
					if k >= len(shown) {
						t.Fatalf("the page shows %d glyphs; forme shaped more", len(shown))
					}
					got := shown[k]
					k++
					if want := set.face.GlyphCode(g.GID); got.code != want {
						t.Errorf("%q: code %d, want %d", run.Text, got.code, want)
					}
					// The text matrix is [1 0 0 -1 At]: up the glyph is up
					// the page.
					want := [2]float64{
						run.At.X.Px() + (g.XOffset-g.VOriginX)*s,
						run.At.Y.Px() - (pen+g.YOffset-g.VOriginY)*s,
					}
					if math.Abs(got.at[0]-want[0]) > 1e-3 || math.Abs(got.at[1]-want[1]) > 1e-3 {
						t.Errorf("%q is drawn at %v; forme hangs it at %v", run.Text, got.at, want)
					}
					pen += g.YAdvance

					// The model layout placed the run by: each character's
					// em box follows the last and its spacing, and is centred
					// across the line.
					top := run.At.Y.Px() + float64(unit)*(size+tc.spacing)
					if y := got.at[1]; !(y > top && y < top+size) {
						t.Errorf("%q's baseline is at %v, outside its em box [%v, %v]", run.Text, y, top, top+size)
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
			}
			if k != len(shown) {
				t.Errorf("the page shows %d glyphs; forme shaped %d", len(shown), k)
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

// glyphOfCode is the glyph a face's code addresses: for a CID-keyed face the
// glyph whose CID it is.
func glyphOfCode(t *testing.T, set oneFace, code int) int {
	t.Helper()
	for g := 0; g < set.face.NumGlyphs(); g++ {
		if set.face.GlyphCode(g) == code {
			return g
		}
	}
	t.Fatalf("no glyph has the code %d", code)
	return 0
}

// TestDrawUprightHangsEachGlyphBelowTheLast holds fonts.Face.DrawUpright to
// forme's metrics over runs of several glyphs, as a caller of the fonts
// package draws them (layout sets each CJK character as its own run): the pen
// moves down by each glyph's vertical advance, and each glyph is hung from its
// vertical origin at the pen. A combining mark rides on the glyph before it.
func TestDrawUprightHangsEachGlyphBelowTheLast(t *testing.T) {
	set := cjkSet(t)
	face := fonts.Adopt(set.face)
	for _, text := range []string{"日本語のテキスト", "がき"} {
		glyphs, _ := set.face.ShapeGlyphsInContext(text, "", "", shape.Features{Vertical: true})
		const size, x0, y0 = 20.0, 100.0, 50.0
		var b content.Builder
		b.BeginText().SetFont("F1", size)
		b.SetTextMatrix(1, 0, 0, -1, x0, y0)
		face.DrawUpright(&b, text, glyphs, size)
		b.EndText()
		stream, err := b.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		shown := shownGlyphs(t, stream, func(code int) float64 {
			return set.face.GlyphAdvance(glyphOfCode(t, set, code))
		})
		if len(shown) != len(glyphs) {
			t.Fatalf("%q: %d glyphs shown, %d shaped", text, len(shown), len(glyphs))
		}
		s, pen := size/1000, 0.0
		for i, g := range glyphs {
			want := [2]float64{x0 + (g.XOffset-g.VOriginX)*s, y0 - (pen+g.YOffset-g.VOriginY)*s}
			if math.Abs(shown[i].at[0]-want[0]) > 1e-3 || math.Abs(shown[i].at[1]-want[1]) > 1e-3 {
				t.Errorf("%q glyph %d is drawn at %v; forme hangs it at %v", text, i, shown[i].at, want)
			}
			pen += g.YAdvance
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
