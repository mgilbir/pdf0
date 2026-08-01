package pdf0

import (
	"github.com/mgilbir/pdf0/object"
	"image"
	"image/color"
	"math"
)

// This file turns the decoded samples of an image XObject into an image.Image,
// honouring the colour space, bit depth, /Decode array and /SMask. It covers the
// device and CIE-based spaces (Gray/RGB/CMYK, CalGray/CalRGB, Lab, ICCBased by
// component count), Indexed palettes and Separation/DeviceN (via tint-transform
// function evaluation into their alternate space).

// imgColorSpace is a resolved image colour space: how many components a pixel's
// samples carry and how to turn them into RGB. For an Indexed space the single
// component is a palette index and toRGB is unused (the index is looked up).
type imgColorSpace struct {
	ncomp   int
	indexed bool
	hival   int
	lookup  []byte // indexed: base.ncomp bytes per palette entry
	base    *imgColorSpace
	tintFn  Object                            // Separation/DeviceN: tint-transform function
	alt     *imgColorSpace                    // Separation/DeviceN: alternate colour space
	decode  []float64                         // default /Decode (min,max per component)
	toRGB   func(c []float64) (r, g, b uint8) // c holds ncomp values already mapped through /Decode
	// toRGB16 is the full-precision counterpart of toRGB, used for 16-bit images.
	// When nil the 8-bit toRGB result is promoted (byte*257), which is exact for
	// spaces whose precision is inherently 8-bit.
	toRGB16 func(c []float64) (r, g, b uint16)
}

// toRGB16Comps converts already-decoded components to 16-bit RGB, using the
// space's own 16-bit conversion when available and otherwise promoting the
// 8-bit result losslessly across the full range (0xFF -> 0xFFFF).
func (cs *imgColorSpace) toRGB16Comps(c []float64) (r, g, b uint16) {
	if cs.toRGB16 != nil {
		return cs.toRGB16(c)
	}
	r8, g8, b8 := cs.toRGB(c)
	return uint16(r8) * 257, uint16(g8) * 257, uint16(b8) * 257
}

// buildImage converts an image XObject's decoded samples to an image, applying
// the colour space, bit depth, /Decode array and soft mask. ok is false for a
// layout it cannot render.
func buildImage(d *Document, st *Stream, raw []byte, w, h, bpc int) (image.Image, bool) {
	if w <= 0 || h <= 0 || bpc <= 0 {
		return nil, false
	}
	cs, ok := resolveColorSpace(d, st.Dict.Get("ColorSpace"))
	if !ok {
		return nil, false
	}
	decode := imageDecode(d, st, cs, bpc)
	maxval := float64(int(1)<<uint(bpc) - 1)
	if maxval <= 0 {
		return nil, false
	}
	if !sampleDataFits(raw, w, h, cs.ncomp, bpc) {
		return nil, false
	}

	colorKey := colorKeyMask(d, st, cs.ncomp) // range array making matching samples transparent

	if bpc == 16 {
		return buildImage16(d, st, raw, w, h, cs, decode, maxval, colorKey)
	}

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
	applyStencilMask(d, st, im)
	applySoftMask(d, st, im)
	return im, true
}

