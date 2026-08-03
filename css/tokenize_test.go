package css

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// The tokenizer against CSS Syntax Level 3 §4, case by case.
//
// Tokens are compared through a compact rendering rather than as structs, so
// that a table reads as the thing being asserted — "ident(color) : dim(12,px)"
// — and a failure names the difference rather than printing two structs.

func brief(t Token) string {
	switch t.Kind {
	case Ident:
		return "ident(" + t.Value + ")"
	case Function:
		return "func(" + t.Value + ")"
	case AtKeyword:
		return "at(" + t.Value + ")"
	case Hash:
		if t.IsID {
			return "hash-id(" + t.Value + ")"
		}
		return "hash(" + t.Value + ")"
	case String:
		return "str(" + t.Value + ")"
	case BadString:
		return "bad-str"
	case URL:
		return "url(" + t.Value + ")"
	case BadURL:
		return "bad-url"
	case Delim:
		return "delim(" + t.Value + ")"
	case Number:
		if t.IsInteger {
			return "int(" + t.Repr + ")"
		}
		return "num(" + t.Repr + ")"
	case Percentage:
		return "pct(" + t.Repr + ")"
	case Dimension:
		return "dim(" + t.Repr + "," + t.Unit + ")"
	case Whitespace:
		return "ws"
	case CDO:
		return "CDO"
	case CDC:
		return "CDC"
	case Colon:
		return ":"
	case Semicolon:
		return ";"
	case Comma:
		return ","
	case LeftSquare:
		return "["
	case RightSquare:
		return "]"
	case LeftParen:
		return "("
	case RightParen:
		return ")"
	case LeftBrace:
		return "{"
	case RightBrace:
		return "}"
	case EOF:
		return "EOF"
	}
	return "?"
}

// dump renders every token but the terminating EOF, which every stream has and
// no table should have to repeat.
func dump(toks []Token) string {
	parts := make([]string, 0, len(toks))
	for _, t := range toks {
		if t.Kind == EOF {
			continue
		}
		parts = append(parts, brief(t))
	}
	return strings.Join(parts, " ")
}

func tokenized(t *testing.T, input string) string {
	t.Helper()
	toks, _ := Tokenize(input)
	if len(toks) == 0 || toks[len(toks)-1].Kind != EOF {
		t.Fatalf("tokenizing %q did not end with an EOF token", input)
	}
	for i, tok := range toks[:len(toks)-1] {
		if tok.Kind == EOF {
			t.Fatalf("tokenizing %q: an EOF token at position %d, before the end", input, i)
		}
	}
	return dump(toks)
}

func check(t *testing.T, cases map[string]string) {
	t.Helper()
	for input, want := range cases {
		if got := tokenized(t, input); got != want {
			t.Errorf("%q\n  got  %s\n  want %s", input, got, want)
		}
	}
}

func TestTokenizeBasicShapes(t *testing.T) {
	check(t, map[string]string{
		"":                "",
		"a":               "ident(a)",
		"a b":             "ident(a) ws ident(b)",
		"color: red;":     "ident(color) : ws ident(red) ;",
		"a{b:c}":          "ident(a) { ident(b) : ident(c) }",
		"a,b":             "ident(a) , ident(b)",
		"[a]":             "[ ident(a) ]",
		"(a)":             "( ident(a) )",
		"@media":          "at(media)",
		"@ media":         "delim(@) ws ident(media)",
		"foo(":            "func(foo)",
		"foo()":           "func(foo) )",
		"*":               "delim(*)",
		"a > b":           "ident(a) ws delim(>) ws ident(b)",
		"<!-- a -->":      "CDO ws ident(a) ws CDC",
		"< a":             "delim(<) ws ident(a)",
		"--x":             "ident(--x)",
		"--":              "ident(--)",
		"-":               "delim(-)",
		"-a":              "ident(-a)",
		"_a":              "ident(_a)",
		"a-1":             "ident(a-1)",
		"\t\n  \t":        "ws",
		"a/*c*/b":         "ident(a) ident(b)",
		"a/*c*/ b":        "ident(a) ws ident(b)",
		"/**/":            "",
		"/*/":             "",
		"a//b":            "ident(a) delim(/) delim(/) ident(b)",
		"!important":      "delim(!) ident(important)",
		"a:hover::before": "ident(a) : ident(hover) : : ident(before)",
	})
}

