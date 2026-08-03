package pdf0

import (
	"bytes"
	"math"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Where a link arrives, blend modes, and page transparency groups.

func aPage() object.IndirectRef { return object.IndirectRef{Number: 7} }

// TestDestinationForms pins each array against what a reader parses. The array
// is positional, so a missing argument is not "leave it alone" — it is a
// different destination, and a reader given four numbers where it expects three
// reads the wrong one.
func TestDestinationForms(t *testing.T) {
	cases := []struct {
		name string
		dest Destination
		want object.Array
	}{
		{
			"the zero value is the whole page",
			Destination{},
			object.Array{aPage(), object.Name("Fit")},
		},
		{
			"a y coordinate to the top of the window",
			Destination{Kind: AtTop, Top: 700},
			object.Array{aPage(), object.Name("FitH"), object.Integer(700)},
		},
		{
			"a corner, with the magnification left alone",
			Destination{Kind: AtPosition, Left: 72, Top: 700},
			object.Array{aPage(), object.Name("XYZ"), object.Integer(72), object.Integer(700), object.Null{}},
		},
		{
			"a corner and a magnification",
			Destination{Kind: AtPosition, Left: 72, Top: 700, Zoom: 1.5},
			object.Array{aPage(), object.Name("XYZ"), object.Integer(72), object.Integer(700), object.Real(1.5)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.dest.destination(aPage())
			if err != nil {
				t.Fatalf("building: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("element %d = %v, want %v (all: %v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

// TestZoomOfZeroIsWrittenAsNull pins the distinction the array cannot express
// by omission. Zero magnification is not a magnification; null is how "leave it
// as it is" is written, and writing 0 instead tells a reader to scale the page
// to nothing.
func TestZoomOfZeroIsWrittenAsNull(t *testing.T) {
	got, err := Destination{Kind: AtPosition, Left: 1, Top: 2}.destination(aPage())
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	if _, isNull := got[4].(object.Null); !isNull {
		t.Errorf("the magnification is %v, want null", got[4])
	}
}

// TestDestinationRefusesWhatIsNotACoordinate collects the values a reader
// cannot act on.
func TestDestinationRefusesWhatIsNotACoordinate(t *testing.T) {
	cases := map[string]Destination{
		"infinite top":       {Kind: AtTop, Top: math.Inf(1)},
		"infinite left":      {Kind: AtPosition, Left: math.Inf(-1)},
		"not-a-number zoom":  {Kind: AtPosition, Zoom: math.NaN()},
		"negative zoom":      {Kind: AtPosition, Zoom: -1},
		"unknown kind":       {Kind: 42},
		"infinite fit-h top": {Kind: AtTop, Top: math.NaN()},
	}
	for name, d := range cases {
		if _, err := d.destination(aPage()); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestLinkCarriesItsDestination is the end-to-end claim, through the page and
// out into a written file.
func TestLinkCarriesItsDestination(t *testing.T) {
	doc := NewDocument()
	target := aPage()
	var b content.Builder
	b.Rect(0, 0, 10, 10).Fill()
	pageRef, err := doc.AddPage(Page{
		Width: 200, Height: 200, Content: &b,
		Links: []Link{{
			Rect: [4]float64{10, 10, 100, 30},
			Page: &target,
			To:   Destination{Kind: AtTop, Top: 700},
		}},
	})
	if err != nil {
		t.Fatalf("adding the page: %v", err)
	}
	page := doc.ResolveDict(pageRef)
	annots, _ := doc.Resolve(page.Get("Annots")).(object.Array)
	if len(annots) != 1 {
		t.Fatalf("the page has %d annotations, want 1", len(annots))
	}
	annot := doc.ResolveDict(annots[0])
	dest, _ := doc.Resolve(annot.Get("Dest")).(object.Array)
	if len(dest) != 3 || dest[1] != object.Name("FitH") || dest[2] != object.Integer(700) {
		t.Errorf("the annotation's destination is %v, want [page /FitH 700]", dest)
	}
}

// TestExternalLinkRejectsAPosition pins that a position on a page is meaningless
// for a link that leaves the document. Dropping it silently would hide the
// caller's confusion about which kind of link they were building.
func TestExternalLinkRejectsAPosition(t *testing.T) {
	l := Link{
		Rect: [4]float64{0, 0, 10, 10},
		URI:  "https://example.org/",
		To:   Destination{Kind: AtTop, Top: 100},
	}
	if _, err := l.annotation(); err == nil {
		t.Error("a URI link with a position within a page was accepted")
	}
}

// TestOutlineEntriesCarryTheirDestination pins the same for bookmarks. A
// contents entry that lands at the top of a long page has pointed at the page
// rather than at the section.
func TestOutlineEntriesCarryTheirDestination(t *testing.T) {
	doc := NewDocument()
	var b content.Builder
	b.Rect(0, 0, 10, 10).Fill()
	pageRef, err := doc.AddPage(Page{Width: 200, Height: 800, Content: &b})
	if err != nil {
		t.Fatalf("adding the page: %v", err)
	}
	err = doc.SetOutline([]OutlineItem{
		{Title: "Top", Page: pageRef},
		{Title: "A Section", Page: pageRef, To: Destination{Kind: AtTop, Top: 420}},
	})
	if err != nil {
		t.Fatalf("setting the outline: %v", err)
	}

	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	root := doc.ResolveDict(catalog.Get("Outlines"))
	first := doc.ResolveDict(root.Get("First"))
	if dest, _ := doc.Resolve(first.Get("Dest")).(object.Array); len(dest) != 2 {
		t.Errorf("the first entry's destination is %v, want the whole page", dest)
	}
	second := doc.ResolveDict(first.Get("Next"))
	dest, _ := doc.Resolve(second.Get("Dest")).(object.Array)
	if len(dest) != 3 || dest[1] != object.Name("FitH") || dest[2] != object.Integer(420) {
		t.Errorf("the second entry's destination is %v, want [page /FitH 420]", dest)
	}
}

// TestBlendModesAreCheckedAgainstTheList is the point of listing them.
//
// A reader that meets a blend mode it does not know is required to treat it as
// Normal, without complaining. So a misspelt mode is not an error anywhere in
// the pipeline — it is a page that quietly loses its blending, noticed much
// later and never traced back. Refusing to write it is the only place the
// mistake is still attributable.
func TestBlendModesAreCheckedAgainstTheList(t *testing.T) {
	for _, mode := range []BlendMode{
		BlendNormal, BlendMultiply, BlendScreen, BlendOverlay, BlendDarken,
		BlendLighten, BlendColorDodge, BlendColorBurn, BlendHardLight,
		BlendSoftLight, BlendDifference, BlendExclusion,
		BlendHue, BlendSaturation, BlendColor, BlendLuminosity,
	} {
		gs, err := Blend(mode)
		if err != nil {
			t.Errorf("%s was refused: %v", mode, err)
			continue
		}
		if got := gs.Get("BM"); got != object.Name(mode) {
			t.Errorf("/BM = %v, want %v", got, mode)
		}
	}
	for _, bad := range []BlendMode{"", "multiply", "Mutliply", "Darker", "normal"} {
		if _, err := Blend(bad); err == nil {
			t.Errorf("%q was accepted as a blend mode", bad)
		}
	}
}

// TestBlendWithOpacityCarriesBoth pins the combined form. CSS applies opacity
// and a blend mode to one element; two graphics states would mean two names and
// two operators for one effect.
func TestBlendWithOpacityCarriesBoth(t *testing.T) {
	gs, err := BlendWithOpacity(BlendMultiply, 0.5, 0.25)
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	if gs.Get("BM") != object.Name("Multiply") {
		t.Errorf("/BM = %v, want Multiply", gs.Get("BM"))
	}
	if gs.Get("ca") != object.Real(0.5) {
		t.Errorf("/ca = %v, want 0.5", gs.Get("ca"))
	}
	if gs.Get("CA") != object.Real(0.25) {
		t.Errorf("/CA = %v, want 0.25", gs.Get("CA"))
	}
	if gs.Get("Type") != object.Name("ExtGState") {
		t.Errorf("/Type = %v, want ExtGState", gs.Get("Type"))
	}
	// And the invalid cases of each half still fail through the combination.
	if _, err := BlendWithOpacity("Nonsense", 1, 1); err == nil {
		t.Error("an unknown blend mode was accepted through the combined form")
	}
	if _, err := BlendWithOpacity(BlendNormal, 2, 1); err == nil {
		t.Error("an opacity outside [0,1] was accepted through the combined form")
	}
}

// TestPageGroupIsWrittenWhenAsked pins the group, and that it names a blending
// colour space. Without one, what a translucent mark composites against is left
// to the reader, so the same file prints differently from how it displays.
func TestPageGroupIsWrittenWhenAsked(t *testing.T) {
	doc := NewDocument()
	var b content.Builder
	b.Rect(0, 0, 10, 10).Fill()

	plain, err := doc.AddPage(Page{Width: 100, Height: 100, Content: &b})
	if err != nil {
		t.Fatalf("adding: %v", err)
	}
	if doc.ResolveDict(plain).Get("Group") != nil {
		t.Error("a page was given a transparency group it did not ask for")
	}

	var b2 content.Builder
	b2.Rect(0, 0, 10, 10).Fill()
	grouped, err := doc.AddPage(Page{Width: 100, Height: 100, Content: &b2, Group: true})
	if err != nil {
		t.Fatalf("adding: %v", err)
	}
	group := doc.ResolveDict(doc.ResolveDict(grouped).Get("Group"))
	if group == nil {
		t.Fatal("the page has no transparency group")
	}
	for key, want := range map[object.Name]object.Object{
		"Type": object.Name("Group"),
		"S":    object.Name("Transparency"),
		"CS":   object.Name("DeviceRGB"),
	} {
		if got := group.Get(key); got != want {
			t.Errorf("group /%s = %v, want %v", key, got, want)
		}
	}
}

// TestBlendingPageValidates runs a page that blends and groups past the
// validator, so the new dictionaries are judged by the same rules any
// conforming file's are. PDF/A-1 forbids transparency, so the levels that
// permit it are the ones checked.
func TestBlendingPageValidates(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)
			gs, err := BlendWithOpacity(BlendMultiply, 0.5, 1)
			if err != nil {
				t.Fatalf("building the graphics state: %v", err)
			}
			var b content.Builder
			b.Save().SetExtGState("GS0").SetRGB(1, 0, 0).Rect(10, 10, 50, 50).Fill().Restore()
			_, err = doc.AddPage(Page{
				Width: 200, Height: 200, Content: &b, Group: true,
				ExtGStates: map[object.Name]object.Object{"GS0": gs},
			})
			if err != nil {
				t.Fatalf("adding the page: %v", err)
			}
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("write: %v", err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if v := ValidatePDFABytes(rd, level, buf.Bytes()); len(v) != 0 {
				t.Errorf("a blending, grouped page is not %s: %v", level, v)
			}
		})
	}
}
