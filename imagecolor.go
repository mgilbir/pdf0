package pdf0

import (
	"image"
	"math"
)

// This file turns the decoded samples of an image XObject into an image.Image,
// honouring the colour space, bit depth, /Decode array and /SMask. It covers the
// device and CIE-based spaces (Gray/RGB/CMYK, CalGray/CalRGB, Lab, ICCBased by
// component count) and Indexed palettes. Separation and DeviceN need tint-
// transform function evaluation, which this package does not do, so they fall
// back to the raw bytes.

// imgColorSpace is a resolved image colour space: how many components a pixel's
// samples carry and how to turn them into RGB. For an Indexed space the single
// component is a palette index and toRGB is unused (the index is looked up).
type imgColorSpace struct {
	ncomp   int
	indexed bool
	hival   int
	lookup  []byte // indexed: base.ncomp bytes per palette entry
	base    *imgColorSpace
	decode  []float64                         // default /Decode (min,max per component)
	toRGB   func(c []float64) (r, g, b uint8) // c holds ncomp values already mapped through /Decode
}

// buildImage converts an image XObject's decoded samples to an image, applying
// the colour space, bit depth, /Decode array and soft mask. ok is false for a
// layout it cannot render.
func (d *Document) buildImage(st *Stream, raw []byte, w, h, bpc int) (image.Image, bool) {
	if w <= 0 || h <= 0 || bpc <= 0 {
		return nil, false
	}
	cs, ok := d.resolveColorSpace(st.Dict.Get("ColorSpace"))
	if !ok {
		return nil, false
	}
	decode := d.imageDecode(st, cs, bpc)
	maxval := float64(int(1)<<uint(bpc) - 1)
	if maxval <= 0 {
		return nil, false
	}
	if !sampleDataFits(raw, w, h, cs.ncomp, bpc) {
		return nil, false
	}

	colorKey := d.colorKeyMask(st, cs.ncomp) // range array making matching samples transparent

	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	sr := sampleReader{data: raw, bpc: bpc, w: w, ncomp: cs.ncomp}
	comps := make([]float64, cs.ncomp)
	rawS := make([]int, cs.ncomp)
	for y := 0; y < h; y++ {
		sr.startRow(y)
		for x := 0; x < w; x++ {
			var r, g, b uint8
			if cs.indexed {
				rawS[0] = sr.next()
				// The sample is an index; /Decode maps it into the palette range.
				idx := int(math.Round(decode[0] + float64(rawS[0])*(decode[1]-decode[0])/maxval))
				r, g, b = cs.paletteRGB(idx)
			} else {
				for k := 0; k < cs.ncomp; k++ {
					rawS[k] = sr.next()
					comps[k] = decode[2*k] + float64(rawS[k])*(decode[2*k+1]-decode[2*k])/maxval
				}
				r, g, b = cs.toRGB(comps)
			}
			a := byte(0xFF)
			if colorKey != nil && inColorKey(rawS, colorKey) {
				a = 0
			}
			o := im.PixOffset(x, y)
			im.Pix[o], im.Pix[o+1], im.Pix[o+2], im.Pix[o+3] = r, g, b, a
		}
	}
	d.applyStencilMask(st, im)
	d.applySoftMask(st, im)
	return im, true
}

// colorKeyMask returns the /Mask colour-key range array [min1 max1 …] when
// present and well-formed for ncomp components, else nil.
func (d *Document) colorKeyMask(st *Stream, ncomp int) []int {
	arr, ok := d.Resolve(st.Dict.Get("Mask")).(Array)
	if !ok || len(arr) != 2*ncomp {
		return nil
	}
	out := make([]int, len(arr))
	for i := range arr {
		out[i] = intValue(d.Resolve(arr[i]))
	}
	return out
}

// inColorKey reports whether every raw sample falls within its colour-key range,
// which marks the pixel transparent.
func inColorKey(samples, ranges []int) bool {
	for k, s := range samples {
		if s < ranges[2*k] || s > ranges[2*k+1] {
			return false
		}
	}
	return true
}

