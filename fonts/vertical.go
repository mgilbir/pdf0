package fonts

import (
	"errors"
	"sort"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

// A face's vertical form: the same font, written down the page.
//
// PDF sets text down the page with a CIDFont whose CMap has writing mode 1
// (ISO 32000-2 9.7.4.3). For a face addressed by glyph index that is
// Identity-V, the vertical twin of the Identity-H every composite face here is
// written with: the codes are the same two bytes, so the ToUnicode CMap, /W,
// /CIDSet and the program are the same too. What changes is what a code
// moves the pen by. In vertical writing the text position moves down by the
// glyph's vertical advance, w1y, and the glyph is drawn with its vertical
// origin at the pen, the position vector (vx, vy) from its horizontal origin.
// The CIDFont's /W2 states those three numbers for each CID and /DW2 the
// usual ones.
//
// They are stated from forme's shape.Face.GlyphVerticalMetrics, which is the
// glyph's own metrics: vmtx and VORG where the face has them, and otherwise
// HarfBuzz's fallbacks (the top phantom point, the ink centred in the line,
// the line's height), which are what shaping starts from. /W is written from
// GlyphAdvance for the same reason: the font dictionary describes the font,
// and shaping's adjustments to a run — a mark's advance taken away, 'vkrn'
// and 'vpal' moving the pen, an offset — are written in the content stream as
// displacements against it, down the line as a TJ number and across it as a
// move of the text line (drawPlannedVertical).
//
// The two forms are two Type 0 fonts over one descendant: one subset, one
// descriptor, one /W and /W2, one ToUnicode CMap. EmbedForms writes the forms
// it is asked for over it, and a document (pdf0's Page.Faces) asks for every
// form its pages have named, each under the name a page drew with.

// errNoVerticalForm is a face that cannot be written vertically: a simple or
// standard font, whose codes are one-byte characters. Writing mode is a
// property of a CMap, and only a composite font has one.
var errNoVerticalForm = errors.New("fonts: only a composite face has a vertical form; " +
	"a simple or standard font is written horizontally, and DrawUpright places its glyphs one by one")

// Vertical is the face's vertical form: the same font, embedded as a Type 0
// font with Identity-V encoding over the same descendant, whose /W2 states each
// glyph's vertical metrics.
//
// Name it on a page as its own font — Page.Faces maps a name to it as to any
// face — select it with Tf, and draw with its DrawUpright. The two forms share
// everything the embedding is made of: the glyphs used, what each was drawn
// for, the subset. A document naming both writes one font program for them.
// Asking again returns the same form, and a vertical form is its own.
//
// Only a composite face has one. A simple or standard face's codes are
// characters, not CIDs, and writing mode belongs to a CIDFont's CMap; its
// DrawUpright places each glyph in the horizontal font instead.
func (f *Face) Vertical() (*Face, error) {
	if f.horizontal != nil {
		return f, nil
	}
	if !f.composite() {
		return nil, errNoVerticalForm
	}
	if f.isType3() {
		return nil, errNoType3Vertical
	}
	if f.vertical == nil {
		f.vertical = &Face{Face: f.Face, rec: f.record(), horizontal: f}
	}
	return f.vertical, nil
}

// IsVertical reports whether the face is a vertical form, from Vertical.
func (f *Face) IsVertical() bool { return f.horizontal != nil }

// Horizontal is the face a vertical form belongs to, and a horizontal face
// itself.
func (f *Face) Horizontal() *Face {
	if f.horizontal != nil {
		return f.horizontal
	}
	return f
}

// drawPlannedVertical writes glyphs set upright in the vertical form: each code
// moves the pen by the glyph's /W2 advance and is hung from its /W2 origin, so
// what is written around a glyph is only where shaping differs from those.
//
// A glyph wants its horizontal origin at pen + (XOffset-VOriginX,
// YOffset-VOriginY), and the font hangs it at the current point less (vx,
// vy). So the current point is moved from the pen by
// (XOffset+vx-VOriginX, YOffset+vy-VOriginY) before the glyph and back after
// it, and the pen then has to end YAdvance further on where the font moved it
// w1y. Down the line both are TJ numbers, which in vertical writing move the
// pen along y (ISO 32000-2 9.4.3), so they take the same sign convention as
// across: a number is minus the movement. Across the line a text operator
// cannot move the pen, and Td moves the line, from the start of the line
// rather than from the pen, so it is given both the move across and how far
// down the line the pen has come. The rise is not used: it moves along text
// space's y, which here is along the line.
func (f *Face) drawPlannedVertical(b *content.Builder, glyphs []Glyph, segs []segment, size float64) {
	var (
		run    []byte
		line   float64 // how far down the line the pen is from the line's start, in thousandths
		across float64 // how far across the line the current line is moved
	)
	flush := func() {
		if len(run) > 0 {
			b.ShowText(run)
			run = nil
		}
	}
	move := func(d float64) {
		if d == 0 {
			return
		}
		flush()
		b.ShowTextAdjusted(content.TextSpan{Adjust: -d})
		line += d
	}
	cross := func(dx float64) {
		if dx == across {
			return
		}
		flush()
		// Td's operands are text space, unscaled by the font size.
		b.MoveText((dx-across)*size/1000, line*size/1000)
		line, across = 0, dx
	}
	for _, seg := range segs {
		if seg.marked {
			flush()
			b.BeginActualText(seg.actual)
		}
		for _, g := range glyphs[seg.lo:seg.hi] {
			adv, vx, vy := f.GlyphVerticalMetrics(g.GID)
			dy := g.YOffset + vy - g.VOriginY
			cross(g.XOffset + vx - g.VOriginX)
			move(dy)
			run = f.appendCode(run, g.GID)
			line += adv
			move(g.YAdvance - adv - dy)
		}
		if seg.marked {
			cross(0)
			flush()
			b.EndMarked()
		}
	}
	// The line is put back where the run began it, so that a caller drawing
	// on from here is on the line it set.
	cross(0)
	flush()
}

// verticalMetrics are /DW2 and /W2 for the glyphs a subset kept: each CID's
// vertical advance and position vector, [w1y vx vy], from the glyph's own
// metrics (ISO 32000-2 9.7.4.3).
//
// /DW2 is [vy w1y], the pair most of the kept glyphs share; a glyph whose
// pair is that one and whose vx is half its width, which is what a CID with
// no /W2 entry gets, needs no entry. The width is the one /W states for it,
// which is the program's advance. Every other kept glyph has one, in the
// consecutive-CID form /W uses.
func (f *Face) verticalMetrics(advances []float64, kept []int) (dw2, w2 object.Array) {
	type metric struct{ w1y, vx, vy, w0 float64 }
	byCID := map[int]metric{}
	for _, gid := range kept {
		if gid < 0 || gid >= len(advances) {
			continue
		}
		a, vx, vy := f.GlyphVerticalMetrics(gid)
		byCID[f.GlyphCode(gid)] = metric{w1y: a, vx: vx, vy: vy, w0: advances[gid]}
	}
	type pair struct{ vy, w1y float64 }
	counts := map[pair]int{}
	for _, m := range byCID {
		counts[pair{m.vy, m.w1y}]++
	}
	// PDF's own default, [880 -1000], until a pair is shared by more.
	best, bestN := pair{880, -1000}, 0
	for p, n := range counts {
		if n > bestN || (n == bestN && (p.vy < best.vy || (p.vy == best.vy && p.w1y < best.w1y))) {
			best, bestN = p, n
		}
	}
	dw2 = object.Array{widthNumber(best.vy), widthNumber(best.w1y)}

	cids := make([]int, 0, len(byCID))
	for cid, m := range byCID {
		if m.vy == best.vy && m.w1y == best.w1y && m.vx == m.w0/2 {
			continue
		}
		cids = append(cids, cid)
	}
	sort.Ints(cids)
	for i := 0; i < len(cids); {
		start := i
		var run object.Array
		for i < len(cids) && (i == start || cids[i] == cids[i-1]+1) {
			m := byCID[cids[i]]
			run = append(run, widthNumber(m.w1y), widthNumber(m.vx), widthNumber(m.vy))
			i++
		}
		w2 = append(w2, object.Integer(cids[start]), run)
	}
	return dw2, w2
}
