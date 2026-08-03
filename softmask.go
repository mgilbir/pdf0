package pdf0

import (
	"fmt"

	"github.com/mgilbir/pdf0/object"
)

// Soft masks: making the transparency of a drawing come from another drawing.
//
// Opacity is one number for everything painted under it. A soft mask is a
// picture of the opacity — a form XObject whose luminosity or alpha becomes,
// pixel for pixel, how much of what is painted shows through. It is what a
// gradient fade, a vignette, a drop shadow and a masked image all are
// underneath (ISO 32000-2 11.6.5).
//
// The mask is a form rather than an image so that it can be *anything*: a
// gradient, a piece of text, a photograph, a shape. AddForm builds one, and
// these turn it into the graphics state the content stream names with gs.

// LuminositySoftMask builds a graphics state whose transparency is the
// brightness of a form: white shows what is painted, black hides it, and the
// greys between are partial.
//
// The backdrop is what the form is composited against before its brightness is
// measured, in DeviceRGB. Black is the usual choice and the one that makes an
// unpainted part of the mask hide rather than show — a mask that leaves most of
// its box untouched over a white backdrop would let everything through, which
// is rarely what was meant.
func LuminositySoftMask(form object.Object, backdrop [3]float64) (*object.Dictionary, error) {
	if form == nil {
		return nil, fmt.Errorf("pdf0: a soft mask needs a form XObject to take its shape from")
	}
	for i, v := range backdrop {
		if v < 0 || v > 1 {
			return nil, fmt.Errorf("pdf0: backdrop component %d is %g, outside [0,1]", i, v)
		}
	}
	mask := &object.Dictionary{}
	mask.Set("Type", object.Name("Mask"))
	mask.Set("S", object.Name("Luminosity"))
	mask.Set("G", form)
	mask.Set("BC", object.Array{
		numberFor(backdrop[0]), numberFor(backdrop[1]), numberFor(backdrop[2]),
	})
	gs := &object.Dictionary{}
	gs.Set("Type", object.Name("ExtGState"))
	gs.Set("SMask", mask)
	return gs, nil
}

// AlphaSoftMask builds a graphics state whose transparency is the *alpha* of a
// form rather than its brightness: where the form painted, what follows shows.
//
// The difference from a luminosity mask is what is being measured. A luminosity
// mask asks how bright the form is, so a black shape hides; an alpha mask asks
// whether the form painted at all, so a black shape shows. Reaching for the
// wrong one produces a mask that is exactly inverted, which is the mistake this
// pair of names exists to make hard.
func AlphaSoftMask(form object.Object) (*object.Dictionary, error) {
	if form == nil {
		return nil, fmt.Errorf("pdf0: a soft mask needs a form XObject to take its shape from")
	}
	mask := &object.Dictionary{}
	mask.Set("Type", object.Name("Mask"))
	mask.Set("S", object.Name("Alpha"))
	mask.Set("G", form)
	gs := &object.Dictionary{}
	gs.Set("Type", object.Name("ExtGState"))
	gs.Set("SMask", mask)
	return gs, nil
}

// NoSoftMask builds a graphics state that removes any soft mask in force.
//
// A mask set inside q…Q disappears with the Q, so this is for the case where a
// caller cannot use the stack — and it is the only way to say "no mask" as
// distinct from "a mask that hides nothing".
func NoSoftMask() *object.Dictionary {
	gs := &object.Dictionary{}
	gs.Set("Type", object.Name("ExtGState"))
	gs.Set("SMask", object.Name("None"))
	return gs
}
