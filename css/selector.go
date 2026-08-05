package css

import (
	"fmt"
	"strings"
)

// Selectors, from Selectors Level 4 — parsed and given a specificity here, and
// matched against a document in the layer above, which needs a document to match
// against.
//
// # The subset, and the line it is drawn on
//
// A PDF page is static. It has no pointer, no focus, no history and no form
// state, so a selector that asks about any of those has no answer here — not a
// false one, none at all. Those are refused: :hover, :focus, :checked,
// :visited and their kin.
//
// The line is *dynamism*, not familiarity. Everything the document itself
// determines is in, including the whole structural family — :nth-child(),
// :first-of-type, :empty, :root — and :link, which looks like one of the
// interactive ones and is not: whether an <a> has an href is a fact about the
// document. Its partner :visited is refused, because that one is a fact about a
// person's browsing history.
//
// # Refusing rather than ignoring
//
// A selector outside the subset makes the whole selector invalid, which is what
// the specification says to do with an unknown pseudo-class, and the rule is
// dropped. That is deliberately not the same as matching nothing quietly: each
// one is reported with Unsupported set, so an author is told the rule never
// applied rather than left wondering why the page looks wrong. Silence here is
// the failure mode §6.3 of the rendering proposal is written about.

// Combinator joins two compound selectors.
type Combinator uint8

const (
	// Descendant is the space in "a b": b anywhere inside a.
	Descendant Combinator = iota
	// Child is ">": b directly inside a.
	Child
	// NextSibling is "+": b immediately after a.
	NextSibling
	// SubsequentSibling is "~": b anywhere after a, under the same parent.
	SubsequentSibling
)

func (c Combinator) String() string {
	switch c {
	case Child:
		return ">"
	case NextSibling:
		return "+"
	case SubsequentSibling:
		return "~"
	}
	return " "
}

// AttrOp is how an attribute selector compares.
type AttrOp uint8

const (
	// AttrExists is "[a]" — the attribute is present, whatever its value.
	AttrExists AttrOp = iota
	// AttrEquals is "[a=v]".
	AttrEquals
	// AttrIncludes is "[a~=v]" — v is one of a whitespace-separated list.
	AttrIncludes
	// AttrDashMatch is "[a|=v]" — v, or v followed by "-". It exists for
	// language subtags, where "en" should match "en-GB".
	AttrDashMatch
	// AttrPrefix is "[a^=v]".
	AttrPrefix
	// AttrSuffix is "[a$=v]".
	AttrSuffix
	// AttrSubstring is "[a*=v]".
	AttrSubstring
)

func (o AttrOp) String() string {
	switch o {
	case AttrEquals:
		return "="
	case AttrIncludes:
		return "~="
	case AttrDashMatch:
		return "|="
	case AttrPrefix:
		return "^="
	case AttrSuffix:
		return "$="
	case AttrSubstring:
		return "*="
	}
	return ""
}

// Attr is one attribute selector.
type Attr struct {
	// Name is the attribute name as written. HTML lowercases attribute names,
	// so the layer that matches folds it; keeping it as written lets a
	// diagnostic quote the author.
	Name string
	Op   AttrOp
	// Value is empty when Op is AttrExists.
	Value string
	// Insensitive is the "i" flag of "[a=v i]", which asks for an
	// ASCII case-insensitive comparison of the *value*. The "s" flag asks for a
	// sensitive one, which is the default, so it is recorded as false rather
	// than as a third state.
	Insensitive bool
}

// PseudoKind names a pseudo-class this engine implements. Anything not here is
// refused at parse time, so there is no kind for "unknown".
type PseudoKind uint8

const (
	// Structural pseudo-classes: everything the document's own shape decides.
	PseudoRoot PseudoKind = iota
	PseudoEmpty
	PseudoFirstChild
	PseudoLastChild
	PseudoOnlyChild
	PseudoFirstOfType
	PseudoLastOfType
	PseudoOnlyOfType
	PseudoNthChild
	PseudoNthLastChild
	PseudoNthOfType
	PseudoNthLastOfType

	// Logical combinations.
	PseudoNot
	PseudoIs
	PseudoWhere

	// PseudoLang is :lang(), which reads the document's own language
	// declaration.
	PseudoLang

	// PseudoAnyLink is :link and :any-link, both of which mean "an element with
	// an href" once :visited cannot be true.
	PseudoAnyLink
)

