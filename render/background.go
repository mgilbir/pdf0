package render

import (
	"fmt"
	"image"
	"math"
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/style"
)

// Background images: reading the seven properties that place one, and turning
// them into the rectangles a backend can draw.
//
// # Why this is a stage of its own rather than a branch in the painter
//
// A background image is not one rectangle. It is a *tiling* — an origin, a tile
// size, a step on each axis and a clip — and every one of those four comes from
// a different property interacting with the other three. background-size decides
// the tile, background-position decides where the first one goes, and
// background-repeat can then change both of them: "round" rescales the tile so a
// whole number fits, and "space" leaves the tile alone and widens the step. So
// the arithmetic has to happen in one place with all seven values in hand, and
// what comes out of it is a small value the painter can emit without deciding
// anything.
//
// It also has to happen where a *finding* can be raised, which is layout rather
// than paint: the painter has no recorder, and the two things worth telling an
// author about a background — an image that did not load and a tiling this
// engine refused to emit — are both discovered here.
//
// # The amplification, which is what makes this different from an <img>
//
// An <img> draws one picture once. A background tiles, and the number of tiles
// is (area / tile size) — a ratio a stylesheet controls both ends of. A one-pixel
// image repeated over an A4 page is four hundred thousand placements;
// "background-size: 0.001px" with the same repeat is four hundred *billion*.
// Neither number appears anywhere in the document, so nothing upstream bounds it.
//
// Two things hold it. The tiling leaves here as a single value with a step in it
// rather than as one operation per tile, so this engine's own memory does not
// depend on the count at all. And the count is checked against a cap anyway,
// because what leaves here is drawn by *something else* — a PDF reader expanding
// a tiling pattern, a rasteriser walking the display list — and handing it four
// hundred billion cells is an amplification whoever we hand it to has to survive.
// See maxBackgroundTiles.

// bgBox names one of the three boxes background-origin and background-clip
// choose between.
type bgBox uint8

const (
	bgBorderBox bgBox = iota
	bgPaddingBox
	bgContentBox
)

// bgRepeat is one axis of background-repeat.
type bgRepeat uint8

const (
	// bgRepeatTile is "repeat": tiles butted against each other, for ever.
	bgRepeatTile bgRepeat = iota
	// bgRepeatNone is "no-repeat": one tile, at the position.
	bgRepeatNone
	// bgRepeatSpace fits whole tiles and spreads the remainder between them,
	// with the first and last against the edges of the positioning area.
	bgRepeatSpace
	// bgRepeatRound rescales the tile so that a whole number of them fits.
	bgRepeatRound
)

// bgSizeKind is which of background-size's three forms was written.
type bgSizeKind uint8

const (
	bgSizeExplicit bgSizeKind = iota
	bgSizeCover
	bgSizeContain
)

// bgPos is one axis of background-position.
//
// It is an offset and an edge to measure it from, which is exactly the shape of
// the four-value syntax — "right 10px" is this with fromEnd set — and it makes
// the two- and one-value forms fall out as the case where the edge is the start
// one. Keeping the edge rather than folding it into a signed offset is what lets
// the percentage rule below stay a single expression.
type bgPos struct {
	// offset is the distance from the edge, as a length or a percentage.
	offset style.Length
	// fromEnd measures it from the right or bottom edge instead of the left or
	// top one.
	fromEnd bool
}

// place resolves one axis of the position.
//
// This is the rule the whole property turns on and the one implementations get
// wrong: a *percentage* positions the image's own corresponding point against
// the box's, so 50% puts the middle of the image at the middle of the area
// rather than moving it half the area's width. That is why the percentage is of
// (area - image) rather than of the area — at 100% the difference is the whole
// image, which is the difference between the picture sitting against the right
// edge and it sitting entirely outside the box.
func (p bgPos) place(area, img style.Unit) style.Unit {
	free := area.Sub(img)
	var at style.Unit
	switch p.offset.Kind {
	case style.LengthPercent:
		at = free.Mul(p.offset.Percent / 100)
	case style.LengthCalc:
		at = free.Mul(p.offset.Percent / 100).Add(p.offset.Value)
	default:
		at = p.offset.Value
	}
	if p.fromEnd {
		return free.Sub(at)
	}
	return at
}

// backgroundLayer is one layer of the background, with everything read and
// nothing yet resolved against a geometry.
type backgroundLayer struct {
	// image is the loaded picture, nil when the layer names none or when the
	// one it named could not be loaded. A layer with no image paints nothing,
	// which is what makes a failed load a hole rather than an error.
	image *ReplacedContent

	repeatX, repeatY bgRepeat
	posX, posY       bgPos

	sizeKind     bgSizeKind
	sizeW, sizeH style.Length

	origin, clip bgBox
	// fixed says the positioning area is the page rather than the box, which is
	// what "background-attachment: fixed" means once there is nothing to scroll.
	fixed bool
}

// bgPaint is one layer resolved against a geometry: everything a backend needs
// and nothing it has to decide.
type bgPaint struct {
	// Clip is the area the tiling is painted into, already narrowed to the band
	// a non-repeating axis covers. Nothing outside it is drawn.
	Clip Rect
	// Tile is the first tile: where it sits and how large it is.
	Tile Rect
	// StepX and StepY are the distance between one tile and the next. They are
	// never zero — an axis that does not repeat has a step of the tile's own
	// size, and is confined to one tile by Clip instead. That keeps a backend
	// from having to represent "no step", which is the case a division by it
	// would find.
	StepX, StepY style.Unit

	Image image.Image
	// Key identifies the source bytes so a backend embeds one picture once.
	Key string
}

