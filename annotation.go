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

	// URI is the address to follow, for a link out of the document. Its scheme
	// must be http, https, ftp, mailto or tel. Surrounding spaces and control
	// characters are removed, as a browser's URL parser removes them; one
	// inside it is refused; and anything outside printable ASCII is
	// percent-encoded as UTF-8.
	URI string

	// Page is a reference to a page in this document, for a link within it.
	// It must be one of PageList's pages; anything else is refused by AddPage.
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
		for _, c := range []struct {
			name string
			v    float64
		}{{"left", d.Left}, {"top", d.Top}, {"zoom", d.Zoom}} {
			if err := checkFinite("the link's destination "+c.name, c.v); err != nil {
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

// annotation builds the annotation dictionary for a link.
//
// The flags are what make it conforming. A non-Popup annotation must declare
// its flags, with Print set and Hidden, Invisible, NoView and ToggleNoView
// clear (ISO 19005 6.3.2) — a link that does not print is one that vanishes
// from the paper copy, and a validator reports it. The border is set to zero
// width because a visible border around every link is a default nobody wants
// and every producer overrides.
func (l Link) annotation() (*object.Dictionary, error) {
	if err := checkBox("the link's rectangle", l.Rect, "nothing could activate it"); err != nil {
		return nil, err
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
		uri, err := linkURI(l.URI)
		if err != nil {
			return nil, err
		}
		action := &object.Dictionary{}
		action.Set("Type", object.Name("Action"))
		action.Set("S", object.Name("URI"))
		action.Set("URI", object.String{Value: []byte(uri)})
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

// linkURISchemes are the schemes a link may carry: the web, file transfer,
// mail and telephone numbers — each an address a reader hands to the program
// that opens it. Anything else is refused rather than written. javascript: and
// vbscript: are scripts, which PDF/A forbids; data: and blob: carry a document
// of their own, which a browser renders as one; file: points into the reader's
// own file system. A scheme outside a list is the only rule that does not have
// to anticipate the next of them.
var linkURISchemes = map[string]bool{
	"http": true, "https": true, "ftp": true, "mailto": true, "tel": true,
}

// linkURI returns the URI a link writes for uri, or why it cannot be one.
//
// The scheme is read the way a browser's URL parser reads it (the WHATWG URL
// Standard, "basic URL parser"), which strips leading and trailing C0 controls
// and spaces and removes every tab, line feed and carriage return wherever it
// is before it looks at the scheme. A check on the string as given is a check
// a browser does not make — "java\tscript:" and " javascript:" are both
// javascript: to it — and a caller building a document from untrusted input, a
// web page say, would carry an attacker's script into a file that claims to
// conform (audit 2026-09-22 C132). So the surrounding controls and spaces are
// stripped here too, and a control character left inside — a tab or newline a
// browser would silently remove, or any other — is refused rather than
// guessed at. What remains must be an absolute URI whose scheme is on the
// list.
//
// What is written is that normalised URI, with every byte outside printable
// ASCII, and the characters a URI may not carry unescaped (space " < > \ ^ `
// { | }), percent-encoded: ISO 32000-2 12.6.4.8 makes /URI 7-bit ASCII, and
// UTF-8 bytes written raw are read by each viewer in its own encoding. An
// existing percent-escape is kept as it is. A non-ASCII host is percent-encoded
// too, which a browser decodes and converts to its IDNA form.
func linkURI(uri string) (string, error) {
	isC0OrSpace := func(r rune) bool { return r <= 0x20 }
	uri = strings.TrimFunc(uri, isC0OrSpace)
	for i := 0; i < len(uri); i++ {
		if c := uri[i]; c < 0x20 || c == 0x7f {
			return "", fmt.Errorf("pdf0: the link's URI contains the control character %#x", c)
		}
	}
	scheme, _, ok := strings.Cut(uri, ":")
	if !ok || !validScheme(scheme) {
		return "", fmt.Errorf("pdf0: %q is not an absolute URI; a link needs a scheme", uri)
	}
	if !linkURISchemes[strings.ToLower(scheme)] {
		return "", fmt.Errorf("pdf0: a %s: URI is not an address a link may carry "+
			"(the schemes allowed are http, https, ftp, mailto and tel)", strings.ToLower(scheme))
	}
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(uri); i++ {
		c := uri[i]
		if c > 0x20 && c < 0x7f && !strings.ContainsRune("\"<>\\^`{|}", rune(c)) {
			out.WriteByte(c)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[c>>4])
		out.WriteByte(hex[c&0xF])
	}
	return out.String(), nil
}

// validScheme reports whether s is a URI scheme: an ASCII letter followed by
// letters, digits, "+", "-" and "." (RFC 3986 3.1).
func validScheme(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}
