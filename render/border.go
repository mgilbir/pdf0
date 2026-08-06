package render

import (
	"strings"

	"github.com/mgilbir/pdf0/style"
)

// Border styles: what a border looks like as well as how wide it is.
//
// Layout only ever asks a border how much room it takes, and every style takes
// the same room. Painting is where they differ, and until now every one of them
// was drawn as a solid band — so "dashed" and "dotted" produced a page that was
// wrong in a way an author sees immediately and a test suite sees as a hundred
// failures.
//
// Each style below is decomposed into filled rectangles rather than expressed as
// a stroke with a dash pattern. That is the same decision the display list makes
// everywhere: a backend that had to understand border-style would be a second
// renderer, and PDF's own dash pattern cannot express the mitred corners or the
// two tones the 3-D styles need.

// borderStyle is what a border-*-style value means for painting.
type borderStyle uint8

const (
	borderNone borderStyle = iota
	borderSolid
	borderDouble
	borderDashed
	borderDotted
	// The four that are drawn in two tones. They differ only in which edges get
	// the light one, which is what makes a groove look cut in and a ridge look
	// raised.
	borderGroove
	borderRidge
	borderInset
	borderOutset
	// borderHidden draws nothing, exactly as borderNone does, and is a separate
	// value because §17.6.2.1 makes it the strongest thing an author can say: a
	// hidden border on any of the boxes meeting at a collapsed grid line
	// suppresses the line entirely, beating even a wider one. Folding it onto
	// none — which is what "they both draw nothing" invites — loses the only
	// way there is to punch a hole in a collapsed table's grid.
	borderHidden
)

func parseBorderStyle(value string) borderStyle {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "hidden":
		return borderHidden
	case "solid":
		return borderSolid
	case "double":
		return borderDouble
	case "dashed":
		return borderDashed
	case "dotted":
		return borderDotted
	case "groove":
		return borderGroove
	case "ridge":
		return borderRidge
	case "inset":
		return borderInset
	case "outset":
		return borderOutset
	}
	return borderNone
}

// side names an edge, which the 3-D styles need because they light two edges and
// darken the other two.
type side uint8

const (
	sideTop side = iota
	sideRight
	sideBottom
	sideLeft
)

// paintEdge draws one border edge in its style.
//
// band is the rectangle the edge occupies, already mitred by the caller: the top
// and bottom take the full width and the sides take what is left, which is right
// for a border of one colour and is where a per-corner implementation would
// start.
func (p *painter) paintEdge(band Rect, kind borderStyle, colour style.RGBA, s side, thickness style.Unit) {
	if band.Empty() || colour.A == 0 {
		return
	}
	horizontal := s == sideTop || s == sideBottom

	switch kind {
	case borderNone, borderHidden:
		// Neither draws anything. Both already have a width of zero, so this is
		// unreachable through an ordinary box — but the collapsing model chooses
		// a style and a width from different boxes, and a default that filled the
		// band solid would turn a suppressed edge into a black rule.
		return

	case borderSolid:
		p.ops = append(p.ops, FillRect{Rect: band, Color: colour})

	case borderDouble:
		// Two lines with a gap, each a third of the width. A double border
		// narrower than three units cannot show all three parts, and the
		// specification says to draw it solid rather than to lose a line.
		third := thickness.Div(3)
		if third <= 0 {
			p.ops = append(p.ops, FillRect{Rect: band, Color: colour})
			return
		}
		if horizontal {
			p.ops = append(p.ops,
				FillRect{Rect: Rect{band.X, band.Y, band.W, third}, Color: colour},
				FillRect{Rect: Rect{band.X, band.Bottom().Sub(third), band.W, third}, Color: colour})
		} else {
			p.ops = append(p.ops,
				FillRect{Rect: Rect{band.X, band.Y, third, band.H}, Color: colour},
				FillRect{Rect: Rect{band.Right().Sub(third), band.Y, third, band.H}, Color: colour})
		}

	case borderDashed, borderDotted:
		p.paintDashes(band, colour, horizontal, thickness, kind == borderDotted)

	case borderGroove, borderRidge, borderInset, borderOutset:
		p.paint3D(band, colour, kind, s, thickness, horizontal)

	default:
		p.ops = append(p.ops, FillRect{Rect: band, Color: colour})
	}
}

