package htmlpdf

import (
	"bytes"
	"github.com/mgilbir/forme/layout"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/forme/style"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
)

// The display list and the PDF it becomes.
//
// Two things are worth testing separately here and are: what the display list
// says, and what the content stream says. They are separated in the engine for
// the reason §7 gives — a layout.layout fault and an emission fault produce pages that
// look like each other's symptom — and testing them together would give that
// separation away.

func renderOf(t *testing.T, htmlSrc string, opts layout.Options, cssSrc ...string) Result {
	t.Helper()
	in := layout.Input{HTML: htmlSrc}
	for _, c := range cssSrc {
		in.CSS = append(in.CSS, layout.Stylesheet{Source: c})
	}
	got, err := Render(in, opts)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	return got
}

// TestRenderProducesAReadablePDF is the end-to-end check: a document goes in and
// a PDF that pdf0 itself can read comes out.
//
// It is a self-check and labelled as one — §7.2 is explicit that validating our
// output with our own reader is not an oracle. What it does catch is the whole
// class of emission faults that make a file unopenable, which is worth having
// before anything subtler.
func TestRenderProducesAReadablePDF(t *testing.T) {
	got := renderOf(t, `<h1>A heading</h1><p>Some text in a paragraph.</p>`, layout.Options{})
	if got.Document == nil {
		t.Fatalf("no document was produced: %v", got.Findings)
	}

	var buf bytes.Buffer
	if err := got.Document.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("the document wrote no bytes")
	}

	doc, err := pdf0.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("the document pdf0 wrote, pdf0 cannot read: %v", err)
	}
	pages := doc.PageList()
	if len(pages) != 1 {
		t.Errorf("the document has %d pages, want 1", len(pages))
	}

	// The text reached the page, which is the whole point.
	text := mustExtractText(t, doc)
	for _, want := range []string{"A heading", "Some text in a paragraph."} {
		if !strings.Contains(text, want) {
			t.Errorf("the extracted text does not contain %q; it is %q", want, text)
		}
	}
}

// TestPageGeometry pins the page a document lands on, in points, since that is
// what a PDF records.
func TestPageGeometry(t *testing.T) {
	got := renderOf(t, `<p>x</p>`, layout.Options{Page: layout.A4})
	if got.Document == nil {
		t.Fatalf("no document: %v", got.Findings)
	}

	var buf bytes.Buffer
	if err := got.Document.Write(&buf); err != nil {
		t.Fatal(err)
	}
	doc, err := pdf0.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	pages := doc.PageList()
	if len(pages) != 1 {
		t.Fatalf("got %d pages", len(pages))
	}
	// layout.A4 is 595.276 x 841.89 points, which is the number every printer expects.
	box := mediaBoxOf(t, doc, pages[0])
	if math.Abs(box[2]-595.276) > 0.01 || math.Abs(box[3]-841.89) > 0.01 {
		t.Errorf("the page is %v x %v points, want layout.A4", box[2], box[3])
	}
}

// mediaBoxOf reads a page's /MediaBox, resolving it through the document.
func mediaBoxOf(t *testing.T, doc *pdf0.Document, page *object.Dictionary) [4]float64 {
	t.Helper()
	arr, ok := doc.Resolve(page.Get("MediaBox")).(object.Array)
	if !ok || len(arr) != 4 {
		t.Fatalf("the page has no usable /MediaBox: %v", page.Get("MediaBox"))
	}
	var out [4]float64
	for i, v := range arr {
		switch n := doc.Resolve(v).(type) {
		case object.Integer:
			out[i] = float64(n)
		case object.Real:
			out[i] = float64(n)
		default:
			t.Fatalf("/MediaBox[%d] is %T", i, n)
		}
	}
	return out
}

// contentStreamOf renders a document and returns the bytes of its page's content
// stream, which is the only place the coordinate transform is visible.
func contentStreamOf(t *testing.T, htmlSrc string, opts layout.Options, cssSrc ...string) string {
	t.Helper()
	got := renderOf(t, htmlSrc, opts, cssSrc...)
	if got.Document == nil {
		t.Fatalf("no document: %v", got.Findings)
	}
	var buf bytes.Buffer
	if err := got.Document.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	doc, err := pdf0.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	pages := doc.PageList()
	if len(pages) != 1 {
		t.Fatalf("got %d pages", len(pages))
	}
	stream, ok := doc.Resolve(pages[0].Get("Contents")).(*object.Stream)
	if !ok {
		t.Fatalf("the page's /Contents is not a stream: %T", pages[0].Get("Contents"))
	}
	data, err := doc.StreamData(stream)
	if err != nil {
		t.Fatalf("decoding the content stream: %v", err)
	}
	return string(data)
}

