package render

import (
	"bytes"
	"math"
	"strconv"
	"strings"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/style"
)

// The display list and the PDF it becomes.
//
// Two things are worth testing separately here and are: what the display list
// says, and what the content stream says. They are separated in the engine for
// the reason §7 gives — a layout fault and an emission fault produce pages that
// look like each other's symptom — and testing them together would give that
// separation away.

func renderOf(t *testing.T, htmlSrc string, opts Options, cssSrc ...string) Result {
	t.Helper()
	in := Input{HTML: htmlSrc}
	for _, c := range cssSrc {
		in.CSS = append(in.CSS, Stylesheet{Source: c})
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
	got := renderOf(t, `<h1>A heading</h1><p>Some text in a paragraph.</p>`, Options{})
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
	text := doc.ExtractText()
	for _, want := range []string{"A heading", "Some text in a paragraph."} {
		if !strings.Contains(text, want) {
			t.Errorf("the extracted text does not contain %q; it is %q", want, text)
		}
	}
}

// TestPageGeometry pins the page a document lands on, in points, since that is
// what a PDF records.
func TestPageGeometry(t *testing.T) {
	got := renderOf(t, `<p>x</p>`, Options{Page: A4})
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
	// A4 is 595.276 x 841.89 points, which is the number every printer expects.
	box := mediaBoxOf(t, doc, pages[0])
	if math.Abs(box[2]-595.276) > 0.01 || math.Abs(box[3]-841.89) > 0.01 {
		t.Errorf("the page is %v x %v points, want A4", box[2], box[3])
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
func contentStreamOf(t *testing.T, htmlSrc string, opts Options, cssSrc ...string) string {
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

// TestTheTransformIsWrittenOnce pins the one "cm" this stage exists to emit, and
// every conversion folded into it.
//
// Nothing above pdfout has ever seen PDF's coordinate system, so this matrix is
// the only place the flip happens — and it was entirely untested until a planted
// defect showed that inverting it, dropping the unit conversion, dropping the
// scale and dropping the page margin all left every other test passing.
func TestTheTransformIsWrittenOnce(t *testing.T) {
	stream := contentStreamOf(t, `<div id="a"></div>`, Options{Page: A4},
		noDefaults+"#a { height: 10px }")

	m := firstMatrix(t, stream)
	const pxToPt = 72.0 / 96.0

	// a is the horizontal scale: layout units to points, unscaled content.
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
	wantX := A4.Margin.Left.Pt()
	wantY := A4.Height.Pt() - A4.Margin.Top.Pt()
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
	avail := A4.Content()
	stream := contentStreamOf(t, `<div id="a"></div>`,
		Options{Page: A4, MinScale: 0.1},
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
	stream := contentStreamOf(t, `<p>text</p>`, Options{Page: A4},
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
	got := renderOf(t, `<div id="top"></div><div id="bottom"></div>`, Options{Page: A4},
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

	var top, bottom *FillRect
	for i := range ops {
		if r, ok := ops[i].(FillRect); ok {
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

func paintOf(t *testing.T, htmlSrc, cssSrc string) []Op {
	t.Helper()
	root := layoutOf(t, A4.Content().W.Px(), htmlSrc, cssSrc)
	return Paint(root)
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
		r, ok := op.(FillRect)
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

// TestBackgroundCoversThePaddingBoxNotTheMargin pins where a background stops.
// One that covered the margin would bleed into the gap between two boxes, which
// is the space meant to show the page through.
func TestBackgroundCoversThePaddingBoxNotTheMargin(t *testing.T) {
	ops := paintOf(t, `<div id="a"></div>`,
		noDefaults+`#a { background-color: #ff0000; height: 50px; margin: 20px;
			border-top-style: solid; border-top-width: 5px }`)

	var bg *FillRect
	for i := range ops {
		if r, ok := ops[i].(FillRect); ok && r.Color.R == 255 {
			c := r
			bg = &c
			break
		}
	}
	if bg == nil {
		t.Fatal("the background did not paint")
	}
	// The margin puts the border box at 20, and the border is 5 wide, so the
	// padding box the background covers starts at 25.
	want, _ := style.FromPx(25)
	if bg.Rect.Y != want {
		t.Errorf("the background starts at y=%v, want 25 — 20px of margin then "+
			"5px of border", bg.Rect.Y.Px())
	}
	// And it does not reach into the margin.
	if bg.Rect.X < 20 {
		t.Errorf("the background starts at x=%v, inside the 20px margin", bg.Rect.X.Px())
	}
}

// TestTextPaintsAtItsBaseline pins that a text op carries the baseline rather
// than the top of the line box, which is what a text backend takes.
func TestTextPaintsAtItsBaseline(t *testing.T) {
	ops := paintOf(t, `<p id="p">text</p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica; line-height: 200px }`)

	var text *DrawText
	for i := range ops {
		if d, ok := ops[i].(DrawText); ok {
			c := d
			text = &c
			break
		}
	}
	if text == nil {
		t.Fatal("no text painted")
	}
	if text.Text != "text" {
		t.Errorf("the run reads %q", text.Text)
	}
	// The baseline is inside the line box and below its top, which a value of
	// zero or of the full line height would not be.
	if text.At.Y <= 0 {
		t.Errorf("the baseline is at y=%v", text.At.Y.Px())
	}
	if text.At.Y.Px() >= 200 {
		t.Errorf("the baseline at %v is below the 200px line box", text.At.Y.Px())
	}
}

// TestSpacesArePainted pins that the gap between two words is drawn rather than
// skipped, and the reason is text extraction rather than ink.
//
// A space marks no paper, so skipping it looks free. But the words either side
// then become separate text operations with only a position jump between them,
// and a reader copying the text gets them run together. This was written the
// other way round first, and the end-to-end test caught it: "A heading" came
// back from the finished PDF as "Aheading".
func TestSpacesArePainted(t *testing.T) {
	ops := paintOf(t, `<p>one two three</p>`,
		noDefaults+`p { font-size: 20px; font-family: Helvetica }`)

	var spaces int
	for _, op := range ops {
		if d, ok := op.(DrawText); ok && strings.TrimSpace(d.Text) == "" {
			spaces++
		}
	}
	if spaces != 2 {
		t.Errorf("%d spaces were painted, want the 2 between the three words", spaces)
	}
}

// TestScaleToFit pins §5: one factor, computed from the natural size, applied to
// everything. It is not re-layout — the line breaks do not move — which is what
// makes the threshold checks exact.
func TestScaleToFit(t *testing.T) {
	// Content that fits needs no scaling.
	got := renderOf(t, `<div id="a"></div>`, Options{Page: A4},
		noDefaults+"#a { height: 100px }")
	if got.Scale != 1 {
		t.Errorf("content that fits was scaled by %v", got.Scale)
	}

	// Content that is smaller in *both* axes — and so could be grown — is still
	// left alone. Using a full-width box here would prove nothing, since an auto
	// width already fills the page and no such document can grow.
	got = renderOf(t, `<div id="a"></div>`, Options{Page: A4},
		noDefaults+"html, body { width: 50px } #a { height: 10px }")
	if got.Scale != 1 {
		t.Errorf("content that could have been grown was grown by %v without being asked",
			got.Scale)
	}

	// Content twice as tall as the page is scaled to about half.
	avail := A4.Content()
	tall := avail.H.Px() * 2
	got = renderOf(t, `<div id="a"></div>`, Options{Page: A4, MinScale: 0.1},
		noDefaults+"#a { height: "+ftoa(tall)+"px }")
	if got.Scale >= 1 {
		t.Fatalf("content twice the page height was not scaled: %v", got.Scale)
	}
	if math.Abs(got.Scale-0.5) > 0.02 {
		t.Errorf("the scale is %v, want about 0.5", got.Scale)
	}
	// The natural size is reported unscaled, which is what a caller adjusting a
	// template needs.
	if math.Abs(got.NaturalSize.H.Px()-tall) > 1 {
		t.Errorf("the natural height is %v, want %v", got.NaturalSize.H.Px(), tall)
	}
}

func ftoa(v float64) string {
	n := int(v)
	return itoa(n)
}

// TestScalingUpIsOffByDefault pins that an underfull page is left alone. Growing
// it is surprising and it degrades images, so it is opt-in.
func TestScalingUpIsOffByDefault(t *testing.T) {
	got := renderOf(t, `<div id="a"></div>`, Options{Page: A4},
		noDefaults+"#a { height: 10px }")
	if got.Scale != 1 {
		t.Errorf("a nearly empty page was scaled by %v", got.Scale)
	}

	// Growing needs room in *both* axes. An auto width already fills the page,
	// so only content that is narrower as well as shorter can grow — which is
	// worth stating, because a test using a full-width box would report that
	// scaling up does not work when it is the content that cannot.
	got = renderOf(t, `<div id="a"></div>`, Options{Page: A4, AllowScaleUp: true},
		noDefaults+"html, body { width: 50px } #a { height: 10px }")
	if got.Scale <= 1 {
		t.Errorf("content smaller than the page in both axes was not grown: %v", got.Scale)
	}
}

// TestMinScaleIsAnError pins the blunt guardrail of §6.1, and that it stops the
// document being produced: a page that only fitted by being made illegible is
// one where no document is better than the document.
func TestMinScaleIsAnError(t *testing.T) {
	fired[RuleMinScale] = true

	avail := A4.Content()
	got := renderOf(t, `<div id="a"></div>`, Options{Page: A4},
		noDefaults+"#a { height: "+ftoa(avail.H.Px()*10)+"px }")

	var found *Finding
	for i := range got.Findings {
		if got.Findings[i].Rule == RuleMinScale {
			f := got.Findings[i]
			found = &f
		}
	}
	if found == nil {
		t.Fatalf("content ten times the page height did not trip min-scale: %v", got.Findings)
	}
	if found.Severity != Error {
		t.Errorf("min-scale was reported as %v, want an error", found.Severity)
	}
	if got.Document != nil {
		t.Error("a document was produced despite an error-severity finding")
	}
	// The message says what the scale was and what the floor is, so an author
	// can decide which to change.
	if !strings.Contains(found.Message, "%") {
		t.Errorf("the message %q does not give the numbers", found.Message)
	}

	// Content that fits says nothing.
	got = renderOf(t, `<div id="a"></div>`, Options{Page: A4}, noDefaults+"#a { height: 10px }")
	for _, f := range got.Findings {
		if f.Rule == RuleMinScale {
			t.Errorf("content that fits tripped min-scale: %v", f)
		}
	}
}

// TestMinFontSizeIsAnError pins the other §6.1 threshold, and the property that
// makes it exact: because the scaling is geometric, the effective size is the
// natural size times one number, so this is a multiplication rather than an
// iteration.
func TestMinFontSizeIsAnError(t *testing.T) {
	fired[RuleMinFontSize] = true

	// 4px is 3pt, below the 6pt floor, with no scaling involved.
	got := renderOf(t, `<p>tiny</p>`, Options{Page: A4},
		noDefaults+"p { font-size: 4px; font-family: Helvetica }")

	var found *Finding
	for i := range got.Findings {
		if got.Findings[i].Rule == RuleMinFontSize {
			f := got.Findings[i]
			found = &f
		}
	}
	if found == nil {
		t.Fatalf("3pt text did not trip min-font-size: %v", got.Findings)
	}
	if found.Severity != Error {
		t.Errorf("min-font-size was reported as %v, want an error", found.Severity)
	}
	if got.Document != nil {
		t.Error("a document was produced despite an error-severity finding")
	}

	// Ordinary text says nothing.
	got = renderOf(t, `<p>ordinary</p>`, Options{Page: A4},
		noDefaults+"p { font-size: 16px; font-family: Helvetica }")
	for _, f := range got.Findings {
		if f.Rule == RuleMinFontSize {
			t.Errorf("16px text tripped min-font-size: %v", f)
		}
	}
}

// TestScalingMakesTextTooSmall pins the interaction between the two thresholds,
// which is the case §6.1 is really about: text that is legible on its own
// becomes illegible once the page is shrunk to fit, and the check has to be
// against the *effective* size rather than the declared one.
func TestScalingMakesTextTooSmall(t *testing.T) {
	avail := A4.Content()
	// 10px is 7.5pt, above the floor. Scaled to a fifth it is 1.5pt, well below.
	got := renderOf(t, `<p id="p">text</p><div id="tall"></div>`,
		Options{Page: A4, MinScale: 0.01},
		noDefaults+`p { font-size: 10px; font-family: Helvetica }
		#tall { height: `+ftoa(avail.H.Px()*5)+`px }`)

	var found bool
	for _, f := range got.Findings {
		if f.Rule == RuleMinFontSize {
			found = true
			if !strings.Contains(f.Message, "before the page scaling") {
				t.Errorf("the message %q does not say the size was legible before scaling",
					f.Message)
			}
		}
	}
	if !found {
		t.Errorf("text made illegible by scaling was not reported: %v", got.Findings)
	}
}

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
	pages := []PageSize{A4, A5, Letter, Legal, {}, PageSizePt(10, 10)}
	for _, d := range docs {
		for _, s := range sheets {
			for _, page := range pages {
				in := Input{HTML: d, CSS: []Stylesheet{{Source: s}}}
				got, err := Render(in, Options{Page: page, MinScale: 0.001})
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
		got, err := Render(Input{HTML: src, CSS: []Stylesheet{{Source: sheet}}},
			Options{Page: A4})
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
func sketchOps(ops []Op) string {
	var b strings.Builder
	for _, op := range ops {
		switch v := op.(type) {
		case FillRect:
			b.WriteString("fill " + v.Rect.String() + " " + v.Color.String() + "\n")
		case DrawText:
			b.WriteString("text " + strconv.Quote(v.Text) + " at " +
				strconv.FormatFloat(v.At.X.Px(), 'f', 2, 64) + "," +
				strconv.FormatFloat(v.At.Y.Px(), 'f', 2, 64) + " " +
				v.Face.Name() + " " + strconv.FormatFloat(v.Size.Px(), 'f', 2, 64) +
				" " + v.Color.String() + "\n")
		}
	}
	return b.String()
}

// TestBorderStylesDiffer pins that each border-style paints something different.
//
// Layout only ever asks a border how wide it is, and every style is the same
// width — so a renderer that ignored the style produced a page that was wrong in
// a way an author sees at once and a test suite sees as a hundred failures.
func TestBorderStylesDiffer(t *testing.T) {
	// All four edges, because the 3-D styles differ from solid only in which
	// edges are lit: an "outset" top edge *is* the plain colour, so a test with
	// a top border alone would report outset and solid as the same thing — and
	// be right about it.
	sheet := func(kind string) string {
		return noDefaults + `#a { height: 50px;
			border-top-width: 9px; border-right-width: 9px;
			border-bottom-width: 9px; border-left-width: 9px;
			border-top-color: #808080; border-right-color: #808080;
			border-bottom-color: #808080; border-left-color: #808080;
			border-top-style: ` + kind + `; border-right-style: ` + kind + `;
			border-bottom-style: ` + kind + `; border-left-style: ` + kind + ` }`
	}
	seen := map[string]string{}
	for _, kind := range []string{
		"solid", "double", "dashed", "dotted", "groove", "ridge", "inset", "outset",
	} {
		ops := paintOf(t, `<div id="a"></div>`, sheet(kind))
		got := sketchOps(ops)
		if got == "" {
			t.Errorf("border-style:%s painted nothing", kind)
			continue
		}
		if other, ok := seen[got]; ok {
			t.Errorf("border-style:%s paints exactly what %s does", kind, other)
		}
		seen[got] = kind
	}

	// "none" and "hidden" paint nothing at all, which is the contrast that makes
	// the assertions above about style rather than about painting in general.
	for _, kind := range []string{"none", "hidden"} {
		ops := paintOf(t, `<div id="a"></div>`, sheet(kind))
		for _, op := range ops {
			if r, ok := op.(FillRect); ok && r.Color.R == 128 {
				t.Errorf("border-style:%s painted a border", kind)
			}
		}
	}
}

// TestDoubleBorderIsTwoLines pins the style whose whole point is the gap. One
// band would be a solid border by another name.
func TestDoubleBorderIsTwoLines(t *testing.T) {
	ops := paintOf(t, `<div id="a"></div>`,
		noDefaults+`#a { height: 50px; border-top-width: 9px;
			border-top-color: #808080; border-top-style: double }`)

	var bands []Rect
	for _, op := range ops {
		if r, ok := op.(FillRect); ok && r.Color.R == 128 {
			bands = append(bands, r.Rect)
		}
	}
	if len(bands) != 2 {
		t.Fatalf("a double border painted %d bands, want 2", len(bands))
	}
	// Each is a third of the width, and there is a third between them.
	px(t, "the first band", bands[0].H, 3)
	px(t, "the second band", bands[1].H, 3)
	px(t, "the gap", bands[1].Y.Sub(bands[0].Bottom()), 3)
}

// TestDashedAndDottedAreRuns pins that these paint many marks rather than one,
// and that a dot is shorter than a dash — the ratio is left open by the
// specification and the difference is not.
func TestDashedAndDottedAreRuns(t *testing.T) {
	count := func(kind string) (marks int, markLen float64) {
		ops := paintOf(t, `<div id="a"></div>`,
			noDefaults+`#a { height: 50px; border-top-width: 4px;
				border-top-color: #808080; border-top-style: `+kind+` }`)
		for _, op := range ops {
			if r, ok := op.(FillRect); ok && r.Color.R == 128 {
				marks++
				markLen = r.Rect.W.Px()
			}
		}
		return
	}
	dashes, dashLen := count("dashed")
	dots, dotLen := count("dotted")

	if dashes < 5 {
		t.Errorf("a dashed border painted %d marks, want a run of them", dashes)
	}
	if dots <= dashes {
		t.Errorf("dotted painted %d marks and dashed %d; a dot is shorter so there "+
			"are more of them", dots, dashes)
	}
	if dotLen >= dashLen {
		t.Errorf("a dot is %v wide and a dash %v; a dot is the shorter", dotLen, dashLen)
	}
}

// TestThreeDBordersUseTwoTones pins that groove, ridge, inset and outset light
// some edges and shadow others — which is the whole of what makes them look
// three-dimensional, and what a single-tone renderer loses.
func TestThreeDBordersUseTwoTones(t *testing.T) {
	for _, kind := range []string{"groove", "ridge", "inset", "outset"} {
		ops := paintOf(t, `<div id="a"></div>`,
			noDefaults+`#a { height: 50px;
				border-top-width: 8px; border-right-width: 8px;
				border-bottom-width: 8px; border-left-width: 8px;
				border-top-color: #808080; border-right-color: #808080;
				border-bottom-color: #808080; border-left-color: #808080;
				border-top-style: `+kind+`; border-right-style: `+kind+`;
				border-bottom-style: `+kind+`; border-left-style: `+kind+` }`)

		tones := map[float64]bool{}
		for _, op := range ops {
			if r, ok := op.(FillRect); ok {
				tones[r.Color.R] = true
			}
		}
		if len(tones) < 2 {
			t.Errorf("border-style:%s used %d tone(s), want two", kind, len(tones))
		}
	}
}

// TestBlackThreeDBorderStaysVisible pins the case a naive darkening loses. Half
// of black is black, so a groove on the colour authors use most would vanish
// into one tone; the second tone is a lightening instead.
func TestBlackThreeDBorderStaysVisible(t *testing.T) {
	black := style.RGBA{A: 1}
	if shade(black, 0.5) == black {
		t.Error("a black border's second tone is also black, so the style disappears")
	}
	// An ordinary colour does darken, so the special case is only for black.
	grey := style.RGBA{R: 200, G: 200, B: 200, A: 1}
	if got := shade(grey, 0.5); got.R >= grey.R {
		t.Errorf("shading grey gave %v, which is no darker", got)
	}
}