// TestTokenizeNumbers walks §4.3.12, whose subtlety is entirely in where a
// number stops. "1e" is a dimension and "1e5" is a number; "1-2" is a dimension
// whose unit begins with a hyphen. Every one of those is a place where reading
// greedily gives the wrong answer.
func TestTokenizeNumbers(t *testing.T) {
	check(t, map[string]string{
		"1":     "int(1)",
		"0":     "int(0)",
		"+1":    "int(+1)",
		"-1":    "int(-1)",
		"1.5":   "num(1.5)",
		"-1.5":  "num(-1.5)",
		".5":    "num(.5)",
		"+.5":   "num(+.5)",
		"-.5":   "num(-.5)",
		"1.":    "int(1) delim(.)",
		".":     "delim(.)",
		"+":     "delim(+)",
		"1e5":   "num(1e5)",
		"1E5":   "num(1E5)",
		"1e+5":  "num(1e+5)",
		"1e-5":  "num(1e-5)",
		"1e":    "dim(1,e)",
		"1e+":   "dim(1,e) delim(+)",
		"1em":   "dim(1,em)",
		"1e5px": "dim(1e5,px)",
		"12px":  "dim(12,px)",
		"-3deg": "dim(-3,deg)",
		"50%":   "pct(50)",
		"-0.5%": "pct(-0.5)",
		// After the number, "-2" does not start an ident sequence — a hyphen
		// followed by a digit is not a name — so this is two numbers, not a
		// dimension. "-a" and "--a" are names, and those are.
		"1-2":    "int(1) int(-2)",
		"1-a":    "dim(1,-a)",
		"1 -2":   "int(1) ws int(-2)",
		"1--a":   "dim(1,--a)",
		"1\\41":  "dim(1,A)",
		"0.0001": "num(0.0001)",
	})
}

// TestNumberValuesAreParsed pins that the numeric value matches the text, and
// that the text is kept: 1.50 and 1.5 are the same number and not the same
// source, and serialising a stylesheet needs both.
func TestNumberValuesAreParsed(t *testing.T) {
	cases := []struct {
		input string
		value float64
		repr  string
		isInt bool
	}{
		{"42", 42, "42", true},
		{"-17", -17, "-17", true},
		{"1.50", 1.5, "1.50", false},
		{"+0.25", 0.25, "+0.25", false},
		{".5", 0.5, ".5", false},
		{"2e3", 2000, "2e3", false},
		{"2e-3", 0.002, "2e-3", false},
		{"1.0", 1, "1.0", false},
	}
	for _, tc := range cases {
		toks, _ := Tokenize(tc.input)
		got := toks[0]
		if got.Number != tc.value {
			t.Errorf("%q has value %v, want %v", tc.input, got.Number, tc.value)
		}
		if got.Repr != tc.repr {
			t.Errorf("%q kept as %q, want %q", tc.input, got.Repr, tc.repr)
		}
		if got.IsInteger != tc.isInt {
			t.Errorf("%q integer=%v, want %v", tc.input, got.IsInteger, tc.isInt)
		}
	}
}

// TestOutOfRangeNumberIsClampedNotInfinite pins the one place this departs from
// a literal reading. An exponent beyond float64 gives an infinity, and an
// infinite length propagates through every calculation it touches and turns a
// page into NaNs. A very large finite number is wrong in the last digit; an
// infinity is wrong everywhere.
func TestOutOfRangeNumberIsClampedNotInfinite(t *testing.T) {
	for _, input := range []string{"1e400", "-1e400"} {
		toks, _ := Tokenize(input)
		v := toks[0].Number
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("%q gave %v, want a finite value", input, v)
		}
		if input[0] == '-' && v >= 0 {
			t.Errorf("%q gave %v, want a negative value", input, v)
		}
	}
}

