// Package css reads CSS. It implements the tokenizer and parser of CSS Syntax
// Level 3, which is the layer every other CSS specification is written on top
// of: selectors, properties, media queries and the rest all describe what a
// valid sequence of these tokens means.
//
// # Why a tokenizer rather than a regular expression
//
// CSS looks like it could be read by matching patterns, and it cannot. A string
// may contain a brace; a comment may contain a quote; a url() may contain
// unquoted parentheses; an identifier may contain an escape that encodes any
// code point at all, including a brace. Every one of those is a place where a
// pattern-matching reader silently disagrees with a browser about where a rule
// ends — and a stylesheet that a browser reads one way and this reads another is
// a rendering bug that no amount of testing the *output* will localise.
//
// So the tokenizer is the specification's algorithm, followed step for step.
//
// # Recovery
//
// Tokenization never fails. Every malformed construct has a defined recovery —
// an unterminated string becomes a bad-string-token, a url() with a stray quote
// becomes a bad-url-token — and the token stream continues. This is not
// leniency: it is what makes a stylesheet with one broken rule render the other
// rules, which is what the specification requires and what an author expects.
//
// The places where recovery happened are reported separately, as Errors, so that
// a caller can tell an author what was wrong without the reading of the document
// depending on it.
package css

import (
	"fmt"
	"unicode/utf8"
)

// Kind is what a token is.
type Kind uint8

// The token types of CSS Syntax Level 3 §4. EOF is the zero value so that
// reading past the end of a token slice yields end-of-file rather than an
// identifier.
const (
	EOF Kind = iota

	// Ident is a bare name: a property name, a keyword, an element name.
	Ident
	// Function is a name immediately followed by "(" — the "(" is part of the
	// token, which is what distinguishes rgb( from the ident rgb.
	Function
	// AtKeyword is "@" followed by a name: @media, @page, @font-face.
	AtKeyword
	// Hash is "#" followed by a name or escape. IsID says whether the name is
	// also a valid identifier, which is what separates the selector #main from
	// the colour #123.
	Hash
	// String is a quoted string, with the quotes removed and escapes resolved.
	String
	// BadString is an unterminated string: recovery for a quote left open at
	// the end of a line.
	BadString
	// URL is the unquoted form, url(foo.png). The quoted form is a Function
	// followed by a String, because that needs no special tokenization.
	URL
	// BadURL is an unquoted url() containing something that cannot appear in
	// one.
	BadURL
	// Delim is a single code point with no other meaning: an operator such as
	// "*", "+" or "/", or a stray character.
	Delim
	// Number is a numeric value. IsInteger says whether it was written without
	// a fractional part or exponent, which some properties care about.
	Number
	// Percentage is a number followed by "%".
	Percentage
	// Dimension is a number followed by a unit: 12px, 1.5em, 90deg.
	Dimension
	// Whitespace is a run of spaces, tabs and newlines collapsed into one
	// token. It is significant in CSS — it is the descendant combinator — so it
	// is a token rather than something skipped.
	Whitespace
	// CDO and CDC are "<!--" and "-->", which exist so that a stylesheet could
	// be embedded in an HTML comment for the benefit of browsers that predate
	// the style element. They are tokens because the specification says so; a
	// stylesheet that uses them meaningfully has not been written since 1998.
	CDO
	CDC

	Colon
	Semicolon
	Comma
	LeftSquare
	RightSquare
	LeftParen
	RightParen
	LeftBrace
	RightBrace
)

// Token is one token of a CSS stylesheet.
//
// Which fields carry meaning depends on Kind, and the zero value of a field is
// not distinguishable from an absent one — a Number token whose Value is empty
// is a token whose Kind was never Number. The accessors below are the safe way
// to read one.
type Token struct {
	Kind Kind

	// Value is the token's text with escapes resolved and delimiters removed:
	// the name of an Ident, Function, AtKeyword or Hash (without the "@" or
	// "#"), the contents of a String or URL, or the single code point of a
	// Delim.
	Value string

	// Unit is the unit of a Dimension: "px", "em", "deg". It is empty for every
	// other kind.
	Unit string

	// Number is the value of a Number, Percentage or Dimension. A Percentage
	// holds the number as written, so 50% is 50 rather than 0.5.
	Number float64

	// Repr is the number exactly as it was written, which is what serialising a
	// stylesheet back out needs: 1.50 and 1.5 are the same value and not the
	// same text.
	Repr string

	// IsInteger reports that a numeric token was written with no fractional
	// part and no exponent. Some properties accept only integers, and 2.0 is
	// not one of them.
	IsInteger bool

	// IsID reports that a Hash token's name is also a valid identifier. "#main"
	// can be an ID selector and "#0f0" cannot, and the two are otherwise the
	// same token.
	IsID bool

	// Offset is the byte offset in the original input at which this token
	// begins, so that a diagnostic can point at the source.
	Offset int
}

