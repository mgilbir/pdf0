package images

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"image"
	"testing"
)

// rgb8 returns the 8-bit RGBA of a pixel.
func rgb8(t *testing.T, m image.Image, x, y int) (r, g, b, a uint8) {
	t.Helper()
	rr, gg, bb, aa := m.At(x, y).RGBA()
	return uint8(rr >> 8), uint8(gg >> 8), uint8(bb >> 8), uint8(aa >> 8)
}

func mustBuild(t *testing.T, st *object.Stream, w, h, bpc int) image.Image {
	t.Helper()
	m, ok := buildImage(core.View{Limits: core.DefaultLimits()}, st, st.Data, w, h, bpc)
	if !ok {
		t.Fatalf("buildImage failed")
	}
	return m
}

func TestBuildImageCMYK(t *testing.T) {
	// cyan, magenta, yellow, black, white in a 5x1 image.
	data := []byte{
		255, 0, 0, 0, // cyan  -> (0,255,255)
		0, 255, 0, 0, // magenta -> (255,0,255)
		0, 0, 255, 0, // yellow -> (255,255,0)
		0, 0, 0, 255, // black -> (0,0,0)
		0, 0, 0, 0, // white -> (255,255,255)
	}
	st := imageXObject(5, 1, 8, "DeviceCMYK", "", data)
	m := mustBuild(t, st, 5, 1, 8)
	want := [][3]uint8{{0, 255, 255}, {255, 0, 255}, {255, 255, 0}, {0, 0, 0}, {255, 255, 255}}
	for x, w := range want {
		r, g, b, _ := rgb8(t, m, x, 0)
		if r != w[0] || g != w[1] || b != w[2] {
			t.Errorf("CMYK pixel %d = (%d,%d,%d), want %v", x, r, g, b, w)
		}
	}
}

func TestBuildImageGrayBitDepths(t *testing.T) {
	// 4-bit gray, two pixels 0x0 and 0xF -> black and white.
	st := imageXObject(2, 1, 4, "DeviceGray", "", []byte{0x0F})
	m := mustBuild(t, st, 2, 1, 4)
	if r, _, _, _ := rgb8(t, m, 0, 0); r != 0 {
		t.Errorf("4bpc pixel0 = %d, want 0", r)
	}
	if r, _, _, _ := rgb8(t, m, 1, 0); r != 255 {
		t.Errorf("4bpc pixel1 = %d, want 255", r)
	}

	// 2-bit gray, four pixels 0,1,2,3 -> 0,85,170,255.
	st = imageXObject(4, 1, 2, "DeviceGray", "", []byte{0b00_01_10_11})
	m = mustBuild(t, st, 4, 1, 2)
	for x, want := range []uint8{0, 85, 170, 255} {
		if r, _, _, _ := rgb8(t, m, x, 0); r != want {
			t.Errorf("2bpc pixel %d = %d, want %d", x, r, want)
		}
	}

	// 16-bit gray, one pixel 0xFFFF -> 255, one 0x0000 -> 0.
	st = imageXObject(2, 1, 16, "DeviceGray", "", []byte{0xFF, 0xFF, 0x00, 0x00})
	m = mustBuild(t, st, 2, 1, 16)
	if r, _, _, _ := rgb8(t, m, 0, 0); r != 255 {
		t.Errorf("16bpc pixel0 = %d, want 255", r)
	}
	if r, _, _, _ := rgb8(t, m, 1, 0); r != 0 {
		t.Errorf("16bpc pixel1 = %d, want 0", r)
	}
}

func TestBuildImageDecodeInvert(t *testing.T) {
	// DeviceGray with /Decode [1 0] inverts: sample 0 -> white, 255 -> black.
	st := imageXObject(2, 1, 8, "DeviceGray", "", []byte{0, 255})
	st.Dict.Set("Decode", object.Array{object.Integer(1), object.Integer(0)})
	m := mustBuild(t, st, 2, 1, 8)
	if r, _, _, _ := rgb8(t, m, 0, 0); r != 255 {
		t.Errorf("inverted pixel0 = %d, want 255", r)
	}
	if r, _, _, _ := rgb8(t, m, 1, 0); r != 0 {
		t.Errorf("inverted pixel1 = %d, want 0", r)
	}
}

