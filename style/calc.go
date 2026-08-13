package style

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
)

// calc(), from CSS Values and Units.
//
// # Why it is evaluated here and not later
//
// A calc() is a length like any other, and this file's job is to turn one into
// the two numbers Length holds: how many units it is, and what percentage of its
// containing block. Everything else in an expression can be settled where the
// expression is read — the font-relative units against the LengthContext the
// caller already supplies, the arithmetic against itself — so a calc() reaches
// layout as a length and layout never learns that it was one.
//
// The percentage is the part that cannot be settled here, because what it is a
// percentage *of* is not known until the containing block is. So it is carried
// alongside the absolute part rather than folded into it, which is what
// LengthCalc is for: "calc(100% - 2em)" is two ems below all of something, and
// neither half of that can be dropped.
//
// # What is refused
//
// A type error, which in this grammar means adding a length to a number,
// multiplying two lengths, or dividing by anything but a number. These are not
// approximated: an expression that does not typecheck is not a length, and CSS
// says the declaration containing it is invalid. The caller reports it and the
// declaration before it stands, which is what a browser does.
//
// Division by zero is the same case and is refused for the same reason rather
// than saturating: a length of infinity is not what anybody wrote.
//
// min(), max() and clamp() are not implemented. They are separate functions
// rather than part of this grammar, and each has its own argument rules; what is
// here is calc() and the parentheses inside it.

// calcTerm is a value part-way through an expression: a plain number, or a
// length with an absolute part and a percentage part.
//
// The two are kept apart because the grammar treats them differently — a number
// may multiply a length and a length may not multiply a length — and because
// "calc(2 * 3)" is a number, which is not a length and not accepted as one.
type calcTerm struct {
	number   float64
	abs      Unit
	pct      float64
	isNumber bool
}

// evalCalc reads a calc() function's arguments into a length.
//
// The second result says the expression was well formed. A malformed one is not
// a length and not a value this engine merely fails to compute: the declaration
// is invalid and the caller drops it.
func evalCalc(vals []css.ComponentValue, ctx LengthContext) (Length, bool) {
	t, rest, ok := calcSum(vals, ctx)
	if !ok || len(skipSpace(rest)) != 0 || t.isNumber {
		return Length{}, false
	}
	if t.pct == 0 {
		return Length{Kind: LengthAbsolute, Value: t.abs}, true
	}
	if t.abs == 0 {
		return Length{Kind: LengthPercent, Percent: t.pct}, true
	}
	return Length{Kind: LengthCalc, Value: t.abs, Percent: t.pct}, true
}

// calcSum is the "+" and "-" level: the loosest-binding one, so it is read last
// and calls into the tighter one for each of its operands.
func calcSum(vals []css.ComponentValue, ctx LengthContext) (calcTerm, []css.ComponentValue, bool) {
	left, rest, ok := calcProduct(vals, ctx)
	if !ok {
		return calcTerm{}, nil, false
	}
	for {
		// CSS requires white space around + and -, and the requirement is not
		// decoration: without it "calc(1px -2px)" would be a subtraction or a
		// length followed by a negative length depending on which way you
		// squint, and the tokenizer has already chosen the second. So an
		// operator is a delimiter with space in front of it, and anything else
		// ends the sum.
		after := skipSpace(rest)
		if len(after) == len(rest) || len(after) == 0 {
			return left, rest, true
		}
		op, isOp := calcOperator(after[0], "+-")
		if !isOp {
			return left, rest, true
		}
		right, more, ok := calcProduct(after[1:], ctx)
		if !ok {
			return calcTerm{}, nil, false
		}
		left, ok = calcAdd(left, right, op == '-')
		if !ok {
			return calcTerm{}, nil, false
		}
		rest = more
	}
}

