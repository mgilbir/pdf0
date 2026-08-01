package pdf0

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// jpegWithDecode builds a document holding a single DCTDecode image XObject with
// the given /Decode array, extracts it, and returns the decoded image.
// abs is a local helper: the one in the filters code moved to internal/core with
// the Paeth predictor that needed it, and exporting it from there to serve a
// test comparison would put an integer utility in a filter package's API.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func jpegWithDecode(t *testing.T, w, h, bpc int, cs string, jb []byte, decode Array) image.Image {
	t.Helper()
	st := imageXObject(w, h, bpc, cs, "DCTDecode", jb)
	if decode != nil {
		st.Dict.Set("Decode", decode)
	}
	d := imageDoc(map[string]*Stream{"Jpeg": st})
	imgs := d.ExtractImages()
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image, got %d", len(imgs))
	}
	if !imgs[0].Decoded || imgs[0].Image == nil {
		t.Fatalf("JPEG not decoded: %+v", imgs[0])
	}
	return imgs[0].Image
}

func gray8At(t *testing.T, m image.Image, x, y int) int {
	t.Helper()
	r, _, _, _ := m.At(x, y).RGBA()
	return int(r >> 8)
}

// TestJPEGDecodeInvertGray checks that /Decode [1 0] inverts a grayscale JPEG
// relative to the identity control. JPEG is lossy, so compare with tolerance.
func TestJPEGDecodeInvertGray(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			src.SetGray(x, y, color.Gray{Y: byte(x * 32)})
		}
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}

	ctrl := jpegWithDecode(t, 8, 8, 8, "DeviceGray", jb.Bytes(), nil)
	inv := jpegWithDecode(t, 8, 8, 8, "DeviceGray", jb.Bytes(), Array{Integer(1), Integer(0)})

	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			c := gray8At(t, ctrl, x, y)
			got := gray8At(t, inv, x, y)
			want := 255 - c
			if abs(got-want) > 8 {
				t.Fatalf("at (%d,%d): control=%d inverted=%d want~%d", x, y, c, got, want)
			}
		}
	}
}

// TestJPEGDecodeInvertRGB checks that /Decode [1 0 1 0 1 0] inverts an RGB JPEG.
func TestJPEGDecodeInvertRGB(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			src.SetRGBA(x, y, color.RGBA{R: byte(x * 32), G: byte(y * 32), B: 100, A: 255})
		}
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}

	ctrl := jpegWithDecode(t, 8, 8, 8, "DeviceRGB", jb.Bytes(), nil)
	inv := jpegWithDecode(t, 8, 8, 8, "DeviceRGB", jb.Bytes(),
		Array{Integer(1), Integer(0), Integer(1), Integer(0), Integer(1), Integer(0)})

	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			cr, cg, cb, _ := ctrl.At(x, y).RGBA()
			gr, gg, gb, _ := inv.At(x, y).RGBA()
			for _, p := range [][2]int{
				{int(gr >> 8), 255 - int(cr>>8)},
				{int(gg >> 8), 255 - int(cg>>8)},
				{int(gb >> 8), 255 - int(cb>>8)},
			} {
				if abs(p[0]-p[1]) > 8 {
					t.Fatalf("at (%d,%d): got %d want~%d", x, y, p[0], p[1])
				}
			}
		}
	}
}

// TestJPEGDecodeIdentityUnchanged checks that an identity /Decode leaves the
// decoded image untouched (same concrete type, same pixels).
func TestJPEGDecodeIdentityUnchanged(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 4, 4))
	for i := range src.Pix {
		src.Pix[i] = byte(i * 15)
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	plain := jpegWithDecode(t, 4, 4, 8, "DeviceGray", jb.Bytes(), nil)
	ident := jpegWithDecode(t, 4, 4, 8, "DeviceGray", jb.Bytes(), Array{Integer(0), Integer(1)})
	if _, ok := ident.(*image.Gray); !ok {
		t.Fatalf("identity /Decode should leave *image.Gray unchanged, got %T", ident)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if gray8At(t, plain, x, y) != gray8At(t, ident, x, y) {
				t.Fatalf("identity /Decode changed pixel at (%d,%d)", x, y)
			}
		}
	}
}

// TestApplyJPEGDecodeCMYK unit-tests the CMYK inversion path exactly, with no
// JPEG round-trip: /Decode [1 0 1 0 1 0 1 0] flips every channel.
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