// buildImage16 renders a 16-bit-per-component image to an *image.NRGBA64,
// preserving the full sample precision that an 8-bit *image.NRGBA would discard.
// It mirrors the 8-bit path but keeps colour arithmetic in floats down to a
// 16-bit clamp so a DeviceGray sample of 0xFFFF yields R=0xFFFF, 0x8000 ~ 0x8000.
func buildImage16(d *Document, st *Stream, raw []byte, w, h int, cs *imgColorSpace, decode []float64, maxval float64, colorKey []int) (image.Image, bool) {
	im := image.NewNRGBA64(image.Rect(0, 0, w, h))
	sr := sampleReader{data: raw, bpc: 16, w: w, ncomp: cs.ncomp}
	comps := make([]float64, cs.ncomp)
	rawS := make([]int, cs.ncomp)
	for y := 0; y < h; y++ {
		sr.startRow(y)
		for x := 0; x < w; x++ {
			var r, g, b uint16
			if cs.indexed {
				rawS[0] = sr.next()
				idx := int(math.Round(decode[0] + float64(rawS[0])*(decode[1]-decode[0])/maxval))
				r, g, b = cs.paletteRGB16(idx)
			} else {
				for k := 0; k < cs.ncomp; k++ {
					rawS[k] = sr.next()
					comps[k] = decode[2*k] + float64(rawS[k])*(decode[2*k+1]-decode[2*k])/maxval
				}
				r, g, b = cs.toRGB16Comps(comps)
			}
			a := uint16(0xFFFF)
			if colorKey != nil && inColorKey(rawS, colorKey) {
				a = 0
			}
			im.SetNRGBA64(x, y, color.NRGBA64{R: r, G: g, B: b, A: a})
		}
	}
	applyStencilMask64(d, st, im)
	applySoftMask64(d, st, im)
	return im, true
}

