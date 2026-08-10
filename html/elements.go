package html

// The element tables: what this engine knows, what it refuses, and the few
// places where HTML lets an end tag be left out.
//
// These are data rather than code on purpose. The subset boundary is the design
// decision this package exists to enforce, and a boundary spread through
// conditionals is one nobody can read off.

// knownElements is everything the layout engine can do something with.
//
// An element outside this set is refused rather than treated as an anonymous
// box. Rendering an unknown tag as a generic inline is what a browser does, and
// it is the wrong answer here: it produces a page that looks nearly right, so
// nothing signals that a <fancy-callout> the author expected to matter was laid
// out as though it were a <span>.
var knownElements = map[string]bool{
	// Document structure.
	"html": true, "head": true, "body": true,
	"title": true, "meta": true, "link": true, "base": true, "style": true,

	// Sections and grouping.
	"address": true, "article": true, "aside": true, "blockquote": true,
	"div": true, "dl": true, "dt": true, "dd": true, "figcaption": true,
	"figure": true, "footer": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "header": true, "hgroup": true,
	"hr": true, "li": true, "main": true, "nav": true, "ol": true, "p": true,
	"pre": true, "section": true, "ul": true,

	// Tables.
	"table": true, "caption": true, "colgroup": true, "col": true,
	"thead": true, "tbody": true, "tfoot": true, "tr": true, "td": true,
	"th": true,

	// Text-level semantics.
	"a": true, "abbr": true, "b": true, "bdi": true, "bdo": true, "br": true,
	"cite": true, "code": true, "data": true, "del": true, "dfn": true,
	"em": true, "i": true, "ins": true, "kbd": true, "mark": true, "q": true,
	"rp": true, "rt": true, "ruby": true, "s": true, "samp": true,
	"small": true, "span": true, "strong": true, "sub": true, "sup": true,
	"time": true, "u": true, "var": true, "wbr": true,

	// Images.
	"img": true, "picture": true, "source": true,

	// Forms, as static boxes.
	//
	// These are here for what they *are* on a page rather than for what they do
	// on a screen. The reason they used to be refused — "form controls are not
	// interactive here" — is about interactivity, and interactivity is not what
	// a printed page has: a <textarea> has an intrinsic size from its cols and
	// rows, it has text in it, and a browser asked to print one puts that text
	// on the paper inside a box. Refusing the element meant a document lost
	// content and said only that an element had been dropped, which is the
	// class of fault this engine reports everywhere else rather than commits.
	//
	// What stays refused is the interaction itself, and it is not a boundary
	// this moves: nothing is submitted, nothing is typed into, no value a reader
	// would have entered is invented, and no PDF form field is produced. See
	// render/control.go for what each control is drawn as and for the findings
	// that name the places where a static box is an approximation of a widget.
	"form": true, "label": true, "fieldset": true, "legend": true,
	"input": true, "button": true, "select": true, "option": true,
	"optgroup": true, "textarea": true,

	// <object>, for its fallback content. Nothing is embedded — see
	// resolveObjects — but HTML says an object whose data cannot be used is
	// represented by its children, and those children are ordinary markup this
	// engine can lay out. <param> comes with it and generates no box.
	"object": true, "param": true,
}

// voidElements have no content and no end tag. Writing one — "</br>" — is an
// error rather than something to be ignored.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// rawTextElements have content that is not markup at all: it runs to the
// matching end tag, and neither "<" nor "&" means anything inside.
var rawTextElements = map[string]bool{
	"style": true, "script": true,
}

// rcdataElements have content with no markup but with character references,
// so "&amp;" in a <title> is an ampersand.
var rcdataElements = map[string]bool{
	"title": true, "textarea": true,
}

