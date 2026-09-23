package pdf0

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

// An uncoloured tiling pattern's cell, and every content stream it invokes, has
// the colour operators, ri, sh, the colour-related graphics-state entries and
// every image but a stencil mask *ignored* (ISO 32000-2 8.6.8, 8.7.3.3). A cell
// that uses any of them draws something other than what was written, so
// AddTilingPattern refuses it (audit 2026-09-22 C130).

func maskImage(mask bool) *object.Stream {
	d := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("XObject")},
		object.Entry{Key: "Subtype", Value: object.Name("Image")},
		object.Entry{Key: "Width", Value: object.Integer(1)},
		object.Entry{Key: "Height", Value: object.Integer(1)},
		object.Entry{Key: "Length", Value: object.Integer(1)},
	)
	if mask {
		d.Set("ImageMask", object.Boolean(true))
	} else {
		d.Set("ColorSpace", object.Name("DeviceGray"))
		d.Set("BitsPerComponent", object.Integer(8))
	}
	return object.NewStream(d, []byte{0})
}

func uncolored(b *content.Builder, xo, gs map[object.Name]object.Object) TilingPattern {
	return TilingPattern{BBox: [4]float64{0, 0, 5, 5}, Uncolored: true, Content: b, XObjects: xo, ExtGStates: gs}
}

func TestUncoloredPatternRefusesWhatItWouldIgnore(t *testing.T) {
	doc := NewDocument()

	// A form whose own content sets a colour, and one that draws such a form:
	// the rule reaches "all other content streams invoked from within".
	var colourful content.Builder
	colourful.SetRGB(1, 0, 0).Rect(0, 0, 1, 1).Fill()
	colourForm, err := doc.AddForm(Form{BBox: [4]float64{0, 0, 1, 1}, Content: &colourful})
	if err != nil {
		t.Fatal(err)
	}
	var wrapper content.Builder
	wrapper.Draw("Inner")
	wrapperForm, err := doc.AddForm(Form{BBox: [4]float64{0, 0, 1, 1}, Content: &wrapper,
		XObjects: map[object.Name]object.Object{"Inner": colourForm}})
	if err != nil {
		t.Fatal(err)
	}
	var plainImageForm content.Builder
	plainImageForm.Draw("Im")
	imageInForm, err := doc.AddForm(Form{BBox: [4]float64{0, 0, 1, 1}, Content: &plainImageForm,
		XObjects: map[object.Name]object.Object{"Im": doc.Add(maskImage(false))}})
	if err != nil {
		t.Fatal(err)
	}

	draw := func(name object.Name) *content.Builder {
		var b content.Builder
		b.Draw(name)
		return &b
	}
	withGS := func() *content.Builder {
		var b content.Builder
		b.SetExtGState("G0").Rect(0, 0, 5, 5).Fill()
		return &b
	}
	cases := map[string]struct {
		p     TilingPattern
		wants string
	}{
		"ri": {uncolored(func() *content.Builder {
			var b content.Builder
			b.SetRenderingIntent(content.Perceptual).Rect(0, 0, 5, 5).Fill()
			return &b
		}(), nil, nil), "ri"},
		"sh": {func() TilingPattern {
			var b content.Builder
			b.Shading("Sh0")
			p := uncolored(&b, nil, nil)
			p.Shadings = map[object.Name]object.Object{"Sh0": object.NewDictionary()}
			return p
		}(), "sh"},
		"an image that is not a stencil mask": {uncolored(draw("Im0"),
			map[object.Name]object.Object{"Im0": maskImage(false)}, nil), "stencil"},
		"an indirect image that is not a stencil mask": {uncolored(draw("Im0"),
			map[object.Name]object.Object{"Im0": doc.Add(maskImage(false))}, nil), "stencil"},
		"a form that sets a colour": {uncolored(draw("Fm0"),
			map[object.Name]object.Object{"Fm0": colourForm}, nil), "rg"},
		"a form drawing a form that sets a colour": {uncolored(draw("Fm0"),
			map[object.Name]object.Object{"Fm0": wrapperForm}, nil), "rg"},
		"a form drawing an image": {uncolored(draw("Fm0"),
			map[object.Name]object.Object{"Fm0": imageInForm}, nil), "stencil"},
		"a graphics state with a transfer function": {uncolored(withGS(), nil,
			map[object.Name]object.Object{"G0": object.NewDictionary(
				object.Entry{Key: "Type", Value: object.Name("ExtGState")},
				object.Entry{Key: "TR", Value: object.Name("Identity")})}), "TR"},
		"a graphics state with a halftone": {uncolored(withGS(), nil,
			map[object.Name]object.Object{"G0": object.NewDictionary(
				object.Entry{Key: "HT", Value: object.Name("Default")})}), "HT"},
		"a graphics state with UseBlackPtComp": {uncolored(withGS(), nil,
			map[object.Name]object.Object{"G0": object.NewDictionary(
				object.Entry{Key: "UseBlackPtComp", Value: object.Name("ON")})}), "UseBlackPtComp"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := doc.AddTilingPattern(tc.p)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q does not mention %q", err, tc.wants)
			}
			if !strings.Contains(err.Error(), "ignore") {
				t.Errorf("error %q does not say the reader ignores it", err)
			}
		})
	}

	// What an uncoloured cell may do: paint a stencil mask, draw a form that
	// sets no colour, and use a graphics state with no colour-related entry.
	// The same operators in a coloured pattern are fine.
	var clean content.Builder
	clean.Rect(0, 0, 1, 1).Fill()
	cleanForm, err := doc.AddForm(Form{BBox: [4]float64{0, 0, 1, 1}, Content: &clean})
	if err != nil {
		t.Fatal(err)
	}
	var ok content.Builder
	ok.Draw("Mask").Draw("Fm0").SetExtGState("G0").Rect(0, 0, 5, 5).Fill()
	if _, err := doc.AddTilingPattern(uncolored(&ok,
		map[object.Name]object.Object{"Mask": maskImage(true), "Fm0": cleanForm},
		map[object.Name]object.Object{"G0": object.NewDictionary(
			object.Entry{Key: "LW", Value: object.Integer(2)})})); err != nil {
		t.Errorf("a stencil mask, a colourless form and a plain graphics state were refused: %v", err)
	}
	coloured := uncolored(draw("Fm0"), map[object.Name]object.Object{"Fm0": colourForm}, nil)
	coloured.Uncolored = false
	if _, err := doc.AddTilingPattern(coloured); err != nil {
		t.Errorf("a coloured pattern drawing a colourful form was refused: %v", err)
	}
}

