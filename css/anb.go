package css

import (
	"math"
	"strconv"
	"strings"
)

// The An+B microsyntax of CSS Syntax Level 3 §6, which is what :nth-child() and
// its four siblings take.
//
// It is written here rather than in the selector parser because it is a
// *syntax*, defined in the syntax specification, and because it is deceptively
// hard: "3n-1" is one dimension token whose unit is "n-1", "3n - 1" is three
// tokens, "3 n" is not an An+B at all, and "+ 2n" is not either. The tokenizer
// splits the same characters differently depending on what surrounds them, so
// the cases below are not variations on a theme — they are the reason browsers
// disagree with hand-written readers about which elements a rule selects.

// AnB is a selection of the indices A×n + B, for every integer n ≥ 0 that makes
// the result positive. It is what :nth-child(2n+1) means.
type AnB struct{ A, B int }

// Matches reports whether a one-based index is selected.
//
// The definition is "there is a non-negative integer n such that index = A×n+B",
// which is a divisibility test rather than a loop — an important difference when
// A is large and the caller is a layout engine walking a hostile document.
func (a AnB) Matches(index int) bool {
	if index < 1 {
		return false
	}
	d := index - a.B
	if a.A == 0 {
		return d == 0
	}
	// n must be a non-negative integer, so the remainder must vanish and the
	// quotient must not be negative.
	if d%a.A != 0 {
		return false
	}
	return d/a.A >= 0
}

// ParseAnB reads an An+B value from component values — the arguments of an
// :nth-child() and friends. It reports false for anything that is not one, which
// makes the whole selector invalid rather than matching nothing.
func ParseAnB(vals []ComponentValue) (AnB, bool) {
	p := &anb{vals: vals}
	p.skipWS()
	out, ok := p.value()
	if !ok {
		return AnB{}, false
	}
	p.skipWS()
	if p.pos != len(p.vals) {
		// Anything left over means this was not an An+B, however well it began.
		return AnB{}, false
	}
	return out, true
}

type anb struct {
	vals []ComponentValue
	pos  int
}

func (p *anb) peek() ComponentValue {
	if p.pos < len(p.vals) {
		return p.vals[p.pos]
	}
	return ComponentValue{Token: Token{Kind: EOF}}
}

func (p *anb) skipWS() {
	for p.pos < len(p.vals) {
		c := p.vals[p.pos]
		if !c.IsToken() || c.Token.Kind != Whitespace {
			return
		}
		p.pos++
	}
}

// value reads the leading form, which fixes A and sometimes B too.
func (p *anb) value() (AnB, bool) {
	c := p.peek()
	if !c.IsToken() {
		return AnB{}, false
	}
	t := c.Token

	switch t.Kind {
	case Ident:
		switch {
		case strings.EqualFold(t.Value, "odd"):
			p.pos++
			return AnB{2, 1}, true
		case strings.EqualFold(t.Value, "even"):
			p.pos++
			return AnB{2, 0}, true
		}
		p.pos++
		return p.fromNIdent(t.Value, 1)

	case Number:
		// A bare integer selects one index and nothing else.
		v, ok := integerOf(t)
		if !ok {
			return AnB{}, false
		}
		p.pos++
		return AnB{0, v}, true

	case Dimension:
		a, ok := integerOf(t)
		if !ok {
			return AnB{}, false
		}
		p.pos++
		return p.fromNUnit(t.Unit, a)

	case Delim:
		// A "+" that the tokenizer did not attach to a number can still begin
		// "+n". It must touch what follows: "+ n" is not an An+B, and the only
		// evidence of the space is that a separate whitespace token exists.
		if !t.IsDelim('+') {
			return AnB{}, false
		}
		p.pos++
		next := p.peek()
		if !next.IsToken() || next.Token.Kind != Ident {
			return AnB{}, false
		}
		p.pos++
		return p.fromNIdent(next.Token.Value, 1)
	}
	return AnB{}, false
}

