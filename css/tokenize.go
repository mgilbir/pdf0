package css

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The tokenizer of CSS Syntax Level 3 §4, followed step for step.
//
// The structure mirrors the specification's algorithms deliberately, down to
// the order of the cases, because that is what makes it checkable against the
// text. Where this departs — the bound on reported errors, the clamping of an
// infinite number — it says so.

// eof is the code point the algorithms call EOF. It is a value rather than a
// flag so that the three-code-point lookaheads the specification uses can be
// written as they are written there.
const eof rune = -1

// maxErrors bounds how many problems are reported from one stylesheet.
//
// A file of pure noise produces a diagnostic per byte, and a caller wanting to
// show an author what is wrong is not helped by ten thousand of them. The last
// entry says that the list was cut, so a truncated report never reads as a
// complete one.
const maxErrors = 100

// Tokenize turns CSS source into tokens.
//
// The returned slice always ends with an EOF token, so a parser reading ahead
// never has to bounds-check: past the end of the input there is always exactly
// one more token, and it says the input has ended.
//
// Errors are the places where the input was malformed and the specification's
// recovery was applied. Tokenizing never fails, so they are advisory: a caller
// that ignores them gets the same tokens a browser would produce.
func Tokenize(input string) ([]Token, []Error) {
	t := newTokenizer(input)
	// One token per two bytes is a fair guess for real CSS and costs nothing
	// when it is wrong.
	out := make([]Token, 0, len(input)/2+1)
	for {
		tok := t.token()
		out = append(out, tok)
		if tok.Kind == EOF {
			return out, t.errs
		}
	}
}

type tokenizer struct {
	// src is the input after the preprocessing of §3.3, as code points. offs
	// holds each one's byte offset in the *original* input, because
	// preprocessing changes lengths — a CRLF becomes one newline — and a
	// diagnostic has to point into the file the author wrote.
	src  []rune
	offs []int
	end  int // len(input), the offset of end-of-file
	pos  int
	errs []Error
}

// newTokenizer applies the input preprocessing of §3.3: the three newline forms
// become one, and anything that cannot be a code point becomes U+FFFD.
//
// Doing this up front rather than in the tokenizer is what lets every algorithm
// below compare against a single newline character. A tokenizer that checks for
// CR, LF, FF and CRLF at each of the dozen places a newline matters gets one of
// them wrong.
func newTokenizer(input string) *tokenizer {
	t := &tokenizer{end: len(input)}
	t.src = make([]rune, 0, len(input))
	t.offs = make([]int, 0, len(input))
	for i := 0; i < len(input); {
		r, size := utf8.DecodeRuneInString(input[i:])
		switch {
		case r == '\r':
			// A carriage return, alone or before a newline, is one newline.
			if i+size < len(input) && input[i+size] == '\n' {
				size++
			}
			r = '\n'
		case r == '\f':
			r = '\n'
		case r == 0:
			// A null is not an error and not dropped: it becomes a replacement
			// character, so that a stylesheet carrying one still parses.
			r = '�'
		}
		t.src = append(t.src, r)
		t.offs = append(t.offs, i)
		i += size
	}
	return t
}

func (t *tokenizer) at(n int) rune {
	if i := t.pos + n; i >= 0 && i < len(t.src) {
		return t.src[i]
	}
	return eof
}

func (t *tokenizer) cur() rune { return t.at(0) }

func (t *tokenizer) advance() {
	if t.pos < len(t.src) {
		t.pos++
	}
}

func (t *tokenizer) skip(n int) {
	for i := 0; i < n; i++ {
		t.advance()
	}
}

// offset is where the current code point begins in the original input.
func (t *tokenizer) offset() int {
	if t.pos < len(t.offs) {
		return t.offs[t.pos]
	}
	return t.end
}

func (t *tokenizer) fail(off int, msg string) {
	switch {
	case len(t.errs) > maxErrors:
		return
	case len(t.errs) == maxErrors:
		t.errs = append(t.errs, Error{
			Offset:  off,
			Message: "further problems in this stylesheet were not reported",
		})
	default:
		t.errs = append(t.errs, Error{Offset: off, Message: msg})
	}
}

