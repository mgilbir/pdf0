package css

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Fuzzing the CSS reader.
//
// A stylesheet is the most obviously hostile input this project has ever
// accepted: it arrives as text, it is nested, and every layer of it has a
// recovery path that only runs on malformed input — which is exactly the code
// least likely to be reached by tests written from the specification. §4.3 of
// the rendering proposal asks for this from the first milestone rather than
// retrofitted, and this is it.
//
// What is checked is not "does it produce the right answer" — a fuzzer has no
// oracle for that; oracle_test.go does. It is the three properties that must
// hold for *every* input, which is what makes them fuzzable:
//
//  1. Totality. Tokenizing and parsing never fail and never panic. The
//     specification requires recovery from everything, so there is no input for
//     which "it errored" is a correct outcome.
//  2. Completeness. The tokenizer consumes the whole input. A tokenizer that
//     stops early is how a stylesheet gets silently truncated, and the
//     truncation looks like a shorter document rather than like a failure.
//  3. Boundedness. The tree never nests past the cap, whatever the input, so
//     the goroutine stack cannot be exhausted.

func fuzzSeeds() []string {
	return []string{
		"",
		"a{b:c}",
		"@media screen and (min-width: 30em) { a.x:hover > b::before { color: #0f0 } }",
		"a: b !important",
		`p { content: "\201C"; background: url(bg.png) no-repeat 50% 0 }`,
		"a, b, c { margin: -1.5em 0 .5em calc(100% - 2px) }",
		"@import url(other.css);",
		"@font-face { src: url('x.ttf') format('truetype') }",

		// The recoveries, one seed each, because they are the paths tests
		// written from the specification reach least often.
		`"unterminated`,
		"\"newline\nin a string\"",
		"/* never closed",
		"url(a b)",
		"url(unclosed",
		`url(a\)b)`,
		"a{",
		"a",
		"@media{",
		"z;a:b",
		"a:b; c+:d",
		"\\",
		"\\41",
		"#",
		"1e",
		"--custom",
		"<!--a-->",
		"\x00",
		"()[]{}",
		")]}",
		strings.Repeat("(", 300),
		strings.Repeat("f(", 300),
	}
}

// FuzzTokenize checks totality and completeness of the tokenizer.
func FuzzTokenize(f *testing.F) {
	for _, s := range fuzzSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		toks, errs := Tokenize(input)

		if len(toks) == 0 {
			t.Fatal("produced no tokens at all; every input has at least an EOF")
		}
		last := toks[len(toks)-1]
		if last.Kind != EOF {
			t.Fatalf("ended with %v rather than EOF", last)
		}
		if last.Offset != len(input) {
			t.Fatalf("ended at offset %d of %d — the whole input was not read",
				last.Offset, len(input))
		}
		for i, tok := range toks[:len(toks)-1] {
			if tok.Kind == EOF {
				t.Fatalf("an EOF token at %d, before the end", i)
			}
			if tok.Offset < 0 || tok.Offset > len(input) {
				t.Fatalf("token %d at offset %d, outside the input", i, tok.Offset)
			}
			// Offsets are what every diagnostic points with, so they have to
			// run forwards.
			if i > 0 && tok.Offset < toks[i-1].Offset {
				t.Fatalf("token %d at offset %d goes backwards from %d",
					i, tok.Offset, toks[i-1].Offset)
			}
		}

		// The diagnostic budget is a promise, and a promise that only holds on
		// the inputs someone thought of is not one.
		if len(errs) > maxErrors+1 {
			t.Fatalf("reported %d problems, past the bound of %d", len(errs), maxErrors)
		}
		for _, e := range errs {
			if e.Offset < 0 || e.Offset > len(input) {
				t.Fatalf("a problem reported at offset %d, outside the input", e.Offset)
			}
			// A message is shown to a person, so it has to be text.
			if !utf8.ValidString(e.Message) {
				t.Fatalf("a problem reported with a message that is not valid UTF-8: %q", e.Message)
			}
		}

		// Position must answer for every offset a token or a problem carries.
		for _, e := range errs {
			if line, col := Position(input, e.Offset); line < 1 || col < 1 {
				t.Fatalf("offset %d is at %d:%d, which is not a place", e.Offset, line, col)
			}
		}
	})
}

// FuzzParse checks totality and boundedness of every parser entry point.
func FuzzParse(f *testing.F) {
	for _, s := range fuzzSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		vals, errs := ParseComponentValues(input)
		checkBounded(t, vals)
		checkErrors(t, errs, len(input))

		rules, errs := ParseStylesheet(input)
		checkRules(t, rules)
		checkErrors(t, errs, len(input))

		rules, errs = ParseRules(input)
		checkRules(t, rules)
		checkErrors(t, errs, len(input))

		decls, rules, errs := ParseDeclarations(input)
		checkRules(t, rules)
		checkErrors(t, errs, len(input))
		for _, d := range decls {
			checkBounded(t, d.Value)
			if d.Name == "" {
				t.Fatal("a declaration with no name")
			}
			// Trailing whitespace is removed by §5.4.5, and a value that keeps
			// it compares unequal to the same value written without it.
			if n := len(d.Value); n > 0 {
				if v := d.Value[n-1]; v.IsToken() && v.Token.Kind == Whitespace {
					t.Fatalf("the value of %q ends in whitespace", d.Name)
				}
			}
		}

		// Reading an already-parsed block has to be as total as reading text.
		ParseDeclarationValues(vals)
		ParseRulesFromValues(vals)
	})
}

func checkRules(t *testing.T, rules []Rule) {
	t.Helper()
	for _, r := range rules {
		checkBounded(t, r.Prelude)
		checkBounded(t, r.Block)
		if r.At && r.Name == "" {
			t.Fatal("an at-rule with no name")
		}
		if !r.At && !r.HasBlock {
			t.Fatal("a qualified rule with no block, which §5.4.3 discards")
		}
	}
}

func checkErrors(t *testing.T, errs []Error, n int) {
	t.Helper()
	if len(errs) > maxErrors+1 {
		t.Fatalf("reported %d problems, past the bound of %d", len(errs), maxErrors)
	}
	for _, e := range errs {
		if e.Offset < 0 || e.Offset > n {
			t.Fatalf("a problem reported at offset %d, outside the input of %d bytes", e.Offset, n)
		}
	}
}

// checkBounded walks a tree iteratively — it is checking a depth bound, so it
// must not need the stack the bound protects.
func checkBounded(t *testing.T, vals []ComponentValue) {
	t.Helper()
	type frame struct {
		vals  []ComponentValue
		depth int
	}
	stack := []frame{{vals, 0}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		// One past the cap: the block that was refused is kept as an empty node
		// so the cut is visible, and it holds nothing.
		if f.depth > maxNestingDepth+1 {
			t.Fatalf("a tree nested %d deep, past the cap of %d", f.depth, maxNestingDepth)
		}
		for _, v := range f.vals {
			if len(v.Values) > 0 && v.IsToken() {
				t.Fatalf("a preserved token carrying %d children", len(v.Values))
			}
			if len(v.Values) > 0 {
				stack = append(stack, frame{v.Values, f.depth + 1})
			}
		}
	}
}
