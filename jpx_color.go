package pdf0

import (
	"image"
	"image/color"
	"math"
)

// JPEG 2000 multi-component assembly and colour transforms (ISO/IEC 15444-1 §G).
// After each component is wavelet-reconstructed, an optional multiple-component
// transform recombines three components — the reversible RCT (with the 5/3
// transform) or the irreversible ICT/YCbCr (with the 9/7) — and the DC level
// shift maps signed samples back to unsigned pixels. This produces the final
// grayscale or RGB image.

// decodeJPXImage decodes a JPEG 2000 image to an image.Image (grayscale for one
// component, RGB for three or more). It returns nil for forms this decoder does
// not handle so the caller can fall back to the raw bytes.
func decodeJPXImage(im *jpxImage) image.Image {
	if im.cod.cbStyle&0x01 != 0 {
		// Arithmetic-bypass code-block coding is not yet handled. Everything else —
		// multiple quality layers, precincts, and the position progressions — is
		// decoded; a tier-1 consistency check declines anything that desyncs.
		return nil
	}
	nc := len(im.comps)
	if nc == 0 || im.xsiz <= 0 || im.ysiz <= 0 {
		return nil
	}
	reversible := im.cod.transform == 1

	// Per-component full-image sample planes (pre level-shift), as float64. The
	// reversible path yields integer-valued floats, which is exact for the RCT.
	planes := make([][]float64, nc)
	for c := range planes {
		planes[c] = make([]float64, im.xsiz*im.ysiz)
	}

	for t := 0; t < im.numXTiles()*im.numYTiles(); t++ {
		tx0, ty0, tx1, ty1 := im.tileCoords(t)
		tcs := make([]*jpxTileComp, nc)
		for c := 0; c < nc; c++ {
			tcs[c] = buildTileComp(im, c, tx0, ty0, tx1, ty1)
		}
		if err := decodeTilePackets(im, tcs, im.tileData(t)); err != nil {
			return nil
		}
		for c := 0; c < nc; c++ {
			comp := im.comps[c]
			if reversible {
				b := reconstructComponent(im, tcs[c])
				if b == nil {
					return nil
				}
				placePlane(planes[c], im.xsiz, im.ysiz, b.x0, b.y0, b.w, b.h, comp.dx, comp.dy, func(i int) float64 { return float64(b.data[i]) })
			} else {
				b := reconstructComponentF(im, tcs[c])
				if b == nil {
					return nil
				}
				placePlane(planes[c], im.xsiz, im.ysiz, b.x0, b.y0, b.w, b.h, comp.dx, comp.dy, func(i int) float64 { return b.data[i] })
			}
		}
	}

	// Inverse multiple-component transform on the first three components.
	if nc >= 3 && im.cod.mct == 1 {
		if reversible {
			inverseRCT(planes)
		} else {
			inverseICT(planes)
		}
	}

	// DC level shift + clamp per component.
	for c := 0; c < nc; c++ {
		comp := im.comps[c]
		shift := 0.0
		if !comp.signed {
			shift = math.Ldexp(1, comp.depth-1)
		}
		maxv := math.Ldexp(1, comp.depth) - 1
		for i := range planes[c] {
			v := math.Round(planes[c][i]) + shift
			if v < 0 {
				v = 0
			}
			if v > maxv {
				v = maxv
			}
			// Scale to 8 bits.
			if comp.depth < 8 {
				v *= float64(int(1) << uint(8-comp.depth))
			} else if comp.depth > 8 {
				v /= float64(int(1) << uint(comp.depth-8))
			}
			planes[c][i] = v
		}
	}

	if nc == 1 {
		g := image.NewGray(image.Rect(0, 0, im.xsiz, im.ysiz))
		for i, v := range planes[0] {
			g.Pix[i] = byte(v)
		}
		return g
	}
	rgba := image.NewRGBA(image.Rect(0, 0, im.xsiz, im.ysiz))
	for i := 0; i < im.xsiz*im.ysiz; i++ {
		rgba.Pix[i*4+0] = byte(planes[0][i])
		rgba.Pix[i*4+1] = byte(planes[1][i])
		rgba.Pix[i*4+2] = byte(planes[2][i])
		rgba.Pix[i*4+3] = 255
	}
	_ = color.RGBA{}
	return rgba
}

// placePlane copies a reconstructed component band into the full-image plane,
// upsampling by the component's sub-sampling factors (nearest neighbour).
func placePlane(plane []float64, imgW, imgH, bx0, by0, bw, bh, dx, dy int, at func(i int) float64) {
	if dx < 1 {
		dx = 1
	}
	if dy < 1 {
		dy = 1
	}
	for y := 0; y < bh; y++ {
		for x := 0; x < bw; x++ {
			v := at(y*bw + x)
			// Component sample (bx0+x, by0+y) covers reference pixels
			// [(bx0+x)*dx, ...) × [(by0+y)*dy, ...).
			for sy := 0; sy < dy; sy++ {
				iy := (by0+y)*dy + sy
				if iy < 0 || iy >= imgH {
					continue
				}
				for sx := 0; sx < dx; sx++ {
					ix := (bx0+x)*dx + sx
					if ix < 0 || ix >= imgW {
						continue
					}
					plane[iy*imgW+ix] = v
				}
			}
		}
	}
}

// inverseRCT applies the reversible colour transform inverse (T.800 G.2) in
// place on the first three integer-valued planes: (Y,Cb,Cr) -> (R,G,B).
func inverseRCT(planes [][]float64) {
	for i := range planes[0] {
		y, cb, cr := planes[0][i], planes[1][i], planes[2][i]
		g := y - math.Floor((cb+cr)/4)
		r := cr + g
		b := cb + g
		planes[0][i], planes[1][i], planes[2][i] = r, g, b
	}
}

// inverseICT applies the irreversible colour transform inverse (T.800 G.1) in
// place on the first three planes: YCbCr -> RGB.
func inverseICT(planes [][]float64) {
	for i := range planes[0] {
		y, cb, cr := planes[0][i], planes[1][i], planes[2][i]
		r := y + 1.402*cr
		g := y - 0.344136*cb - 0.714136*cr
		b := y + 1.772*cb
		planes[0][i], planes[1][i], planes[2][i] = r, g, b
	}
}
