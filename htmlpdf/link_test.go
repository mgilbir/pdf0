package htmlpdf

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
)

// Links: forme puts each <a href> in the display list as a Link, with one
// area per fragment of the <a>, and the backend writes each area as a link
// annotation through pdf0's own builder (Page.Links, pdf0.LinkURI).
//
// The oracle for where an annotation goes is forme's own Link: the test
// composes the same input, finds the Link, and puts its areas through the
// page transform (CSS px to points, y flipped, scaled, inside the margin),
// written out here again. The oracle for what it says is pdf0.LinkURI, the
// rule every link a page carries goes through.

// annotation is one link annotation as a reader finds it on the page.
type annotation struct {
	rect [4]float64
	uri  string
}

// pageLinks reads the link annotations of a document's one page, in order.
func pageLinks(t *testing.T, doc *pdf0.Document) []annotation {
	t.Helper()
	pg := doc.PageList()[0]
	annots, _ := doc.Resolve(pg.Get("Annots")).(object.Array)
	var out []annotation
	for _, a := range annots {
		d := doc.ResolveDict(a)
		if d == nil {
			t.Fatalf("an /Annots entry is %T, not a dictionary", a)
		}
		if st, _ := d.Get("Subtype").(object.Name); st != "Link" {
			t.Fatalf("an annotation of subtype %v", d.Get("Subtype"))
		}
		var an annotation
		rect, _ := doc.Resolve(d.Get("Rect")).(object.Array)
		if len(rect) != 4 {
			t.Fatalf("/Rect is %v", d.Get("Rect"))
		}
		for i, v := range rect {
			switch n := doc.Resolve(v).(type) {
			case object.Integer:
				an.rect[i] = float64(n)
			case object.Real:
				an.rect[i] = float64(n)
			default:
				t.Fatalf("/Rect entry %d is %T", i, v)
			}
		}
		action := doc.ResolveDict(d.Get("A"))
		if action == nil {
			t.Fatalf("a link annotation with no action: %v", d)
		}
		if s, _ := action.Get("S").(object.Name); s != "URI" {
			t.Fatalf("the action is /%s, want /URI", s)
		}
		uri, _ := doc.Resolve(action.Get("URI")).(object.String)
		an.uri = string(uri.Value)
		out = append(out, an)
	}
	return out
}

// expectedLinks is what the page should carry for an input: every area of
// every Link forme lays out, through the page transform, with the URI
// pdf0.LinkURI makes of its href.
func expectedLinks(t *testing.T, in Input, opts Options) []annotation {
	t.Helper()
	c := layout.Compose(in, opts)
	const pxToPt = 72.0 / 96.0
	k := pxToPt * c.Scale
	tx := c.Page.Margin.Left.Pt()
	ty := c.Page.Height.Pt() - c.Page.Margin.Top.Pt()
	var out []annotation
	for _, op := range c.Ops {
		l, ok := op.(layout.Link)
		if !ok {
			continue
		}
		uri, err := pdf0.LinkURI(l.Href)
		if err != nil {
			t.Fatalf("the fixture's link %q is one pdf0 refuses: %v", l.Href, err)
		}
		for _, r := range l.Rects {
			x, y, w, h := r.X.Px(), r.Y.Px(), r.W.Px(), r.H.Px()
			out = append(out, annotation{
				rect: [4]float64{tx + k*x, ty - k*(y+h), tx + k*(x+w), ty - k*y},
				uri:  uri,
			})
		}
	}
	return out
}

func sameLinks(t *testing.T, got, want []annotation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("the page has %d link annotations, want %d:\n got %v\nwant %v", len(got), len(want), got, want)
	}
	// The builder writes a coordinate as the content builder writes a number,
	// to a few decimal places; a thousandth of a point is well inside that.
	const eps = 1e-3
	for i := range want {
		for j := range 4 {
			if math.Abs(got[i].rect[j]-want[i].rect[j]) > eps {
				t.Errorf("annotation %d: /Rect %v, want %v", i, got[i].rect, want[i].rect)
				break
			}
		}
		if got[i].uri != want[i].uri {
			t.Errorf("annotation %d: /URI %q, want %q", i, got[i].uri, want[i].uri)
		}
	}
}

