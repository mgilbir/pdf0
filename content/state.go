package content

import (
	"slices"

	"github.com/mgilbir/pdf0/object"
)

// The part of the graphics state a drawing above the Builder has to ask about:
// the font and size the last Tf selected, and the fill colour. A bitmap font
// drawn as several Type 3 fonts switches between them inside a run and has to
// put the caller's font back afterwards, and one whose glyphs carry the text
// colour has to know it (see docs/proposals/bitmap-fonts-type3.md).
//
// Each is known only once this stream has set it. A page's stream starts from
// the default state, but a form XObject's, a pattern's or an appearance's
// starts from whatever state it is drawn in. A Builder does not know which kind
// of stream it is building, so before a stream sets the font or the colour,
// it reports neither.
//
// q and Q save and restore both, as they save and restore the graphics state.
// BT and ET do not touch them: the text state outlives a text object.

// Color is a fill colour as the stream set it.
type Color struct {
	// Space is the colour space: DeviceGray, DeviceRGB, DeviceCMYK, Pattern,
	// or the name of a /ColorSpace resource.
	Space object.Name
	// Components are the colour's components in Space. They are nil for a
	// pattern without them, and after cs selected a resource space and no
	// colour has been set in it yet, because that space's initial colour
	// depends on what the resource is.
	Components []float64
	// Pattern is the pattern scn named, when Space is Pattern.
	Pattern object.Name
}

// IsDevice reports whether the colour is in DeviceGray, DeviceRGB or
// DeviceCMYK with its components known, which is a colour that can be
// written anywhere without reference to a resource.
func (c Color) IsDevice() bool {
	switch c.Space {
	case "DeviceGray", "DeviceRGB", "DeviceCMYK":
		return c.Components != nil
	}
	return false
}

// tracked is the state a Builder follows, and what q saves.
type tracked struct {
	font    object.Name
	size    float64
	hasFont bool
	fill    Color
	hasFill bool
}

// Font returns the font name and size the last Tf in this stream selected, as
// q and Q have saved and restored them. ok is false before this stream has
// selected a font.
func (b *Builder) Font() (name object.Name, size float64, ok bool) {
	return b.state.font, b.state.size, b.state.hasFont
}

// FillColor returns the fill colour this stream last set, as q and Q have
// saved and restored it. ok is false before this stream has set one. The
// components are a copy.
func (b *Builder) FillColor() (c Color, ok bool) {
	if !b.state.hasFill {
		return Color{}, false
	}
	c = b.state.fill
	c.Components = slices.Clone(c.Components)
	return c, true
}

// setFill records a fill colour an operator has just set.
func (b *Builder) setFill(c Color) {
	if b.err != nil {
		return
	}
	b.state.fill = c
	b.state.hasFill = true
}

// initialComponents is the colour cs selects with a device space: black in
// each, which in DeviceCMYK is K alone (ISO 32000-2 8.6.8, Table 73).
func initialComponents(space object.Name) []float64 {
	switch space {
	case "DeviceGray":
		return []float64{0}
	case "DeviceRGB":
		return []float64{0, 0, 0}
	case "DeviceCMYK":
		return []float64{0, 0, 0, 1}
	}
	return nil
}
