package render

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// Glyphs that are rectangles, and why the reftest comparison has to know.
//
// # The measurement that made this necessary
//
// picture_test.go compares fills as a picture and text as marks: two runs agree
// if they are the same string at the same place. That is right for a face whose
// glyphs are letters, and it is exactly wrong for the font a quarter of this
// suite is written in.
//
// Ahem's glyphs are not letters. Every one of them is a filled rectangle, and
// that is the whole reason the font exists — a test asserts that a box is 80x20
// by setting four characters of 20px Ahem, because those four characters *are* a
// black 80x20 rectangle. The suite's reference documents then draw the same
// rectangle the ordinary way, with a background colour or a solid PNG. So the
// pair renders identically and the comparison saw a run of text on one side and
// a fill on the other, and called every one of them a difference.
//
// Measured over the suite before this existed: 2293 failures, of which 767 were
// in documents that use Ahem. Converting its glyphs to the rectangles they are
// turned 361 of those failures into passes, with one test moving the other way.
// That is the largest single cluster of failures in the suite and none of it was
// a layout fault — the geometry was already right, to the unit, in every one of
// the cases dumped and read by hand.
//
// # The rule, which is about outlines rather than about Ahem
//
// Naming Ahem here would be a fudge: a table of "these characters are squares"
// keyed on a font this repository does not ship, unverifiable and wrong the day
// the file changes. What is implemented instead is a fact about *outlines*:
//
//	a glyph whose outline is a single closed contour of four on-curve points
//	forming an axis-aligned rectangle inks exactly that rectangle.
//
// That is true of any font, it is read out of the font program rather than
// assumed, and it is the same reasoning the uniform-image equivalence in
// picture_test.go already rests on — a mark that puts one colour over one
// rectangle is a fill of that rectangle, whatever produced it.
//
// It happens to cover the whole of Ahem. Classifying every one of the 278
// characters in the shipped file gives: 258 full em squares, 14 blank, and 6
// partial rectangles — 'p' is the descender box, 'É' the ascender box, and two
// pairs are a vertical and a horizontal bar. TestBlockGlyphsCoverAhem asserts
// that, against the file, so a font that stopped being rectangles would be
// caught rather than silently mis-compared.
//
// # What this does not do, and the two guards on it
//
// Turning a run into rectangles loses the ability to see that two *different*
// words are at the same place. For a face of rectangles that is not a loss —
// "XXXX" and "abcd" in Ahem are the same picture, and no browser running the
// reftest could tell them apart either — but it would be a real one for a face
// where only some glyphs are rectangles. So the conversion is all or nothing: a
// run is converted only when every character in it is a rectangle or blank, and
// otherwise it is compared as text exactly as before.
//
// The second guard is on the arithmetic. Reconstructing where each glyph sits
// inside a run means re-deriving the pen positions, and a reconstruction that
// disagreed with the engine would hide the disagreement rather than show it. So
// the widths are checked: the advances summed here must add up to the width the
// face itself measures for the run, and a run where they do not is left as text.
// That catches kerning, ligatures and anything else shaping does between two
// characters, none of which this reconstruction models.

// blockFont answers, for a rune, whether the glyph is a filled rectangle and
// where that rectangle is, in font units with the origin on the baseline and y
// upwards.
type blockFont struct {
	unitsPerEm int
	// rects is keyed by rune. A rune present with an empty rectangle is a glyph
	// that inks nothing, such as a space; a rune absent is one whose outline is
	// not a rectangle, or that the font does not have.
	rects map[rune]blockRect
}

// blockRect is a glyph's ink, in font units. An empty one is a blank glyph.
type blockRect struct {
	x0, y0, x1, y1 int
	blank          bool
}

// blockFonts maps the faces the harness loaded to their rectangle tables.
//
// It is keyed on the face pointer because that is what a display list carries,
// and it is written once while the font set is built and only read afterwards.
var blockFonts = map[*fonts.Face]*blockFont{}

// blockFills converts the rectangle-glyph runs of a display list into fills,
// leaving every other op alone.
//
// It runs over a rendering before it is compared rather than inside the
// comparison so that pictureEqual stays what it is: a function of two display
// lists. What arrives here is a list where a run of Ahem is text; what leaves is
// one where it is the rectangles it draws.
func blockFills(ops []Op) []Op {
	if len(blockFonts) == 0 {
		return ops
	}
	var out []Op
	changed := false
	for i, op := range ops {
		v, ok := op.(DrawText)
		if !ok {
			if changed {
				out = append(out, op)
			}
			continue
		}
		bf := blockFonts[v.Face]
		if bf == nil {
			if changed {
				out = append(out, op)
			}
			continue
		}
		fills, ok := bf.fills(v, v.Face.Measure)
		if !ok {
			if changed {
				out = append(out, op)
			}
			continue
		}
		if !changed {
			// The first conversion; everything before it passed through
			// untouched, so copy it in one go rather than op by op.
			out = append(make([]Op, 0, len(ops)+len(fills)), ops[:i]...)
			changed = true
		}
		out = append(out, fills...)
	}
	if !changed {
		return ops
	}
	return out
}

