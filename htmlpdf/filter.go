package htmlpdf

import (
	"fmt"
	"math"

	"github.com/mgilbir/forme/layout"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
)

// Filters: forme's FilterGroup, a group of operations filtered as one.
//
// forme folds into the marks every filter it can apply to them exactly —
// opacity where the marks do not overlap, a colour function over solid
// colours, a drop shadow of marks as more marks — and leaves a group only for
// what it could not (Filter Effects 1 §5, forme's filter.go and
// filtercolour.go). So what arrives here is what has to be applied to the
// group's result, pixel by pixel, and a PDF page can do that for two of the
// four functions exactly:
//
//   - opacity() is a transparency group (ISO 32000-2 11.4, 11.6.4.4): the
//     operations drawn into a form XObject whose /Group is an isolated
//     transparency group, painted with /ca and /CA at the amount. The group's
//     result is faded as a whole, which is what makes overlapping marks
//     composite before they are faded and not after.
//   - drop-shadow() with no blur is the group's alpha, moved by the offset,
//     flooded with the shadow's colour and composited under the group
//     (Filter Effects 1 §13.1.10): an alpha soft mask (11.6.5.2) of the group
//     drawn at the offset, a fill of the colour at its own alpha over the
//     sheet through that mask, and the group over it, all in one group.
//
// The other two are refused, because PDF has no way to say them:
//
//   - blur(), and a drop shadow's blur: a Gaussian convolution of the group's
//     pixels, for which PDF has no operator or filter. Rasterising the group
//     would put a picture of vector content in the file at a resolution this
//     backend would have to choose, which is not the page forme composed.
//   - the colour-matrix functions (brightness, contrast, grayscale,
//     hue-rotate, invert, saturate, sepia) that forme could not fold into the
//     marks, which leaves it only where the group holds a picture or overlaps
//     itself: each pixel's colour times a matrix. PDF's blend modes are fixed
//     functions of two colours and its transfer functions act on device
//     colour, and no combination of them is that matrix.
//
// A chain is applied in order, each function to the result of the one before,
// so each step is a group of its own drawing the step before.

// filterUndrawable is why a filter group cannot be written, or "".
func filterUndrawable(g layout.FilterGroup) string {
	for _, f := range g.Filters {
		switch f.Kind {
		case layout.FilterOpacity:
			if !(f.Amount >= 0 && f.Amount <= 1) {
				return fmt.Sprintf("an opacity of %g, which is no opacity", f.Amount)
			}
		case layout.FilterBlur:
			if f.StdDev > 0 {
				return fmt.Sprintf("a blur of standard deviation %gpx (filter: blur()), which PDF has no "+
					"operation for", f.StdDev.Px())
			}
		case layout.FilterDropShadow:
			if f.StdDev > 0 {
				return fmt.Sprintf("a drop shadow blurred by a standard deviation of %gpx, which PDF has no "+
					"operation for", f.StdDev.Px())
			}
			for _, v := range []float64{f.Color.R, f.Color.G, f.Color.B, f.Color.A} {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return "a drop shadow whose colour is not a number"
				}
			}
		case layout.FilterColorMatrix:
			return "a colour-matrix filter (brightness, contrast, grayscale, hue-rotate, invert, saturate " +
				"or sepia) over a picture or overlapping marks, which PDF has no operation for"
		default:
			return fmt.Sprintf("a filter function of a kind (%d) this backend does not know", f.Kind)
		}
	}
	return ""
}

// filterGroup draws a filter group: its operations as a transparency group,
// each function of its chain a group of its own over the step before, and the
// result through the group's clip.
func (c *canvas) filterGroup(g layout.FilterGroup) error {
	if len(g.Ops) == 0 {
		return nil
	}
	step, err := c.group(func(fc *canvas) error { return fc.drawOps(g.Ops) })
	if err != nil {
		return err
	}
	for _, f := range g.Filters {
		f := f
		switch f.Kind {
		case layout.FilterOpacity:
			if !(f.Amount > 0) {
				return nil // faded to nothing: the group paints nothing
			}
			inner := step
			step, err = c.group(func(fc *canvas) error {
				name := fc.xobject(inner)
				fc.b.Save()
				fc.alphas.use(fc.b, f.Amount)
				fc.b.Draw(name)
				fc.b.Restore()
				return nil
			})
		case layout.FilterDropShadow:
			inner := step
			step, err = c.group(func(fc *canvas) error { return fc.dropShadow(inner, f) })
		default:
			// A blur of nothing; checkDrawable refused every other kind.
		}
		if err != nil {
			return err
		}
	}
	b := c.b
	b.Save()
	clipTo(b, g.Clip)
	b.Draw(c.xobject(step))
	b.Restore()
	return nil
}

// dropShadow draws a sharp drop shadow of a group and the group over it.
func (c *canvas) dropShadow(inner object.IndirectRef, f layout.FilterFunction) error {
	b := c.b
	if f.Color.A > 0 {
		// The shadow's shape: the group's alpha, drawn at the offset.
		mask, err := c.group(func(mc *canvas) error {
			mc.b.Save()
			mc.b.Translate(f.Offset.X.Px(), f.Offset.Y.Px())
			mc.b.Draw(mc.xobject(inner))
			mc.b.Restore()
			return nil
		})
		if err != nil {
			return err
		}
		gs, err := c.w.doc.AlphaSoftMask(mask)
		if err != nil {
			return fmt.Errorf("writing a drop shadow's mask: %w", err)
		}
		name := object.Name(fmt.Sprintf("SM%d", len(c.states)+1))
		c.states[name] = gs
		c.w.transparent = true
		b.Save()
		b.SetExtGState(name)
		c.alphas.use(b, f.Color.A)
		b.SetRGB(f.Color.R/255, f.Color.G/255, f.Color.B/255)
		s := c.sheet()
		b.Rect(s[0], s[1], s[2]-s[0], s[3]-s[1])
		b.Fill()
		b.Restore()
	}
	b.Draw(c.xobject(inner))
	return nil
}

// group draws into a new form XObject, an isolated transparency group in
// layout's coordinates as large as the sheet, and returns it.
func (c *canvas) group(draw func(fc *canvas) error) (object.IndirectRef, error) {
	fc := c.w.newCanvas([6]float64{1, 0, 0, 1, 0, 0})
	// A group inside a curved clip is still inside it: a link drawn in the
	// group is refused as one drawn beside it is. A form is painted in the
	// coordinates in force, so inside a TransformGroup its own are the
	// group's.
	fc.curved = c.curved
	fc.toLayout = c.toLayout
	if err := draw(fc); err != nil {
		return object.IndirectRef{}, err
	}
	c.w.transparent = true
	ref, err := c.w.doc.AddForm(pdf0.Form{
		BBox:       c.sheet(),
		Content:    fc.b,
		Group:      true,
		Faces:      fc.fonts,
		XObjects:   fc.xobjects,
		Patterns:   fc.patterns,
		Shadings:   fc.shadings,
		ExtGStates: fc.states,
	})
	if err != nil {
		return object.IndirectRef{}, fmt.Errorf("writing a filter group: %w", err)
	}
	return ref, nil
}

// xobject is the name this stream shows a form under.
func (c *canvas) xobject(ref object.IndirectRef) object.Name {
	for name, r := range c.xobjects {
		if r == object.Object(ref) {
			return name
		}
	}
	name := object.Name(fmt.Sprintf("Fm%d", len(c.xobjects)+1))
	c.xobjects[name] = ref
	return name
}
