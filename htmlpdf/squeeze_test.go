package htmlpdf

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/paragraph"
	"github.com/mgilbir/forme/style"
	pdf0 "github.com/mgilbir/pdf0"
)

// A run squeezed across the direction it advances in (DrawText.WidthScale): a
// text-combine-upright composition wider than the em it stands in (CSS Writing
// Modes 9.1.3). It is written with PDF's horizontal scaling, Tz, which scales
// the glyphs and every displacement along the run (ISO 32000-2 9.3.4) — which
// is what forme squeezes — and not its letter-spacing, which forme does not.

// squeezedRun is the one run of a composed page with WidthScale set.
func squeezedRun(t *testing.T, c layout.Composed) layout.DrawText {
	t.Helper()
	var out []layout.DrawText
	for _, op := range c.Ops {
		if v, ok := op.(layout.DrawText); ok && v.WidthScale > 0 {
			out = append(out, v)
		}
	}
	if len(out) != 1 {
		t.Fatalf("layout squeezed %d runs; the case needs one", len(out))
	}
	return out[0]
}

// TestACombinedRunIsSqueezedIntoItsEm composes "12345" as one
// text-combine-upright composition, which Noto Sans has no width variant for,
// so layout squeezes it to fit an em. The page draws it with Tz at a hundred
// times the squeeze, each glyph where forme's reference drawing puts it (its
// advance and offset squeezed), and Ghostscript finds its ink inside the em
// square layout centred it in. It extracts as written.
func TestACombinedRunIsSqueezedIntoItsEm(t *testing.T) {
	const text = "12345"
	set := notoSansSet(t)
	in := Input{
		HTML: `<p>` + text + `</p>`,
		CSS: []Stylesheet{{Source: `html { writing-mode: vertical-rl } ` +
			`p { margin: 0; text-combine-upright: all }`}},
		Fonts: set,
	}
	c := layout.Compose(in, Options{})
	run := squeezedRun(t, c)
	if run.Sideways || run.Upright || !(run.WidthScale < 1) {
		t.Fatalf("the composition is Sideways=%v Upright=%v squeezed by %v; the case expects a horizontal run squeezed",
			run.Sideways, run.Upright, run.WidthScale)
	}
	doc, _ := roundTrip(t, in, Options{})
	stream := contentOf(t, doc)
	if want := formatPt(100*run.WidthScale) + " Tz"; !bytes.Contains(stream, []byte(want)) {
		t.Errorf("the stream does not set %q:\n%s", want, stream)
	}
	if got := strings.TrimSpace(mustExtractText(t, doc)); got != text {
		t.Errorf("extracted %q, want %q", got, text)
	}
	checkSqueezedPlacement(t, doc, run)

	// The ink: an em wide at most, where the unsqueezed run is nearly three.
	ps := pageSpaceOf(c)
	em := run.Size.Px() * ps.k
	box := inkBox(t, doc)
	const slack = 1.0
	if w := box[2] - box[0]; w > em+slack {
		t.Errorf("the composition's ink is %.2f pt wide; its em is %.2f", w, em)
	}
	full := run.Face.Measure(text, run.Size.Px()) * ps.k
	if full < 2*em {
		t.Fatalf("the unsqueezed run is %.2f pt, under two ems; the case does not show a squeeze", full)
	}
}

// checkSqueezedPlacement holds where the page puts each glyph of a squeezed
// horizontal run to forme's model (its reference drawing's, for a run with
// WidthScale): the pen starts at At and moves by each glyph's advance times
// the squeeze, a glyph is displaced by its offset times the squeeze across and
// its offset unsqueezed up, and letter-spacing falls unsqueezed after each
// typographic character unit (paragraph.SpacingAfterOffsets), counted after
// the last glyph of the cluster it ends in.
func checkSqueezedPlacement(t *testing.T, doc *pdf0.Document, run layout.DrawText) {
	t.Helper()
	placed, ends := readPage(t, doc, nil)
	glyphs, _ := layout.ShapedGlyphs(run)
	if len(placed) != len(glyphs) {
		t.Fatalf("the page shows %d glyphs; forme shaped %d", len(placed), len(glyphs))
	}
	text := layout.ShapedText(run)
	unitEnds := paragraph.SpacingAfterOffsets(text)
	// Each cluster runs from its offset to the next larger one.
	next := func(c int) int {
		n := len(text)
		for _, g := range glyphs {
			if g.Cluster > c && g.Cluster < n {
				n = g.Cluster
			}
		}
		return n
	}
	s := run.Size.Px() / 1000
	pen := run.At.X.Px()
	for i, g := range glyphs {
		want := [2]float64{pen + g.XOffset*s*run.WidthScale, run.At.Y.Px() - g.YOffset*s}
		if got := placed[i].at; math.Abs(got[0]-want[0]) > 1e-3 || math.Abs(got[1]-want[1]) > 1e-3 {
			t.Errorf("glyph %d is drawn at %v; forme squeezes it to %v", i, got, want)
		}
		pen += g.XAdvance * s * run.WidthScale
		if i+1 == len(glyphs) || glyphs[i+1].Cluster != g.Cluster {
			for at := range unitEnds {
				if at >= g.Cluster && at < next(g.Cluster) {
					pen += run.CharSpacing.Px()
				}
			}
		}
	}
	if len(ends) != 1 || math.Abs(ends[0][0]-pen) > 1e-3 {
		t.Errorf("the run leaves the pen at %v; forme's model at %v", ends, pen)
	}
}

