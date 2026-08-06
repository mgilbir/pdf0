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

// computeCounters walks the document and records what each element sees.
//
// The value an element sees is the one *after* its own resets and increments,
// because that is what its marker and its ::before and ::after content use: "li
// { counter-increment: item } li::before { content: counter(item) }" must show
// the item's own number, not the previous one's.
func computeCounters(root *html.Node, styles map[*html.Node]style.ComputedStyle,
	pseudo map[style.PseudoKey]style.ComputedStyle) map[*html.Node]counterValues {

	out := map[*html.Node]counterValues{}
	state := newCounterState()

	var walk func(n *html.Node, depth int)
	walk = func(n *html.Node, depth int) {
		if n.Type == html.ElementNode {
			state.enter(depth)
			cs := styles[n]
			// Reset before increment: "counter-reset: n 0; counter-increment: n"
			// on one element yields 1, and the specification fixes the order
			// rather than leaving it to the declaration order.
			for _, r := range parseCounterList(cs["counter-reset"], 0) {
				state.reset(r.name, r.value, depth)
			}
			for _, r := range parseCounterList(cs["counter-increment"], 1) {
				state.increment(r.name, r.value, depth)
			}
			// A pseudo-element may carry its own, and ::before comes before the
			// element's content in document order.
			if ps, ok := pseudo[style.PseudoKey{Node: n, Name: "before"}]; ok {
				for _, r := range parseCounterList(ps["counter-reset"], 0) {
					state.reset(r.name, r.value, depth)
				}
				for _, r := range parseCounterList(ps["counter-increment"], 1) {
					state.increment(r.name, r.value, depth)
				}
			}
			out[n] = state.snapshot()
		}
		for _, child := range n.Children {
			walk(child, depth+1)
		}
	}
	walk(root, 0)
	return out
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
