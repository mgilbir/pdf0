package pdf0

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"strconv"

	"github.com/mgilbir/gopenjpeg"
)

// decodeJPX decodes a JPEG 2000 (JPXDecode) codestream or JP2 container to a
// standard-library image using gopenjpeg, a pure-Go port of OpenJPEG. It returns
// nil for inputs it cannot render (decode error, ICC-only colour, sub-sampled or
// >16-bit components) so the caller can fall back to the raw bytes.
func decodeJPX(data []byte) image.Image {
	img, err := gopenjpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	// Normalise colour (sYCC/eYCC/CMYK -> sRGB and upsample sub-sampled
	// components); images carrying only an ICC profile keep their raw layout.
	_ = img.ConvertToRGB()
	if std, err := img.ToStandard(); err == nil {
		return std
	}
	// ToStandard declines sub-sampled or many-component images; assemble them from
	// the component data, upsampling each to the largest component's grid.
	return jpxComponentsToImage(img)
}

// jpxComponentsToImage builds a grayscale or RGB image from a decoded JPEG 2000
// image's components, nearest-neighbour upsampling any sub-sampled component to
// the largest component's dimensions and scaling each sample to 8 bits.
func jpxComponentsToImage(img *gopenjpeg.Image) image.Image {
	nc := img.NumComponents()
	if nc == 0 {
		return nil
	}
	refW, refH := 0, 0
	for i := 0; i < nc; i++ {
		c := img.Component(i)
		if int(c.W) > refW {
			refW = int(c.W)
		}
		if int(c.H) > refH {
			refH = int(c.H)
		}
	}
	if refW <= 0 || refH <= 0 {
		return nil
	}
	// at returns component c's sample covering reference pixel (rx,ry), level-
	// shifted, scaled to 8 bits and clamped.
	at := func(c gopenjpeg.Component, rx, ry int) uint8 {
		cx, cy := rx*int(c.W)/refW, ry*int(c.H)/refH
		v := c.Data[cy*int(c.W)+cx]
		if c.Sgnd {
			v += 1 << (c.Prec - 1)
		}
		if c.Prec < 8 {
			v <<= (8 - c.Prec)
		} else if c.Prec > 8 {
			v >>= (c.Prec - 8)
		}
		if v < 0 {
			v = 0
		} else if v > 0xff {
			v = 0xff
		}
		return uint8(v)
	}
	if nc == 1 {
		g := image.NewGray(image.Rect(0, 0, refW, refH))
		c := img.Component(0)
		for y := 0; y < refH; y++ {
			for x := 0; x < refW; x++ {
				g.Pix[y*refW+x] = at(c, x, y)
			}
		}
		return g
	}
	c0, c1, c2 := img.Component(0), img.Component(1), img.Component(2)
	rgba := image.NewNRGBA(image.Rect(0, 0, refW, refH))
	for y := 0; y < refH; y++ {
		for x := 0; x < refW; x++ {
			i := rgba.PixOffset(x, y)
			rgba.Pix[i] = at(c0, x, y)
			rgba.Pix[i+1] = at(c1, x, y)
			rgba.Pix[i+2] = at(c2, x, y)
			rgba.Pix[i+3] = 0xff
		}
	}
	return rgba
}

// This file extracts the raster images embedded in a PDF's pages. For each image
// XObject it reports the image geometry and, where the codec is one Go can decode
// without a large bespoke implementation, the decoded pixels:
//
//   - DCTDecode (JPEG)                  -> decoded via image/jpeg (stdlib)
//   - raw, FlateDecode, LZWDecode, etc. -> decoded from the sample bytes
//   - CCITTFaxDecode (Group 3/4 fax)    -> decoded by the built-in ccitt.go codec
//   - JBIG2Decode                       -> generic, symbol/text, refinement and
//     halftone regions (arithmetic and Huffman) decoded by jbig2.go
//   - JPXDecode                         -> JPEG 2000 decoded by gopenjpeg, a
//     pure-Go port of the OpenJPEG reference codec

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
// decoded when the codec is one this package handles. Form XObjects are followed
// into their own resources, so images nested inside forms are found too.
func (d *Document) ExtractImages() []ExtractedImage {
	cat := getCatalog(d)
	if cat == nil {
		return nil
	}
	var out []ExtractedImage
	seen := map[int]bool{}
	for _, pg := range collectPages(d, cat.Get("Pages")) {
		d.collectImagesFrom(resolveResources(d, pg.dict), seen, 0, &out)
		// Annotation appearance streams (/Annots -> /AP) are form XObjects with
		// their own resources, a common home for images (stamps, form fields).
		if annots, ok := d.Resolve(pg.dict.Get("Annots")).(Array); ok {
			for _, a := range annots {
				ad := d.ResolveDict(a)
				if ad == nil {
					continue
				}
				ap := d.ResolveDict(ad.Get("AP"))
				if ap == nil {
					continue
				}
				for _, entry := range ap.Values {
					d.collectAppearanceImages(entry, seen, &out)
				}
			}
		}
	}
	return out
}

// collectAppearanceImages walks an annotation appearance entry (/N, /D or /R),
// which is either a form-XObject stream or a subdictionary of appearance states
// (each value a stream), following each into its resources.
func (d *Document) collectAppearanceImages(entry Object, seen map[int]bool, out *[]ExtractedImage) {
	switch v := d.Resolve(entry).(type) {
	case *Stream:
		if num := refNum(entry); num > 0 {
			if seen[num] {
				return
			}
			seen[num] = true
		}
		d.collectImagesFrom(d.ResolveDict(v.Dict.Get("Resources")), seen, 1, out)
	case *Dictionary:
		for _, state := range v.Values {
			d.collectAppearanceImages(state, seen, out)
		}
	}
}

