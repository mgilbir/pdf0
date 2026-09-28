package htmlpdf

import (
	"errors"
	"image"
	"image/color"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
)

// Filter groups: opacity as a transparency group, a sharp drop shadow as an
// alpha soft mask, and the refusal of what PDF cannot say.

// filterInput is a document whose one picture, half.png, is 40 × 20: its
// right half opaque blue and its left half transparent.
func filterInput(t *testing.T, html string) Input {
	t.Helper()
	dir := t.TempDir()
	img := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 20; x < 40; x++ {
			img.Set(x, y, color.NRGBA{B: 255, A: 255})
		}
	}
	writeImage(t, filepath.Join(dir, "half.png"), img)
	res, err := layout.NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Close() })
	return Input{HTML: html, Resources: res}
}

// theFilterGroup is the one FilterGroup of a composed page.
func theFilterGroup(t *testing.T, c layout.Composed) layout.FilterGroup {
	t.Helper()
	var out []layout.FilterGroup
	for _, op := range c.Ops {
		if v, ok := op.(layout.FilterGroup); ok {
			out = append(out, v)
		}
	}
	if len(out) != 1 {
		t.Fatalf("layout drew %d filter groups; the case needs one", len(out))
	}
	return out[0]
}

// formsDrawn is the form XObjects a stream draws with Do, in order, and the
// /ca in force at each.
func formsDrawn(t *testing.T, doc *pdf0.Document, stream []byte, res *object.Dictionary) (forms []*object.Stream, alphas []float64) {
	t.Helper()
	ca := 1.0
	var saved []float64
	var last string
	ops := strings.Fields(string(stream))
	for i, tok := range ops {
		switch tok {
		case "q":
			saved = append(saved, ca)
		case "Q":
			ca, saved = saved[len(saved)-1], saved[:len(saved)-1]
		case "gs":
			gs := doc.ResolveDict(doc.ResolveDict(res.Get("ExtGState")).Get(object.Name(strings.TrimPrefix(ops[i-1], "/"))))
			if v, ok := doc.Resolve(gs.Get("ca")).(object.Real); ok {
				ca = float64(v)
			}
		case "Do":
			last = strings.TrimPrefix(ops[i-1], "/")
			st, ok := doc.Resolve(doc.ResolveDict(res.Get("XObject")).Get(object.Name(last))).(*object.Stream)
			if !ok {
				t.Fatalf("Do %s names no stream", last)
			}
			if st.Dict.Get("Subtype") == object.Name("Form") {
				forms, alphas = append(forms, st), append(alphas, ca)
			}
		}
	}
	return forms, alphas
}

// isGroup reports whether a form is an isolated transparency group.
func isGroup(doc *pdf0.Document, form *object.Stream) bool {
	g := doc.ResolveDict(form.Dict.Get("Group"))
	return g != nil && g.Get("S") == object.Name("Transparency") && g.Get("I") == object.Boolean(true)
}

// TestAnOpacityGroupFadesItsMarksTogether: two overlapping squares under
// "filter: opacity(0.5)" are faded as one group, so where they overlap the
// page shows the upper square at half strength over the white page — not the
// upper square at half over the lower at half, which is what fading each mark
// would show. forme leaves the group for exactly that reason.
func TestAnOpacityGroupFadesItsMarksTogether(t *testing.T) {
	in := Input{HTML: `<div style="filter: opacity(0.5)">` +
		`<div style="background:rgb(255,0,0);width:50px;height:50px"></div>` +
		`<div style="background:rgb(0,0,255);width:50px;height:50px;margin-top:-25px"></div></div>`}
	c := layout.Compose(in, Options{})
	g := theFilterGroup(t, c)
	if len(g.Filters) != 1 || g.Filters[0].Kind != layout.FilterOpacity {
		t.Fatalf("the group's filters are %+v; the case needs one opacity", g.Filters)
	}
	doc, _ := roundTrip(t, in, Options{})
	page := doc.PageList()[0]
	outer, _ := formsDrawn(t, doc, contentOf(t, doc), doc.ResolveDict(page.Get("Resources")))
	if len(outer) != 1 || !isGroup(doc, outer[0]) {
		t.Fatalf("the page draws %d forms; want one transparency group", len(outer))
	}
	data, _ := doc.StreamData(outer[0])
	inner, alphas := formsDrawn(t, doc, data, doc.ResolveDict(outer[0].Dict.Get("Resources")))
	if len(inner) != 1 || !isGroup(doc, inner[0]) || alphas[0] != 0.5 {
		t.Fatalf("the opacity step draws %d forms at alphas %v; want one group at 0.5", len(inner), alphas)
	}
	if page.Get("Group") == nil {
		t.Error("a page with transparency has no page group")
	}
	for _, r := range renderings(t, doc, c, 96) {
		for _, p := range []struct {
			x, y float64
			want color.RGBA
		}{
			{20, 20, color.RGBA{255, 128, 128, 255}}, // the red square alone
			{20, 45, color.RGBA{128, 128, 255, 255}}, // the overlap: the blue square over white
			{20, 70, color.RGBA{128, 128, 255, 255}}, // the blue square alone
			{70, 45, color.RGBA{255, 255, 255, 255}}, // beside both
		} {
			if got := r.at(p.x, p.y); !near(got, p.want, 3) {
				t.Errorf("%s draws (%v, %v) %v; want %v", r.name, p.x, p.y, got, p.want)
			}
		}
	}
}

