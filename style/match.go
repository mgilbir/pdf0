// Package style applies a stylesheet to a document.
//
// It is the third of the six stages in the rendering proposal's §3: the html
// package gives it a tree, the css package gives it rules, and what comes out is
// a styled tree — every element with the declarations that won for it.
//
// This file is the first half, selector matching. It is also the first thing
// that *uses* the selector structures the css package builds, which is worth
// saying plainly: until something matches, a selector parser can only be tested
// for shape, and a wrong shape and a right one are indistinguishable.
package style

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
)

// maxMatchSteps bounds the work one selector may spend on one element.
//
// Matching a descendant combinator walks every ancestor, and a selector with
// several of them backtracks: "a b c d e" against a deep tree is a product, not
// a sum. A stylesheet is untrusted, and a few hundred bytes of selector against
// a few kilobytes of markup is the cheapest denial of service either input
// offers. Real selectors settle in tens of steps.
//
// A budget that trips is *reported*, never silently answered "no match" — see
// Matcher.Tripped. A layout engine that quietly stopped matching would produce
// a document with styles missing and nothing to say so, which is the failure
// mode the whole reporting design exists to prevent.
const maxMatchSteps = 10000

// Matcher applies selectors to one document.
//
// It holds the work budget, which is why matching goes through a value rather
// than a bare function: the budget has to outlive a single call to be worth
// anything, and whether it tripped has to be readable afterwards.
type Matcher struct {
	// root is the document's outermost element, which :root selects.
	root *html.Node

	// kids memoizes each parent's element children, and idx each element's
	// position among them.
	//
	// Without these, every step of a sibling walk rescans the parent's whole
	// child list, so ":nth-child" over a list of n items costs n² — and a list
	// of ten thousand rows is an ordinary table, not a hostile one. The tree
	// does not change while it is being matched, so the memo is safe and is
	// built once per parent that is asked about.
	kids map[*html.Node][]*html.Node
	idx  map[*html.Node]int

	steps   int
	tripped bool
}

// NewMatcher prepares to match selectors against a document.
func NewMatcher(doc *html.Node) *Matcher {
	return &Matcher{
		root: documentElement(doc),
		kids: map[*html.Node][]*html.Node{},
		idx:  map[*html.Node]int{},
	}
}