// applyStencilMask applies a stencil /Mask (a 1-bit image XObject): samples of 1
// mark pixels to hide, so those become transparent. /Decode [1 0] inverts it.
func (d *Document) applyStencilMask(st *Stream, im *image.NRGBA) {
	mk, ok := d.Resolve(st.Dict.Get("Mask")).(*Stream)
	if !ok {
		return
	}
	mw := intValue(d.Resolve(mk.Dict.Get("Width")))
	mh := intValue(d.Resolve(mk.Dict.Get("Height")))
	if mw <= 0 || mh <= 0 || !sampleDataFits(decodeContentStream(d, mk), mw, mh, 1, 1) {
		return
	}
	data := decodeContentStream(d, mk)
	hideBit := byte(1) // default /Decode [0 1]: a 1 sample hides
	if arr, ok := d.Resolve(mk.Dict.Get("Decode")).(Array); ok && len(arr) == 2 && floatValue(d.Resolve(arr[0])) == 1 {
		hideBit = 0
	}
	stride := (mw + 7) / 8
	w, h := im.Rect.Dx(), im.Rect.Dy()
	for y := 0; y < h; y++ {
		my := y * mh / h
		row := data[my*stride:]
		for x := 0; x < w; x++ {
			mx := x * mw / w
			if (row[mx/8]>>(7-uint(mx%8)))&1 == hideBit {
				im.Pix[im.PixOffset(x, y)+3] = 0
			}
		}
	}
}

// paletteRGB looks up an Indexed palette entry and converts it through the base
// colour space.
func (cs *imgColorSpace) paletteRGB(idx int) (r, g, b uint8) {
	if idx < 0 {
		idx = 0
	}
	if idx > cs.hival {
		idx = cs.hival
	}
	off := idx * cs.base.ncomp
	if off+cs.base.ncomp > len(cs.lookup) {
		return 0, 0, 0
	}
	bc := make([]float64, cs.base.ncomp)
	for k := range bc {
		bc[k] = float64(cs.lookup[off+k]) / 255
	}
	return cs.base.toRGB(bc)
}

// sampleReader reads bpc-bit samples MSB-first from packed rows; each image row
// starts on a byte boundary, as PDF requires.
type sampleReader struct {
	data          []byte
	bpc, w, ncomp int
	bytePos, bit  int
}

func (s *sampleReader) startRow(y int) {
	rowBytes := (s.w*s.ncomp*s.bpc + 7) / 8
	s.bytePos = y * rowBytes
	s.bit = 0
}

func (s *sampleReader) next() int {
	v := 0
	for i := 0; i < s.bpc; i++ {
		b := 0
		if s.bytePos < len(s.data) {
			b = int(s.data[s.bytePos]>>(7-uint(s.bit))) & 1
		}
		v = (v << 1) | b
		if s.bit++; s.bit == 8 {
			s.bit = 0
			s.bytePos++
		}
	}
	return v
}

// sampleDataFits reports whether data holds at least one full image of w x h
// pixels with ncomp components at bpc bits, rows byte-aligned.
func sampleDataFits(data []byte, w, h, ncomp, bpc int) bool {
	rowBytes := (w*ncomp*bpc + 7) / 8
	return len(data) >= rowBytes*h
}

// resolveColorSpace resolves a PDF colour-space object to an imgColorSpace, or
// (nil,false) for one this decoder cannot render.
func (d *Document) resolveColorSpace(obj Object) (*imgColorSpace, bool) {
	switch cs := d.Resolve(obj).(type) {
	case Name:
		return deviceColorSpace(string(cs))
	case Array:
		if len(cs) == 0 {
			return nil, false
		}
		head, _ := d.Resolve(cs[0]).(Name)
		switch head {
		case "ICCBased":
			return d.iccBasedColorSpace(cs)
		case "CalRGB":
			return deviceColorSpace("DeviceRGB")
		case "CalGray":
			return deviceColorSpace("DeviceGray")
		case "Lab":
			return d.labColorSpace(cs)
		case "Indexed", "I":
			return d.indexedColorSpace(cs)
		case "DeviceGray", "DeviceRGB", "DeviceCMYK", "G", "RGB", "CMYK":
			return deviceColorSpace(string(head))
		case "Separation", "DeviceN":
			// Needs tint-transform function evaluation; not rendered.
			return nil, false
		}
	}
	return nil, false
}