// Pseudo is one pseudo-class in a compound selector.
type Pseudo struct {
	Kind PseudoKind
	// Name is the pseudo-class as the author wrote it, for diagnostics.
	Name string

	// AnB is set for the four :nth-* kinds.
	AnB AnB
	// Of is the "of S" of ":nth-child(An+B of S)", empty when absent.
	Of []Selector
	// Args is set for :not(), :is() and :where().
	Args []Selector
	// Langs is set for :lang().
	Langs []string
}

// Compound is a run of simple selectors that all constrain the same element,
// together with the combinator joining it to the compound before it.
type Compound struct {
	// Combinator joins this compound to the one before it. It is meaningless on
	// the first compound of a selector, where it is Descendant.
	Combinator Combinator

	// Type is the element name, empty if none was written. Universal is "*".
	// Both may be absent, which is what ".c" is.
	Type      string
	Universal bool

	// IDs is every "#name" in the compound. More than one is legal and is not a
	// mistake to be corrected here: "#a#b" matches nothing, and "#a#a" matches
	// what "#a" matches while counting twice towards specificity, which is a
	// long-standing way to raise a rule's weight without touching the document.
	// Refusing either would reject stylesheets that browsers accept.
	IDs     []string
	Classes []string
	Attrs   []Attr
	Pseudos []Pseudo
}

// Selector is one complex selector: compound selectors joined by combinators.
//
// The last compound is the *subject* — the element the selector selects. That
// matters more than it looks: matching runs right to left, from the subject
// outwards, because a document has far more elements than a selector has
// compounds and the subject is the cheapest thing to reject on.
type Selector struct {
	Compounds []Compound

	// PseudoElement is "before", "after", "first-line", "first-letter" or
	// "marker", empty when there is none. It is on the selector rather than on
	// a compound because at most one may appear and only on the subject.
	PseudoElement string

	Specificity Specificity

	// Offset is the byte offset in the source at which the selector begins.
	Offset int
}

// Specificity is the (a, b, c) of Selectors Level 4 §17: identifiers, then
// classes and attributes and pseudo-classes, then element names and
// pseudo-elements. It decides which of two declarations wins when both apply.
type Specificity struct{ A, B, C int }

// Less reports whether s loses to other. The three components are compared in
// order and do not carry: a thousand classes lose to one identifier, which is
// why this is not a single number.
func (s Specificity) Less(other Specificity) bool {
	if s.A != other.A {
		return s.A < other.A
	}
	if s.B != other.B {
		return s.B < other.B
	}
	return s.C < other.C
}

func (s Specificity) String() string { return fmt.Sprintf("(%d,%d,%d)", s.A, s.B, s.C) }

// add sums two specificities, which is what building a compound does.
func (s Specificity) add(o Specificity) Specificity {
	return Specificity{s.A + o.A, s.B + o.B, s.C + o.C}
}

// max returns the more specific of two, which is what :is() and :not()
// contribute.
func (s Specificity) max(o Specificity) Specificity {
	if s.Less(o) {
		return o
	}
	return s
}

// pseudoClasses is the implemented subset, keyed by the lowercased name. A
// pseudo-class absent from here and from dynamicPseudoClasses is not a
// pseudo-class at all.
var pseudoClasses = map[string]PseudoKind{
	"root":             PseudoRoot,
	"empty":            PseudoEmpty,
	"first-child":      PseudoFirstChild,
	"last-child":       PseudoLastChild,
	"only-child":       PseudoOnlyChild,
	"first-of-type":    PseudoFirstOfType,
	"last-of-type":     PseudoLastOfType,
	"only-of-type":     PseudoOnlyOfType,
	"nth-child":        PseudoNthChild,
	"nth-last-child":   PseudoNthLastChild,
	"nth-of-type":      PseudoNthOfType,
	"nth-last-of-type": PseudoNthLastOfType,
	"not":              PseudoNot,
	"is":               PseudoIs,
	"matches":          PseudoIs, // the old spelling of :is()
	"where":            PseudoWhere,
	"lang":             PseudoLang,
	"link":             PseudoAnyLink,
	"any-link":         PseudoAnyLink,
}

