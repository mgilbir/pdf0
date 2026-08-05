package css

import "strings"

// The parser of CSS Syntax Level 3 §5: the layer that turns a flat token stream
// into rules, declarations and the nested component values they are built from.
//
// Like the tokenizer, this follows the specification's algorithms step for step,
// and for the same reason: every place a reader disagrees with a browser about
// where a rule ends is a rendering bug that testing the output cannot localise.
//
// # What this layer does and does not decide
//
// It decides *structure* — that "a > b" is a prelude of five component values
// and that "color: red" is a declaration named "color". It decides nothing about
// *meaning*: it does not know that "color" is a property, that "a > b" is a
// selector, or that "@media" takes a query. That is deliberate and it is what
// the specification's own layering says. A stylesheet full of properties this
// engine has never heard of parses here exactly as well as one full of
// properties it implements, which is what lets the layer above report each
// unsupported declaration rather than failing the file (see the proposal's
// §6.3).
//
// # Recovery
//
// Parsing never fails, for the same reason tokenizing never fails: one broken
// rule must not cost the author the rest of the stylesheet. Every malformed
// construct has a defined recovery, and each one is reported as an Error so a
// caller can say what was wrong without the reading of the file depending on it.

// maxNestingDepth bounds how deeply blocks and functions may nest.
//
// Each level of "(", "[", "{" or a function costs a stack frame, and a
// stylesheet is untrusted input: a few hundred kilobytes of "(((((((..." would
// otherwise exhaust the goroutine stack, which aborts the process in a way no
// recover can catch. Real CSS nests a handful of levels — calc(1px + var(--x))
// is three — so this is far above anything an author writes and far below
// anything that hurts.
//
// Hitting it is not an error the way a PDF's depth cap is. Parsing stays total:
// the offending block is consumed to its matching close so the token stream
// stays in step, its contents are dropped, and the trip is reported. See
// skipBlock.
const maxNestingDepth = 128

// A ComponentValue is one node of a parsed stylesheet: a preserved token, a
// function call, or a block delimited by (), [] or {}.
//
// Which of the three it is, is read off Token.Kind, and no separate tag is
// needed because the parser never leaves the ambiguous kinds preserved. A
// Function token always became a function, and an opening delimiter always
// became a block, so:
//
//   - Token.Kind == Function — a function. Token.Value is its name, Values its
//     arguments.
//   - Token.Kind == LeftParen, LeftSquare or LeftBrace — a block. Values is its
//     contents.
//   - anything else — a preserved token, and Values is nil.
//
// The methods below say the same thing without the caller having to remember it.
type ComponentValue struct {
	// Token is the preserved token, the function token, or the block's opening
	// delimiter, depending on which of the three this node is.
	Token Token

	// Values is the contents of a block or the arguments of a function, and nil
	// for a preserved token.
	//
	// A block's delimiters are not in it: they are Token and its mirror, and
	// the closing one may not have been present at all if the input ended
	// early. This is why the closing delimiter is not kept — after recovery
	// there may not be one to keep.
	Values []ComponentValue
}

// IsFunction reports whether this node is a function call.
func (c ComponentValue) IsFunction() bool { return c.Token.Kind == Function }

// IsBlock reports whether this node is a (), [] or {} block.
func (c ComponentValue) IsBlock() bool {
	switch c.Token.Kind {
	case LeftParen, LeftSquare, LeftBrace:
		return true
	}
	return false
}

// IsToken reports whether this node is a preserved token — neither a function
// nor a block.
func (c ComponentValue) IsToken() bool { return !c.IsFunction() && !c.IsBlock() }

// A Declaration is a property name and the value assigned to it.
type Declaration struct {
	// Name is the property name as written, with escapes resolved and the case
	// as the author typed it. Property names are matched case-insensitively, so
	// a caller comparing this must fold case rather than compare directly.
	Name string

	// Value is the component values between the colon and the end of the
	// declaration, with the "!important" removed if it was there and with
	// leading and trailing whitespace stripped. Whitespace *within* the value
	// is kept, because it separates the parts of a shorthand.
	Value []ComponentValue

	// Important reports that the declaration ended with "!important", which
	// changes where it sorts in the cascade.
	Important bool

	// Offset is the byte offset in the source at which the property name
	// begins.
	Offset int
}