// TestALinkIsALinkAnnotation: one line, one area, one annotation, over the
// text the <a> holds, going where the href says, as the builder spells it.
func TestALinkIsALinkAnnotation(t *testing.T) {
	in := Input{HTML: `<p>see <a href="https://example.com/a b?q=ü">this link</a> here</p>`}
	doc, _ := roundTrip(t, in, Options{})
	got := pageLinks(t, doc)
	want := expectedLinks(t, in, Options{})
	if len(want) != 1 {
		t.Fatalf("forme laid out %d link areas for one line; the fixture is wrong", len(want))
	}
	sameLinks(t, got, want)
	// The builder's spelling: 7-bit ASCII, the space and the UTF-8 of ü
	// percent-encoded (ISO 32000-2 12.6.4.8).
	if want := "https://example.com/a%20b?q=%C3%BC"; got[0].uri != want {
		t.Errorf("/URI %q, want %q", got[0].uri, want)
	}
	// And the area is where the words are: the run "this link" starts
	// inside it and is set on a baseline inside it.
	at := textOrigin(t, in, "this")
	r := got[0].rect
	if at[0] < r[0] || at[0] > r[2] || at[1] < r[1] || at[1] > r[3] {
		t.Errorf("the words start at %v, outside the link's area %v", at, r)
	}
	if got := strings.TrimSpace(mustExtractText(t, doc)); got != "see this link here" {
		t.Errorf("extracted %q", got)
	}
}

// textOrigin is where, in page space, the run whose text begins with prefix
// starts: its pen position through the page transform.
func textOrigin(t *testing.T, in Input, prefix string) [2]float64 {
	t.Helper()
	c := layout.Compose(in, Options{})
	k := 72.0 / 96.0 * c.Scale
	tx := c.Page.Margin.Left.Pt()
	ty := c.Page.Height.Pt() - c.Page.Margin.Top.Pt()
	for _, op := range c.Ops {
		if d, ok := op.(layout.DrawText); ok && strings.HasPrefix(strings.TrimSpace(d.Text), prefix) {
			return [2]float64{tx + k*d.At.X.Px(), ty - k*d.At.Y.Px()}
		}
	}
	t.Fatalf("no run begins with %q", prefix)
	return [2]float64{}
}

// TestALinkBrokenAcrossLinesIsOneAnnotationPerLine: a link that spans lines
// is an annotation for each line's area, each going to the same place.
//
// One annotation per area rather than one with /QuadPoints: a processor that
// does not read /QuadPoints (it is PDF 1.6, and optional to honour) activates
// the whole /Rect, which for a link broken across lines is a box covering the
// middle of every line between — other words, and other links. Separate
// annotations are exact in every reader.
func TestALinkBrokenAcrossLinesIsOneAnnotationPerLine(t *testing.T) {
	in := Input{
		HTML: `<p>A <a href="mailto:someone@example.com">link long enough to be broken ` +
			`over three lines of this narrow paragraph</a> ends.</p>`,
		CSS: []Stylesheet{{Source: `p { width: 120px }`}},
	}
	doc, _ := roundTrip(t, in, Options{})
	want := expectedLinks(t, in, Options{})
	if len(want) < 3 {
		t.Fatalf("forme laid the link out over %d areas; the fixture wants three or more", len(want))
	}
	got := pageLinks(t, doc)
	sameLinks(t, got, want)
	// Each line's area is below the last.
	for i := 1; i < len(got); i++ {
		if !(got[i].rect[3] <= got[i-1].rect[1]+1e-6) {
			t.Errorf("area %d %v is not below area %d %v", i, got[i].rect, i-1, got[i-1].rect)
		}
	}
}

