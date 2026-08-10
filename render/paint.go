package render

import (
	"image"
	"math"
	"sort"
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// The display list: the sixth of §3's stages, and the one that has no PDF in it
// at all.
//
// Keeping this apart from the stage that writes a content stream is what makes
// the whole testing story of §7 possible. The display list is where a rasterizer
// attaches, and it separates "did we lay this out correctly" from "did we emit
// correct PDF" — two failure modes that are miserable to debug together, because
// each can produce a page that looks exactly like the other's symptom.
//
// It is also why the coordinates here are still CSS's: origin at the top left,
// y increasing downwards, lengths in layout units. The flip to PDF's bottom-left
// origin and the conversion to points happen once, in pdfout, and a coordinate
// system that changed halfway through would make every sign error plausible.

// Op is one primitive of the display list.
//
// The set is deliberately small. Anything a backend cannot draw directly is
// something this stage should have decomposed — a border is four filled bands
// rather than a "border" primitive, because a backend that had to understand
// border-collapse would be a second layout engine.
type Op interface{ isOp() }

// FillRect paints a rectangle in a solid colour.
type FillRect struct {
	Rect  Rect
	Color style.RGBA
	// Overhang marks a fill whose position no layout decision accounted for: a
	// text decoration, and the background and border of an inline box.
	//
	// It exists for the overflow-page guardrail, which is about *boxes* leaving
	// the page and reads the display list to find them. Text is not checked by it
	// at all — a glyph whose ascender reaches above the page top produces a
	// DrawText, which the guard skips — so an overline over the same letters must
	// be skipped too. Without this, "line-height: 0.3" on the first line of a page
	// puts the overline a few pixels above the top edge, the guard fires at Error
	// severity, and no document is produced at all: an overhang of two pixels
	// turned into a refusal, from a rule whose whole purpose is to catch a wrong
	// scale calculation.
	//
	// An inline box's decoration is the same case and reaches it by the same
	// route. §10.6.1 gives the box a content area the height of its *font* rather
	// than of the line it sits on, and §8.4 and §8.5 keep its vertical border and
	// padding out of layout entirely — so a "line-height: 0.5" span, or one with
	// ten pixels of padding, puts ink above the first line of a page that nothing
	// in the flow ever measured. The scale-to-fit calculation cannot have
	// accounted for it, so a guard checking that calculation must not read it.
	Overhang bool
}

// DrawText draws a run of text with the origin of its baseline at At.
//
// The position is the baseline rather than the top of the line box, because that
// is what a text-drawing backend takes and because converting between them needs
// the face's metrics — which this stage has and the backend may not.
type DrawText struct {
	At   Point
	Text string
	// RTL says the run reads right to left.
	//
	// The text is in *logical* order — the order it is written and read, which
	// is what a reader copying it out of the page expects and what the string
	// here has to be for the text of the document to survive. Which way the
	// glyphs go is a separate fact, and it is one the backend has to be told
	// rather than one it can work out: a run of punctuation between two Hebrew
	// words is right-to-left because of its neighbours, and by the time the run
	// reaches a backend the neighbours are gone.
	RTL   bool
	Face  *fonts.Face
	Size  style.Unit
	Color style.RGBA
	// CharSpacing is letter-spacing: an extra advance after every character.
	//
	// It is a property of the drawing rather than of the position because layout
	// already spent it — the run's width includes it, and the run after this one
	// is placed accordingly — so a backend that ignored it would draw the glyphs
	// bunched at the left of a gap the right size.
	CharSpacing style.Unit

	// Clip is §11.1's clipping, when something cuts this run.
	//
	// It is set only when the clip really does cut the run: a run wholly inside
	// its clip carries none, and one wholly outside is not emitted at all. That
	// is not an optimisation — it is what keeps the display list of a document
	// whose text merely happens to sit inside an "overflow: hidden" box
	// identical to the list of one that does not, which is what the reftest
	// comparison needs to be able to say the two look the same.
	Clip Clip
}

// DrawImage paints a decoded image to fill a rectangle.
//
// The rectangle is the element's *content* box, which is where a replaced
// element's content goes: inside its padding, inside its border. The image is
// stretched to it rather than fitted, because the sizing rules upstream have
// already chosen a rectangle with the right shape — object-fit, which is what
// asks for anything else, is not implemented.
type DrawImage struct {
	Rect  Rect
	Image image.Image
	// Key identifies the source bytes. Two elements naming one file carry the
	// same key, which is what lets the backend embed the picture once — a
	// document with a logo in a header repeated on every row would otherwise
	// carry it as many times as it is drawn.
	Key string
	// Clip is §11.1's clipping, when something clips this picture.
	//
	// It cannot be folded into Rect the way a fill's is, because Rect is where
	// the picture is *stretched to*: narrowing it would squeeze the whole
	// image into the visible strip rather than cutting the part that is not.
	// A backend must intersect its own clipping path with this.
	Clip Clip
}

// TileImage paints a picture repeatedly across a rectangle.
//
// It is one operation for a whole tiling rather than one per tile, and that is a
// decision about safety rather than about tidiness. The number of tiles is
// (area / tile size), and a stylesheet chooses both: "background-size: 0.001px"
// with "repeat" over an A4 page is four hundred billion placements. An engine
// that emitted one operation each would allocate until it died on a document an
// attacker wrote, and no cap on the *document* bounds it, because nothing in the
// document says the number.
//
// So the tiling leaves layout as a description — where the first tile is, how
// far apart they are, and what area they may be drawn into — and the count
// appears nowhere. A backend expands it with the mechanism it has: PDF has
// tiling patterns, which are exactly this value. The count is still checked
// against maxBackgroundTiles before this is built, because whatever expands it
// is entitled not to be handed four hundred billion cells.
type TileImage struct {
	// Clip is the area painted. Nothing is drawn outside it, including the part
	// of a tile that reaches past it.
	Clip Rect
	// Tile is the first tile: its position and its size.
	Tile Rect
	// StepX and StepY are the distance to the next tile on each axis, and are
	// always greater than zero. An axis that does not repeat has a step of the
	// tile's own size and a Clip no wider than one tile, so the neighbouring
	// cells fall outside — which is what keeps a backend from needing a case for
	// "does not repeat".
	StepX, StepY style.Unit

	Image image.Image
	// Key identifies the source bytes, so a backend embeds one picture once.
	Key string
}

// Tiles is how many tiles touch the clip on each axis, which is what a consumer
// that expands them needs before it starts.
func (t TileImage) Tiles() (cols, rows int) {
	if t.StepX <= 0 || t.StepY <= 0 || t.Clip.Empty() || t.Tile.Empty() {
		return 0, 0
	}
	return tileSpan(t.Clip.X, t.Clip.Right(), t.Tile.X, t.Tile.W, t.StepX),
		tileSpan(t.Clip.Y, t.Clip.Bottom(), t.Tile.Y, t.Tile.H, t.StepY)
}

// tileSpan counts the tiles on one axis that overlap [clipLo, clipHi).
//
// A tile at tileLo + k·step covers [that, that + size), so it overlaps when
// k·step < clipHi − tileLo and k·step > clipLo − tileLo − size. The two bounds
// are what the floor and the ceiling below are: the index of the first tile that
// reaches into the clip, and of the last one that starts before it ends.
func tileSpan(clipLo, clipHi, tileLo, size, step style.Unit) int {
	lo := math.Floor(clipLo.Sub(tileLo).Sub(size).Px()/step.Px()) + 1
	hi := math.Ceil(clipHi.Sub(tileLo).Px()/step.Px()) - 1
	if hi < lo {
		return 0
	}
	n := hi - lo + 1
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(n)
}

func (FillRect) isOp()  {}
func (DrawText) isOp()  {}
func (DrawImage) isOp() {}
func (TileImage) isOp() {}

// Paint turns a fragment tree into a display list, in painting order.
//
// The order is CSS 2.1 Appendix E. It used to be tree order, with a note saying
// that this would stop being true the moment positioning arrived, and it has:
// tree order is painting order only while nothing is out of flow and nothing
// asks to be painted somewhere else in the stack. Both are now possible, so what
// is here is the real algorithm.
//
// # What a stacking context is, and what it is not
//
// §9.9 and Appendix E divide the tree into *stacking contexts*, each of which is
// painted as an atomic unit in the eight steps of §E.2. The root element makes
// one. So does
// a positioned box with a z-index that is not auto — and only that, which is the
// distinction the whole scheme turns on and the one that reads as a technicality
// until it bites. A positioned box with "z-index: auto" is painted as a unit,
// at the same level as one with "z-index: 0", but it does *not* make a stacking
// context: its own positioned descendants are hoisted out and sorted against its
// siblings rather than against each other. So a descendant with "z-index: -1"
// paints behind an ancestor whose z-index is auto and in front of one whose
// z-index is 0, from the same markup. Collapsing auto onto 0 gives a page where
// that descendant is simply not visible, which looks like a missing box rather
// than like a stacking bug.
//
// # What each step of Appendix E is for
//
// The eight steps exist to make three guarantees that tree order alone does not.
// Backgrounds of every ordinary block in a subtree are painted before any of
// the text in it, so a later sibling's background cannot cover an earlier one's
// words. Floats are a layer of their own between the two, so a floated image
// sits over the block backgrounds it overlaps and under the text that runs
// around it. And everything positioned is painted after everything that is not,
// which is what makes "position: relative" with no offsets at all a way to lift
// a box above its neighbours — a fact that looks like an accident of the
// specification and is relied on constantly.
//
// # What is not done
//
// Opacity and transforms also create stacking contexts and are not implemented,
// so neither appears here, and step 2 is a background image, which nothing draws
// yet. Every other step of §E.2 is present, reduced to the primitives this engine
// emits.
func Paint(root *Fragment) []Op {
	if root == nil {
		return nil
	}
	p := &painter{colors: map[string]style.RGBA{}}
	p.canvasBackground(root)
	p.stackingContext(root)
	return p.ops
}

// canvasBackground paints the page's own background, before anything else.
//
// It is CSS 2.1 §14.2 and css-backgrounds-3 §2.11: the root element's background
// becomes the canvas's and covers the whole canvas, and when the root declares
// none it is taken from <body> instead. The consequence is the one authors rely
// on without knowing they do — "body { background: silver }" makes the *page*
// silver rather than a rectangle the height of the text — and it is the reason
// the element the background came from is not painted again here.
//
// The images are positioned against the root element's box even when the values
// came from <body>, which is what the specification says in as many words and is
// decided in layout; what is left here is the order.
func (p *painter) canvasBackground(root *Fragment) {
	if root == nil || root.canvas.Empty() {
		return
	}
	if b := root.canvasColor; b != nil {
		if c, ok := p.color(b, "background-color"); ok && c.A > 0 {
			p.ops = append(p.ops, FillRect{Rect: root.canvas, Color: c})
		}
	}
	p.backgroundImages(root.canvasLayers)
}

// backgroundImages emits one operation per resolved layer.
//
// The layers arrive in painting order, so this is a loop and not a decision. All
// the arithmetic — the tile, the step, the clip — happened in layout, where a
// finding could be raised about it.
func (p *painter) backgroundImages(layers []bgPaint) {
	for _, l := range layers {
		if l.Image == nil || l.Clip.Empty() || l.Tile.Empty() {
			continue
		}
		p.ops = append(p.ops, TileImage{
			Clip: l.Clip, Tile: l.Tile,
			StepX: l.StepX, StepY: l.StepY,
			Image: l.Image, Key: l.Key,
		})
	}
}

type painter struct {
	ops []Op
	// colors memoizes parsing a computed colour, which is asked for once per
	// box per property and is almost always one of a handful of values.
	colors map[string]style.RGBA
}

// stackLevel is one positioned box waiting to be painted, with what decides
// where in the order it goes.
type stackLevel struct {
	frag *Fragment
	// z is the z-index, with auto counted as zero. §E.2 step 7 paints
	// "z-index: auto" and "z-index: 0" together in tree order, so for the
	// purpose of *ordering* the two really are the same number — they differ
	// only in whether the box becomes a context of its own, which is asked
	// separately.
	z int
	// order is the box's index in document order, which is what breaks a tie
	// between equal z-indexes.
	order int
}

// layers is what one stacking context's subtree contributes, split into
// Appendix E's steps.
type layers struct {
	// blocks are the in-flow, non-positioned, non-floating descendants whose
	// backgrounds and borders are §E.2 step 4.
	blocks []*Fragment
	// floats are the non-positioned floating descendants of §E.2 step 5, each painted
	// whole rather than as a background here and text later — §E.2 says a float
	// is painted as though it created a stacking context, so its own text goes
	// with its own background rather than joining the parent's text layer.
	floats []*Fragment
	// content are the fragments with inline content — line boxes and list
	// markers — which is §E.2 step 6.
	content []*Fragment
	// tables are the tables using §17.6.2's collapsing border model, whose grid
	// lines are drawn after every background in the table and not with the
	// table's own. §17.5.1 paints a table in six layers — the table, the column
	// groups, the columns, the row groups, the rows and the cells — and a border
	// centred on a grid line runs under the edge of a row and of two cells, so a
	// background painted after it would rub it out.
	tables []*Fragment
	// positioned are §E.2 steps 3, 7 and 8, which are one list sorted by z rather
	// than three: the steps differ only in the sign of the number.
	positioned []stackLevel
}

// stackingContext paints a fragment and everything under it, in the order of
// Appendix E §E.2.
func (p *painter) stackingContext(f *Fragment) {
	if f.Box == nil {
		return
	}
	lv := &layers{}
	p.gather(f, lv, true, true)

	// Step 1: the context root's own background and border.
	p.decorations(f)

	sortLevels(lv.positioned)
	at := 0

	// Step 3: the stacking contexts with a negative z-index, most negative
	// first. They go behind the in-flow content of this context but in front of
	// its root's own background, which is the one thing a negative z-index
	// cannot get behind — and is why "z-index: -1" on a child does not hide it
	// under its own parent's background.
	for at < len(lv.positioned) && lv.positioned[at].z < 0 {
		p.stackLevel(lv.positioned[at])
		at++
	}

	// Steps 4, 5 and 6: block backgrounds, then floats, then inline content.
	for _, g := range lv.blocks {
		p.decorations(g)
	}
	for _, g := range lv.tables {
		p.paintCollapsed(g)
	}
	for _, g := range lv.floats {
		p.unit(g)
	}
	for _, g := range lv.content {
		p.content(g)
	}

	// Steps 7 and 8: everything positioned, in z order. The two steps are one
	// loop because the sort has already put the zeroes before the positives and
	// there is nothing between them.
	for ; at < len(lv.positioned); at++ {
		p.stackLevel(lv.positioned[at])
	}
}

// stackLevel paints one positioned box, as a context of its own or as a unit.
//
// The choice is §9.9's: a z-index that is not auto makes a stacking context, and
// everything inside it — including its positioned descendants, however extreme
// their z-index — is sealed within it. A z-index of auto does not, so the box is
// painted as a unit and its positioned descendants have already been hoisted
// into the enclosing context by gather.
func (p *painter) stackLevel(s stackLevel) {
	if s.frag.Box.ZAuto {
		p.unit(s.frag)
		return
	}
	p.stackingContext(s.frag)
}

// unit paints a fragment and its non-positioned content as one indivisible
// group, in the same block-float-text layering a stacking context uses for its
// own content.
//
// It is what a float is painted by (§E.2 step 5) and what a positioned box with
// "z-index: auto" is painted by (step 7): both are atomic with respect to their
// surroundings and neither seals its positioned descendants in.
func (p *painter) unit(f *Fragment) {
	lv := &layers{}
	p.gather(f, lv, true, false)

	p.decorations(f)
	for _, g := range lv.blocks {
		p.decorations(g)
	}
	for _, g := range lv.tables {
		p.paintCollapsed(g)
	}
	for _, g := range lv.floats {
		p.unit(g)
	}
	for _, g := range lv.content {
		p.content(g)
	}
}

// gather walks a subtree and sorts what it finds into Appendix E's layers.
//
// root says whether f itself is the thing being painted, whose own background is
// step 1 rather than step 4. collect says whether positioned descendants belong
// to this walk's layers: they do when it is collecting for a stacking context,
// and they do not when it is collecting for a unit, because a unit's positioned
// descendants were hoisted into the enclosing context before it was painted.
func (p *painter) gather(f *Fragment, lv *layers, root, collect bool) {
	if f.Box == nil {
		return
	}
	if !root {
		lv.blocks = append(lv.blocks, f)
	}
	if len(f.collapsed) > 0 {
		// Collected even when f is the root of this walk, because its grid lines
		// go after the backgrounds of everything inside it rather than with its
		// own.
		lv.tables = append(lv.tables, f)
	}
	if len(f.Lines) > 0 || f.Marker != nil || f.Box.Replaced != nil {
		lv.content = append(lv.content, f)
	}
	for _, c := range f.Children {
		if c.Box == nil {
			continue
		}
		if c.Box.Position.positioned() {
			if !collect {
				// Already hoisted; painting it here as well would draw it twice.
				continue
			}
			lv.positioned = append(lv.positioned, stackLevel{
				frag: c, z: levelOf(c), order: c.Box.Order,
			})
			if c.Box.ZAuto {
				// Not a stacking context, so the positioned boxes inside it
				// belong to this one. Without this hoist a "z-index: 5" inside a
				// plain "position: relative" wrapper would be trapped under
				// everything the wrapper is under, which is the bug that makes
				// authors write z-indexes in the thousands.
				p.hoist(c, lv)
			}
			continue
		}
		if c.Box.Float != FloatNone {
			lv.floats = append(lv.floats, c)
			if collect {
				// A float is atomic for its own content and transparent for its
				// positioned descendants: §E.2 step 5 says so in as many words,
				// and it is what stops a float trapping a positioned box behind
				// the text of the paragraph beside it.
				p.hoist(c, lv)
			}
			continue
		}
		p.gather(c, lv, false, collect)
	}
}

// hoist finds the positioned boxes inside a subtree that is painted as a unit,
// so that they take their place in the enclosing stacking context instead.
func (p *painter) hoist(f *Fragment, lv *layers) {
	for _, c := range f.Children {
		if c.Box == nil {
			continue
		}
		if c.Box.Position.positioned() {
			lv.positioned = append(lv.positioned, stackLevel{
				frag: c, z: levelOf(c), order: c.Box.Order,
			})
			if c.Box.ZAuto {
				p.hoist(c, lv)
			}
			continue
		}
		p.hoist(c, lv)
	}
}

// levelOf is a box's stacking level: its z-index, with auto counted as zero for
// ordering. See stackLevel.z.
func levelOf(f *Fragment) int {
	if f.Box.ZAuto {
		return 0
	}
	return f.Box.ZIndex
}

// sortLevels orders the positioned boxes by z-index and then by tree order.
//
// Ascending, so the most negative is painted first and the largest last, which
// is the whole of what a z-index means. The tie-break is tree order and not
// something arbitrary: two boxes at the same level are stacked back to front in
// the order they were written, which is the rule that makes overlapping cards in
// a list read correctly without any of them naming a number.
func sortLevels(levels []stackLevel) {
	sort.SliceStable(levels, func(i, j int) bool {
		if levels[i].z != levels[j].z {
			return levels[i].z < levels[j].z
		}
		return levels[i].order < levels[j].order
	})
}

// clipping applies a clip to everything a painting step produced.
//
// It is a wrapper around the step rather than an argument threaded into it, for
// the reason inlineDecorations gives about its own flag: the decompositions
// underneath — a dashed border is a dozen fills, a 3-D one is two tones, a
// tiling is an area and a step — would each have to learn about clipping, and a
// second copy of any of them is what those functions exist to prevent. One
// place applies the clip, to whatever the shared code produced, and a painting
// step that forgot to clip is not expressible: the three steps that emit
// anything all go through here.
func (p *painter) clipping(c Clip, paint func()) {
	if c.blocks() {
		// Nothing can be painted through it, so nothing is built and then
		// thrown away. This is the case "clip: rect(0, 0, 0, 0)" and a
		// zero-sized "overflow: hidden" box are, and it is common enough in a
		// hostile document to be worth not doing the work for.
		return
	}
	at := len(p.ops)
	paint()
	if !c.Active {
		return
	}
	p.ops = clipOps(p.ops, at, c)
}

// clipOps narrows every operation from index at onwards, dropping the ones that
// no longer mark anything.
func clipOps(ops []Op, at int, c Clip) []Op {
	kept := ops[:at]
	for _, op := range ops[at:] {
		switch v := op.(type) {
		case FillRect:
			// Exact: a rectangle cut by a rectangle is a rectangle. No clip
			// travels with it, which is what keeps the overflow-page guardrail
			// and the reftest comparison from needing to know about clipping at
			// all.
			v.Rect = v.Rect.Intersect(c.Rect)
			if v.Rect.Empty() {
				continue
			}
			kept = append(kept, v)

		case TileImage:
			// The area a tiling may paint is already one of its fields, and
			// this is the same statement narrowed. The tile positions are not
			// touched, so a tiling cut in half still lines up with the one
			// beside it.
			v.Clip = v.Clip.Intersect(c.Rect)
			if v.Clip.Empty() {
				continue
			}
			kept = append(kept, v)

		case DrawImage:
			if c.Rect.Intersect(v.Rect).Empty() {
				continue
			}
			v.Clip = v.Clip.meet(c)
			if c.Rect.Contains(v.Rect) {
				// Wholly visible, so the clip is not worth carrying: it would
				// make an unclipped picture and a clipped one compare as
				// different marks when they put the same ink on the page.
				v.Clip = Clip{}
			}
			kept = append(kept, v)

		case DrawText:
			ink := textInk(v)
			if ink.Empty() {
				// Nothing measurable to place: a run with no face, which the
				// engine does not produce and a caller building a display list
				// by hand might. Keeping it is the safe direction — the other
				// one drops every such run through any clip at all, since an
				// empty rectangle meets nothing.
				kept = append(kept, v)
				continue
			}
			if c.hides(ink) {
				// Every glyph is outside the clip. This is the case the whole
				// feature exists for and the only one that can be settled
				// exactly without cutting a letter in half.
				continue
			}
			if !c.admits(ink) {
				v.Clip = v.Clip.meet(c)
			}
			kept = append(kept, v)

		default:
			kept = append(kept, op)
		}
	}
	return kept
}

// textInk is the area a run of text may put ink in.
//
// The face's declared ascent and descent rather than a measured outline: this
// engine has no glyph rasteriser, and the same two numbers are what inline
// layout gave the run's line box, so a run is bounded here by exactly the
// rectangle layout reserved for it. A face that states neither falls back to a
// generous em above the baseline and a third of one below, which errs towards
// keeping a run that is only just visible rather than dropping one that is.
func textInk(v DrawText) Rect {
	above, below := v.Size, v.Size.Mul(0.3)
	if v.Face != nil {
		if upem := float64(v.Face.UnitsPerEm()); upem > 0 {
			d := v.Face.Descriptor()
			above = v.Size.Mul(float64(d.Ascent) / upem)
			below = v.Size.Mul(-float64(d.Descent) / upem)
		}
	}
	var width style.Unit
	if v.Face != nil {
		w, _ := style.FromPx(v.Face.Measure(v.Text, v.Size.Px()))
		width = w.Add(v.CharSpacing.Mul(float64(len([]rune(v.Text)))))
	}
	return Rect{
		X: v.At.X, Y: v.At.Y.Sub(above),
		W: width, H: above.Add(below),
	}
}

// decorations paints a box's own background and border, which is what §E.2 steps
// 1 and 4 both consist of.
// The clip is the box's own — §11.1.1 clips a box's *contents* and not the box,
// so an "overflow: hidden" element with a wide border still draws all of it.
// What can cut a box's own background is §11.1.2's "clip", which is in clipSelf
// and not in clipContent.
func (p *painter) decorations(f *Fragment) {
	if f.Box == nil {
		return
	}
	p.clipping(f.clipSelf, func() { p.paintDecorations(f) })
}

func (p *painter) paintDecorations(f *Fragment) {
	if isHidden(f.Box) {
		// §11.2: the box is laid out and not drawn. It has already taken its
		// space — every position on this page was computed with it in — so this
		// is the only place the property has any effect, and it is asked per box
		// rather than per subtree because a descendant may set "visibility:
		// visible" and reappear.
		return
	}
	if f.bgSuppressed {
		// This box's background became the canvas's, and was painted over the
		// whole page before anything else. Painting it again over its own box
		// would double a translucent colour and would put a "no-repeat" image
		// down twice in two places.
		p.borders(f)
		return
	}
	// The background paints over the *border* box by default, under the border
	// rather than up to it — which is what background-clip's initial value of
	// border-box means, and is why a dashed border shows the background through
	// its gaps rather than the page. It stops at the border box and never
	// reaches the margin, which is the space that is meant to show through.
	//
	// The colour goes first and the images over it, in the order §E.2 steps 1 and
	// 4 give: "the background color of the element, then the background image".
	if bg, ok := p.color(f.Box, "background-color"); ok && bg.A > 0 {
		if rect := f.bgColorRect; !rect.Empty() {
			p.ops = append(p.ops, FillRect{Rect: rect, Color: bg})
		}
	}
	p.backgroundImages(f.background)
	p.borders(f)
}

// content paints the inline-level marks a fragment carries: its list marker and
// its line boxes, which are §E.2 step 6.
//
// The marker is here rather than with the decorations because it is text. A
// marker painted with the backgrounds would be covered by the background of any
// box painted after it in step 4, which for an "outside" marker — one that sits
// in the margin, outside its own list item — is a real overlap rather than a
// theoretical one.
// The clip is the box's *content* clip, so its own "overflow" cuts the text and
// pictures inside it to its padding box. That is §11.1.1 in one line, and it is
// the half of clipping every author uses.
func (p *painter) content(f *Fragment) {
	p.clipping(f.clipContent, func() { p.paintContent(f) })
}

func (p *painter) paintContent(f *Fragment) {
	// A replaced element's content is painted here rather than with the
	// backgrounds, and for the same reason the marker is: it is content. §E.2
	// paints every block background in a stacking context before any of its
	// content, so an image drawn with the backgrounds would be covered by the
	// background of any box painted after it — which for an image overlapping
	// its next sibling is a real overlap rather than a theoretical one.
	hidden := isHidden(f.Box)
	if r := f.Box.Replaced; r != nil && r.Image != nil && !hidden {
		if rect := f.ContentRect(); !rect.Empty() {
			p.ops = append(p.ops, DrawImage{Rect: rect, Image: r.Image, Key: r.Key})
		}
	}
	if m := f.Marker; m != nil && m.Face != nil && !hidden {
		p.ops = append(p.ops, DrawText{
			At: Point{
				X: f.BorderRect.X.Add(m.At.X),
				Y: f.BorderRect.Y.Add(m.At.Y),
			},
			Text: m.Text, Face: m.Face, Size: m.Size, Color: m.Color,
		})
	}
	p.lines(f)
}

// inlineDecorations paints one fragment of an inline box: the same background
// and border every other box gets, marked as an overhang.
//
// The marking is done over the operations rather than passed into the painting,
// because the decomposition it would have to be threaded through is border.go's
// — a dashed border is a dozen fills and a 3-D one is two tones — and a second
// copy of that decomposition is exactly what border.go exists to prevent. What
// is here is one flag applied to whatever the shared code produced.
func (p *painter) inlineDecorations(f *Fragment) {
	at := len(p.ops)
	p.decorations(f)
	for i := at; i < len(p.ops); i++ {
		r, ok := p.ops[i].(FillRect)
		if !ok {
			continue
		}
		r.Overhang = true
		p.ops[i] = r
	}
}

// borders paints the four edges as filled bands.
//
// Four rectangles rather than a stroked outline, because a stroke is centred on
// its path and a CSS border is not: it lies entirely inside the border box. A
// stroked border would be half a width out on every side, which is invisible at
// one pixel and obvious at ten.
//
// The corners are mitred by giving the top and bottom bands the full width and
// the sides only what is left, which is right for a solid border of one colour
// and is where a proper implementation of border-style would start.
func (p *painter) borders(f *Fragment) {
	if f.inCollapsedGrid {
		// §17.6.2: this box's declared border is one of the candidates the grid
		// lines were resolved from, and it was either drawn as part of one of
		// them or beaten by another box's. Drawing it here as well would put a
		// losing candidate on the page after the winner — and would draw it at
		// its full width over a line that is meant to be shared, which is the
		// separated model showing through.
		return
	}
	r := f.BorderRect
	e := f.Border

	// The sides take what the top and bottom leave, which mitres the corners
	// well enough for a border of one colour and is where a per-corner
	// implementation would start.
	inner := Rect{
		Y: r.Y.Add(e.Top),
		H: r.H.Sub(e.Top).Sub(e.Bottom),
	}
	if inner.H < 0 {
		inner.H = 0
	}

	edges := [4]struct {
		width style.Unit
		band  Rect
		name  string
		side  side
	}{
		{e.Top, Rect{r.X, r.Y, r.W, e.Top}, "top", sideTop},
		{e.Right, Rect{r.Right().Sub(e.Right), inner.Y, e.Right, inner.H}, "right", sideRight},
		{e.Bottom, Rect{r.X, r.Bottom().Sub(e.Bottom), r.W, e.Bottom}, "bottom", sideBottom},
		{e.Left, Rect{r.X, inner.Y, e.Left, inner.H}, "left", sideLeft},
	}
	for _, edge := range edges {
		if edge.width <= 0 {
			continue
		}
		colour, ok := p.color(f.Box, "border-"+edge.name+"-color")
		if !ok || colour.A == 0 {
			continue
		}
		kind := parseBorderStyle(f.Box.Style["border-"+edge.name+"-style"])
		p.paintEdge(edge.band, kind, colour, edge.side, edge.width)
	}
}

// lines paints the text of a block container.
func (p *painter) lines(f *Fragment) {
	if len(f.Lines) == 0 {
		return
	}
	content := f.ContentRect()
	for _, line := range f.Lines {
		baseline := content.Y.Add(line.Rect.Y).Add(line.Baseline)
		// §E.2's inline layer, in the order it gives: for each line box, the
		// background and border of the inline boxes on it, then the text. They
		// are in tree order among themselves, so an inner box's background is
		// painted over the box it is inside.
		//
		// One deviation, which is the whole of what is not exact here: the
		// specification interleaves each inline box's own text with its
		// decoration, so text belonging to a box painted *earlier* in tree order
		// goes under a later box's background. Two inline boxes on a line only
		// overlap where a negative margin makes them, and painting all the
		// decorations of a line first is what keeps this a loop rather than a
		// second traversal of the tree.
		for _, box := range line.Boxes {
			p.inlineDecorations(box)
		}
		for _, run := range line.Runs {
			// Spaces are drawn, not skipped, and the reason is text extraction
			// rather than ink. A space glyph marks no paper, so skipping it
			// looks like a free optimisation — but then the words either side
			// are separate text operations with only a position jump between
			// them, and a reader copying the text gets them run together. That
			// was found by reading back a rendered page: "A heading" came out
			// as "Aheading".
			//
			// A preserved tab is the one character that cannot be drawn as
			// itself. No face has a glyph for U+0009, so setting it emits
			// .notdef — a box where white space should be, which is the tofu
			// this engine has a whole guardrail about. Its advance is already
			// spent: line breaking resolved it against the tab stops and gave
			// the next run its position, so what is left to draw is white
			// space, and a space is the character that draws it.
			if isHidden(run.Box) {
				// A run belongs to the inline box it came from, which may be
				// visible inside a hidden block or hidden inside a visible one.
				// Asking per run rather than per fragment is what makes
				// "visibility: visible" on a <span> inside a hidden paragraph
				// show that span and nothing else.
				continue
			}
			colour, ok := p.color(run.Box, "color")
			if !ok {
				colour = style.RGBA{A: 1}
			}
			at := Point{
				X: content.X.Add(line.Rect.X).Add(run.X).Add(run.Offset.X),
				// The run's own baseline: the line's, displaced by §10.8.1's
				// vertical-align, and then by §9.4.3's relative positioning.
				// The two are added rather than chosen between, and in that
				// order — a raised <sup> that is also relatively positioned
				// moves twice.
				Y: baseline.Add(run.Shift).Add(run.Offset.Y),
			}
			// The two lines that sit clear of the letters are drawn first, so the
			// text is over them where they touch; the line-through is drawn after,
			// because it goes across the letters rather than under them. That is
			// the order every renderer uses, and it only matters where a
			// decoration's colour differs from the text's — which is precisely the
			// case §16.3.1 exists to describe.
			p.decorate(run, at, false)
			p.ops = append(p.ops, DrawText{
				At:          at,
				Text:        drawableText(run.Text),
				RTL:         run.RTL,
				Face:        run.Face,
				Size:        run.Size,
				Color:       colour,
				CharSpacing: run.LetterSpacing,
			})
			p.decorate(run, at, true)
		}
	}
}

// decorate paints the lines ruled across one run.
//
// over selects the pass: the line-through, which goes on top of the letters, or
// the underline and overline, which go under them.
//
// The colour is the *declaring* box's rather than the run's, which is the whole
// subtlety of §16.3.1 and is why a decoration carries the box that produced it.
// A decoration with no colour of its own resolves "currentcolor" against that
// box too, so "p { text-decoration: underline; color: black } em { color: red }"
// rules a black line under red words.
//
// So is the *height*, and for the same reason. §16.3.1 draws a decoration across
// the whole of the box that declared it "without paying any attention to" the
// descendants it crosses, so the band is placed at the declaring box's baseline
// rather than at the run's: three spans at three vertical-aligns under one
// overlining div are ruled by one straight line. at.Y is the run's own baseline
// and carries the run's own shift, which is undone here and the declaring box's
// put in its place.
func (p *painter) decorate(run TextRun, at Point, over bool) {
	if len(run.Decorations) == 0 || run.Width <= 0 {
		return
	}
	metrics := decorationMetricsFor(run.Face, run.Size)
	lineY := at.Y.Sub(run.Shift)
	for _, d := range run.Decorations {
		if (d.kind == decorationLineThrough) != over {
			continue
		}
		colour, ok := p.color(d.by, "text-decoration-color")
		if !ok || colour.A == 0 {
			continue
		}
		band := decorationBand(d.kind, at.X, run.Width, lineY.Add(d.shift), metrics)
		if band.Empty() {
			continue
		}
		p.ops = append(p.ops, FillRect{Rect: band, Color: colour, Overhang: true})
	}
}

// drawableText replaces the characters a face cannot set with the white space
// they stand for.
//
// It is only the tab, and only because a tab's whole meaning is its position:
// the advance was decided against the tab stops before this, so nothing about
// the page depends on the character reaching the face — and everything about it
// depends on .notdef not being drawn where an author wrote an indent.
func drawableText(s string) string {
	if strings.IndexByte(s, '\t') < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\t", " ")
}

// color resolves a computed colour property.
//
// "currentcolor" is the one value that is not a colour but a reference to one —
// it means whatever "color" resolved to, which is what makes a border take the
// text's colour without the author repeating it.
func (p *painter) color(b *Box, property string) (style.RGBA, bool) {
	if b == nil {
		return style.RGBA{}, false
	}
	raw := strings.TrimSpace(b.Style[property])
	if raw == "" {
		return style.RGBA{}, false
	}
	if strings.EqualFold(raw, "currentcolor") {
		if property == "color" {
			// A "color: currentcolor" is circular; the initial value breaks it,
			// which is what the specification says to do.
			return style.RGBA{A: 1}, true
		}
		return p.color(b, "color")
	}
	if got, ok := p.colors[raw]; ok {
		return got, got.A >= 0
	}
	c, ok := parseColorValue(raw)
	if !ok {
		return style.RGBA{}, false
	}
	p.colors[raw] = c
	return c, true
}

// parseColorValue reads a computed colour that is not "currentcolor".
//
// It is separate from the painter's memoized lookup because layout asks the same
// question once, of the root and of <body>, when it decides whether either
// declares a background to give the canvas.
func parseColorValue(raw string) (style.RGBA, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "currentcolor") {
		return style.RGBA{}, false
	}
	vals, _ := css.ParseComponentValues(raw)
	return style.ParseColor(vals)
}