// dynamicPseudoClasses are correct CSS that a static page cannot answer: they
// ask about a pointer, a keyboard, form state or browsing history, none of
// which a PDF has.
//
// They are listed rather than lumped in with the unknown so that the diagnostic
// can say which it is. "no such pseudo-class" sends an author looking for a
// typo; ":hover cannot apply to a printed page" tells them the truth, which is
// that the rule was understood and deliberately not applied.
var dynamicPseudoClasses = map[string]bool{
	"active": true, "hover": true, "focus": true, "focus-visible": true,
	"focus-within": true, "visited": true, "target": true, "target-within": true,
	"checked": true, "indeterminate": true, "default": true, "disabled": true,
	"enabled": true, "read-only": true, "read-write": true,
	"placeholder-shown": true, "valid": true, "invalid": true,
	"in-range": true, "out-of-range": true, "required": true, "optional": true,
	"user-valid": true, "user-invalid": true, "autofill": true,
	"playing": true, "paused": true, "muted": true, "seeking": true,
	"buffering": true, "stalled": true, "fullscreen": true, "modal": true,
	"popover-open": true, "picture-in-picture": true, "current": true,
	"past": true, "future": true, "local-link": true, "defined": true,
	"host": true, "host-context": true,
}

// pseudoElements is the implemented subset. The rest are refused: ::selection
// needs a selection, ::backdrop needs a top layer, and ::part and ::slotted
// need a shadow tree, which needs scripting, which this engine never has.
var pseudoElements = map[string]bool{
	"before": true, "after": true,
	"first-line": true, "first-letter": true,
	"marker": true,
}

// reasonForPseudoElement adds why a refused pseudo-element is refused, when
// there is something better to say than "not implemented".
//
// The distinction is the same one dynamicPseudoClasses draws: an author whose
// ::selection rule did nothing is helped by learning that a printed page has no
// selection, and misled by a message that suggests the name was wrong.
func reasonForPseudoElement(lower string) string {
	switch lower {
	case "selection", "target-text", "highlight", "spelling-error", "grammar-error":
		return ": a page laid out once has nothing selected or highlighted"
	case "backdrop":
		return ": there is no top layer on a printed page"
	case "part", "slotted":
		return ": a shadow tree needs scripting, which this engine never runs"
	case "placeholder":
		return ": form fields are not interactive here"
	}
	return ""
}

// legacyPseudoElements may be written with one colon, because they predate the
// two-colon notation and every browser still accepts them.
var legacyPseudoElements = map[string]bool{
	"before": true, "after": true, "first-line": true, "first-letter": true,
}

// ParseSelectorList parses the prelude of a style rule into selectors.
//
// A selector that cannot be parsed, or that falls outside the subset, is
// dropped and reported. If *any* of them is dropped the whole list is invalid —
// that is what the specification requires, and it is the safe direction: a rule
// whose selector list was silently narrowed applies to fewer elements than its
// author asked for, and nothing about the resulting page says so. ok reports
// whether the list survived intact.
func ParseSelectorList(vals []ComponentValue) (sels []Selector, errs []Error, ok bool) {
	p := &selParser{}
	out, all := p.list(vals, 0)
	// Usability is "every selector written was understood", not "nothing was
	// reported". The two differ inside :is() and :where(), which are forgiving:
	// an argument they could not use is dropped and still reported, and the rule
	// around it stands. Tying ok to the error list would make those two fatal
	// and defeat the whole point of a forgiving selector list.
	if !all || len(out) == 0 {
		// Nothing is returned when the list is unusable, rather than the part
		// of it that parsed. A caller that forgot to check ok would otherwise
		// apply the rule to the selectors that survived — which is a rule the
		// author never wrote, narrower than the one they did, and with nothing
		// about the resulting page to say so.
		return nil, p.errs, false
	}
	return out, p.errs, true
}