// token consumes one token (§4.3.1).
func (t *tokenizer) token() Token {
	t.consumeComments()
	start := t.offset()
	r := t.cur()

	switch {
	case r == eof:
		return Token{Kind: EOF, Offset: t.end}

	case isWhitespace(r):
		for isWhitespace(t.cur()) {
			t.advance()
		}
		return Token{Kind: Whitespace, Offset: start}

	case r == '"' || r == '\'':
		t.advance()
		return t.consumeString(r, start)

	case r == '#':
		if isIdent(t.at(1)) || validEscape(t.at(1), t.at(2)) {
			t.advance()
			// The type flag is decided before the name is consumed, because it
			// asks whether what follows *would start* an identifier.
			tok := Token{Kind: Hash, Offset: start}
			tok.IsID = wouldStartIdent(t.cur(), t.at(1), t.at(2))
			tok.Value = t.consumeIdentSequence()
			return tok
		}
		t.advance()
		return Token{Kind: Delim, Value: "#", Offset: start}

	case r == '(':
		t.advance()
		return Token{Kind: LeftParen, Offset: start}
	case r == ')':
		t.advance()
		return Token{Kind: RightParen, Offset: start}

	case r == '+':
		if wouldStartNumber(t.cur(), t.at(1), t.at(2)) {
			return t.consumeNumeric(start)
		}
		t.advance()
		return Token{Kind: Delim, Value: "+", Offset: start}

	case r == ',':
		t.advance()
		return Token{Kind: Comma, Offset: start}

	case r == '-':
		// The order matters: -1 is a number, --> is a CDC, and --custom is an
		// identifier. Checking for the identifier first would swallow all three.
		if wouldStartNumber(t.cur(), t.at(1), t.at(2)) {
			return t.consumeNumeric(start)
		}
		if t.at(1) == '-' && t.at(2) == '>' {
			t.skip(3)
			return Token{Kind: CDC, Offset: start}
		}
		if wouldStartIdent(t.cur(), t.at(1), t.at(2)) {
			return t.consumeIdentLike(start)
		}
		t.advance()
		return Token{Kind: Delim, Value: "-", Offset: start}

	case r == '.':
		if wouldStartNumber(t.cur(), t.at(1), t.at(2)) {
			return t.consumeNumeric(start)
		}
		t.advance()
		return Token{Kind: Delim, Value: ".", Offset: start}

	case r == ':':
		t.advance()
		return Token{Kind: Colon, Offset: start}
	case r == ';':
		t.advance()
		return Token{Kind: Semicolon, Offset: start}

	case r == '<':
		if t.at(1) == '!' && t.at(2) == '-' && t.at(3) == '-' {
			t.skip(4)
			return Token{Kind: CDO, Offset: start}
		}
		t.advance()
		return Token{Kind: Delim, Value: "<", Offset: start}

	case r == '@':
		if wouldStartIdent(t.at(1), t.at(2), t.at(3)) {
			t.advance()
			return Token{Kind: AtKeyword, Value: t.consumeIdentSequence(), Offset: start}
		}
		t.advance()
		return Token{Kind: Delim, Value: "@", Offset: start}

	case r == '[':
		t.advance()
		return Token{Kind: LeftSquare, Offset: start}
	case r == ']':
		t.advance()
		return Token{Kind: RightSquare, Offset: start}
	case r == '{':
		t.advance()
		return Token{Kind: LeftBrace, Offset: start}
	case r == '}':
		t.advance()
		return Token{Kind: RightBrace, Offset: start}

	case r == '\\':
		if validEscape(t.cur(), t.at(1)) {
			return t.consumeIdentLike(start)
		}
		t.fail(start, "a backslash at the end of a line does not escape anything")
		t.advance()
		return Token{Kind: Delim, Value: "\\", Offset: start}

	case isDigit(r):
		return t.consumeNumeric(start)

	case isIdentStart(r):
		return t.consumeIdentLike(start)
	}

	t.advance()
	return Token{Kind: Delim, Value: string(r), Offset: start}
}

// consumeComments removes /* … */ (§4.3.2). Comments are not tokens: they may
// appear anywhere a token may, so removing them here means no other algorithm
// has to know they exist.
func (t *tokenizer) consumeComments() {
	for t.cur() == '/' && t.at(1) == '*' {
		start := t.offset()
		t.skip(2)
		for {
			if t.cur() == eof {
				t.fail(start, "unterminated comment")
				return
			}
			if t.cur() == '*' && t.at(1) == '/' {
				t.skip(2)
				break
			}
			t.advance()
		}
	}
}

