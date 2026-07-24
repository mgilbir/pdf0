package pdf0

// JBIG2 generic refinement region decoding (ISO/IEC 14492 §6.3). Refinement
// re-decodes a bitmap using a reference bitmap (a previously decoded version, or
// a symbol being adapted) as extra context, so only the differences are coded.
// It is used both as a standalone region and to adapt symbol instances in text
// regions (SBREFINE) and symbol dictionaries (SDREFAGG).

// Refinement templates (T.88 6.3.5.3): coding pixels read the bitmap being
// decoded, reference pixels read the reference bitmap. The order is significant
// — it defines the context bit weights, which the reused TPGRON contexts assume.
var jbRefCodingTemplates = [2][]atPixel{
	{{0, -1}, {1, -1}, {-1, 0}},
	{{-1, -1}, {0, -1}, {1, -1}, {-1, 0}},
}

var jbRefReferenceTemplates = [2][]atPixel{
	{{0, -1}, {1, -1}, {-1, 0}, {0, 0}, {1, 0}, {-1, 1}, {0, 1}, {1, 1}},
	{{0, -1}, {-1, 0}, {0, 0}, {1, 0}, {0, 1}, {1, 1}},
}

var jbRefReusedContexts = [2]int{0x0020, 0x0008}

// decodeRefinement decodes a refinement of ref into a new w x h bitmap using the
// caller-owned GR context. offX/offY position the reference under the output
// (output pixel (x,y) sees reference pixel (x-offX, y-offY)). template must be 0
// or 1; at holds the two AT pixels for template 0 (coding AT then reference AT).
func decodeRefinement(dec *mqDecoder, cx []mqState, w, h, template int, ref *jbBitmap, offX, offY int, tpgron bool, at []atPixel) *jbBitmap {
	coding := append([]atPixel{}, jbRefCodingTemplates[template]...)
	reference := append([]atPixel{}, jbRefReferenceTemplates[template]...)
	if template == 0 {
		if len(at) >= 1 {
			coding = append(coding, at[0])
		}
		if len(at) >= 2 {
			reference = append(reference, at[1])
		}
	}

	out := newJBBitmap(w, h, 0)
	ltp := 0
	for y := 0; y < h; y++ {
		if tpgron {
			ltp ^= dec.decode(cx, jbRefReusedContexts[template])
		}
		for x := 0; x < w; x++ {
			if ltp == 1 {
				// Typical prediction: if the reference 3x3 neighbourhood is
				// uniform, copy that value rather than decoding.
				rx, ry := x-offX, y-offY
				sum := 0
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						sum += int(ref.get(rx+dx, ry+dy))
					}
				}
				if sum == 0 {
					out.pix[y*w+x] = 0
					continue
				}
				if sum == 9 {
					out.pix[y*w+x] = 1
					continue
				}
			}
			ctx := 0
			for _, p := range coding {
				ctx = (ctx << 1) | int(out.get(x+p.x, y+p.y))
			}
			for _, p := range reference {
				ctx = (ctx << 1) | int(ref.get(x+p.x-offX, y+p.y-offY))
			}
			out.pix[y*w+x] = byte(dec.decode(cx, ctx))
		}
	}
	return out
}