// A Rule is either a qualified rule — a style rule, whose prelude is a selector
// list — or an at-rule such as @media or @page.
//
// The two are one type because the parser cannot tell what either means: at this
// layer a qualified rule is "some component values, then a {} block", and that
// is all that distinguishes it from an at-rule beginning with "@".
type Rule struct {
	// At reports whether this is an at-rule. It is a field rather than a test
	// on Name because it is the one thing that genuinely separates the two
	// kinds, and reading it should not depend on knowing that a qualified
	// rule's name is empty.
	At bool

	// Name is an at-rule's name without the "@" — "media", "page", "import".
	// It is empty for a qualified rule.
	Name string

	// Prelude is everything before the block: an at-rule's parameters, or a
	// qualified rule's selector list, still unparsed.
	Prelude []ComponentValue

	// Block is the contents of the {} block, and HasBlock says whether there
	// was one. The two are separate because "@import url(x);" has no block and
	// "@media print {}" has an empty one, and a caller has to tell them apart.
	Block    []ComponentValue
	HasBlock bool

	// Offset is the byte offset in the source at which the rule begins.
	Offset int
}

// ParseComponentValues parses a list of component values (§5.3.10).
//
// This is the entry point for anything that is a *value* rather than a
// stylesheet: the prelude of a rule, the value of a declaration, the arguments
// of a media query.
func ParseComponentValues(input string) ([]ComponentValue, []Error) {
	p := newParser(input)
	return p.componentValues(), p.errs
}

// ParseStylesheet parses a stylesheet (§5.3.3): a list of rules, at the top
// level, where "<!--" and "-->" are ignored rather than being read as the start
// of a qualified rule.
func ParseStylesheet(input string) ([]Rule, []Error) {
	p := newParser(input)
	return p.rules(true), p.errs
}

// ParseRules parses a list of rules that is not a whole stylesheet (§5.3.4) —
// the contents of an @media block, say. The difference from ParseStylesheet is
// only the handling of "<!--" and "-->", which are historical and which a nested
// context has no reason to ignore.
func ParseRules(input string) ([]Rule, []Error) {
	p := newParser(input)
	return p.rules(false), p.errs
}

// ParseDeclarations parses a list of declarations (§5.3.6): the contents of a
// style rule's block.
//
// Declarations and at-rules are returned separately rather than interleaved,
// because an at-rule inside a declaration block is CSS Nesting, which this
// engine does not implement — the layer above reports each one as unsupported
// rather than acting on it. Both carry an Offset, so a caller that does need
// source order can recover it without this returning a sum type that every
// caller would then have to switch on.
func ParseDeclarations(input string) ([]Declaration, []Rule, []Error) {
	p := newParser(input)
	decls, rules := p.declarations()
	return decls, rules, p.errs
}

// ParseDeclarationValues is ParseDeclarations over already-parsed input, which
// is what reading the block of a rule that has already been parsed needs.
//
// Re-tokenizing the block's source text instead would be wrong as well as
// wasteful: the nesting was worked out once already, and a second pass over text
// that was recovered from — an unclosed function, say — need not reach the same
// answer.
func ParseDeclarationValues(block []ComponentValue) ([]Declaration, []Rule, []Error) {
	p := &parser{vals: block}
	decls, rules := p.declarations()
	return decls, rules, p.errs
}

// ParseRulesFromValues is ParseRules over already-parsed component values, which
// is what reading the body of an @media block needs.
func ParseRulesFromValues(block []ComponentValue) ([]Rule, []Error) {
	p := &parser{vals: block}
	return p.rules(false), p.errs
}

// parser walks either a token stream or an already-parsed list of component
// values.
//
// Two inputs rather than one because the specification's algorithms are defined
// over both: a stylesheet is parsed from tokens, while the block of a rule that
// has already been parsed is a list of component values, and re-tokenizing it
// would lose the nesting that was already worked out. Which one is live is
// decided by fromValues.
type parser struct {
	toks []Token
	vals []ComponentValue

	pos   int
	depth int
	errs  []Error
}

func newParser(input string) *parser {
	toks, errs := Tokenize(input)
	return &parser{toks: toks, errs: errs}
}