// maxBackgroundTiles bounds the tiles one layer may ask for.
//
// The count is what a stylesheet controls both ends of: the painting area comes
// from the box and the tile from background-size, and "background-size: 0.001px"
// over an A4 page asks for four hundred billion of them. This engine emits one
// value however many there are, so the cap is not protecting *this* process — it
// is protecting whatever draws what this produces, which is a PDF reader
// expanding a tiling pattern cell by cell.
//
// A million is past anything a document means. A one-pixel image repeated over
// an A4 page is four hundred thousand tiles, which is already pathological and
// is allowed; ten times that is not a design, it is an attack or a mistake, and
// both are worth a finding.
//
// It is a variable so that a test can lower it far enough to watch it fire
// without laying out a page that takes a minute to refuse.
var maxBackgroundTiles float64 = 1 << 20

// maxBackgroundLayers bounds how many layers of one box's background are
// painted.
//
// The style package refuses a shorthand with more than this many layers, so a
// value that reaches here past the bound was written as longhands — which is a
// different declaration and needs its own check. Each layer is a tiling of its
// own, so the two bounds multiply.
//
// A variable so that a test can lower it.
var maxBackgroundLayers = 1024

// resolveBackgrounds computes every fragment's background painting, after layout
// has settled the geometry and before anything is painted.
//
// It is a pass rather than a step inside layout because it needs the *absolute*
// rectangles — a fixed-attachment layer is positioned against the page, and a
// percentage position is of a box whose width is not known until its own
// children are placed — and because doing it here keeps the layout walk free of
// a property none of its arithmetic depends on.
func (l *layouter) resolveBackgrounds(root *Fragment, canvas Rect) {
	if root == nil {
		return
	}
	root.canvas = canvas

	// §2.11.1: the root element's background is the canvas's, painted over the
	// whole canvas rather than over the root's own box — and taken from <body>
	// when the root itself declares none. Both are decided before the walk, so
	// that the walk can simply skip whichever box gave its background away.
	source := l.canvasBackgroundSource(root)
	if source != nil {
		source.bgSuppressed = true
		// "Any images are sized and positioned as if for the root element
		// itself", so the positioning area is the root's box whichever element
		// the properties were taken from. Only the painting area moves.
		root.canvasLayers = l.paintsFor(source.Box, root, canvas, canvas)
		root.canvasColor = source.Box
	}

	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if f.Box != nil && !f.bgSuppressed {
			f.background = l.paintsFor(f.Box, f, canvas, Rect{})
			f.bgColorRect = l.colorRect(f)
		}
		// An inline box's own fragments, one per line it is broken across. They
		// are not children — see LineFragment.Boxes — and each is positioned
		// against its own rectangle, so a background image on a <span> that wraps
		// starts afresh on each line rather than continuing across the break.
		// That is what the slice model asks for and what every browser does.
		for i := range f.Lines {
			for _, ib := range f.Lines[i].Boxes {
				ib.background = l.paintsFor(ib.Box, ib, canvas, Rect{})
				ib.bgColorRect = l.colorRect(ib)
			}
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
}

// canvasBackgroundSource finds the fragment whose background becomes the
// canvas's, or nil when nothing does.
//
// The rule reads as a special case and is not one: a page has to have a
// background, the root element is the only box guaranteed to exist, and an
// author who writes "body { background: silver }" means the page rather than a
// rectangle the height of the text. So the root's background is propagated, and
// when the root declares none the first <body> child's is propagated in its
// place — and the element it came from then paints nothing of its own, which is
// what stops the same colour being laid down twice at two different sizes.
func (l *layouter) canvasBackgroundSource(root *Fragment) *Fragment {
	// §17.4's wrapper stands where the root element does and is anonymous, so a
	// document whose root declared "display: table" arrives here as a box with no
	// element at all — and the rule below, which is about the root *element*,
	// answered "not an HTML document" and propagated nothing. The page then had
	// no background and <body> painted its own colour at its own size, which is
	// the one thing §2.11.2 exists to prevent.
	if root.Box != nil && root.Box.TableWrapper {
		for _, c := range root.Children {
			if c.Box != nil && c.Box.Element != nil {
				root = c
				break
			}
		}
	}
	if root.Box == nil || root.Box.Element == nil {
		return nil
	}
	if !strings.EqualFold(root.Box.Element.Name, "html") {
		// Not an HTML document's root. §2.11.2's propagation from <body> is
		// specific to HTML, and propagating the root's own background is not:
		// but without an <html> element this engine is being handed a fragment
		// tree by a test rather than a document, and inventing a canvas for it
		// would paint over the very thing the test is asserting.
		return nil
	}
	if l.hasOwnBackground(root.Box) {
		return root
	}
	body := bodyOf(root)
	if body != nil && l.hasOwnBackground(body.Box) {
		return body
	}
	// Neither declares one. Nothing is painted, which is right: the canvas is
	// whatever the reader puts behind the page.
	return nil
}

// hasOwnBackground reports whether a box declares a background of its own, which
// is what §2.11.2 asks before propagating from <body>: a colour that is not
// transparent, or an image.
func (l *layouter) hasOwnBackground(b *Box) bool {
	if b == nil {
		return false
	}
	raw := b.Style["background-color"]
	if strings.EqualFold(strings.TrimSpace(raw), "currentcolor") {
		// A background of "currentcolor" is the text colour, which is black by
		// default — so an element declaring it *does* have a background, and
		// reading the value literally would propagate <body>'s over the top of it.
		raw = b.Style["color"]
	}
	if c, ok := parseColorValue(raw); ok && c.A > 0 {
		return true
	}
	for _, raw := range splitCommaValues(b.Style["background-image"]) {
		if strings.TrimSpace(raw) != "" && !strings.EqualFold(strings.TrimSpace(raw), "none") {
			return true
		}
	}
	return false
}

// bodyOf finds the <body> the root element generated a box for.
func bodyOf(root *Fragment) *Fragment {
	var found *Fragment
	var walk func(*Fragment, int)
	walk = func(f *Fragment, depth int) {
		if found != nil || depth > 4 {
			// Bounded, because the only thing between the root and the body is
			// whatever anonymous or table-wrapper box the box tree put there.
			// Searching the whole document would find a <body> inside an
			// <iframe>-shaped mistake and propagate its background to the page.
			return
		}
		for _, c := range f.Children {
			if c.Box != nil && c.Box.Element != nil &&
				strings.EqualFold(c.Box.Element.Name, "body") {
				found = c
				return
			}
			walk(c, depth+1)
		}
	}
	walk(root, 0)
	return found
}

// colorRect is where a box's background *colour* goes.
//
// It is the painting area of the bottom layer, which is background-clip's job
// and is why this is not simply the padding box. The default is the border box,
// so a colour runs under the border — which is what makes a dashed border show
// the colour through its gaps rather than the page.
func (l *layouter) colorRect(f *Fragment) Rect {
	if f.Box == nil {
		return f.BorderRect
	}
	raw := strings.TrimSpace(f.Box.Style["background-clip"])
	if raw == "" || strings.EqualFold(raw, "border-box") {
		return f.BorderRect
	}
	clips := l.bgBoxes(f.Box, "background-clip", bgBorderBox)
	return boxRect(f, clips[len(clips)-1])
}

// boxRect is one of the three rectangles an origin or a clip names.
func boxRect(f *Fragment, which bgBox) Rect {
	switch which {
	case bgPaddingBox:
		return f.PaddingRect()
	case bgContentBox:
		return f.ContentRect()
	}
	return f.BorderRect
}

// paintsFor resolves every layer of one box's background against a geometry.
//
// f supplies the rectangles; canvas is what a fixed-attachment layer is
// positioned against; over, when it is not empty, replaces the painting area —
// which is what the canvas propagation needs and nothing else does.
//
// The layers come back in *painting* order: CSS lists them front to back, so the
// last one written is painted first and the first one written ends up on top.
func (l *layouter) paintsFor(b *Box, f *Fragment, canvas, over Rect) []bgPaint {
	layers := l.backgroundLayers(b)
	if len(layers) == 0 {
		return nil
	}
	out := make([]bgPaint, 0, len(layers))
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		if layer.image == nil || layer.image.Image == nil {
			continue
		}
		positioning := boxRect(f, layer.origin)
		if layer.fixed {
			positioning = canvas
		}
		painting := boxRect(f, layer.clip)
		if !over.Empty() {
			painting = over
		}
		if p, ok := l.tiling(b, layer, positioning, painting); ok {
			out = append(out, p)
		}
	}
	return out
}

