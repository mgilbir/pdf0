package style

import (
	"sort"
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
)

// The cascade: deciding which declaration wins when several apply, and what an
// element's value is when none does.
//
// This is the part of styling that fails *silently* when it is wrong. A
// mismatched selector shows up as a rule that does nothing, which an author
// notices; a cascade that orders two declarations wrongly produces a page where
// the wrong one won, which looks like a design decision. So the ordering below
// follows CSS Cascade Level 4 §6 exactly, and the tests assert which declaration
// won rather than that a value was produced.

// Origin is where a stylesheet came from. It is the first and strongest term in
// the cascade, ahead of specificity — an author's ordinary rule beats a user
// agent's however specific the latter is.
type Origin uint8

const (
	// OriginUserAgent is this engine's own default stylesheet: what makes <p> a
	// block and <b> bold. It loses to everything.
	OriginUserAgent Origin = iota
	// OriginUser is a stylesheet the caller supplies on behalf of the reader.
	OriginUser
	// OriginAuthor is the document's own CSS — its <style> elements and the
	// stylesheets it links.
	OriginAuthor
)

// A Sheet is a stylesheet together with where it came from.
type Sheet struct {
	Origin Origin
	Rules  []css.Rule
}

// A Finding is something the styling stage noticed and a caller should hear
// about.
//
// It is the same shape as the css and html packages' Error, and for the same
// reason: an author needs to tell "I wrote this wrongly" from "this engine does
// not do that". The layer that turns these into pdf0.Violation values lands with
// the guardrail framework in phase 3; until then this carries the information so
// that nothing has to be reconstructed later.
type Finding struct {
	// Offset is the byte offset in the stylesheet the finding came from.
	Offset int
	// Message says what happened.
	Message string
	// Unsupported marks correct CSS this engine does not implement — the
	// unsupported-property finding of the proposal's §6.3 — as against a
	// stylesheet that is malformed.
	Unsupported bool
	// Property is the declaration's name, when the finding is about one.
	Property string
}

// maxFindings bounds the report, so a stylesheet full of properties this engine
// does not implement produces a list a person can read.
const maxFindings = 200

// A declaration that matched an element, with everything the cascade sorts on.
type candidate struct {
	property  string
	value     []css.ComponentValue
	important bool
	origin    Origin
	spec      css.Specificity
	// order is the position of the declaration in the whole input, which breaks
	// the remaining ties. Two declarations that are equal in every other term
	// are decided by which was written later, so this has to be a single
	// sequence across all sheets rather than an index within one.
	//
	// It is numbered from zero across the real declarations, which leaves the
	// negative numbers free for the things that are ordered against them
	// without being in a stylesheet at all — see hintOrder.
	order int
	// offset is where it was written, for diagnostics.
	offset int
}

// Styler applies stylesheets to a document.
type Styler struct {
	matcher  *Matcher
	findings []Finding
	// seen suppresses repeat reports of the same unsupported property. A
	// stylesheet using "flex-wrap" forty times is one thing an author needs to
	// be told, not forty.
	seen map[string]bool
}

// ComputedStyle is one element's resolved property values, keyed by property
// name. Every property in the registry is present, so a caller never has to
// distinguish "unset" from "absent".
type ComputedStyle map[string]string

// PseudoKey names one pseudo-element of one element.
type PseudoKey struct {
	Node *html.Node
	// Name is "before", "after" and so on, without the colons.
	Name string
}

// Styled is the result: a computed style for every element of the document.
type Styled struct {
	// Styles maps each element to its computed values.
	Styles map[*html.Node]ComputedStyle

	// Pseudo holds the computed values of the pseudo-elements that any rule
	// selected. An entry exists only where a rule matched, because a
	// pseudo-element that nothing styles generates nothing — unlike a real
	// element, which exists whether or not anything mentions it.
	Pseudo map[PseudoKey]ComputedStyle
	// OwnFontSize and OwnPseudoFontSize mark the elements whose font-size came
	// from a declaration of their own rather than from their parent.
	//
	// Nothing else in the cascade needs this and font-size does, because it is
	// the one property whose computed value is not the value stored here: CSS
	// makes it an absolute length, and what is in Styles is what the author
	// wrote. A consumer resolving "2em" against the parent's size gets the right
	// answer for the element that declared it and the wrong one for every
	// descendant that merely inherited it — twice the parent at each level, so a
	// paragraph four levels down a "font-size: 2em" wrapper is set in 256px.
	//
	// That was not hypothetical. It is what this map was added to stop.
	OwnFontSize       map[*html.Node]bool
	OwnPseudoFontSize map[PseudoKey]bool

	// Findings is everything worth telling the caller, in stylesheet order.
	Findings []Finding
	// Incomplete reports that the selector-matching budget tripped, so some
	// rules did not get the chance to apply. A caller rendering an incomplete
	// result is rendering something other than the stylesheet describes.
	Incomplete bool
}