// firstMatrix returns the operands of the first "cm" in a content stream.
// TestTheTransformIsWrittenOnce pins the one "cm" this stage exists to emit, and
// every conversion folded into it.
//
// Nothing above pdfout has ever seen PDF's coordinate system, so this matrix is
// the only place the flip happens — and it was entirely untested until a planted
// defect showed that inverting it, dropping the unit conversion, dropping the
// scale and dropping the page margin all left every other test passing.
func TestTheTransformIsWrittenOnce(t *testing.T) {
	stream := contentStreamOf(t, `<div id="a"></div>`, layout.Options{Page: layout.A4},
		noDefaults+"#a { height: 10px }")

	m := firstMatrix(t, stream)
	const pxToPt = 72.0 / 96.0

	// a is the horizontal scale: layout.layout units to points, unscaled content.
	if math.Abs(m[0]-pxToPt) > 1e-9 {
		t.Errorf("the horizontal scale is %v, want %v — a CSS pixel is 1/96 inch "+
			"and a point is 1/72", m[0], pxToPt)
	}
	// d is negative: that inversion *is* the difference between the two
	// coordinate systems, and a positive one draws the page upside down.
	if m[3] >= 0 {
		t.Errorf("the vertical scale is %v; it must be negative to flip CSS's "+
			"downwards y into PDF's upwards y", m[3])
	}
	if math.Abs(m[3]+pxToPt) > 1e-9 {
		t.Errorf("the vertical scale is %v, want %v", m[3], -pxToPt)
	}
	// There is no skew.
	if m[1] != 0 || m[2] != 0 {
		t.Errorf("the transform skews: b=%v c=%v", m[1], m[2])
	}
	// The origin moves to the top left of the content box: in from the left
	// margin, and down from the top of the sheet by the top margin.
	wantX := layout.A4.Margin.Left.Pt()
	wantY := layout.A4.Height.Pt() - layout.A4.Margin.Top.Pt()
	if math.Abs(m[4]-wantX) > 0.01 {
		t.Errorf("the origin is at x=%v, want the left margin %v", m[4], wantX)
	}
	if math.Abs(m[5]-wantY) > 0.01 {
		t.Errorf("the origin is at y=%v, want %v — the top of the sheet less the "+
			"top margin, since y now runs downwards", m[5], wantY)
	}
}

// TestTheTransformCarriesTheScale pins that §5's factor is in the matrix rather
// than applied to the geometry. One "cm" is what keeps the output vector: the
// text stays selectable and no image is resampled.
func TestTheTransformCarriesTheScale(t *testing.T) {
	avail := layout.A4.Content()
	stream := contentStreamOf(t, `<div id="a"></div>`,
		layout.Options{Page: layout.A4, MinScale: 0.1},
		noDefaults+"#a { height: "+ftoa(avail.H.Px()*2)+"px }")

	m := firstMatrix(t, stream)
	const pxToPt = 72.0 / 96.0
	// The content is about twice the page height, so the factor is about a half.
	got := m[0] / pxToPt
	if math.Abs(got-0.5) > 0.03 {
		t.Errorf("the transform carries a scale of %v, want about 0.5", got)
	}
	if math.Abs(m[3]/-pxToPt-got) > 1e-9 {
		t.Errorf("the two axes are scaled differently: %v and %v", m[0], m[3])
	}
}

// TestTextIsNotMirrored pins the consequence of the flip that is easiest to
// miss. The transform inverts the y axis, so text drawn through it would come
// out mirrored; the text matrix inverts it again locally, which leaves the
// glyphs upright while the position still comes from the flipped system.
func TestTextIsNotMirrored(t *testing.T) {
	stream := contentStreamOf(t, `<p>text</p>`, layout.Options{Page: layout.A4},
		noDefaults+"p { font-size: 20px; font-family: Helvetica }")

	var found bool
	for _, line := range strings.Split(stream, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 7 || fields[6] != "Tm" {
			continue
		}
		found = true
		d, err := strconv.ParseFloat(fields[3], 64)
		if err != nil {
			t.Fatalf("the text matrix has a non-numeric operand %q", fields[3])
		}
		if d >= 0 {
			t.Errorf("the text matrix has d=%v; it must be negative to undo the "+
				"page transform's flip, or the glyphs come out mirrored", d)
		}
	}
	if !found {
		t.Fatalf("no text matrix was written:\n%s", stream)
	}
}

