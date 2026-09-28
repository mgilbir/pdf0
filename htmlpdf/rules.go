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
	// RuleVerticalText is a run set down the page whose turn is not one forme
	// makes: Anticlockwise or Upright without Sideways, or Upright with
	// Anticlockwise (CSS Writing Modes 5.1: sideways-lr turns every
	// character, so nothing on it stands upright). The backend does not guess
	// what such a run means. Every turn forme does make is drawn: a run turned
	// sideways, by turning the text matrix, and a run set upright, in the
	// face's vertical form by its vertical metrics, or on em boxes where the
	// face states none, as layout measured it.
	RuleVerticalText layout.Rule = "backend-vertical-text"

	// RuleLinkDropped is a hyperlink this backend cannot write as a link
	// annotation, so the page would have the link's text and nothing to
	// follow: a reference relative to the HTML document, whose address this
	// backend is not given (forme resolves one against a <base href> with an
	// http or https URL, and that one is written); a fragment with no such
	// base, whose target the display list does not place; or a URI pdf0's
	// link builder refuses (see pdf0.LinkURI). Every other link is written.
	RuleLinkDropped layout.Rule = "backend-link-dropped"

	// RuleUnknownOp is a display-list operation this backend does not know,
	// which can only be one a newer layout engine added. Leaving it out
	// would leave part of the page undrawn.
	RuleUnknownOp layout.Rule = "backend-unknown-op"

	// RuleUndrawable is an operation this backend knows and cannot write as
	// the display list states it, because ISO 32000-2 has no way to say it
	// exactly: a Gaussian blur, for one, which a PDF page has no operator
	// for. The finding's message names the first such operation and why.
	// Leaving it out, or drawing an approximation of it, would put a page in
	// the file other than the one layout composed.
	RuleUndrawable layout.Rule = "backend-undrawable"
)