// TestASqueezedRunKeepsItsLetterSpacing draws a squeezed run with
// letter-spacing, which forme adds after each unit without squeezing it (its
// reference drawing, and DrawText.WidthScale's width). Tz squeezes every
// displacement, so the backend writes the spacing divided by the squeeze; the
// page must end the run where forme's model does, not half a spacing short.
func TestASqueezedRunKeepsItsLetterSpacing(t *testing.T) {
	set := notoSansSet(t)
	size, _ := style.FromPx(16)
	spacing, _ := style.FromPx(3)
	x, _ := style.FromPx(20)
	y, _ := style.FromPx(40)
	run := layout.DrawText{
		At: layout.Point{X: x, Y: y}, Text: "Wide text", Face: set.face,
		Size: size, Color: style.RGBA{A: 1}, CharSpacing: spacing, WidthScale: 0.5,
	}
	doc, err := writePage([]layout.Op{run}, layout.PageSizePt(200, 100), 1)
	if err != nil {
		t.Fatal(err)
	}
	doc = rewritten(t, doc)
	checkSqueezedPlacement(t, doc, run)
	if got := strings.TrimSpace(mustExtractText(t, doc)); !strings.Contains(strings.ReplaceAll(got, " ", ""), "Widetext") {
		t.Errorf("extracted %q", got)
	}
}

// TestASqueezeExtractsAsTheUnsqueezedRunDoes: pdf0's extractor reads a gap in
// a TJ array as a space by its size in thousandths of the em, and Tz scales
// the gap and the glyphs alike, so a squeeze changes nothing it decides. The
// same run with and without a squeeze extracts as the same text.
func TestASqueezeExtractsAsTheUnsqueezedRunDoes(t *testing.T) {
	set := notoSansSet(t)
	size, _ := style.FromPx(16)
	for _, text := range []string{"12 34", "AVATAR Wave", "office"} {
		var got []string
		for _, ws := range []float64{0, 0.3} {
			run := layout.DrawText{Text: text, Face: set.face, Size: size, Color: style.RGBA{A: 1}, WidthScale: ws}
			run.At.Y, _ = style.FromPx(40)
			doc, err := writePage([]layout.Op{run}, layout.PageSizePt(300, 100), 1)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, strings.TrimSpace(mustExtractText(t, rewritten(t, doc))))
		}
		if got[0] != text || got[1] != got[0] {
			t.Errorf("%q extracts as %q unsqueezed and %q squeezed", text, got[0], got[1])
		}
	}
}

// TestASqueezedUprightRunIsRefused: Tz scales across the page, and an upright
// run advances down it, so a squeeze of one cannot be written. forme makes
// none; the rule says so if it ever does.
func TestASqueezedUprightRunIsRefused(t *testing.T) {
	for _, tc := range []struct {
		v    layout.DrawText
		want bool
	}{
		{layout.DrawText{Sideways: true, Upright: true, WidthScale: 0.5}, true},
		{layout.DrawText{WidthScale: -1}, true},
		{layout.DrawText{WidthScale: math.NaN()}, true},
		{layout.DrawText{WidthScale: math.Inf(1)}, true},
		{layout.DrawText{WidthScale: 0.5}, false},
		{layout.DrawText{Sideways: true, WidthScale: 0.5}, false},
		{layout.DrawText{}, false},
	} {
		if got := undrawable(tc.v) != ""; got != tc.want {
			t.Errorf("Sideways=%v Upright=%v WidthScale=%v: refused %v, want %v",
				tc.v.Sideways, tc.v.Upright, tc.v.WidthScale, got, tc.want)
		}
	}
}

// rewritten is a document written and read back, which is what a reader has.
func rewritten(t *testing.T, doc *pdf0.Document) *pdf0.Document {
	t.Helper()
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := pdf0.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return back
}