// TestUncoloredCellSeesInlineImages: a form carried over from a read document
// can paint an inline image, which the content lexer reports whole. One that
// is not a stencil mask is refused like an image XObject; a stencil mask
// (/IM true) is allowed. Binary sample bytes that spell an operator are not
// read as one.
func TestUncoloredCellSeesInlineImages(t *testing.T) {
	doc := NewDocument()
	form := func(body string) object.Object {
		return doc.Add(object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("XObject")},
			object.Entry{Key: "Subtype", Value: object.Name("Form")},
			object.Entry{Key: "BBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(1), object.Integer(1)}},
			object.Entry{Key: "Length", Value: object.Integer(len(body))},
		), []byte(body)))
	}
	var b content.Builder
	b.Draw("Fm0")
	colour := uncolored(&b, map[object.Name]object.Object{"Fm0": form("q BI /W 1 /H 1 /CS /G /BPC 8 ID \x00 EI Q")}, nil)
	if _, err := doc.AddTilingPattern(colour); err == nil || !strings.Contains(err.Error(), "inline image") {
		t.Errorf("a colour inline image in the cell's form: err = %v, want the inline image refused", err)
	}
	var b2 content.Builder
	b2.Draw("Fm0")
	mask := uncolored(&b2, map[object.Name]object.Object{"Fm0": form("q BI /W 8 /H 1 /IM true /L 2 ID rg EI Q")}, nil)
	if _, err := doc.AddTilingPattern(mask); err != nil {
		t.Errorf("a stencil-mask inline image whose data spells rg was refused: %v", err)
	}
}

// TestUncoloredCheckStopsAtACycle pins that a form that draws itself, which a
// read document can contain, ends the walk rather than recursing for ever.
func TestUncoloredCheckStopsAtACycle(t *testing.T) {
	doc := NewDocument()
	form := object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("XObject")},
		object.Entry{Key: "Subtype", Value: object.Name("Form")},
		object.Entry{Key: "BBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(1), object.Integer(1)}},
	), []byte("/Self Do"))
	selfRef := doc.Add(form)
	form.Dict.Set("Resources", object.NewDictionary(object.Entry{Key: "XObject",
		Value: object.NewDictionary(object.Entry{Key: "Self", Value: selfRef})}))
	var b content.Builder
	b.Draw("Fm0")
	if _, err := doc.AddTilingPattern(uncolored(&b, map[object.Name]object.Object{"Fm0": selfRef}, nil)); err != nil {
		t.Errorf("a self-drawing colourless form was refused: %v", err)
	}
}