// collectImagesFrom walks a resource dictionary's /XObject entries, extracting
// image XObjects and recursing into form XObjects' own resources. seen guards
// against revisiting a shared or self-referential XObject; depth bounds runaway
// recursion.
func (d *Document) collectImagesFrom(res *Dictionary, seen map[int]bool, depth int, out *[]ExtractedImage) {
	if res == nil || depth > 16 {
		return
	}
	xobjs := d.ResolveDict(res.Get("XObject"))
	if xobjs == nil {
		return
	}
	for i := range xobjs.Keys {
		ref := xobjs.Values[i]
		st, ok := d.Resolve(ref).(*Stream)
		if !ok {
			continue
		}
		if num := refNum(ref); num > 0 {
			if seen[num] {
				continue
			}
			seen[num] = true
		}
		switch sub, _ := st.Dict.Get("Subtype").(Name); sub {
		case "Image":
			*out = append(*out, d.extractImage(st, refNum(ref)))
		case "Form":
			d.collectImagesFrom(d.ResolveDict(st.Dict.Get("Resources")), seen, depth+1, out)
		}
	}
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
	case "CCITTFaxDecode":
		encoded, params, ok := ccittEncodedAndParams(d, st, img.Width, img.Height)
		if !ok {
			img.Encoded = st.Data
			img.Note = "CCITTFaxDecode preceding filter chain could not be reversed; the raw encoded bytes are provided"
			break
		}
		samples, err := decodeCCITT(encoded, params)
		if err != nil {
			img.Encoded = st.Data
			img.Note = "CCITTFaxDecode failed: " + err.Error()
			break
		}
		if m, ok := samplesToImage(samples, img.Width, img.Height, 1, "DeviceGray"); ok {
			img.Image, img.Decoded = m, true
		} else {
			img.Encoded = samples
			img.Note = "unsupported CCITT sample layout"
		}
	case "JBIG2Decode":
		encoded, globals, ok := jbig2EncodedAndGlobals(d, st)
		if !ok {
			img.Encoded = st.Data
			img.Note = "JBIG2Decode preceding filter chain could not be reversed; the raw encoded bytes are provided"
			break
		}
		samples, err := decodeJBIG2(globals, encoded, img.Width, img.Height)
		if err != nil {
			img.Encoded = st.Data
			img.Note = "JBIG2Decode not decoded (" + err.Error() + "); the raw encoded bytes are provided"
			break
		}
		if m, ok := samplesToImage(samples, img.Width, img.Height, 1, "DeviceGray"); ok {
			img.Image, img.Decoded = m, true
		} else {
			img.Encoded = samples
			img.Note = "unsupported JBIG2 sample layout"
		}
	case "JPXDecode":
		if m := decodeJPX(st.Data); m != nil {
			img.Image, img.Decoded = m, true
			break
		}
		img.Encoded = st.Data
		img.Note = "JPXDecode not decoded; raw bytes provided"
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

// ccittEncodedAndParams returns the CCITT-encoded bytes for an image XObject —
// reversing any general-purpose filters (Flate/LZW/ASCIIHex) that precede the
// CCITTFaxDecode codec in the filter chain — together with the /DecodeParms that
// steer the fax decoder. ok is false when a preceding filter cannot be reversed.
func ccittEncodedAndParams(d *Document, st *Stream, width, height int) (encoded []byte, params ccittParams, ok bool) {
	filters := streamFilters(d, st)
	if len(filters) == 0 {
		return nil, params, false
	}
	last := len(filters) - 1
	parms := d.Resolve(st.Dict.Get("DecodeParms"))

	encoded = st.Data
	for i := 0; i < last; i++ {
		out, err := applyFilter(filters[i], encoded, parmsDictAt(parms, i))
		if err != nil {
			return nil, params, false
		}
		encoded = out
	}

	cp := parmsDictAt(parms, last)
	params = ccittParams{columns: 1728, rows: height, k: 0}
	if cp != nil {
		if v, kOK := d.Resolve(cp.Get("K")).(Integer); kOK {
			params.k = int(v)
		}
		if v, cOK := d.Resolve(cp.Get("Columns")).(Integer); cOK {
			params.columns = int(v)
		}
		if v, rOK := d.Resolve(cp.Get("Rows")).(Integer); rOK && int(v) > 0 {
			params.rows = int(v)
		}
		if b, aOK := d.Resolve(cp.Get("EncodedByteAlign")).(Boolean); aOK {
			params.byteAlign = bool(b)
		}
	}
	if params.columns <= 0 {
		params.columns = width
	}
	return encoded, params, true
}

// jbig2EncodedAndGlobals returns the JBIG2-encoded bytes for an image XObject
// (reversing any general-purpose filters that precede JBIG2Decode) and the
// decoded /JBIG2Globals shared-segment stream when present. ok is false when a
// preceding filter cannot be reversed.
func jbig2EncodedAndGlobals(d *Document, st *Stream) (encoded, globals []byte, ok bool) {
	filters := streamFilters(d, st)
	if len(filters) == 0 {
		return nil, nil, false
	}
	last := len(filters) - 1
	parms := d.Resolve(st.Dict.Get("DecodeParms"))

	encoded = st.Data
	for i := 0; i < last; i++ {
		out, err := applyFilter(filters[i], encoded, parmsDictAt(parms, i))
		if err != nil {
			return nil, nil, false
		}
		encoded = out
	}

	if cp := parmsDictAt(parms, last); cp != nil {
		if gs, ok := d.Resolve(cp.Get("JBIG2Globals")).(*Stream); ok {
			if data, err := decodeStreamData(gs); err == nil {
				globals = data
			}
		}
	}
	return encoded, globals, true
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