// Apply computes a style for every element in a document.
func Apply(doc *html.Node, sheets []Sheet) Styled {
	s := &Styler{matcher: NewMatcher(doc), seen: map[string]bool{}}

	// Expand shorthands and drop what the engine does not implement, once for
	// the whole run rather than once per element — the answer does not depend
	// on the element, and a document of ten thousand nodes would otherwise ask
	// the same question ten thousand times.
	rules := s.prepare(sheets)

	out := Styled{
		Styles:            map[*html.Node]ComputedStyle{},
		Pseudo:            map[PseudoKey]ComputedStyle{},
		OwnFontSize:       map[*html.Node]bool{},
		OwnPseudoFontSize: map[PseudoKey]bool{},
	}
	// Document order, so a parent is always computed before its children and
	// inheritance can read the parent's finished values.
	doc.Walk(func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		cs, own := s.computeFor(n, rules, out.Styles, "")
		out.Styles[n] = cs
		if own {
			out.OwnFontSize[n] = true
		}
		for _, name := range pseudoElementNames {
			if !s.anyRuleTargets(rules, n, name) {
				continue
			}
			// A pseudo-element inherits from the element it belongs to, which is
			// why the parent style passed here is that element's own.
			key := PseudoKey{Node: n, Name: name}
			pcs, own := s.computeForPseudo(n, rules, out.Styles[n], name)
			out.Pseudo[key] = pcs
			if own {
				out.OwnPseudoFontSize[key] = true
			}
		}
		return true
	})

	out.Findings = s.findings
	out.Incomplete = s.matcher.Tripped()
	if out.Incomplete {
		s.report(Finding{
			Message: "matching stopped early: some rules did not get the chance " +
				"to apply, so this document is styled less than its stylesheet describes",
		})
		out.Findings = s.findings
	}
	return out
}

// preparedRule is a rule with its selectors parsed and its declarations
// expanded, ready to be matched against every element.
type preparedRule struct {
	selectors []css.Selector
	decls     []preparedDecl
	origin    Origin
}

type preparedDecl struct {
	property  string
	value     []css.ComponentValue
	important bool
	order     int
	offset    int
}

func (s *Styler) prepare(sheets []Sheet) []preparedRule {
	var out []preparedRule
	order := 0

	for _, sheet := range sheets {
		for _, rule := range sheet.Rules {
			if rule.At {
				// At-rules are a stage of their own — @media has to be
				// evaluated against the page it is being laid out for, and
				// @page describes the surface rather than the content. Neither
				// belongs in the cascade, and reporting them here is how their
				// absence stays visible until they arrive.
				s.report(Finding{
					Offset:      rule.Offset,
					Message:     "@" + rule.Name + " is not applied yet",
					Unsupported: true,
					Property:    "@" + rule.Name,
				})
				continue
			}

			sels, errs, ok := css.ParseSelectorList(rule.Prelude)
			for _, e := range errs {
				s.report(Finding{
					Offset:      e.Offset,
					Message:     e.Message,
					Unsupported: e.Unsupported,
				})
			}
			if !ok {
				// An unusable selector list invalidates the rule, which is what
				// the specification requires — and the findings above already
				// said why.
				continue
			}

			decls, _, derrs := css.ParseDeclarationValues(rule.Block)
			for _, e := range derrs {
				s.report(Finding{Offset: e.Offset, Message: e.Message, Unsupported: e.Unsupported})
			}

			prepared := preparedRule{selectors: sels, origin: sheet.Origin}
			for _, d := range decls {
				for _, e := range s.expand(d, sheet.Origin) {
					e.order = order
					order++
					prepared.decls = append(prepared.decls, e)
				}
			}
			if len(prepared.decls) > 0 {
				out = append(out, prepared)
			}
		}
	}
	return out
}

