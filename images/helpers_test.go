package images

import (
	"image"

	"github.com/mgilbir/pdf0/object"
)

// imageXObject makes an image XObject dictionary + stream.
//
// The root package has an identical helper for its own image tests. A test
// helper cannot cross a package boundary, and duplicating ten lines of
// dictionary construction is cheaper than exporting a fixture builder from a
// package whose API is otherwise two symbols.
func imageXObject(w, h, bpc int, cs, filter string, data []byte) *object.Stream {
	d := object.Dictionary{}
	d.Set("Type", object.Name("XObject"))
	d.Set("Subtype", object.Name("Image"))
	d.Set("Width", object.Integer(w))
	d.Set("Height", object.Integer(h))
	d.Set("BitsPerComponent", object.Integer(bpc))
	if cs != "" {
		d.Set("ColorSpace", object.Name(cs))
	}
	if filter != "" {
		d.Set("Filter", object.Name(filter))
	}
	return &object.Stream{Dict: d, Data: data}
}

// nrgbaPix returns the raw (non-premultiplied) R,G,B,A bytes of a pixel.
func nrgbaPix(m *image.NRGBA, x, y int) [4]byte {
	o := m.PixOffset(x, y)
	return [4]byte{m.Pix[o], m.Pix[o+1], m.Pix[o+2], m.Pix[o+3]}
}

// nrgbaPix is repeated from the root package's mask tests for the same reason
// as imageXObject above.