// Delim returns the code point of a Delim token, or 0 for any other kind.
func (t Token) Delim() rune {
	if t.Kind != Delim {
		return 0
	}
	for _, r := range t.Value {
		return r
	}
	return 0
}

// IsDelim reports whether the token is the given delimiter, which is the check
// almost every caller of Delim actually wanted.
func (t Token) IsDelim(r rune) bool { return t.Kind == Delim && t.Delim() == r }

// String renders a token for diagnostics. It is not a serialiser — it is meant
// to be read in an error message, not fed back to a parser.
func (t Token) String() string {
	switch t.Kind {
	case EOF:
		return "end of input"
	case Whitespace:
		return "whitespace"
	case Ident:
		return fmt.Sprintf("ident %q", t.Value)
	case Function:
		return fmt.Sprintf("function %s(", t.Value)
	case AtKeyword:
		return fmt.Sprintf("at-keyword @%s", t.Value)
	case Hash:
		return fmt.Sprintf("hash #%s", t.Value)
	case String:
		return fmt.Sprintf("string %q", t.Value)
	case BadString:
		return "unterminated string"
	case URL:
		return fmt.Sprintf("url(%s)", t.Value)
	case BadURL:
		return "malformed url()"
	case Delim:
		return fmt.Sprintf("%q", t.Value)
	case Number:
		return fmt.Sprintf("number %s", t.Repr)
	case Percentage:
		return fmt.Sprintf("percentage %s%%", t.Repr)
	case Dimension:
		return fmt.Sprintf("dimension %s%s", t.Repr, t.Unit)
	case CDO:
		return "<!--"
	case CDC:
		return "-->"
	case Colon:
		return `":"`
	case Semicolon:
		return `";"`
	case Comma:
		return `","`
	case LeftSquare:
		return `"["`
	case RightSquare:
		return `"]"`
	case LeftParen:
		return `"("`
	case RightParen:
		return `")"`
	case LeftBrace:
		return `"{"`
	case RightBrace:
		return `"}"`
	}
	return "unknown token"
}

// Error is a place where the input was not well formed and the specification's
// recovery was applied.
//
// It never stops the reading of a stylesheet. It exists so that a caller can
// tell an author what was wrong, which is the difference between a tool that
// renders a broken document and one that says why it looks wrong.
type Error struct {
	// Offset is the byte offset in the input where the problem was noticed.
	Offset int
	// Message says what was wrong, in terms of the source rather than of the
	// algorithm: "unterminated string", not "unexpected EOF in state 7".
	Message string
}

func (e Error) Error() string { return fmt.Sprintf("byte %d: %s", e.Offset, e.Message) }

// Position converts a byte offset into a line and column, both counted from 1,
// with the column counted in code points rather than bytes — which is what an
// editor shows and therefore what an author can act on.
//
// An offset that falls inside a multi-byte character names that character
// rather than the one after it. Token offsets are always at character
// boundaries, so this only arises for an offset computed some other way, and
// pointing one character past the problem is the wrong direction to be wrong in.
//
// An offset past the end of the input gives the position just after the last
// character, which is where an "unexpected end of input" belongs.
func Position(input string, offset int) (line, col int) {
	if offset > len(input) {
		offset = len(input)
	}
	line, col = 1, 1
	for i := 0; i < len(input); {
		// The size comes from decoding rather than from the rune, because an
		// invalid byte decodes to a replacement character three bytes wide that
		// occupies one byte of input.
		_, size := utf8.DecodeRuneInString(input[i:])
		if i+size > offset {
			return line, col
		}
		if input[i] == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
		i += size
	}
	return line, col
}
