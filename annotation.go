package pdf0

import (
	"fmt"
	"math"
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
	Page *object.IndirectRef

	// To says where on that page to go. The zero value shows the whole page,
	// which is right for a link to a chapter and wrong for a link to a
	// paragraph — a reader that jumps to the top of a forty-page section has not
	// answered the question the link asked.
	To Destination
}

// Destination is where on a page a link arrives.
//
// The zero value is the whole page, so a Link that says nothing about it keeps
// the behaviour it had before this existed.
type Destination struct {
	// Kind selects between the forms below.
	Kind DestinationKind

	// Top is the y coordinate to bring to the top of the window, for AtPosition
	// and AtTop. It is in the page's own coordinate space, where y increases
	// upwards — so the top of a US Letter page is 792, not 0. Getting that
	// backwards sends every anchor to the wrong end of the page.
	Top float64

	// Left is the x coordinate to bring to the left edge, for AtPosition.
	Left float64

	// Zoom is the magnification for AtPosition. Zero means "leave it as it is",
	// which is almost always what an anchor within a document wants: changing
	// the reader's zoom because they followed a link is a surprise.
	Zoom float64
}

// DestinationKind is which of the destination forms a link uses.
type DestinationKind int

const (
	// WholePage fits the entire page in the window (/Fit). It is the zero value.
	WholePage DestinationKind = iota
	// AtTop brings a given y coordinate to the top of the window and leaves the
	// horizontal position and magnification alone (/FitH). This is the right
	// choice for an anchor in flowing text.
	AtTop
	// AtPosition brings a given corner to the top left and optionally sets the
	// magnification (/XYZ).
	AtPosition
)

// destination builds the destination array for a link into this document.
//
// A destination is [page /Name args…], and the argument that is omitted is
// written as null rather than left out: the array is positional, so a shorter
// one does not mean "leave this alone", it means a different destination.
func (d Destination) destination(page object.IndirectRef) (object.Array, error) {
	switch d.Kind {
	case WholePage:
		return object.Array{page, object.Name("Fit")}, nil
	case AtTop:
		if err := checkFinite("the link's destination top", d.Top); err != nil {
			return nil, err
		}
		return object.Array{page, object.Name("FitH"), numberFor(d.Top)}, nil
	case AtPosition:
		for name, v := range map[string]float64{
			"left": d.Left, "top": d.Top, "zoom": d.Zoom,
		} {
			if err := checkFinite("the link's destination "+name, v); err != nil {
				return nil, err
			}
		}
		if d.Zoom < 0 {
			return nil, fmt.Errorf("pdf0: the link's destination zoom is %g; magnification cannot be negative", d.Zoom)
		}
		zoom := object.Object(object.Null{})
		if d.Zoom != 0 {
			// A zoom of zero and an omitted zoom mean the same thing to a reader
			// — leave the magnification alone — and null is how that is written.
			zoom = numberFor(d.Zoom)
		}
		return object.Array{page, object.Name("XYZ"), numberFor(d.Left), numberFor(d.Top), zoom}, nil
	}
	return nil, fmt.Errorf("pdf0: unknown link destination kind %d", d.Kind)
}

func checkFinite(what string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("pdf0: %s is %v, which is not a coordinate", what, v)
	}
	return nil
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
	case l.URI != "" && l.To != (Destination{}):
		// Where to arrive on a page is meaningless for a link that leaves the
		// document, and silently dropping it would hide a caller's mistake about
		// which kind of link they were building.
		return nil, fmt.Errorf("pdf0: the link goes to a URI, so a position within a page has no meaning")
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
	dest, err := l.To.destination(*l.Page)
	if err != nil {
		return nil, err
	}
	a.Set("Dest", dest)
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