// droppedElements are the ones refused for what they *do* rather than for being
// unknown, and each has its own reason.
//
// The first three are §4.1 of the rendering proposal: they are the entirety of
// the code-execution and remote-content surface. A renderer that ignored them
// silently would still be one that had read them, and an author who embedded a
// <script> expecting it to be inert deserves to be told it was thrown away
// rather than left to assume it ran.
//
// The form controls used to be here, under "form controls are not interactive
// here". That reason confused interactivity with layout: a control has a size
// and content on a printed page whether or not anything can be clicked, and
// dropping it lost the content. They are in knownElements now, and the
// interactivity boundary is stated there rather than deleted.
var droppedElements = map[string]string{
	"script":   "scripts are never run, and never will be",
	"iframe":   "a nested browsing context would need a network and a renderer for it",
	"embed":    "an embedded plugin would need a plugin",
	"applet":   "applets would need a virtual machine",
	"canvas":   "a canvas is drawn by script, which is never run",
	"audio":    "a page laid out once cannot play anything",
	"video":    "a page laid out once cannot play anything",
	"details":  "a disclosure widget needs somewhere to click",
	"summary":  "a disclosure widget needs somewhere to click",
	"dialog":   "a dialog is opened by script, which is never run",
	"template": "a template's content is instantiated by script",
	"slot":     "a shadow tree needs scripting",
	"noscript": "there is no script for this to be an alternative to",
	"map":      "an image map needs somewhere to click",
	"area":     "an image map needs somewhere to click",
	"marquee":  "a page laid out once cannot animate",
	"progress": "a progress bar reflects a state that does not change here",
	"meter":    "a meter reflects a state that does not change here",
	"output":   "an output is computed by script",
}

// closedByStartTag says which open element an incoming start tag ends.
//
// This is HTML's *optional end tags* (§13.1.2.4), not error recovery. Leaving
// out "</li>" is correct HTML, and every template in the world does it, so
// refusing it would refuse the input this engine exists to read. The rules are
// closed and few, which is what makes them safe to implement without taking on
// the rest of the recovery algorithm.
//
// Keyed by the open element; the value is the set of start tags that close it.
var closedByStartTag = map[string]map[string]bool{
	"p": setOf(
		"address", "article", "aside", "blockquote", "details", "div", "dl",
		"fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3",
		"h4", "h5", "h6", "header", "hgroup", "hr", "main", "menu", "nav", "ol",
		"p", "pre", "section", "table", "ul",
	),
	"li":       setOf("li"),
	"dt":       setOf("dt", "dd"),
	"dd":       setOf("dt", "dd"),
	"rt":       setOf("rt", "rp"),
	"rp":       setOf("rt", "rp"),
	"option":   setOf("option", "optgroup"),
	"optgroup": setOf("optgroup"),
	"thead":    setOf("tbody", "tfoot"),
	"tbody":    setOf("tbody", "tfoot"),
	"tfoot":    setOf("tbody"),
	"tr":       setOf("tr", "tbody", "tfoot", "thead"),
	"td":       setOf("td", "th", "tr", "tbody", "tfoot", "thead"),
	"th":       setOf("td", "th", "tr", "tbody", "tfoot", "thead"),
	"caption":  setOf("colgroup", "thead", "tbody", "tfoot", "tr"),
	"colgroup": setOf("thead", "tbody", "tfoot", "tr"),
}

// closedByParentEnd is the other half of optional end tags: these close when
// their parent does, without an end tag of their own.
var closedByParentEnd = setOf(
	"p", "li", "dt", "dd", "rt", "rp", "option", "optgroup",
	"thead", "tbody", "tfoot", "tr", "td", "th", "caption", "colgroup",
)

// headElements belong in <head> when no <body> has begun.
//
// <style> and <link> are deliberately absent: both are legal in the body, and
// moving one there into the head would change the order stylesheets apply in,
// which changes which declaration wins.
var headElements = setOf("title", "meta", "base")

// metadataElements produce nothing visible. They may appear in the body without
// starting one, so that "<meta charset=utf-8><p>x</p>" does not put the <meta>
// in the body and the <p> after it.
var metadataElements = setOf("title", "meta", "base", "link", "style")

func setOf(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}