// expand turns one declaration into the longhands it sets, dropping and
// reporting anything the engine does not implement.
func (s *Styler) expand(d css.Declaration, origin Origin) []preparedDecl {
	name := strings.ToLower(d.Name)

	if nonNegative[name] && hasNegativeNumber(d.Value) {
		// A declaration whose value is illegal is not a declaration with a
		// strange value: CSS 2.1 §4.2 says the whole declaration is dropped, and
		// what stands is whatever the cascade would have produced without it.
		//
		// That is why this cannot be done where the value is read. "height: 0;
		// height: -1px" has to compute to zero, and a layout that refuses the
		// negative number sees only the last declaration and falls back to
		// auto — which is a full-height box where the author asked for none.
		// The suite has thirty-five tests of exactly that shape, one per
		// property per unit, and they are what found it.
		//
		// The finding is not marked unsupported. Nothing is missing from the
		// engine here; a stylesheet said something CSS forbids and CSS says
		// what to do about it.
		s.report(Finding{
			Offset: d.Offset,
			Message: "\"" + name + ": " + serialize(d.Value) + "\" is negative, which " +
				name + " does not allow, so the declaration was dropped",
			Property: name,
		})
		return nil
	}

	if _, ok := properties[name]; ok {
		// A registered property that nothing reads is reported here rather than
		// dropped. The value still cascades — inheritance and the computed
		// value are right, and the day the property is implemented there is
		// nothing to undo — but the silence that the registry entry bought is
		// given back. See unimplemented.go for why that silence is the failure
		// mode this guards.
		// Only for a declaration someone wrote. The engine's own default sheet
		// uses several of these — "a { text-decoration: underline }" among them
		// — and reporting those would put a finding on every document ever
		// rendered, including documents with no link in them: this runs when the
		// sheet is parsed, not when a rule matches. That is noise, and noise in
		// the one channel that says what the page is missing is worse than
		// silence, because it is what makes the channel stop being read.
		//
		// The gap is still real for the default sheet. It belongs in the note on
		// the property rather than in every document's findings.
		if reason, missing := unimplementedReason(name); missing &&
			origin != OriginUserAgent && !s.seen[name] {
			s.seen[name] = true
			s.report(Finding{
				Offset: d.Offset,
				Message: "the property \"" + name + "\" is not implemented, so " +
					reason,
				Unsupported: true,
				Property:    name,
			})
		}
		return []preparedDecl{{
			property: name, value: d.Value, important: d.Important, offset: d.Offset,
		}}
	}

	if sh, ok := shorthands[name]; ok {
		// A CSS-wide keyword on a shorthand sets every longhand to it, which
		// the expander below cannot express — it splits on whitespace and would
		// hand "inherit" to the first slot only.
		if kw := wideKeyword(d.Value); kw != "" {
			var out []preparedDecl
			for _, longhand := range shorthandLonghands(name) {
				out = append(out, preparedDecl{
					property: longhand, value: d.Value,
					important: d.Important, offset: d.Offset,
				})
			}
			return out
		}
		parts, unsupported, ok := sh.expand(d.Value)
		for _, part := range unsupported {
			// A part of the shorthand this engine understood and cannot
			// produce. Naming it is the difference between an author learning
			// their background image did not appear and wondering why the page
			// is blank.
			key := name + "\x00" + part
			if !s.seen[key] {
				s.seen[key] = true
				s.report(Finding{
					Offset: d.Offset,
					Message: "\"" + part + "\" in the " + name +
						" shorthand is not implemented, so it was not applied",
					Unsupported: true,
					Property:    name,
				})
			}
		}
		if !ok {
			s.report(Finding{
				Offset:   d.Offset,
				Message:  "\"" + name + ": " + serialize(d.Value) + "\" is not a value this engine can read",
				Property: name,
			})
			return nil
		}
		out := make([]preparedDecl, 0, len(parts))
		for longhand, value := range parts {
			out = append(out, preparedDecl{
				property: longhand, value: value,
				important: d.Important, offset: d.Offset,
			})
		}
		// Map iteration is random, so the order numbers these longhands receive
		// would otherwise differ from run to run.
		//
		// Nothing observable depends on it today, and that is worth writing down
		// rather than leaving as an implied guarantee: two longhands of one
		// shorthand are different properties, so they never compete, and every
		// declaration outside this shorthand is numbered entirely before or
		// entirely after all four. Removing this sort does not change a single
		// computed value, and the determinism test does not fail — which was
		// checked rather than assumed.
		//
		// It stays because the numbers are reproducible with it and arbitrary
		// without it, and the moment anything begins to read them — a finding
		// per longhand, a cache keyed on them — the cost of not having it is a
		// bug that appears one run in ten.
		sort.Slice(out, func(i, j int) bool { return out[i].property < out[j].property })
		return out
	}

	// The unsupported-property finding: a declaration parsed and then not
	// applied. It is on by default and it is the cheapest guardrail in the
	// design — a page where a property was dropped is plausible and wrong.
	if !s.seen[name] {
		s.seen[name] = true
		s.report(Finding{
			Offset:      d.Offset,
			Message:     "the property \"" + name + "\" is not implemented, so it was not applied",
			Unsupported: true,
			Property:    name,
		})
	}
	return nil
}