// tiling is the arithmetic: from a layer and two rectangles to a first tile, a
// step and a clip.
func (l *layouter) tiling(b *Box, layer backgroundLayer, positioning, painting Rect) (bgPaint, bool) {
	if painting.Empty() {
		return bgPaint{}, false
	}
	// The *positioning* area may legitimately have no area: the root element of
	// a document whose body is empty is a box of zero height, and its background
	// still covers the canvas. Only the painting area has to be real. Everything
	// that would divide by a zero side — cover, contain, a percentage size,
	// "round" — produces a tile of no size, which is refused below by the rule
	// that refuses one anyway.
	w, h, wAuto, hAuto := l.tileSize(layer, positioning)
	if w <= 0 || h <= 0 {
		// §3.9: "If the image's width or height is zero, nothing is painted."
		// It is reached by "background-size: 0", and by an intrinsic size that
		// rounded to nothing after a contain.
		return bgPaint{}, false
	}

	// "round" is a second pass over the size rather than a repeat mode, which is
	// where the specification puts it: the tile is rescaled so that a whole
	// number of them fits the positioning area. And when only one axis rounds
	// and the other was auto, the other is rescaled with it — otherwise
	// "background-repeat: round no-repeat" would squash the picture by however
	// much the rounding moved it.
	roundX, roundY := layer.repeatX == bgRepeatRound, layer.repeatY == bgRepeatRound
	if roundX || roundY {
		before := Size{W: w, H: h}
		if roundX {
			n, ok := wholeTiles(positioning.W, w)
			if !ok {
				return bgPaint{}, false
			}
			w = positioning.W.Div(n)
		}
		if roundY {
			n, ok := wholeTiles(positioning.H, h)
			if !ok {
				return bgPaint{}, false
			}
			h = positioning.H.Div(n)
		}
		switch {
		case roundX && !roundY && hAuto && before.W > 0:
			h = h.Mul(w.Px() / before.W.Px())
		case roundY && !roundX && wAuto && before.H > 0:
			w = w.Mul(h.Px() / before.H.Px())
		}
		if w <= 0 || h <= 0 {
			return bgPaint{}, false
		}
	}

	x, stepX, clipX, clipW, okX := axisTiling(
		layer.repeatX, layer.posX, positioning.X, positioning.W, w,
		painting.X, painting.W)
	if !okX {
		return bgPaint{}, false
	}
	y, stepY, clipY, clipH, okY := axisTiling(
		layer.repeatY, layer.posY, positioning.Y, positioning.H, h,
		painting.Y, painting.H)
	if !okY {
		return bgPaint{}, false
	}

	clip := Rect{X: clipX, Y: clipY, W: clipW, H: clipH}
	if clip.Empty() {
		return bgPaint{}, false
	}
	if !l.tilesWithinCap(b, clip, stepX, stepY) {
		return bgPaint{}, false
	}
	return bgPaint{
		Clip:  clip,
		Tile:  Rect{X: x, Y: y, W: w, H: h},
		StepX: stepX, StepY: stepY,
		Image: layer.image.Image,
		Key:   layer.image.Key,
	}, true
}