// TestCoordinatesFlipExactlyOnce pins the conversion this stage exists for. CSS
// puts the origin at the top left with y downwards and PDF at the bottom left
// with y upwards, so a box near the top of the document must be near the *top*
// of the page — which is a large y in PDF coordinates.
//
// Getting this wrong produces a document that is upside down, which is obvious,
// or one that is off by the page height, which is not.
func TestCoordinatesFlipExactlyOnce(t *testing.T) {
	// Two boxes, one at the top and one far below it.
	got := renderOf(t, `<div id="top"></div><div id="bottom"></div>`, layout.Options{Page: layout.A4},
		noDefaults+"#top { height: 20px } #bottom { height: 20px; margin-top: 400px }")
	if got.Document == nil {
		t.Fatalf("no document: %v", got.Findings)
	}
	if got.Scale != 1 {
		t.Fatalf("the content was scaled by %v; this test needs it unscaled", got.Scale)
	}

	ops := paintOf(t, `<div id="top"></div><div id="bottom"></div>`,
		noDefaults+`#top { height: 20px; background-color: red }
		#bottom { height: 20px; margin-top: 400px; background-color: blue }`)

	var top, bottom *layout.FillRect
	for i := range ops {
		if r, ok := ops[i].(layout.FillRect); ok {
			c := r
			if c.Color.R > 200 {
				top = &c
			} else if c.Color.B > 200 {
				bottom = &c
			}
		}
	}
	if top == nil || bottom == nil {
		t.Fatalf("the two boxes did not paint: %d ops", len(ops))
	}
	// In the display list, y still increases downwards, so the second box has
	// the larger y.
	if !(top.Rect.Y < bottom.Rect.Y) {
		t.Errorf("in the display list the boxes are at y=%v and y=%v; y increases downwards",
			top.Rect.Y.Px(), bottom.Rect.Y.Px())
	}
}

// TestBackgroundsAndBordersPaint pins that a box's decorations reach the display
// list, in the order the specification puts them: the background under the
// border, and both under the content.
func TestBackgroundsAndBordersPaint(t *testing.T) {
	ops := paintOf(t, `<div id="a">text</div>`,
		noDefaults+`#a { background-color: #ff0000; height: 50px;
			border-top-style: solid; border-top-width: 10px;
			border-top-color: #0000ff }`)

	var sawBackground, sawBorder int
	for i, op := range ops {
		r, ok := op.(layout.FillRect)
		if !ok {
			continue
		}
		switch {
		case r.Color.R == 255 && r.Color.B == 0:
			sawBackground = i + 1
		case r.Color.B == 255 && r.Color.R == 0:
			sawBorder = i + 1
		}
	}
	if sawBackground == 0 {
		t.Error("the background did not paint")
	}
	if sawBorder == 0 {
		t.Error("the border did not paint")
	}
	if sawBackground > sawBorder {
		t.Error("the background painted after the border; it belongs underneath")
	}
}