// TestASharpDropShadowIsTheGroupsAlphaMoved: a picture whose left half is
// transparent casts a shadow of its right half only, moved by the offset and
// in the shadow's colour at its alpha, under the picture.
func TestASharpDropShadowIsTheGroupsAlphaMoved(t *testing.T) {
	in := filterInput(t, `<img src="half.png" style="filter: drop-shadow(6px 6px 0 rgba(0,0,0,0.5))">`)
	c := layout.Compose(in, Options{})
	g := theFilterGroup(t, c)
	if len(g.Filters) != 1 || g.Filters[0].Kind != layout.FilterDropShadow || g.Filters[0].StdDev != 0 {
		t.Fatalf("the group's filters are %+v; the case needs one sharp drop shadow", g.Filters)
	}
	doc, _ := roundTrip(t, in, Options{})
	page := doc.PageList()[0]
	outer, _ := formsDrawn(t, doc, contentOf(t, doc), doc.ResolveDict(page.Get("Resources")))
	if len(outer) != 1 || !isGroup(doc, outer[0]) {
		t.Fatalf("the page draws %d forms; want one transparency group", len(outer))
	}
	// The shadow step: a soft mask of alpha, and the group drawn after it.
	res := doc.ResolveDict(outer[0].Dict.Get("Resources"))
	var mask *object.Dictionary
	for v := range doc.ResolveDict(res.Get("ExtGState")).Values() {
		if sm := doc.ResolveDict(doc.ResolveDict(v).Get("SMask")); sm != nil {
			mask = sm
		}
	}
	if mask == nil || mask.Get("S") != object.Name("Alpha") {
		t.Fatalf("the shadow step has no alpha soft mask: %v", mask)
	}
	// Image at (8, 8), 40 × 20; its opaque half from x = 28. The shadow is
	// that half moved by (6, 6).
	for _, r := range renderings(t, doc, c, 96) {
		for _, p := range []struct {
			x, y float64
			want color.RGBA
		}{
			{38, 18, color.RGBA{0, 0, 255, 255}},     // the picture's opaque half
			{18, 18, color.RGBA{255, 255, 255, 255}}, // its transparent half, and no shadow behind it
			{51, 25, color.RGBA{128, 128, 128, 255}}, // the shadow, right of the picture
			{40, 31, color.RGBA{128, 128, 128, 255}}, // the shadow, below the picture
			{20, 31, color.RGBA{255, 255, 255, 255}}, // below the transparent half: no shadow
			{51, 11, color.RGBA{255, 255, 255, 255}}, // right of the picture, above the shadow
		} {
			if got := r.at(p.x, p.y); !near(got, p.want, 3) {
				t.Errorf("%s draws (%v, %v) %v; want %v", r.name, p.x, p.y, got, p.want)
			}
		}
	}
}

// TestAFilterPDFCannotSayIsRefused: a blur, a blurred drop shadow and a
// colour matrix over a picture are refused under RuleUndrawable, each saying
// what it is; under a policy that lets the page through, the group is left
// out and the page says so.
func TestAFilterPDFCannotSayIsRefused(t *testing.T) {
	for _, tc := range []struct{ filter, says string }{
		{"blur(2px)", "blur"},
		{"drop-shadow(2px 2px 3px black)", "drop shadow blurred"},
		{"grayscale(1)", "colour-matrix"},
		{"invert(1)", "colour-matrix"},
	} {
		in := filterInput(t, `<img src="half.png" style="filter: `+tc.filter+`">`)
		_, err := Render(in, Options{})
		var refused *RefusedError
		if !errors.As(err, &refused) {
			t.Errorf("%s: rendered (%v); want a refusal", tc.filter, err)
			continue
		}
		found := false
		for _, f := range refused.Findings {
			if f.Rule == RuleUndrawable && strings.Contains(f.Message, tc.says) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: refused with %+v; want %s saying %q", tc.filter, refused.Findings, RuleUndrawable, tc.says)
		}
		in.Policy = layout.Policy{RuleUndrawable: layout.Warn}
		out, err := Render(in, Options{})
		if err != nil {
			t.Errorf("%s under a Warn policy: %v", tc.filter, err)
			continue
		}
		if strings.Contains(string(contentOf(t, out.Document)), "Do") {
			t.Errorf("%s under a Warn policy: the page still draws the group:\n%s", tc.filter, contentOf(t, out.Document))
		}
	}
}
