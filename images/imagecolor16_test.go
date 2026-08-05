package images

import (
	"github.com/mgilbir/pdf0/object"
	"image"
	"testing"
)

// rgba16 returns the 16-bit RGBA of a pixel. color.Color.RGBA already returns
// 16-bit components (alpha-premultiplied), but for an opaque pixel that equals
// the stored value, so it exposes the full precision an NRGBA64 preserves.
func rgba16(t *testing.T, m image.Image, x, y int) (r, g, b, a uint16) {
	t.Helper()
	rr, gg, bb, aa := m.At(x, y).RGBA()
	return uint16(rr), uint16(gg), uint16(bb), uint16(aa)
}

// near reports whether got is within tol of want (16-bit rounding slack).
func near(got, want, tol uint16) bool {
	if got > want {
		return got-want <= tol
	}
	return want-got <= tol
}

func TestBuildImage16GrayFullPrecision(t *testing.T) {
	// 16-bit gray, three pixels 0xFFFF, 0x8000, 0x0000.
	st := imageXObject(3, 1, 16, "DeviceGray", "", []byte{
		0xFF, 0xFF,
		0x80, 0x00,
		0x00, 0x00,
	})
	m := mustBuild(t, st, 3, 1, 16)
	if _, ok := m.(*image.NRGBA64); !ok {
		t.Fatalf("16bpc image is %T, want *image.NRGBA64", m)
	}
	// 0xFFFF must stay 0xFFFF, not 0xFF00 (byte-promotion would lose the low byte).
	if r, g, b, a := rgba16(t, m, 0, 0); r != 0xFFFF || g != 0xFFFF || b != 0xFFFF || a != 0xFFFF {
		t.Errorf("pixel0 = (%#x,%#x,%#x,%#x), want all 0xFFFF", r, g, b, a)
	}
	// 0x8000 must land near 0x8000 (not 0x8080 from byte replication, not a
	// value rounded through a single byte).
	if r, _, _, _ := rgba16(t, m, 1, 0); !near(r, 0x8000, 2) {
		t.Errorf("pixel1 red = %#x, want ~0x8000", r)
	}
	if r, _, _, _ := rgba16(t, m, 2, 0); r != 0 {
		t.Errorf("pixel2 red = %#x, want 0", r)
	}
}

func TestBuildImage16RGBFullPrecision(t *testing.T) {
	// 16-bit RGB single pixel with distinct per-channel values.
	st := imageXObject(1, 1, 16, "DeviceRGB", "", []byte{
		0xFF, 0xFF, // R = 0xFFFF
		0x80, 0x00, // G ~ 0x8000
		0x12, 0x34, // B = 0x1234
	})
	m := mustBuild(t, st, 1, 1, 16)
	if _, ok := m.(*image.NRGBA64); !ok {
		t.Fatalf("16bpc image is %T, want *image.NRGBA64", m)
	}
	r, g, b, a := rgba16(t, m, 0, 0)
	if r != 0xFFFF {
		t.Errorf("R = %#x, want 0xFFFF", r)
	}
	if !near(g, 0x8000, 2) {
		t.Errorf("G = %#x, want ~0x8000", g)
	}
	if b != 0x1234 {
		t.Errorf("B = %#x, want 0x1234", b)
	}
	if a != 0xFFFF {
		t.Errorf("A = %#x, want 0xFFFF", a)
	}
}

func TestBuildImage16SoftMask(t *testing.T) {
	// A 2x1 16-bit gray base with an 8-bit gray SMask giving alpha 0 then 255.
	base := imageXObject(2, 1, 16, "DeviceGray", "", []byte{0x40, 0x00, 0xC0, 0x00})
	sm := imageXObject(2, 1, 8, "DeviceGray", "", []byte{0, 255})
	base.Dict.Set("SMask", sm)
	m := mustBuild(t, base, 2, 1, 16)
	if _, ok := m.(*image.NRGBA64); !ok {
		t.Fatalf("16bpc image is %T, want *image.NRGBA64", m)
	}
	if _, _, _, a := rgba16(t, m, 0, 0); a != 0 {
		t.Errorf("smask alpha0 = %#x, want 0", a)
	}
	// 8-bit 255 promotes to 0xFFFF.
	if _, _, _, a := rgba16(t, m, 1, 0); a != 0xFFFF {
		t.Errorf("smask alpha1 = %#x, want 0xFFFF", a)
	}
}

func TestBuildImage16StencilMask(t *testing.T) {
	base := imageXObject(2, 1, 16, "DeviceGray", "", []byte{0x40, 0x00, 0xC0, 0x00})
	mk := imageXObject(2, 1, 1, "", "", []byte{0b10000000}) // pixel0 hidden, pixel1 shown
	mk.Dict.Set("ImageMask", object.Boolean(true))
	base.Dict.Set("Mask", mk)
	m := mustBuild(t, base, 2, 1, 16)
	if _, _, _, a := rgba16(t, m, 0, 0); a != 0 {
		t.Errorf("stencil hidden alpha = %#x, want 0", a)
	}
	if _, _, _, a := rgba16(t, m, 1, 0); a != 0xFFFF {
		t.Errorf("stencil shown alpha = %#x, want 0xFFFF", a)
	}
}

func TestBuildImage16Indexed(t *testing.T) {
	// Indexed over DeviceRGB with a 16-bit index sample. 8-bit palette entries
	// promote losslessly to 16-bit (0xFF -> 0xFFFF).
	st := imageXObject(2, 1, 16, "", "", []byte{0x00, 0x00, 0x00, 0x01})
	st.Dict.Set("ColorSpace", object.Array{
		object.Name("Indexed"), object.Name("DeviceRGB"), object.Integer(1),
		object.String{Value: []byte{255, 0, 0, 0, 255, 0}},
	})
	m := mustBuild(t, st, 2, 1, 16)
	if r, g, b, _ := rgba16(t, m, 0, 0); r != 0xFFFF || g != 0 || b != 0 {
		t.Errorf("indexed pixel0 = (%#x,%#x,%#x), want (0xFFFF,0,0)", r, g, b)
	}
	if r, g, b, _ := rgba16(t, m, 1, 0); r != 0 || g != 0xFFFF || b != 0 {
		t.Errorf("indexed pixel1 = (%#x,%#x,%#x), want (0,0xFFFF,0)", r, g, b)
	}
}

func TestBuildImage16CMYK(t *testing.T) {
	// 16-bit CMYK: cyan (C=max) -> R=0, G=0xFFFF, B=0xFFFF.
	st := imageXObject(1, 1, 16, "DeviceCMYK", "", []byte{
		0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	})
	m := mustBuild(t, st, 1, 1, 16)
	if r, g, b, _ := rgba16(t, m, 0, 0); r != 0 || g != 0xFFFF || b != 0xFFFF {
		t.Errorf("CMYK cyan = (%#x,%#x,%#x), want (0,0xFFFF,0xFFFF)", r, g, b)
	}
}