// colorKeyMask returns the /Mask colour-key range array [min1 max1 …] when
// present and well-formed for ncomp components, else nil.
func colorKeyMask(d *Document, st *Stream, ncomp int) []int {
	arr, ok := d.Resolve(st.Dict.Get("Mask")).(Array)
	if !ok || len(arr) != 2*ncomp {
		return nil
	}
	out := make([]int, len(arr))
	for i := range arr {
		out[i] = object.Int(d.Resolve(arr[i]))
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

// stencilMask decodes a stencil /Mask (a 1-bit image XObject) into its packed
// rows plus the sample value that marks a pixel hidden. ok is false when there
// is no usable stencil mask. /Decode [1 0] inverts which sample hides.
func stencilMask(d *Document, st *Stream) (data []byte, mw, mh int, hideBit byte, ok bool) {
	mk, ok := d.Resolve(st.Dict.Get("Mask")).(*Stream)
	if !ok {
		return nil, 0, 0, 0, false
	}
	mw = object.Int(d.Resolve(mk.Dict.Get("Width")))
	mh = object.Int(d.Resolve(mk.Dict.Get("Height")))
	data = decodeImageSamples(d.canceler(), mk, d.lim())
	if mw <= 0 || mh <= 0 || !sampleDataFits(data, mw, mh, 1, 1) {
		return nil, 0, 0, 0, false
	}
	hideBit = byte(1) // default /Decode [0 1]: a 1 sample hides
	if arr, ok := d.Resolve(mk.Dict.Get("Decode")).(Array); ok && len(arr) == 2 && object.Float(d.Resolve(arr[0])) == 1 {
		hideBit = 0
	}
	return data, mw, mh, hideBit, true
}

// applyStencilMask applies a stencil /Mask (a 1-bit image XObject): samples of 1
// mark pixels to hide, so those become transparent. /Decode [1 0] inverts it.
func applyStencilMask(d *Document, st *Stream, im *image.NRGBA) {
	data, mw, mh, hideBit, ok := stencilMask(d, st)
	if !ok {
		return
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

// applyStencilMask64 is the *image.NRGBA64 counterpart of applyStencilMask.
func applyStencilMask64(d *Document, st *Stream, im *image.NRGBA64) {
	data, mw, mh, hideBit, ok := stencilMask(d, st)
	if !ok {
		return
	}
	stride := (mw + 7) / 8
	w, h := im.Rect.Dx(), im.Rect.Dy()
	for y := 0; y < h; y++ {
		my := y * mh / h
		row := data[my*stride:]
		for x := 0; x < w; x++ {
			mx := x * mw / w
			if (row[mx/8]>>(7-uint(mx%8)))&1 == hideBit {
				o := im.PixOffset(x, y)
				im.Pix[o+6], im.Pix[o+7] = 0, 0 // 16-bit alpha, big-endian
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

// paletteRGB16 is the 16-bit counterpart of paletteRGB. Palette entries are
// 8-bit, so precision comes only from the base space's conversion arithmetic
// (Lab/ICC); toRGB16Comps promotes exactly when the base has no 16-bit path.
func (cs *imgColorSpace) paletteRGB16(idx int) (r, g, b uint16) {
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
	return cs.base.toRGB16Comps(bc)
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
func resolveColorSpace(d *Document, obj Object) (*imgColorSpace, bool) {
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
			return iccBasedColorSpace(d, cs)
		case "CalRGB":
			return deviceColorSpace("DeviceRGB")
		case "CalGray":
			return deviceColorSpace("DeviceGray")
		case "Lab":
			return labColorSpace(d, cs)
		case "Indexed", "I":
			return indexedColorSpace(d, cs)
		case "DeviceGray", "DeviceRGB", "DeviceCMYK", "G", "RGB", "CMYK":
			return deviceColorSpace(string(head))
		case "Separation":
			return separationColorSpace(d, cs)
		case "DeviceN":
			return deviceNColorSpace(d, cs)
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
		}, toRGB16: func(c []float64) (uint16, uint16, uint16) {
			v := clamp16(c[0])
			return v, v, v
		}}, true
	case "DeviceRGB", "CalRGB", "RGB":
		return &imgColorSpace{ncomp: 3, decode: []float64{0, 1, 0, 1, 0, 1}, toRGB: func(c []float64) (uint8, uint8, uint8) {
			return clamp8(c[0]), clamp8(c[1]), clamp8(c[2])
		}, toRGB16: func(c []float64) (uint16, uint16, uint16) {
			return clamp16(c[0]), clamp16(c[1]), clamp16(c[2])
		}}, true
	case "DeviceCMYK", "CMYK":
		return &imgColorSpace{ncomp: 4, decode: []float64{0, 1, 0, 1, 0, 1, 0, 1}, toRGB: cmykToRGB, toRGB16: cmykToRGB16}, true
	}
	return nil, false
}

// iccBasedColorSpace renders an ICCBased space by its component count (/N): 1 as
// grayscale, 3 as RGB, 4 as CMYK, matching the profile's device class. A
// present /Alternate is used when /N is absent or unusual.
func iccBasedColorSpace(d *Document, cs Array) (*imgColorSpace, bool) {
	if len(cs) < 2 {
		return nil, false
	}
	st, ok := d.Resolve(cs[1]).(*Stream)
	if !ok {
		return nil, false
	}
	switch object.Int(d.Resolve(st.Dict.Get("N"))) {
	case 1:
		return deviceColorSpace("DeviceGray")
	case 3:
		return deviceColorSpace("DeviceRGB")
	case 4:
		return deviceColorSpace("DeviceCMYK")
	}
	if alt := st.Dict.Get("Alternate"); alt != nil {
		return resolveColorSpace(d, alt)
	}
	return nil, false
}

// indexedColorSpace resolves [/Indexed base hival lookup] into a palette lookup
// over its base colour space.
func indexedColorSpace(d *Document, cs Array) (*imgColorSpace, bool) {
	if len(cs) < 4 {
		return nil, false
	}
	base, ok := resolveColorSpace(d, cs[1])
	if !ok || base.indexed {
		return nil, false
	}
	hival := object.Int(d.Resolve(cs[2]))
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

// separationColorSpace resolves [/Separation name altSpace tintFn]: one tint
// component fed through tintFn into the alternate space.
func separationColorSpace(d *Document, cs Array) (*imgColorSpace, bool) {
	if len(cs) < 4 {
		return nil, false
	}
	return tintColorSpace(d, 1, cs[2], cs[3])
}

// deviceNColorSpace resolves [/DeviceN names altSpace tintFn]: len(names) tint
// components fed through tintFn into the alternate space.
func deviceNColorSpace(d *Document, cs Array) (*imgColorSpace, bool) {
	if len(cs) < 4 {
		return nil, false
	}
	names, ok := d.Resolve(cs[1]).(Array)
	if !ok || len(names) == 0 {
		return nil, false
	}
	return tintColorSpace(d, len(names), cs[2], cs[3])
}

// tintColorSpace builds an imgColorSpace with ncomp tint components whose toRGB
// runs the tint-transform function into the alternate space's toRGB. It refuses
// the space if the tint function does not evaluate for a probe input, so callers
// fall back to the raw bytes rather than render garbage.
func tintColorSpace(d *Document, ncomp int, altObj, tintFn Object) (*imgColorSpace, bool) {
	alt, ok := resolveColorSpace(d, altObj)
	if !ok || alt.indexed {
		return nil, false
	}
	// Verify the tint function evaluates to the alternate space's arity.
	probe := make([]float64, ncomp)
	altComps, ok := d.view().EvalFunction(tintFn, probe)
	if !ok || len(altComps) != alt.ncomp {
		return nil, false
	}
	decode := make([]float64, 2*ncomp)
	for i := 0; i < ncomp; i++ {
		decode[2*i], decode[2*i+1] = 0, 1
	}
	return &imgColorSpace{
		ncomp:  ncomp,
		tintFn: tintFn,
		alt:    alt,
		decode: decode,
		toRGB: func(c []float64) (uint8, uint8, uint8) {
			comps, ok := d.view().EvalFunction(tintFn, c)
			if !ok || len(comps) != alt.ncomp {
				return 0, 0, 0
			}
			return alt.toRGB(comps)
		},
	}, true
}

// labColorSpace resolves a [/Lab dict] space; toRGB converts CIE L*a*b* (D50) to
// sRGB.
func labColorSpace(d *Document, cs Array) (*imgColorSpace, bool) {
	wp := [3]float64{0.9642, 1.0, 0.8249} // D50, the usual Lab reference white
	amin, amax, bmin, bmax := -100.0, 100.0, -100.0, 100.0
	if len(cs) >= 2 {
		if dict := d.ResolveDict(cs[1]); dict != nil {
			if arr, ok := d.Resolve(dict.Get("WhitePoint")).(Array); ok && len(arr) == 3 {
				for i := 0; i < 3; i++ {
					wp[i] = object.Float(d.Resolve(arr[i]))
				}
			}
			if arr, ok := d.Resolve(dict.Get("Range")).(Array); ok && len(arr) == 4 {
				amin, amax = object.Float(d.Resolve(arr[0])), object.Float(d.Resolve(arr[1]))
				bmin, bmax = object.Float(d.Resolve(arr[2])), object.Float(d.Resolve(arr[3]))
			}
		}
	}
	return &imgColorSpace{
		ncomp:  3,
		decode: []float64{0, 100, amin, amax, bmin, bmax},
		toRGB: func(c []float64) (uint8, uint8, uint8) {
			r, g, b := labToSRGB(c[0], c[1], c[2], wp)
			return clamp8(r), clamp8(g), clamp8(b)
		},
		toRGB16: func(c []float64) (uint16, uint16, uint16) {
			r, g, b := labToSRGB(c[0], c[1], c[2], wp)
			return clamp16(r), clamp16(g), clamp16(b)
		},
	}, true
}

func cmykToRGB(c []float64) (uint8, uint8, uint8) {
	k := c[3]
	return clamp8((1 - c[0]) * (1 - k)), clamp8((1 - c[1]) * (1 - k)), clamp8((1 - c[2]) * (1 - k))
}

func cmykToRGB16(c []float64) (uint16, uint16, uint16) {
	k := c[3]
	return clamp16((1 - c[0]) * (1 - k)), clamp16((1 - c[1]) * (1 - k)), clamp16((1 - c[2]) * (1 - k))
}

// labToSRGB converts CIE L*a*b* to gamma-encoded sRGB in [0,1], adapted to the
// given white point. Callers clamp to 8- or 16-bit.
func labToSRGB(l, a, bb float64, wp [3]float64) (r, g, b float64) {
	fy := (l + 16) / 116
	fx := fy + a/500
	fz := fy - bb/200
	gg := func(t float64) float64 {
		if t3 := t * t * t; t3 > 0.008856 {
			return t3
		}
		return (t - 16.0/116) / 7.787
	}
	x := wp[0] * gg(fx)
	y := wp[1] * gg(fy)
	z := wp[2] * gg(fz)
	// XYZ (D50-ish) to linear sRGB.
	lr := 3.1338*x - 1.6168*y - 0.4906*z
	lg := -0.9787*x + 1.9161*y + 0.0334*z
	lb := 0.0719*x - 0.2289*y + 1.4052*z
	return gammaSRGB(lr), gammaSRGB(lg), gammaSRGB(lb)
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

// clamp16 maps a [0,1] value to the full 16-bit range so 1.0 -> 0xFFFF exactly.
func clamp16(v float64) uint16 {
	switch {
	case v <= 0:
		return 0
	case v >= 1:
		return 65535
	default:
		return uint16(v*65535 + 0.5)
	}
}

// imageDecode returns the effective /Decode array (min,max per component). It
// uses an explicit /Decode when present, else the colour-space default — which
// for an Indexed space is [0, 2^bpc-1] so a sample is used directly as an index.
func imageDecode(d *Document, st *Stream, cs *imgColorSpace, bpc int) []float64 {
	def := cs.decode
	if cs.indexed {
		def = []float64{0, float64(int(1)<<uint(bpc) - 1)}
	}
	if arr, ok := d.Resolve(st.Dict.Get("Decode")).(Array); ok && len(arr) == len(def) {
		out := make([]float64, len(arr))
		for i := range arr {
			out[i] = object.Float(d.Resolve(arr[i]))
		}
		return out
	}
	return def
}

// applySoftMask composites a /SMask (a DeviceGray image giving per-pixel alpha)
// onto im, nearest-neighbour scaling the mask to the image's dimensions.
func applySoftMask(d *Document, st *Stream, im *image.NRGBA) {
	sm, ok := d.Resolve(st.Dict.Get("SMask")).(*Stream)
	if !ok {
		return
	}
	alpha, mw, mh, ok := decodeAlphaMask(d, sm)
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

// applySoftMask64 is the *image.NRGBA64 counterpart of applySoftMask. The mask
// carries one alpha byte per pixel, promoted to 16 bits (byte*257).
func applySoftMask64(d *Document, st *Stream, im *image.NRGBA64) {
	sm, ok := d.Resolve(st.Dict.Get("SMask")).(*Stream)
	if !ok {
		return
	}
	alpha, mw, mh, ok := decodeAlphaMask(d, sm)
	if !ok || mw <= 0 || mh <= 0 {
		return
	}
	w, h := im.Rect.Dx(), im.Rect.Dy()
	for y := 0; y < h; y++ {
		my := y * mh / h
		for x := 0; x < w; x++ {
			mx := x * mw / w
			a := uint16(alpha[my*mw+mx]) * 257
			o := im.PixOffset(x, y)
			im.Pix[o+6], im.Pix[o+7] = uint8(a>>8), uint8(a) // 16-bit alpha, big-endian
		}
	}
}

// decodeAlphaMask decodes a soft-mask image XObject to one alpha byte per pixel.
func decodeAlphaMask(d *Document, sm *Stream) (alpha []byte, w, h int, ok bool) {
	w = object.Int(d.Resolve(sm.Dict.Get("Width")))
	h = object.Int(d.Resolve(sm.Dict.Get("Height")))
	bpc := object.Int(d.Resolve(sm.Dict.Get("BitsPerComponent")))
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
	raw := decodeImageSamples(d.canceler(), sm, d.lim())
	if !sampleDataFits(raw, w, h, 1, bpc) {
		return nil, 0, 0, false
	}
	dec := []float64{0, 1}
	if arr, ok := d.Resolve(sm.Dict.Get("Decode")).(Array); ok && len(arr) == 2 {
		dec[0], dec[1] = object.Float(d.Resolve(arr[0])), object.Float(d.Resolve(arr[1]))
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
