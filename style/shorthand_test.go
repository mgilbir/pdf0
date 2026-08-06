package style

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/css"
)

// The shorthands whose parts are told apart by type rather than by position.
//
// Every test here uses values that could only land where they do if the part
// was identified by *what it is*. "border: red solid 5px" written backwards
// still has to produce the same three longhands, which a positional reader
// cannot manage.

// expandOf runs a declaration through the cascade and returns one element's
// computed values.
func expandOf(t *testing.T, decl string) ComputedStyle {
	t.Helper()
	doc := parseDoc(t, `<p id="t">x</p>`)
	got := Apply(doc, []Sheet{author(t, "#t { "+decl+" }")})
	return got.Styles[elementFor(t, doc, "#t")]
}

// TestBorderShorthandIdentifiesByType pins that the three parts may be written
// in any order. A positional reader gets every one of these wrong.
func TestBorderShorthandIdentifiesByType(t *testing.T) {
	for _, decl := range []string{
		"border: 5px solid red",
		"border: solid 5px red",
		"border: red 5px solid",
		"border: solid red 5px",
	} {
		cs := expandOf(t, decl)
		for _, side := range []string{"top", "right", "bottom", "left"} {
			if got := cs["border-"+side+"-width"]; got != "5px" {
				t.Errorf("%q gave border-%s-width %q, want 5px", decl, side, got)
			}
			if got := cs["border-"+side+"-style"]; got != "solid" {
				t.Errorf("%q gave border-%s-style %q, want solid", decl, side, got)
			}
			if got := cs["border-"+side+"-color"]; got != "red" {
				t.Errorf("%q gave border-%s-color %q, want red", decl, side, got)
			}
		}
	}
}

// TestBorderShorthandResetsWhatItOmits pins the rule that makes a shorthand a
// shorthand, and the one most often got wrong.
//
// "border: solid" does not only set the style. It sets the width and colour to
// their initial values too, so the 5px written before it is gone. An expander
// returning only the parts it saw would leave a five-pixel border, which is a
// page the stylesheet does not explain.
func TestBorderShorthandResetsWhatItOmits(t *testing.T) {
	cs := expandOf(t, "border-width: 5px; border: solid")
	if got := cs["border-top-width"]; got != "medium" {
		t.Errorf("border-top-width is %q; the shorthand resets it to medium", got)
	}
	if got := cs["border-top-style"]; got != "solid" {
		t.Errorf("border-top-style is %q", got)
	}

	// And the other way round: a longhand after the shorthand wins, which only
	// works because the shorthand expanded into competing declarations.
	cs = expandOf(t, "border: 5px solid red; border-top-width: 9px")
	if got := cs["border-top-width"]; got != "9px" {
		t.Errorf("border-top-width is %q, want the later longhand's 9px", got)
	}
	if got := cs["border-right-width"]; got != "5px" {
		t.Errorf("border-right-width is %q; only the top was overridden", got)
	}
}

// TestPerSideBorderShorthand pins that "border-top" sets three longhands on one
// side and leaves the others alone.
func TestPerSideBorderShorthand(t *testing.T) {
	cs := expandOf(t, "border-left: 3px dashed blue")
	if cs["border-left-width"] != "3px" || cs["border-left-style"] != "dashed" ||
		cs["border-left-color"] != "blue" {
		t.Errorf("border-left gave %q %q %q",
			cs["border-left-width"], cs["border-left-style"], cs["border-left-color"])
	}
	// The other sides keep their initial values.
	if cs["border-top-style"] != "none" {
		t.Errorf("border-top-style is %q; border-left must not touch it", cs["border-top-style"])
	}
}

// TestBorderNoneIsAStyleNotAWidth pins the ambiguity a single keyword set would
// create. "none" is a style and "medium" is a width, and reading "border: none"
// as a width would leave the style at solid.
func TestBorderNoneIsAStyleNotAWidth(t *testing.T) {
	// A style is set first, so only a shorthand that actually read "none" as a
	// style can produce it. Asserting on "border: none" alone proves nothing:
	// border-*-style's *initial* value is already none, so a shorthand that
	// rejected the declaration outright would give the same answer — which is
	// what the first version of this test did, and a planted defect caught it.
	cs := expandOf(t, "border-style: dotted; border: none")
	if got := cs["border-top-style"]; got != "none" {
		t.Errorf("border-top-style is %q, want none", got)
	}
	cs = expandOf(t, "border: medium")
	if got := cs["border-top-width"]; got != "medium" {
		t.Errorf("border-top-width is %q, want medium", got)
	}
	if got := cs["border-top-style"]; got != "none" {
		t.Errorf("border-top-style is %q; 'medium' is a width and says nothing "+
			"about the style", got)
	}
}