// fromValues reports whether this parser walks component values rather than
// tokens.
func (p *parser) fromValues() bool { return p.toks == nil }

func (p *parser) fail(off int, msg string) {
	switch {
	case len(p.errs) > maxErrors:
		return
	case len(p.errs) == maxErrors:
		p.errs = append(p.errs, Error{
			Offset:  off,
			Message: "further problems in this stylesheet were not reported",
		})
	default:
		p.errs = append(p.errs, Error{Offset: off, Message: msg})
	}
}

// peek returns the next node without consuming it. Past the end it returns an
// EOF token, so no caller has to bounds-check.
func (p *parser) peek() ComponentValue {
	if p.fromValues() {
		if p.pos < len(p.vals) {
			return p.vals[p.pos]
		}
		return ComponentValue{Token: Token{Kind: EOF, Offset: p.endOffset()}}
	}
	if p.pos < len(p.toks) {
		return ComponentValue{Token: p.toks[p.pos]}
	}
	return ComponentValue{Token: Token{Kind: EOF, Offset: p.endOffset()}}
}

// endOffset is where the input ended, for a diagnostic that has run off the end.
func (p *parser) endOffset() int {
	if n := len(p.toks); n > 0 {
		return p.toks[n-1].Offset
	}
	if n := len(p.vals); n > 0 {
		return p.vals[n-1].Token.Offset
	}
	return 0
}

func (p *parser) next() ComponentValue {
	c := p.peek()
	if c.Token.Kind != EOF {
		p.pos++
	}
	return c
}

func (p *parser) atEOF() bool { return p.peek().Token.Kind == EOF }

func (p *parser) skipWhitespace() {
	for p.peek().Token.Kind == Whitespace {
		p.pos++
	}
}

// componentValues consumes to the end of the input (§5.4.7 in the list form).
func (p *parser) componentValues() []ComponentValue {
	var out []ComponentValue
	for !p.atEOF() {
		out = append(out, p.componentValue())
	}
	return out
}

// componentValue consumes one component value (§5.4.6): a block, a function, or
// a preserved token.
func (p *parser) componentValue() ComponentValue {
	c := p.next()

	// A parser walking component values has already done this work: what it
	// holds are blocks and functions, not the delimiters that open them.
	if p.fromValues() {
		return c
	}

	switch c.Token.Kind {
	case LeftBrace, LeftSquare, LeftParen:
		return p.block(c.Token)
	case Function:
		return p.function(c.Token)
	}
	return c
}

// mirror is the token that closes what open opens.
func mirror(open Kind) Kind {
	switch open {
	case LeftBrace:
		return RightBrace
	case LeftSquare:
		return RightSquare
	case LeftParen:
		return RightParen
	}
	return EOF
}

// block consumes a simple block (§5.4.7), the opening delimiter already
// consumed.
func (p *parser) block(open Token) ComponentValue {
	if p.depth >= maxNestingDepth {
		p.fail(open.Offset, "blocks are nested too deeply to read")
		p.skipBlock(mirror(open.Kind))
		return ComponentValue{Token: open}
	}
	p.depth++
	defer func() { p.depth-- }()

	end := mirror(open.Kind)
	out := ComponentValue{Token: open}
	for {
		switch c := p.peek(); {
		case c.Token.Kind == end:
			p.pos++
			return out
		case c.Token.Kind == EOF:
			// An unclosed block still yields what it held. Discarding it would
			// throw away every rule in a stylesheet whose last brace is
			// missing, which is the commonest way a stylesheet is broken.
			p.fail(open.Offset, "a block that is never closed")
			return out
		default:
			out.Values = append(out.Values, p.componentValue())
		}
	}
}

// function consumes a function (§5.4.8), the function token already consumed.
func (p *parser) function(name Token) ComponentValue {
	if p.depth >= maxNestingDepth {
		p.fail(name.Offset, "functions are nested too deeply to read")
		p.skipBlock(RightParen)
		return ComponentValue{Token: name}
	}
	p.depth++
	defer func() { p.depth-- }()

	out := ComponentValue{Token: name}
	for {
		switch c := p.peek(); {
		case c.Token.Kind == RightParen:
			p.pos++
			return out
		case c.Token.Kind == EOF:
			p.fail(name.Offset, "a function call that is never closed")
			return out
		default:
			out.Values = append(out.Values, p.componentValue())
		}
	}
}

