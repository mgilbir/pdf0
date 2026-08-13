package render

import (
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/html"
	"github.com/mgilbir/pdf0/style"
)

// Form controls, as the static boxes a printed page has.
//
// # What this is and is not
//
// A page laid out once is not interactive, and for a long time that was the
// reason this engine refused a form control outright. It is the wrong reason.
// Interactivity is not what a control contributes to a *page*: a <textarea> has
// an intrinsic size from its cols and rows, it has text in it, and a browser
// asked to print one puts that text on the paper inside a box. Refusing the
// element threw the text away and reported only that an element had been
// dropped — a document silently short of content, which is the class of fault
// this engine reports everywhere else rather than commits.
//
// So the controls are laid out, and the boundary that stays is the one that was
// always meant:
//
//   - nothing is submitted and nothing is typed into;
//   - no value a reader would have entered is invented. What is drawn is what
//     the markup says — a "value" attribute, an option's label, a textarea's
//     content — and nothing else;
//   - no PDF form field is produced. An AcroForm is a different feature and
//     this is not a back door to one;
//   - a control whose rendering is a widget rather than a box says so, through
//     RuleControlApproximated, rather than being drawn as something plausible.
//
// # Where the sizes come from
//
// HTML's rendering section gives a control an intrinsic size in *characters*
// and *lines* rather than in pixels: a textarea is cols characters wide and rows
// lines tall, a text input is size characters wide and one line tall. Both are
// resolved against the element's own font at layout time, which is the only
// place either number exists — a character is the advance of "0" in the face
// that will set the text, which is what the "ch" unit means, and a line is the
// line-height the box has computed.
//
// The two deliberate deviations from what a browser produces:
//
//   - a browser adds the width of a scrollbar to a textarea's intrinsic width,
//     because it renders one. Nothing here renders a scrollbar, so nothing is
//     added for it, and a textarea is the width its author asked for in
//     characters rather than that plus a chrome that is not on the page.
//   - HTML says "average character width" where this uses the advance of "0".
//     The two are the same for the monospaced faces that cols and size are
//     nearly always about, and the specification fixes neither number.
//
// # The bounds
//
// cols, rows and size are integers in untrusted markup. Each is clamped, and the
// clamp is reported rather than silent — see maxControlChars.

// controlKind is what sort of control an element is, for the purposes of
// laying it out. It is not the type attribute: several input types are the same
// box, and the ones that are not are told apart here.
type controlKind uint8

const (
	controlNone controlKind = iota
	// controlTextArea is <textarea>: cols by rows, its own text inside it,
	// preserved white space that wraps.
	controlTextArea
	// controlField is an <input> that is a one-line text-entry box.
	controlField
	// controlButton is <button> and the button-shaped inputs, whose label is
	// centred and whose box shrinks to fit it.
	controlButton
	// controlToggle is a checkbox or a radio: a small fixed square, sized by
	// the user-agent stylesheet rather than from here.
	controlToggle
	// controlSelect is <select>, drawn as the field it is on paper.
	controlSelect
	// controlWidget is a control whose rendering is a widget with no static
	// form at all — a file picker, a slider, a colour swatch. It is drawn as an
	// empty field and reported.
	controlWidget
)

// Control is what a box needs to know to be laid out as a control.
//
// It is a value on the Box for the same reason ReplacedContent is: being a
// control changes where a box's size comes from and nothing else about it. A
// control still floats, still positions, still takes part in a line, and every
// rule about those applies unchanged.
type Control struct {
	Kind controlKind

	// Chars is the intrinsic content width in "0" advances, or zero when the
	// control has none and shrinks to fit instead.
	Chars int
	// Lines is the intrinsic content height in line boxes, or zero when the
	// height is the content's own.
	//
	// It is a *used* height rather than a minimum, which is what makes a
	// textarea with twenty lines of text in it two lines tall and scrolled:
	// rows says how much of the control is on the page, not how much text it
	// holds.
	Lines int
}

// maxControlChars and maxControlLines bound cols, rows and size.
//
// HTML puts no limit on any of them, so this is a limit of this engine's and is
// reported as one. It is needed because the three are integers in untrusted
// markup and each multiplies a font metric: "cols=2000000000" asks for a box
// eighty billion pixels wide, which saturates the layout unit and leaves a
// document whose one control is the whole page.
//
// Ten thousand is far past anything a page can show — a ten-thousand-character
// line is a hundred and sixty thousand pixels, some five hundred sheets of A4
// side by side — and far short of where the arithmetic stops being exact. The
// point of the number is not to be the right editorial limit but to be one, and
// to be visible when it decides.
const (
	maxControlChars = 10000
	maxControlLines = 10000
)

