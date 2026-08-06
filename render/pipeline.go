package render

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
	"github.com/mgilbir/pdf0/style"
)

// Running the stages that exist, and translating what each of them says into
// the guardrail vocabulary.
//
// The translation is the part worth attention. The html, css and style packages
// each report in their own terms, and each already distinguishes "the input is
// wrong" from "this engine does not do that" — but a caller should not have to
// learn three vocabularies to find that out. Everything arrives here as a rule
// identifier and a place in the author's input.
//
// This is not the final entry point. Render will be, once there is a layout to
// run and a page to run it onto; what is here is the prefix of it that exists,
// exposed because the stages are worth testing against real documents rather
// than against hand-built trees.

// Stylesheet is one stylesheet with a name to report against.
type Stylesheet struct {
	// Name identifies the sheet in a finding — a filename, usually. It is empty
	// for the document's own <style> content.
	Name string
	// Source is the CSS.
	Source string
}

// Input is a document and the stylesheets to apply to it.
type Input struct {
	// HTML is the document source.
	HTML string
	// CSS is the author's stylesheets, in the order they apply.
	CSS []Stylesheet
	// Policy chooses what each rule does. A nil policy uses the defaults.
	Policy Policy
	// UserCSS is a stylesheet applied on the reader's behalf, which sits
	// between the engine's defaults and the author's. It is separate from CSS
	// because its origin is different, and origin is the strongest term in the
	// cascade.
	UserCSS string
}

// Built is the result of the stages that exist.
type Built struct {
	// Document is the parsed tree, present even when findings were raised.
	Document *html.Node
	// Root is the root of the box tree, nil when the document produces no
	// boxes — which "html { display: none }" legitimately does.
	Root *Box
	// Styles is every element's computed style.
	Styles map[*html.Node]style.ComputedStyle
	// Findings is everything worth telling the caller, ordered deterministically.
	Findings []Finding
	// Failed reports that something fired at Error severity, so a caller that
	// went on to render would be rendering something it was told not to.
	Failed bool
	// Truncated reports that the finding list was cut.
	Truncated bool
}

// Build parses, styles and boxes a document.
func Build(in Input) Built {
	rec := NewRecorder(in.Policy)

	doc, htmlErrs, _ := html.Parse(in.HTML)
	for _, e := range htmlErrs {
		rule := RuleInvalidMarkup
		if e.Unsupported {
			rule = RuleUnsupportedElement
		}
		rec.Report(rule, AtHTML(e.Offset), e.Message)
	}

	sheets := make([]style.Sheet, 0, len(in.CSS)+2)
	sheets = append(sheets, parseSheet(rec, style.OriginUserAgent, "user agent", UserAgentCSS))
	if in.UserCSS != "" {
		sheets = append(sheets, parseSheet(rec, style.OriginUser, "user", in.UserCSS))
	}
	// A <style> element in the document is an author stylesheet like any other,
	// and comes before the ones the caller passed only because it was written
	// first — order is the cascade's last tie-break and it has to be the order
	// the author would expect.
	for _, s := range documentStylesheets(doc) {
		sheets = append(sheets, parseSheet(rec, style.OriginAuthor, "", s))
	}
	for _, s := range in.CSS {
		sheets = append(sheets, parseSheet(rec, style.OriginAuthor, s.Name, s.Source))
	}

	styled := style.Apply(doc, sheets)
	for _, f := range styled.Findings {
		rec.ReportDetail(Finding{
			Rule:     ruleForStyleFinding(f),
			Source:   AtCSS(f.Offset),
			Message:  f.Message,
			Property: f.Property,
		})
	}
	if styled.Incomplete {
		rec.Report(RuleLimit, NoSource,
			"selector matching stopped early, so some rules did not apply")
	}

	root := BuildBoxes(doc, styled.Styles, rec)
	reportUnsupportedDisplays(doc, styled.Styles, rec)

	return Built{
		Document:  doc,
		Root:      root,
		Styles:    styled.Styles,
		Findings:  rec.Findings(),
		Failed:    rec.Failed(),
		Truncated: rec.Truncated(),
	}
}

// parseSheet reads one stylesheet, reporting what it could not read.
func parseSheet(rec *Recorder, origin style.Origin, name, src string) style.Sheet {
	rules, errs := css.ParseStylesheet(src)
	for _, e := range errs {
		rec.ReportDetail(Finding{
			Rule:    RuleInvalidCSS,
			Source:  Source{HTMLOffset: -1, CSSOffset: e.Offset, Sheet: name},
			Message: e.Message,
		})
	}
	return style.Sheet{Origin: origin, Rules: rules}
}

// documentStylesheets collects the text of every <style> element.
//
// A <link rel=stylesheet> is *not* followed. Nothing here resolves a URL — §4.1
// makes that the caller's resolver's job and forbids this engine a network —
// and the blocked reference is reported where the element is dropped rather than
// here.
func documentStylesheets(doc *html.Node) []string {
	var out []string
	doc.Walk(func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Name == "style" {
			if text := n.TextContent(); text != "" {
				out = append(out, text)
			}
			return false
		}
		return true
	})
	return out
}

// ruleForStyleFinding maps the styling stage's report onto a rule.
//
// The stage knows three things about a finding — whether it is unsupported,
// whether it names a property, and whether that name is an at-rule — and those
// three answer which rule it is without the stage having to know the catalogue.
func ruleForStyleFinding(f style.Finding) Rule {
	if !f.Unsupported {
		return RuleInvalidCSS
	}
	switch {
	case len(f.Property) > 0 && f.Property[0] == '@':
		return RuleUnsupportedAtRule
	case f.Property != "":
		return RuleUnsupportedProperty
	}
	// Unsupported with no property named is a selector: the styling stage
	// reports those straight from the selector parser.
	return RuleUnsupportedSelector
}

// reportUnsupportedDisplays names the display values the box tree recognised
// and could not honour.
//
// "display: contents" is the one that matters. It asks for an element's own box
// to be replaced by its children, and the closest available answer — treating it
// as inline — is wrong in a way that shows: the box takes part in layout when
// the author asked for it not to. That is precisely a value this engine reads
// and does not implement, so it is reported rather than quietly approximated.
func reportUnsupportedDisplays(doc *html.Node, styles map[*html.Node]style.ComputedStyle, rec *Recorder) {
	doc.Walk(func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		cs, ok := styles[n]
		if !ok {
			return true
		}
		if cs["display"] == "contents" {
			rec.ReportDetail(Finding{
				Rule:     RuleUnsupportedValue,
				Source:   AtHTML(n.Offset),
				Message:  "\"display: contents\" is not implemented; the element was laid out as an inline box",
				Path:     PathOf(n),
				Property: "display",
			})
		}
		return true
	})
}

// PathOf renders an element's position in the document, so a finding about a
// stylesheet shared by many elements can still say which one it is about.
//
// It is a readable path rather than a selector that would round-trip: it names
// the element chain with the identifiers and classes that distinguish it, which
// is what someone reading a report needs.
func PathOf(n *html.Node) string {
	if n == nil || n.Type != html.ElementNode {
		return ""
	}
	var parts []string
	for cur := n; cur != nil && cur.Type == html.ElementNode; cur = cur.Parent {
		part := cur.Name
		if id, ok := cur.Attr("id"); ok && id != "" {
			part += "#" + id
		} else if class, ok := cur.Attr("class"); ok {
			if fields := strings.Fields(class); len(fields) > 0 {
				part += "." + fields[0]
			}
		}
		parts = append(parts, part)
	}
	// Built innermost first; a path reads outermost first.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, " > ")
}