// fills is the rectangles a run inks, or nothing if the run is not all
// rectangles or the reconstruction does not add up.
//
// measure is the face's own width for a string at a size. It is a parameter
// rather than a call on v.Face because of what the probe below the guard found:
// no font in this checkout has two rectangle glyphs that shape to anything other
// than the sum of their advances, so nothing available could make the guard
// fire, and a guard that cannot be seen to fail is decoration. Injecting the
// measurement is what lets a test show it working.
func (b *blockFont) fills(v DrawText, measure func(s string, size float64) float64) ([]Op, bool) {
	if v.Face == nil || v.Text == "" {
		return nil, false
	}
	runes := []rune(v.Text)
	rects := make([]blockRect, len(runes))
	for i, r := range runes {
		br, ok := b.rects[r]
		if !ok {
			return nil, false
		}
		rects[i] = br
	}

	// The advance of each glyph, from the face rather than from the table, so
	// that the two are independent and the check below is worth making.
	adv := make([]float64, len(runes))
	total := 0.0
	for i, r := range runes {
		adv[i] = measure(string(r), v.Size.Px())
		total += adv[i]
	}
	// The run's own width, less the letter-spacing layout added, must be what
	// the per-character advances sum to. Anything else means shaping moved a
	// glyph — a kern, a ligature, a mark attached to the letter before it — and
	// this reconstruction does not know where to.
	if math.Abs(total-measure(v.Text, v.Size.Px())) > 0.01 {
		return nil, false
	}

	// A right-to-left run draws its first character at the right-hand end.
	order := make([]int, len(runes))
	for i := range order {
		order[i] = i
		if v.RTL {
			order[i] = len(runes) - 1 - i
		}
	}

	scale := v.Size.Px() / float64(b.unitsPerEm)
	pen := v.At.X
	var out []Op
	for _, i := range order {
		br := rects[i]
		step, ok := style.FromPx(adv[i])
		if !ok {
			return nil, false
		}
		if !br.blank {
			x0, ok0 := style.FromPx(float64(br.x0) * scale)
			x1, ok1 := style.FromPx(float64(br.x1) * scale)
			y0, ok2 := style.FromPx(float64(br.y0) * scale)
			y1, ok3 := style.FromPx(float64(br.y1) * scale)
			if !ok0 || !ok1 || !ok2 || !ok3 {
				return nil, false
			}
			rect := Rect{
				X: pen.Add(x0),
				// y grows downwards on the page and upwards in the font, so
				// the top of the ink is the baseline less its highest point.
				Y: v.At.Y.Sub(y1),
				W: x1.Sub(x0),
				H: y1.Sub(y0),
			}
			// A run that §11.1 cut carries its clip, and the rectangles it is
			// being turned into have to be cut the same way. A glyph whose ink
			// is a rectangle *can* be clipped by arithmetic, which is exactly
			// why this reconstruction is allowed to do it — and forgetting to
			// would make the comparison see the whole of a square that the page
			// shows half of.
			if v.Clip.Active {
				rect = rect.Intersect(v.Clip.Rect)
				if rect.Empty() {
					pen = pen.Add(step).Add(v.CharSpacing)
					continue
				}
			}
			out = append(out, FillRect{Rect: rect, Color: v.Color})
		}
		pen = pen.Add(step).Add(v.CharSpacing)
	}
	return out, true
}

