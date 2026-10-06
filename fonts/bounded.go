package fonts

import (
	"context"

	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
)

// Shaping text a caller does not control.
//
// Shaping runs the font's own lookup programs over the text, and a hostile
// font, or ordinary text that is very long, can make that cost what the caller
// cannot afford: a lookup that multiplies glyphs, a chain of contextual
// lookups that revisits every position. Shape, ShapeWith and DrawShaped shape
// without bounds, as forme's ShapeGlyphs does. Their Context forms shape under
// a context and RunLimits — the bytes of input, the glyphs a run may grow to,
// and the lookup work charged — and fail rather than finish when either says
// stop: with the context's error, or one wrapping ErrRunLimit.
//
// A run that fails is not drawn and not recorded. The stream is unchanged, and
// the face's record of the glyphs used, which decides what is embedded, has
// nothing from it. A run that succeeds is exactly what the unbounded call
// would have produced.
//
// htmlpdf's text is shaped by forme's layout, which these do not reach.

// RunLimits bounds one shaped run: its input bytes, the glyphs it may grow
// to and the lookup work charged for it. A zero field is the default: 4096
// bytes and 32768 glyphs, as forme's, and DefaultRunWork.
type RunLimits = shape.RunLimits

// DefaultRunWork is the lookup work a run may be charged when RunLimits
// leaves it zero.
//
// It is not forme's default, 64 million units, which refuses ordinary text:
// forme charges about 460 thousand units a byte of Latin and 8 million a byte
// of Devanagari in Noto Sans, whatever lookups apply, so 64 million is 140
// bytes of Latin and 8 of Devanagari (forme#916). This admits the input
// default, 4096 bytes, of the costliest text measured with twice that to
// spare, and is about 70 ms of shaping.
const DefaultRunWork = 1 << 36

// ErrRunLimit is what a run that went past its RunLimits fails with,
// wrapped.
var ErrRunLimit = shape.ErrRunLimit

// shapeBounded runs one shaping call under limits on a private clone of the
// face, and, when it succeeds, records on this face the glyphs the clone
// recorded, as the unbounded call would have. That is the whole record for a
// simple face, whose glyphs only shaping records; a composite face's are
// recorded again as they are drawn (plan). A run that fails records nothing.
func (f *Face) shapeBounded(ctx context.Context, limits RunLimits, shapeFn func(*shape.Face) ([]Glyph, int)) ([]Glyph, int, error) {
	var (
		glyphs  []Glyph
		missing int
		used    []int
	)
	if limits.MaxWork == 0 {
		limits.MaxWork = DefaultRunWork
	}
	if _, err := f.Face.WithShapingLimits(ctx, limits, func(c *shape.Face) error {
		glyphs, missing = shapeFn(c)
		used = c.Used()
		return nil
	}); err != nil {
		return nil, 0, err
	}
	f.Use(used...)
	return glyphs, missing, nil
}

// ShapeContext is Shape under a context and limits; see RunLimits. On error
// there are no spans and nothing is recorded.
func (f *Face) ShapeContext(ctx context.Context, s string, limits RunLimits) (spans []content.TextSpan, missing int, err error) {
	glyphs, missing, err := f.shapeBounded(ctx, limits, func(c *shape.Face) ([]Glyph, int) { return c.ShapeGlyphs(s) })
	if err != nil {
		return nil, 0, err
	}
	spans, dropped := f.spans(glyphs, s)
	return spans, missing + dropped, nil
}

// ShapeWithContext is ShapeWith under a context and limits; see RunLimits. The
// feature tags count towards the input bytes. On error there are no spans and
// nothing is recorded.
func (f *Face) ShapeWithContext(ctx context.Context, s string, limits RunLimits, features ...string) (spans []content.TextSpan, missing int, err error) {
	glyphs, missing, err := f.shapeBounded(ctx, limits, func(c *shape.Face) ([]Glyph, int) {
		return c.ShapeGlyphsWith(s, features...)
	})
	if err != nil {
		return nil, 0, err
	}
	spans, dropped := f.spans(glyphs, s)
	return spans, missing + dropped, nil
}

// DrawShapedContext is DrawShaped under a context and limits; see RunLimits.
// On error nothing is written to b and nothing is recorded.
func (f *Face) DrawShapedContext(ctx context.Context, b *content.Builder, s string, size float64, limits RunLimits) (missing int, err error) {
	glyphs, missing, err := f.shapeBounded(ctx, limits, func(c *shape.Face) ([]Glyph, int) { return c.ShapeGlyphs(s) })
	if err != nil {
		return 0, err
	}
	f.draw(b, glyphs, s, size)
	return missing, nil
}