// skipBlock discards input up to and including the delimiter that closes the
// block already open, without recursing.
//
// This is what keeps the depth cap from being a correctness bug. Stopping at the
// first close delimiter would leave the reader inside a nested block, and every
// rule after it would be misparsed; recursing to find the right one is the
// stack exhaustion the cap exists to prevent. So nesting is counted on the heap
// instead, and the cap costs only the contents of one absurdly nested block.
func (p *parser) skipBlock(end Kind) {
	depth := 1
	for !p.atEOF() {
		c := p.next()
		if p.fromValues() {
			// Already-parsed values carry their nesting in the tree rather than
			// in the stream, so one node is one block and there is nothing to
			// count.
			continue
		}
		switch c.Token.Kind {
		case end:
			if depth--; depth == 0 {
				return
			}
		case LeftBrace, LeftSquare, LeftParen, Function:
			// Only a delimiter of the same kind nests: a "[" inside a "(" block
			// does not change how many ")" are needed to leave it. The mirror
			// check keeps the count honest.
			if mirror(c.Token.Kind) == end || (c.Token.Kind == Function && end == RightParen) {
				depth++
			}
		}
	}
}

// rules consumes a list of rules (§5.4.1). At the top level of a stylesheet
// "<!--" and "-->" are skipped; anywhere else they begin a qualified rule.
func (p *parser) rules(topLevel bool) []Rule {
	var out []Rule
	for {
		switch c := p.peek(); {
		case c.Token.Kind == EOF:
			return out

		case c.Token.Kind == Whitespace:
			p.pos++

		case c.Token.Kind == CDO || c.Token.Kind == CDC:
			if topLevel {
				p.pos++
				continue
			}
			if r, ok := p.qualifiedRule(); ok {
				out = append(out, r)
			}

		case c.Token.Kind == AtKeyword:
			out = append(out, p.atRule())

		default:
			if r, ok := p.qualifiedRule(); ok {
				out = append(out, r)
			}
		}
	}
}

// atRule consumes an at-rule (§5.4.2), positioned at the at-keyword.
func (p *parser) atRule() Rule {
	at := p.next()
	out := Rule{At: true, Name: at.Token.Value, Offset: at.Token.Offset}
	for {
		switch c := p.peek(); {
		case c.Token.Kind == Semicolon:
			p.pos++
			return out

		case c.Token.Kind == EOF:
			// A statement at-rule needs no block, so running out of input is
			// only an error if the rule was still collecting its prelude. It
			// was, or we would have returned; but "@import 'x'" with no
			// semicolon is so common that reporting it would be noise.
			return out

		case c.Token.Kind == LeftBrace:
			out.Block, out.HasBlock = p.takeBlock(c), true
			return out

		default:
			out.Prelude = append(out.Prelude, p.componentValue())
		}
	}
}

// takeBlock consumes a {} block and returns its contents.
//
// It exists because the two inputs the specification defines these algorithms
// over reach a block differently: in a token stream the "{" is a delimiter and
// the contents are still ahead, while in a list of component values the block is
// a single node whose contents were worked out already.
func (p *parser) takeBlock(c ComponentValue) []ComponentValue {
	p.pos++
	if p.fromValues() {
		return c.Values
	}
	return p.block(c.Token).Values
}

// qualifiedRule consumes a qualified rule (§5.4.3). It returns ok=false when the
// input ended before the block, which the specification discards: a selector
// with no body is not a rule, and keeping it would let a truncated file apply
// styles its author never wrote.
func (p *parser) qualifiedRule() (Rule, bool) {
	out := Rule{Offset: p.peek().Token.Offset}
	for {
		switch c := p.peek(); {
		case c.Token.Kind == EOF:
			p.fail(out.Offset, "a rule with no block: the \"{\" is missing")
			return Rule{}, false

		case c.Token.Kind == LeftBrace:
			out.Block, out.HasBlock = p.takeBlock(c), true
			return out, true

		default:
			out.Prelude = append(out.Prelude, p.componentValue())
		}
	}
}