// TestInvalidBorderSetsNothing pins that half a shorthand is not applied. A
// declaration this engine cannot read leaves every longhand where it was, rather
// than applying the parts that happened to parse.
func TestInvalidBorderSetsNothing(t *testing.T) {
	cs := expandOf(t, "border-style: dotted; border: 5px nonsense red")
	if got := cs["border-top-style"]; got != "dotted" {
		t.Errorf("border-top-style is %q; an invalid shorthand changes nothing", got)
	}
	if got := cs["border-top-width"]; got != "medium" {
		t.Errorf("border-top-width is %q; the invalid shorthand set it anyway", got)
	}
}

// TestBackgroundShorthand pins the colour, and the reset that comes with it.
func TestBackgroundShorthand(t *testing.T) {
	if got := expandOf(t, "background: red")["background-color"]; got != "red" {
		t.Errorf("background-color is %q, want red", got)
	}
	// The shorthand controls the colour, so one that does not mention it resets
	// it — a background image does not sit on the colour set by an earlier rule.
	cs := expandOf(t, "background-color: red; background: none")
	if got := cs["background-color"]; got != "transparent" {
		t.Errorf("background-color is %q; the shorthand resets it to transparent", got)
	}
}

// TestBackgroundShorthandExpandsEveryPart pins that the shorthand reaches all
// eight longhands, whatever order the parts are written in, and that the two
// <box> values are the origin and then the clip.
func TestBackgroundShorthandExpandsEveryPart(t *testing.T) {
	cs := expandOf(t, "background: no-repeat fixed center / cover url(p.png) "+
		"content-box padding-box red")
	for _, want := range []struct{ property, value string }{
		{"background-color", "red"},
		{"background-image", "url(p.png)"},
		{"background-repeat", "no-repeat"},
		{"background-attachment", "fixed"},
		{"background-position", "center"},
		{"background-size", "cover"},
		{"background-origin", "content-box"},
		{"background-clip", "padding-box"},
	} {
		if got := cs[want.property]; got != want.value {
			t.Errorf("%s is %q, want %q", want.property, got, want.value)
		}
	}

	// One <box> sets both, which is the line of the grammar that surprises: the
	// two longhands' *initial* values differ, so a single keyword is not the same
	// as leaving it out.
	one := expandOf(t, "background: url(p.png) content-box")
	if one["background-origin"] != "content-box" || one["background-clip"] != "content-box" {
		t.Errorf("one <box> value gave origin=%q clip=%q; it sets both",
			one["background-origin"], one["background-clip"])
	}

	// And the reset: a shorthand that names only a colour puts every other
	// longhand back to its initial value, whatever an earlier rule set.
	reset := expandOf(t, "background: url(p.png) no-repeat right bottom / 50% "+
		"border-box fixed; background: red")
	for _, want := range []struct{ property, value string }{
		{"background-color", "red"},
		{"background-image", "none"},
		{"background-repeat", "repeat"},
		{"background-attachment", "scroll"},
		{"background-position", "0% 0%"},
		{"background-size", "auto"},
		{"background-origin", "padding-box"},
		{"background-clip", "border-box"},
	} {
		if got := reset[want.property]; got != want.value {
			t.Errorf("after a reset %s is %q, want %q", want.property, got, want.value)
		}
	}
}

// TestBackgroundShorthandLayers pins the comma-separated form, and the rule that
// only the last layer may carry a colour.
func TestBackgroundShorthandLayers(t *testing.T) {
	cs := expandOf(t, "background: url(a.png) no-repeat, url(b.png) repeat-x blue")
	if got := cs["background-image"]; got != "url(a.png),url(b.png)" {
		t.Errorf("background-image is %q", got)
	}
	if got := cs["background-repeat"]; got != "no-repeat,repeat-x" {
		t.Errorf("background-repeat is %q", got)
	}
	if got := cs["background-color"]; got != "blue" {
		t.Errorf("background-color is %q, want blue", got)
	}

	// A colour in a layer that is not the last makes the whole declaration
	// invalid, which sets nothing rather than setting the parts that parsed.
	invalid := expandOf(t,
		"background-color: green; background: red url(a.png), url(b.png)")
	if v := invalid["background-color"]; v != "green" {
		t.Errorf("background-color is %q; an invalid shorthand changes nothing", v)
	}
	if v := invalid["background-image"]; v != "none" {
		t.Errorf("background-image is %q; an invalid shorthand changes nothing", v)
	}
}

