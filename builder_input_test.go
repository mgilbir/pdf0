package pdf0

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

// Builders validate at the call, not at Write (audit 2026-09-22 C131). A NaN,
// an infinity or a size that is not a size is refused by the builder that was
// handed it, and a builder that refuses leaves the document exactly as it was:
// validate everything, then mutate.

// docSnapshot is the document as bytes that do not depend on map order: every
// object, in object-number order, serialized, then the trailer.
func docSnapshot(t *testing.T, d *Document) string {
	t.Helper()
	nums := make([]int, 0, len(d.Objects))
	for n := range d.Objects {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	var buf bytes.Buffer
	s := NewSerializer(&buf)
	for _, n := range nums {
		fmt.Fprintf(&buf, "%d: ", n)
		if err := s.WriteObject(d.Objects[n].Value); err != nil {
			t.Fatalf("snapshot of object %d: %v", n, err)
		}
		buf.WriteByte('\n')
	}
	if err := s.WriteObject(&d.Trailer); err != nil {
		t.Fatalf("snapshot of the trailer: %v", err)
	}
	return buf.String()
}

func square() *content.Builder {
	var b content.Builder
	b.Rect(0, 0, 1, 1).Fill()
	return &b
}

// usedFace is a face that has set some text, so embedding it writes objects:
// a failure after it has been embedded is a failure that left objects behind.
func usedFace(t *testing.T) (*fonts.Face, *content.Builder) {
	t.Helper()
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	var b content.Builder
	b.BeginText().SetFont("F0", 12)
	face.DrawShaped(&b, "Hi", 12)
	b.EndText()
	return face, &b
}

func TestDocumentBuildersRefuseBadNumbersAndChangeNothing(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	type builderCase struct {
		name  string
		setup func(*Document) // runs before the snapshot; may add a page
		call  func(*Document) error
	}
	var firstPage object.IndirectRef
	withPage := func(d *Document) {
		ref, err := d.AddPage(Page{Width: 100, Height: 100, Content: square()})
		if err != nil {
			t.Fatal(err)
		}
		firstPage = ref
	}
	page := func(p Page) func(*Document) error {
		return func(d *Document) error { _, err := d.AddPage(p); return err }
	}
	link := func(r [4]float64) []Link { return []Link{{Rect: r, URI: "https://example.com"}} }
	var cases []builderCase
	for _, v := range []float64{nan, inf, -inf, -1, 0} {
		cases = append(cases,
			builderCase{fmt.Sprintf("AddPage width %v", v), nil, page(Page{Width: v, Height: 100, Content: square()})},
			builderCase{fmt.Sprintf("AddPage height %v", v), nil, page(Page{Width: 100, Height: v, Content: square()})},
		)
	}
	for _, v := range []float64{nan, inf, -inf} {
		cases = append(cases,
			builderCase{fmt.Sprintf("AddPage link rect %v", v), nil,
				page(Page{Width: 100, Height: 100, Content: square(), Links: link([4]float64{0, 0, v, 10})})},
			builderCase{fmt.Sprintf("AddPage link rect origin %v", v), nil,
				page(Page{Width: 100, Height: 100, Content: square(), Links: link([4]float64{v, 0, 10, 10})})},
			builderCase{fmt.Sprintf("AddForm bbox %v", v), nil, func(d *Document) error {
				_, err := d.AddForm(Form{BBox: [4]float64{0, 0, v, 10}, Content: square()})
				return err
			}},
			builderCase{fmt.Sprintf("AddForm bbox origin %v", v), nil, func(d *Document) error {
				_, err := d.AddForm(Form{BBox: [4]float64{v, 0, 10, 10}, Content: square()})
				return err
			}},
			builderCase{fmt.Sprintf("AddForm matrix %v", v), nil, func(d *Document) error {
				_, err := d.AddForm(Form{BBox: [4]float64{0, 0, 10, 10}, Matrix: &[6]float64{1, 0, 0, 1, v, 0}, Content: square()})
				return err
			}},
			builderCase{fmt.Sprintf("AddTilingPattern bbox origin %v", v), nil, func(d *Document) error {
				_, err := d.AddTilingPattern(TilingPattern{BBox: [4]float64{v, 0, 10, 10}, Content: square()})
				return err
			}},
			builderCase{fmt.Sprintf("SetOutline destination %v", v), withPage, func(d *Document) error {
				return d.SetOutline([]OutlineItem{
					{Title: "fine", Page: firstPage},
					{Title: "broken", Page: firstPage, To: Destination{Kind: AtTop, Top: v}},
				})
			}},
			builderCase{fmt.Sprintf("SetOutline nested destination %v", v), withPage, func(d *Document) error {
				return d.SetOutline([]OutlineItem{{Title: "fine", Page: firstPage, Children: []OutlineItem{
					{Title: "broken", Page: firstPage, To: Destination{Kind: AtPosition, Left: v}},
				}}})
			}},
		)
	}

	// Refusals that come after a face has been embedded, which is where a
	// half-built document came from: the face's objects were written, then
	// the call failed.
	cases = append(cases,
		builderCase{"AddPage face, then an undefined resource", nil, func(d *Document) error {
			face, b := usedFace(t)
			b.Save().Draw("Missing").Restore()
			_, err := d.AddPage(Page{Width: 100, Height: 100, Content: b, Faces: map[object.Name]*fonts.Face{"F0": face}})
			return err
		}},
		builderCase{"AddTilingPattern face, then a NaN matrix", nil, func(d *Document) error {
			face, b := usedFace(t)
			_, err := d.AddTilingPattern(TilingPattern{BBox: [4]float64{0, 0, 10, 10}, Content: b,
				Matrix: &[6]float64{nan, 0, 0, 1, 0, 0}, Faces: map[object.Name]*fonts.Face{"F0": face}})
			return err
		}},
		builderCase{"AddForm face, then an undefined resource", nil, func(d *Document) error {
			face, b := usedFace(t)
			b.Save().Draw("Missing").Restore()
			_, err := d.AddForm(Form{BBox: [4]float64{0, 0, 10, 10}, Content: b, Faces: map[object.Name]*fonts.Face{"F0": face}})
			return err
		}},
		builderCase{"AddPage with a nil resource value", nil, page(Page{Width: 100, Height: 100, Content: func() *content.Builder {
			var b content.Builder
			b.SetExtGState("G0")
			return &b
		}(), ExtGStates: map[object.Name]object.Object{"G0": nil}})},
		// F0 embeds, then F1 — which has set nothing — cannot be: embedding it
		// would write a font with no glyphs. F0's objects must not stay.
		builderCase{"AddPage face, then a face that cannot be embedded", nil, func(d *Document) error {
			face, b := usedFace(t)
			unused, err := fonts.NotoSans()
			if err != nil {
				t.Fatal(err)
			}
			_, err = d.AddPage(Page{Width: 100, Height: 100, Content: b,
				Faces: map[object.Name]*fonts.Face{"F0": face, "F1": unused}})
			return err
		}},
		builderCase{"AddPage face, then a second nil face", nil, func(d *Document) error {
			face, b := usedFace(t)
			_, err := d.AddPage(Page{Width: 100, Height: 100, Content: b,
				Faces: map[object.Name]*fonts.Face{"F0": face, "F1": nil}})
			return err
		}},
	)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDocument()
			if tc.setup != nil {
				tc.setup(d)
			}
			before := docSnapshot(t, d)
			if err := tc.call(d); err == nil {
				t.Fatal("accepted")
			}
			if after := docSnapshot(t, d); after != before {
				t.Errorf("the refused call changed the document:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// TestFreeBuildersRefuseBadNumbers covers the builders that return a
// dictionary: the value they refuse is one that would otherwise fail only at
// Write, in whatever document it was put into.
func TestFreeBuildersRefuseBadNumbers(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	stops := func(off, c float64) []Stop {
		return []Stop{{Offset: 0, Color: [3]float64{0, 0, 0}}, {Offset: off, Color: [3]float64{c, 1, 1}}, {Offset: 1, Color: [3]float64{1, 1, 1}}}
	}
	good := stops(0.5, 0.5)
	grad, err := LinearGradient(0, 0, 1, 0, good)
	if err != nil {
		t.Fatal(err)
	}
	maskDoc := NewDocument()
	maskForm, err := maskDoc.AddForm(Form{BBox: [4]float64{0, 0, 1, 1}, Content: square(), Group: true})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func() error{}
	for _, v := range []float64{nan, inf, -inf} {
		v := v
		for name, call := range map[string]func() error{
			"Opacity fill":          func() error { _, err := Opacity(v, 1); return err },
			"Opacity stroke":        func() error { _, err := Opacity(1, v); return err },
			"BlendWithOpacity":      func() error { _, err := BlendWithOpacity(BlendMultiply, v, 1); return err },
			"LinearGradient x1":     func() error { _, err := LinearGradient(0, 0, v, 0, good); return err },
			"LinearGradient y0":     func() error { _, err := LinearGradient(0, v, 1, 0, good); return err },
			"LinearGradient offset": func() error { _, err := LinearGradient(0, 0, 1, 0, stops(v, 0.5)); return err },
			"LinearGradient colour": func() error { _, err := LinearGradient(0, 0, 1, 0, stops(0.5, v)); return err },
			"RadialGradient centre": func() error { _, err := RadialGradient(v, 0, 0, 1, 1, 5, good); return err },
			"RadialGradient radius": func() error { _, err := RadialGradient(0, 0, 0, 1, 1, v, good); return err },
			"LuminositySoftMask": func() error {
				_, err := maskDoc.LuminositySoftMask(maskForm, []float64{v, 0, 0})
				return err
			},
			"ShadingPattern matrix": func() error {
				_, err := ShadingPattern(grad, &[6]float64{1, 0, 0, 1, v, 0})
				return err
			},
		} {
			cases[fmt.Sprintf("%s %v", name, v)] = call
		}
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// TestBuilderErrorsNameTheValue pins that a refusal says which number was
// wrong, not only that one was.
func TestBuilderErrorsNameTheValue(t *testing.T) {
	d := NewDocument()
	_, err := d.AddPage(Page{Width: math.Inf(1), Height: 100, Content: square()})
	if err == nil || !strings.Contains(err.Error(), "width") {
		t.Errorf("error %v does not name the width", err)
	}
}