// maxSelectorDepth bounds recursion through :is(), :not() and :where().
//
// The component-value tree is already capped, so this cannot be driven far by
// nesting alone; it is here because the recursion is mutual and a bound that is
// only implied by another bound is one that a later change can remove without
// noticing.
const maxSelectorDepth = 32

type selParser struct {
	errs []Error
}

func (p *selParser) fail(off int, msg string) {
	p.add(Error{Offset: off, Message: msg})
}

func (p *selParser) unsupported(off int, msg string) {
	p.add(Error{Offset: off, Message: msg, Unsupported: true})
}

func (p *selParser) add(e Error) {
	switch {
	case len(p.errs) > maxErrors:
		return
	case len(p.errs) == maxErrors:
		p.errs = append(p.errs, Error{
			Offset:  e.Offset,
			Message: "further problems in this stylesheet were not reported",
		})
	default:
		p.errs = append(p.errs, e)
	}
}

// list splits on top-level commas and parses each complex selector. all reports
// whether every one of them survived, which is what the callers that are not
// forgiving need to know.
func (p *selParser) list(vals []ComponentValue, depth int) (sels []Selector, all bool) {
	if depth > maxSelectorDepth {
		p.fail(offsetOf(vals), "selectors are nested too deeply to read")
		return nil, false
	}
	parts := splitOnComma(vals)
	var out []Selector
	for _, part := range parts {
		if s, ok := p.complex(part, depth); ok {
			out = append(out, s)
		}
	}
	return out, len(out) == len(parts)
}

// splitOnComma divides a selector list. The commas are at the top level by
// construction: one inside a function or a block belongs to that function.
func splitOnComma(vals []ComponentValue) [][]ComponentValue {
	var out [][]ComponentValue
	start := 0
	for i, v := range vals {
		if v.IsToken() && v.Token.Kind == Comma {
			out = append(out, vals[start:i])
			start = i + 1
		}
	}
	return append(out, vals[start:])
}

func offsetOf(vals []ComponentValue) int {
	if len(vals) > 0 {
		return vals[0].Token.Offset
	}
	return 0
}

// complex parses one complex selector: compounds joined by combinators.
func (p *selParser) complex(vals []ComponentValue, depth int) (Selector, bool) {
	vals = trimWhitespace(vals)
	if len(vals) == 0 {
		p.fail(offsetOf(vals), "an empty selector")
		return Selector{}, false
	}

	out := Selector{Offset: vals[0].Token.Offset}
	combinator := Descendant
	i := 0

	for i < len(vals) {
		// A run of simple selectors, up to the next combinator.
		end := i
		for end < len(vals) && !isCombinatorAt(vals, end) {
			end++
		}
		if end == i {
			p.fail(vals[i].Token.Offset, "a combinator with nothing before it")
			return Selector{}, false
		}

		c, pseudoElem, ok := p.compound(vals[i:end], depth)
		if !ok {
			return Selector{}, false
		}
		c.Combinator = combinator

		if out.PseudoElement != "" {
			// Only the subject may carry one, so anything after it is an error
			// rather than a second pseudo-element.
			p.fail(vals[i].Token.Offset,
				"nothing may follow the pseudo-element ::"+out.PseudoElement)
			return Selector{}, false
		}
		out.PseudoElement = pseudoElem
		out.Compounds = append(out.Compounds, c)

		i = end
		if i >= len(vals) {
			break
		}

		// Read the combinator, and the whitespace around it.
		combinator, i, ok = p.combinator(vals, i)
		if !ok {
			return Selector{}, false
		}
		if i >= len(vals) {
			p.fail(vals[len(vals)-1].Token.Offset,
				"the selector ends with the combinator \""+combinator.String()+"\"")
			return Selector{}, false
		}
	}

	out.Specificity = specificityOf(out)
	return out, true
}

