package pdf0

import (
	"image"
	"image/draw"
)

// This file applies soft (/SMask) and stencil (/Mask) transparency to images
// that were decoded through a codec path (DCTDecode, and once merged
// CCITTFaxDecode / JBIG2Decode / JPXDecode). buildImage already applies masks
// on the raw/Flate sample path; the codec paths hand back an opaque image, so
// applyImageMasks composites the alpha after the fact.
//
// Colour-key masking (/Mask as an Array) is deliberately NOT handled here: it
// tests the ORIGINAL per-component sample values against a range, and those
// samples are gone once a lossy/opaque codec has produced an image.Image. Only
// a stencil /Mask (a *Stream) and a soft /SMask can be applied post-codec.

// applyImageMasks applies a stencil /Mask and/or a soft /SMask to a codec-
// decoded image. It returns m unchanged when neither is present; otherwise it
// returns a fresh *image.NRGBA with the alpha channel composited in.
func (d *Document) applyImageMasks(st *Stream, m image.Image) image.Image {
	if m == nil {
		return m
	}
	_, hasSMask := d.Resolve(st.Dict.Get("SMask")).(*Stream)
	stencil, hasStencil := d.Resolve(st.Dict.Get("Mask")).(*Stream)
	if !hasSMask && !hasStencil {
		// A colour-key /Mask (an Array) cannot be applied without the original
		// samples, so an image carrying only that is left opaque.
		return m
	}

	// Draw the codec image into a fresh NRGBA so the existing mask helpers,
	// which operate on *image.NRGBA, can composite alpha onto it.
	b := m.Bounds()
	im := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(im, im.Bounds(), m, b.Min, draw.Src)

	if hasStencil {
		_ = stencil
		d.applyStencilMask(st, im)
	}
	if hasSMask {
		d.applySoftMask(st, im)
	}
	return im
}