// documentElement is the <html> element, which is what :root means. It is not
// simply "the node with no parent" — that is the document node, which is not an
// element and which no selector matches.
func documentElement(doc *html.Node) *html.Node {
	if doc == nil {
		return nil
	}
	if doc.Type == html.ElementNode {
		return doc
	}
	for _, c := range doc.Children {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}

// Tripped reports whether the work budget stopped a match short.
//
// When it has, the answers this Matcher gave are a lower bound: some selectors
// that should have matched did not. A caller must report that rather than render
// as though the stylesheet had been applied in full.
func (m *Matcher) Tripped() bool { return m.tripped }

// Match reports whether an element is selected.
func (m *Matcher) Match(s css.Selector, n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || len(s.Compounds) == 0 {
		return false
	}
	m.steps = 0
	return m.complex(s.Compounds, len(s.Compounds)-1, n)
}

// complex matches compounds[:i+1] with compounds[i] against n.
//
// It runs right to left, from the subject outwards, which is not a
// micro-optimisation: a document has far more elements than a selector has
// compounds, and the subject is the cheapest thing to reject on. Matching left
// to right would search the tree for the first compound and then check whether
// anything under it was the element in hand.
func (m *Matcher) complex(compounds []css.Compound, i int, n *html.Node) bool {
	if m.spent() {
		return false
	}
	if !m.compound(compounds[i], n) {
		return false
	}
	if i == 0 {
		return true
	}

	switch compounds[i].Combinator {
	case css.Child:
		p := parentElement(n)
		return p != nil && m.complex(compounds, i-1, p)

	case css.NextSibling:
		s := m.prevElement(n)
		return s != nil && m.complex(compounds, i-1, s)

	case css.SubsequentSibling:
		for s := m.prevElement(n); s != nil; s = m.prevElement(s) {
			if m.complex(compounds, i-1, s) {
				return true
			}
			if m.spent() {
				return false
			}
		}
		return false

	default: // Descendant
		for p := parentElement(n); p != nil; p = parentElement(p) {
			if m.complex(compounds, i-1, p) {
				return true
			}
			if m.spent() {
				return false
			}
		}
		return false
	}
}

// spent charges one step and reports whether the budget is gone.
func (m *Matcher) spent() bool {
	if m.tripped {
		return true
	}
	m.steps++
	if m.steps > maxMatchSteps {
		m.tripped = true
		return true
	}
	return false
}

// compound reports whether one element satisfies every part of a compound.
//
// The order is cheapest-first and deliberately so: a type mismatch rejects most
// elements for most selectors, and the pseudo-classes — which may walk siblings
// or recurse into another selector list — are asked last.
func (m *Matcher) compound(c css.Compound, n *html.Node) bool {
	if c.Type != "" && !strings.EqualFold(c.Type, n.Name) {
		return false
	}
	for _, id := range c.IDs {
		// Two different identifiers in one compound match nothing, which falls
		// out of this rather than needing a rule: an element has one id.
		if v, ok := n.Attr("id"); !ok || v != id {
			return false
		}
	}
	for _, class := range c.Classes {
		if !hasClass(n, class) {
			return false
		}
	}
	for _, a := range c.Attrs {
		if !matchAttr(a, n) {
			return false
		}
	}
	for _, p := range c.Pseudos {
		if !m.pseudo(p, n) {
			return false
		}
	}
	return true
}

// hasClass reports whether an element carries a class.
//
// The attribute is a whitespace-separated set, so this is a membership test and
// not a substring one: class="subtitle" must not match ".title".
func hasClass(n *html.Node, want string) bool {
	v, ok := n.Attr("class")
	if !ok {
		return false
	}
	for _, got := range strings.Fields(v) {
		if got == want {
			return true
		}
	}
	return false
}

func matchAttr(a css.Attr, n *html.Node) bool {
	v, ok := n.Attr(a.Name)
	if !ok {
		return false
	}
	if a.Op == css.AttrExists {
		return true
	}

	got, want := v, a.Value
	if a.Insensitive {
		got, want = strings.ToLower(got), strings.ToLower(want)
	}

	switch a.Op {
	case css.AttrEquals:
		return got == want
	case css.AttrIncludes:
		// A whitespace-separated set, like class. An empty value or one
		// containing whitespace can never match, because it is not a member of
		// any such set.
		if want == "" || strings.ContainsAny(want, " \t\n\r\f") {
			return false
		}
		return slices(got, want)
	case css.AttrDashMatch:
		// "en" matches "en" and "en-GB" but not "english". This exists for
		// language subtags, which is why the boundary is the hyphen and not any
		// prefix.
		return got == want || strings.HasPrefix(got, want+"-")
	case css.AttrPrefix:
		return want != "" && strings.HasPrefix(got, want)
	case css.AttrSuffix:
		return want != "" && strings.HasSuffix(got, want)
	case css.AttrSubstring:
		// The empty string is a substring of everything, so the specification
		// makes it match nothing instead — otherwise [href*=""] would select
		// every element with an href, which is what [href] already says.
		return want != "" && strings.Contains(got, want)
	}
	return false
}

func slices(value, want string) bool {
	for _, f := range strings.Fields(value) {
		if f == want {
			return true
		}
	}
	return false
}

func (m *Matcher) pseudo(p css.Pseudo, n *html.Node) bool {
	switch p.Kind {
	case css.PseudoRoot:
		return n == m.root

	case css.PseudoEmpty:
		// "Empty" counts text of any kind, including whitespace: a paragraph
		// containing a single space is not empty. Comments do not count, and
		// this tree has none to begin with.
		for _, c := range n.Children {
			if c.Type == html.ElementNode {
				return false
			}
			if c.Type == html.TextNode && c.Text != "" {
				return false
			}
		}
		return true

	case css.PseudoFirstChild:
		return m.prevElement(n) == nil
	case css.PseudoLastChild:
		return m.nextElement(n) == nil
	case css.PseudoOnlyChild:
		return m.prevElement(n) == nil && m.nextElement(n) == nil

	case css.PseudoFirstOfType:
		return m.prevOfType(n) == nil
	case css.PseudoLastOfType:
		return m.nextOfType(n) == nil
	case css.PseudoOnlyOfType:
		return m.prevOfType(n) == nil && m.nextOfType(n) == nil

	case css.PseudoNthChild:
		return p.AnB.Matches(m.indexOf(n, p.Of, false, false))
	case css.PseudoNthLastChild:
		return p.AnB.Matches(m.indexOf(n, p.Of, true, false))
	case css.PseudoNthOfType:
		return p.AnB.Matches(m.indexOf(n, nil, false, true))
	case css.PseudoNthLastOfType:
		return p.AnB.Matches(m.indexOf(n, nil, true, true))

	case css.PseudoNot:
		for _, s := range p.Args {
			if m.complexFrom(s, n) {
				return false
			}
		}
		return true

	case css.PseudoIs, css.PseudoWhere:
		for _, s := range p.Args {
			if m.complexFrom(s, n) {
				return true
			}
		}
		return false

	case css.PseudoLang:
		return matchLang(n, p.Langs)

	case css.PseudoAnyLink:
		// :link and :any-link are the same thing once :visited cannot be true,
		// and both are about the document rather than about a person: an
		// element is a link when it is an <a> or an <area> with an href.
		if !strings.EqualFold(n.Name, "a") && !strings.EqualFold(n.Name, "area") {
			return false
		}
		return n.HasAttr("href")
	}
	return false
}

// complexFrom matches a whole selector with n as its subject, which is what the
// argument of :is(), :not() and :where() asks.
func (m *Matcher) complexFrom(s css.Selector, n *html.Node) bool {
	if len(s.Compounds) == 0 {
		return false
	}
	return m.complex(s.Compounds, len(s.Compounds)-1, n)
}

// indexOf returns an element's one-based position among its siblings, counting
// from the end when last is set, and counting only siblings that match — the
// same element name for the of-type family, or the "of S" list for :nth-child.
//
// It returns 0 for an element with no parent, which no An+B selects.
func (m *Matcher) indexOf(n *html.Node, of []css.Selector, last, sameType bool) int {
	parent := n.Parent
	if parent == nil {
		return 0
	}
	sibs := m.children(parent)
	if last {
		// Counting from the end. The memo is shared, so it is walked backwards
		// rather than reversed — reversing it in place would corrupt every
		// later question about the same parent.
		sibs = reversed(sibs)
	}

	idx := 0
	for _, s := range sibs {
		if sameType && !strings.EqualFold(s.Name, n.Name) {
			continue
		}
		if len(of) > 0 && !m.matchesAny(of, s) {
			continue
		}
		idx++
		if s == n {
			return idx
		}
	}
	// The element itself was filtered out by "of", so it is not in the series
	// at all and nothing selects it.
	return 0
}

func (m *Matcher) matchesAny(sels []css.Selector, n *html.Node) bool {
	for _, s := range sels {
		if m.complexFrom(s, n) {
			return true
		}
	}
	return false
}

// matchLang implements :lang(), which reads the nearest lang attribute at or
// above the element and compares it as a language range.
//
// The comparison is the dash-match of attribute selectors, so :lang(en) selects
// an element declared "en-GB" — which is the whole reason the pseudo-class
// exists rather than authors writing [lang|=en].
func matchLang(n *html.Node, langs []string) bool {
	value := ""
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Type != html.ElementNode {
			continue
		}
		if v, ok := cur.Attr("lang"); ok && v != "" {
			value = v
			break
		}
	}
	if value == "" {
		return false
	}
	value = strings.ToLower(value)
	for _, want := range langs {
		want = strings.ToLower(want)
		if want == "*" || value == want || strings.HasPrefix(value, want+"-") {
			return true
		}
	}
	return false
}