// isCombinatorAt reports whether a combinator begins at i. Whitespace counts,
// because the descendant combinator *is* whitespace — but only whitespace that
// separates two compounds, which the caller settles by trimming the ends first.
func isCombinatorAt(vals []ComponentValue, i int) bool {
	v := vals[i]
	if !v.IsToken() {
		return false
	}
	switch v.Token.Kind {
	case Whitespace:
		return true
	case Delim:
		return v.Token.IsDelim('>') || v.Token.IsDelim('+') || v.Token.IsDelim('~')
	}
	return false
}

// combinator reads one combinator and any whitespace around it, returning where
// the next compound begins.
//
// Whitespace is only the descendant combinator when nothing else is there:
// "a > b" has a space on each side of the ">" and is one child combinator, not
// three combinators.
func (p *selParser) combinator(vals []ComponentValue, i int) (Combinator, int, bool) {
	sawSpace := false
	out := Descendant
	explicit := false

	for i < len(vals) && isCombinatorAt(vals, i) {
		t := vals[i].Token
		if t.Kind == Whitespace {
			sawSpace = true
			i++
			continue
		}
		if explicit {
			p.fail(t.Offset, "two combinators in a row")
			return out, i, false
		}
		switch {
		case t.IsDelim('>'):
			out = Child
		case t.IsDelim('+'):
			out = NextSibling
		case t.IsDelim('~'):
			out = SubsequentSibling
		}
		explicit = true
		i++
	}
	if !explicit && !sawSpace {
		p.fail(offsetOf(vals[i:]), "expected a combinator")
		return out, i, false
	}
	return out, i, true
}

func trimWhitespace(vals []ComponentValue) []ComponentValue {
	for len(vals) > 0 && vals[0].IsToken() && vals[0].Token.Kind == Whitespace {
		vals = vals[1:]
	}
	for len(vals) > 0 && vals[len(vals)-1].IsToken() && vals[len(vals)-1].Token.Kind == Whitespace {
		vals = vals[:len(vals)-1]
	}
	return vals
}

// compound parses one compound selector — the simple selectors that all
// constrain the same element — and the pseudo-element that may follow it.
func (p *selParser) compound(vals []ComponentValue, depth int) (Compound, string, bool) {
	var out Compound
	var pseudoElem string
	i := 0

	// A type or universal selector, if present, must come first.
	if len(vals) > 0 && vals[0].IsToken() {
		switch t := vals[0].Token; {
		case t.Kind == Ident:
			out.Type = t.Value
			i = 1
		case t.IsDelim('*'):
			out.Universal = true
			i = 1
		case t.IsDelim('|'):
			p.unsupported(t.Offset, "namespaces in selectors are not implemented")
			return out, "", false
		}
	}

	for i < len(vals) {
		v := vals[i]

		// A namespace separator anywhere makes this a qualified name.
		if v.IsToken() && v.Token.IsDelim('|') {
			p.unsupported(v.Token.Offset, "namespaces in selectors are not implemented")
			return out, "", false
		}

		if v.IsBlock() && v.Token.Kind == LeftSquare {
			a, ok := p.attribute(v)
			if !ok {
				return out, "", false
			}
			out.Attrs = append(out.Attrs, a)
			i++
			continue
		}

		if !v.IsToken() && !v.IsFunction() {
			p.fail(v.Token.Offset, "unexpected "+v.Token.String()+" in a selector")
			return out, "", false
		}

		t := v.Token
		switch {
		case t.Kind == Hash:
			if !t.IsID {
				p.fail(t.Offset, "\"#"+t.Value+"\" is a colour, not an identifier selector")
				return out, "", false
			}
			out.IDs = append(out.IDs, t.Value)
			i++

		case t.IsDelim('.'):
			if i+1 >= len(vals) || !vals[i+1].IsToken() || vals[i+1].Token.Kind != Ident {
				p.fail(t.Offset, "expected a class name after \".\"")
				return out, "", false
			}
			out.Classes = append(out.Classes, vals[i+1].Token.Value)
			i += 2

		case t.Kind == Colon:
			var ok bool
			var elem string
			i, elem, ok = p.pseudo(vals, i, &out, depth)
			if !ok {
				return out, "", false
			}
			if elem != "" {
				if pseudoElem != "" {
					p.fail(t.Offset, "a second pseudo-element, ::"+elem)
					return out, "", false
				}
				pseudoElem = elem
			}

		case t.Kind == Ident:
			p.fail(t.Offset, "an element name must come first in \""+t.Value+"\"")
			return out, "", false

		default:
			p.fail(t.Offset, "unexpected "+t.String()+" in a selector")
			return out, "", false
		}
	}
	return out, pseudoElem, true
}