// TestTokenizeStrings walks §4.3.5. The two recoveries differ, and the
// difference is what a following rule sees: a quote left open at the end of a
// file still yields its text, while a newline inside one throws the string away
// and leaves the newline for whatever comes next.
func TestTokenizeStrings(t *testing.T) {
	check(t, map[string]string{
		`"a"`:      "str(a)",
		`'a'`:      "str(a)",
		`""`:       "str()",
		`"a'b"`:    "str(a'b)",
		`'a"b'`:    `str(a"b)`,
		`"a\"b"`:   `str(a"b)`,
		`"a`:       "str(a)",
		"\"a\nb\"": "bad-str ws ident(b) str()",
		"\"a\\\nb\"":// a backslash before a newline is a line continuation
		"str(ab)",
		`"a\`:     "str(a)",
		`"\41"`:   "str(A)",
		`"\41 B"`: "str(AB)",
		`"a\ b"`:  "str(a b)",
	})
}

// TestBadStringLeavesTheNewline is the recovery that a following rule depends
// on. The newline is not consumed, so the declaration the broken string was in
// ends where the author's line ended rather than swallowing the next one.
func TestBadStringLeavesTheNewline(t *testing.T) {
	toks, _ := Tokenize("\"oops\ncolor: red")
	if toks[0].Kind != BadString {
		t.Fatalf("first token is %v, want a bad string", toks[0])
	}
	if toks[1].Kind != Whitespace {
		t.Errorf("second token is %v, want the newline the string did not consume", toks[1])
	}
	if got := dump(toks); got != "bad-str ws ident(color) : ws ident(red)" {
		t.Errorf("got %s", got)
	}
}

// TestTokenizeEscapes walks §4.3.7. The substitutions are the security-relevant
// part: a stylesheet may ask for a code point that does not exist, and the
// answer is a replacement character rather than a panic or a malformed rune.
func TestTokenizeEscapes(t *testing.T) {
	check(t, map[string]string{
		`\41`:     "ident(A)",
		`\41 B`:   "ident(AB)",
		`\41  B`:  "ident(A) ws ident(B)", // one space ends the escape, the second is whitespace
		`\000041`: "ident(A)",
		// Only six hex digits are an escape. Here they spell U+0041, and the
		// seventh character is ordinary text — which is why a hex escape in a
		// name is usually written with a trailing space.
		`\0000411`:         "ident(A1)",
		`\0`:               "ident(�)",
		`\D800`:            "ident(�)",
		`\110000`:          "ident(�)",
		`\FFFFFF`:          "ident(�)",
		`\-`:               "ident(-)",
		`\{`:               "ident({)",
		`a\{b`:             "ident(a{b)",
		"\\\n":             "delim(\\) ws",
		`\`:                "ident(�)",
		`-\41`:             "ident(-A)",
		`#\41`:             "hash-id(A)",
		`@\41`:             "at(A)",
		`url(a\29 b)` + "": "url(a)b)",
	})
}

// TestTokenizeHash pins the type flag, which is the whole difference between a
// selector and a colour. "#main" can be an ID and "#0f0" cannot, and nothing
// else about the two tokens differs.
func TestTokenizeHash(t *testing.T) {
	cases := map[string]bool{
		"#main": true,
		"#-a":   true,
		"#_a":   true,
		"#--a":  true,
		"#\\41": true,
		"#0f0":  false,
		"#123":  false,
		"#1a":   false,
	}
	for input, wantID := range cases {
		toks, _ := Tokenize(input)
		if toks[0].Kind != Hash {
			t.Errorf("%q is %v, want a hash", input, toks[0])
			continue
		}
		if toks[0].IsID != wantID {
			t.Errorf("%q has IsID=%v, want %v", input, toks[0].IsID, wantID)
		}
	}
	// A "#" with nothing that can follow it is just a delimiter.
	check(t, map[string]string{
		"#":   "delim(#)",
		"# a": "delim(#) ws ident(a)",
		"#!":  "delim(#) delim(!)",
	})
}

// TestTokenizeURL walks §4.3.6, the only place in CSS where a function's
// contents are tokenized by different rules than every other function's.
//
// The quoted form is deliberately *not* one of these: url("a") is an ordinary
// function call with a string argument, and treating it as a url token would
// mean re-implementing string parsing inside url parsing.
func TestTokenizeURL(t *testing.T) {
	check(t, map[string]string{
		"url(a)":               "url(a)",
		"url()":                "url()",
		"URL(a)":               "url(a)",
		"Url(a)":               "url(a)",
		"url( a )":             "url(a)",
		"url(  a  )":           "url(a)",
		"url(a/b.png?x=1&y=2)": "url(a/b.png?x=1&y=2)",
		// The quoted form is a function and a string, and the whitespace
		// between them survives as a token.
		`url("a")`:   "func(url) str(a) )",
		`url('a')`:   "func(url) str(a) )",
		`url( "a" )`: "func(url) ws str(a) ws )",
		`URL("a")`:   "func(URL) str(a) )",
		// The recoveries.
		"url(a b)":  "bad-url",
		`url(a"b)`:  "bad-url",
		"url(a'b)":  "bad-url",
		"url(a(b))": "bad-url )",
		"url(a":     "url(a)",
		"url(a ":    "url(a)",
		`url(a\)b)`: "url(a)b)",
	})
}

// TestBadURLIsConsumedToItsEnd pins the recovery. A malformed url() has to be
// discarded up to its closing parenthesis, honouring escapes on the way, or the
// reader resumes in the middle of an address and every rule after it is
// misparsed.
func TestBadURLIsConsumedToItsEnd(t *testing.T) {
	check(t, map[string]string{
		"url(a b); color: red": "bad-url ; ws ident(color) : ws ident(red)",
		// The escaped ")" does not end it; the real one does.
		`url(a b\)c); x`: "bad-url ; ws ident(x)",
	})
}

// TestPreprocessingNormalisesNewlines pins §3.3. Every algorithm below compares
// against a single newline character, which is only correct because all three
// forms were turned into one before any of them ran.
func TestPreprocessingNormalisesNewlines(t *testing.T) {
	for _, nl := range []string{"\n", "\r\n", "\r", "\f"} {
		got := tokenized(t, "a"+nl+"b")
		if got != "ident(a) ws ident(b)" {
			t.Errorf("with newline %q: got %s", nl, got)
		}
		// And inside a string, where a newline is an error rather than space.
		if got := tokenized(t, `"a`+nl+`b"`); got != "bad-str ws ident(b) str()" {
			t.Errorf("in a string, with newline %q: got %s", nl, got)
		}
	}
}

// TestNullBecomesAReplacementCharacter pins the other half of §3.3. A null is
// not dropped and not an error: it becomes U+FFFD, which is itself a valid
// identifier character, so a stylesheet carrying one still parses.
func TestNullBecomesAReplacementCharacter(t *testing.T) {
	check(t, map[string]string{
		"a\x00b": "ident(a�b)",
		"\x00":   "ident(�)",
	})
}

// TestIdentifiersAcceptTheSpecifiedNonASCII pins the list rather than "anything
// above ASCII". The gaps are what matter: the multiplication and division signs
// sit between the accented letter ranges and are not name characters, so "a×b"
// is three tokens and not one.
func TestIdentifiersAcceptTheSpecifiedNonASCII(t *testing.T) {
	check(t, map[string]string{
		"é":     "ident(é)",
		"naïve": "ident(naïve)",
		"日本語":   "ident(日本語)",
		"a·b":   "ident(a·b)",                 // U+00B7 is a name character
		"a×b":   "ident(a) delim(×) ident(b)", // U+00D7 is not
		"a÷b":   "ident(a) delim(÷) ident(b)", // U+00F7 is not
		" a":    "delim( ) ident(a)",          // a no-break space is not a name character, nor whitespace
	})
}

// TestOffsetsPointIntoTheOriginalSource is what makes a diagnostic usable. The
// tokenizer works on preprocessed code points, whose indices do not match the
// bytes an author wrote — a two-byte character or a CRLF puts them out of step
// — so every token carries the offset in the original text.
func TestOffsetsPointIntoTheOriginalSource(t *testing.T) {
	const input = "é a\r\nb"
	toks, _ := Tokenize(input)
	want := []struct {
		kind   Kind
		offset int
	}{
		{Ident, 0},      // é occupies bytes 0-1
		{Whitespace, 2}, //
		{Ident, 3},      // a
		{Whitespace, 4}, // the CRLF, two bytes, one token
		{Ident, 6},      // b
		{EOF, 7},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens (%s), want %d", len(toks), dump(toks), len(want))
	}
	for i, w := range want {
		if toks[i].Kind != w.kind || toks[i].Offset != w.offset {
			t.Errorf("token %d is %v at %d, want kind %d at %d",
				i, toks[i], toks[i].Offset, w.kind, w.offset)
		}
	}
}

// TestPositionReportsLineAndColumn pins the conversion an author reads. The
// column counts characters rather than bytes, because that is what an editor
// shows.
func TestPositionReportsLineAndColumn(t *testing.T) {
	const input = "a {\n  cölor: red;\n}"
	cases := []struct{ offset, line, col int }{
		{0, 1, 1},
		{2, 1, 3},
		{4, 2, 1},
		{6, 2, 3},   // 'c'
		{8, 2, 4},   // 'ö' is two bytes, so the next character is column 5
		{9, 2, 5},   // 'l'
		{100, 3, 2}, // past the end: just after the last character
	}
	for _, tc := range cases {
		line, col := Position(input, tc.offset)
		if line != tc.line || col != tc.col {
			t.Errorf("offset %d is at %d:%d, want %d:%d", tc.offset, line, col, tc.line, tc.col)
		}
	}
}

// TestErrorsAreReportedWithTheirPlace pins that recovery is visible. Each of
// these tokenizes successfully, and each says what was wrong and where.
func TestErrorsAreReportedWithTheirPlace(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`"unterminated`, "unterminated string"},
		{"\"broken\nrest", "a newline inside a quoted string"},
		{"/* never closed", "unterminated comment"},
		{"url(a b)", "a space inside an unquoted url()"},
		{`url(a"b)`, "cannot appear in an unquoted url()"},
		{"url(unclosed", "unterminated url()"},
	}
	for _, tc := range cases {
		_, errs := Tokenize(tc.input)
		if len(errs) == 0 {
			t.Errorf("%q reported nothing, want %q", tc.input, tc.want)
			continue
		}
		if !strings.Contains(errs[0].Message, tc.want) {
			t.Errorf("%q reported %q, want it to mention %q", tc.input, errs[0].Message, tc.want)
		}
		if errs[0].Offset < 0 || errs[0].Offset > len(tc.input) {
			t.Errorf("%q reported offset %d, outside the input", tc.input, errs[0].Offset)
		}
	}
}

