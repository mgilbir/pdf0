package images

import (
	"image"
	"image/color"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Unit tests that came with the functions they exercise when this package
// split from the root package.

func TestApplyJPEGDecodeCMYK(t *testing.T) {
	src := image.NewCMYK(image.Rect(0, 0, 2, 2))
	vals := []color.CMYK{
		{C: 0, M: 64, Y: 128, K: 255},
		{C: 255, M: 0, Y: 10, K: 200},
		{C: 30, M: 200, Y: 90, K: 5},
		{C: 100, M: 100, Y: 100, K: 100},
	}
	i := 0
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			src.SetCMYK(x, y, vals[i])
			i++
		}
	}
	decode := []float64{1, 0, 1, 0, 1, 0, 1, 0}
	out := applyJPEGDecode(src, decode)
	cm, ok := out.(*image.CMYK)
	if !ok {
		t.Fatalf("CMYK input should yield *image.CMYK, got %T", out)
	}
	i = 0
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			want := color.CMYK{
				C: 255 - vals[i].C,
				M: 255 - vals[i].M,
				Y: 255 - vals[i].Y,
				K: 255 - vals[i].K,
			}
			got := cm.CMYKAt(x, y)
			if got != want {
				t.Fatalf("at (%d,%d): got %+v want %+v", x, y, got, want)
			}
			i++
		}
	}

	// Identity /Decode returns the input unchanged.
	if applyJPEGDecode(src, []float64{0, 1, 0, 1, 0, 1, 0, 1}) != image.Image(src) {
		t.Fatal("identity CMYK /Decode should return the same image")
	}
	// Wrong component count leaves the image unchanged.
	if applyJPEGDecode(src, []float64{1, 0}) != image.Image(src) {
		t.Fatal("mismatched component count should return the same image")
	}
}
func TestApplyImageMasks(t *testing.T) {
	// A 2x1 opaque RGBA base.
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	src.Set(1, 0, color.RGBA{R: 40, G: 50, B: 60, A: 255})

	st := imageXObject(2, 1, 8, "DeviceRGB", "DCTDecode", nil)
	sm := imageXObject(2, 1, 8, "DeviceGray", "", []byte{0, 128})
	st.Dict.Set("SMask", sm)

	out := applyImageMasks(core.View{Limits: core.DefaultLimits()}, st, src)
	nrgba, ok := out.(*image.NRGBA)
	if !ok {
		t.Fatalf("expected *image.NRGBA, got %T", out)
	}
	// Colour is preserved exactly (no codec involved here); alpha from SMask.
	// Read the NRGBA pixels directly: .RGBA() would premultiply by alpha.
	if p := nrgbaPix(nrgba, 0, 0); p != [4]byte{10, 20, 30, 0} {
		t.Errorf("pixel0 = %v, want [10 20 30 0]", p)
	}
	if p := nrgbaPix(nrgba, 1, 0); p != [4]byte{40, 50, 60, 128} {
		t.Errorf("pixel1 = %v, want [40 50 60 128]", p)
	}

	// With no mask keys, the image is returned unchanged.
	plain := imageXObject(2, 1, 8, "DeviceRGB", "DCTDecode", nil)
	if got := applyImageMasks(core.View{Limits: core.DefaultLimits()}, plain, src); got != image.Image(src) {
		t.Errorf("no-mask image should be returned unchanged")
	}
}
func TestBilevelDecodeInversion(t *testing.T) {
	mk := func(decode object.Object) *object.Stream {
		st := &object.Stream{}
		st.Dict.Set("Width", object.Integer(1))
		st.Dict.Set("Height", object.Integer(1))
		st.Dict.Set("BitsPerComponent", object.Integer(1))
		st.Dict.Set("ColorSpace", object.Name("DeviceGray"))
		if decode != nil {
			st.Dict.Set("Decode", decode)
		}
		return st
	}
	pixel := func(st *object.Stream) uint32 {
		img := &ExtractedImage{Width: 1, Height: 1, ColorSpace: "DeviceGray", BitsPerComponent: 1}
		renderBilevelSamples(core.View{Limits: core.DefaultLimits()}, st, img, []byte{0x80}, "unsupported") // sample bit = 1
		if !img.Decoded || img.Image == nil {
			t.Fatal("bilevel samples should decode")
		}
		r, _, _, _ := img.Image.At(0, 0).RGBA()
		return r >> 8
	}

	if got := pixel(mk(nil)); got != 255 {
		t.Errorf("plain bilevel: pixel = %d, want 255 (white)", got)
	}
	if got := pixel(mk(object.Array{object.Integer(1), object.Integer(0)})); got != 0 {
		t.Errorf("/Decode [1 0]: pixel = %d, want 0 (inverted to black)", got)
	}
}

// TestSamplesToImageRGB pins the raw RGB sample layout. It was an assertion
// inside the root package's extraction test; it belongs with the function.
func TestSamplesToImageRGB(t *testing.T) {
	m, ok := samplesToImage([]byte{255, 0, 0, 0, 255, 0}, 1, 2, 8, "DeviceRGB")
	if !ok {
		t.Fatal("RGB samples should decode")
	}
	if r, _, _, _ := m.At(0, 0).RGBA(); r>>8 != 255 {
		t.Error("RGB pixel wrong")
	}
}