func deviceColorSpace(name string) (*imgColorSpace, bool) {
	switch name {
	case "DeviceGray", "CalGray", "G":
		return &imgColorSpace{ncomp: 1, decode: []float64{0, 1}, toRGB: func(c []float64) (uint8, uint8, uint8) {
			v := clamp8(c[0])
			return v, v, v
		}}, true
	case "DeviceRGB", "CalRGB", "RGB":
		return &imgColorSpace{ncomp: 3, decode: []float64{0, 1, 0, 1, 0, 1}, toRGB: func(c []float64) (uint8, uint8, uint8) {
			return clamp8(c[0]), clamp8(c[1]), clamp8(c[2])
		}}, true
	case "DeviceCMYK", "CMYK":
		return &imgColorSpace{ncomp: 4, decode: []float64{0, 1, 0, 1, 0, 1, 0, 1}, toRGB: cmykToRGB}, true
	}
	return nil, false
}

// iccBasedColorSpace renders an ICCBased space by its component count (/N): 1 as
// grayscale, 3 as RGB, 4 as CMYK, matching the profile's device class. A
// present /Alternate is used when /N is absent or unusual.
func (d *Document) iccBasedColorSpace(cs Array) (*imgColorSpace, bool) {
	if len(cs) < 2 {
		return nil, false
	}
	st, ok := d.Resolve(cs[1]).(*Stream)
	if !ok {
		return nil, false
	}
	switch intValue(d.Resolve(st.Dict.Get("N"))) {
	case 1:
		return deviceColorSpace("DeviceGray")
	case 3:
		return deviceColorSpace("DeviceRGB")
	case 4:
		return deviceColorSpace("DeviceCMYK")
	}
	if alt := st.Dict.Get("Alternate"); alt != nil {
		return d.resolveColorSpace(alt)
	}
	return nil, false
}

// indexedColorSpace resolves [/Indexed base hival lookup] into a palette lookup
// over its base colour space.
func (d *Document) indexedColorSpace(cs Array) (*imgColorSpace, bool) {
	if len(cs) < 4 {
		return nil, false
	}
	base, ok := d.resolveColorSpace(cs[1])
	if !ok || base.indexed {
		return nil, false
	}
	hival := intValue(d.Resolve(cs[2]))
	if hival < 0 || hival > 65535 {
		return nil, false
	}
	var lookup []byte
	switch t := d.Resolve(cs[3]).(type) {
	case String:
		lookup = t.Value
	case *Stream:
		lookup = decodeContentStream(d, t)
	default:
		return nil, false
	}
	if len(lookup) < (hival+1)*base.ncomp {
		return nil, false
	}
	return &imgColorSpace{
		ncomp:   1,
		indexed: true,
		hival:   hival,
		lookup:  lookup,
		base:    base,
		decode:  []float64{0, float64(hival)},
	}, true
}

// labColorSpace resolves a [/Lab dict] space; toRGB converts CIE L*a*b* (D50) to
// sRGB.
func (d *Document) labColorSpace(cs Array) (*imgColorSpace, bool) {
	wp := [3]float64{0.9642, 1.0, 0.8249} // D50, the usual Lab reference white
	amin, amax, bmin, bmax := -100.0, 100.0, -100.0, 100.0
	if len(cs) >= 2 {
		if dict := d.ResolveDict(cs[1]); dict != nil {
			if arr, ok := d.Resolve(dict.Get("WhitePoint")).(Array); ok && len(arr) == 3 {
				for i := 0; i < 3; i++ {
					wp[i] = floatValue(d.Resolve(arr[i]))
				}
			}
			if arr, ok := d.Resolve(dict.Get("Range")).(Array); ok && len(arr) == 4 {
				amin, amax = floatValue(d.Resolve(arr[0])), floatValue(d.Resolve(arr[1]))
				bmin, bmax = floatValue(d.Resolve(arr[2])), floatValue(d.Resolve(arr[3]))
			}
		}
	}
	return &imgColorSpace{
		ncomp:  3,
		decode: []float64{0, 100, amin, amax, bmin, bmax},
		toRGB: func(c []float64) (uint8, uint8, uint8) {
			return labToRGB(c[0], c[1], c[2], wp)
		},
	}, true
}

func cmykToRGB(c []float64) (uint8, uint8, uint8) {
	k := c[3]
	return clamp8((1 - c[0]) * (1 - k)), clamp8((1 - c[1]) * (1 - k)), clamp8((1 - c[2]) * (1 - k))
}