// tilesWithinCap refuses a tiling whose cell count is past what this engine will
// hand to a backend, and says so.
func (l *layouter) tilesWithinCap(b *Box, clip Rect, stepX, stepY style.Unit) bool {
	if stepX <= 0 || stepY <= 0 {
		return false
	}
	// In floating point on purpose: the product of two counts overflows an int32
	// long before it reaches the cap, and a cap that can only be exceeded by
	// wrapping is one that never fires.
	cols := math.Ceil(clip.W.Px()/stepX.Px()) + 1
	rows := math.Ceil(clip.H.Px()/stepY.Px()) + 1
	if cols*rows <= maxBackgroundTiles {
		return true
	}
	l.rec.ReportDetail(Finding{
		Rule:   RuleLimit,
		Source: AtHTML(offsetOf(b)),
		Message: fmt.Sprintf(
			"a background image would be tiled about %.0f times to cover %.0f by %.0f "+
				"pixels, past the %.0f this engine will emit; it was not drawn",
			cols*rows, clip.W.Px(), clip.H.Px(), maxBackgroundTiles),
		Path:     PathOf(b.Element),
		Property: "background-size",
	})
	return false
}

// axisTiling resolves one axis: where the first tile goes, how far apart the
// tiles are, and how much of the painting area they may be drawn into.
//
// The clip is narrowed here rather than left to a backend because that is what
// makes "no repeat" expressible without a special case: an axis that does not
// repeat is one tile wide of clip, so a step of the tile's own size puts every
// other tile outside it. A backend that has to understand "step zero means only
// one" is a backend that has to reimplement this function.
func axisTiling(
	repeat bgRepeat, pos bgPos,
	areaStart, areaSize, tile style.Unit,
	paintStart, paintSize style.Unit,
) (at, step, clipStart, clipSize style.Unit, ok bool) {

	switch repeat {
	case bgRepeatRound:
		// The rescaling already happened, in tiling: the tile handed in here
		// divides the positioning area a whole number of times, so all that is
		// left is to start at the area's edge and butt them together. The
		// position is deliberately ignored, which is what the property means —
		// a rounded tiling fills the area exactly and has nowhere to be moved to.
		step = tile
		at = areaStart
		clipStart, clipSize = paintStart, paintSize

	case bgRepeatSpace:
		// Whole tiles, spread out, first and last against the edges.
		n := math.Floor(areaSize.Px() / tile.Px())
		if n < 2 {
			// Room for one or none: §3.6 says the position is used and the image
			// is not repeated, which is exactly no-repeat.
			return axisTiling(bgRepeatNone, pos, areaStart, areaSize, tile, paintStart, paintSize)
		}
		if n > maxBackgroundTiles {
			return 0, 0, 0, 0, false
		}
		gap := areaSize.Sub(tile.Mul(n)).Div(n - 1)
		step = tile.Add(gap)
		at = areaStart
		clipStart, clipSize = paintStart, paintSize

	case bgRepeatNone:
		at = areaStart.Add(pos.place(areaSize, tile))
		step = tile
		// One tile only, which the clip enforces: the neighbouring cells of the
		// tiling fall exactly outside it.
		clipStart = style.Max(paintStart, at)
		clipSize = style.Min(paintStart.Add(paintSize), at.Add(tile)).Sub(clipStart)

	default: // bgRepeatTile
		at = areaStart.Add(pos.place(areaSize, tile))
		step = tile
		clipStart, clipSize = paintStart, paintSize
	}

	if step <= 0 {
		// A tile that rounded away to nothing in fixed point. Drawing it would
		// be an infinite number of invisible marks.
		return 0, 0, 0, 0, false
	}
	if clipSize <= 0 {
		return 0, 0, 0, 0, false
	}
	return at, step, clipStart, clipSize, true
}

