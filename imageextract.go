package pdf0

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"iter"
	"strconv"

	"github.com/mgilbir/gopenjpeg"
)

// This file owns image extraction: the ExtractImages/Images API, the traversal
// that reaches image XObjects through page resources, form XObjects and
// annotation appearance streams, and the dispatch from a stream's filter chain
// to a decoder (ISO 32000-2 clause 8.9). The codecs live elsewhere — ccitt.go,
// jbig2*.go, imagejpeg.go, imagecolor.go, imagemask.go — only the JPXDecode
// bridge to gopenjpeg is here; an image no codec can render yields the raw
// encoded bytes and a Note rather than an error, so one bad image never aborts
// the walk. The per-codec support table is below, before ExtractedImage.

// decodeJPX decodes a JPEG 2000 (JPXDecode) codestream or JP2 container to a
// standard-library image using gopenjpeg, a pure-Go port of OpenJPEG. It returns
// nil for inputs it cannot render (decode error, ICC-only colour, sub-sampled or
// >16-bit components) so the caller can fall back to the raw bytes.
// smaskInData is the image dictionary's /SMaskInData value, which governs
// whether an opacity channel packaged in the codestream is used (see
// jpxComponentsToImage).
func decodeJPX(data []byte, smaskInData int) image.Image {
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
	return jpxComponentsToImage(img, smaskInData)
}

// jpxComponentsToImage builds a grayscale or RGB image from a decoded JPEG 2000
// image's components, nearest-neighbour upsampling any sub-sampled component to
// the largest component's dimensions and scaling each sample to 8 bits.
//
// Channel roles follow the specs rather than position alone. An opacity
// channel is the one the codestream's channel-definition (cdef) box flags
// (ISO 15444-1, surfaced as Component.Alpha), or — for a codestream without
// cdef that carries one channel more than its colour space needs (the shape
// real-world grey+extra files take) — the trailing extra channel. Whether
// that opacity channel is USED is the image dictionary's /SMaskInData
// (ISO 32000-2, Table 87): 0 (the default) means encoded soft-mask
// information "shall be ignored", so only the colour channels render; 1 means
// the samples carry an opacity channel (rendered into the alpha of an NRGBA);
// 2 means the colour channels are premultiplied with the opacity channel
// (rendered as a premultiplied-alpha RGBA).
func jpxComponentsToImage(img *gopenjpeg.Image, smaskInData int) image.Image {
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
	// shifted, scaled to 8 bits and clamped. A component with no samples (or a
	// short Data slice from a damaged codestream) reads as 0 rather than
	// panicking — this runs on untrusted input.
	at := func(c gopenjpeg.Component, rx, ry int) uint8 {
		if c.W == 0 || c.H == 0 {
			return 0
		}
		cx, cy := rx*int(c.W)/refW, ry*int(c.H)/refH
		idx := cy*int(c.W) + cx
		if idx < 0 || idx >= len(c.Data) {
			return 0
		}
		v := c.Data[idx]
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
	// Identify the opacity channel: the cdef-flagged component, or — with no
	// cdef box — the trailing extra channel of a grey+extra pair (the layout
	// the sweep-13 files carry; ISO 15444-1 leaves an unflagged extra channel
	// undefined, and two-channel greyscale is the only unambiguous case).
	alphaIdx := -1
	for i := 0; i < nc; i++ {
		if img.Component(i).Alpha != 0 {
			alphaIdx = i
			break
		}
	}
	if alphaIdx < 0 && nc == 2 {
		alphaIdx = 1
	}
	var colour []gopenjpeg.Component
	for i := 0; i < nc; i++ {
		if i != alphaIdx {
			colour = append(colour, img.Component(i))
		}
	}
	// /SMaskInData 0: encoded soft-mask information shall be ignored
	// (ISO 32000-2, Table 87) — only the colour channels render.
	useAlpha := alphaIdx >= 0 && smaskInData > 0
	ca := gopenjpeg.Component{}
	if useAlpha {
		ca = img.Component(alphaIdx)
	}

	if len(colour) == 1 {
		c := colour[0]
		if !useAlpha {
			g := image.NewGray(image.Rect(0, 0, refW, refH))
			for y := 0; y < refH; y++ {
				for x := 0; x < refW; x++ {
					g.Pix[y*refW+x] = at(c, x, y)
				}
			}
			return g
		}
		if smaskInData == 2 {
			// Colour samples are premultiplied with the opacity channel —
			// exactly Go's alpha-premultiplied RGBA representation.
			rgba := image.NewRGBA(image.Rect(0, 0, refW, refH))
			for y := 0; y < refH; y++ {
				for x := 0; x < refW; x++ {
					i := rgba.PixOffset(x, y)
					g := at(c, x, y)
					rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2] = g, g, g
					rgba.Pix[i+3] = at(ca, x, y)
				}
			}
			return rgba
		}
		rgba := image.NewNRGBA(image.Rect(0, 0, refW, refH))
		for y := 0; y < refH; y++ {
			for x := 0; x < refW; x++ {
				i := rgba.PixOffset(x, y)
				g := at(c, x, y)
				rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2] = g, g, g
				rgba.Pix[i+3] = at(ca, x, y)
			}
		}
		return rgba
	}
	if len(colour) < 3 {
		return nil // no unambiguous colour layout
	}
	c0, c1, c2 := colour[0], colour[1], colour[2]
	set := func(px []byte, i, x, y int) {
		px[i] = at(c0, x, y)
		px[i+1] = at(c1, x, y)
		px[i+2] = at(c2, x, y)
	}
	if useAlpha && smaskInData == 2 {
		rgba := image.NewRGBA(image.Rect(0, 0, refW, refH))
		for y := 0; y < refH; y++ {
			for x := 0; x < refW; x++ {
				i := rgba.PixOffset(x, y)
				set(rgba.Pix, i, x, y)
				rgba.Pix[i+3] = at(ca, x, y)
			}
		}
		return rgba
	}
	rgba := image.NewNRGBA(image.Rect(0, 0, refW, refH))
	for y := 0; y < refH; y++ {
		for x := 0; x < refW; x++ {
			i := rgba.PixOffset(x, y)
			set(rgba.Pix, i, x, y)
			if useAlpha {
				rgba.Pix[i+3] = at(ca, x, y)
			} else {
				rgba.Pix[i+3] = 0xff
			}
		}
	}
	return rgba
}

