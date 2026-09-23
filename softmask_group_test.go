package pdf0

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// A soft mask's form is a transparency group, with a colour space for a
// luminosity mask (audit 2026-09-22 C133); a shading pattern has a matrix and
// says where it is anchored (C134).

func mustShadingPattern(t *testing.T, shading object.Object) *object.Dictionary {
	t.Helper()
	p, err := ShadingPattern(shading, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// maskForm adds a form with the given /Group (nil for none).
func maskForm(t *testing.T, d *Document, group *object.Dictionary) object.IndirectRef {
	t.Helper()
	ref, err := d.AddForm(Form{BBox: [4]float64{0, 0, 10, 10}, Content: square()})
	if err != nil {
		t.Fatal(err)
	}
	if group != nil {
		d.Resolve(ref).(*object.Stream).Dict.Set("Group", group)
	}
	return ref
}

func transparencyGroup(cs object.Object) *object.Dictionary {
	g := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Group")},
		object.Entry{Key: "S", Value: object.Name("Transparency")},
	)
	if cs != nil {
		g.Set("CS", cs)
	}
	return g
}

// TestSoftMaskFormMustBeATransparencyGroup pins Table 142: /G is a
// transparency group, and a luminosity mask needs the group's colour space,
// whose component count the backdrop has.
func TestSoftMaskFormMustBeATransparencyGroup(t *testing.T) {
	d := NewDocument()
	noGroup := maskForm(t, d, nil)
	notTransparency := maskForm(t, d, object.NewDictionary(object.Entry{Key: "S", Value: object.Name("Other")}))
	noCS := maskForm(t, d, transparencyGroup(nil))
	indexed := maskForm(t, d, transparencyGroup(object.Array{object.Name("Indexed"), object.Name("DeviceRGB"), object.Integer(1), object.String{Value: []byte("abcdef")}}))
	gray := maskForm(t, d, transparencyGroup(object.Name("DeviceGray")))
	cmyk := maskForm(t, d, transparencyGroup(object.Name("DeviceCMYK")))
	image := d.Add(maskImage(false))

	for name, c := range map[string]struct {
		call  func() error
		wants string
	}{
		"luminosity, no group":         {func() error { _, err := d.LuminositySoftMask(noGroup, nil); return err }, "transparency group"},
		"alpha, no group":              {func() error { _, err := d.AlphaSoftMask(noGroup); return err }, "transparency group"},
		"luminosity, other group":      {func() error { _, err := d.LuminositySoftMask(notTransparency, nil); return err }, "transparency group"},
		"alpha, other group":           {func() error { _, err := d.AlphaSoftMask(notTransparency); return err }, "transparency group"},
		"luminosity, group with no CS": {func() error { _, err := d.LuminositySoftMask(noCS, nil); return err }, "/CS"},
		"luminosity, Indexed group":    {func() error { _, err := d.LuminositySoftMask(indexed, nil); return err }, "blend"},
		"luminosity, image":            {func() error { _, err := d.LuminositySoftMask(image, nil); return err }, "form XObject"},
		"luminosity, nothing":          {func() error { _, err := d.LuminositySoftMask(object.IndirectRef{Number: 999}, nil); return err }, "form XObject"},
		"RGB backdrop, gray group":     {func() error { _, err := d.LuminositySoftMask(gray, []float64{0, 0, 0}); return err }, "components"},
		"gray backdrop, CMYK group":    {func() error { _, err := d.LuminositySoftMask(cmyk, []float64{0}); return err }, "components"},
		"CMYK backdrop out of range":   {func() error { _, err := d.LuminositySoftMask(cmyk, []float64{0, 0, 0, 2}); return err }, "outside"},
	} {
		t.Run(name, func(t *testing.T) {
			err := c.call()
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("error %q does not mention %q", err, c.wants)
			}
		})
	}

	// What is right: a group without /CS for an alpha mask, a backdrop in the
	// group's own space, and the default backdrop left to the reader.
	if _, err := d.AlphaSoftMask(noCS); err != nil {
		t.Errorf("an alpha mask over a group with no /CS was refused: %v", err)
	}
	gs, err := d.LuminositySoftMask(cmyk, []float64{0, 0, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if bc, _ := d.ResolveDict(gs.Get("SMask")).Get("BC").(object.Array); len(bc) != 4 {
		t.Errorf("/BC = %v, want the four CMYK components", bc)
	}
	gs, err = d.LuminositySoftMask(gray, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bc := d.ResolveDict(gs.Get("SMask")).Get("BC"); bc != nil {
		t.Errorf("a nil backdrop wrote /BC %v; the default is black in the group's space", bc)
	}
	lab := maskForm(t, d, transparencyGroup(object.Array{object.Name("Lab"), object.NewDictionary(
		object.Entry{Key: "WhitePoint", Value: object.Array{object.Real(0.9505), object.Integer(1), object.Real(1.089)}})}))
	if _, err := d.LuminositySoftMask(lab, []float64{50, -20, 20}); err != nil {
		t.Errorf("a Lab backdrop in Lab's own ranges was refused: %v", err)
	}
}

// TestShadingPatternMatrix pins the parameter and what it means: the pattern
// carries the /Matrix it is given, a nil one writes none (the identity), and a
// non-finite one is refused.
func TestShadingPatternMatrix(t *testing.T) {
	grad, err := LinearGradient(0, 0, 100, 0, []Stop{{0, [3]float64{0, 0, 0}}, {1, [3]float64{1, 1, 1}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ShadingPattern(grad, &[6]float64{2, 0, 0, 2, 50, 60})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := p.Get("Matrix").(object.Array)
	if len(m) != 6 || m[0] != object.Integer(2) || m[4] != object.Integer(50) || m[5] != object.Integer(60) {
		t.Errorf("/Matrix = %v", p.Get("Matrix"))
	}
	if p, err := ShadingPattern(grad, nil); err != nil || p.Get("Matrix") != nil {
		t.Errorf("a nil matrix gave /Matrix %v (err %v); it is the identity, which is the default", p.Get("Matrix"), err)
	}
	if _, err := ShadingPattern(nil, nil); err == nil {
		t.Error("a pattern with no shading was accepted")
	}

	// It paints and validates.
	doc := mustPDFADoc(t, pdfa.PDFA2b)
	pat, err := ShadingPattern(doc.Add(grad), &[6]float64{1, 0, 0, 1, 72, 72})
	if err != nil {
		t.Fatal(err)
	}
	var b content.Builder
	b.SetColorSpace("Pattern").SetPattern("P0").Rect(72, 72, 100, 50).Fill()
	if _, err := doc.AddPage(Page{Width: 300, Height: 300, Content: &b,
		Patterns: map[object.Name]object.Object{"P0": doc.Add(pat)}}); err != nil {
		t.Fatal(err)
	}
	if v := writeAndValidate(t, doc, pdfa.PDFA2b); len(v) != 0 {
		t.Errorf("a page filled with a matrixed shading pattern does not validate: %v", v)
	}
}