// Tree navigation. Every one of these skips text nodes, because a selector
// speaks about elements and nothing else — "p + p" means two paragraphs with
// only text between them, not two paragraphs with nothing at all.

func parentElement(n *html.Node) *html.Node {
	if n == nil || n.Parent == nil || n.Parent.Type != html.ElementNode {
		return nil
	}
	return n.Parent
}

// children returns a parent's element children, memoized.
func (m *Matcher) children(parent *html.Node) []*html.Node {
	if parent == nil {
		return nil
	}
	if got, ok := m.kids[parent]; ok {
		return got
	}
	out := make([]*html.Node, 0, len(parent.Children))
	for _, c := range parent.Children {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	m.kids[parent] = out
	for i, c := range out {
		m.idx[c] = i
	}
	return out
}

// siblingIndex is an element's position among its parent's element children,
// or -1 if it has no element parent.
func (m *Matcher) siblingIndex(n *html.Node) int {
	parent := n.Parent
	if parent == nil {
		return -1
	}
	m.children(parent) // fills m.idx
	if i, ok := m.idx[n]; ok {
		return i
	}
	return -1
}

func (m *Matcher) prevElement(n *html.Node) *html.Node {
	i := m.siblingIndex(n)
	if i <= 0 {
		return nil
	}
	return m.kids[n.Parent][i-1]
}

func (m *Matcher) nextElement(n *html.Node) *html.Node {
	i := m.siblingIndex(n)
	if i < 0 {
		return nil
	}
	sibs := m.kids[n.Parent]
	if i+1 >= len(sibs) {
		return nil
	}
	return sibs[i+1]
}

func (m *Matcher) prevOfType(n *html.Node) *html.Node {
	for s := m.prevElement(n); s != nil; s = m.prevElement(s) {
		if strings.EqualFold(s.Name, n.Name) {
			return s
		}
	}
	return nil
}

func (m *Matcher) nextOfType(n *html.Node) *html.Node {
	for s := m.nextElement(n); s != nil; s = m.nextElement(s) {
		if strings.EqualFold(s.Name, n.Name) {
			return s
		}
	}
	return nil
}

// reversed returns a copy walked backwards. It copies because the slice it is
// given is the memo, which every other question about that parent shares.
func reversed(ns []*html.Node) []*html.Node {
	out := make([]*html.Node, len(ns))
	for i, n := range ns {
		out[len(ns)-1-i] = n
	}
	return out
}