func TestBuildImageIndexed(t *testing.T) {
	// Indexed over DeviceRGB: palette {red, green}, indices [0,1,0].
	st := imageXObject(3, 1, 8, "", "", []byte{0, 1, 0})
	st.Dict.Set("ColorSpace", object.Array{
		object.Name("Indexed"), object.Name("DeviceRGB"), object.Integer(1),
		object.String{Value: []byte{255, 0, 0, 0, 255, 0}},
	})
	m := mustBuild(t, st, 3, 1, 8)
	want := [][3]uint8{{255, 0, 0}, {0, 255, 0}, {255, 0, 0}}
	for x, w := range want {
		r, g, b, _ := rgb8(t, m, x, 0)
		if r != w[0] || g != w[1] || b != w[2] {
			t.Errorf("indexed pixel %d = (%d,%d,%d), want %v", x, r, g, b, w)
		}
	}

	// 1-bit indexed selects palette entries directly.
	st = imageXObject(2, 1, 1, "", "", []byte{0b01000000})
	st.Dict.Set("ColorSpace", object.Array{
		object.Name("Indexed"), object.Name("DeviceRGB"), object.Integer(1),
		object.String{Value: []byte{1, 2, 3, 250, 251, 252}},
	})
	m = mustBuild(t, st, 2, 1, 1)
	if r, g, b, _ := rgb8(t, m, 0, 0); r != 1 || g != 2 || b != 3 {
		t.Errorf("1bpc indexed pixel0 = (%d,%d,%d), want (1,2,3)", r, g, b)
	}
	if r, g, b, _ := rgb8(t, m, 1, 0); r != 250 || g != 251 || b != 252 {
		t.Errorf("1bpc indexed pixel1 = (%d,%d,%d), want (250,251,252)", r, g, b)
	}
}

func TestBuildImageICCBased(t *testing.T) {
	// ICCBased with /N 3 renders as RGB.
	prof := &object.Stream{Dict: object.Dictionary{}, Data: []byte{}}
	prof.Dict.Set("N", object.Integer(3))
	st := imageXObject(1, 1, 8, "", "", []byte{10, 20, 30})
	st.Dict.Set("ColorSpace", object.Array{object.Name("ICCBased"), prof})
	m := mustBuild(t, st, 1, 1, 8)
	if r, g, b, _ := rgb8(t, m, 0, 0); r != 10 || g != 20 || b != 30 {
		t.Errorf("ICCBased N=3 pixel = (%d,%d,%d), want (10,20,30)", r, g, b)
	}
}

func TestBuildImageSoftMask(t *testing.T) {
	// A 2x1 grey base with a 2x1 grey SMask giving alpha 0 then 255.
	base := imageXObject(2, 1, 8, "DeviceGray", "", []byte{100, 200})
	sm := imageXObject(2, 1, 8, "DeviceGray", "", []byte{0, 255})
	base.Dict.Set("SMask", sm)
	m := mustBuild(t, base, 2, 1, 8)
	if _, _, _, a := rgb8(t, m, 0, 0); a != 0 {
		t.Errorf("smask alpha0 = %d, want 0", a)
	}
	if _, _, _, a := rgb8(t, m, 1, 0); a != 255 {
		t.Errorf("smask alpha1 = %d, want 255", a)
	}
}

func TestBuildImageLab(t *testing.T) {
	// L*=100, a*=b*=0 is white; L*=0 is black. Lab samples are 8-bit here.
	st := imageXObject(2, 1, 8, "", "", []byte{
		255, 128, 128, // L=100, a=0, b=0 (a,b decode [-128,127]->~0)
		0, 128, 128, // L=0
	})
	st.Dict.Set("ColorSpace", object.Array{object.Name("Lab"), func() *object.Dictionary {
		d := &object.Dictionary{}
		d.Set("WhitePoint", object.Array{object.Real(0.9642), object.Real(1.0), object.Real(0.8249)})
		d.Set("Range", object.Array{object.Integer(-128), object.Integer(127), object.Integer(-128), object.Integer(127)})
		return d
	}()})
	m := mustBuild(t, st, 2, 1, 8)
	r, g, b, _ := rgb8(t, m, 0, 0)
	if r < 240 || g < 240 || b < 240 {
		t.Errorf("Lab white = (%d,%d,%d), want ~255", r, g, b)
	}
	r, g, b, _ = rgb8(t, m, 1, 0)
	if r > 15 || g > 15 || b > 15 {
		t.Errorf("Lab black = (%d,%d,%d), want ~0", r, g, b)
	}
}

