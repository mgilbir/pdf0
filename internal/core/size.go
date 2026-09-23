package core

import (
	"fmt"
	"math"

	"github.com/mgilbir/pdf0/internal/checked"
)

// The image budget.
//
// Every buffer or loop sized from an image's geometry — whatever the codec, and
// wherever the geometry came from (the image dictionary, a fax stream's
// parameters, a JPEG or JPEG 2000 header, a soft mask's own dictionary) — is
// held to one budget, Limits.ImagePixels, through CheckImage. The product is
// formed by checked.Mul, so a dimension of 2^60 is refused rather than wrapped
// (audit 2026-09-22 C11).

// DefaultMaxImagePixels is the largest decoded image, in pixels, that
// extraction will build. It is the same bound the encoder applies to images a
// caller embeds (images.MaxPixels), so pdf0 never refuses to read back an image
// it would itself have written. 1<<26 is 67 megapixels: an A4 page scanned at
// 600 dpi is 35.
const DefaultMaxImagePixels = 1 << 26

// ImageSampleFactor is how many samples per pixel the budget allows before it
// counts extra components as extra pixels. Four is CMYK, or RGB with alpha: an
// ordinary image at the pixel budget passes, and a 32-component DeviceN or a
// JPEG 2000 codestream declaring thousands of components does not get the same
// pixel count multiplied by its component count for free.
const ImageSampleFactor = 4

// LimitError is a budget refusing work before it was done. It says which guard,
// what was asked for, and the bound, and whether the bound was pdf0's default
// or one the caller configured.
type LimitError struct {
	Guard string
	What  string
	Unit  string
	// Need is the size that was asked for, or -1 when it is not known or did
	// not fit in an int64 at all.
	Need  int64
	Bound int64
	def   int64
}

func (e *LimitError) Error() string {
	prov := "pdf0's default"
	if e.Bound != e.def {
		prov = "configured by the caller"
	}
	if e.Need < 0 {
		return fmt.Sprintf("resource limit reached (%s): %s exceeds the bound of %d %s (%s)", e.Guard, e.What, e.Bound, e.Unit, prov)
	}
	return fmt.Sprintf("resource limit reached (%s): %s needs %d %s, over the bound of %d (%s)", e.Guard, e.What, e.Need, e.Unit, e.Bound, prov)
}

// ImageBudgetError is the error for an image a codec found over the pixel
// budget part-way through, when the size it would have reached is not known —
// a fax stream with no /Rows, a JBIG2 stream whose regions add up.
func (l Limits) ImageBudgetError(what string) error {
	return &LimitError{Guard: GuardImagePixels, What: what, Unit: "pixels", Need: -1, Bound: l.ImagePixelBound(), def: DefaultMaxImagePixels}
}

// ImagePixelBound is the resolved pixel budget.
func (l Limits) ImagePixelBound() int64 { return l.WithDefaults().ImagePixels }

// ImageSampleBound is the resolved sample budget: ImageSampleFactor samples per
// pixel of the pixel budget, saturating rather than wrapping.
func (l Limits) ImageSampleBound() int64 {
	b, ok := checked.Mul(l.ImagePixelBound(), ImageSampleFactor)
	if !ok {
		return math.MaxInt64
	}
	return b
}

// CheckImage holds a decoded image of w×h pixels with ncomp components to the
// image budget. It returns nil when the image may be built, and a *LimitError
// when it may not — including when a dimension is negative or the product
// overflows, which are the cases the budget exists for.
//
// Call it before the allocation, with the dimensions the allocation will use.
// A caller holding dimensions from two sources (a dictionary and a codec
// header) checks the ones it allocates from.
func (l Limits) CheckImage(w, h, ncomp int64) error {
	bound := l.ImagePixelBound()
	what := fmt.Sprintf("a %d×%d image", w, h)
	px, ok := checked.Mul(w, h)
	if !ok {
		px = -1
	}
	if !ok || px > bound {
		return &LimitError{Guard: GuardImagePixels, What: what, Unit: "pixels", Need: px, Bound: bound, def: DefaultMaxImagePixels}
	}
	if ncomp > ImageSampleFactor {
		sbound := l.ImageSampleBound()
		samples, ok := checked.Mul(px, ncomp)
		if !ok {
			samples = -1
		}
		if !ok || samples > sbound {
			return &LimitError{
				Guard: GuardImagePixels,
				What:  fmt.Sprintf("%s of %d components", what, ncomp),
				Unit:  "samples",
				Need:  samples, Bound: sbound, def: DefaultMaxImagePixels * ImageSampleFactor,
			}
		}
	}
	return nil
}
