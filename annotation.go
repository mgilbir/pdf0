package pdf0

import (
	"fmt"
	"strings"

	"github.com/mgilbir/pdf0/object"
)

// Link annotations: the rectangles that make part of a page follow a reference.
//
// A page could carry drawn marks and nothing else. A link is the first thing
// beyond marks that a document generated from a source with hyperlinks needs,
// and it is not something a caller can add afterwards without rebuilding the
// page dictionary that AddPage exists to build.

// Link is a rectangle on a page that follows a reference when activated.
//
// Exactly one destination is given: a URI for somewhere outside the document,
// or a page for somewhere inside it. Both or neither is an error, because a
// link that goes nowhere is a rectangle a reader will highlight and then do
// nothing with, which is worse than no link at all.
type Link struct {
	// Rect is the active area in page coordinates, as
	// [xMin, yMin, xMax, yMax].
	Rect [4]float64

	// URI is the address to follow, for a link out of the document.
	URI string

	// Page is a reference to a page in this document, for a link within it.
	// The view is left to the reader, which shows the whole page.
	Page *object.IndirectRef
}

// annotation builds the annotation dictionary for a link.
//
// The flags are what make it conforming. A non-Popup annotation must declare
// its flags, with Print set and Hidden, Invisible, NoView and ToggleNoView
// clear (ISO 19005 6.3.2) — a link that does not print is one that vanishes
// from the paper copy, and a validator reports it. The border is set to zero
// width because a visible border around every link is a default nobody wants
// and every producer overrides.
func (l Link) annotation() (*object.Dictionary, error) {
	if l.Rect[2] <= l.Rect[0] || l.Rect[3] <= l.Rect[1] {
		return nil, fmt.Errorf("pdf0: the link's rectangle %v has no area, so nothing could activate it", l.Rect)
	}
	switch {
	case l.URI != "" && l.Page != nil:
		return nil, fmt.Errorf("pdf0: the link names both a URI and a page; it can have one destination")
	case l.URI == "" && l.Page == nil:
		return nil, fmt.Errorf("pdf0: the link names no destination")
	}

	a := &object.Dictionary{}
	a.Set("Type", object.Name("Annot"))
	a.Set("Subtype", object.Name("Link"))
	a.Set("Rect", object.Array{
		numberFor(l.Rect[0]), numberFor(l.Rect[1]),
		numberFor(l.Rect[2]), numberFor(l.Rect[3]),
	})
	const flagPrint = 1 << 2
	a.Set("F", object.Integer(flagPrint))
	a.Set("Border", object.Array{object.Integer(0), object.Integer(0), object.Integer(0)})

	if l.URI != "" {
		if err := checkURI(l.URI); err != nil {
			return nil, err
		}
		action := &object.Dictionary{}
		action.Set("Type", object.Name("Action"))
		action.Set("S", object.Name("URI"))
		action.Set("URI", object.String{Value: []byte(l.URI)})
		a.Set("A", action)
		return a, nil
	}
	// An internal link is a destination rather than an action. The two are
	// interchangeable to a reader and not to a validator: PDF/A restricts what
	// actions a document may carry, and a destination is not an action at all.
	a.Set("Dest", object.Array{*l.Page, object.Name("Fit")})
	return a, nil
}

// checkURI refuses the destinations that are not addresses.
//
// A javascript: URI is a script by another name, and PDF/A forbids scripts. The
// check is here rather than left to the validator because a caller building a
// document from untrusted input — a web page, say — would otherwise carry an
// attacker's script into a file that claims to conform.
func checkURI(uri string) error {
	if strings.ContainsAny(uri, "\x00\r\n") {
		return fmt.Errorf("pdf0: the link's URI contains a control character")
	}
	scheme, _, ok := strings.Cut(uri, ":")
	if !ok {
		return fmt.Errorf("pdf0: %q is not an absolute URI; a link needs a scheme", uri)
	}
	switch strings.ToLower(scheme) {
	case "javascript", "vbscript", "data":
		return fmt.Errorf("pdf0: a %s: URI is a script rather than an address, and may not be linked to", scheme)
	}
	return nil
}