// TestFontShorthand pins the one shorthand that is positional at the end: the
// size, an optional line-height after a slash, then the family.
func TestFontShorthand(t *testing.T) {
	cs := expandOf(t, `font: italic bold 12px/1.5 "Noto Sans", serif`)
	if got := cs["font-style"]; got != "italic" {
		t.Errorf("font-style is %q", got)
	}
	if got := cs["font-weight"]; got != "bold" {
		t.Errorf("font-weight is %q", got)
	}
	if got := cs["font-size"]; got != "12px" {
		t.Errorf("font-size is %q", got)
	}
	if got := cs["line-height"]; got != "1.5" {
		t.Errorf("line-height is %q", got)
	}
	if !strings.Contains(cs["font-family"], "Noto Sans") ||
		!strings.Contains(cs["font-family"], "serif") {
		t.Errorf("font-family is %q, want both families", cs["font-family"])
	}

	// Without a line-height the shorthand still resets it, which is what makes
	// "font: 12px serif" undo an inherited one.
	cs = expandOf(t, "line-height: 3; font: 12px serif")
	if got := cs["line-height"]; got != "normal" {
		t.Errorf("line-height is %q; the shorthand resets it", got)
	}
}

// TestFontShorthandNeedsSizeAndFamily pins that the two required parts are
// required. A value missing either is not a font shorthand and sets nothing.
func TestFontShorthandNeedsSizeAndFamily(t *testing.T) {
	for _, decl := range []string{"font: bold", "font: 12px", "font: italic bold"} {
		cs := expandOf(t, "font-size: 30px; "+decl)
		if got := cs["font-size"]; got != "30px" {
			t.Errorf("%q changed font-size to %q; it is not a valid shorthand", decl, got)
		}
	}
}

// TestListStyleShorthand pins the type and the position, and that "none" is read
// as the type — which is what an author means by "list-style: none".
func TestListStyleShorthand(t *testing.T) {
	cs := expandOf(t, "list-style: square inside")
	if cs["list-style-type"] != "square" || cs["list-style-position"] != "inside" {
		t.Errorf("got %q %q", cs["list-style-type"], cs["list-style-position"])
	}
	// Written the other way round.
	cs = expandOf(t, "list-style: inside square")
	if cs["list-style-type"] != "square" || cs["list-style-position"] != "inside" {
		t.Errorf("reversed gave %q %q", cs["list-style-type"], cs["list-style-position"])
	}
	if got := expandOf(t, "list-style: none")["list-style-type"]; got != "none" {
		t.Errorf("list-style:none gave type %q, want none", got)
	}
}

// TestTextDecorationShorthand pins the line and the colour.
func TestTextDecorationShorthand(t *testing.T) {
	cs := expandOf(t, "text-decoration: underline red")
	if cs["text-decoration-line"] != "underline" {
		t.Errorf("line is %q", cs["text-decoration-line"])
	}
	if cs["text-decoration-color"] != "red" {
		t.Errorf("colour is %q", cs["text-decoration-color"])
	}
	// And it resets: "text-decoration: none" clears an inherited underline's
	// colour back to currentcolor as well as the line.
	cs = expandOf(t, "text-decoration: underline red; text-decoration: none")
	if cs["text-decoration-line"] != "none" || cs["text-decoration-color"] != "currentcolor" {
		t.Errorf("the reset gave %q %q",
			cs["text-decoration-line"], cs["text-decoration-color"])
	}
}

// TestTextDecorationTakesSeveralLines pins that the line part is a set.
//
// "underline overline" is one declaration asking for two lines, and the value
// has to survive as both — an expander that kept the first would produce a page
// with one line on it and a finding blaming the second, which describes the
// engine's own reading rather than anything unimplemented.
func TestTextDecorationTakesSeveralLines(t *testing.T) {
	cs := expandOf(t, "text-decoration: underline overline")
	if cs["text-decoration-line"] != "underline overline" {
		t.Errorf("line is %q, want %q", cs["text-decoration-line"], "underline overline")
	}
	// In either order, and with a colour among them.
	cs = expandOf(t, "text-decoration: red line-through underline")
	if cs["text-decoration-line"] != "line-through underline" {
		t.Errorf("line is %q, want %q", cs["text-decoration-line"], "line-through underline")
	}
	if cs["text-decoration-color"] != "red" {
		t.Errorf("colour is %q, want red", cs["text-decoration-color"])
	}
}