// TestWellFormedInputReportsNothing is the other side: recovery is reported
// only when it happened. A tokenizer that warns about correct CSS trains its
// users to ignore it.
func TestWellFormedInputReportsNothing(t *testing.T) {
	const sheet = `
/* a stylesheet that is entirely correct */
@media screen and (min-width: 30em) {
  a.link:hover > .child::before {
    content: "\201C";
    background: url(bg.png) no-repeat 50% 0;
    margin: -1.5em 0 .5em calc(100% - 2px);
    color: #0f0;
  }
}
`
	if _, errs := Tokenize(sheet); len(errs) != 0 {
		t.Errorf("a well-formed stylesheet reported %d problems: %v", len(errs), errs)
	}
}

// TestReportedErrorsAreBounded pins that a hostile file cannot turn a few
// hundred kilobytes into an unbounded diagnostic list, and that the truncation
// says so rather than passing for a complete report.
func TestReportedErrorsAreBounded(t *testing.T) {
	_, errs := Tokenize(strings.Repeat("\"\n", maxErrors*3))
	if len(errs) != maxErrors+1 {
		t.Fatalf("got %d errors, want %d and a note", len(errs), maxErrors)
	}
	last := errs[len(errs)-1].Message
	if !strings.Contains(last, "not reported") {
		t.Errorf("the last entry is %q; a truncated list must say it was truncated", last)
	}
}