// paintDashes fills an edge with a run of marks.
//
// The dash length is three times the border width and a dot is one — the ratios
// every renderer uses, since the specification leaves them open. The marks are
// spread so that one lands at each end, which is what keeps a corner from
// looking chewed.
func (p *painter) paintDashes(band Rect, colour style.RGBA, horizontal bool,
	thickness style.Unit, dotted bool) {

	unit := thickness
	if !dotted {
		unit = thickness.Mul(3)
	}
	if unit <= 0 {
		p.ops = append(p.ops, FillRect{Rect: band, Color: colour})
		return
	}

	length := band.W
	if !horizontal {
		length = band.H
	}
	// A mark and the gap after it. Fitting a whole number of them across the
	// edge is what puts one at each end.
	period := unit.Mul(2)
	count := int(length.Px() / period.Px())
	if count < 1 {
		p.ops = append(p.ops, FillRect{Rect: band, Color: colour})
		return
	}
	step := length.Div(float64(count))
	mark := step.Div(2)

	for i := 0; i < count; i++ {
		at := step.Mul(float64(i))
		var r Rect
		if horizontal {
			r = Rect{band.X.Add(at), band.Y, mark, band.H}
		} else {
			r = Rect{band.X, band.Y.Add(at), band.W, mark}
		}
		p.ops = append(p.ops, FillRect{Rect: r, Color: colour})
	}
}

// paint3D draws the four styles that use two tones.
//
// groove and ridge split the edge in half and put a different tone on each,
// which is what makes one look cut into the page and the other raised from it.
// inset and outset use one tone for the whole edge, light or dark depending on
// which side it is — the top and left of an inset box are the shadowed ones.
func (p *painter) paint3D(band Rect, colour style.RGBA, kind borderStyle,
	s side, thickness style.Unit, horizontal bool) {

	dark := shade(colour, 0.5)
	light := colour

	topLeft := s == sideTop || s == sideLeft
	switch kind {
	case borderInset:
		if topLeft {
			p.ops = append(p.ops, FillRect{Rect: band, Color: dark})
		} else {
			p.ops = append(p.ops, FillRect{Rect: band, Color: light})
		}
	case borderOutset:
		if topLeft {
			p.ops = append(p.ops, FillRect{Rect: band, Color: light})
		} else {
			p.ops = append(p.ops, FillRect{Rect: band, Color: dark})
		}
	default:
		// groove and ridge: two halves, and which half is dark is what tells
		// them apart.
		outer, inner := dark, light
		if kind == borderRidge {
			outer, inner = light, dark
		}
		if !topLeft {
			outer, inner = inner, outer
		}
		half := thickness.Div(2)
		if half <= 0 {
			p.ops = append(p.ops, FillRect{Rect: band, Color: outer})
			return
		}
		if horizontal {
			p.ops = append(p.ops,
				FillRect{Rect: Rect{band.X, band.Y, band.W, half}, Color: outer},
				FillRect{Rect: Rect{band.X, band.Y.Add(half), band.W, band.H.Sub(half)}, Color: inner})
		} else {
			p.ops = append(p.ops,
				FillRect{Rect: Rect{band.X, band.Y, half, band.H}, Color: outer},
				FillRect{Rect: Rect{band.X.Add(half), band.Y, band.W.Sub(half), band.H}, Color: inner})
		}
	}
}

// shade darkens a colour towards black by a factor.
//
// The specification leaves the two tones of a 3-D border to the renderer and
// says only that they must differ. Half the brightness is what browsers settled
// on, and it keeps a black border visible: a border that darkened to nothing
// would vanish on exactly the colour authors use most.
func shade(c style.RGBA, factor float64) style.RGBA {
	if c.R == 0 && c.G == 0 && c.B == 0 {
		// Black cannot be darkened, so the second tone is a lightening instead.
		return style.RGBA{R: 128, G: 128, B: 128, A: c.A}
	}
	return style.RGBA{R: c.R * factor, G: c.G * factor, B: c.B * factor, A: c.A}
}
