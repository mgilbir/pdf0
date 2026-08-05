package css

import (
	"strings"
	"testing"
)

// The parser of CSS Syntax Level 3 §5.
//
// These tests cover what the external suite in oracle_test.go does not. That
// division is deliberate and worth stating: the suite is the authority on
// whether pdf0 reads CSS the way the specification says, because it was written
// by someone else, and these tests would carry no such authority if they
// duplicated it. What is here is the ground the suite leaves uncovered —
// measured, not guessed, by planting each fault and checking whether the suite
// noticed.
//
// The trailing-whitespace rule of §5.4.5 is the case that motivated saying so.
// Removing the trim leaves every one of the suite's declaration cases passing,
// because none of them writes a space before the semicolon.

// render is a compact rendering of a component value, for tables that should
// read as the thing being asserted.
func render(c ComponentValue) string {
	switch {
	case c.IsFunction():
		return c.Token.Value + "(" + renderAll(c.Values) + ")"
	case c.IsBlock():
		open := c.Token
		var l, r string
		switch open.Kind {
		case LeftBrace:
			l, r = "{", "}"
		case LeftSquare:
			l, r = "[", "]"
		default:
			l, r = "(", ")"
		}
		return l + renderAll(c.Values) + r
	}
	return brief(c.Token)
}

func renderAll(vals []ComponentValue) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, render(v))
	}
	return strings.Join(parts, " ")
}

// TestDeclarationValueIsTrimmed pins the last step of §5.4.5. Whitespace before
// the semicolon is not part of the value, and a value that keeps it compares
// unequal to the same value written without it — which is how a cascade that
// should have found two declarations identical finds them different.
func TestDeclarationValueIsTrimmed(t *testing.T) {
	cases := map[string]string{
		"a: b":            "ident(b)",
		"a: b ":           "ident(b)",
		"a: b\t\n ":       "ident(b)",
		"a:   b   ":       "ident(b)",
		"a: b c ":         "ident(b) ws ident(c)",
		"a: b  c\t":       "ident(b) ws ident(c)",
		"a:b !important ": "ident(b)",
		// Whitespace is trimmed from the front by the colon step, and from the
		// back by the trim; what is between two values stays, because it
		// separates the parts of a shorthand.
		"a:\t\n b\t\n c\t\n": "ident(b) ws ident(c)",
	}
	for input, want := range cases {
		decls, _, _ := ParseDeclarations(input)
		if len(decls) != 1 {
			t.Errorf("%q gave %d declarations, want 1", input, len(decls))
			continue
		}
		if got := renderAll(decls[0].Value); got != want {
			t.Errorf("%q\n  got  %s\n  want %s", input, got, want)
		}
	}
}

// TestDeclarationValueIsNeverEmptySlice pins that a declaration with nothing but
// whitespace after the colon has an empty value rather than a value of one
// space. "a: ;" declares nothing, and a cascade that saw a whitespace token
// would treat it as a value.
func TestDeclarationValueIsNeverEmptySlice(t *testing.T) {
	for _, input := range []string{"a:", "a: ", "a:\t\n ", "a:  !important"} {
		decls, _, _ := ParseDeclarations(input)
		if len(decls) != 1 {
			t.Errorf("%q gave %d declarations, want 1", input, len(decls))
			continue
		}
		if n := len(decls[0].Value); n != 0 {
			t.Errorf("%q has a value of %d component values (%s), want none",
				input, n, renderAll(decls[0].Value))
		}
	}
}