// newBlockFont reads a TrueType font program and classifies every character it
// has a glyph for.
//
// Only the tables this needs are read — the character map to find the glyph, the
// header for the em and the index format, and the outlines themselves. A font
// with no outlines of its own, such as one of the fourteen standard PDF faces,
// has no rectangles to find and returns nothing.
func newBlockFont(data []byte) (*blockFont, error) {
	tables, err := sfntTables(data)
	if err != nil {
		return nil, err
	}
	head, glyf, loca := tables["head"], tables["glyf"], tables["loca"]
	maxp, cmap := tables["maxp"], tables["cmap"]
	if len(head) < 54 || glyf == nil || loca == nil || len(maxp) < 6 || cmap == nil {
		return nil, errors.New("no TrueType outlines")
	}
	upem := int(binary.BigEndian.Uint16(head[18:]))
	if upem <= 0 {
		return nil, errors.New("a font with no em")
	}
	longLoca := int16(binary.BigEndian.Uint16(head[50:])) != 0
	numGlyphs := int(binary.BigEndian.Uint16(maxp[4:]))

	chars, err := cmapFormat4(cmap)
	if err != nil {
		return nil, err
	}
	out := &blockFont{unitsPerEm: upem, rects: map[rune]blockRect{}}
	for r, gid := range chars {
		if gid <= 0 || gid >= numGlyphs {
			continue
		}
		start, end, ok := locaRange(loca, gid, longLoca)
		if !ok {
			continue
		}
		if start == end {
			out.rects[r] = blockRect{blank: true}
			continue
		}
		if end > len(glyf) {
			continue
		}
		if br, ok := rectContour(glyf[start:end]); ok {
			out.rects[r] = br
		}
	}
	if len(out.rects) == 0 {
		return nil, errors.New("no rectangle glyphs")
	}
	return out, nil
}

// sfntTables splits a font program into its tables.
func sfntTables(data []byte) (map[string][]byte, error) {
	if len(data) < 12 {
		return nil, errors.New("not a font")
	}
	n := int(binary.BigEndian.Uint16(data[4:]))
	if 12+n*16 > len(data) {
		return nil, errors.New("a truncated table directory")
	}
	out := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		o := 12 + i*16
		tag := string(data[o : o+4])
		off := int(binary.BigEndian.Uint32(data[o+8:]))
		length := int(binary.BigEndian.Uint32(data[o+12:]))
		if off < 0 || length < 0 || off > len(data) {
			continue
		}
		if off+length > len(data) {
			length = len(data) - off
		}
		out[tag] = data[off : off+length]
	}
	return out, nil
}

// locaRange is a glyph's extent within the outline table.
func locaRange(loca []byte, gid int, long bool) (start, end int, ok bool) {
	if long {
		if (gid+2)*4 > len(loca) {
			return 0, 0, false
		}
		start = int(binary.BigEndian.Uint32(loca[gid*4:]))
		end = int(binary.BigEndian.Uint32(loca[(gid+1)*4:]))
	} else {
		if (gid+2)*2 > len(loca) {
			return 0, 0, false
		}
		start = int(binary.BigEndian.Uint16(loca[gid*2:])) * 2
		end = int(binary.BigEndian.Uint16(loca[(gid+1)*2:])) * 2
	}
	if end < start {
		return 0, 0, false
	}
	return start, end, true
}