// consumeString reads a quoted string (§4.3.5), the opening quote already
// consumed.
func (t *tokenizer) consumeString(ending rune, start int) Token {
	var b strings.Builder
	for {
		switch r := t.cur(); {
		case r == ending:
			t.advance()
			return Token{Kind: String, Value: b.String(), Offset: start}

		case r == eof:
			// What was read is still a string: a quote left open at the end of
			// a file yields the text up to there, not nothing.
			t.fail(start, "unterminated string")
			return Token{Kind: String, Value: b.String(), Offset: start}

		case r == '\n':
			// A newline inside a string is a different recovery: the string is
			// bad, and the newline belongs to whatever follows it, so it is
			// deliberately left unconsumed.
			t.fail(t.offset(), "a newline inside a quoted string")
			return Token{Kind: BadString, Offset: start}

		case r == '\\':
			switch {
			case t.at(1) == eof:
				// The backslash is consumed and contributes nothing; the next
				// pass sees EOF and reports the unterminated string.
				t.advance()
			case t.at(1) == '\n':
				// An escaped newline is a line continuation: neither character
				// is part of the value.
				t.skip(2)
			default:
				t.advance()
				b.WriteRune(t.consumeEscape())
			}

		default:
			t.advance()
			b.WriteRune(r)
		}
	}
}

// consumeURL reads the unquoted form, url(…), with the "(" already consumed
// (§4.3.6).
//
// The quoted form never reaches here: it is a function-token followed by an
// ordinary string, which needs no special handling. This exists only because
// url(foo.png) has no quotes and so cannot be tokenized by the string rules.
func (t *tokenizer) consumeURL(start int) Token {
	for isWhitespace(t.cur()) {
		t.advance()
	}
	var b strings.Builder
	for {
		switch r := t.cur(); {
		case r == ')':
			t.advance()
			return Token{Kind: URL, Value: b.String(), Offset: start}

		case r == eof:
			t.fail(start, "unterminated url()")
			return Token{Kind: URL, Value: b.String(), Offset: start}

		case isWhitespace(r):
			for isWhitespace(t.cur()) {
				t.advance()
			}
			if t.cur() == ')' {
				t.advance()
				return Token{Kind: URL, Value: b.String(), Offset: start}
			}
			if t.cur() == eof {
				t.fail(start, "unterminated url()")
				return Token{Kind: URL, Value: b.String(), Offset: start}
			}
			// Trailing space is allowed; space in the middle is not, because
			// there is no way to tell where the address ends.
			t.fail(t.offset(), "a space inside an unquoted url(): quote the address")
			t.consumeBadURL()
			return Token{Kind: BadURL, Offset: start}

		case r == '"' || r == '\'' || r == '(' || isNonPrintable(r):
			t.fail(t.offset(), fmt.Sprintf("%q cannot appear in an unquoted url(): quote the address", r))
			t.consumeBadURL()
			return Token{Kind: BadURL, Offset: start}

		case r == '\\':
			if validEscape(r, t.at(1)) {
				t.advance()
				b.WriteRune(t.consumeEscape())
				continue
			}
			t.fail(t.offset(), "a backslash inside url() that does not escape anything")
			t.consumeBadURL()
			return Token{Kind: BadURL, Offset: start}

		default:
			t.advance()
			b.WriteRune(r)
		}
	}
}

// consumeBadURL discards the rest of a malformed url() (§4.3.14).
//
// It has to honour escapes even though it is throwing everything away, because
// an escaped ")" does not close the url and a reader that stopped at it would
// resume in the middle of a value.
func (t *tokenizer) consumeBadURL() {
	for {
		switch r := t.cur(); {
		case r == eof:
			return
		case r == ')':
			t.advance()
			return
		case validEscape(r, t.at(1)):
			t.advance()
			t.consumeEscape()
		default:
			t.advance()
		}
	}
}