// tileSize is background-size resolved against the positioning area.
//
// The intrinsic ratio is what makes the "auto" cases more than a default: an
// image with a known ratio and one specified dimension takes the other from the
// ratio, which is how "background-size: 100% auto" fits the width and keeps the
// picture from being squashed.
// wAuto and hAuto report which dimensions were left to the image, which the
// "round" rescaling above needs: it restores the ratio only on an axis nobody
// asked for a size on.
func (l *layouter) tileSize(layer backgroundLayer, area Rect) (w, h style.Unit, wAuto, hAuto bool) {
	img := layer.image
	iw, ih := img.Width, img.Height
	if iw <= 0 || ih <= 0 {
		return 0, 0, false, false
	}
	ratio := img.Ratio
	if ratio <= 0 {
		ratio = iw.Px() / ih.Px()
	}

	switch layer.sizeKind {
	case bgSizeCover, bgSizeContain:
		// The scale that makes the image just cover, or just fit inside, the
		// area. The two differ by which of the two factors is taken, and by
		// nothing else.
		sx := area.W.Px() / iw.Px()
		sy := area.H.Px() / ih.Px()
		s := math.Min(sx, sy)
		if layer.sizeKind == bgSizeCover {
			s = math.Max(sx, sy)
		}
		return iw.Mul(s), ih.Mul(s), false, false
	}

	w, wAuto = resolveBgLength(layer.sizeW, area.W)
	h, hAuto = resolveBgLength(layer.sizeH, area.H)
	switch {
	case wAuto && hAuto:
		return iw, ih, true, true
	case wAuto:
		return h.Mul(ratio), h, true, false
	case hAuto:
		return w, w.Div(ratio), false, true
	}
	return w, h, false, false
}

// wholeTiles is how many copies of a tile "round" fits into an area.
//
// The count is rounded rather than floored, which is the whole difference
// between "round" and "space": an image a little wider than half the area is
// stretched to fill it rather than shrunk so that two fit.
func wholeTiles(area, tile style.Unit) (float64, bool) {
	if tile <= 0 {
		return 0, false
	}
	n := math.Round(area.Px() / tile.Px())
	if n < 1 {
		n = 1
	}
	if n > maxBackgroundTiles {
		// More tiles than anything downstream will draw. The cap that measures
		// the finished step cannot catch this one, because the step is what this
		// is about to compute.
		return 0, false
	}
	return n, true
}

// resolveBgLength turns one component of background-size into a number, or says
// it was auto.
func resolveBgLength(l style.Length, basis style.Unit) (style.Unit, bool) {
	switch l.Kind {
	case style.LengthPercent:
		return basis.Mul(l.Percent / 100), false
	case style.LengthCalc:
		return basis.Mul(l.Percent / 100).Add(l.Value), false
	case style.LengthAbsolute:
		return l.Value, false
	}
	return 0, true
}

// backgroundLayers reads a box's seven background properties into layers.
//
// The number of layers is background-image's, and every other property is used
// cyclically — a list of two repeats against three images gives the third image
// the first repeat. That is the CSS rule and it is not a convenience: it is what
// makes "background-image: url(a), url(b); background-repeat: no-repeat" mean
// no-repeat for both.
func (l *layouter) backgroundLayers(b *Box) []backgroundLayer {
	if b == nil {
		return nil
	}
	// The overwhelmingly common answer, taken before the memo and without
	// allocating anything: almost every box in a document has no background
	// image, and a memo entry for each of them would cost more than the parse it
	// saved.
	raw := strings.TrimSpace(b.Style["background-image"])
	if raw == "" || isNoneValue(raw) {
		return nil
	}
	if got, ok := l.backgrounds[b]; ok {
		return got
	}
	out := l.readBackgroundLayers(b, raw)
	l.backgrounds[b] = out
	return out
}

func (l *layouter) readBackgroundLayers(b *Box, raw string) []backgroundLayer {
	images := splitCommaValues(raw)
	if len(images) == 0 {
		return nil
	}
	// Every layer with no image paints nothing, so a list that is "none" all the
	// way through costs one walk and no arithmetic.
	any := false
	for _, v := range images {
		if !isNoneValue(v) {
			any = true
			break
		}
	}
	if !any {
		return nil
	}
	if len(images) > maxBackgroundLayers {
		l.rec.ReportDetail(Finding{
			Rule:     RuleLimit,
			Source:   AtHTML(offsetOf(b)),
			Message:  fmt.Sprintf("a background of %d layers is past the %d this engine will paint", len(images), maxBackgroundLayers),
			Path:     PathOf(b.Element),
			Property: "background-image",
		})
		images = images[:maxBackgroundLayers]
	}

	repeats := l.bgRepeats(b)
	positions := l.bgPositions(b)
	sizes := l.bgSizes(b)
	origins := l.bgBoxes(b, "background-origin", bgPaddingBox)
	clips := l.bgBoxes(b, "background-clip", bgBorderBox)
	fixed := l.bgAttachments(b)

	at := func(n int, i int) int { return i % n }
	out := make([]backgroundLayer, 0, len(images))
	for i, spec := range images {
		layer := backgroundLayer{
			repeatX: repeats[at(len(repeats), i)].x,
			repeatY: repeats[at(len(repeats), i)].y,
			posX:    positions[at(len(positions), i)].x,
			posY:    positions[at(len(positions), i)].y,

			sizeKind: sizes[at(len(sizes), i)].kind,
			sizeW:    sizes[at(len(sizes), i)].w,
			sizeH:    sizes[at(len(sizes), i)].h,

			origin: origins[at(len(origins), i)],
			clip:   clips[at(len(clips), i)],
			fixed:  fixed[at(len(fixed), i)],
		}
		layer.image = l.backgroundImage(b, spec)
		out = append(out, layer)
	}
	return out
}