// TestImportant walks the "!important" rule of §5.4.5. The two tokens need not
// be adjacent and the word is matched without regard to case, so the shapes
// below are all the same declaration — and the near misses below them are not.
func TestImportant(t *testing.T) {
	important := []string{
		"a: b !important",
		"a: b!important",
		"a: b ! important",
		"a: b !IMPORTANT",
		"a: b !Important",
		"a: b\t!\timportant\t",
		"a: b !/* comment */important",
		`a: b !\69mportant`,
	}
	for _, input := range important {
		decls, _, _ := ParseDeclarations(input)
		if len(decls) != 1 {
			t.Errorf("%q gave %d declarations, want 1", input, len(decls))
			continue
		}
		if !decls[0].Important {
			t.Errorf("%q is not important, and should be", input)
		}
		if got := renderAll(decls[0].Value); got != "ident(b)" {
			t.Errorf("%q left the value as %s, want ident(b)", input, got)
		}
	}

	notImportant := []struct {
		input string
		value string
	}{
		{"a: b", "ident(b)"},
		// The space before the "!" survives: it is not trailing, because the
		// "!" is, and nothing removed the "!" because no "important" followed.
		{"a: b !", "ident(b) ws delim(!)"},
		{"a: b important", "ident(b) ws ident(important)"},
		{"a: b !importantly", "ident(b) ws delim(!) ident(importantly)"},
		{"a: b !important x", "ident(b) ws delim(!) ident(important) ws ident(x)"},
		// The "!" has to be a delimiter, not part of something else.
		{"a: b important!", "ident(b) ws ident(important) delim(!)"},
		{`a: b "!important"`, `ident(b) ws str(!important)`},
	}
	for _, tc := range notImportant {
		decls, _, _ := ParseDeclarations(tc.input)
		if len(decls) != 1 {
			t.Errorf("%q gave %d declarations, want 1", tc.input, len(decls))
			continue
		}
		if decls[0].Important {
			t.Errorf("%q is important, and should not be", tc.input)
		}
		if got := renderAll(decls[0].Value); got != tc.value {
			t.Errorf("%q\n  got  %s\n  want %s", tc.input, got, tc.value)
		}
	}
}

// TestDeclarationOffsets pins that a declaration remembers where it began.
// ParseDeclarations returns declarations and at-rules in separate slices, and
// the offset is the only thing that can put them back in source order — so a
// wrong one silently reorders a cascade.
func TestDeclarationOffsets(t *testing.T) {
	const input = "a: 1; bb: 2;\n  ccc: 3"
	decls, _, _ := ParseDeclarations(input)
	if len(decls) != 3 {
		t.Fatalf("got %d declarations, want 3", len(decls))
	}
	for _, want := range []struct {
		name   string
		offset int
	}{{"a", 0}, {"bb", 6}, {"ccc", 15}} {
		var found *Declaration
		for i := range decls {
			if decls[i].Name == want.name {
				found = &decls[i]
			}
		}
		if found == nil {
			t.Errorf("no declaration named %q", want.name)
			continue
		}
		if found.Offset != want.offset {
			t.Errorf("%q begins at %d, want %d", want.name, found.Offset, want.offset)
		}
		if got := input[found.Offset : found.Offset+len(want.name)]; got != want.name {
			t.Errorf("the offset of %q points at %q", want.name, got)
		}
	}
}

// TestRuleOffsetsAndOrder pins the same for rules, and that the two kinds
// interleave by offset the way they were written.
func TestRuleOffsetsAndOrder(t *testing.T) {
	const input = "a: 1; @media print { b: 2 } c: 3"
	decls, rules, _ := ParseDeclarations(input)
	if len(decls) != 2 || len(rules) != 1 {
		t.Fatalf("got %d declarations and %d rules, want 2 and 1", len(decls), len(rules))
	}
	if rules[0].Offset != 6 {
		t.Errorf("the at-rule begins at %d, want 6", rules[0].Offset)
	}
	// The at-rule sits between the two declarations, which is only visible
	// through the offsets.
	if !(decls[0].Offset < rules[0].Offset && rules[0].Offset < decls[1].Offset) {
		t.Errorf("offsets %d, %d, %d do not put the at-rule between the declarations",
			decls[0].Offset, rules[0].Offset, decls[1].Offset)
	}
}

// TestParseDeclarationValues reads a block that has already been parsed, which
// is the path every style rule takes: ParseStylesheet gives the block as
// component values, and re-tokenizing it would lose the nesting already worked
// out. It has to agree with reading the same text from source.
func TestParseDeclarationValues(t *testing.T) {
	const sheet = "a { color: red; margin: calc(1px + 2px) 0 !important }"
	rules, errs := ParseStylesheet(sheet)
	if len(errs) != 0 {
		t.Fatalf("parsing reported %v", errs)
	}
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}

	decls, _, errs := ParseDeclarationValues(rules[0].Block)
	if len(errs) != 0 {
		t.Fatalf("reading the block reported %v", errs)
	}
	if len(decls) != 2 {
		t.Fatalf("got %d declarations, want 2", len(decls))
	}
	if decls[0].Name != "color" || renderAll(decls[0].Value) != "ident(red)" {
		t.Errorf("first declaration is %q = %s", decls[0].Name, renderAll(decls[0].Value))
	}
	if decls[1].Name != "margin" || !decls[1].Important {
		t.Errorf("second declaration is %q, important=%v", decls[1].Name, decls[1].Important)
	}
	// The function survived the round through component values with its
	// arguments intact, which is the thing re-tokenizing would have broken.
	if got := renderAll(decls[1].Value); got != "calc(dim(1,px) ws delim(+) ws dim(2,px)) ws int(0)" {
		t.Errorf("margin is %s", got)
	}
}

