package htmlpdf

import "github.com/mgilbir/forme/layout"

// The rules this backend raises itself.
//
// layout.Compose reports everything the engine could not do. These are the
// things the engine did and this backend cannot write: the display list says
// them, and a PDF page drawn from it would say something else. Each defaults to
// Error, which refuses the document, and each is an ordinary rule a caller's
// Input.Policy can lower — to Warn, to get the document with the finding, or
// to Ignore — so that a caller who can live with the loss says so rather than
// finding it later.
const (
	// RuleVerticalText is a run set down the page: writing-mode vertical-rl,
	// vertical-lr, sideways-rl or sideways-lr, or text-orientation upright.
	// Its glyphs would be drawn across the page, in the wrong place and the
	// wrong way up.
	RuleVerticalText layout.Rule = "backend-vertical-text"

	// RuleLinkDropped is a document with a hyperlink. The display list
	// carries no links — a box's href is not a mark on the page — so the
	// page would have the link's text and nothing to follow.
	RuleLinkDropped layout.Rule = "backend-link-dropped"

	// RuleUnknownOp is a display-list operation this backend does not know,
	// which can only be one a newer layout engine added. Leaving it out
	// would leave part of the page undrawn.
	RuleUnknownOp layout.Rule = "backend-unknown-op"
)
