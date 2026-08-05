package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Gradients, form XObjects and links, judged at every conformance level. Each
// touches rules the validator checks hard — colour and functions for a shading,
// resources and transparency groups for a form, flags and actions for a link —
// so the levels are where they are worth running.

// validateAtEveryLevel writes a document, reads it back and reports any finding
// at each PDF/A level, for a document the caller builds per level.
func validateAtEveryLevel(t *testing.T, build func(*Document) error) {
	t.Helper()
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)
			if err := build(doc); err != nil {
				t.Fatalf("building: %v", err)
			}
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("write: %v", err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			for _, e := range ValidatePDFABytes(rd, level, buf.Bytes()) {
				t.Errorf("violation: %s", e.Error())
			}
		})
	}
}

// TestGradientsValidate covers both shading types and both ways of using one:
// painted over the clip with sh, and used as a pattern to fill a path.
func TestGradientsValidate(t *testing.T) {
	validateAtEveryLevel(t, func(doc *Document) error {
		linear, err := LinearGradient(72, 600, 372, 600, []Stop{
			{Offset: 0, Color: [3]float64{1, 0, 0}},
			{Offset: 0.5, Color: [3]float64{1, 1, 0}},
			{Offset: 1, Color: [3]float64{0, 0.4, 1}},
		})
		if err != nil {
			return err
		}
		radial, err := RadialGradient(200, 400, 0, 200, 400, 90, []Stop{
			{Offset: 0, Color: [3]float64{1, 1, 1}},
			{Offset: 1, Color: [3]float64{0.1, 0.1, 0.4}},
		})
		if err != nil {
			return err
		}

		var b content.Builder
		// Painted over a clip: sh ignores the current path.
		b.Save().Rect(72, 560, 300, 80).Clip().EndPath().Shading("Sh0").Restore()
		// Used as a pattern: any fill takes the gradient as its colour.
		b.Save().
			SetColorSpace("Pattern").SetPattern("P0").
			Rect(72, 320, 260, 160).Fill().
			Restore()

		_, err = doc.AddPage(Page{
			Width: 612, Height: 792, Content: &b,
			Shadings: map[object.Name]object.Object{"Sh0": doc.Add(linear)},
			Patterns: map[object.Name]object.Object{"P0": doc.Add(ShadingPattern(doc.Add(radial)))},
		})
		return err
	})
}