func TestBuildImageColorKeyMask(t *testing.T) {
	// RGB image; /Mask [0 0 0 0 0 0] makes pure black transparent.
	st := imageXObject(2, 1, 8, "DeviceRGB", "", []byte{0, 0, 0, 10, 20, 30})
	st.Dict.Set("Mask", object.Array{object.Integer(0), object.Integer(0), object.Integer(0), object.Integer(0), object.Integer(0), object.Integer(0)})
	m := mustBuild(t, st, 2, 1, 8)
	if _, _, _, a := rgb8(t, m, 0, 0); a != 0 {
		t.Errorf("colour-key masked pixel alpha = %d, want 0", a)
	}
	if _, _, _, a := rgb8(t, m, 1, 0); a != 255 {
		t.Errorf("unmasked pixel alpha = %d, want 255", a)
	}
}

func TestBuildImageStencilMask(t *testing.T) {
	// A 2x1 grey base with a 2x1 stencil /Mask hiding the first pixel.
	base := imageXObject(2, 1, 8, "DeviceGray", "", []byte{100, 200})
	mk := imageXObject(2, 1, 1, "", "", []byte{0b10000000}) // pixel0=1 hides, pixel1=0 shows
	mk.Dict.Set("ImageMask", object.Boolean(true))
	base.Dict.Set("Mask", mk)
	m := mustBuild(t, base, 2, 1, 8)
	if _, _, _, a := rgb8(t, m, 0, 0); a != 0 {
		t.Errorf("stencil hidden pixel alpha = %d, want 0", a)
	}
	if _, _, _, a := rgb8(t, m, 1, 0); a != 255 {
		t.Errorf("stencil shown pixel alpha = %d, want 255", a)
	}
}

func TestBuildImageSeparationFallsBack(t *testing.T) {
	// A Separation whose tint function cannot be evaluated (here a bogus scalar)
	// declines rendering, so callers fall back to the raw bytes.
	st := imageXObject(1, 1, 8, "", "", []byte{128})
	st.Dict.Set("ColorSpace", object.Array{object.Name("Separation"), object.Name("Spot"), object.Name("DeviceGray"), object.Integer(0)})
	if _, ok := buildImage(core.View{Limits: core.DefaultLimits()}, st, st.Data, 1, 1, 8); ok {
		t.Error("Separation with unusable tint should not be rendered")
	}
}

func TestBuildImageSeparation(t *testing.T) {
	// Separation over DeviceGray with a type-2 tint that inverts the tint value:
	// tint 0 -> gray 1 (white), tint 1 -> gray 0 (black).
	tint := &object.Dictionary{}
	tint.Set("FunctionType", object.Integer(2))
	tint.Set("Domain", object.Array{object.Real(0), object.Real(1)})
	tint.Set("C0", object.Array{object.Real(1)})
	tint.Set("C1", object.Array{object.Real(0)})
	tint.Set("N", object.Real(1))

	st := imageXObject(2, 1, 8, "", "", []byte{0, 255})
	st.Dict.Set("ColorSpace", object.Array{object.Name("Separation"), object.Name("Spot"), object.Name("DeviceGray"), tint})
	m := mustBuild(t, st, 2, 1, 8)
	// pixel0 tint 0 -> white
	if r, _, _, _ := rgb8(t, m, 0, 0); r != 255 {
		t.Errorf("separation pixel0 = %d, want 255", r)
	}
	// pixel1 tint 1 -> black
	if r, _, _, _ := rgb8(t, m, 1, 0); r != 0 {
		t.Errorf("separation pixel1 = %d, want 0", r)
	}
}

func TestBuildImageDeviceN(t *testing.T) {
	// DeviceN with two colorants over DeviceRGB, tint via a type-4 function that
	// maps (c0,c1) -> (c0, c1, 0). A pixel (1,0) becomes red.
	tint := &object.Stream{Dict: object.Dictionary{}, Data: []byte("{ 0 }")}
	tint.Dict.Set("FunctionType", object.Integer(4))
	tint.Dict.Set("Domain", object.Array{object.Real(0), object.Real(1), object.Real(0), object.Real(1)})
	tint.Dict.Set("Range", object.Array{object.Real(0), object.Real(1), object.Real(0), object.Real(1), object.Real(0), object.Real(1)})

	st := imageXObject(1, 1, 8, "", "", []byte{255, 0})
	st.Dict.Set("ColorSpace", object.Array{
		object.Name("DeviceN"),
		object.Array{object.Name("ColA"), object.Name("ColB")},
		object.Name("DeviceRGB"),
		tint,
	})
	m := mustBuild(t, st, 1, 1, 8)
	if r, g, b, _ := rgb8(t, m, 0, 0); r != 255 || g != 0 || b != 0 {
		t.Errorf("deviceN pixel = (%d,%d,%d), want (255,0,0)", r, g, b)
	}
}