// TestALinkIsPlacedThroughTheScaleAndTheMargin: a page that had to be
// shrunk to fit, with a margin, still puts the annotation over the words.
func TestALinkIsPlacedThroughTheScaleAndTheMargin(t *testing.T) {
	in := Input{
		HTML: `<div style="width: 380px"><a href="https://example.com/">wide</a></div>`,
		CSS:  []Stylesheet{{Source: `@page { size: 200pt 200pt; margin: 20pt 10pt }`}},
	}
	out, err := Render(in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !(out.Scale < 1) {
		t.Fatalf("the fixture was not scaled (%v); it tests nothing about the scale", out.Scale)
	}
	doc := reread(t, out.Document)
	sameLinks(t, pageLinks(t, doc), expectedLinks(t, in, Options{}))
}

// TestALinkThatCannotBeWrittenIsRefused: an href forme lets through that a
// PDF link cannot carry refuses the document under RuleLinkDropped, and a
// caller who lowers the rule gets the page without that link and with the
// finding. A reference with no scheme is relative to the HTML document,
// whose address this backend is never told, and a PDF reader resolves it
// against the PDF's own; a fragment names a place in the HTML document, and
// the display list says nothing about where that is. A control character
// inside a URI is refused by the builder.
func TestALinkThatCannotBeWrittenIsRefused(t *testing.T) {
	for _, tc := range []struct{ href, why string }{
		{"other.html", "relative to the HTML document"},
		{"/abs/path", "relative to the HTML document"},
		{"#section", "a fragment of the HTML document"},
		{"https://example.com/a&#1;b", "control character"},
	} {
		href := tc.href
		in := Input{HTML: `<p>go <a href="` + href + `">there</a> or <a href="https://example.com/">here</a></p>`}
		_, err := Render(in, Options{})
		var refused *RefusedError
		if !errors.As(err, &refused) || !hasRule(refused.Findings, RuleLinkDropped, layout.Error) {
			t.Errorf("%q: rendered with err %v; want a %s refusal", href, err, RuleLinkDropped)
			continue
		}
		// The finding says which link, and why, so the author can fix it.
		for _, f := range refused.Findings {
			if f.Rule == RuleLinkDropped && !strings.Contains(f.Message, tc.why) {
				t.Errorf("%q: the finding says %q; want it to say the link is %s", href, f.Message, tc.why)
			}
		}
		in.Policy = layout.Policy{RuleLinkDropped: layout.Warn}
		out, err := Render(in, Options{})
		if err != nil {
			t.Errorf("%q: with the rule lowered: %v", href, err)
			continue
		}
		if !hasRule(out.Findings, RuleLinkDropped, layout.Warn) {
			t.Errorf("%q: the dropped link was not reported: %v", href, out.Findings)
		}
		// The link that can be written still is.
		got := pageLinks(t, reread(t, out.Document))
		if len(got) != 1 || got[0].uri != "https://example.com/" {
			t.Errorf("%q: the page's links are %v; want the one to https://example.com/", href, got)
		}
	}
}

// TestALinkFormeRefusesIsNotAPDFLink: a scheme forme will not make a link of
// (javascript:, data:, file:) never reaches the backend. forme reports it,
// at Warn, and the page is written with the words and no annotation.
func TestALinkFormeRefusesIsNotAPDFLink(t *testing.T) {
	for _, href := range []string{"javascript:alert(1)", "data:text/html,x", "file:///etc/passwd"} {
		out, err := Render(Input{HTML: `<p><a href="` + href + `">words</a></p>`}, Options{})
		if err != nil {
			t.Errorf("%q: %v", href, err)
			continue
		}
		if got := pageLinks(t, reread(t, out.Document)); len(got) != 0 {
			t.Errorf("%q: the page carries %v", href, got)
		}
		if !hasRule(out.Findings, layout.RuleLinkRefused, layout.Warn) {
			t.Errorf("%q: forme did not report the link it refused: %v", href, out.Findings)
		}
	}
}
