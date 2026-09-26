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
	pdf0 "github.com/mgilbir/pdf0"
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
