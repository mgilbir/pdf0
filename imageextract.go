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
//   - DCTDecode (JPEG)                  -> decoded via image/jpeg (stdlib)
//   - raw, FlateDecode, LZWDecode, etc. -> decoded from the sample bytes
//   - CCITTFaxDecode (Group 3/4 fax)    -> decoded by the built-in ccitt.go codec
//   - JBIG2Decode                       -> generic and symbol/text regions
//     decoded by jbig2.go (halftone and refinement fall back to the raw bytes)
//   - JPXDecode                         -> not decoded; the raw encoded bytes and
//     the geometry are returned. JPEG 2000 is a large wavelet codec with no
//     standard-library support; decoding it faithfully is out of scope here.

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