// pseudo parses a pseudo-class or pseudo-element beginning at the colon in
// vals[i]. It returns the index after it and the pseudo-element name, if that is
// what it was.
func (p *selParser) pseudo(vals []ComponentValue, i int, out *Compound, depth int) (int, string, bool) {
	colon := vals[i].Token
	i++

	// A second colon means a pseudo-element.
	element := false
	if i < len(vals) && vals[i].IsToken() && vals[i].Token.Kind == Colon {
		element = true
		i++
	}
	if i >= len(vals) {
		p.fail(colon.Offset, "expected a name after \":\"")
		return i, "", false
	}

	v := vals[i]
	if !v.IsToken() && !v.IsFunction() {
		p.fail(colon.Offset, "expected a name after \":\"")
		return i, "", false
	}
	name := v.Token.Value
	lower := strings.ToLower(name)

	if element {
		if !pseudoElements[lower] {
			p.unsupported(colon.Offset, "the pseudo-element ::"+name+
				" is not implemented"+reasonForPseudoElement(lower))
			return i, "", false
		}
		if v.IsFunction() {
			p.unsupported(colon.Offset, "the pseudo-element ::"+name+"() is not implemented")
			return i, "", false
		}
		return i + 1, lower, true
	}

	// One colon may still be a pseudo-element, for the four that predate the
	// two-colon notation.
	if !v.IsFunction() && legacyPseudoElements[lower] {
		return i + 1, lower, true
	}

	kind, known := pseudoClasses[lower]
	if !known {
		if dynamicPseudoClasses[lower] {
			p.unsupported(colon.Offset, "\":"+name+
				"\" depends on how a document is being interacted with, "+
				"which a page laid out once cannot know")
			return i, "", false
		}
		p.unsupported(colon.Offset, "the pseudo-class \":"+name+"\" is not implemented")
		return i, "", false
	}

	ps := Pseudo{Kind: kind, Name: lower}

	switch kind {
	case PseudoNthChild, PseudoNthLastChild, PseudoNthOfType, PseudoNthLastOfType:
		if !v.IsFunction() {
			p.fail(colon.Offset, "\":"+name+"\" needs an An+B in parentheses")
			return i, "", false
		}
		anb, of, ok := p.nth(v, lower, depth)
		if !ok {
			return i, "", false
		}
		ps.AnB, ps.Of = anb, of

	case PseudoNot, PseudoIs, PseudoWhere:
		if !v.IsFunction() {
			p.fail(colon.Offset, "\":"+name+"\" needs a selector list in parentheses")
			return i, "", false
		}
		args, all := p.list(v.Values, depth+1)
		// :is() and :where() are forgiving: an argument they cannot use is
		// dropped and the rest stand. :not() is not — the specification says an
		// invalid argument makes it invalid, and it has to be that way round,
		// because dropping an argument from :not() *widens* what the rule
		// matches. Being forgiving there would apply a style to elements the
		// author explicitly excluded.
		if kind == PseudoNot && !all {
			p.fail(colon.Offset, "\":not()\" has an argument this engine cannot use, "+
				"which would widen what the rule matches")
			return i, "", false
		}
		if len(args) == 0 {
			// Nothing left at all is not forgivable either way: :is() with
			// nothing matches nothing and :not() with nothing matches
			// everything, and neither is what was written.
			p.fail(colon.Offset, "\":"+name+"\" has no selector this engine can use")
			return i, "", false
		}
		ps.Args = args

	case PseudoLang:
		if !v.IsFunction() {
			p.fail(colon.Offset, "\":lang()\" needs a language in parentheses")
			return i, "", false
		}
		langs, ok := p.langs(v)
		if !ok {
			return i, "", false
		}
		ps.Langs = langs

	default:
		if v.IsFunction() {
			p.fail(colon.Offset, "\":"+name+"\" takes no arguments")
			return i, "", false
		}
	}

	out.Pseudos = append(out.Pseudos, ps)
	return i + 1, "", true
}

