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
	// RuleVerticalText is a run set down the page that cannot be drawn where
	// layout placed it. A run turned sideways is drawn, by turning the text
	// matrix, and so is one set upright in a face whose vertical advances are
	// an em, as a CJK face's are. Refused are an upright run in any other face
	// (layout measures upright text at an em per character and does not read
	// the face's vertical metrics, so it would be drawn longer or shorter than
	// its space), and a combination of turns forme does not make.
	RuleVerticalText layout.Rule = "backend-vertical-text"

	// RuleLinkDropped is a hyperlink this backend cannot write as a link
	// annotation, so the page would have the link's text and nothing to
	// follow: a reference relative to the HTML document, whose address this
	// backend is not given; a fragment, whose target the display list does
	// not place; or a URI pdf0's link builder refuses (see pdf0.LinkURI).
	// Every other link is written.
	RuleLinkDropped layout.Rule = "backend-link-dropped"

	// RuleUnknownOp is a display-list operation this backend does not know,
	// which can only be one a newer layout engine added. Leaving it out
	// would leave part of the page undrawn.
	RuleUnknownOp layout.Rule = "backend-unknown-op"
)