// defaultControlCols, defaultControlRows and defaultInputSize are HTML's
// defaults for the three attributes.
//
// Zero is not a separate case from absent, and that is HTML's rule rather than a
// simplification: cols, rows and size are each "limited to only positive
// numbers", so a value that is zero, negative or not a number at all is invalid
// and the default applies. An engine that read cols="0" as a zero-width control
// would produce a box no browser produces.
const (
	defaultControlCols = 20
	defaultControlRows = 2
	defaultInputSize   = 20
)

// inputTypeOf reads an <input>'s type, lower-cased, with HTML's fallback.
//
// A missing or unrecognised type is "text", which is what HTML says and is what
// makes "<input>" a text field.
func inputTypeOf(n *html.Node) string {
	v, _ := n.Attr("type")
	t := strings.ToLower(strings.TrimSpace(v))
	switch t {
	case "text", "password", "search", "tel", "url", "email", "number",
		"date", "month", "week", "time", "datetime-local",
		"submit", "reset", "button", "checkbox", "radio",
		"file", "range", "color", "image", "hidden":
		return t
	}
	return "text"
}

// controlKindOf classifies an element.
func controlKindOf(n *html.Node) controlKind {
	if n == nil || n.Type != html.ElementNode {
		return controlNone
	}
	switch strings.ToLower(n.Name) {
	case "textarea":
		return controlTextArea
	case "select":
		return controlSelect
	case "button":
		return controlButton
	case "input":
		switch inputTypeOf(n) {
		case "submit", "reset", "button":
			return controlButton
		case "checkbox", "radio":
			return controlToggle
		case "file", "range", "color", "image":
			return controlWidget
		case "hidden":
			// It generates no box at all — the user-agent stylesheet says so —
			// so it never reaches layout and needs no kind of its own.
			return controlNone
		}
		return controlField
	}
	return controlNone
}

// controlFor builds the Control for an element, reporting what it had to clamp
// and what it can only approximate.
func (b *boxBuilder) controlFor(n *html.Node) *Control {
	kind := controlKindOf(n)
	if kind == controlNone {
		return nil
	}
	c := &Control{Kind: kind}
	switch kind {
	case controlTextArea:
		c.Chars = b.positiveAttr(n, "cols", defaultControlCols, maxControlChars)
		c.Lines = b.positiveAttr(n, "rows", defaultControlRows, maxControlLines)
	case controlField:
		c.Chars = b.positiveAttr(n, "size", defaultInputSize, maxControlChars)
		c.Lines = 1
	case controlSelect:
		c.Lines = b.selectRows(n)
	case controlWidget:
		// A field-shaped box, so that the control is visibly on the page, and a
		// finding saying that the widget itself is not.
		c.Chars = defaultInputSize
		c.Lines = 1
	}
	b.reportApproximation(n, kind)
	return c
}