// nonNegative lists the longhands whose value CSS 2.1 says may not be negative.
//
// Each entry is a property whose definition carries the words "Negative values
// are illegal" or "Negative lengths are not allowed": the sizes of §10.2, §10.4,
// §10.5 and §10.7, the paddings of §8.4 and the border widths of §8.5.1. The
// list is deliberately short and deliberately not "everything that looks like a
// length" — a negative margin, a negative text-indent, a negative letter-spacing
// and a negative word-spacing are all legal and all useful, and dropping one of
// those would break a page that is doing nothing wrong.
//
// The border widths differ from the paddings in what dropping them produces, and
// that is why they cannot be handled where they are read. A padding's initial
// value is zero, so clamping a negative one to zero gives the right answer by
// accident; a border width's initial value is "medium", which is three pixels of
// ink. Layout clamped, so "border-top-width: -1pt" drew no border where CSS asks
// for the initial one — fourteen tests in css/CSS2/borders, one per unit per
// side, and every one of them invisible until inline boxes started painting
// their borders, because the reference draws its two rules on a <span>.
//
// The shorthands are not here. "padding: 1px -2px" is invalid as a whole, and
// catching it needs the shorthand expander rather than a name lookup; what
// happens today is that the negative reaches two longhands, which is a gap this
// records rather than hides.
var nonNegative = map[string]bool{
	"width": true, "height": true,
	"min-width": true, "min-height": true,
	"max-width": true, "max-height": true,
	"padding-top": true, "padding-right": true,
	"padding-bottom": true, "padding-left": true,
	"border-top-width": true, "border-right-width": true,
	"border-bottom-width": true, "border-left-width": true,
}

// hasNegativeNumber reports whether any numeric token in a value is negative.
//
// It reads the tokens rather than parsing a length, because this runs when a
// sheet is prepared and there is no element, no font size and no containing
// block yet. A negative number is negative whatever unit it carries and
// whatever it would have resolved to, which is what makes the syntactic test
// exactly as strong as the semantic one for this rule.
//
// A function's arguments are not looked into. "calc(10px - 20px)" is negative
// and this does not say so; calc is not implemented, so there is nothing here
// to be wrong about yet, and guessing at the sign of an expression that is not
// evaluated would drop declarations that are perfectly legal.
func hasNegativeNumber(vals []css.ComponentValue) bool {
	for _, v := range vals {
		if !v.IsToken() {
			continue
		}
		switch v.Token.Kind {
		case css.Number, css.Percentage, css.Dimension:
			if v.Token.Number < 0 {
				return true
			}
		}
	}
	return false
}

// shorthandLonghands lists what a shorthand sets, for the CSS-wide-keyword path.
func shorthandLonghands(name string) []string {
	sh, ok := shorthands[name]
	if !ok {
		return nil
	}
	out := append([]string(nil), sh.longhands...)
	sort.Strings(out)
	return out
}

func (s *Styler) report(f Finding) {
	switch {
	case len(s.findings) > maxFindings:
		return
	case len(s.findings) == maxFindings:
		s.findings = append(s.findings, Finding{
			Message: "further styling problems were not reported",
		})
	default:
		s.findings = append(s.findings, f)
	}
}

// pseudoElementNames are the ones that generate a box of their own.
//
// ::first-line and ::first-letter are deliberately absent: they style part of
// something that already exists rather than generating anything, and they need
// the line breaking to have happened before there is a first line to style.
var pseudoElementNames = []string{"before", "after", "marker"}

// anyRuleTargets reports whether any rule selects a pseudo-element of an
// element.
//
// It exists so that a pseudo-element with nothing said about it costs nothing: a
// document of ten thousand elements would otherwise compute three extra styles
// each, all of them the initial values, and generate nothing from any of them.
func (s *Styler) anyRuleTargets(rules []preparedRule, n *html.Node, name string) bool {
	for _, r := range rules {
		for _, sel := range r.selectors {
			if sel.PseudoElement != name {
				continue
			}
			if s.matcher.Match(sel, n) {
				return true
			}
		}
	}
	return false
}

