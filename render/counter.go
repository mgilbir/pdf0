package render

import (
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
	"github.com/mgilbir/pdf0/style"
)

// CSS counters, CSS 2.1 §12.4.
//
// A counter is the only piece of state in the cascade. Everything else about an
// element is decided by the element and its ancestors; a counter's value depends
// on how many elements came *before* it in the document, which is why this is a
// walk of its own in document order rather than something the box builder can
// answer while descending.
//
// # Scope, which is the part that is easy to get wrong
//
// "counter-reset" does not set a counter, it *creates* one. The new counter is
// in scope for the element, its descendants, and its following siblings and
// their descendants — and it hides any counter of the same name from an
// enclosing scope rather than overwriting it. That is what makes nested lists
// number independently, and what makes counters() able to produce "2.1.3": the
// three values are three counters of the same name, all alive at once, one per
// level.
//
// So the state is a stack per name rather than a value per name, and each entry
// remembers the depth of the element that created it. Leaving that depth pops
// it. A previous sibling's counter is kept — it was created at the same depth,
// and siblings share a scope — which is the single rule that separates this from
// a plain "reset on enter, restore on exit" and the reason the comparison is on
// depth rather than on identity.

// counterEntry is one counter in one scope.
type counterEntry struct {
	value int
	// depth is the tree depth of the element whose counter-reset created it. It
	// is what decides when the entry leaves scope.
	depth int
}

// counterState is the set of counters visible at a point in the walk.
type counterState struct {
	stacks map[string][]counterEntry
}

// counterValues is what a counter() or counters() at an element resolves to.
type counterValues map[string][]int

// maxCounterDepth bounds how many counters of one name may be alive at once.
//
// The stack grows with nesting, and nesting is attacker-controlled. The HTML
// parser already caps element depth, so this cannot be reached through markup
// alone; it is here because the two limits are in different packages and a
// change to one should not silently uncap the other.
const maxCounterDepth = 512

// maxCounterNames bounds how many distinct counters a document may have.
//
// Each name is a map key taken from the document text, so without this a
// stylesheet of a hundred thousand distinct counter-reset names is a hundred
// thousand live maps. The number is far past any real document.
const maxCounterNames = 1024

func newCounterState() *counterState {
	return &counterState{stacks: map[string][]counterEntry{}}
}

// enter drops the counters created below the depth being entered.
//
// Popping on the way *down* rather than on the way back up is what lets a
// following sibling see a counter its predecessor created: that entry sits at
// the same depth, so it survives, while anything created deeper does not.
func (c *counterState) enter(depth int) {
	for name, stack := range c.stacks {
		n := len(stack)
		for n > 0 && stack[n-1].depth > depth {
			n--
		}
		if n == 0 {
			delete(c.stacks, name)
			continue
		}
		c.stacks[name] = stack[:n]
	}
}

// reset creates a counter in the scope of an element at the given depth.
//
// A second counter-reset of the same name at the same depth replaces the first
// rather than nesting inside it — they are the same scope, and two counters
// could not be told apart there.
func (c *counterState) reset(name string, value, depth int) {
	stack := c.stacks[name]
	if n := len(stack); n > 0 && stack[n-1].depth == depth {
		stack[n-1].value = value
		return
	}
	if len(c.stacks) >= maxCounterNames && stack == nil {
		return
	}
	if len(stack) >= maxCounterDepth {
		return
	}
	c.stacks[name] = append(stack, counterEntry{value: value, depth: depth})
}

// increment adds to the innermost counter of a name.
//
// A counter that is not in scope is created on the spot with value zero before
// being incremented, which is what §12.4.3 requires — and is why "li {
// counter-increment: item }" numbers a list even with no counter-reset anywhere.
func (c *counterState) increment(name string, by, depth int) {
	stack := c.stacks[name]
	if len(stack) == 0 {
		c.reset(name, 0, depth)
		stack = c.stacks[name]
		if len(stack) == 0 {
			// Refused by a cap.
			return
		}
	}
	n := len(stack) - 1
	// Saturating, because the increment is a number out of the document and a
	// stylesheet asking for two billion twice should not wrap to a negative
	// count.
	sum := int64(stack[n].value) + int64(by)
	switch {
	case sum > 1<<31-1:
		stack[n].value = 1<<31 - 1
	case sum < -(1 << 31):
		stack[n].value = -(1 << 31)
	default:
		stack[n].value = int(sum)
	}
}