// TestNestingIsBounded is the security property: a stylesheet is untrusted, and
// a few kilobytes of "(" must not exhaust the goroutine stack, which aborts the
// process in a way no recover can catch.
//
// The cap has to hold the reader in step as well as upright. A block dropped for
// being too deep is still consumed to its matching close, so what follows it
// parses as written — the assertions below are that the trailing rule survives,
// not merely that nothing crashed.
func TestNestingIsBounded(t *testing.T) {
	deep := maxNestingDepth * 8

	for _, tc := range []struct{ name, input string }{
		{"parentheses", strings.Repeat("(", deep) + strings.Repeat(")", deep)},
		{"brackets", strings.Repeat("[", deep) + strings.Repeat("]", deep)},
		{"braces", strings.Repeat("{", deep) + strings.Repeat("}", deep)},
		{"functions", strings.Repeat("f(", deep) + strings.Repeat(")", deep)},
		{"unclosed", strings.Repeat("(", deep)},
		{"alternating", strings.Repeat("([{", deep) + strings.Repeat("}])", deep)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vals, errs := ParseComponentValues(tc.input)
			if len(errs) == 0 {
				t.Errorf("nesting %d deep was read without a word about it", deep)
			}
			// Total: it returned, and it returned something.
			if len(vals) == 0 {
				t.Errorf("produced nothing at all")
			}
			// One past the cap, because the block that was refused is still
			// there as an empty node: the cut is visible in the tree rather
			// than leaving it looking as though the author wrote nothing.
			// What matters is that it holds nothing, so recursion stopped.
			if got := depthOf(vals); got > maxNestingDepth+1 {
				t.Errorf("built a tree %d deep, past the cap of %d", got, maxNestingDepth)
			}
		})
	}

	// What the cap refuses has to be *discarded*, not handed back to the frame
	// above. Returning without consuming would leave the contents in the stream
	// for an ancestor to re-read, which reattaches them at a shallower depth —
	// a tree the author did not write, built from input that was rejected for
	// being too deep. Balanced brackets hide this, because the counts still
	// work out; a marker inside the refused region does not.
	marker := strings.Repeat("(", deep) + " MARKER " + strings.Repeat(")", deep)
	vals, _ := ParseComponentValues(marker)
	if findIdent(vals, "MARKER") {
		t.Error("content of a block refused for depth was kept, reattached above the cap")
	}

	// The cap keeps the reader in step: a rule after an over-nested block is
	// still read correctly, rather than being swallowed by a block the parser
	// never found the end of.
	input := "x { a: " + strings.Repeat("(", deep) + strings.Repeat(")", deep) + " } y { b: 2 }"
	rules, _ := ParseStylesheet(input)
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2 — the reader lost its place after the deep block", len(rules))
	}
	decls, _, _ := ParseDeclarationValues(rules[1].Block)
	if len(decls) != 1 || decls[0].Name != "b" {
		t.Errorf("the rule after the deep block read as %v", decls)
	}
}

// findIdent reports whether an identifier of the given name appears anywhere in
// a tree.
func findIdent(vals []ComponentValue, name string) bool {
	for _, v := range vals {
		if v.IsToken() && v.Token.Kind == Ident && v.Token.Value == name {
			return true
		}
		if findIdent(v.Values, name) {
			return true
		}
	}
	return false
}

func depthOf(vals []ComponentValue) int {
	best := 0
	for _, v := range vals {
		if d := 1 + depthOf(v.Values); d > best {
			best = d
		}
	}
	return best
}

// TestParseIsTotal is the property that matters most for untrusted input: every
// byte sequence produces a result, and every one terminates. Nothing in CSS
// parsing is allowed to fail, so nothing here may panic or hang.
func TestParseIsTotal(t *testing.T) {
	inputs := []string{
		"", "{", "}", "(", ")", "[", "]", "@", "@media", "@media{", "a", "a{",
		"a{b", "a{b:", "a{b:c", ";", ":", "!important", "a:b!", "url(", `"`,
		"/*", "@media print{a{b:c", "}}}}", ")))", "a{}{}{}", "@{}", "@ {}",
		strings.Repeat("{", 5000),
		strings.Repeat("}", 5000),
		strings.Repeat("a{", 5000),
		strings.Repeat("@m ", 5000),
		strings.Repeat(";", 5000),
		strings.Repeat("a:b;", 5000),
		strings.Repeat("f(", 5000),
	}
	for _, in := range inputs {
		// Each entry point, because they recover differently.
		ParseComponentValues(in)
		ParseStylesheet(in)
		ParseRules(in)
		ParseDeclarations(in)

		vals, _ := ParseComponentValues(in)
		ParseDeclarationValues(vals)
	}
}