// computeForPseudo resolves the properties of a pseudo-element.
//
// It inherits from the element it belongs to rather than from that element's
// parent, which is what makes "p { color: red } p::before { content: '>' }" draw
// a red marker without the author saying so twice.
func (s *Styler) computeForPseudo(n *html.Node, rules []preparedRule,
	owner ComputedStyle, name string) (ComputedStyle, bool) {
	return s.computeFor(n, rules, map[*html.Node]ComputedStyle{n: owner}, name)
}

// computeFor resolves every property for one element, or for one of its
// pseudo-elements when pseudo is not empty.
//
// It also reports whether font-size came from a declaration rather than by
// inheritance, which is the one thing a consumer cannot recover from the map it
// returns. See Styled.OwnFontSize for why that matters.
func (s *Styler) computeFor(n *html.Node, rules []preparedRule,
	done map[*html.Node]ComputedStyle, pseudo string) (ComputedStyle, bool) {

	var cands []candidate
	for _, r := range rules {
		spec, ok := s.matchSpecificityFor(r, n, pseudo)
		if !ok {
			continue
		}
		for _, d := range r.decls {
			cands = append(cands, candidate{
				property: d.property, value: d.value, important: d.important,
				origin: r.origin, spec: spec,
				order: d.order, offset: d.offset,
			})
		}
	}

	// The presentational hints of hints.go, at the very bottom of the author
	// origin: zero specificity and an order number below every declaration an
	// author wrote, so any author rule at all beats them and no user-agent rule
	// ever does. They belong to the element and not to its pseudo-elements,
	// which have no attributes of their own.
	if pseudo == "" {
		for property, value := range presentationalHints(n) {
			cands = append(cands, candidate{
				property: property, value: value,
				origin: OriginAuthor, order: hintOrder, offset: n.Offset,
			})
		}
	}

	// An inline style="..." attribute, which cascades above every author rule
	// of the same importance. It has no selector, so it has no specificity;
	// what puts it on top is its own step in the cascade order.
	//
	// It applies to the element and not to its pseudo-elements: there is no
	// syntax for writing one on a ::before, so a style attribute reaching one
	// would be a rule the author had no way to express.
	var inline map[string]preparedDecl
	if pseudo == "" {
		inline = s.inlineDeclarations(n)
	}

	winners := map[string]candidate{}
	for _, c := range cands {
		if best, ok := winners[c.property]; ok && !beats(c, best) {
			continue
		}
		winners[c.property] = c
	}

	var parent ComputedStyle
	if pseudo != "" {
		// A pseudo-element inherits from its own element, which the caller put
		// in the map under that element's own key.
		parent = done[n]
	} else if p := parentElement(n); p != nil {
		parent = done[p]
	}

	out := make(ComputedStyle, len(properties))
	ownFontSize := false
	for name, prop := range properties {
		value, have := "", false

		if c, ok := winners[name]; ok {
			value, have = serialize(c.value), true
		}
		if d, ok := inline[name]; ok {
			// Inline wins over everything an author rule can say, important or
			// not — except an important author rule, which the cascade puts
			// above it. That case is rare enough, and the ordering subtle
			// enough, that it is spelled out rather than left to fall out.
			if c, ok := winners[name]; !ok || !c.important {
				value, have = serialize(d.value), true
			}
		}

		out[name] = s.resolve(name, prop, value, have, parent)
		if name == "font-size" {
			ownFontSize = have && declaresItsOwnValue(value, prop)
		}
	}
	return out, ownFontSize
}

// declaresItsOwnValue reports whether a winning declaration says something about
// the element rather than deferring to its parent.
//
// The three CSS-wide keywords that defer are the ones that reach inheritFrom:
// "inherit" always, and "unset" and "revert" on a property that inherits. Every
// other value — including "initial", which is a statement about this element —
// is the element's own.
func declaresItsOwnValue(value string, prop property) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case kwInherit:
		return false
	case kwUnset, kwRevert:
		return !prop.inherits
	}
	return true
}

