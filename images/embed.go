package images

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image"

	"github.com/mgilbir/pdf0/object"
)

// Embedding an image as a PDF XObject: the inverse of what the rest of this
// package does.
//
// The two directions check each other. Whatever Embed writes, Walk reads back,
// and the pixels must survive the trip — which is a far stronger statement than
// "the dictionary has the right keys", and it is the test this code is held to.

// Allocator adds an object to a document and returns the reference to it. It is
// declared here, where it is consumed, so this package does not depend on the
// one that implements it; *pdf0.Document does.
type Allocator interface {
	Add(object.Object) object.IndirectRef
}

// MaxPixels bounds the images this package will encode. An image is width ×
// height × components bytes once expanded, and the multiplication is where a
// hostile or mistaken caller turns a small number into an allocation that ends
// the process. A caller with a genuine need for something larger should encode
// it themselves and use EmbedJPEG, which stores encoded bytes without
// expanding them.
const MaxPixels = 1 << 26 // 67 megapixels

// Embed writes a Go image into doc as an image XObject and returns the
// reference to put in a page's /Resources /XObject.
//
// The pixels are stored losslessly: 8 bits per component, DeviceRGB or
// DeviceGray, Flate-compressed. An image carrying transparency also gets an
// /SMask — a second, greyscale image holding the alpha channel, which is how
// PDF represents it (ISO 32000-2 11.6.5.3).
//
// Colour choice follows the image rather than a parameter: a Gray or Gray16
// image is written as DeviceGray, anything else as DeviceRGB. Writing a
// greyscale photograph as RGB would triple it for nothing, and guessing that an
// RGB image is "really" grey would be a lossy decision taken behind the
// caller's back.
func Embed(doc Allocator, img image.Image) (object.IndirectRef, error) {
	if img == nil {
		return object.IndirectRef{}, errors.New("images: cannot embed a nil image")
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return object.IndirectRef{}, fmt.Errorf("images: image has no area (%dx%d)", w, h)
	}
	if int64(w)*int64(h) > MaxPixels {
		return object.IndirectRef{}, fmt.Errorf("images: %dx%d exceeds the %d-pixel limit", w, h, MaxPixels)
	}

	gray := isGray(img)
	samples, alpha := sampleBytes(img, gray)

	space := object.Name("DeviceRGB")
	if gray {
		space = "DeviceGray"
	}
	xobj, err := imageStream(samples, w, h, space)
	if err != nil {
		return object.IndirectRef{}, err
	}
	if alpha != nil {
		mask, err := imageStream(alpha, w, h, "DeviceGray")
		if err != nil {
			return object.IndirectRef{}, err
		}
		xobj.Dict.Set("SMask", doc.Add(mask))
	}
	return doc.Add(xobj), nil
}

// EmbedJPEG writes already-encoded JPEG bytes into doc without re-encoding
// them, using the DCTDecode filter a PDF reader applies directly.
//
// This is the right way to place a photograph that is already a JPEG. Decoding
// it to pixels and re-encoding with Flate would make the file larger — often
// several times — and decoding then re-encoding as JPEG would lose quality for
// nothing. The bytes go in as they are.
//
// The geometry and colour space are the caller's to state correctly, because
// this deliberately does not parse the JPEG: components must be 1 for grey or 3
// for colour. A four-component (CMYK) JPEG is refused rather than mislabelled.
func EmbedJPEG(doc Allocator, jpeg []byte, width, height, components int) (object.IndirectRef, error) {
	if len(jpeg) == 0 {
		return object.IndirectRef{}, errors.New("images: no JPEG data")
	}
	if width <= 0 || height <= 0 {
		return object.IndirectRef{}, fmt.Errorf("images: image has no area (%dx%d)", width, height)
	}
	var space object.Name
	switch components {
	case 1:
		space = "DeviceGray"
	case 3:
		space = "DeviceRGB"
	default:
		return object.IndirectRef{}, fmt.Errorf("images: %d-component JPEG is not supported; give 1 (grey) or 3 (colour)", components)
	}
	xobj := &object.Stream{Dict: object.Dictionary{}, Data: jpeg}
	xobj.Dict.Set("Type", object.Name("XObject"))
	xobj.Dict.Set("Subtype", object.Name("Image"))
	xobj.Dict.Set("Width", object.Integer(width))
	xobj.Dict.Set("Height", object.Integer(height))
	xobj.Dict.Set("ColorSpace", space)
	xobj.Dict.Set("BitsPerComponent", object.Integer(8))
	xobj.Dict.Set("Filter", object.Name("DCTDecode"))
	xobj.Dict.Set("Length", object.Integer(len(jpeg)))
	return doc.Add(xobj), nil
}