// nth parses the argument of :nth-child() and its three siblings, which is an
// An+B optionally followed by "of" and a selector list.
func (p *selParser) nth(fn ComponentValue, name string, depth int) (AnB, []Selector, bool) {
	args := fn.Values

	// "of" splits the argument, and it is an identifier at the top level.
	split := -1
	for i, v := range args {
		if v.IsToken() && v.Token.Kind == Ident && strings.EqualFold(v.Token.Value, "of") {
			split = i
			break
		}
	}
	rest := args
	var of []Selector
	if split >= 0 {
		rest = args[:split]
		if name != "nth-child" && name != "nth-last-child" {
			p.fail(fn.Token.Offset, "\":"+name+"\" does not take \"of\"")
			return AnB{}, nil, false
		}
		var all bool
		of, all = p.list(args[split+1:], depth+1)
		// "of S" narrows which elements are counted, so dropping one of its
		// selectors changes the indices every other part of the rule depends on.
		// It is not forgiving.
		if !all || len(of) == 0 {
			p.fail(fn.Token.Offset, "\":"+name+"\" has no usable selector after \"of\"")
			return AnB{}, nil, false
		}
	}

	anb, ok := ParseAnB(rest)
	if !ok {
		p.fail(fn.Token.Offset, "\":"+name+"\" needs an An+B, such as 2n+1 or odd")
		return AnB{}, nil, false
	}
	return anb, of, true
}

// langs parses the argument of :lang(), which is one or more language ranges,
// written as identifiers or strings.
func (p *selParser) langs(fn ComponentValue) ([]string, bool) {
	var out []string
	for _, part := range splitOnComma(fn.Values) {
		part = trimWhitespace(part)
		if len(part) != 1 || !part[0].IsToken() {
			p.fail(fn.Token.Offset, "\":lang()\" takes language names, such as :lang(en)")
			return nil, false
		}
		t := part[0].Token
		if t.Kind != Ident && t.Kind != String {
			p.fail(t.Offset, "\":lang()\" takes language names, such as :lang(en)")
			return nil, false
		}
		out = append(out, t.Value)
	}
	if len(out) == 0 {
		p.fail(fn.Token.Offset, "\":lang()\" needs a language")
		return nil, false
	}
	return out, true
}