// TestErrorsAreBoundedInTheParser pins that the diagnostic budget the tokenizer
// keeps is shared with the parser rather than restarted by it. Without that, a
// file that trips the bound in each layer reports twice the cap, and the promise
// that a report is bounded is only half true.
func TestErrorsAreBoundedInTheParser(t *testing.T) {
	_, errs := ParseComponentValues(strings.Repeat("f(", maxErrors*4))
	if len(errs) > maxErrors+1 {
		t.Errorf("got %d errors, want at most %d and a note", len(errs), maxErrors)
	}
	if len(errs) == 0 {
		t.Fatal("an input of nothing but unclosed functions reported nothing")
	}
	if last := errs[len(errs)-1].Message; !strings.Contains(last, "not reported") {
		t.Errorf("the last entry is %q; a truncated list must say it was truncated", last)
	}
}

// TestWellFormedStylesheetParsesCleanly is the other side of recovery: a
// stylesheet that is correct produces the rules it declares and no diagnostics.
// A parser that warns about correct CSS trains its users to ignore it.
func TestWellFormedStylesheetParsesCleanly(t *testing.T) {
	const sheet = `
/* a stylesheet that is entirely correct */
@charset "utf-8";
@import url(other.css);
@media screen and (min-width: 30em) {
  a.link:hover > .child::before {
    content: "\201C";
    background: url(bg.png) no-repeat 50% 0;
    margin: -1.5em 0 .5em calc(100% - 2px);
    color: #0f0 !important;
  }
}
p, blockquote { font: 12pt/1.4 "Noto Sans", sans-serif; }
`
	rules, errs := ParseStylesheet(sheet)
	if len(errs) != 0 {
		t.Errorf("a well-formed stylesheet reported %d problems: %v", len(errs), errs)
	}
	if len(rules) != 4 {
		t.Fatalf("got %d rules, want 4", len(rules))
	}
	for i, want := range []struct {
		at       bool
		name     string
		hasBlock bool
	}{
		{true, "charset", false},
		{true, "import", false},
		{true, "media", true},
		{false, "", true},
	} {
		got := rules[i]
		if got.At != want.at || got.Name != want.name || got.HasBlock != want.hasBlock {
			t.Errorf("rule %d is at=%v name=%q hasBlock=%v, want at=%v name=%q hasBlock=%v",
				i, got.At, got.Name, got.HasBlock, want.at, want.name, want.hasBlock)
		}
	}

	// The @media block holds one style rule, read from the block that was
	// already parsed.
	inner, errs := ParseRulesFromValues(rules[2].Block)
	if len(errs) != 0 {
		t.Errorf("reading the @media block reported %v", errs)
	}
	if len(inner) != 1 || inner[0].At {
		t.Fatalf("the @media block holds %d rules, want 1 style rule", len(inner))
	}
	decls, _, _ := ParseDeclarationValues(inner[0].Block)
	if len(decls) != 4 {
		t.Errorf("the style rule holds %d declarations, want 4", len(decls))
	}
}

// TestUnclosedBlockKeepsWhatItHeld pins the recovery a truncated stylesheet
// depends on. A file whose last brace is missing is the commonest way a
// stylesheet is broken, and discarding the rule would throw away everything the
// author did write.
func TestUnclosedBlockKeepsWhatItHeld(t *testing.T) {
	rules, errs := ParseStylesheet("a { color: red; background: blue")
	if len(errs) == 0 {
		t.Error("an unclosed block was read without a word about it")
	}
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	decls, _, _ := ParseDeclarationValues(rules[0].Block)
	if len(decls) != 2 {
		t.Errorf("got %d declarations, want the 2 that were written", len(decls))
	}
}

// TestRuleWithNoBlockIsDropped pins the other half of §5.4.3. A selector whose
// "{" never arrives is not a rule, and keeping it would let a truncated file
// apply styles nobody wrote.
func TestRuleWithNoBlockIsDropped(t *testing.T) {
	rules, errs := ParseStylesheet("a { color: red } b, c")
	if len(errs) == 0 {
		t.Error("a rule with no block was read without a word about it")
	}
	if len(rules) != 1 {
		t.Errorf("got %d rules, want only the one that had a block", len(rules))
	}
}