// rectContour reports whether a glyph description is a single axis-aligned
// filled rectangle, and what rectangle.
//
// Everything else — a composite glyph, more than one contour, a curve, four
// points that are not the corners of a rectangle — is refused. A glyph this
// refuses is compared as text, which is what every glyph was before, so refusing
// too much costs a pass and accepting too much costs the oracle its teeth. Each
// condition below has a planted defect against it in blockglyph_check_test.go.
//
// The four-point limit is the one that is a bound on what is analysed rather
// than a claim about rectangles, and saying so is what keeps it honest: a
// contour that goes round the four corners more than once inks the same
// rectangle and is refused anyway, because working out which of those are
// rectangles and which are not is more than this needs to do. Refusing is the
// safe direction. Ahem has no such glyph, and neither does any other font in the
// checkout.
func rectContour(g []byte) (blockRect, bool) {
	if len(g) < 10 {
		return blockRect{}, false
	}
	if int16(binary.BigEndian.Uint16(g)) != 1 {
		// A composite glyph has a negative count, and a letter has several
		// contours. Neither is a rectangle.
		return blockRect{}, false
	}
	o := 10
	if o+2 > len(g) {
		return blockRect{}, false
	}
	npts := int(binary.BigEndian.Uint16(g[o:])) + 1
	o += 2
	if npts != 4 {
		return blockRect{}, false
	}
	if o+2 > len(g) {
		return blockRect{}, false
	}
	o += 2 + int(binary.BigEndian.Uint16(g[o:])) // instructions

	flags := make([]byte, 0, npts)
	for len(flags) < npts {
		if o >= len(g) {
			return blockRect{}, false
		}
		f := g[o]
		o++
		flags = append(flags, f)
		if f&8 != 0 { // repeat
			if o >= len(g) {
				return blockRect{}, false
			}
			for k := int(g[o]); k > 0 && len(flags) < npts; k-- {
				flags = append(flags, f)
			}
			o++
		}
	}
	for _, f := range flags {
		if f&1 == 0 {
			// An off-curve point is a curve, not a corner.
			return blockRect{}, false
		}
	}

	coords := func(shortBit, sameBit byte) ([]int, bool) {
		out := make([]int, npts)
		v := 0
		for i, f := range flags {
			switch {
			case f&shortBit != 0:
				if o >= len(g) {
					return nil, false
				}
				d := int(g[o])
				o++
				if f&sameBit == 0 {
					d = -d
				}
				v += d
			case f&sameBit != 0:
				// Repeat of the previous coordinate.
			default:
				if o+2 > len(g) {
					return nil, false
				}
				v += int(int16(binary.BigEndian.Uint16(g[o:])))
				o += 2
			}
			out[i] = v
		}
		return out, true
	}
	xs, ok := coords(2, 16)
	if !ok {
		return blockRect{}, false
	}
	ys, ok := coords(4, 32)
	if !ok {
		return blockRect{}, false
	}

	x0, x1, y0, y1 := xs[0], xs[0], ys[0], ys[0]
	for i := range xs {
		x0, x1 = min(x0, xs[i]), max(x1, xs[i])
		y0, y1 = min(y0, ys[i]), max(y1, ys[i])
	}
	// The four points must be the four corners of the bounding box, each once.
	// That covers the degenerate shapes as well as the misshapen ones: four
	// collinear points enclose no area, and they cannot make four distinct
	// corners of a box one of whose sides is zero — there are only two such
	// points to be had. An earlier version tested for zero area separately and a
	// planted defect showed the branch was unreachable, which is the whole
	// reason this comment says so rather than the code saying it twice.
	corners := map[[2]int]bool{}
	for i := range xs {
		if (xs[i] != x0 && xs[i] != x1) || (ys[i] != y0 && ys[i] != y1) {
			return blockRect{}, false
		}
		corners[[2]int{xs[i], ys[i]}] = true
	}
	if len(corners) != 4 {
		return blockRect{}, false
	}
	// Four corners can be visited in an order that is not a rectangle. A contour
	// that crosses from one corner to the one diagonally opposite draws a bow
	// tie — two triangles meeting at a point, which is half the ink and in the
	// wrong places. Every side of a rectangle moves along one axis only, so
	// each step must change exactly one coordinate.
	for i := range xs {
		j := (i + 1) % len(xs)
		if (xs[i] == xs[j]) == (ys[i] == ys[j]) {
			return blockRect{}, false
		}
	}
	return blockRect{x0: x0, y0: y0, x1: x1, y1: y1}, true
}

// cmapFormat4 reads the Unicode character map.
//
// Format 4 is the one every font in this checkout uses for the Basic
// Multilingual Plane, and the characters a rectangle font is addressed by are
// all in it. A font with only a format 12 table yields nothing here and its runs
// are compared as text, which is the safe direction.
func cmapFormat4(cmap []byte) (map[rune]int, error) {
	if len(cmap) < 4 {
		return nil, errors.New("a truncated character map")
	}
	n := int(binary.BigEndian.Uint16(cmap[2:]))
	out := map[rune]int{}
	for i := 0; i < n; i++ {
		o := 4 + i*8
		if o+8 > len(cmap) {
			break
		}
		off := int(binary.BigEndian.Uint32(cmap[o+4:]))
		if off < 0 || off+14 > len(cmap) {
			continue
		}
		sub := cmap[off:]
		if binary.BigEndian.Uint16(sub) != 4 {
			continue
		}
		segX2 := int(binary.BigEndian.Uint16(sub[6:]))
		endO, startO := 14, 14+segX2+2
		deltaO, rangeO := startO+segX2, startO+2*segX2
		if rangeO+segX2 > len(sub) {
			continue
		}
		for s := 0; s < segX2/2; s++ {
			end := int(binary.BigEndian.Uint16(sub[endO+s*2:]))
			start := int(binary.BigEndian.Uint16(sub[startO+s*2:]))
			delta := int(binary.BigEndian.Uint16(sub[deltaO+s*2:]))
			ro := int(binary.BigEndian.Uint16(sub[rangeO+s*2:]))
			if start > end {
				continue
			}
			for c := start; c <= end && c != 0xFFFF; c++ {
				var gid int
				if ro == 0 {
					gid = (c + delta) & 0xFFFF
				} else {
					gi := rangeO + s*2 + ro + (c-start)*2
					if gi+2 > len(sub) {
						continue
					}
					gid = int(binary.BigEndian.Uint16(sub[gi:]))
					if gid != 0 {
						gid = (gid + delta) & 0xFFFF
					}
				}
				if gid != 0 {
					out[rune(c)] = gid
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no Unicode character map")
	}
	return out, nil
}