// TestBackgroundCoversTheBorderBoxNotTheMargin pins where a background stops.
//
// It runs *under* the border and stops at the border box, which is
// background-clip's initial value and is why a dashed border shows the
// background through its gaps rather than the page. It never reaches the margin,
// which is the space meant to show the page through.
//
// This test asserted the padding box until background-clip was implemented, and
// the engine agreed with it. Both were wrong: CSS 2.1 §14.2 says the background
// covers "the content, padding and border areas", and the two only look alike
// while every border is opaque and solid.
// TestScaleToFit pins §5: one factor, computed from the natural size, applied to
// everything. It is not re-layout.layout — the line breaks do not move — which is what
// makes the threshold checks exact.
// TestRenderIsTotal pins that no document and no options panic it.
// TestRenderIsTotal pins that no document and no options panic it.
func TestRenderIsTotal(t *testing.T) {
	docs := []string{
		"", "<p>x</p>", "<div><p>a</p>b</div>", "<h1>t</h1><ul><li>a<li>b</ul>",
		"<p>" + strings.Repeat("word ", 500) + "</p>",
		"<p>日本語</p>", "<p>مرحبا</p>",
	}
	sheets := []string{
		"", "* { display: none }", "p { font-size: 1px }",
		"p { font-size: 1000px }", "* { margin: 100px }",
	}
	pages := []layout.PageSize{layout.A4, layout.A5, layout.Letter, layout.Legal, {}, layout.PageSizePt(10, 10)}
	for _, d := range docs {
		for _, s := range sheets {
			for _, page := range pages {
				in := layout.Input{HTML: d, CSS: []layout.Stylesheet{{Source: s}}}
				got, err := Render(in, layout.Options{Page: page, MinScale: 0.001})
				if err != nil {
					continue
				}
				if got.Document != nil {
					var buf bytes.Buffer
					_ = got.Document.Write(&buf)
				}
			}
		}
	}
}

// TestRenderIsDeterministic pins that two renders of one document agree. It is
// the property §7's comparison testing needs, and the one that map iteration
// quietly breaks.
//
// The comparison is of the *display list* rather than of the file, and that is
// not a weaker claim — it is the right one. A PDF carries an /ID, which is a
// unique identifier for the file and is deliberately different every time; two
// byte-identical files would mean that identifier was not doing its job. The
// display list is the stage §7 attaches at, and it is what has to be
// reproducible.
func TestRenderIsDeterministic(t *testing.T) {
	const src = `<h1>Title</h1><p>Some <em>text</em> with <strong>emphasis</strong>.</p>
		<ul><li>one<li>two</ul>`
	const sheet = "h1 { color: #123456 } p { background-color: #eee }"

	first := sketchOps(paintOf(t, src, sheet))
	for i := 0; i < 20; i++ {
		if again := sketchOps(paintOf(t, src, sheet)); again != first {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, again)
		}
	}

	// And the file is reproducible apart from that identifier, which is checked
	// by rendering twice and comparing the lengths — a difference in content
	// would move them.
	render := func() int {
		got, err := Render(layout.Input{HTML: src, CSS: []layout.Stylesheet{{Source: sheet}}},
			layout.Options{Page: layout.A4})
		if err != nil || got.Document == nil {
			t.Fatalf("rendering: %v %v", err, got.Findings)
		}
		var buf bytes.Buffer
		if err := got.Document.Write(&buf); err != nil {
			t.Fatalf("writing: %v", err)
		}
		return buf.Len()
	}
	n := render()
	for i := 0; i < 5; i++ {
		if again := render(); again != n {
			t.Errorf("run %d wrote %d bytes where the first wrote %d", i, again, n)
		}
	}
}

// sketchOps renders a display list as text, so a difference names itself.