// backgroundImage finds the loaded picture one layer names.
//
// Loading happened in the pass that loads an <img>'s, under the same policy and
// against the same document-wide decode budget: see image.go. What is left here
// is the lookup, and the report for a value that is a real CSS image this engine
// cannot produce.
func (l *layouter) backgroundImage(b *Box, raw string) *ReplacedContent {
	raw = strings.TrimSpace(raw)
	if raw == "" || isNoneValue(raw) {
		return nil
	}
	if ref, ok := urlValue(raw); ok {
		if b.BackgroundImages != nil {
			return b.BackgroundImages[ref]
		}
		return nil
	}
	// A gradient, an image-set, element(). Each is a real value this engine
	// reads and cannot paint, and each leaves a box that looks as though the
	// declaration were absent — which is the silent failure the whole finding
	// vocabulary exists for.
	l.reportOnce("bg-image:"+raw, Finding{
		Rule:     RuleUnsupportedValue,
		Source:   AtHTML(offsetOf(b)),
		Message:  "the background image " + quoteValue(raw) + " is not one this engine can paint; no image was drawn",
		Path:     PathOf(b.Element),
		Property: "background-image",
	})
	return nil
}

// reportOnce raises a finding the first time a key is seen, so a stylesheet rule
// that puts one gradient on four hundred elements is one thing to be told.
func (l *layouter) reportOnce(key string, f Finding) {
	if l.reportedBackgrounds[key] {
		return
	}
	l.reportedBackgrounds[key] = true
	l.rec.ReportDetail(f)
}

type bgRepeatPair struct{ x, y bgRepeat }

// bgRepeats reads background-repeat.
func (l *layouter) bgRepeats(b *Box) []bgRepeatPair {
	out := make([]bgRepeatPair, 0, 1)
	for _, raw := range splitCommaValues(b.Style["background-repeat"]) {
		words := strings.Fields(strings.ToLower(raw))
		pair, ok := repeatPair(words)
		if !ok {
			l.reportOnce("bg-repeat:"+raw, Finding{
				Rule:     RuleUnsupportedValue,
				Source:   AtHTML(offsetOf(b)),
				Message:  "the background repeat " + quoteValue(raw) + " is not one this engine reads; the image was tiled",
				Path:     PathOf(b.Element),
				Property: "background-repeat",
			})
		}
		out = append(out, pair)
	}
	if len(out) == 0 {
		out = append(out, bgRepeatPair{})
	}
	return out
}

func repeatPair(words []string) (bgRepeatPair, bool) {
	one := func(w string) (bgRepeat, bool) {
		switch w {
		case "repeat":
			return bgRepeatTile, true
		case "no-repeat":
			return bgRepeatNone, true
		case "space":
			return bgRepeatSpace, true
		case "round":
			return bgRepeatRound, true
		}
		return bgRepeatTile, false
	}
	switch len(words) {
	case 1:
		switch words[0] {
		case "repeat-x":
			return bgRepeatPair{x: bgRepeatTile, y: bgRepeatNone}, true
		case "repeat-y":
			return bgRepeatPair{x: bgRepeatNone, y: bgRepeatTile}, true
		}
		v, ok := one(words[0])
		return bgRepeatPair{x: v, y: v}, ok
	case 2:
		x, okx := one(words[0])
		y, oky := one(words[1])
		return bgRepeatPair{x: x, y: y}, okx && oky
	}
	return bgRepeatPair{}, false
}

type bgPosPair struct{ x, y bgPos }

// bgPositions reads background-position, in all four of its lengths.
func (l *layouter) bgPositions(b *Box) []bgPosPair {
	out := make([]bgPosPair, 0, 1)
	for _, raw := range splitCommaValues(b.Style["background-position"]) {
		vals, _ := css.ParseComponentValues(raw)
		pair, ok := l.parsePosition(b, vals)
		if !ok {
			l.reportOnce("bg-position:"+raw, Finding{
				Rule:     RuleUnsupportedValue,
				Source:   AtHTML(offsetOf(b)),
				Message:  "the background position " + quoteValue(raw) + " is not one this engine reads; the image was placed at the top left",
				Path:     PathOf(b.Element),
				Property: "background-position",
			})
		}
		out = append(out, pair)
	}
	if len(out) == 0 {
		out = append(out, bgPosPair{})
	}
	return out
}

// posTerm is one parsed component of a position: an edge keyword with an
// optional offset, or a bare length.
type posTerm struct {
	// edge is "left", "right", "top", "bottom", "center" or "" for a bare
	// length.
	edge   string
	offset style.Length
	// hasOffset distinguishes "left" from "left 0px", which resolve the same
	// way but parse differently.
	hasOffset bool
}

func (t posTerm) horizontal() bool {
	return t.edge == "left" || t.edge == "right"
}

func (t posTerm) vertical() bool {
	return t.edge == "top" || t.edge == "bottom"
}