// positiveAttr reads one of HTML's "limited to only positive numbers"
// attributes, applying the default and the bound.
//
// The clamp is a finding rather than a silent maximum, because a control ten
// thousand characters wide is not what the document asked for and the page it
// produces would otherwise be inexplicable.
func (b *boxBuilder) positiveAttr(n *html.Node, name string, fallback, limit int) int {
	raw, ok := n.Attr(name)
	if !ok {
		return fallback
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v < 1 {
		// Invalid, which zero and every negative are. HTML applies the default,
		// so "cols=0" and no cols at all are the same control — a difference an
		// implementation invents by reading the attribute as a number rather
		// than as HTML says to.
		return fallback
	}
	if v > limit {
		b.rec.ReportDetail(Finding{
			Rule:   RuleLimit,
			Source: AtHTML(n.Offset),
			Message: "the " + name + " attribute asks for " + strconv.Itoa(v) +
				", more than the " + strconv.Itoa(limit) + " this engine will size a control to; " +
				"it was laid out at " + strconv.Itoa(limit),
			Path: PathOf(n),
		})
		return limit
	}
	return v
}

// selectRows is how many option rows a <select> shows.
//
// A select with neither "multiple" nor a size above one is a drop-down, which
// shows exactly one row whatever is in it; anything else is a list box, which
// shows as many rows as its size says. HTML's default size for a list box is
// four.
func (b *boxBuilder) selectRows(n *html.Node) int {
	multiple := n.HasAttr("multiple")
	size := b.positiveAttr(n, "size", 0, maxControlLines)
	if size <= 1 && !multiple {
		return 1
	}
	if size <= 1 {
		return 4
	}
	return size
}

// selectIsDropDown reports whether only the chosen option is shown.
func selectIsDropDown(n *html.Node) bool {
	if n.HasAttr("multiple") {
		return false
	}
	raw, ok := n.Attr("size")
	if !ok {
		return true
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	return err != nil || v <= 1
}

// reportApproximation names the controls whose rendering here is a box standing
// in for a widget.
//
// It fires where the difference is *visible on the page* and nowhere else. A
// text field with a border round its value is what a browser prints, so it says
// nothing; a slider is a track and a thumb at a position, and a bordered empty
// box is not a picture of one.
func (b *boxBuilder) reportApproximation(n *html.Node, kind controlKind) {
	switch kind {
	case controlWidget:
		b.rec.ReportDetail(Finding{
			Rule:   RuleControlApproximated,
			Source: AtHTML(n.Offset),
			Message: "an input of type " + quoteValue(inputTypeOf(n)) +
				" is drawn as an empty field: its widget has no static form this engine can put on a page",
			Path: PathOf(n),
		})

	case controlToggle:
		if !n.HasAttr("checked") {
			// An unchecked box is an empty square, which is what is drawn.
			return
		}
		b.rec.ReportDetail(Finding{
			Rule:   RuleControlApproximated,
			Source: AtHTML(n.Offset),
			Message: "this " + inputTypeOf(n) + " is checked and is drawn as an empty square; " +
				"the mark inside a checked control is a widget rather than a box",
			Path: PathOf(n),
		})

	case controlSelect:
		options := optionsOf(n)
		if selectIsDropDown(n) {
			if len(options) <= 1 {
				return
			}
			b.rec.ReportDetail(Finding{
				Rule:   RuleControlApproximated,
				Source: AtHTML(n.Offset),
				Message: "this drop-down shows only its selected option; the other " +
					strconv.Itoa(len(options)-1) + " are not on the page",
				Path: PathOf(n),
			})
			return
		}
		if rows := b.selectRows(n); len(options) > rows {
			b.rec.ReportDetail(Finding{
				Rule:   RuleControlApproximated,
				Source: AtHTML(n.Offset),
				Message: "this list box has " + strconv.Itoa(len(options)) + " options and shows " +
					strconv.Itoa(rows) + "; the rest are below the edge of a box that cannot be scrolled here",
				Path: PathOf(n),
			})
		}
	}
}

// optionsOf collects a select's options, through any optgroups.
func optionsOf(sel *html.Node) []*html.Node {
	var out []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for _, c := range n.Children {
			if c.Type != html.ElementNode {
				continue
			}
			switch strings.ToLower(c.Name) {
			case "option":
				out = append(out, c)
			case "optgroup":
				walk(c)
			}
		}
	}
	walk(sel)
	return out
}

// chosenOption is the option a drop-down shows.
//
// HTML's selectedness: the last option with a "selected" attribute wins, and
// with none the first option is selected. "Last" rather than "first" is the rule
// as written, and it is what a browser does with markup that names two.
func chosenOption(sel *html.Node) *html.Node {
	options := optionsOf(sel)
	var chosen *html.Node
	for _, o := range options {
		if o.HasAttr("selected") {
			chosen = o
		}
	}
	if chosen != nil {
		return chosen
	}
	if len(options) > 0 {
		return options[0]
	}
	return nil
}

// controlLabel is the text a control shows, or the empty string when it shows
// none.
//
// The one rule worth stating is the one that is not about layout: a password's
// value is masked rather than printed. Every user agent does it, and a page that
// printed the string would be a page that leaked it — the markup is untrusted
// and the PDF is not private, and a value that was hidden on the screen it came
// from must not become plain text on paper.
func controlLabel(n *html.Node, kind controlKind) string {
	switch kind {
	case controlField:
		value, _ := n.Attr("value")
		if inputTypeOf(n) == "password" {
			return strings.Repeat("•", countRunes(value))
		}
		return value

	case controlButton:
		if !strings.EqualFold(n.Name, "input") {
			// A <button>'s label is its children, which are ordinary markup and
			// are laid out as such.
			return ""
		}
		if value, ok := n.Attr("value"); ok {
			return value
		}
		// HTML leaves the default label to the user agent, and these are the
		// two every one of them uses.
		switch inputTypeOf(n) {
		case "submit":
			return "Submit"
		case "reset":
			return "Reset"
		}
		return ""
	}
	return ""
}

// countRunes is the length of a masked value, in characters rather than bytes,
// so that a multi-byte password is not masked to three times its length.
func countRunes(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// maxLabelRunes bounds a synthesised label.
//
// The value attribute is untrusted and is bounded only by the document, which is
// megabytes. Every other text in the document is laid out at the length it has,
// so this is not a general bound on text — it is a bound on what one *control*
// contributes, and a control showing more than this is showing a value nobody
// wrote to be read.
const maxLabelRunes = 4096

// controlContent gives a control the text it shows, as an ordinary inline text
// box.
//
// It goes through collapseWhitespace and transformText exactly as a text node
// does, because it is text in the document and inherits every rule about text.
// A second path would be a second set of answers.
func (b *boxBuilder) controlContent(box *Box, n *html.Node, cs style.ComputedStyle,
	fontSize style.Unit) {

	if box.Control == nil {
		return
	}
	label := controlLabel(n, box.Control.Kind)
	if label == "" {
		return
	}
	if countRunes(label) > maxLabelRunes {
		b.rec.ReportDetail(Finding{
			Rule:   RuleLimit,
			Source: AtHTML(n.Offset),
			Message: "this control's value is longer than the " + strconv.Itoa(maxLabelRunes) +
				" characters this engine will draw in one; it was cut",
			Path: PathOf(n),
		})
		label = truncateRunes(label, maxLabelRunes)
	}
	text := collapseWhitespace(label, cs["white-space-collapse"])
	text, b.afterWord = transformText(text, transformOf(cs["text-transform"]), b.afterWord)
	if text == "" {
		return
	}
	if !b.room(n) {
		return
	}
	box.Children = append(box.Children, &Box{
		Outer: OuterInline, Inner: InnerText,
		Style: cs, Text: text, FontSize: fontSize, Parent: box,
	})
}

func truncateRunes(s string, n int) string {
	i := 0
	for at := range s {
		if i == n {
			return s[:at]
		}
		i++
	}
	return s
}

// selectAncestor is the <select> a node's boxes belong to, or nil.
//
// The walk stops at the first element that is not a select or one of the two it
// may contain, which is what keeps this cheap enough to ask about every child in
// the document: for all but a handful of elements it is one comparison. It is
// bounded for the same reason — HTML nests a select at most two deep, and the
// optional-end-tag rules in the html package close an <optgroup> when another
// opens, so the chain cannot be built up from markup.
func selectAncestor(n *html.Node) *html.Node {
	for cur := n; cur != nil && cur.Type == html.ElementNode; cur = cur.Parent {
		switch strings.ToLower(cur.Name) {
		case "select":
			return cur
		case "optgroup", "option":
			continue
		}
		return nil
	}
	return nil
}

// controlSkipsChild reports whether a child of a select — or of an optgroup
// inside one — generates no box.
//
// A drop-down shows the option it has selected and nothing else, which is what a
// browser puts on screen and on paper. A list box shows its options, and a
// select's other children — the white space between the tags, most often — are
// not rendered by either.
//
// What is left out is reported by reportApproximation rather than dropped in
// silence: a page showing one of six options is a page short of five, and the
// document says so even if the paper cannot.
func controlSkipsChild(parent *Box, child *html.Node) bool {
	if parent.Element == nil {
		return false
	}
	sel := selectAncestor(parent.Element)
	if sel == nil {
		return false
	}
	if strings.EqualFold(parent.Element.Name, "option") {
		// Inside an option, its label is ordinary markup.
		return false
	}
	if !selectIsDropDown(sel) {
		return !isOptionLike(child)
	}
	chosen := chosenOption(sel)
	if chosen == nil {
		// A drop-down with no options shows nothing at all.
		return true
	}
	return !containsOrIs(child, chosen)
}

// isOptionLike reports whether a node is one of the two elements a select
// renders.
func isOptionLike(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	name := strings.ToLower(n.Name)
	return name == "option" || name == "optgroup"
}

// containsOrIs reports whether target is n or is inside it, which is what an
// <optgroup> holding the chosen option needs.
func containsOrIs(n, target *html.Node) bool {
	if target == nil {
		return false
	}
	for cur := target; cur != nil; cur = cur.Parent {
		if cur == n {
			return true
		}
	}
	return false
}

// controlIntrinsicWidth is a control's auto width, as a content width.
//
// Zero and false where the control has none, which is what makes a <button>
// shrink to fit its label and a checkbox take the size the stylesheet gave it.
func (l *layouter) controlIntrinsicWidth(b *Box) (style.Unit, bool) {
	if b == nil || b.Control == nil || b.Control.Chars <= 0 {
		return 0, false
	}
	zero, ok := l.zeroAdvance(b)
	if !ok || zero <= 0 {
		// No face has been chosen, or it states no advance for "0". Half an em
		// per character is the same stand-in CSS Values gives "ex" for a face
		// with no x-height, and for the same reason: a control of no width at
		// all is the one answer that is silently wrong.
		l.ensureFontSize(b)
		zero = b.FontSize.Div(2)
	}
	return zero.Mul(float64(b.Control.Chars)), true
}

// controlIntrinsicHeight is a control's auto height, as a content height.
//
// It is rows line boxes for a textarea, one for a text field, and as many as a
// list box shows. A control with more content than that scrolls in a browser and
// is clipped here, which is the same page: the overflow property the user-agent
// stylesheet gives it is what cuts it.
func (l *layouter) controlIntrinsicHeight(b *Box) (style.Unit, bool) {
	if b == nil || b.Control == nil || b.Control.Lines <= 0 {
		return 0, false
	}
	return l.lineHeight(b).Mul(float64(b.Control.Lines)), true
}
