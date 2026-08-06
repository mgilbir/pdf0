package style

// Properties the registry accepts and nothing acts on.
//
// # Why this file exists
//
// The registry in property.go is the engine's statement about what it
// understands. A property in it is parsed, cascaded, inherited and resolved —
// and a declaration naming it is *not* reported, precisely because being in the
// registry is what "supported" means.
//
// That statement was wrong for sixteen properties at once, and the way it went
// wrong is worth recording because nothing in the design caught it. Adding a
// property to the registry is the first step of implementing one, and it is the
// step that makes the guardrail go quiet. Whoever adds the entry intends to
// write the code that reads it; if they stop there, the engine claims the
// property, silently ignores it, and reports nothing. "text-align: center"
// produced a flush-left heading with a clean bill of health for as long as the
// engine has existed.
//
// So an entry here is a promise that is not yet kept, and it restores the
// finding that the registry entry suppressed. The value is still cascaded, so
// inheritance and the computed value are right and the day someone implements
// the property there is nothing to undo — the entry is deleted and the report
// stops.
//
// # Why the message is per property
//
// "not implemented" tells an author nothing about what they will see. What they
// need is the consequence: whether the page will be laid out as though the
// declaration were absent, and what that looks like. Each string below finishes
// the sentence "the property was not applied, so ...".
var unimplementedProperties = map[string]string{
	"direction": "the text is laid out left to right whatever the value says, " +
		"so a right-to-left document is set in the wrong order",
	"unicode-bidi": "no bidirectional reordering is done, so mixed-direction " +
		"text is set in the order the characters appear rather than the order they read",
	"opacity": "the box is painted fully opaque, so anything it was meant to " +
		"show through it is hidden",

	// The rest are being implemented. Each entry goes when its property does.
	"text-decoration-line": "no underline, overline or line-through is drawn",
	"text-decoration-color": "no decoration is drawn, so the colour it would " +
		"have been drawn in does not arise",
	"text-transform": "the text is set with the case the document has, so " +
		"\"uppercase\" leaves lower-case letters lower case",
	"box-sizing": "a declared width is the content width whatever the value " +
		"says, so a box asking for border-box comes out wider by its padding and border",
	"visibility":  "the box is painted, so \"hidden\" shows",
	"text-indent": "the first line starts at the same place as the rest",
	"letter-spacing": "the characters are set at the face's own advances, so " +
		"the run is narrower or wider than asked for",
	"word-spacing": "the spaces are set at the face's own advance",
}

// readByConstruction lists properties whose names are built rather than written.
//
// The guard in unimplemented_test.go looks for each registered property as a
// literal in the source, which cannot see "border-" + side + "-width". Every
// entry here is a property that is genuinely read, by code that assembles its
// name from a side or an edge, and each one is checked by naming the site.
//
// It is a short list on purpose. A property that is neither found as a literal
// nor listed here is one the engine has quietly stopped applying, and that is
// what the guard is for.
//
// The value is the literal fragment the name is assembled from, and the guard
// requires *that* to appear in the source. Without it this map would be an
// unchecked way to wave anything through — which it was, until a planted entry
// for "text-indent" went unnoticed. The padding and margin edges were in here
// too and did not belong: they are read by their full names.
var readByConstruction = map[string]string{
	// render/layout.go borderWidths reads "border-" + side + "-width" and
	// "-style"; render/paint.go paintBorders reads "border-" + edge + "-color".
	"border-top-width": "border-", "border-right-width": "border-",
	"border-bottom-width": "border-", "border-left-width": "border-",
	"border-top-style": "border-", "border-right-style": "border-",
	"border-bottom-style": "border-", "border-left-style": "border-",
	"border-top-color": "border-", "border-right-color": "border-",
	"border-bottom-color": "border-", "border-left-color": "border-",
}

// unimplementedReason returns why a registered property does nothing, if so.
func unimplementedReason(name string) (string, bool) {
	reason, ok := unimplementedProperties[name]
	return reason, ok
}