// pos turns a term into an axis position.
func (t posTerm) pos() bgPos {
	switch t.edge {
	case "center":
		return bgPos{offset: style.Length{Kind: style.LengthPercent, Percent: 50}}
	case "right", "bottom":
		return bgPos{offset: t.offset, fromEnd: true}
	case "left", "top":
		return bgPos{offset: t.offset}
	}
	return bgPos{offset: t.offset}
}

func (l *layouter) parsePosition(b *Box, vals []css.ComponentValue) (bgPosPair, bool) {
	terms, ok := l.positionTerms(b, vals)
	if !ok || len(terms) == 0 || len(terms) > 2 {
		return bgPosPair{}, false
	}
	if len(terms) == 1 {
		t := terms[0]
		if t.hasOffset {
			// "left 10px" on its own is not a position: the one-value form takes
			// a keyword or a length, never a keyword *and* a length.
			return bgPosPair{}, false
		}
		if t.vertical() {
			return bgPosPair{x: bgPos{offset: style.Length{Kind: style.LengthPercent, Percent: 50}}, y: t.pos()}, true
		}
		return bgPosPair{x: t.pos(), y: bgPos{offset: style.Length{Kind: style.LengthPercent, Percent: 50}}}, true
	}

	a, c := terms[0], terms[1]
	// Two keywords may be written in either order — "top left" is a position and
	// so is "left top" — but a length is always on the axis its place says.
	if a.vertical() || c.horizontal() {
		if a.edge == "" || c.edge == "" {
			return bgPosPair{}, false
		}
		a, c = c, a
	}
	if a.vertical() || c.horizontal() {
		// Two of the same axis: "left right".
		return bgPosPair{}, false
	}
	if (a.hasOffset && a.edge == "") || (c.hasOffset && c.edge == "") {
		return bgPosPair{}, false
	}
	return bgPosPair{x: a.pos(), y: c.pos()}, true
}

// positionTerms groups a position value into its one or two terms.
func (l *layouter) positionTerms(b *Box, vals []css.ComponentValue) ([]posTerm, bool) {
	parts := splitValueParts(vals)
	var out []posTerm
	for i := 0; i < len(parts); {
		part := parts[i]
		if kw, ok := identOf(part); ok {
			switch kw {
			case "left", "right", "top", "bottom", "center":
				term := posTerm{edge: kw}
				// A keyword takes a following length as *its own* offset only in
				// the three- and four-value syntax, where every axis still names
				// an edge. In the two-value form the length belongs to the other
				// axis: CSS Backgrounds' grammar reads "left -1em" as the
				// alternative "[left|center|right|<length-percentage>]
				// [top|center|bottom|<length-percentage>]", so it is the left
				// edge horizontally and an em above the top vertically — not an
				// em in from the left and nothing said about the vertical.
				//
				// Grouping greedily and then refusing the result is what this did
				// first, and it turned "background: url(x) left -1em" into no
				// position at all: the image landed an em below where the
				// stylesheet put it, which in the suite's margin-collapse tests
				// slides a whole band of red out from under the box meant to
				// cover it.
				if kw != "center" && len(parts) > 2 && i+1 < len(parts) {
					if length, ok := l.lengthOfValues(b, parts[i+1]); ok {
						term.offset, term.hasOffset = length, true
						i += 2
						out = append(out, term)
						continue
					}
				}
				i++
				out = append(out, term)
				continue
			}
			return nil, false
		}
		length, ok := l.lengthOfValues(b, part)
		if !ok || length.Kind == style.LengthAuto {
			return nil, false
		}
		out = append(out, posTerm{offset: length})
		i++
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

type bgSizeValue struct {
	kind bgSizeKind
	w, h style.Length
}

// bgSizes reads background-size.
func (l *layouter) bgSizes(b *Box) []bgSizeValue {
	out := make([]bgSizeValue, 0, 1)
	for _, raw := range splitCommaValues(b.Style["background-size"]) {
		vals, _ := css.ParseComponentValues(raw)
		size, ok := l.parseSize(b, vals)
		if !ok {
			l.reportOnce("bg-size:"+raw, Finding{
				Rule:     RuleUnsupportedValue,
				Source:   AtHTML(offsetOf(b)),
				Message:  "the background size " + quoteValue(raw) + " is not one this engine reads; the image was drawn at its own size",
				Path:     PathOf(b.Element),
				Property: "background-size",
			})
		}
		out = append(out, size)
	}
	if len(out) == 0 {
		out = append(out, bgSizeValue{w: style.Auto, h: style.Auto})
	}
	return out
}

func (l *layouter) parseSize(b *Box, vals []css.ComponentValue) (bgSizeValue, bool) {
	auto := bgSizeValue{w: style.Auto, h: style.Auto}
	parts := splitValueParts(vals)
	switch len(parts) {
	case 1:
		if kw, ok := identOf(parts[0]); ok {
			switch kw {
			case "cover":
				return bgSizeValue{kind: bgSizeCover}, true
			case "contain":
				return bgSizeValue{kind: bgSizeContain}, true
			}
		}
		w, ok := l.lengthOfValues(b, parts[0])
		if !ok || negativeLength(w) {
			return auto, false
		}
		return bgSizeValue{w: w, h: style.Auto}, true
	case 2:
		w, okw := l.lengthOfValues(b, parts[0])
		h, okh := l.lengthOfValues(b, parts[1])
		if !okw || !okh || negativeLength(w) || negativeLength(h) {
			return auto, false
		}
		return bgSizeValue{w: w, h: h}, true
	}
	return auto, false
}

// negativeLength reports a length CSS forbids here: a background is not sized
// backwards.
// A calc() holding both a length and a percentage is deliberately not judged
// here. Whether it comes out negative depends on the box it is a percentage of,
// so it is not something the value can be asked on its own — CSS makes that a
// clamp at used-value time rather than a parse error, and this is the parse.
func negativeLength(l style.Length) bool {
	return (l.Kind == style.LengthAbsolute && l.Value < 0) ||
		(l.Kind == style.LengthPercent && l.Percent < 0)
}

// bgBoxes reads background-origin or background-clip.
func (l *layouter) bgBoxes(b *Box, property string, initial bgBox) []bgBox {
	out := make([]bgBox, 0, 1)
	for _, raw := range splitCommaValues(b.Style[property]) {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "border-box":
			out = append(out, bgBorderBox)
		case "padding-box":
			out = append(out, bgPaddingBox)
		case "content-box":
			out = append(out, bgContentBox)
		default:
			// "text" is the one that matters: it clips the background to the
			// glyphs, which needs a rasterised text mask this engine has no way
			// to build. Falling back to the initial value paints a rectangle
			// where the author asked for lettering.
			l.reportOnce(property+":"+raw, Finding{
				Rule:   RuleUnsupportedValue,
				Source: AtHTML(offsetOf(b)),
				Message: "the value " + quoteValue(raw) + " of " + property +
					" is not one this engine reads; the initial value was used",
				Path:     PathOf(b.Element),
				Property: property,
			})
			out = append(out, initial)
		}
	}
	if len(out) == 0 {
		out = append(out, initial)
	}
	return out
}

// bgAttachments reads background-attachment.
//
// "scroll" and "local" are the same thing here, and that is not an
// approximation: they differ by what happens when a box or its ancestor is
// scrolled, and a page does not scroll. "fixed" is a real difference and is
// implemented — it positions the image against the page rather than against the
// box, so a fixed background on a box halfway down the page starts its tiling
// from the page's corner, not from the box's.
func (l *layouter) bgAttachments(b *Box) []bool {
	out := make([]bool, 0, 1)
	for _, raw := range splitCommaValues(b.Style["background-attachment"]) {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "fixed":
			out = append(out, true)
		case "scroll", "local":
			out = append(out, false)
		default:
			l.reportOnce("bg-attachment:"+raw, Finding{
				Rule:   RuleUnsupportedValue,
				Source: AtHTML(offsetOf(b)),
				Message: "the value " + quoteValue(raw) +
					" of background-attachment is not one this engine reads; the image scrolls with the box",
				Path:     PathOf(b.Element),
				Property: "background-attachment",
			})
			out = append(out, false)
		}
	}
	if len(out) == 0 {
		out = append(out, false)
	}
	return out
}

