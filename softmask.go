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
// gradient, a piece of text, a photograph, a shape. AddForm builds one — with
// Group set, because the form a mask is taken from has to be a transparency
// group (Table 142, /G) — and these turn it into the graphics state the
// content stream names with gs.

// LuminositySoftMask builds a graphics state whose transparency is the
// brightness of a form: white shows what is painted, black hides it, and the
// greys between are partial.
//
// form must be a form XObject of this document that is a transparency group
// with a /CS: the group's colour space is the one the form is composited in
// before its brightness is measured, and a luminosity mask needs one (11.6.5.2).
// AddForm with Group set builds exactly that, in DeviceRGB.
//
// backdrop is the colour the form is composited against, in the group's colour
// space, so it has that space's number of components, each in [0,1]. Nil means
// black, the default, and is the usual choice: it makes an unpainted part of
// the mask hide rather than show — a mask that leaves most of its box
// untouched over a white backdrop would let everything through, which is
// rarely what was meant.
func (d *Document) LuminositySoftMask(form object.IndirectRef, backdrop []float64) (*object.Dictionary, error) {
	group, err := d.maskGroup(form)
	if err != nil {
		return nil, err
	}
	components, unit, err := d.blendingComponents(group.Get("CS"))
	if err != nil {
		return nil, err
	}
	mask := &object.Dictionary{}
	mask.Set("Type", object.Name("Mask"))
	mask.Set("S", object.Name("Luminosity"))
	mask.Set("G", form)
	if backdrop != nil {
		if len(backdrop) != components {
			return nil, fmt.Errorf("pdf0: the backdrop has %d components, and the mask's group colour space %v has %d",
				len(backdrop), group.Get("CS"), components)
		}
		bc := make(object.Array, len(backdrop))
		for i, v := range backdrop {
			check := checkUnit
			if !unit {
				check = checkFinite // Lab components have ranges of their own
			}
			if err := check(fmt.Sprintf("backdrop component %d", i), v); err != nil {
				return nil, err
			}
			bc[i] = numberFor(v)
		}
		mask.Set("BC", bc)
	}
	gs := &object.Dictionary{}
	gs.Set("Type", object.Name("ExtGState"))
	gs.Set("SMask", mask)
	return gs, nil
}

// AlphaSoftMask builds a graphics state whose transparency is the *alpha* of a
// form rather than its brightness: where the form painted, what follows shows.
// form must be a form XObject of this document that is a transparency group.
//
// The difference from a luminosity mask is what is being measured. A luminosity
// mask asks how bright the form is, so a black shape hides; an alpha mask asks
// whether the form painted at all, so a black shape shows. Reaching for the
// wrong one produces a mask that is exactly inverted, which is the mistake this
// pair of names exists to make hard.
func (d *Document) AlphaSoftMask(form object.IndirectRef) (*object.Dictionary, error) {
	if _, err := d.maskGroup(form); err != nil {
		return nil, err
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

// maskGroup checks that form is a form XObject of this document whose /Group
// is a transparency group, which Table 142 requires of a soft mask's /G, and
// returns the group dictionary. A form that is not a group composites each of
// its marks into the page separately, and the mask a reader derives from it
// is undefined.
func (d *Document) maskGroup(form object.IndirectRef) (*object.Dictionary, error) {
	if d == nil {
		return nil, errNilDocument
	}
	stream, ok := d.Resolve(form).(*object.Stream)
	if !ok {
		return nil, fmt.Errorf("pdf0: a soft mask needs a form XObject to take its shape from; object %d is not a stream", form.Number)
	}
	if st, _ := d.Resolve(stream.Dict.Get("Subtype")).(object.Name); st != "Form" {
		return nil, fmt.Errorf("pdf0: a soft mask needs a form XObject; object %d is a %v XObject", form.Number, stream.Dict.Get("Subtype"))
	}
	group := d.ResolveDict(stream.Dict.Get("Group"))
	if group == nil {
		return nil, fmt.Errorf("pdf0: the soft mask's form (object %d) is not a transparency group; "+
			"a mask's form has to be one (ISO 32000-2 Table 142) — build it with Form.Group set", form.Number)
	}
	if s, _ := d.Resolve(group.Get("S")).(object.Name); s != "Transparency" {
		return nil, fmt.Errorf("pdf0: the soft mask's form (object %d) has a /Group of subtype %v, not a transparency group",
			form.Number, group.Get("S"))
	}
	return group, nil
}

// blendingComponents is the number of colour components of a transparency
// group's colour space, which a luminosity mask's backdrop is expressed in, and
// whether each lies in [0,1] (all but Lab's do). The space must be present and
// be one a group may blend in: a device or CIE-based space, not Indexed,
// Pattern or a special one (11.3.4, 11.6.6).
func (d *Document) blendingComponents(cs object.Object) (n int, unit bool, err error) {
	if cs == nil {
		return 0, false, fmt.Errorf("pdf0: a luminosity mask's form group has no /CS; the mask's brightness is measured " +
			"in the group's colour space, and one has to be stated (ISO 32000-2 11.6.5.2)")
	}
	switch v := d.Resolve(cs).(type) {
	case object.Name:
		switch v {
		case "DeviceGray":
			return 1, true, nil
		case "DeviceRGB":
			return 3, true, nil
		case "DeviceCMYK":
			return 4, true, nil
		}
	case object.Array:
		if len(v) == 0 {
			break
		}
		family, _ := d.Resolve(v[0]).(object.Name)
		switch family {
		case "CalGray":
			return 1, true, nil
		case "CalRGB":
			return 3, true, nil
		case "Lab":
			return 3, false, nil
		case "ICCBased":
			if len(v) > 1 {
				if st, ok := d.Resolve(v[1]).(*object.Stream); ok {
					if n, ok := d.Resolve(st.Dict.Get("N")).(object.Integer); ok && (n == 1 || n == 3 || n == 4) {
						return int(n), true, nil
					}
				}
			}
			return 0, false, fmt.Errorf("pdf0: the mask's group colour space is an ICCBased space with no usable /N")
		}
	}
	return 0, false, fmt.Errorf("pdf0: the mask's group colour space %v is not one a transparency group can blend in "+
		"(a device or CIE-based space)", cs)
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