// calcProduct is the "*" and "/" level, which binds tighter and needs no space
// around its operators.
func calcProduct(vals []css.ComponentValue, ctx LengthContext) (calcTerm, []css.ComponentValue, bool) {
	left, rest, ok := calcValue(vals, ctx)
	if !ok {
		return calcTerm{}, nil, false
	}
	for {
		after := skipSpace(rest)
		if len(after) == 0 {
			return left, rest, true
		}
		op, isOp := calcOperator(after[0], "*/")
		if !isOp {
			return left, rest, true
		}
		right, more, ok := calcValue(after[1:], ctx)
		if !ok {
			return calcTerm{}, nil, false
		}
		if op == '*' {
			left, ok = calcMul(left, right)
		} else {
			left, ok = calcDiv(left, right)
		}
		if !ok {
			return calcTerm{}, nil, false
		}
		rest = more
	}
}

// calcValue is one operand: a number, a length, a percentage, a parenthesised
// sum, or a nested calc().
func calcValue(vals []css.ComponentValue, ctx LengthContext) (calcTerm, []css.ComponentValue, bool) {
	vals = skipSpace(vals)
	if len(vals) == 0 {
		return calcTerm{}, nil, false
	}
	v := vals[0]
	switch {
	case v.IsBlock() && v.Token.Kind == css.LeftParen,
		v.IsFunction() && strings.EqualFold(v.Token.Value, "calc"):
		inner, rest, ok := calcSum(v.Values, ctx)
		if !ok || len(skipSpace(rest)) != 0 {
			return calcTerm{}, nil, false
		}
		return inner, vals[1:], true

	case v.IsToken() && v.Token.Kind == css.Number:
		return calcTerm{number: v.Token.Number, isNumber: true}, vals[1:], true

	case v.IsToken() && v.Token.Kind == css.Percentage:
		return calcTerm{pct: v.Token.Number}, vals[1:], true

	case v.IsToken() && v.Token.Kind == css.Dimension:
		px, known, supported := pxPerUnit(v.Token.Unit, ctx)
		if !supported || !known {
			return calcTerm{}, nil, false
		}
		u, ok := FromPx(v.Token.Number * px)
		if !ok {
			return calcTerm{}, nil, false
		}
		return calcTerm{abs: u}, vals[1:], true
	}
	return calcTerm{}, nil, false
}

// calcOperator reads one of a set of single-character operators.
func calcOperator(v css.ComponentValue, of string) (byte, bool) {
	if !v.IsToken() || v.Token.Kind != css.Delim || len(v.Token.Value) != 1 {
		return 0, false
	}
	c := v.Token.Value[0]
	if strings.IndexByte(of, c) < 0 {
		return 0, false
	}
	return c, true
}

// calcAdd is "+" and "-": both operands have to be the same kind of thing.
func calcAdd(a, b calcTerm, minus bool) (calcTerm, bool) {
	if a.isNumber != b.isNumber {
		return calcTerm{}, false
	}
	if minus {
		b.number, b.abs, b.pct = -b.number, Unit(0).Sub(b.abs), -b.pct
	}
	if a.isNumber {
		return calcTerm{number: a.number + b.number, isNumber: true}, true
	}
	return calcTerm{abs: a.abs.Add(b.abs), pct: a.pct + b.pct}, true
}

// calcMul is "*": one side has to be a number, since a length times a length is
// an area and there is nowhere in CSS to put one.
func calcMul(a, b calcTerm) (calcTerm, bool) {
	switch {
	case a.isNumber && b.isNumber:
		return calcTerm{number: a.number * b.number, isNumber: true}, true
	case a.isNumber:
		return calcTerm{abs: b.abs.Mul(a.number), pct: b.pct * a.number}, true
	case b.isNumber:
		return calcTerm{abs: a.abs.Mul(b.number), pct: a.pct * b.number}, true
	}
	return calcTerm{}, false
}

// calcDiv is "/": the divisor has to be a number, and not zero.
func calcDiv(a, b calcTerm) (calcTerm, bool) {
	if !b.isNumber || b.number == 0 {
		return calcTerm{}, false
	}
	if a.isNumber {
		return calcTerm{number: a.number / b.number, isNumber: true}, true
	}
	return calcTerm{abs: a.abs.Div(b.number), pct: a.pct / b.number}, true
}

// skipSpace drops the white space in front of a value.
func skipSpace(vals []css.ComponentValue) []css.ComponentValue {
	for len(vals) > 0 && vals[0].IsToken() && vals[0].Token.Kind == css.Whitespace {
		vals = vals[1:]
	}
	return vals
}