// splitCommaValues divides a computed value into its comma-separated layers.
//
// It works on the text rather than on component values because that is what the
// cascade stores, and it has to skip a comma inside a function: "rgb(1, 2, 3)"
// is one value and url(a),url(b) is two.
func splitCommaValues(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(raw[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(raw[start:]))
}

// splitValueParts divides one layer's component values on whitespace.
func splitValueParts(vals []css.ComponentValue) [][]css.ComponentValue {
	var out [][]css.ComponentValue
	var cur []css.ComponentValue
	for _, v := range vals {
		if v.IsToken() && v.Token.Kind == css.Whitespace {
			if len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, v)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func identOf(part []css.ComponentValue) (string, bool) {
	if len(part) != 1 || !part[0].IsToken() || part[0].Token.Kind != css.Ident {
		return "", false
	}
	return strings.ToLower(part[0].Token.Value), true
}

func isNoneValue(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "none")
}

// urlValue extracts the reference from a url() value, in both spellings.
//
// "url(a.png)" is a single URL token; "url('a.png')" is a function with a string
// in it, because the two are tokenized by different rules. A reader that handled
// only the first would silently drop every quoted reference, which is the more
// common of the two forms in real stylesheets.
func urlValue(raw string) (string, bool) {
	vals, _ := css.ParseComponentValues(raw)
	parts := splitValueParts(vals)
	if len(parts) != 1 || len(parts[0]) != 1 {
		return "", false
	}
	v := parts[0][0]
	if v.IsToken() && v.Token.Kind == css.URL {
		return v.Token.Value, true
	}
	if v.IsFunction() && strings.EqualFold(v.Token.Value, "url") {
		for _, inner := range v.Values {
			if !inner.IsToken() {
				continue
			}
			switch inner.Token.Kind {
			case css.Whitespace:
				continue
			case css.String:
				return inner.Token.Value, true
			}
			return "", false
		}
	}
	return "", false
}

// backgroundImageRefs lists the files a computed background-image names, which
// is what the loading pass needs and all it needs.
func backgroundImageRefs(raw string) []string {
	var out []string
	for _, layer := range splitCommaValues(raw) {
		if ref, ok := urlValue(layer); ok && strings.TrimSpace(ref) != "" {
			out = append(out, ref)
		}
	}
	return out
}