// find is the fragment for an element, which every geometric assertion here
// starts from.
//
// A copy of the layout engine's own helper, and deliberately a copy: it is
// eight lines, and a test helper exported across a module boundary is API this
// module would then be holding forme to.
func find(t *testing.T, root *layout.Fragment, id string) *layout.Fragment {
	t.Helper()
	var found *layout.Fragment
	var walk func(*layout.Fragment)
	walk = func(f *layout.Fragment) {
		if found != nil || f == nil {
			return
		}
		if f.Box.Element != nil {
			if got, _ := f.Box.Element.Attr("id"); got == id {
				found = f
				return
			}
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("no fragment for #%s", id)
	}
	return found
}

// noDefaults suppresses the user-agent margins, whose arithmetic would
// otherwise be in every expected value here.
const noDefaults = `
html, body, div, p, section { margin: 0; padding: 0; border-top-style: none;
  border-right-style: none; border-bottom-style: none; border-left-style: none }
`

// layoutOf builds and lays out a document in a page of the given width, for the
// tests here that need a fragment tree rather than a finished document.
func layoutOf(t *testing.T, width float64, htmlSrc string, cssSrc ...string) *layout.Fragment {
	t.Helper()
	in := layout.Input{HTML: htmlSrc}
	for _, c := range cssSrc {
		in.CSS = append(in.CSS, layout.Stylesheet{Source: c})
	}
	got := layout.Build(in)
	if got.Root == nil {
		t.Fatalf("the document produced no boxes")
	}
	w, _ := style.FromPx(width)
	h, _ := style.FromPx(10000)
	frag := layout.Layout(got.Root, layout.Size{W: w, H: h}, nil, layout.NewRecorder(nil))
	if frag == nil {
		t.Fatal("layout produced no fragment")
	}
	return frag
}

// lineX returns the x of the first run of the first line of an element.
func lineX(t *testing.T, root *layout.Fragment, id string) float64 {
	t.Helper()
	f := find(t, root, id)
	if len(f.Lines) == 0 || len(f.Lines[0].Runs) == 0 {
		t.Fatalf("#%s has no line runs to align", id)
	}
	return f.Lines[0].Runs[0].X.Px()
}

// writePNG puts a solid blue PNG of the given size on disk.
func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.WriteFile(path, encodePNG(t, w, h), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeImage(t *testing.T, path string, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// firstMatrix returns the operands of the first "cm" in a content stream.
func firstMatrix(t *testing.T, stream string) [6]float64 {
	t.Helper()
	for _, line := range strings.Split(stream, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 7 || fields[6] != "cm" {
			continue
		}
		var out [6]float64
		for i := 0; i < 6; i++ {
			v, err := strconv.ParseFloat(fields[i], 64)
			if err != nil {
				t.Fatalf("the transform has a non-numeric operand %q", fields[i])
			}
			out[i] = v
		}
		return out
	}
	t.Fatalf("the content stream has no transform:\n%s", stream)
	return [6]float64{}
}

func ftoa(v float64) string {
	n := int(v)
	return itoa(n)
}

func paintOf(t *testing.T, htmlSrc, cssSrc string) []layout.Op {
	t.Helper()
	root := layoutOf(t, layout.A4.Content().W.Px(), htmlSrc, cssSrc)
	return layout.Paint(root)
}

// sketchOps renders a display list as text, so a difference names itself.
func sketchOps(ops []layout.Op) string {
	var b strings.Builder
	for _, op := range ops {
		switch v := op.(type) {
		case layout.FillRect:
			b.WriteString("fill " + v.Rect.String() + " " + v.Color.String() + "\n")
		case layout.DrawText:
			b.WriteString("text " + strconv.Quote(v.Text) + " at " +
				strconv.FormatFloat(v.At.X.Px(), 'f', 2, 64) + "," +
				strconv.FormatFloat(v.At.Y.Px(), 'f', 2, 64) + " " +
				v.Face.Name() + " " + strconv.FormatFloat(v.Size.Px(), 'f', 2, 64) +
				" " + v.Color.String() + "\n")
		}
	}
	return b.String()
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{B: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestHTMLAndCSSAndAPageSizeMakeAPDF is the whole of what this repository and
// forme were built to do, asserted in one place.
//
// The three inputs are a document, a stylesheet and a sheet of paper; the
// output is a PDF. Every other test here is about one step of that, and each
// would still pass if the steps had stopped fitting together — the layout
// engine is a separate module now, and nothing else checks that the two ends of
// the seam still meet. examples/html_to_pdf is the same path with prose around
// it, and CI runs it.
func TestHTMLAndCSSAndAPageSizeMakeAPDF(t *testing.T) {
	// A5 in points, with a 15mm margin: not a named size, so the numbers have
	// to survive the whole way rather than being matched against a constant.
	const wantW, wantH = 419.53, 595.28
	page := layout.PageSizePt(wantW, wantH).WithMarginPt(42.52)

	got, err := Render(layout.Input{
		HTML: `<h1>Aurora</h1>
		       <table><tr><td>Grating</td><td class="n">445.50</td></tr></table>
		       <p class="note">Payment within 30 days.</p>`,
		CSS: []layout.Stylesheet{{Source: `
			body { font-family: Helvetica; font-size: 11pt }
			h1 { font-size: 20pt; border-bottom: 2pt solid #b8860b }
			table { width: 100%; border-collapse: collapse }
			td { border-bottom: 0.5pt solid #cccccc }
			.n { text-align: right }
			.note { font-size: 9pt; color: #666666 }`}},
	}, layout.Options{Page: page})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if got.Document == nil {
		t.Fatalf("no document was produced: %v", got.Findings)
	}
	if len(got.Findings) != 0 {
		t.Errorf("an ordinary document raised %v", got.Findings)
	}
	if got.Scale != 1 {
		t.Errorf("the content was scaled to %v; it fits at full size", got.Scale)
	}

	var buf bytes.Buffer
	if err := got.Document.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	doc, err := pdf0.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("the document pdf0 wrote, pdf0 cannot read: %v", err)
	}

	pages := doc.PageList()
	if len(pages) != 1 {
		t.Fatalf("the document has %d pages, want 1", len(pages))
	}
	// The sheet the caller asked for, in the units a PDF records.
	box := mediaBoxOf(t, doc, pages[0])
	if math.Abs(box[2]-wantW) > 0.01 || math.Abs(box[3]-wantH) > 0.01 {
		t.Errorf("the page is %v x %v pt, want %v x %v", box[2], box[3], wantW, wantH)
	}

	// The text arrived, in reading order.
	text := mustExtractText(t, doc)
	for _, want := range []string{"Aurora", "Grating", "445.50", "Payment within 30 days."} {
		if !strings.Contains(text, want) {
			t.Errorf("the extracted text is missing %q; it is %q", want, text)
		}
	}

	// And the stylesheet was obeyed, which is the half a text extraction cannot
	// show. It is asked of the *content stream of this document* and not of a
	// second layout run: the point of the test is that the two ends of the seam
	// meet, and a render that dropped the stylesheets on the way in would still
	// have satisfied a separately composed control. That is not hypothetical —
	// it is what the first version of this test did, and planting the fault
	// found it green.
	stream := contentStreamOf(t, `<h1>Aurora</h1><p class="note">Payment within 30 days.</p>`,
		layout.Options{Page: page}, `
			body { font-family: Helvetica; font-size: 11pt }
			h1 { font-size: 20pt; border-bottom: 2pt solid #b8860b }
			.note { font-size: 9pt }`)

	// The heading's rule, in the colour the stylesheet names. The operands are
	// parsed rather than matched as text: a PDF states them at full float
	// precision, and pinning that spelling would be a test of strconv.
	if !hasFill(stream, 0xb8/255.0, 0x86/255.0, 0x0b/255.0) {
		t.Errorf("no fill in #b8860b — the heading's border-bottom was not painted "+
			"in the colour the stylesheet gave it:\n%s", stream)
	}
	// The two sizes, which only the stylesheet asks for. A PDF states them on
	// Tf in the units the text matrix is in, which here is CSS pixels: 20pt is
	// 26.67 of them and 9pt is 12.
	sizes := fontSizes(stream)
	if !hasSize(sizes, 20*4/3.0) || !hasSize(sizes, 9*4/3.0) {
		t.Errorf("the text is set at %v; the stylesheet asks for a 20pt heading "+
			"and a 9pt note, which is %.2f and %.2f in the page's units",
			sizes, 20*4/3.0, 9*4/3.0)
	}
}

// hasFill reports whether the content stream sets a non-stroking colour close
// to the one given. Close, because a channel is written as a float.
func hasFill(stream string, r, g, b float64) bool {
	for _, line := range strings.Split(stream, "\n") {
		f := strings.Fields(line)
		if len(f) != 4 || f[3] != "rg" {
			continue
		}
		var got [3]float64
		ok := true
		for i := range got {
			v, err := strconv.ParseFloat(f[i], 64)
			if err != nil {
				ok = false
				break
			}
			got[i] = v
		}
		if ok && math.Abs(got[0]-r) < 0.002 && math.Abs(got[1]-g) < 0.002 &&
			math.Abs(got[2]-b) < 0.002 {
			return true
		}
	}
	return false
}

// fontSizes is every size the content stream selects with Tf.
func fontSizes(stream string) []float64 {
	var out []float64
	for _, line := range strings.Split(stream, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[2] != "Tf" {
			continue
		}
		if v, err := strconv.ParseFloat(f[1], 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func hasSize(sizes []float64, want float64) bool {
	for _, s := range sizes {
		if math.Abs(s-want) < 0.05 {
			return true
		}
	}
	return false
}

// mustExtractText is ExtractText for a test that expects every page's text:
// an error fails the test.
func mustExtractText(t testing.TB, d *pdf0.Document) string {
	t.Helper()
	text, err := d.ExtractText()
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	return text
}
