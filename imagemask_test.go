package pdf0

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// jpegBytes encodes a solid-grey WxH image as JPEG.
// rgb8 is repeated from the images package's colour tests: a test helper
// cannot cross a package boundary.
// rgb8 returns the 8-bit RGBA of a pixel.
func rgb8(t *testing.T, m image.Image, x, y int) (r, g, b, a uint8) {
	t.Helper()
	rr, gg, bb, aa := m.At(x, y).RGBA()
	return uint8(rr >> 8), uint8(gg >> 8), uint8(bb >> 8), uint8(aa >> 8)
}

func jpegBytes(t *testing.T, w, h int, gray byte) []byte {
	t.Helper()
	src := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src.SetGray(x, y, color.Gray{Y: gray})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// nrgbaPix returns the raw (non-premultiplied) R,G,B,A bytes of a pixel.
func nrgbaPix(m *image.NRGBA, x, y int) [4]byte {
	o := m.PixOffset(x, y)
	return [4]byte{m.Pix[o], m.Pix[o+1], m.Pix[o+2], m.Pix[o+3]}
}

// extractFirst extracts the single named image from a one-image doc.
func extractFirst(t *testing.T, st *Stream) ExtractedImage {
	t.Helper()
	d := imageDoc(map[string]*Stream{"Img": st})
	imgs := d.ExtractImages()
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image, got %d", len(imgs))
	}
	return imgs[0]
}

// TestMaskDCTSoftMask: a JPEG image with a grey /SMask gets per-pixel alpha
// applied post-decode. Alpha is exact even though JPEG colour is lossy.
func TestMaskDCTSoftMask(t *testing.T) {
	base := imageXObject(2, 1, 8, "DeviceGray", "DCTDecode", jpegBytes(t, 2, 1, 128))
	sm := imageXObject(2, 1, 8, "DeviceGray", "", []byte{0, 255}) // alpha 0 then 255
	base.Dict.Set("SMask", sm)

	im := extractFirst(t, base)
	if !im.Decoded {
		t.Fatal("JPEG+SMask image not decoded")
	}
	nrgba, ok := im.Image.(*image.NRGBA)
	if !ok {
		t.Fatalf("expected *image.NRGBA, got %T", im.Image)
	}
	if _, _, _, a := rgb8(t, nrgba, 0, 0); a != 0 {
		t.Errorf("smask alpha0 = %d, want 0", a)
	}
	if _, _, _, a := rgb8(t, nrgba, 1, 0); a != 255 {
		t.Errorf("smask alpha1 = %d, want 255", a)
	}
	// Colour survives (roughly) the round trip: mid grey.
	if r, _, _, _ := rgb8(t, nrgba, 1, 0); r < 108 || r > 148 {
		t.Errorf("smask pixel1 grey = %d, want ~128", r)
	}
}

// TestMaskDCTStencilMask: a JPEG image with a 1-bit stencil /Mask hides pixels.
func TestMaskDCTStencilMask(t *testing.T) {
	base := imageXObject(2, 1, 8, "DeviceGray", "DCTDecode", jpegBytes(t, 2, 1, 200))
	mk := imageXObject(2, 1, 1, "", "", []byte{0b10000000}) // pixel0=1 hides, pixel1=0 shows
	mk.Dict.Set("ImageMask", Boolean(true))
	base.Dict.Set("Mask", mk)

	im := extractFirst(t, base)
	nrgba, ok := im.Image.(*image.NRGBA)
	if !ok {
		t.Fatalf("expected *image.NRGBA, got %T", im.Image)
	}
	if _, _, _, a := rgb8(t, nrgba, 0, 0); a != 0 {
		t.Errorf("stencil hidden pixel alpha = %d, want 0", a)
	}
	if _, _, _, a := rgb8(t, nrgba, 1, 0); a != 255 {
		t.Errorf("stencil shown pixel alpha = %d, want 255", a)
	}
}

// TestMaskDCTNoMaskUnchanged: a plain JPEG passes through untouched (not
// forced to NRGBA) so masks aren't spuriously applied.
func TestMaskDCTNoMaskUnchanged(t *testing.T) {
	base := imageXObject(2, 1, 8, "DeviceGray", "DCTDecode", jpegBytes(t, 2, 1, 128))
	im := extractFirst(t, base)
	if !im.Decoded {
		t.Fatal("plain JPEG not decoded")
	}
	// jpeg.Decode of a grayscale image returns *image.Gray; applyImageMasks
	// must return it unchanged (no NRGBA conversion) when there is no mask.
	if _, isNRGBA := im.Image.(*image.NRGBA); isNRGBA {
		t.Errorf("plain JPEG should not be converted to NRGBA")
	}
}

// TestMaskColorKeyIgnoredForCodec: a colour-key /Mask (Array) on a codec image
// is skipped (raw samples unavailable), leaving the image opaque and unchanged.
func TestMaskColorKeyIgnoredForCodec(t *testing.T) {
	base := imageXObject(2, 1, 8, "DeviceGray", "DCTDecode", jpegBytes(t, 2, 1, 128))
	base.Dict.Set("Mask", Array{Integer(0), Integer(255)}) // colour-key range
	im := extractFirst(t, base)
	if _, isNRGBA := im.Image.(*image.NRGBA); isNRGBA {
		t.Errorf("colour-key /Mask on a codec image should be ignored (no NRGBA conversion)")
	}
}

// TestApplyImageMasks exercises applyImageMasks directly on a hand-built RGBA.
