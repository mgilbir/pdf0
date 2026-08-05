package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

func blankPage(t *testing.T, doc *Document) object.IndirectRef {
	t.Helper()
	var b content.Builder
	b.Rect(0, 0, 10, 10).Fill()
	ref, err := doc.AddPage(Page{Width: 612, Height: 792, Content: &b})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// TestOutlineTreeIsLinkedBothWays pins the structure a reader walks. The format
// is a doubly linked tree, and a reader that meets an inconsistent set of links
// shows a mangled outline rather than reporting anything — so every link is
// checked here.
func TestOutlineTreeIsLinkedBothWays(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	p1, p2, p3 := blankPage(t, doc), blankPage(t, doc), blankPage(t, doc)

	if err := doc.SetOutline([]OutlineItem{
		{Title: "Introduction", Page: p1},
		{Title: "Body", Page: p2, Open: true, Children: []OutlineItem{
			{Title: "First part", Page: p2},
			{Title: "Second part", Page: p3},
		}},
		{Title: "Appendix", Page: p3},
	}); err != nil {
		t.Fatalf("SetOutline: %v", err)
	}

	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	root := doc.ResolveDict(catalog.Get("Outlines"))
	if root == nil {
		t.Fatal("the catalog names no outline")
	}
	// Five entries are visible: three at the top and the two under the open one.
	if got, _ := root.Get("Count").(object.Integer); got != 5 {
		t.Errorf("root /Count = %v, want 5", got)
	}

	// Walk the top level forwards, checking the back links as we go.
	var titles []string
	var prev object.Object
	for ref := root.Get("First"); ref != nil; {
		item := doc.ResolveDict(ref)
		if item == nil {
			t.Fatal("an outline entry is missing")
		}
		titles = append(titles, string(item.Get("Title").(object.String).Value))
		if p := item.Get("Prev"); (p == nil) != (prev == nil) {
			t.Errorf("%v: /Prev disagrees with the walk", titles[len(titles)-1])
		}
		if item.Get("Parent") == nil {
			t.Errorf("%v: no /Parent", titles[len(titles)-1])
		}
		prev, ref = ref, item.Get("Next")
	}
	want := []string{"Introduction", "Body", "Appendix"}
	if strings.Join(titles, ",") != strings.Join(want, ",") {
		t.Errorf("top level = %v, want %v", titles, want)
	}
	if last := doc.ResolveDict(root.Get("Last")); last == nil ||
		string(last.Get("Title").(object.String).Value) != "Appendix" {
		t.Error("/Last does not name the final entry")
	}
}

// TestClosedOutlineEntryHasANegativeCount pins the sign convention, which is
// how the format says "collapsed, with this many inside" rather than "open,
// with this many showing". Getting it backwards opens every bookmark in a long
// document at once.
func TestClosedOutlineEntryHasANegativeCount(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	p := blankPage(t, doc)
	if err := doc.SetOutline([]OutlineItem{
		{Title: "Closed", Page: p, Children: []OutlineItem{{Title: "Hidden", Page: p}}},
		{Title: "Open", Page: p, Open: true, Children: []OutlineItem{{Title: "Shown", Page: p}}},
	}); err != nil {
		t.Fatal(err)
	}
	root := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Outlines"))
	closed := doc.ResolveDict(root.Get("First"))
	if got, _ := closed.Get("Count").(object.Integer); got != -1 {
		t.Errorf("a closed entry's /Count = %v, want -1", got)
	}
	open := doc.ResolveDict(closed.Get("Next"))
	if got, _ := open.Get("Count").(object.Integer); got != 1 {
		t.Errorf("an open entry's /Count = %v, want 1", got)
	}
	// Three entries showing: two at the top plus the one under the open entry.
	if got, _ := root.Get("Count").(object.Integer); got != 3 {
		t.Errorf("root /Count = %v, want 3", got)
	}
}

// TestOutlineTitlesCarryTheirScript pins that a title outside ASCII survives.
// A PDF text string is bytes or UTF-16 behind a byte-order mark, and writing
// the wrong one turns a Russian heading into mojibake.
func TestOutlineTitlesCarryTheirScript(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	p := blankPage(t, doc)
	if err := doc.SetOutline([]OutlineItem{
		{Title: "Introduction", Page: p},
		{Title: "Введение", Page: p},
	}); err != nil {
		t.Fatal(err)
	}
	root := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Outlines"))
	ascii := doc.ResolveDict(root.Get("First"))
	if got := ascii.Get("Title").(object.String).Value; string(got) != "Introduction" {
		t.Errorf("an ASCII title was written as %q", got)
	}
	cyrillic := doc.ResolveDict(ascii.Get("Next")).Get("Title").(object.String).Value
	if len(cyrillic) < 2 || cyrillic[0] != 0xFE || cyrillic[1] != 0xFF {
		t.Errorf("a non-ASCII title was not written as UTF-16 with a byte-order mark: %q", cyrillic)
	}
}

// TestOutlineValidatesAtEveryLevel runs an outlined document past the
// validator. An outline is navigation rather than behaviour, so its entries
// carry destinations and not actions — which is a distinction PDF/A cares about
// and a reader does not.
func TestOutlineValidatesAtEveryLevel(t *testing.T) {
	validateAtEveryLevel(t, func(doc *Document) error {
		p := blankPage(t, doc)
		return doc.SetOutline([]OutlineItem{
			{Title: "Chapter one", Page: p, Open: true, Children: []OutlineItem{
				{Title: "A section", Page: p},
			}},
			{Title: "Chapter two", Page: p},
		})
	})
}

// TestOutlineInputIsChecked pins the entries that could not be shown.
func TestOutlineInputIsChecked(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	p := blankPage(t, doc)
	if err := doc.SetOutline([]OutlineItem{{Page: p}}); err == nil {
		t.Error("an entry with no title was accepted")
	}
	if err := doc.SetOutline([]OutlineItem{{Title: "Nowhere"}}); err == nil {
		t.Error("an entry naming no page was accepted")
	}
	deep := OutlineItem{Title: "x", Page: p}
	for i := 0; i < maxOutlineDepth+2; i++ {
		deep = OutlineItem{Title: "x", Page: p, Children: []OutlineItem{deep}}
	}
	if err := doc.SetOutline([]OutlineItem{deep}); err == nil {
		t.Error("an outline nested past the limit was accepted")
	}
	// And setting an empty outline removes it rather than writing an empty one.
	if err := doc.SetOutline([]OutlineItem{{Title: "Real", Page: p}}); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetOutline(nil); err != nil {
		t.Fatal(err)
	}
	if doc.ResolveDict(doc.Trailer.Get("Root")).Get("Outlines") != nil {
		t.Error("an empty outline left an /Outlines entry behind")
	}
}

// TestSoftMaskShapesTransparency covers the capability end to end: a gradient
// painted into a form, used as the luminosity of a mask, so that what is drawn
// under it fades across the page.
//
// PDF/A-1 is excluded deliberately: it forbids transparency outright, so a soft
// mask cannot appear in a conforming PDF/A-1 file at all, and the level below
// pins that rather than skipping it.
func TestSoftMaskShapesTransparency(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)
			maskRef := buildFadeMask(t, doc)
			gs, err := LuminositySoftMask(maskRef, [3]float64{0, 0, 0})
			if err != nil {
				t.Fatal(err)
			}
			var page content.Builder
			page.Save().
				SetExtGState("GS0").
				SetRGB(0.8, 0.1, 0.1).Rect(72, 500, 300, 200).Fill().
				Restore()
			if _, err := doc.AddPage(Page{
				Width: 612, Height: 792, Content: &page,
				ExtGStates: map[object.Name]object.Object{"GS0": doc.Add(gs)},
			}); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatal(err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range ValidatePDFABytes(rd, level, buf.Bytes()) {
				t.Errorf("violation: %s", e.Error())
			}
		})
	}
}