// TestGradientStopsAreChecked pins the input rules. A ramp that does not run
// from 0 to 1, or whose stops go backwards, describes no gradient — and the
// resulting file would render as something, which is why it is caught here.
func TestGradientStopsAreChecked(t *testing.T) {
	good := []Stop{{Offset: 0, Color: [3]float64{0, 0, 0}}, {Offset: 1, Color: [3]float64{1, 1, 1}}}
	cases := []struct {
		name  string
		stops []Stop
	}{
		{"one stop", good[:1]},
		{"not starting at zero", []Stop{{Offset: 0.2}, {Offset: 1}}},
		{"not ending at one", []Stop{{Offset: 0}, {Offset: 0.8}}},
		{"out of order", []Stop{{Offset: 0}, {Offset: 0.7}, {Offset: 0.3}, {Offset: 1}}},
		{"offset outside the range", []Stop{{Offset: 0}, {Offset: 1.5}}},
		{"colour outside the range", []Stop{{Offset: 0, Color: [3]float64{2, 0, 0}}, {Offset: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LinearGradient(0, 0, 100, 0, tc.stops); err == nil {
				t.Error("accepted")
			}
		})
	}
	if _, err := LinearGradient(50, 50, 50, 50, good); err == nil {
		t.Error("a gradient between one point and itself was accepted")
	}
	if _, err := RadialGradient(0, 0, -1, 0, 0, 10, good); err == nil {
		t.Error("a negative radius was accepted")
	}
}

// TestMultiStopGradientSplitsItsDomain pins the stitching function's shape. The
// interior stop offsets must appear as /Bounds, and each sub-function must be
// encoded over its whole domain — otherwise the colours between the stops are
// sampled from the wrong part of each ramp, which looks like a rendering
// artefact rather than a defect in the file.
func TestMultiStopGradientSplitsItsDomain(t *testing.T) {
	sh, err := LinearGradient(0, 0, 100, 0, []Stop{
		{Offset: 0, Color: [3]float64{1, 0, 0}},
		{Offset: 0.25, Color: [3]float64{0, 1, 0}},
		{Offset: 1, Color: [3]float64{0, 0, 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fn, ok := sh.Get("Function").(*object.Dictionary)
	if !ok {
		t.Fatal("the shading has no function dictionary")
	}
	if got, _ := fn.Get("FunctionType").(object.Integer); got != 3 {
		t.Errorf("FunctionType = %v, want 3 (stitching)", got)
	}
	bounds, _ := fn.Get("Bounds").(object.Array)
	if len(bounds) != 1 || object.Float(bounds[0]) != 0.25 {
		t.Errorf("Bounds = %v, want [0.25]: the one interior stop", bounds)
	}
	fns, _ := fn.Get("Functions").(object.Array)
	if len(fns) != 2 {
		t.Errorf("Functions has %d entries, want 2: one per interval", len(fns))
	}
	encode, _ := fn.Get("Encode").(object.Array)
	if len(encode) != 4 {
		t.Fatalf("Encode has %d entries, want 4: a pair per interval", len(encode))
	}
	for i := 0; i < len(encode); i += 2 {
		if object.Float(encode[i]) != 0 || object.Float(encode[i+1]) != 1 {
			t.Errorf("Encode pair %d is [%v %v], want [0 1]", i/2, encode[i], encode[i+1])
		}
	}
	// Two stops need no stitching at all.
	simple, err := LinearGradient(0, 0, 1, 0, []Stop{{Offset: 0}, {Offset: 1, Color: [3]float64{1, 1, 1}}})
	if err != nil {
		t.Fatal(err)
	}
	f2, _ := simple.Get("Function").(*object.Dictionary)
	if got, _ := f2.Get("FunctionType").(object.Integer); got != 2 {
		t.Errorf("a two-stop gradient used function type %v, want 2 (exponential)", got)
	}
}

// TestFormXObjectValidates covers reuse and grouping: one form painted twice,
// and a transparency group faded as a whole.
func TestFormXObjectValidates(t *testing.T) {
	validateAtEveryLevel(t, func(doc *Document) error {
		var badge content.Builder
		badge.Save().SetRGB(0.9, 0.3, 0.1).Rect(0, 0, 60, 60).Fill().Restore()
		badge.Save().SetGray(1).Rect(10, 10, 40, 40).Fill().Restore()
		formRef, err := doc.AddForm(Form{BBox: [4]float64{0, 0, 60, 60}, Content: &badge})
		if err != nil {
			return err
		}

		var page content.Builder
		// The same drawing, twice, at different places and scales.
		page.Save().Concat(1, 0, 0, 1, 72, 650).Draw("Badge").Restore()
		page.Save().Concat(0.5, 0, 0, 0.5, 200, 650).Draw("Badge").Restore()

		_, err = doc.AddPage(Page{
			Width: 612, Height: 792, Content: &page,
			XObjects: map[object.Name]object.Object{"Badge": formRef},
		})
		return err
	})
}

// TestFormBoundingBoxMustHaveArea pins the silent failure a form has. Marks
// outside the box are clipped away, so a box with no area is a form that draws
// nothing at all — and nothing else would report it.
func TestFormBoundingBoxMustHaveArea(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	var b content.Builder
	b.Rect(0, 0, 10, 10).Fill()
	if _, err := doc.AddForm(Form{BBox: [4]float64{0, 0, 0, 10}, Content: &b}); err == nil {
		t.Error("a form with an empty bounding box was accepted")
	}
	var b2 content.Builder
	b2.Rect(0, 0, 10, 10).Fill()
	if _, err := doc.AddForm(Form{BBox: [4]float64{0, 0, 10, 10}, Content: &b2}); err != nil {
		t.Errorf("a form with a real box was refused: %v", err)
	}
}

// TestFormUndefinedResourceIsRefused pins that a form gets the same check a
// page does. Its resources are its own, so a name it uses and does not define
// is as broken there as on a page.
func TestFormUndefinedResourceIsRefused(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	var b content.Builder
	b.Draw("Missing")
	_, err := doc.AddForm(Form{BBox: [4]float64{0, 0, 10, 10}, Content: &b})
	if err == nil {
		t.Fatal("a form using an undefined XObject was accepted")
	}
	if !strings.Contains(err.Error(), "Missing") {
		t.Errorf("error %q does not name the missing resource", err)
	}
}

// TestTransparencyGroupIsWritten pins the entries that make a form a group.
// Isolated and non-knockout is what makes opacity behave as a caller expects,
// and both are easy to omit without any visible difference until two shapes
// overlap.
func TestTransparencyGroupIsWritten(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	var b content.Builder
	b.Rect(0, 0, 10, 10).Fill()
	ref, err := doc.AddForm(Form{BBox: [4]float64{0, 0, 10, 10}, Content: &b, Group: true})
	if err != nil {
		t.Fatal(err)
	}
	form, ok := doc.Resolve(ref).(*object.Stream)
	if !ok {
		t.Fatal("the form is not a stream")
	}
	group, ok := doc.Resolve(form.Dict.Get("Group")).(*object.Dictionary)
	if !ok {
		t.Fatal("the form carries no /Group")
	}
	if got, _ := group.Get("S").(object.Name); got != "Transparency" {
		t.Errorf("/S = %v, want Transparency", got)
	}
	if got, _ := group.Get("I").(object.Boolean); !got {
		t.Error("the group is not isolated, so opacity would fade the page beneath it too")
	}
	// A form that did not ask for a group must not have one.
	var plain content.Builder
	plain.Rect(0, 0, 10, 10).Fill()
	ref2, _ := doc.AddForm(Form{BBox: [4]float64{0, 0, 10, 10}, Content: &plain})
	if s, _ := doc.Resolve(ref2).(*object.Stream); s.Dict.Get("Group") != nil {
		t.Error("a form that asked for no group was given one")
	}
}

// TestLinksValidate covers both destinations at every level. A link's flags and
// its action are what the validator checks, and a URI action is permitted where
// a script is not.
func TestLinksValidate(t *testing.T) {
	validateAtEveryLevel(t, func(doc *Document) error {
		var b content.Builder
		b.Save().SetRGB(0, 0, 0.8).Rect(72, 700, 200, 20).Fill().Restore()
		first, err := doc.AddPage(Page{
			Width: 612, Height: 792, Content: &b,
			Links: []Link{{Rect: [4]float64{72, 700, 272, 720}, URI: "https://example.org/spec"}},
		})
		if err != nil {
			return err
		}
		var b2 content.Builder
		b2.Save().SetGray(0.5).Rect(72, 700, 200, 20).Fill().Restore()
		_, err = doc.AddPage(Page{
			Width: 612, Height: 792, Content: &b2,
			Links: []Link{{Rect: [4]float64{72, 700, 272, 720}, Page: &first}},
		})
		return err
	})
}

// TestLinkDestinationsAreChecked pins the input rules, above all the one that
// matters for a document built from untrusted input: a javascript: URI is a
// script by another name, and carrying one into a file that claims to conform
// would be carrying an attacker's code.
func TestLinkDestinationsAreChecked(t *testing.T) {
	page := object.IndirectRef{Number: 3}
	cases := []struct {
		name string
		link Link
	}{
		{"no destination", Link{Rect: [4]float64{0, 0, 10, 10}}},
		{"two destinations", Link{Rect: [4]float64{0, 0, 10, 10}, URI: "https://example.org", Page: &page}},
		{"empty rectangle", Link{Rect: [4]float64{0, 0, 0, 10}, URI: "https://example.org"}},
		{"javascript", Link{Rect: [4]float64{0, 0, 10, 10}, URI: "javascript:alert(1)"}},
		{"JavaScript in mixed case", Link{Rect: [4]float64{0, 0, 10, 10}, URI: "JavaScript:alert(1)"}},
		{"data URI", Link{Rect: [4]float64{0, 0, 10, 10}, URI: "data:text/html,<script>"}},
		{"relative", Link{Rect: [4]float64{0, 0, 10, 10}, URI: "/relative/path"}},
		{"newline injection", Link{Rect: [4]float64{0, 0, 10, 10}, URI: "https://example.org\n/Type/Action"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewPDFADocument(pdfa.PDFA2b)
			var b content.Builder
			b.Rect(0, 0, 10, 10).Fill()
			if _, err := doc.AddPage(Page{Width: 612, Height: 792, Content: &b, Links: []Link{tc.link}}); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// TestLinkAnnotationPrints pins the flag that decides whether a link survives
// onto paper. A non-Popup annotation must declare Print and must not declare
// Hidden or NoView; the validator reports the omission, and a reader simply
// drops the link from the printed page.
func TestLinkAnnotationPrints(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	var b content.Builder
	b.Rect(0, 0, 10, 10).Fill()
	ref, err := doc.AddPage(Page{
		Width: 612, Height: 792, Content: &b,
		Links: []Link{{Rect: [4]float64{10, 10, 100, 30}, URI: "https://example.org"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	annots, _ := doc.Resolve(doc.ResolveDict(ref).Get("Annots")).(object.Array)
	if len(annots) != 1 {
		t.Fatalf("the page carries %d annotations, want 1", len(annots))
	}
	a := doc.ResolveDict(annots[0])
	flags, _ := a.Get("F").(object.Integer)
	const print, hidden, noView = 1 << 2, 1 << 1, 1 << 5
	if int(flags)&print == 0 {
		t.Error("the link does not declare Print, so it would vanish from a printed page")
	}
	if int(flags)&(hidden|noView) != 0 {
		t.Errorf("flags %d declare Hidden or NoView", flags)
	}
	action := doc.ResolveDict(a.Get("A"))
	if action == nil {
		t.Fatal("the link carries no action")
	}
	if got, _ := action.Get("S").(object.Name); got != "URI" {
		t.Errorf("action type = %v, want URI", got)
	}
}

// TestShadingTypeMatchesItsCoordinates pins the pairing that decides how a
// reader interprets the numbers. An axial shading has four coordinates — two
// points — and a radial one has six, two circles. Declaring the wrong type does
// not make a file invalid: the reader takes the first four numbers as an axis
// and paints something, so a radial gradient comes out as a stripe. Nothing
// else here would report it.
func TestShadingTypeMatchesItsCoordinates(t *testing.T) {
	stops := []Stop{{Offset: 0, Color: [3]float64{1, 0, 0}}, {Offset: 1, Color: [3]float64{0, 0, 1}}}

	linear, err := LinearGradient(0, 0, 100, 0, stops)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := linear.Get("ShadingType").(object.Integer); got != 2 {
		t.Errorf("a linear gradient declared ShadingType %v, want 2 (axial)", got)
	}
	if c, _ := linear.Get("Coords").(object.Array); len(c) != 4 {
		t.Errorf("a linear gradient has %d coordinates, want 4: two points", len(c))
	}

	radial, err := RadialGradient(10, 20, 0, 10, 20, 50, stops)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := radial.Get("ShadingType").(object.Integer); got != 3 {
		t.Errorf("a radial gradient declared ShadingType %v, want 3", got)
	}
	c, _ := radial.Get("Coords").(object.Array)
	if len(c) != 6 {
		t.Fatalf("a radial gradient has %d coordinates, want 6: two circles", len(c))
	}
	// And in the order the format gives them: centre, radius, centre, radius.
	for i, want := range []float64{10, 20, 0, 10, 20, 50} {
		if got := object.Float(c[i]); got != want {
			t.Errorf("coordinate %d = %v, want %v", i, got, want)
		}
	}
}