// declarations consumes a list of declarations (§5.4.4).
func (p *parser) declarations() ([]Declaration, []Rule) {
	var decls []Declaration
	var rules []Rule
	for {
		switch c := p.peek(); {
		case c.Token.Kind == EOF:
			return decls, rules

		case c.Token.Kind == Whitespace || c.Token.Kind == Semicolon:
			p.pos++

		case c.Token.Kind == AtKeyword:
			rules = append(rules, p.atRule())

		case c.Token.Kind == Ident:
			// The declaration is parsed from the run up to the next semicolon,
			// so that a malformed one cannot swallow the declarations after it.
			run := p.until(Semicolon)
			if d, ok := parseDeclarationFrom(run, p); ok {
				decls = append(decls, d)
			}

		default:
			p.fail(c.Token.Offset, "expected a property name")
			p.until(Semicolon)
		}
	}
}

// until collects component values up to, but not including, the next top-level
// token of the given kind.
func (p *parser) until(end Kind) []ComponentValue {
	var out []ComponentValue
	for {
		c := p.peek()
		if c.Token.Kind == EOF || c.Token.Kind == end {
			return out
		}
		out = append(out, p.componentValue())
	}
}

// parseDeclarationFrom consumes a declaration from a run of component values
// (§5.4.5). Errors are reported through owner, which holds the shared budget.
func parseDeclarationFrom(run []ComponentValue, owner *parser) (Declaration, bool) {
	q := &parser{vals: run, errs: owner.errs}
	defer func() { owner.errs = q.errs }()

	name := q.next()
	if name.Token.Kind != Ident {
		q.fail(name.Token.Offset, "expected a property name")
		return Declaration{}, false
	}
	out := Declaration{Name: name.Token.Value, Offset: name.Token.Offset}

	q.skipWhitespace()
	if c := q.peek(); c.Token.Kind != Colon {
		q.fail(c.Token.Offset, "expected \":\" after the property name "+quoteName(out.Name))
		return Declaration{}, false
	}
	q.pos++
	q.skipWhitespace()

	for !q.atEOF() {
		out.Value = append(out.Value, q.componentValue())
	}

	out.Value, out.Important = takeImportant(out.Value)
	out.Value = trimTrailingWhitespace(out.Value)
	return out, true
}

// takeImportant removes a trailing "!important" and reports whether it was
// there.
//
// The two tokens need not be adjacent — "! important" is legal, and so is a
// comment between them, which the tokenizer already removed. So this looks at
// the last two values that are not whitespace rather than at the last two
// values, and removes exactly those two along with anything between them.
func takeImportant(vals []ComponentValue) ([]ComponentValue, bool) {
	// The order is "!" then "important", so the last of the two is the ident.
	word := lastNonWhitespace(vals, len(vals))
	if word < 0 {
		return vals, false
	}
	if v := vals[word]; !v.IsToken() || v.Token.Kind != Ident ||
		!strings.EqualFold(v.Token.Value, "important") {
		return vals, false
	}

	bang := lastNonWhitespace(vals, word)
	if bang < 0 {
		return vals, false
	}
	if v := vals[bang]; !v.IsToken() || !v.Token.IsDelim('!') {
		return vals, false
	}

	// Everything before the "!" is the value; whatever sat between the "!" and
	// the "important" was whitespace and goes with them.
	return vals[:bang], true
}

// lastNonWhitespace returns the index of the last value before limit that is not
// whitespace, or -1.
func lastNonWhitespace(vals []ComponentValue, limit int) int {
	for i := limit - 1; i >= 0; i-- {
		if !(vals[i].IsToken() && vals[i].Token.Kind == Whitespace) {
			return i
		}
	}
	return -1
}

func trimTrailingWhitespace(vals []ComponentValue) []ComponentValue {
	n := lastNonWhitespace(vals, len(vals)) + 1
	if n == 0 {
		return nil
	}
	return vals[:n]
}

// quoteName renders a property name for a diagnostic without letting a hostile
// stylesheet put control characters into a caller's log.
func quoteName(s string) string {
	const max = 40
	var b strings.Builder
	b.WriteByte('"')
	for i, r := range s {
		if i >= max {
			b.WriteString("...")
			break
		}
		if r < 0x20 || r == 0x7F {
			b.WriteByte('?')
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