// attribute parses one "[...]" selector.
func (p *selParser) attribute(block ComponentValue) (Attr, bool) {
	vals := trimWhitespace(block.Values)
	if len(vals) == 0 {
		p.fail(block.Token.Offset, "an empty attribute selector")
		return Attr{}, false
	}

	// The name. A namespace may precede it as "ns|", "*|" or a bare "|", and
	// each has to be told from a malformed name — an author who wrote a
	// namespace wrote correct CSS this engine does not implement, and saying
	// "expected an attribute name" sends them hunting for a typo.
	if vals[0].IsToken() && (vals[0].Token.IsDelim('|') ||
		(vals[0].Token.IsDelim('*') && len(vals) > 1 &&
			vals[1].IsToken() && vals[1].Token.IsDelim('|'))) {
		p.unsupported(vals[0].Token.Offset, "namespaces in selectors are not implemented")
		return Attr{}, false
	}
	if !vals[0].IsToken() || vals[0].Token.Kind != Ident {
		p.fail(vals[0].Token.Offset, "expected an attribute name")
		return Attr{}, false
	}
	out := Attr{Name: vals[0].Token.Value}
	vals = trimWhitespace(vals[1:])

	if len(vals) == 0 {
		return out, true // "[a]"
	}

	// A namespace separator between the name and the operator.
	if vals[0].IsToken() && vals[0].Token.IsDelim('|') &&
		!(len(vals) > 1 && vals[1].IsToken() && vals[1].Token.IsDelim('=')) {
		p.unsupported(vals[0].Token.Offset, "namespaces in selectors are not implemented")
		return Attr{}, false
	}

	// The operator. All but "=" are two delimiters, because the current
	// specification has no single token for them.
	op, n, ok := attrOpAt(vals)
	if !ok {
		p.fail(vals[0].Token.Offset, "expected =, ~=, |=, ^=, $= or *= after the attribute name")
		return Attr{}, false
	}
	out.Op = op
	vals = trimWhitespace(vals[n:])

	// The value.
	if len(vals) == 0 {
		p.fail(block.Token.Offset, "the attribute selector has no value after \""+op.String()+"\"")
		return Attr{}, false
	}
	if !vals[0].IsToken() || (vals[0].Token.Kind != Ident && vals[0].Token.Kind != String) {
		p.fail(vals[0].Token.Offset, "an attribute value must be a name or a quoted string")
		return Attr{}, false
	}
	out.Value = vals[0].Token.Value
	vals = trimWhitespace(vals[1:])

	// The optional case flag.
	if len(vals) == 0 {
		return out, true
	}
	if len(vals) > 1 || !vals[0].IsToken() || vals[0].Token.Kind != Ident {
		p.fail(vals[0].Token.Offset, "unexpected extra content in an attribute selector")
		return Attr{}, false
	}
	switch strings.ToLower(vals[0].Token.Value) {
	case "i":
		out.Insensitive = true
	case "s":
		out.Insensitive = false
	default:
		p.fail(vals[0].Token.Offset, "expected \"i\" or \"s\" after the attribute value")
		return Attr{}, false
	}
	return out, true
}

// attrOpAt reads the comparison operator, returning how many values it spans.
func attrOpAt(vals []ComponentValue) (AttrOp, int, bool) {
	if !vals[0].IsToken() {
		return 0, 0, false
	}
	if vals[0].Token.IsDelim('=') {
		return AttrEquals, 1, true
	}
	if len(vals) < 2 || !vals[1].IsToken() || !vals[1].Token.IsDelim('=') {
		return 0, 0, false
	}
	switch {
	case vals[0].Token.IsDelim('~'):
		return AttrIncludes, 2, true
	case vals[0].Token.IsDelim('|'):
		return AttrDashMatch, 2, true
	case vals[0].Token.IsDelim('^'):
		return AttrPrefix, 2, true
	case vals[0].Token.IsDelim('$'):
		return AttrSuffix, 2, true
	case vals[0].Token.IsDelim('*'):
		return AttrSubstring, 2, true
	}
	return 0, 0, false
}

// specificityOf computes (a, b, c) per Selectors Level 4 §17.
func specificityOf(s Selector) Specificity {
	var out Specificity
	for _, c := range s.Compounds {
		out = out.add(compoundSpecificity(c))
	}
	if s.PseudoElement != "" {
		out.C++
	}
	return out
}

func compoundSpecificity(c Compound) Specificity {
	var out Specificity
	out.A += len(c.IDs)
	out.B += len(c.Classes) + len(c.Attrs)
	if c.Type != "" {
		out.C++
	}
	// The universal selector contributes nothing, which is why "*" loses to
	// every other selector rather than tying with a type.
	for _, ps := range c.Pseudos {
		out = out.add(pseudoSpecificity(ps))
	}
	return out
}

func pseudoSpecificity(ps Pseudo) Specificity {
	switch ps.Kind {
	case PseudoWhere:
		// :where() is the whole point of :where(): it contributes nothing, so a
		// rule can be broadly targeted and still easy to override.
		return Specificity{}

	case PseudoNot, PseudoIs:
		// The specificity of the most specific argument, and nothing for the
		// pseudo-class itself.
		return mostSpecific(ps.Args)

	case PseudoNthChild, PseudoNthLastChild:
		// A pseudo-class, plus the most specific of the "of" list.
		return Specificity{0, 1, 0}.add(mostSpecific(ps.Of))
	}
	return Specificity{0, 1, 0}
}

func mostSpecific(sels []Selector) Specificity {
	var out Specificity
	for _, s := range sels {
		out = out.max(s.Specificity)
	}
	return out
}
