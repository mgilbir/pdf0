package htmlpdf

import (
	"testing"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// A reader's model of where each glyph of a page lands, built from the file
// alone: the fonts' dictionaries as written — the encoding's writing mode,
// /W and /DW, /W2 and /DW2 — and the text operators of the content stream,
// followed as ISO 32000-2 9.4.4 and 9.2.4 say a reader follows them. It knows
// nothing of forme or of the fonts package, so a test that holds its answer
// to forme's shaping holds the font dictionary and the content stream to it
// together.

// placedGlyph is one code a page shows, the font it was shown in, and where
// the glyph's horizontal origin is, in the coordinates the text matrix maps
// into.
type placedGlyph struct {
	font string
	code int
	at   [2]float64
	// vertical is a glyph shown in a font that writes down the page.
	vertical bool
}

// fontModel is a font as its dictionary states its metrics, in thousandths of
// an em.
type fontModel struct {
	vertical bool // an Identity-V (or other WMode 1) Type 0 font
	twoByte  bool // a Type 0 font over Identity-H or -V
	w0       func(code int) float64
	// v is a code's vertical metrics: its advance down the line, negative,
	// and its position vector.
	v func(code int) (w1y, vx, vy float64)
}

// fontModels reads the models of the fonts a page's resources name. A simple
// font's widths are asked of width, since a standard font's are not in the
// file.
func fontModels(t *testing.T, doc *pdf0.Document, page *object.Dictionary, width func(font string, code int) float64) map[string]fontModel {
	t.Helper()
	out := map[string]fontModel{}
	res := doc.ResolveDict(page.Get("Resources"))
	fontsDict := doc.ResolveDict(res.Get("Font"))
	if fontsDict == nil {
		return out
	}
	num := func(o object.Object) float64 {
		switch v := doc.Resolve(o).(type) {
		case object.Integer:
			return float64(v)
		case object.Real:
			return float64(v)
		}
		t.Fatalf("a font metric is %v, not a number", o)
		return 0
	}
	for name := range fontsDict.Keys() {
		f := doc.ResolveDict(fontsDict.Get(name))
		key := string(name)
		if f.Get("Subtype") != object.Name("Type0") {
			out[key] = fontModel{w0: func(code int) float64 { return width(key, code) }}
			continue
		}
		enc, _ := f.Get("Encoding").(object.Name)
		if enc != "Identity-H" && enc != "Identity-V" {
			t.Fatalf("font %s has encoding %v, which this reader does not model", key, f.Get("Encoding"))
		}
		desc := doc.ResolveDict(doc.Resolve(f.Get("DescendantFonts")).(object.Array)[0])
		widths := map[int]float64{}
		dw := 1000.0
		if d := desc.Get("DW"); d != nil {
			dw = num(d)
		}
		if w, ok := doc.Resolve(desc.Get("W")).(object.Array); ok {
			for i := 0; i < len(w); {
				first := int(num(w[i]))
				if arr, ok := doc.Resolve(w[i+1]).(object.Array); ok {
					for j, x := range arr {
						widths[first+j] = num(x)
					}
					i += 2
					continue
				}
				last := int(num(w[i+1]))
				for c := first; c <= last; c++ {
					widths[c] = num(w[i+2])
				}
				i += 3
			}
		}
		type vm struct{ w1y, vx, vy float64 }
		vmetrics := map[int]vm{}
		dw2 := [2]float64{880, -1000}
		if d, ok := doc.Resolve(desc.Get("DW2")).(object.Array); ok {
			dw2 = [2]float64{num(d[0]), num(d[1])}
		}
		if w, ok := doc.Resolve(desc.Get("W2")).(object.Array); ok {
			for i := 0; i < len(w); {
				first := int(num(w[i]))
				if arr, ok := doc.Resolve(w[i+1]).(object.Array); ok {
					if len(arr)%3 != 0 {
						t.Fatalf("a /W2 run has %d numbers, not triples", len(arr))
					}
					for j := 0; j < len(arr); j += 3 {
						vmetrics[first+j/3] = vm{num(arr[j]), num(arr[j+1]), num(arr[j+2])}
					}
					i += 2
					continue
				}
				last := int(num(w[i+1]))
				for c := first; c <= last; c++ {
					vmetrics[c] = vm{num(w[i+2]), num(w[i+3]), num(w[i+4])}
				}
				i += 5
			}
		}
		w0 := func(code int) float64 {
			if w, ok := widths[code]; ok {
				return w
			}
			return dw
		}
		out[key] = fontModel{
			vertical: enc == "Identity-V",
			twoByte:  true,
			w0:       w0,
			v: func(code int) (float64, float64, float64) {
				if m, ok := vmetrics[code]; ok {
					return m.w1y, m.vx, m.vy
				}
				// A CID with no /W2 entry: /DW2's advance and origin height,
				// and half its width across (9.7.4.3).
				return dw2[1], w0(code) / 2, dw2[0]
			},
		}
	}
	return out
}

// placedGlyphs follows a content stream's text state — Tm, Td, Tf, Ts, the
// strings of Tj and TJ and TJ's numbers — over the fonts' models, and
// returns every glyph shown and where, and for each Tm where the current
// point is when the next Tm or the end of the text object comes: where the
// run drawn from that Tm left the pen. Tz scales the horizontal displacements
// — a glyph's width and a TJ number — as 9.4.4 has it (tx is multiplied by
// Th) and leaves vertical ones alone. Tc, Tw, TL, T*, TD, ' and " are not
// written by the code under test, and meeting one fails the test rather than
// being followed wrongly.
func placedGlyphs(t *testing.T, stream []byte, models map[string]fontModel) (glyphs []placedGlyph, ends [][2]float64) {
	t.Helper()
	var (
		operands []core.ContentToken
		tm       [6]float64
		line     [2]float64 // the text line matrix's origin, in text space of the last Tm
		cur      [2]float64 // the current point, likewise
		rise     float64
		size     float64
		font     string
		model    fontModel
		inArray  bool
		started  bool  // a Tm has been set whose run has not ended
		th       = 1.0 // horizontal scaling, Tz / 100
		saved    []float64
	)
	textSpace := func(x, y float64) [2]float64 {
		return [2]float64{tm[0]*x + tm[2]*y + tm[4], tm[1]*x + tm[3]*y + tm[5]}
	}
	show := func(codes []byte) {
		step := 1
		if model.twoByte {
			step = 2
		}
		for j := 0; j+step <= len(codes); j += step {
			code := int(codes[j])
			if step == 2 {
				code = code<<8 | int(codes[j+1])
			}
			if model.vertical {
				if rise != 0 {
					t.Fatalf("a rise of %v in vertical writing, which moves along the line", rise)
				}
				w1y, vx, vy := model.v(code)
				glyphs = append(glyphs, placedGlyph{font, code, textSpace(cur[0]-vx*size/1000, cur[1]-vy*size/1000), true})
				cur[1] += w1y * size / 1000
				continue
			}
			glyphs = append(glyphs, placedGlyph{font, code, textSpace(cur[0], cur[1]+rise), false})
			cur[0] += model.w0(code) * size / 1000 * th
		}
	}
	for tk := range core.TokenizeContent(core.Canceler{}, stream) {
		switch tk.Kind {
		case core.KindArrayStart:
			inArray = true
			continue
		case core.KindArrayEnd:
			inArray = false
			continue
		case core.KindString:
			if inArray {
				show(tk.Str)
				continue
			}
		case core.KindNumber:
			if inArray {
				// Subtracted from the coordinate the font writes along.
				if model.vertical {
					cur[1] -= tk.Number() * size / 1000
				} else {
					cur[0] -= tk.Number() * size / 1000 * th
				}
				continue
			}
		case core.KindOp:
			n := len(operands)
			switch {
			case tk.Op == "Tm" && n >= 6:
				if started {
					ends = append(ends, textSpace(cur[0], cur[1]))
				}
				started = true
				for i, o := range operands[n-6:] {
					tm[i] = o.Number()
				}
				line, cur = [2]float64{}, [2]float64{}
			case tk.Op == "ET" && started:
				ends = append(ends, textSpace(cur[0], cur[1]))
				started = false
			case tk.Op == "Td" && n >= 2:
				line[0] += operands[n-2].Number()
				line[1] += operands[n-1].Number()
				cur = line
			case tk.Op == "Tf" && n >= 2:
				font, size = operands[n-2].Name, operands[n-1].Number()
				m, ok := models[font]
				if !ok {
					t.Fatalf("the stream selects %s, which the page's resources do not name", font)
				}
				model = m
			case tk.Op == "Ts" && n >= 1:
				rise = operands[n-1].Number()
			case tk.Op == "Tz" && n >= 1:
				th = operands[n-1].Number() / 100
			case tk.Op == "q":
				// The text state is part of the graphics state (9.3.1), so
				// a Q takes a run's Tz away with the rest of it.
				saved = append(saved, th)
			case tk.Op == "Q" && len(saved) > 0:
				th, saved = saved[len(saved)-1], saved[:len(saved)-1]
			case tk.Op == "Tj" && n >= 1:
				show(operands[n-1].Str)
			case tk.Op == "Tc" || tk.Op == "Tw" || tk.Op == "TL" ||
				tk.Op == "T*" || tk.Op == "'" || tk.Op == "\"" || tk.Op == "TD":
				t.Fatalf("the stream uses %s, which this reader does not follow", tk.Op)
			}
			operands = operands[:0]
			continue
		}
		operands = append(operands, tk)
	}
	return glyphs, ends
}
