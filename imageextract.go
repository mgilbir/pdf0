package pdf0

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"strconv"
)

// This file extracts the raster images embedded in a PDF's pages. For each image
// XObject it reports the image geometry and, where the codec is one Go can decode
// without a large bespoke implementation, the decoded pixels:
//
//   - DCTDecode (JPEG)                      -> decoded via image/jpeg (stdlib)
//   - raw, FlateDecode, LZWDecode, etc.     -> decoded from the sample bytes
//   - CCITTFaxDecode, JBIG2Decode, JPXDecode -> not decoded; the raw encoded bytes
//     and the geometry are returned. These are large dedicated codecs (Group 4
//     fax, JBIG2 arithmetic coding, JPEG 2000 wavelets) with no standard-library
//     support; decoding them faithfully is out of scope here.

// ExtractedImage is one image XObject: its geometry, its codec, and its decoded
// pixels when available.
type ExtractedImage struct {
	ObjNum           int         // object number of the image XObject
	Width, Height    int         // pixel dimensions
	BitsPerComponent int         // bits per colour component
	ColorSpace       string      // colour space name (best effort)
	Filter           string      // the image codec (the last filter in the chain)
	Image            image.Image // decoded pixels, or nil if the codec was not decoded
	Encoded          []byte      // the encoded stream bytes when Image is nil
	Decoded          bool        // whether Image holds decoded pixels
	Note             string      // why the image was not decoded, when applicable
}

// ExtractImages returns every image XObject drawn from the document's pages, each
// decoded when the codec is one this package handles.
func (d *Document) ExtractImages() []ExtractedImage {
	cat := getCatalog(d)
	if cat == nil {
		return nil
	}
	var out []ExtractedImage
	seen := map[int]bool{}
	for _, pg := range collectPages(d, cat.Get("Pages")) {
		res := resolveResources(d, pg.dict)
		if res == nil {
			continue
		}
		xobjs := d.ResolveDict(res.Get("XObject"))
		if xobjs == nil {
			continue
		}
		for i, key := range xobjs.Keys {
			_ = key
			st, ok := d.Resolve(xobjs.Values[i]).(*Stream)
			if !ok {
				continue
			}
			if sub, _ := st.Dict.Get("Subtype").(Name); sub != "Image" {
				continue
			}
			num := refNum(xobjs.Values[i])
			if seen[num] {
				continue
			}
			seen[num] = true
			out = append(out, d.extractImage(st, num))
		}
	}
	return out
}

func (d *Document) extractImage(st *Stream, num int) ExtractedImage {
	img := ExtractedImage{
		ObjNum:           num,
		Width:            intValue(d.Resolve(st.Dict.Get("Width"))),
		Height:           intValue(d.Resolve(st.Dict.Get("Height"))),
		BitsPerComponent: intValue(d.Resolve(st.Dict.Get("BitsPerComponent"))),
		ColorSpace:       colorSpaceName(d, st.Dict.Get("ColorSpace")),
	}
	if b, _ := d.Resolve(st.Dict.Get("ImageMask")).(Boolean); bool(b) {
		img.ColorSpace = "ImageMask"
		img.BitsPerComponent = 1
	}
	filters := streamFilters(d, st)
	if len(filters) > 0 {
		img.Filter = string(filters[len(filters)-1])
	}

	switch img.Filter {
	case "DCTDecode":
		if m, err := jpeg.Decode(bytes.NewReader(st.Data)); err == nil {
			img.Image, img.Decoded = m, true
		} else {
			img.Encoded, img.Note = st.Data, "JPEG decode failed: "+err.Error()
		}
	case "CCITTFaxDecode", "JBIG2Decode", "JPXDecode":
		img.Encoded = st.Data
		img.Note = "the " + img.Filter + " image codec is not decoded; the raw encoded bytes are provided"
	default:
		// No filter, or a general-purpose filter chain (Flate/LZW/RunLength/ASCII):
		// decodeContentStream reverses the chain to raw samples.
		raw := decodeContentStream(d, st)
		if m, ok := samplesToImage(raw, img.Width, img.Height, img.BitsPerComponent, img.ColorSpace); ok {
			img.Image, img.Decoded = m, true
		} else {
			img.Encoded = raw
			img.Note = "unsupported sample layout (colour space " + img.ColorSpace + ", " + strconv.Itoa(img.BitsPerComponent) + " bpc)"
		}
	}
	return img
}

// samplesToImage builds an image from decoded PDF sample bytes for the common
// grayscale and RGB layouts. Rows are byte-aligned, as PDF requires.
func samplesToImage(data []byte, w, h, bpc int, cs string) (image.Image, bool) {
	if w <= 0 || h <= 0 {
		return nil, false
	}
	gray := cs == "DeviceGray" || cs == "CalGray" || cs == "G"
	mask := cs == "ImageMask"
	rgb := cs == "DeviceRGB" || cs == "CalRGB" || cs == "RGB"

	switch {
	case (gray || mask) && bpc == 1:
		stride := (w + 7) / 8
		if len(data) < stride*h {
			return nil, false
		}
		im := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			row := data[y*stride:]
			for x := 0; x < w; x++ {
				bit := (row[x/8] >> (7 - uint(x%8))) & 1
				// For an image mask a 1 marks the area to paint; render 1 as black.
				v := byte(0)
				if (mask && bit == 0) || (!mask && bit == 1) {
					v = 255
				}
				im.SetGray(x, y, color.Gray{Y: v})
			}
		}
		return im, true
	case gray && bpc == 8:
		if len(data) < w*h {
			return nil, false
		}
		im := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			copy(im.Pix[y*im.Stride:], data[y*w:y*w+w])
		}
		return im, true
	case rgb && bpc == 8:
		if len(data) < w*h*3 {
			return nil, false
		}
		im := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			src := data[y*w*3:]
			for x := 0; x < w; x++ {
				im.SetRGBA(x, y, color.RGBA{R: src[x*3], G: src[x*3+1], B: src[x*3+2], A: 255})
			}
		}
		return im, true
	}
	return nil, false
}

// colorSpaceName returns a best-effort colour space name: a direct name, or the
// leading name of an array space (e.g. ICCBased, Indexed).
func colorSpaceName(d *Document, obj Object) string {
	switch cs := d.Resolve(obj).(type) {
	case Name:
		return string(cs)
	case Array:
		if len(cs) > 0 {
			if n, ok := d.Resolve(cs[0]).(Name); ok {
				return string(n)
			}
		}
	}
	return ""
}

func intValue(obj Object) int {
	switch n := obj.(type) {
	case Integer:
		return int(n)
	case Real:
		return int(n)
	}
	return 0
}