// snapshot records every value of every counter in scope, outermost first.
//
// All of them, rather than the innermost, because counters() needs the whole
// chain — "2.1.3" is one counter name at three levels.
func (c *counterState) snapshot() counterValues {
	if len(c.stacks) == 0 {
		return nil
	}
	out := make(counterValues, len(c.stacks))
	for name, stack := range c.stacks {
		vals := make([]int, len(stack))
		for i, e := range stack {
			vals[i] = e.value
		}
		out[name] = vals
	}
	return out
}

// counterSnapshots is what every box that can name a counter sees.
//
// Elements and pseudo-elements are kept apart because they see different things:
// a ::before that resets a counter of its own must show its own value in its
// content, and the element it hangs from must not — the pseudo-element is a
// child of the element, so its scope is nested inside.
type counterSnapshots struct {
	elements map[*html.Node]counterValues
	pseudo   map[style.PseudoKey]counterValues
	// quoteDepth is the level of quotation nesting each pseudo-element's content
	// begins at. It rides along with the counters because it is the same kind of
	// value — see quotes.go — and because threading it through a second walk of
	// the same tree in the same order would be two chances to disagree about
	// document order rather than one.
	quoteDepth map[style.PseudoKey]int
}

// computeCounters walks the document and records what each box sees.
//
// The value an element sees is the one *after* its own resets and increments,
// because that is what its marker uses: "li { counter-increment: item }" must
// show the item's own number, not the previous one's.
//
// # A pseudo-element is a child, not a second helping of its element
//
// §12.4.1's scope is a tree scope, and ::before and ::after are in the tree: they
// are the element's first and last children. Applying their counter-reset in the
// *element's* scope instead — which is what this did — is wrong in a way that
// only shows when both carry a counter of the same name, and then it is wrong
// loudly. "html { counter-reset: c 0 c 4 c 0; counter-increment: c 1 c 3 }" with
// "html::before { counter-reset: c 9999; counter-increment: c 9999 }" must leave
// html's counter at 4, because the 9999 is a *nested* counter that dies with the
// pseudo-element; sharing the scope made the reset overwrite html's own and the
// document numbered from 19998.
//
// Document order decides the rest: ::before applies before the element's
// children and ::after after them, so a counter either of them creates is in
// scope for its following siblings exactly as any other child's would be.
//
// # What does not count
//
// §12.4.1 excludes two things and both of them are the difference between a rule
// that reads right and a document that numbers wrong:
//
//   - An element with "display: none" cannot reset or increment a counter, and
//     neither can anything inside it. It is not in the formatting structure at
//     all, so there is nothing to number.
//   - A pseudo-element that generates no box does not either, and "generates no
//     box" is decided by its content: the initial value is "normal", so *every*
//     element in every document has a ::before rule's worth of declarations
//     sitting on it that must do nothing. A stylesheet saying only
//     "#one::before { counter-increment: c }" increments nothing at all.
//
// "visibility: hidden" is deliberately not here. A hidden box is laid out and
// takes its room; only display removes it.
func computeCounters(root *html.Node, styles map[*html.Node]style.ComputedStyle,
	pseudo map[style.PseudoKey]style.ComputedStyle) counterSnapshots {

	out := counterSnapshots{
		elements:   map[*html.Node]counterValues{},
		pseudo:     map[style.PseudoKey]counterValues{},
		quoteDepth: map[style.PseudoKey]int{},
	}
	state := newCounterState()
	// §12.3.1's level of quotation, which runs across the whole document in
	// document order and belongs to no element.
	depthOfQuotes := 0

	// apply runs one box's declarations in one scope. Reset before increment:
	// "counter-reset: n 0; counter-increment: n" on one element yields 1, and the
	// specification fixes the order rather than leaving it to the declaration
	// order.
	apply := func(cs style.ComputedStyle, depth int) {
		for _, r := range parseCounterList(cs["counter-reset"], 0) {
			state.reset(r.name, r.value, depth)
		}
		for _, r := range parseCounterList(cs["counter-increment"], 1) {
			state.increment(r.name, r.value, depth)
		}
	}
	// atPseudo applies one pseudo-element's declarations in a scope of its own
	// and records what its content() will see.
	atPseudo := func(n *html.Node, name string, depth int) {
		key := style.PseudoKey{Node: n, Name: name}
		cs, ok := pseudo[key]
		if !ok || !generatesPseudoBox(cs) {
			return
		}
		state.enter(depth)
		apply(cs, depth)
		out.pseudo[key] = state.snapshot()
		// The depth this pseudo-element's content *starts* at, recorded before
		// its own keywords move it: "content: open-quote" draws the mark for the
		// level it is opening, not for the one it leaves behind.
		out.quoteDepth[key] = depthOfQuotes
		depthOfQuotes = quoteDepthAfter(cs["content"], depthOfQuotes, parseQuotes(cs["quotes"]))
	}

	var walk func(n *html.Node, depth int)
	walk = func(n *html.Node, depth int) {
		if n.Type == html.ElementNode {
			cs := styles[n]
			if displayIsNone(cs) {
				return
			}
			state.enter(depth)
			apply(cs, depth)
			out.elements[n] = state.snapshot()
			atPseudo(n, "before", depth+1)
		}
		for _, child := range n.Children {
			walk(child, depth+1)
		}
		if n.Type == html.ElementNode {
			atPseudo(n, "after", depth+1)
		}
	}
	walk(root, 0)
	return out
}