// Per-codec support. For each image XObject the extractor reports the geometry
// and, where the codec is one Go can decode without a large bespoke
// implementation, the decoded pixels:
//
//   - DCTDecode (JPEG)                  -> decoded via image/jpeg (stdlib)
//   - raw, FlateDecode, LZWDecode,
//     ASCIIHexDecode                     -> decoded from the sample bytes
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
//
// Every decoded image is held in the returned slice at once; on a large scan
// document that is unbounded memory. Use Images to iterate lazily with at most
// one decoded image live at a time.
func (d *Document) ExtractImages() []ExtractedImage {
	var out []ExtractedImage
	d.walkImages(func(im ExtractedImage) bool {
		out = append(out, im)
		return true
	})
	return out
}

// Images returns an iterator over the image XObjects drawn from the document's
// pages, in the same order ExtractImages reports them. Each image is decoded
// only as it is yielded, so — unlike ExtractImages, which materializes every
// decoded image at once — iteration keeps at most one decoded image live at a
// time (unless the caller retains them), and breaking out of the loop skips
// the remaining decode work entirely.
func (d *Document) Images() iter.Seq[ExtractedImage] {
	return d.walkImages
}

// walkImages drives the image traversal, calling yield for each image until it
// returns false.
func (d *Document) walkImages(yield func(ExtractedImage) bool) {
	// Install a per-run cache on a shallow copy, as the validators do: a tint
	// transform evaluates per pixel, and without the cache each evaluation
	// re-decoded the function stream (and re-parsed a type-4 program) — a
	// sub-megabyte image took minutes (sweep #13).
	if d.valCache == nil {
		runDoc := *d
		runDoc.valCache = &validationCache{
			pages:   make(map[int][]pageInfo),
			content: make(map[*Stream][]byte),
		}
		d = &runDoc
	}
	cat := getCatalog(d)
	if cat == nil {
		return
	}
	seen := map[int]bool{}
	for _, pg := range collectPages(d, cat.Get("Pages")) {
		if !d.collectImagesFrom(resolveResources(d, pg.dict), seen, 0, yield) {
			return
		}
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
					if !d.collectAppearanceImages(entry, seen, yield) {
						return
					}
				}
			}
		}
	}
}

// collectAppearanceImages walks an annotation appearance entry (/N, /D or /R),
// which is either a form-XObject stream or a subdictionary of appearance states
// (each value a stream), following each into its resources. It returns false
// once yield does.
func (d *Document) collectAppearanceImages(entry Object, seen map[int]bool, yield func(ExtractedImage) bool) bool {
	switch v := d.Resolve(entry).(type) {
	case *Stream:
		if num := refNum(entry); num > 0 {
			if seen[num] {
				return true
			}
			seen[num] = true
		}
		return d.collectImagesFrom(d.ResolveDict(v.Dict.Get("Resources")), seen, 1, yield)
	case *Dictionary:
		for _, state := range v.Values {
			if !d.collectAppearanceImages(state, seen, yield) {
				return false
			}
		}
	}
	return true
}