// fromNIdent handles the forms where the "n" arrived inside an identifier: "n",
// "-n", "n-", "-n-", "n-3" and "-n-3". sign is the multiplier a leading "+"
// would have contributed, which is always 1 — a leading "-" is part of the
// identifier itself.
func (p *anb) fromNIdent(name string, sign int) (AnB, bool) {
	a := sign
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "−") {
		// Only a true hyphen-minus counts; the tokenizer cannot produce the
		// other, and accepting it would select on text no browser reads.
		if !strings.HasPrefix(name, "-") {
			return AnB{}, false
		}
		a = -1
		name = name[1:]
	}
	return p.afterN(name, a)
}

// fromNUnit handles the same forms where the "n" arrived as a dimension's unit:
// "3n", "3n-", "3n-3".
func (p *anb) fromNUnit(unit string, a int) (AnB, bool) {
	return p.afterN(unit, a)
}

// afterN reads what follows the "n", given that A is already known. rest is the
// text from the "n" onwards: "n", "n-" or "n-<digits>".
func (p *anb) afterN(rest string, a int) (AnB, bool) {
	switch {
	case strings.EqualFold(rest, "n"):
		// "3n" — B is whatever follows as separate tokens, or zero.
		b, ok := p.trailingB()
		if !ok {
			return AnB{}, false
		}
		return AnB{a, b}, true

	case strings.EqualFold(rest, "n-"):
		// "3n-" — the digits are a token of their own and carry no sign, so the
		// "-" already read is the sign.
		v, ok := p.signlessInteger()
		if !ok {
			return AnB{}, false
		}
		return AnB{a, -v}, true

	default:
		// "3n-3" — the whole of B came in the same token, and its sign is the
		// "-" that joins them.
		if len(rest) < 3 || (rest[0] != 'n' && rest[0] != 'N') || rest[1] != '-' {
			return AnB{}, false
		}
		v, ok := digits(rest[2:])
		if !ok {
			return AnB{}, false
		}
		return AnB{a, -v}, true
	}
}

// trailingB reads the optional "+ 3" or "- 3" or "+3" that may follow an "n".
//
// Absent one, B is zero and nothing is consumed — including any whitespace
// looked past, so that the caller's end-of-input check still sees the input as
// it was.
func (p *anb) trailingB() (int, bool) {
	save := p.pos
	p.skipWS()

	c := p.peek()
	if !c.IsToken() {
		p.pos = save
		return 0, true
	}
	t := c.Token

	// "3n +3": the sign is part of the number token, and it must be there — a
	// signless integer here would be two values, not one An+B.
	if t.Kind == Number {
		if !signed(t) {
			p.pos = save
			return 0, true
		}
		v, ok := integerOf(t)
		if !ok {
			return 0, false
		}
		p.pos++
		return v, true
	}

	// "3n + 3": the sign is a delimiter of its own, and then the integer must
	// carry no sign of its own.
	if t.Kind == Delim && (t.IsDelim('+') || t.IsDelim('-')) {
		sign := 1
		if t.IsDelim('-') {
			sign = -1
		}
		p.pos++
		v, ok := p.signlessInteger()
		if !ok {
			return 0, false
		}
		return sign * v, true
	}

	p.pos = save
	return 0, true
}

// signlessInteger reads an integer written without a sign, skipping whitespace
// before it.
func (p *anb) signlessInteger() (int, bool) {
	p.skipWS()
	c := p.peek()
	if !c.IsToken() || c.Token.Kind != Number || signed(c.Token) {
		return 0, false
	}
	v, ok := integerOf(c.Token)
	if !ok {
		return 0, false
	}
	p.pos++
	return v, true
}

// signed reports whether a numeric token was written with an explicit sign,
// which is the difference between "3n +1" (an An+B) and "3n 1" (not one).
func signed(t Token) bool {
	return strings.HasPrefix(t.Repr, "+") || strings.HasPrefix(t.Repr, "-")
}

// integerOf returns a numeric token's value as an int, rejecting anything
// written with a fractional part or an exponent, and anything too large to be an
// index. "3.1n" is not an An+B, and neither is a value no document can have that
// many children to match.
func integerOf(t Token) (int, bool) {
	if !t.IsInteger {
		return 0, false
	}
	if t.Number > math.MaxInt32 || t.Number < math.MinInt32 {
		return 0, false
	}
	return int(t.Number), true
}

// digits parses a run of ASCII digits, which is what follows the "n-" in the
// forms that carry B in the same token.
func digits(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil || v > math.MaxInt32 {
		return 0, false
	}
	return v, true
}