// displayIsNone reports the one display value that takes a box out of the
// formatting structure rather than merely changing its shape.
func displayIsNone(cs style.ComputedStyle) bool {
	outer, _, _ := displayOf(cs)
	return outer == OuterNone
}

// generatesPseudoBox reports whether a ::before or ::after produces a box, which
// is what decides whether its counters happen at all.
//
// The content value is read here rather than through resolveContent because the
// question is asked before any counter has a value — and it can be, since the
// three values that mean "no box" are the three that need nothing resolved.
func generatesPseudoBox(cs style.ComputedStyle) bool {
	if displayIsNone(cs) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cs["content"])) {
	case "", "normal", "none":
		return false
	}
	return true
}

// counterRequest is one name-and-number pair from a counter-reset or
// counter-increment.
type counterRequest struct {
	name  string
	value int
}

// parseCounterList reads "chapter section 2 note -1".
//
// A name may be followed by a number; when it is not, the default applies — zero
// for a reset and one for an increment, which is why the caller passes it.
func parseCounterList(raw string, byDefault int) []counterRequest {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "none") {
		return nil
	}
	vals, _ := css.ParseComponentValues(raw)
	var out []counterRequest
	for _, v := range vals {
		if !v.IsToken() {
			continue
		}
		switch v.Token.Kind {
		case css.Whitespace:
		case css.Ident:
			if len(out) >= maxCounterNames {
				return out
			}
			out = append(out, counterRequest{name: v.Token.Value, value: byDefault})
		case css.Number:
			// The number belongs to the name before it. One with no name before
			// it is a malformed declaration, and dropping it is what the
			// specification's grammar does.
			if len(out) > 0 {
				out[len(out)-1].value = int(v.Token.Number)
			}
		default:
			// Anything else makes the declaration invalid.
			return nil
		}
	}
	return out
}

// formatCounter renders one counter value in a list style.
//
// It shares markerText with list markers deliberately: "counter(n, upper-roman)"
// and "list-style-type: upper-roman" are the same numbering in the
// specification, and two implementations of it would drift.
func formatCounter(value int, listStyle string) string {
	if listStyle == "" {
		listStyle = "decimal"
	}
	if strings.EqualFold(strings.TrimSpace(listStyle), "none") {
		return ""
	}
	if text := markerText(listStyle, value); text != "" {
		return strings.TrimSuffix(text, ".")
	}
	return strconv.Itoa(value)
}