// resolve turns a winning declaration — or the absence of one — into a computed
// value, applying the CSS-wide keywords and inheritance.
func (s *Styler) resolve(name string, prop property, value string, have bool, parent ComputedStyle) string {
	inheritFrom := func() string {
		if parent == nil {
			// The root has no parent to inherit from, so it takes the initial
			// value — which is what "the initial value" means for the root.
			return prop.initial
		}
		if v, ok := parent[name]; ok {
			return v
		}
		return prop.initial
	}

	if have {
		switch strings.ToLower(value) {
		case kwInherit:
			return inheritFrom()
		case kwInitial:
			return prop.initial
		case kwUnset:
			// "unset" is "inherit if the property inherits, initial if it does
			// not" — the keyword that means "as though nothing had been said".
			if prop.inherits {
				return inheritFrom()
			}
			return prop.initial
		case kwRevert:
			// Reverting to the previous origin is not implemented. Treating it
			// as "unset" is the closest available answer and is wrong whenever a
			// user-agent rule set the property, so it is reported rather than
			// quietly substituted.
			if !s.seen["revert"] {
				s.seen["revert"] = true
				s.report(Finding{
					Message: "\"revert\" is not implemented and was read as \"unset\", " +
						"which differs wherever a lower-priority stylesheet set the property",
					Unsupported: true,
					Property:    name,
				})
			}
			if prop.inherits {
				return inheritFrom()
			}
			return prop.initial
		}
		return value
	}

	if prop.inherits {
		return inheritFrom()
	}
	return prop.initial
}

// matchSpecificity reports whether a rule applies to an element, and with what
// specificity.
//
// The two answers come together because they are one walk: asking "does it
// match" and then "how specific" would match every selector of every rule twice,
// for every element in the document.
//
// The specificity is that of the most specific selector that *matched*, not the
// most specific in the list. "a, #b {…}" applies to an <a> with the specificity
// of "a"; taking "#b" would let the rule beat declarations it should lose to.
func (s *Styler) matchSpecificityFor(r preparedRule, n *html.Node, pseudo string) (css.Specificity, bool) {
	var best css.Specificity
	found := false
	for _, sel := range r.selectors {
		// A rule with a pseudo-element styles that and nothing else, and a rule
		// without one never styles a pseudo-element. Getting this backwards
		// gives every ::before its element's whole style twice over.
		if sel.PseudoElement != pseudo {
			continue
		}
		if !s.matcher.Match(sel, n) {
			continue
		}
		if !found || best.Less(sel.Specificity) {
			best, found = sel.Specificity, true
		}
	}
	return best, found
}

// beats reports whether a wins over b, by CSS Cascade Level 4 §6.
//
// The order of the terms is the whole of it, and each one is only consulted when
// everything above it ties:
//
//  1. Importance and origin *together*. An important declaration reverses the
//     origin order, so an important user-agent rule beats an important author
//     one — which is how a user stylesheet can force a minimum contrast that a
//     page cannot override.
//  2. Specificity.
//  3. Order of appearance.
func beats(a, b candidate) bool {
	ao, bo := cascadeRank(a), cascadeRank(b)
	if ao != bo {
		return ao > bo
	}
	if a.spec != b.spec {
		return b.spec.Less(a.spec)
	}
	// Later wins. Equal orders cannot happen — every declaration gets its own
	// number — so this is a total order and the result does not depend on the
	// order candidates were collected in.
	return a.order > b.order
}

// cascadeRank is the combined importance-and-origin term, highest wins.
//
// Importance does not simply beat non-importance: it *inverts* the origin
// ordering. The sequence, weakest first, is user-agent, user, author, then
// important author, important user, important user-agent.
func cascadeRank(c candidate) int {
	if !c.important {
		return int(c.origin) // 0, 1, 2
	}
	// 3, 4, 5 with the origins reversed: author important is 3, user is 4,
	// user-agent is 5.
	return 3 + (int(OriginAuthor) - int(c.origin))
}

// inlineDeclarations reads an element's style attribute.
//
// The attribute holds a declaration list with no selector and no braces, so it
// is parsed as a block's contents rather than as a stylesheet.
func (s *Styler) inlineDeclarations(n *html.Node) map[string]preparedDecl {
	raw, ok := n.Attr("style")
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	decls, _, errs := css.ParseDeclarations(raw)
	for _, e := range errs {
		s.report(Finding{
			Offset: n.Offset, Message: "in a style attribute: " + e.Message,
			Unsupported: e.Unsupported,
		})
	}

	out := map[string]preparedDecl{}
	for _, d := range decls {
		for _, e := range s.expand(d, OriginAuthor) {
			// A later declaration in the same attribute wins, and importance
			// wins over its absence — the same rules as any other block, with
			// no specificity to separate them.
			if prev, ok := out[e.property]; ok && prev.important && !e.important {
				continue
			}
			out[e.property] = e
		}
	}
	return out
}