// TestTokenizeIsTotal is the property that matters most for untrusted input:
// every byte sequence produces a token stream, and the stream always terminates.
// Nothing in CSS tokenization is allowed to fail, so nothing here may panic,
// hang, or return without reaching the end of the input.
func TestTokenizeIsTotal(t *testing.T) {
	inputs := []string{
		"", "\x00", "\\", "\\\\", "\"", "'", "/*", "*/", "(", ")", "{", "}",
		"url(", "url(\\", `url("`, "#", "@", "-", "+", ".", "<", "<!", "<!-",
		"-->", "--", "1e", "1e+", "\\\n", "�", "\x7f", "\x0b",
		strings.Repeat("\\", 1000),
		strings.Repeat("/*", 1000),
		strings.Repeat("url(", 1000),
		strings.Repeat("\"", 1000),
		strings.Repeat("\x00", 1000),
	}
	for _, in := range inputs {
		toks, _ := Tokenize(in)
		if len(toks) == 0 {
			t.Errorf("%q produced no tokens at all", truncate(in))
			continue
		}
		last := toks[len(toks)-1]
		if last.Kind != EOF {
			t.Errorf("%q ended with %v rather than EOF", truncate(in), last)
		}
		if last.Offset != len(in) {
			t.Errorf("%q ended at offset %d, want %d — the whole input was not read",
				truncate(in), last.Offset, len(in))
		}
	}
}

func truncate(s string) string {
	if len(s) <= 32 {
		return s
	}
	return fmt.Sprintf("%s... (%d bytes)", s[:32], len(s))
}