// TestTextDecorationRefusesAnImpossibleSet pins the two combinations that are
// not values at all.
//
// The assertion is against the *initial* value in a document where an earlier
// declaration set something else, because "none" is text-decoration-line's own
// initial value: a test that only checked for "none" would pass just as happily
// if the declaration had been accepted and read as none.
func TestTextDecorationRefusesAnImpossibleSet(t *testing.T) {
	for _, bad := range []string{
		"text-decoration: none underline",
		"text-decoration: underline none",
		"text-decoration: underline underline",
	} {
		cs := expandOf(t, "text-decoration: overline; "+bad)
		if cs["text-decoration-line"] != "overline" {
			t.Errorf("%q gave %q; an invalid shorthand sets nothing, so the earlier "+
				"declaration stands", bad, cs["text-decoration-line"])
		}
	}
}

// TestShorthandWideKeywordsReachEveryLonghand pins that "border: inherit" sets
// all twelve longhands. The list is declared rather than discovered precisely so
// that this works — there is no value to probe an expander with here.
func TestShorthandWideKeywordsReachEveryLonghand(t *testing.T) {
	doc := parseDoc(t, `<div id="outer"><p id="t">x</p></div>`)
	got := Apply(doc, []Sheet{author(t,
		"#outer { border: 7px solid red } #t { border: inherit }")})
	cs := got.Styles[elementFor(t, doc, "#t")]

	for _, side := range []string{"top", "right", "bottom", "left"} {
		if cs["border-"+side+"-width"] != "7px" {
			t.Errorf("border-%s-width is %q, want the inherited 7px",
				side, cs["border-"+side+"-width"])
		}
		if cs["border-"+side+"-style"] != "solid" {
			t.Errorf("border-%s-style is %q", side, cs["border-"+side+"-style"])
		}
	}
}

// TestShorthandLonghandsMatchWhatTheExpanderProduces is the guard on the list.
//
// It is declared by hand, so it can drift from what the expander actually
// returns — and a longhand missing from it would silently stop taking a
// CSS-wide keyword while working normally otherwise, which is close to
// undiscoverable.
func TestShorthandLonghandsMatchWhatTheExpanderProduces(t *testing.T) {
	samples := map[string]string{
		"margin":          "1px",
		"padding":         "1px",
		"border-width":    "1px",
		"border-style":    "solid",
		"border-color":    "red",
		"overflow":        "hidden",
		"border":          "1px solid red",
		"border-top":      "1px solid red",
		"border-right":    "1px solid red",
		"border-bottom":   "1px solid red",
		"border-left":     "1px solid red",
		"background":      "red",
		"list-style":      "square inside",
		"font":            "12px serif",
		"text-decoration": "underline red",
	}
	if len(samples) != len(shorthands) {
		t.Errorf("%d shorthands are declared and %d have a sample here; every one "+
			"needs checking", len(shorthands), len(samples))
	}
	for name, sample := range samples {
		sh, ok := shorthands[name]
		if !ok {
			t.Errorf("no shorthand named %q", name)
			continue
		}
		vals, _ := css.ParseComponentValues(sample)
		produced, _, ok := sh.expand(vals)
		if !ok {
			t.Errorf("%s: the sample %q does not expand", name, sample)
			continue
		}
		declared := map[string]bool{}
		for _, l := range sh.longhands {
			declared[l] = true
		}
		for l := range produced {
			if !declared[l] {
				t.Errorf("%s produces %q, which is not in its declared longhands", name, l)
			}
		}
		for l := range declared {
			if _, ok := produced[l]; !ok {
				t.Errorf("%s declares %q, which the sample %q did not produce",
					name, l, sample)
			}
		}
		// Every longhand a shorthand controls must be a property the engine has.
		for _, l := range sh.longhands {
			if _, ok := properties[l]; !ok {
				t.Errorf("%s controls %q, which is not in the property registry",
					name, l)
			}
		}
	}
}