// imageStream builds one Flate-compressed image XObject over raw samples.
func imageStream(samples []byte, w, h int, space object.Name) (*object.Stream, error) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(samples); err != nil {
		return nil, fmt.Errorf("images: compressing samples: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("images: compressing samples: %w", err)
	}
	s := &object.Stream{Dict: object.Dictionary{}, Data: buf.Bytes()}
	s.Dict.Set("Type", object.Name("XObject"))
	s.Dict.Set("Subtype", object.Name("Image"))
	s.Dict.Set("Width", object.Integer(w))
	s.Dict.Set("Height", object.Integer(h))
	s.Dict.Set("ColorSpace", space)
	s.Dict.Set("BitsPerComponent", object.Integer(8))
	s.Dict.Set("Filter", object.Name("FlateDecode"))
	s.Dict.Set("Length", object.Integer(len(s.Data)))
	return s, nil
}

// sampleBytes expands an image to the row-major, 8-bit samples a PDF image
// stream holds, and separates the alpha channel when there is one.
//
// The non-premultiplied types are read through their own pixel buffers rather
// than through At. That is not only faster — it is the only way to be exact.
// Go's generic colour interface is alpha-premultiplied, and /SMask is not, so
// the generic path has to divide alpha back out; at low alpha the premultiplied
// value simply does not carry the precision to divide back. A pixel of
// (200,100,50) at alpha 1/255 comes back (200,99,49) through At and exactly
// through Pix. NRGBA is also what image/png decodes to, so this is the common
// case, not an optimisation for a corner.
func sampleBytes(img image.Image, gray bool) (samples, alpha []byte) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	switch src := img.(type) {
	case *image.NRGBA:
		samples = make([]byte, 0, w*h*3)
		alpha = make([]byte, 0, w*h)
		for y := 0; y < h; y++ {
			row := src.Pix[(y+b.Min.Y-src.Rect.Min.Y)*src.Stride:]
			for x := 0; x < w; x++ {
				p := row[(x+b.Min.X-src.Rect.Min.X)*4:]
				samples = append(samples, p[0], p[1], p[2])
				alpha = append(alpha, p[3])
			}
		}
		return samples, alpha
	case *image.Gray:
		samples = make([]byte, 0, w*h)
		for y := 0; y < h; y++ {
			row := src.Pix[(y+b.Min.Y-src.Rect.Min.Y)*src.Stride:]
			for x := 0; x < w; x++ {
				samples = append(samples, row[x+b.Min.X-src.Rect.Min.X])
			}
		}
		return samples, nil
	}

	comps := 3
	if gray {
		comps = 1
	}
	samples = make([]byte, 0, w*h*comps)
	var alphaBuf []byte
	if hasAlpha(img) {
		alphaBuf = make([]byte, 0, w*h)
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA() // 16-bit, alpha-premultiplied
			if a != 0 && a != 0xFFFF {
				r = r * 0xFFFF / a
				g = g * 0xFFFF / a
				bl = bl * 0xFFFF / a
			}
			if gray {
				samples = append(samples, byte(r>>8))
			} else {
				samples = append(samples, byte(r>>8), byte(g>>8), byte(bl>>8))
			}
			if alphaBuf != nil {
				alphaBuf = append(alphaBuf, byte(a>>8))
			}
		}
	}
	return samples, alphaBuf
}

// isGray reports whether an image's own type says it is greyscale. It is
// deliberately a question about the type rather than about the pixels: an RGB
// image that happens to hold grey pixels is still an RGB image, and deciding
// otherwise would silently discard the caller's choice.
func isGray(img image.Image) bool {
	switch img.(type) {
	case *image.Gray, *image.Gray16:
		return true
	}
	return false
}

// hasAlpha reports whether an image's type can carry transparency. As with
// isGray this asks the type, not the pixels — scanning for a transparent pixel
// would make the output depend on the content in a way a caller cannot predict.
func hasAlpha(img image.Image) bool {
	switch img.(type) {
	case *image.RGBA, *image.RGBA64, *image.NRGBA, *image.NRGBA64, *image.Alpha, *image.Alpha16:
		return true
	}
	return false
}