// collectImagesFrom walks a resource dictionary's /XObject entries, extracting
// image XObjects and recursing into form XObjects' own resources. seen guards
// against revisiting a shared or self-referential XObject; depth bounds runaway
// recursion. It returns false once yield does.
func (d *Document) collectImagesFrom(res *Dictionary, seen map[int]bool, depth int, yield func(ExtractedImage) bool) bool {
	if res == nil || depth > 16 {
		return true
	}
	xobjs := d.ResolveDict(res.Get("XObject"))
	if xobjs == nil {
		return true
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
			if !yield(d.extractImage(st, refNum(ref))) {
				return false
			}
		case "Form":
			if !d.collectImagesFrom(d.ResolveDict(st.Dict.Get("Resources")), seen, depth+1, yield) {
				return false
			}
		}
	}
	return true
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
			m = applyJPEGDecode(m, jpegDecodeArray(d, st))
			img.Image, img.Decoded = d.applyImageMasks(st, m), true
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
		d.renderBilevelSamples(st, &img, samples, "unsupported CCITT sample layout")
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
		d.renderBilevelSamples(st, &img, samples, "unsupported JBIG2 sample layout")
	case "JPXDecode":
		if m := decodeJPX(st.Data, intValue(d.Resolve(st.Dict.Get("SMaskInData")))); m != nil {
			img.Image, img.Decoded = d.applyImageMasks(st, m), true
			break
		}
		img.Encoded = st.Data
		img.Note = "JPXDecode not decoded; raw bytes provided"
	default:
		// No filter, or a general-purpose filter chain (Flate/LZW/ASCIIHex — the
		// only ones applyFilter reverses): reverse the chain to raw samples, which
		// buildImage renders through the colour space, bit depth, /Decode and masks
		// (image masks keep their own 1-bit stencil rendering).
		d.renderSamples(st, &img, decodeImageSamples(st), "unsupported sample layout (colour space "+img.ColorSpace+", "+strconv.Itoa(img.BitsPerComponent)+" bpc)")
	}
	return img
}

// decodeImageSamples reverses a sample stream's filter chain WITHOUT the run
// cache (unlike decodeContentStream): image-sized sample data is used once,
// and retaining it in the cache for the whole run — or charging it against
// the shared content budget — would bloat memory and starve the small shared
// streams (tint functions, palettes) the cache exists for. The same 64MB
// per-stream bound applies.
func decodeImageSamples(st *Stream) []byte {
	if decoded, err := decodeStreamData(st); err == nil && len(decoded) <= maxContentStreamSize {
		return decoded
	}
	return nil
}

// renderSamples turns decoded image samples into an image.Image, applying the
// same colour space, bit depth, /Decode, and mask handling regardless of which
// codec produced the samples. An /ImageMask goes through the stencil path;
// everything else through buildImage, which also composites the stencil /Mask and
// soft /SMask. It is used by the general-purpose branch.
func (d *Document) renderSamples(st *Stream, img *ExtractedImage, samples []byte, unsupportedNote string) {
	var m image.Image
	var ok bool
	if img.ColorSpace == "ImageMask" {
		m, ok = imageMaskToImage(d, st, samples, img.Width, img.Height)
	} else {
		m, ok = d.buildImage(st, samples, img.Width, img.Height, img.BitsPerComponent)
	}
	if ok {
		img.Image, img.Decoded = m, true
	} else {
		img.Encoded = samples
		img.Note = unsupportedNote
	}
}

// renderBilevelSamples renders the 1-bpp samples a CCITT/JBIG2 codec produced.
// The common case (a plain DeviceGray image with no /Decode) keeps the fast,
// byte- and type-identical DeviceGray path. Only when the image is an /ImageMask,
// or carries a /Decode that inverts polarity, does it route through the shared
// mask/decode-aware path — which the codec branches previously bypassed, so an
// image mask rendered as an opaque raster and a /Decode [1 0] was ignored
// (audit C30).
func (d *Document) renderBilevelSamples(st *Stream, img *ExtractedImage, samples []byte, unsupportedNote string) {
	if img.ColorSpace == "ImageMask" || d.Resolve(st.Dict.Get("Decode")) != nil {
		d.renderSamples(st, img, samples, unsupportedNote)
		return
	}
	if m, ok := samplesToImage(samples, img.Width, img.Height, 1, "DeviceGray"); ok {
		img.Image, img.Decoded = d.applyImageMasks(st, m), true
	} else {
		img.Encoded = samples
		img.Note = unsupportedNote
	}
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

func floatValue(obj Object) float64 {
	switch n := obj.(type) {
	case Integer:
		return float64(n)
	case Real:
		return float64(n)
	}
	return 0
}

// imageMaskToImage renders a 1-bit stencil mask (/ImageMask true): samples select
// where the fill colour would paint. It is rendered as black where painted, white
// elsewhere; /Decode [1 0] inverts which bit paints.
func imageMaskToImage(d *Document, st *Stream, data []byte, w, h int) (image.Image, bool) {
	if w <= 0 || h <= 0 || !sampleDataFits(data, w, h, 1, 1) {
		return nil, false
	}
	paintBit := byte(0) // default /Decode [0 1]: a 0 sample paints
	if arr, ok := d.Resolve(st.Dict.Get("Decode")).(Array); ok && len(arr) == 2 && floatValue(d.Resolve(arr[0])) == 1 {
		paintBit = 1
	}
	stride := (w + 7) / 8
	im := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := data[y*stride:]
		for x := 0; x < w; x++ {
			bit := (row[x/8] >> (7 - uint(x%8))) & 1
			v := byte(255)
			if bit == paintBit {
				v = 0 // painted -> black
			}
			im.SetGray(x, y, color.Gray{Y: v})
		}
	}
	return im, true
}