// consumeEscape reads what follows a backslash (§4.3.7), the backslash already
// consumed.
func (t *tokenizer) consumeEscape() rune {
	r := t.cur()
	if r == eof {
		t.fail(t.offset(), "an escape at the end of the input")
		return '�'
	}
	if !isHexDigit(r) {
		// Any other character escapes to itself, which is how a literal quote
		// or brace is written.
		t.advance()
		return r
	}
	v := 0
	for n := 0; n < 6 && isHexDigit(t.cur()); n++ {
		v = v*16 + hexValue(t.cur())
		t.advance()
	}
	// One whitespace character may follow a hex escape to end it, and is part
	// of the escape rather than of the text: "\41 B" is "AB", not "A B".
	if isWhitespace(t.cur()) {
		t.advance()
	}
	if v == 0 || (v >= 0xD800 && v <= 0xDFFF) || v > 0x10FFFF {
		// A null, a surrogate half or a value outside Unicode is not a code
		// point. Substituting rather than failing keeps the stylesheet usable.
		return '�'
	}
	return rune(v)
}

// consumeIdentSequence reads a name (§4.3.11), resolving escapes.
func (t *tokenizer) consumeIdentSequence() string {
	var b strings.Builder
	for {
		r := t.cur()
		switch {
		case isIdent(r):
			t.advance()
			b.WriteRune(r)
		case validEscape(r, t.at(1)):
			t.advance()
			b.WriteRune(t.consumeEscape())
		default:
			return b.String()
		}
	}
}

// consumeIdentLike reads an identifier, a function or a url (§4.3.4).
//
// The three are one algorithm because they begin identically: a name. What
// follows it decides which of them it was, and url() is singled out because its
// contents are tokenized by different rules than any other function's.
func (t *tokenizer) consumeIdentLike(start int) Token {
	name := t.consumeIdentSequence()

	if strings.EqualFold(name, "url") && t.cur() == '(' {
		t.advance()
		// Whitespace between the "(" and a quote is kept — one space's worth —
		// because it is what tells the two forms apart. url( "a" ) is an
		// ordinary function call with a string argument; url( a ) is not.
		for isWhitespace(t.cur()) && isWhitespace(t.at(1)) {
			t.advance()
		}
		q, after := t.cur(), t.at(1)
		if q == '"' || q == '\'' || (isWhitespace(q) && (after == '"' || after == '\'')) {
			return Token{Kind: Function, Value: name, Offset: start}
		}
		return t.consumeURL(start)
	}

	if t.cur() == '(' {
		t.advance()
		return Token{Kind: Function, Value: name, Offset: start}
	}
	return Token{Kind: Ident, Value: name, Offset: start}
}

// consumeNumeric reads a number and whatever qualifies it (§4.3.3): a unit
// makes it a dimension, a percent sign a percentage.
func (t *tokenizer) consumeNumeric(start int) Token {
	value, repr, isInteger := t.consumeNumber()

	if wouldStartIdent(t.cur(), t.at(1), t.at(2)) {
		return Token{
			Kind: Dimension, Number: value, Repr: repr, IsInteger: isInteger,
			Unit: t.consumeIdentSequence(), Offset: start,
		}
	}
	if t.cur() == '%' {
		t.advance()
		// A percentage carries no integer flag: the specification does not give
		// it one, and inventing it here would let a caller act on a distinction
		// that no other reader of CSS makes.
		return Token{Kind: Percentage, Number: value, Repr: repr, Offset: start}
	}
	return Token{Kind: Number, Number: value, Repr: repr, IsInteger: isInteger, Offset: start}
}

// consumeNumber reads the number itself (§4.3.12), returning its value, the
// text exactly as written, and whether it was an integer.
func (t *tokenizer) consumeNumber() (value float64, repr string, isInteger bool) {
	var b strings.Builder
	isInteger = true

	if r := t.cur(); r == '+' || r == '-' {
		b.WriteRune(r)
		t.advance()
	}
	for isDigit(t.cur()) {
		b.WriteRune(t.cur())
		t.advance()
	}
	if t.cur() == '.' && isDigit(t.at(1)) {
		isInteger = false
		b.WriteRune('.')
		t.advance()
		for isDigit(t.cur()) {
			b.WriteRune(t.cur())
			t.advance()
		}
	}
	// An exponent only counts if a digit actually follows it, so the "e" in
	// "1em" stays with the unit rather than being read as a broken exponent.
	if e := t.cur(); e == 'e' || e == 'E' {
		digitAt, signed := 1, false
		if s := t.at(1); s == '+' || s == '-' {
			digitAt, signed = 2, true
		}
		if isDigit(t.at(digitAt)) {
			isInteger = false
			b.WriteRune(e)
			t.advance()
			if signed {
				b.WriteRune(t.cur())
				t.advance()
			}
			for isDigit(t.cur()) {
				b.WriteRune(t.cur())
				t.advance()
			}
		}
	}

	repr = b.String()
	return parseNumber(repr), repr, isInteger
}