// labToRGB converts CIE L*a*b* to sRGB via XYZ, adapted to the given white point.
func labToRGB(l, a, bb float64, wp [3]float64) (uint8, uint8, uint8) {
	fy := (l + 16) / 116
	fx := fy + a/500
	fz := fy - bb/200
	g := func(t float64) float64 {
		if t3 := t * t * t; t3 > 0.008856 {
			return t3
		}
		return (t - 16.0/116) / 7.787
	}
	x := wp[0] * g(fx)
	y := wp[1] * g(fy)
	z := wp[2] * g(fz)
	// XYZ (D50-ish) to linear sRGB.
	r := 3.1338*x - 1.6168*y - 0.4906*z
	gr := -0.9787*x + 1.9161*y + 0.0334*z
	b := 0.0719*x - 0.2289*y + 1.4052*z
	return clamp8(gammaSRGB(r)), clamp8(gammaSRGB(gr)), clamp8(gammaSRGB(b))
}

func gammaSRGB(v float64) float64 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

func clamp8(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 1:
		return 255
	default:
		return uint8(v*255 + 0.5)
	}
}

// imageDecode returns the effective /Decode array (min,max per component). It
// uses an explicit /Decode when present, else the colour-space default — which
// for an Indexed space is [0, 2^bpc-1] so a sample is used directly as an index.
func (d *Document) imageDecode(st *Stream, cs *imgColorSpace, bpc int) []float64 {
	def := cs.decode
	if cs.indexed {
		def = []float64{0, float64(int(1)<<uint(bpc) - 1)}
	}
	if arr, ok := d.Resolve(st.Dict.Get("Decode")).(Array); ok && len(arr) == len(def) {
		out := make([]float64, len(arr))
		for i := range arr {
			out[i] = floatValue(d.Resolve(arr[i]))
		}
		return out
	}
	return def
}

// applySoftMask composites a /SMask (a DeviceGray image giving per-pixel alpha)
// onto im, nearest-neighbour scaling the mask to the image's dimensions.
func (d *Document) applySoftMask(st *Stream, im *image.NRGBA) {
	sm, ok := d.Resolve(st.Dict.Get("SMask")).(*Stream)
	if !ok {
		return
	}
	alpha, mw, mh, ok := d.decodeAlphaMask(sm)
	if !ok || mw <= 0 || mh <= 0 {
		return
	}
	w, h := im.Rect.Dx(), im.Rect.Dy()
	for y := 0; y < h; y++ {
		my := y * mh / h
		for x := 0; x < w; x++ {
			mx := x * mw / w
			o := im.PixOffset(x, y)
			im.Pix[o+3] = alpha[my*mw+mx]
		}
	}
}

// decodeAlphaMask decodes a soft-mask image XObject to one alpha byte per pixel.
func (d *Document) decodeAlphaMask(sm *Stream) (alpha []byte, w, h int, ok bool) {
	w = intValue(d.Resolve(sm.Dict.Get("Width")))
	h = intValue(d.Resolve(sm.Dict.Get("Height")))
	bpc := intValue(d.Resolve(sm.Dict.Get("BitsPerComponent")))
	if w <= 0 || h <= 0 || bpc <= 0 {
		return nil, 0, 0, false
	}
	filters := streamFilters(d, sm)
	last := ""
	if len(filters) > 0 {
		last = string(filters[len(filters)-1])
	}
	if last == "DCTDecode" || last == "JPXDecode" {
		return nil, 0, 0, false // decoded elsewhere; rare for a mask
	}
	raw := decodeContentStream(d, sm)
	if !sampleDataFits(raw, w, h, 1, bpc) {
		return nil, 0, 0, false
	}
	dec := []float64{0, 1}
	if arr, ok := d.Resolve(sm.Dict.Get("Decode")).(Array); ok && len(arr) == 2 {
		dec[0], dec[1] = floatValue(d.Resolve(arr[0])), floatValue(d.Resolve(arr[1]))
	}
	maxval := float64(int(1)<<uint(bpc) - 1)
	alpha = make([]byte, w*h)
	sr := sampleReader{data: raw, bpc: bpc, w: w, ncomp: 1}
	for y := 0; y < h; y++ {
		sr.startRow(y)
		for x := 0; x < w; x++ {
			v := dec[0] + float64(sr.next())*(dec[1]-dec[0])/maxval
			alpha[y*w+x] = clamp8(v)
		}
	}
	return alpha, w, h, true
}
