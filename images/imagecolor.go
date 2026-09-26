package images

import (
	"errors"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
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
	tintFn  object.Object                     // Separation/DeviceN: tint-transform function
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

// errUnsupportedLayout is buildImage declining a sample layout it cannot
// render: a colour space it does not know, a bit depth it does not read, data
// shorter than the geometry. The caller reports it with a codec-specific Note.
var errUnsupportedLayout = errors.New("unsupported sample layout")

// buildImage converts an image XObject's decoded samples to an image, applying
// the colour space, bit depth, /Decode array and soft mask. The error is
// errUnsupportedLayout for a layout it cannot render, a *core.LimitError for
// geometry over the image budget, or the colour-space resolver's refusal of a
// space that is cyclic or nested too deeply.
// maskErr, when not nil, says why a mask was left out of an image that was
// otherwise built.
func buildImage(d core.View, st *object.Stream, raw []byte, w, h, bpc int) (m image.Image, maskErr, err error) {
	if w <= 0 || h <= 0 || bpc <= 0 {
		return nil, nil, errUnsupportedLayout
	}
	cs, err := resolveColorSpace(d, st.Dict.Get("ColorSpace"))
	if err != nil {
		return nil, nil, err
	}
	if err := d.Limits.CheckImage(int64(w), int64(h), int64(cs.ncomp)); err != nil {
		return nil, nil, err
	}
	decode := imageDecode(d, st, cs, bpc)
	maxval := float64(int(1)<<uint(bpc) - 1)
	if maxval <= 0 {
		return nil, nil, errUnsupportedLayout
	}
	if !sampleDataFits(raw, w, h, cs.ncomp, bpc) {
		return nil, nil, errUnsupportedLayout
	}

	colorKey := colorKeyMask(d, st, cs.ncomp) // range array making matching samples transparent

	if bpc == 16 {
		m, maskErr := buildImage16(d, st, raw, w, h, cs, decode, maxval, colorKey)
		return m, maskErr, nil
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
	return im, applySoftMask(d, st, im), nil
}

// buildImage16 renders a 16-bit-per-component image to an *image.NRGBA64,
// preserving the full sample precision that an 8-bit *image.NRGBA would discard.
// It mirrors the 8-bit path but keeps colour arithmetic in floats down to a
// 16-bit clamp so a DeviceGray sample of 0xFFFF yields R=0xFFFF, 0x8000 ~ 0x8000.
func buildImage16(d core.View, st *object.Stream, raw []byte, w, h int, cs *imgColorSpace, decode []float64, maxval float64, colorKey []int) (image.Image, error) {
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
	return im, applySoftMask64(d, st, im)
}

// colorKeyMask returns the /Mask colour-key range array [min1 max1 …] when
// present and well-formed for ncomp components, else nil.
func colorKeyMask(d core.View, st *object.Stream, ncomp int) []int {
	arr, ok := d.Resolve(st.Dict.Get("Mask")).(object.Array)
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
func stencilMask(d core.View, st *object.Stream) (data []byte, mw, mh int, hideBit byte, ok bool) {
	mk, ok := d.Resolve(st.Dict.Get("Mask")).(*object.Stream)
	if !ok {
		return nil, 0, 0, 0, false
	}
	mw = object.Int(d.Resolve(mk.Dict.Get("Width")))
	mh = object.Int(d.Resolve(mk.Dict.Get("Height")))
	data, _ = decodeImageSamples(d, mk) // reason: extraction; an undecoded mask is not applied
	if mw <= 0 || mh <= 0 || !sampleDataFits(data, mw, mh, 1, 1) {
		return nil, 0, 0, 0, false
	}
	hideBit = byte(1) // default /Decode [0 1]: a 1 sample hides
	if arr, ok := d.Resolve(mk.Dict.Get("Decode")).(object.Array); ok && len(arr) == 2 && object.Float(d.Resolve(arr[0])) == 1 {
		hideBit = 0
	}
	return data, mw, mh, hideBit, true
}

// applyStencilMask applies a stencil /Mask (a 1-bit image XObject): samples of 1
// mark pixels to hide, so those become transparent. /Decode [1 0] inverts it.
func applyStencilMask(d core.View, st *object.Stream, im *image.NRGBA) {
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
func applyStencilMask64(d core.View, st *object.Stream, im *image.NRGBA64) {
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
// pixels with ncomp components at bpc bits, rows byte-aligned. Every argument
// can come from the file, so nothing is multiplied that could wrap: each
// dimension is compared against what the data could hold, by division. A
// non-positive argument never fits.
func sampleDataFits(data []byte, w, h, ncomp, bpc int) bool {
	if w <= 0 || h <= 0 || ncomp <= 0 || bpc <= 0 {
		return false
	}
	bits := int64(len(data)) * 8
	if int64(w) > bits/int64(ncomp)/int64(bpc) {
		return false // one row alone is larger than the data
	}
	rowBytes := (int64(w)*int64(ncomp)*int64(bpc) + 7) / 8 // ≤ len(data), no overflow
	return int64(h) <= int64(len(data))/rowBytes
}

// maxColorSpaceDepth bounds how deeply colour spaces may nest: an Indexed base,
// a Separation or DeviceN alternate, an ICCBased /Alternate, each of which is a
// colour space in its own right. Real files nest two or three deep (an Indexed
// over an ICCBased with a device alternate); sixteen is the bound the other
// file-structure walks in this package use.
const maxColorSpaceDepth = 16

// errColorSpaceDepth is a colour space nested past maxColorSpaceDepth.
var errColorSpaceDepth = fmt.Errorf("colour space nested more than %d deep", maxColorSpaceDepth)

// colorSpaceCycleError is a colour space that contains itself: object n is its
// own base or alternate, directly or through others. Every one of those is a
// reference the file controls, and the resolver used to follow them until the
// stack overflowed — a fatal error, not a recoverable panic (audit 2026-09-22
// C13).
type colorSpaceCycleError int

func (e colorSpaceCycleError) Error() string {
	return fmt.Sprintf("colour space object %d contains itself", int(e))
}

// csResolver resolves one image's colour space. It carries the depth and the
// object numbers on the path from the image's /ColorSpace to the space being
// resolved, so that a space reached again through its own base or alternate is
// refused as a cycle and a chain that is merely long is refused at the depth
// bound. The path is unwound as each level returns: a space may legitimately
// be reached twice by different routes, and only a revisit on the current path
// is a cycle. Each level has at most one nested space, so the walk is a chain
// and the depth bound alone bounds its cost.
type csResolver struct {
	d     core.View
	depth int
	path  []int
}

// resolveColorSpace resolves a PDF colour-space object to an imgColorSpace. The
// error is errUnsupportedLayout for a space this decoder cannot render,
// errColorSpaceDepth or a colorSpaceCycleError for one it refuses to follow.
func resolveColorSpace(d core.View, obj object.Object) (*imgColorSpace, error) {
	r := &csResolver{d: d}
	return r.resolve(obj)
}

func (r *csResolver) resolve(obj object.Object) (*imgColorSpace, error) {
	if r.depth >= maxColorSpaceDepth {
		return nil, errColorSpaceDepth
	}
	if ref, ok := obj.(object.IndirectRef); ok {
		for _, n := range r.path {
			if n == ref.Number {
				return nil, colorSpaceCycleError(n)
			}
		}
		r.path = append(r.path, ref.Number)
		defer func() { r.path = r.path[:len(r.path)-1] }()
	}
	r.depth++
	defer func() { r.depth-- }()

	d := r.d
	switch cs := d.Resolve(obj).(type) {
	case object.Name:
		return deviceColorSpace(string(cs))
	case object.Array:
		if len(cs) == 0 {
			return nil, errUnsupportedLayout
		}
		head, _ := d.Resolve(cs[0]).(object.Name)
		switch head {
		case "ICCBased":
			return r.iccBased(cs)
		case "CalRGB":
			return deviceColorSpace("DeviceRGB")
		case "CalGray":
			return deviceColorSpace("DeviceGray")
		case "Lab":
			return labColorSpace(d, cs), nil
		case "Indexed", "I":
			return r.indexed(cs)
		case "DeviceGray", "DeviceRGB", "DeviceCMYK", "G", "RGB", "CMYK":
			return deviceColorSpace(string(head))
		case "Separation":
			if len(cs) < 4 {
				return nil, errUnsupportedLayout
			}
			return r.tint(1, cs[2], cs[3])
		case "DeviceN":
			// [/DeviceN names altSpace tintFn]: len(names) tint components fed
			// through tintFn into the alternate space.
			if len(cs) < 4 {
				return nil, errUnsupportedLayout
			}
			names, ok := d.Resolve(cs[1]).(object.Array)
			if !ok || len(names) == 0 {
				return nil, errUnsupportedLayout
			}
			return r.tint(len(names), cs[2], cs[3])
		}
	}
	return nil, errUnsupportedLayout
}

func deviceColorSpace(name string) (*imgColorSpace, error) {
	switch name {
	case "DeviceGray", "CalGray", "G":
		return &imgColorSpace{ncomp: 1, decode: []float64{0, 1}, toRGB: func(c []float64) (uint8, uint8, uint8) {
			v := clamp8(c[0])
			return v, v, v
		}, toRGB16: func(c []float64) (uint16, uint16, uint16) {
			v := clamp16(c[0])
			return v, v, v
		}}, nil
	case "DeviceRGB", "CalRGB", "RGB":
		return &imgColorSpace{ncomp: 3, decode: []float64{0, 1, 0, 1, 0, 1}, toRGB: func(c []float64) (uint8, uint8, uint8) {
			return clamp8(c[0]), clamp8(c[1]), clamp8(c[2])
		}, toRGB16: func(c []float64) (uint16, uint16, uint16) {
			return clamp16(c[0]), clamp16(c[1]), clamp16(c[2])
		}}, nil
	case "DeviceCMYK", "CMYK":
		return &imgColorSpace{ncomp: 4, decode: []float64{0, 1, 0, 1, 0, 1, 0, 1}, toRGB: cmykToRGB, toRGB16: cmykToRGB16}, nil
	}
	return nil, errUnsupportedLayout
}

// iccBased renders an ICCBased space by its component count (/N): 1 as
// grayscale, 3 as RGB, 4 as CMYK, matching the profile's device class. A
// present /Alternate is used when /N is absent or unusual.
func (r *csResolver) iccBased(cs object.Array) (*imgColorSpace, error) {
	if len(cs) < 2 {
		return nil, errUnsupportedLayout
	}
	st, ok := r.d.Resolve(cs[1]).(*object.Stream)
	if !ok {
		return nil, errUnsupportedLayout
	}
	switch object.Int(r.d.Resolve(st.Dict.Get("N"))) {
	case 1:
		return deviceColorSpace("DeviceGray")
	case 3:
		return deviceColorSpace("DeviceRGB")
	case 4:
		return deviceColorSpace("DeviceCMYK")
	}
	if alt := st.Dict.Get("Alternate"); alt != nil {
		return r.resolve(alt)
	}
	return nil, errUnsupportedLayout
}

// indexed resolves [/Indexed base hival lookup] into a palette lookup over its
// base colour space.
func (r *csResolver) indexed(cs object.Array) (*imgColorSpace, error) {
	if len(cs) < 4 {
		return nil, errUnsupportedLayout
	}
	base, err := r.resolve(cs[1])
	if err != nil {
		return nil, err
	}
	if base.indexed {
		return nil, errUnsupportedLayout
	}
	d := r.d
	hival := object.Int(d.Resolve(cs[2]))
	if hival < 0 || hival > 65535 {
		return nil, errUnsupportedLayout
	}
	var lookup []byte
	switch t := d.Resolve(cs[3]).(type) {
	case object.String:
		lookup = t.Value
	case *object.Stream:
		lookup, _ = d.Content(t) // reason: extraction; a lookup that did not decode is too short below and the image is not rendered
	default:
		return nil, errUnsupportedLayout
	}
	if len(lookup) < (hival+1)*base.ncomp {
		return nil, errUnsupportedLayout
	}
	return &imgColorSpace{
		ncomp:   1,
		indexed: true,
		hival:   hival,
		lookup:  lookup,
		base:    base,
		decode:  []float64{0, float64(hival)},
	}, nil
}

// The tint-transform memo's bounds: inputs of at most tintMemoArity
// components are memoised, and at most maxTintMemo of them per colour space.
const (
	tintMemoArity = 8
	maxTintMemo   = 1 << 16
)

// tint builds an imgColorSpace with ncomp tint components (one for Separation,
// one per colorant for DeviceN) whose toRGB runs the tint-transform function
// into the alternate space's toRGB. It refuses the space if the tint function
// does not evaluate for a probe input, so callers fall back to the raw bytes
// rather than render garbage.
func (r *csResolver) tint(ncomp int, altObj, tintFn object.Object) (*imgColorSpace, error) {
	alt, err := r.resolve(altObj)
	if err != nil {
		return nil, err
	}
	if alt.indexed {
		return nil, errUnsupportedLayout
	}
	d := r.d
	// Verify the tint function evaluates to the alternate space's arity.
	probe := make([]float64, ncomp)
	altComps, ok := d.EvalFunction(tintFn, probe)
	if !ok || len(altComps) != alt.ncomp {
		return nil, errUnsupportedLayout
	}
	decode := make([]float64, 2*ncomp)
	for i := 0; i < ncomp; i++ {
		decode[2*i], decode[2*i+1] = 0, 1
	}
	// The tint transform is evaluated once per distinct input, not once per
	// pixel. An image's samples take few distinct values — an 8-bit
	// Separation has 256 — and a type-4 program of half a million steps,
	// evaluated for each of a million pixels, was hours of work (audit
	// 2026-09-22 C51). The memo is bounded (maxTintMemo entries, for inputs of
	// at most tintMemoArity components); past it, inputs are evaluated as
	// before, each evaluation charged to the run's work meter.
	type tintKey [tintMemoArity]float64
	type rgb struct{ r, g, b uint8 }
	memo := map[tintKey]rgb{}
	eval := func(c []float64) (uint8, uint8, uint8) {
		comps, ok := d.EvalFunction(tintFn, c)
		if !ok || len(comps) != alt.ncomp {
			return 0, 0, 0
		}
		return alt.toRGB(comps)
	}
	return &imgColorSpace{
		ncomp:  ncomp,
		tintFn: tintFn,
		alt:    alt,
		decode: decode,
		toRGB: func(c []float64) (uint8, uint8, uint8) {
			if len(c) > tintMemoArity {
				return eval(c)
			}
			var k tintKey
			copy(k[:], c)
			if v, ok := memo[k]; ok {
				return v.r, v.g, v.b
			}
			r, g, b := eval(c)
			if len(memo) < maxTintMemo {
				memo[k] = rgb{r, g, b}
			}
			return r, g, b
		},
	}, nil
}

// labColorSpace resolves a [/Lab dict] space; toRGB converts CIE L*a*b* (D50) to
// sRGB.
func labColorSpace(d core.View, cs object.Array) *imgColorSpace {
	wp := [3]float64{0.9642, 1.0, 0.8249} // D50, the usual Lab reference white
	amin, amax, bmin, bmax := -100.0, 100.0, -100.0, 100.0
	if len(cs) >= 2 {
		if dict := d.ResolveDict(cs[1]); dict != nil {
			if arr, ok := d.Resolve(dict.Get("WhitePoint")).(object.Array); ok && len(arr) == 3 {
				for i := 0; i < 3; i++ {
					wp[i] = object.Float(d.Resolve(arr[i]))
				}
			}
			if arr, ok := d.Resolve(dict.Get("Range")).(object.Array); ok && len(arr) == 4 {
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
	}
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
func imageDecode(d core.View, st *object.Stream, cs *imgColorSpace, bpc int) []float64 {
	def := cs.decode
	if cs.indexed {
		def = []float64{0, float64(int(1)<<uint(bpc) - 1)}
	}
	if arr, ok := d.Resolve(st.Dict.Get("Decode")).(object.Array); ok && len(arr) == len(def) {
		out := make([]float64, len(arr))
		for i := range arr {
			out[i] = object.Float(d.Resolve(arr[i]))
		}
		return out
	}
	return def
}

// applySoftMask composites a /SMask (a DeviceGray image giving per-pixel alpha)
// onto im, nearest-neighbour scaling the mask to the image's dimensions. It
// returns the budget error when the mask's own geometry was refused, so the
// caller can say why the image carries no mask; a mask that is absent or does
// not decode is left out silently, as it always was.
func applySoftMask(d core.View, st *object.Stream, im *image.NRGBA) error {
	sm, ok := d.Resolve(st.Dict.Get("SMask")).(*object.Stream)
	if !ok {
		return nil
	}
	alpha, mw, mh, err := decodeAlphaMask(d, sm)
	if err != nil || alpha == nil {
		return err
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
	return nil
}

// applySoftMask64 is the *image.NRGBA64 counterpart of applySoftMask. The mask
// carries one alpha byte per pixel, promoted to 16 bits (byte*257).
func applySoftMask64(d core.View, st *object.Stream, im *image.NRGBA64) error {
	sm, ok := d.Resolve(st.Dict.Get("SMask")).(*object.Stream)
	if !ok {
		return nil
	}
	alpha, mw, mh, err := decodeAlphaMask(d, sm)
	if err != nil || alpha == nil {
		return err
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
	return nil
}

// decodeAlphaMask decodes a soft-mask image XObject to one alpha byte per
// pixel. A nil alpha with a nil error is a mask that is absent, malformed or in
// a codec this path does not read; a *core.LimitError is one whose geometry is
// over the image budget. The mask's dimensions are its own, independent of the
// image it masks, so they are held to the budget separately.
func decodeAlphaMask(d core.View, sm *object.Stream) (alpha []byte, w, h int, err error) {
	w = object.Int(d.Resolve(sm.Dict.Get("Width")))
	h = object.Int(d.Resolve(sm.Dict.Get("Height")))
	bpc := object.Int(d.Resolve(sm.Dict.Get("BitsPerComponent")))
	if w <= 0 || h <= 0 || bpc <= 0 {
		return nil, 0, 0, nil
	}
	if err := d.Limits.CheckImage(int64(w), int64(h), 1); err != nil {
		d.Note(core.GuardImagePixels, err.Error(), 0)
		return nil, 0, 0, fmt.Errorf("the /SMask was not applied: %w", err)
	}
	filters := d.StreamFilters(sm)
	last := ""
	if len(filters) > 0 {
		last = string(filters[len(filters)-1])
	}
	if last == "DCTDecode" || last == "JPXDecode" {
		return nil, 0, 0, nil // decoded elsewhere; rare for a mask
	}
	raw, _ := decodeImageSamples(d, sm) // reason: extraction; an undecoded soft mask is not applied
	if !sampleDataFits(raw, w, h, 1, bpc) {
		return nil, 0, 0, nil
	}
	dec := []float64{0, 1}
	if arr, ok := d.Resolve(sm.Dict.Get("Decode")).(object.Array); ok && len(arr) == 2 {
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
	return alpha, w, h, nil
}