// parseNumber converts what consumeNumber assembled.
//
// The only input ParseFloat can reject here is one whose exponent is out of
// range, which it reports alongside an infinity. An infinite length would poison
// every arithmetic that touched it and produce a page of NaNs, so it is clamped
// to the largest finite value: a number too big to represent is still, for
// layout, just a very large number.
func parseNumber(repr string) float64 {
	v, err := strconv.ParseFloat(repr, 64)
	if err == nil {
		return v
	}
	switch {
	case math.IsInf(v, 1):
		return math.MaxFloat64
	case math.IsInf(v, -1):
		return -math.MaxFloat64
	}
	return 0
}

// validEscape reports whether two code points begin an escape (§4.3.8).
//
// End of input counts: a trailing backslash is a valid escape that yields a
// replacement character, which is what consumeEscape does with it.
func validEscape(first, second rune) bool { return first == '\\' && second != '\n' }

// wouldStartIdent reports whether three code points begin a name (§4.3.9).
func wouldStartIdent(a, b, c rune) bool {
	switch {
	case a == '-':
		return isIdentStart(b) || b == '-' || validEscape(b, c)
	case isIdentStart(a):
		return true
	case a == '\\':
		return validEscape(a, b)
	}
	return false
}

// wouldStartNumber reports whether three code points begin a number (§4.3.10).
func wouldStartNumber(a, b, c rune) bool {
	switch {
	case a == '+' || a == '-':
		return isDigit(b) || (b == '.' && isDigit(c))
	case a == '.':
		return isDigit(b)
	}
	return isDigit(a)
}

// Code point classes (§4.2). Carriage return and form feed do not appear: the
// preprocessing turned both into a newline before any of this ran.
func isWhitespace(r rune) bool { return r == '\n' || r == '\t' || r == ' ' }
func isDigit(r rune) bool      { return r >= '0' && r <= '9' }
func isLetter(r rune) bool     { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }

func isHexDigit(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func hexValue(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	default:
		return int(r-'A') + 10
	}
}

// isNonPrintable is the set that may not appear unescaped in an unquoted url().
func isNonPrintable(r rune) bool {
	return (r >= 0 && r <= 0x08) || r == 0x0B || (r >= 0x0E && r <= 0x1F) || r == 0x7F
}

// isNonASCIIIdent is the specification's list of code points outside ASCII that
// may appear in a name.
//
// It is a list rather than "anything above U+007F" because the ranges left out
// are the ones that would make an identifier ambiguous: the C1 controls, the
// bidirectional formatting characters, the private use areas, and the
// non-characters. A reader that admits all of them accepts names a browser
// rejects, which is a difference an author cannot see and cannot debug.
func isNonASCIIIdent(r rune) bool {
	switch {
	case r == 0x00B7:
		return true
	case r >= 0x00C0 && r <= 0x00D6:
		return true
	case r >= 0x00D8 && r <= 0x00F6:
		return true
	case r >= 0x00F8 && r <= 0x037D:
		return true
	case r >= 0x037F && r <= 0x1FFF:
		return true
	case r == 0x200C, r == 0x200D, r == 0x203F, r == 0x2040:
		return true
	case r >= 0x2070 && r <= 0x218F:
		return true
	case r >= 0x2C00 && r <= 0x2FEF:
		return true
	case r >= 0x3001 && r <= 0xD7FF:
		return true
	case r >= 0xF900 && r <= 0xFDCF:
		return true
	case r >= 0xFDF0 && r <= 0xFFFD:
		return true
	case r >= 0x10000:
		return true
	}
	return false
}

func isIdentStart(r rune) bool { return isLetter(r) || r == '_' || isNonASCIIIdent(r) }
func isIdent(r rune) bool      { return isIdentStart(r) || isDigit(r) || r == '-' }
