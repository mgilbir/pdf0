package pdf0

import (
	"image"
	"image/color"
	"math"
)

// This file applies a PDF /Decode array to the pixels decoded from a DCTDecode
// (JPEG) image. Go's image/jpeg returns *image.Gray, *image.YCbCr,
// *image.CMYK or *image.NRGBA and already honours the Adobe APP14 colour
// transform, but it knows nothing of the PDF /Decode array, which remaps each
// colour component linearly. The most important case is CMYK JPEGs, which are
// commonly stored inverted and carry /Decode [1 0 1 0 1 0 1 0]; inverted
// grayscale ([1 0]) and RGB ([1 0 1 0 1 0]) are handled likewise.

// jpegDecodeArray reads the image XObject's /Decode array as a slice of floats,
// or nil when the key is absent or malformed.
func jpegDecodeArray(d *Document, st *Stream) []float64 {
	arr, ok := d.Resolve(st.Dict.Get("Decode")).(Array)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]float64, len(arr))
	for i, v := range arr {
		out[i] = floatValue(d.Resolve(v))
	}
	return out
}

// isIdentityDecode reports whether decode is the identity map (every component
// pair is [0 1]), or empty. An identity map leaves the image unchanged.
func isIdentityDecode(decode []float64) bool {
	if len(decode)%2 != 0 {
		return true // malformed: treat as no-op
	}
	for i := 0; i+1 < len(decode); i += 2 {
		if decode[i] != 0 || decode[i+1] != 1 {
			return false
		}
	}
	return true
}

// remapComponent maps an 8-bit sample through one [min max] /Decode pair:
// v in [0,1] becomes min + v*(max-min), clamped back to [0,1] and to 8 bits.
func remapComponent(sample uint8, min, max float64) uint8 {
	v := float64(sample) / 255
	out := min + v*(max-min)
	if out < 0 {
		out = 0
	} else if out > 1 {
		out = 1
	}
	return uint8(math.Round(out * 255))
}

// applyJPEGDecode returns a new image with the /Decode array applied to every
// pixel. The component count must match the image type (1 for Gray, 3 for
// YCbCr/RGB, 4 for CMYK); if it does not, or the map is the identity, the image
// is returned unchanged. CMYK input yields *image.CMYK; everything else yields
// *image.NRGBA.
func applyJPEGDecode(img image.Image, decode []float64) image.Image {
	if isIdentityDecode(decode) {
		return img
	}
	b := img.Bounds()

	switch src := img.(type) {
	case *image.CMYK:
		if len(decode) != 8 {
			return img
		}
		dst := image.NewCMYK(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				si := src.PixOffset(x, y)
				di := dst.PixOffset(x, y)
				dst.Pix[di+0] = remapComponent(src.Pix[si+0], decode[0], decode[1])
				dst.Pix[di+1] = remapComponent(src.Pix[si+1], decode[2], decode[3])
				dst.Pix[di+2] = remapComponent(src.Pix[si+2], decode[4], decode[5])
				dst.Pix[di+3] = remapComponent(src.Pix[si+3], decode[6], decode[7])
			}
		}
		return dst

	case *image.Gray:
		if len(decode) != 2 {
			return img
		}
		dst := image.NewNRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				v := remapComponent(src.GrayAt(x, y).Y, decode[0], decode[1])
				dst.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
			}
		}
		return dst

	default:
		// 3-component RGB: YCbCr, NRGBA, RGBA, etc. Convert each pixel to
		// non-premultiplied 8-bit RGB, remap the three channels, keep alpha.
		if len(decode) != 6 {
			return img
		}
		dst := image.NewNRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
				dst.SetNRGBA(x, y, color.NRGBA{
					R: remapComponent(c.R, decode[0], decode[1]),
					G: remapComponent(c.G, decode[2], decode[3]),
					B: remapComponent(c.B, decode[4], decode[5]),
					A: c.A,
				})
			}
		}
		return dst
	}
}
