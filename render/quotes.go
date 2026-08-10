package render

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
)

// Quotation marks: CSS 2.1 §12.3, the "quotes" property and the four keywords
// that draw from it.
//
// # Why this is a walk and not a lookup
//
// "content: open-quote" does not say which mark to draw. It says *a* mark, at
// the current level of nesting, out of a list the "quotes" property supplies —
// so a document quoting inside a quotation gets the inner pair without saying
// so anywhere. The level is the number of quotes opened and not yet closed at
// that point in the document, which makes it the same kind of value a counter
// is: state that depends on everything before it in document order and that no
// element can answer about itself. It is threaded through the same walk for
// exactly that reason, and counter.go's note on why that walk exists applies
// unchanged.
//
// # The four keywords are two decisions, not four
//
// Each keyword answers "open or close?" and "draw it or not?" independently, and
// writing them as the pair rather than as four cases is what keeps the depth
// arithmetic in one place. no-open-quote is an open-quote that draws nothing —
// it still deepens the nesting, which is the whole point of it: a stylesheet
// suppressing the outer quotation mark of a run-on paragraph must not thereby
// give the inner quotation the outer pair.

// quotePair is one level's opening and closing mark.
type quotePair struct{ open, close string }

// quoteList is the "quotes" property: a pair per level of nesting, outermost
// first.
//
// An empty list is "quotes: none", which is not the same as having no
// declaration: it means the keywords still count the nesting and draw nothing.
type quoteList []quotePair

// maxQuotePairs bounds how many levels a "quotes" declaration may name.
//
// The list comes out of the document and every pair holds two strings taken
// from it, so without a bound a stylesheet is a list as long as it likes. Past
// the last pair the deepest one repeats, so a document nested deeper than this
// is not mis-rendered by the cap — it gets the same mark it would have got from
// the pair the cap dropped, which is the specification's own answer for running
// off the end.
const maxQuotePairs = 256

// maxQuoteDepth bounds the level of nesting.
//
// Depth is attacker-controlled twice over: once by how many open-quotes a
// content value names and once by how many elements match the rule. It indexes
// nothing unbounded — past the last pair the last pair repeats — so this is not
// what stops a bad index; it is what stops the count itself from running away
// on a document built to make it, and it saturates rather than wrapping because
// a depth that wrapped negative would read as "no quotation open" and let a
// close-quote draw an opening mark.
const maxQuoteDepth = 1 << 20

// parseQuotes reads a "quotes" value into its pairs.
//
// A malformed value never reaches here: §4.2 drops the declaration where the
// sheet is prepared, so what arrives is "none" or an even number of strings. It
// is defensive about it anyway, because the initial value travels the same path
// and a computed style can be built by hand in a test.
func parseQuotes(raw string) quoteList {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "none") {
		return nil
	}
	strs := quoteStrings(trimmed)
	if !pairable(strs) {
		return nil
	}
	out := make(quoteList, 0, len(strs)/2)
	for i := 0; i+1 < len(strs) && len(out) < maxQuotePairs; i += 2 {
		out = append(out, quotePair{open: strs[i], close: strs[i+1]})
	}
	return out
}

// pairable reports that a list of strings can be read as pairs.
func pairable(strs []string) bool { return len(strs) >= 2 && len(strs)%2 == 0 }

// quoteStrings pulls the string tokens out of a "quotes" value, or nil if it
// holds anything else.
//
// Anything else makes the value invalid rather than partly usable: "quotes: 'a'
// 5 'b'" is not a declaration with a stray number in it, and taking the two
// strings out of it would render a document neither the author nor the
// specification asked for.
func quoteStrings(raw string) []string {
	vals, _ := css.ParseComponentValues(raw)
	var out []string
	for _, v := range vals {
		if !v.IsToken() {
			return nil
		}
		switch v.Token.Kind {
		case css.Whitespace:
		case css.String:
			if len(out) >= 2*maxQuotePairs {
				// Past what will be kept. Reading the rest would decide nothing
				// and is work in proportion to the document.
				return out
			}
			out = append(out, v.Token.Value)
		default:
			return nil
		}
	}
	return out
}

// quoteOp is one quote keyword's effect: which end of a quotation it is, and
// whether it draws anything.
type quoteOp struct{ opening, draws bool }

// quoteKeyword reads one of the four keywords.
func quoteKeyword(ident string) (quoteOp, bool) {
	switch strings.ToLower(ident) {
	case "open-quote":
		return quoteOp{opening: true, draws: true}, true
	case "close-quote":
		return quoteOp{opening: false, draws: true}, true
	case "no-open-quote":
		return quoteOp{opening: true, draws: false}, true
	case "no-close-quote":
		return quoteOp{opening: false, draws: false}, true
	}
	return quoteOp{}, false
}

// applyQuote is the whole of §12.3.1's arithmetic: what a keyword draws at a
// depth, and what the depth becomes.
//
// The two ends are not symmetric and the asymmetry is the part worth stating.
// An opening mark is the one *at* the current depth and then the depth grows; a
// closing mark takes the depth back down first and then draws the mark that
// belongs to the level being left. Doing either the other way round pairs the
// outer opening mark with the inner closing one, which reads as correct on a
// single quotation and goes wrong the moment one nests.
//
// A close with nothing open is an error, and §12.3.1 lets the user agent render
// nothing and leave the depth at zero. That is what happens here: the
// alternative is a negative depth, which would index from the wrong end of the
// list on the next open-quote.
func applyQuote(op quoteOp, depth int, quotes quoteList) (text string, next int) {
	if op.opening {
		if op.draws {
			text = quotes.open(depth)
		}
		next = depth + 1
		if next > maxQuoteDepth {
			next = maxQuoteDepth
		}
		return text, next
	}
	if depth <= 0 {
		return "", 0
	}
	next = depth - 1
	if op.draws {
		text = quotes.close(next)
	}
	return text, next
}

// open and close are the marks for a level.
//
// A level past the end of the list gets the last pair, which is §12.3.2's rule
// for a quotation nested deeper than the author provided for — and is why a
// two-pair list still renders a document quoted five deep rather than dropping
// the marks.
func (q quoteList) open(depth int) string  { return q.at(depth).open }
func (q quoteList) close(depth int) string { return q.at(depth).close }

func (q quoteList) at(depth int) quotePair {
	if len(q) == 0 {
		// "quotes: none". The keywords still count, and draw nothing.
		return quotePair{}
	}
	if depth < 0 {
		depth = 0
	}
	if depth >= len(q) {
		depth = len(q) - 1
	}
	return q[depth]
}

// quoteDepthAfter is what the depth becomes once a content value has been read.
//
// The walk needs this without needing the text, because it runs before any
// counter has a value and before any attribute has been looked at — and because
// the depth is what it is threading. It shares applyQuote with the text
// production rather than counting opens and closes, since the arithmetic is not
// a net count: "close-quote close-quote open-quote" from a depth of one ends at
// one and not at zero, because the second close had nothing to close.
func quoteDepthAfter(raw string, depth int, quotes quoteList) int {
	trimmed := strings.TrimSpace(raw)
	switch strings.ToLower(trimmed) {
	case "", "normal", "none":
		return depth
	}
	vals, _ := css.ParseComponentValues(trimmed)
	for _, v := range vals {
		if !v.IsToken() || v.Token.Kind != css.Ident {
			continue
		}
		if op, isQuote := quoteKeyword(v.Token.Value); isQuote {
			_, depth = applyQuote(op, depth, quotes)
		}
	}
	return depth
}
