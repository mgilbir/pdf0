package pdf0

import (
	"fmt"

	"github.com/mgilbir/pdf0/object"
)

// Gradients.
//
// The content builder can paint a shading with sh and can fill with a shading
// pattern, and until now nothing could construct one for it to name. These do:
// a linear gradient along an axis, and a radial one between two circles, which
// are the two forms CSS names and the two ISO 32000-2 8.7.4.5 gives their own
// shading types.
//
// The colour ramp is a PDF function, and the shape of that function is where
// the work is. Two stops are one exponential interpolation; more are a
// stitching function joining one interpolation per interval, with the domain
// split at the stop offsets. Getting the split wrong produces a gradient whose
// colours are right at the ends and wrong everywhere between — which looks
// like a rendering artefact rather than like a bug in the file.

// Stop is one colour stop of a gradient: where it sits along the ramp, and what
// colour the ramp has there.
type Stop struct {
	// Offset is the position along the gradient, 0 at the start and 1 at the
	// end.
	Offset float64
	// Color is the colour in DeviceRGB, each component in [0,1].
	Color [3]float64
}

// LinearGradient builds an axial shading running from (x0, y0) to (x1, y1),
// in the coordinate space the shading is used in.
//
// The gradient is extended past both ends, so a shape larger than the axis is
// filled with the end colours rather than left unpainted — which is what CSS
// does and what a caller almost always means.
func LinearGradient(x0, y0, x1, y1 float64, stops []Stop) (*object.Dictionary, error) {
	fn, err := gradientFunction(stops)
	if err != nil {
		return nil, err
	}
	if x0 == x1 && y0 == y1 {
		return nil, fmt.Errorf("pdf0: a linear gradient needs two distinct points, got (%g, %g) twice", x0, y0)
	}
	sh := &object.Dictionary{}
	sh.Set("ShadingType", object.Integer(2)) // axial
	sh.Set("ColorSpace", object.Name("DeviceRGB"))
	sh.Set("Coords", object.Array{
		numberFor(x0), numberFor(y0), numberFor(x1), numberFor(y1),
	})
	sh.Set("Function", fn)
	sh.Set("Extend", object.Array{object.Boolean(true), object.Boolean(true)})
	return sh, nil
}

// RadialGradient builds a radial shading between two circles: from the one of
// radius r0 at (x0, y0) to the one of radius r1 at (x1, y1).
//
// A CSS radial gradient is the case where the inner circle has radius zero and
// the same centre; the general two-circle form is what PDF offers, and it also
// expresses a cone.
func RadialGradient(x0, y0, r0, x1, y1, r1 float64, stops []Stop) (*object.Dictionary, error) {
	fn, err := gradientFunction(stops)
	if err != nil {
		return nil, err
	}
	if r0 < 0 || r1 < 0 {
		return nil, fmt.Errorf("pdf0: a radial gradient cannot have a negative radius (%g, %g)", r0, r1)
	}
	if r0 == r1 && x0 == x1 && y0 == y1 {
		return nil, fmt.Errorf("pdf0: a radial gradient needs two distinct circles")
	}
	sh := &object.Dictionary{}
	sh.Set("ShadingType", object.Integer(3)) // radial
	sh.Set("ColorSpace", object.Name("DeviceRGB"))
	sh.Set("Coords", object.Array{
		numberFor(x0), numberFor(y0), numberFor(r0),
		numberFor(x1), numberFor(y1), numberFor(r1),
	})
	sh.Set("Function", fn)
	sh.Set("Extend", object.Array{object.Boolean(true), object.Boolean(true)})
	return sh, nil
}

// ShadingPattern wraps a shading as a pattern, which is what fills a path with
// a gradient rather than painting the whole clip region.
//
// The two ways of using a shading are genuinely different. sh paints everywhere
// the clip allows, ignoring the current path; a pattern is a colour, selected
// in the /Pattern colour space and used by any fill. Filling a rounded
// rectangle with a gradient is the second.
func ShadingPattern(shading object.Object) *object.Dictionary {
	p := &object.Dictionary{}
	p.Set("Type", object.Name("Pattern"))
	p.Set("PatternType", object.Integer(2)) // shading pattern
	p.Set("Shading", shading)
	return p
}

// gradientFunction builds the PDF function that maps a position along the
// gradient to a colour.
//
// Two stops need one exponential interpolation. More need a stitching function
// (ISO 32000-2 7.10.4) holding one interpolation per interval, with /Bounds at
// the interior offsets and an /Encode pair per interval mapping it back onto
// [0,1] — without which each segment would sample its interpolation over the
// wrong part of its range and the colours between the stops would be wrong.
func gradientFunction(stops []Stop) (object.Object, error) {
	if len(stops) < 2 {
		return nil, fmt.Errorf("pdf0: a gradient needs at least two colour stops, got %d", len(stops))
	}
	prev := -1.0
	for i, s := range stops {
		if s.Offset < 0 || s.Offset > 1 {
			return nil, fmt.Errorf("pdf0: colour stop %d is at %g, outside [0,1]", i, s.Offset)
		}
		if s.Offset < prev {
			return nil, fmt.Errorf("pdf0: colour stop %d is at %g, before the one before it at %g", i, s.Offset, prev)
		}
		prev = s.Offset
		for c, v := range s.Color {
			if v < 0 || v > 1 {
				return nil, fmt.Errorf("pdf0: colour stop %d component %d is %g, outside [0,1]", i, c, v)
			}
		}
	}
	if stops[0].Offset != 0 || stops[len(stops)-1].Offset != 1 {
		return nil, fmt.Errorf("pdf0: a gradient's stops must run from 0 to 1, got %g to %g",
			stops[0].Offset, stops[len(stops)-1].Offset)
	}

	if len(stops) == 2 {
		return interpolation(stops[0].Color, stops[1].Color), nil
	}

	fns := make(object.Array, 0, len(stops)-1)
	bounds := make(object.Array, 0, len(stops)-2)
	encode := make(object.Array, 0, 2*(len(stops)-1))
	for i := 0; i+1 < len(stops); i++ {
		fns = append(fns, interpolation(stops[i].Color, stops[i+1].Color))
		if i > 0 {
			bounds = append(bounds, numberFor(stops[i].Offset))
		}
		// Each sub-function is sampled over the whole of its own domain,
		// whatever slice of the gradient it covers.
		encode = append(encode, object.Integer(0), object.Integer(1))
	}
	stitch := &object.Dictionary{}
	stitch.Set("FunctionType", object.Integer(3))
	stitch.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
	stitch.Set("Functions", fns)
	stitch.Set("Bounds", bounds)
	stitch.Set("Encode", encode)
	return stitch, nil
}

// interpolation is a type 2 function running from one colour to another.
func interpolation(from, to [3]float64) *object.Dictionary {
	f := &object.Dictionary{}
	f.Set("FunctionType", object.Integer(2))
	f.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
	f.Set("C0", object.Array{numberFor(from[0]), numberFor(from[1]), numberFor(from[2])})
	f.Set("C1", object.Array{numberFor(to[0]), numberFor(to[1]), numberFor(to[2])})
	f.Set("N", object.Integer(1)) // linear
	return f
}