// buildFadeMask paints a black-to-white gradient into a form, which as a
// luminosity mask fades from hidden to shown.
func buildFadeMask(t *testing.T, doc *Document) object.IndirectRef {
	t.Helper()
	grad, err := LinearGradient(72, 0, 372, 0, []Stop{
		{Offset: 0, Color: [3]float64{0, 0, 0}},
		{Offset: 1, Color: [3]float64{1, 1, 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var mask content.Builder
	mask.Save().Rect(72, 500, 300, 200).Clip().EndPath().Shading("Fade").Restore()
	ref, err := doc.AddForm(Form{
		BBox:     [4]float64{72, 500, 372, 700},
		Content:  &mask,
		Group:    true, // a mask's form must be a transparency group
		Shadings: map[object.Name]object.Object{"Fade": doc.Add(grad)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// TestSoftMaskKindsAreDistinct pins the pair of names apart. A luminosity mask
// measures how bright the form is, so a black shape hides what follows; an
// alpha mask measures whether the form painted at all, so a black shape shows
// it. Reaching for the wrong one inverts the mask exactly.
func TestSoftMaskKindsAreDistinct(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	form := buildFadeMask(t, doc)

	lum, err := LuminositySoftMask(form, [3]float64{0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := doc.ResolveDict(lum.Get("SMask")).Get("S").(object.Name); got != "Luminosity" {
		t.Errorf("luminosity mask /S = %v", got)
	}
	alpha, err := AlphaSoftMask(form)
	if err != nil {
		t.Fatal(err)
	}
	am := doc.ResolveDict(alpha.Get("SMask"))
	if got, _ := am.Get("S").(object.Name); got != "Alpha" {
		t.Errorf("alpha mask /S = %v", got)
	}
	// An alpha mask has no backdrop: there is no brightness to composite for.
	if am.Get("BC") != nil {
		t.Error("an alpha mask carries a backdrop colour, which means nothing for it")
	}
	if got, _ := NoSoftMask().Get("SMask").(object.Name); got != "None" {
		t.Errorf("NoSoftMask /SMask = %v, want None", got)
	}
	if _, err := LuminositySoftMask(nil, [3]float64{0, 0, 0}); err == nil {
		t.Error("a mask with no form was accepted")
	}
	if _, err := LuminositySoftMask(form, [3]float64{2, 0, 0}); err == nil {
		t.Error("a backdrop outside [0,1] was accepted")
	}
}
