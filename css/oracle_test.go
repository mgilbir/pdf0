package css

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// The css package against an external oracle: the CSS parsing tests, written by
// Simon Sapin, dedicated to the public domain under CC0, and used as the
// conformance suite by tinycss2 (Python), rust-cssparser (Rust, and so Servo)
// and Crass (Ruby).
//
// # Why this and not more tests of our own
//
// docs/adr/0003-arlington-as-parser-oracle.md records two attempts this
// repository made at a guard that guarded nothing — one that tested pdf0's own
// trivial output, one whose consistency check was tautological — and the lesson
// it drew: a check built from the same understanding as the thing it checks
// cannot find a misunderstanding. The tokenizer and parser tests next door are
// worth having and they have that exact weakness. They asserts that the code
// does what its author read the specification to say.
//
// These expectations were written by someone else, from the specification, and
// three independent implementations are held to them. So a disagreement here is
// evidence about pdf0.
//
// # How our results are expressed in the suite's notation
//
// The suite writes an AST as nested JSON arrays, described in its README. Two
// things need saying about the translation, because a projection that quietly
// rewrites a result until it matches would be the tautology again.
//
// First, the suite models a *parse error as a node in the stream* — tinycss2
// returns them inline — while pdf0 keeps tokens and diagnostics apart, tokens in
// the tree and problems in an Errors slice. Its nine error spellings split
// cleanly in two, and the split is the README's, not one invented here:
//
//   - "bad-string", "bad-url", ")", "]" and "}" are *nodes*. The README defines
//     each as the representation of a token — a <bad-string-token>, a
//     <bad-url-token>, an unmatched close delimiter — so each maps to the token
//     pdf0 produces, and each is compared.
//   - "invalid", "eof-in-string", "eof-in-url", "empty" and "extra-input" are
//     *diagnostics*. They stand for nothing in the value stream; they say the
//     input was malformed. These are dropped from the expected node sequence and
//     checked against pdf0's Errors slice instead — by presence, not by message,
//     because matching our wording to tinycss2's would be a hand-written table
//     that could be tuned until it passed.
//
// Second, nothing else is adjusted. Where pdf0 disagrees with the suite the
// case is listed in deviations, with the reason, and the reason has to be about
// the specification rather than about this code.

// oracleEnv is the environment variable `make css-tests` sets.
const oracleEnv = "CSS_PARSING_TESTS"

// diagnostics are the suite's error spellings that stand for a problem rather
// than for a node. See the note above.
var diagnostics = map[string]bool{
	"invalid":       true,
	"eof-in-string": true,
	"eof-in-url":    true,
	"empty":         true,
	"extra-input":   true,
}

// The suite is built on the 2021 Candidate Recommendation draft of CSS Syntax
// Level 3, and the specification has moved since. Where the two disagree pdf0
// follows the current text: a browser today does what the current text says, and
// matching a superseded draft would put pdf0 alone.
//
// Each disagreement is excused by a *rule* naming the construct that was
// removed, rather than by a list of inputs. That is deliberate. A list keyed on
// input strings excuses whatever those inputs happen to produce, so a real
// regression in an excused case would go unnoticed; a rule keyed on the
// superseded construct excuses only cases that actually exercise it, and stops
// applying by itself if the suite is ever regenerated against the current text.
//
// deviationRules is checked against the *expected* result, so the question asked
// is "does this case test something the specification no longer has", never
// "did pdf0 fail here".
var deviationRules = []struct {
	name string
	why  string
	// applies reports whether an expected result exercises the construct.
	applies func(any) bool
}{
	{
		name: "unicode-range is no longer a token",
		// The tokenizer produces <unicode-range-token> only when its "unicode
		// ranges allowed" flag is set, and §4.3.14 notes the algorithm "is not
		// produced by the tokenizer under normal circumstances". The production
		// moved to the value layer (§5.5.11), reached only from the
		// unicode-range descriptor of @font-face — which is not in the subset
		// this engine implements. So "U+1?" is an ident, a delimiter and a
		// number, which is what pdf0 produces.
		why: "the current specification tokenizes U+1? as ident, delim and number",
		applies: func(v any) bool {
			return containsNode(v, func(arr []any) bool {
				tag, ok := arr[0].(string)
				return ok && tag == "unicode-range"
			})
		},
	},
	{
		name: "the attribute-match tokens were removed",
		// <include-match-token> and its five siblings — ~= |= ^= $= *= and the
		// column token || — are absent from the token list in §4. Selectors
		// Level 4 parses each from the two delimiters it is written with, so
		// "^=" is a "^" and an "=", which is what pdf0 produces.
		why: "~= |= ^= $= *= and || are two delimiters, not one token",
		applies: func(v any) bool {
			return containsString(v, func(s string) bool {
				switch s {
				case "~=", "|=", "^=", "$=", "*=", "||":
					return true
				}
				return false
			})
		},
	},
	{
		name: "the C1 controls are not ident code points",
		// The draft this suite was built on admitted every code point at or
		// above U+0080 into a name. The current definition of "non-ASCII ident
		// code point" is an explicit list that begins at U+00B7 and leaves out
		// the C1 controls, the bidirectional formatting characters, the private
		// use areas and the non-characters. pdf0 implements that list, so
		// U+0080 is a delimiter rather than part of an identifier.
		why: "non-ASCII ident code points are an explicit list starting at U+00B7",
		applies: func(v any) bool {
			return containsNode(v, func(arr []any) bool {
				tag, _ := arr[0].(string)
				if tag != "ident" && tag != "at-keyword" && tag != "hash" {
					return false
				}
				name, ok := arr[1].(string)
				if !ok {
					return false
				}
				for _, r := range name {
					if r >= 0x80 && r <= 0x9F {
						return true
					}
				}
				return false
			})
		},
	},
}

// containsNode reports whether any array node anywhere in an expected result
// satisfies pred.
func containsNode(v any, pred func([]any) bool) bool {
	arr, ok := v.([]any)
	if !ok {
		return false
	}
	if len(arr) > 1 {
		if _, isStr := arr[0].(string); isStr && pred(arr) {
			return true
		}
	}
	for _, e := range arr {
		if containsNode(e, pred) {
			return true
		}
	}
	return false
}

// containsString reports whether any bare string anywhere in an expected result
// satisfies pred. A bare string is how the notation writes a delimiter, so this
// is how a token that is no longer one is spotted.
func containsString(v any, pred func(string) bool) bool {
	switch t := v.(type) {
	case string:
		return pred(t)
	case []any:
		for _, e := range t {
			if containsString(e, pred) {
				return true
			}
		}
	}
	return false
}

// excuse returns the rule that lets a case through, if any.
func excuse(expected any) (name, why string, ok bool) {
	for _, r := range deviationRules {
		if r.applies(expected) {
			return r.name, r.why, true
		}
	}
	return "", "", false
}

func oracleDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(oracleEnv)
	if dir == "" {
		t.Skipf("set %s (or run `make test-css`) to check the parser against the CSS parsing tests", oracleEnv)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("%s=%s: %v", oracleEnv, dir, err)
	}
	return dir
}

// loadPairs reads one suite file, whose JSON is a flat array alternating input
// and expected result.
func loadPairs(t *testing.T, dir, name string) [][2]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	var flat []any
	if err := json.Unmarshal(raw, &flat); err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	if len(flat)%2 != 0 {
		t.Fatalf("%s holds %d entries, which is not a whole number of pairs", name, len(flat))
	}
	out := make([][2]any, 0, len(flat)/2)
	for i := 0; i < len(flat); i += 2 {
		out = append(out, [2]any{flat[i], flat[i+1]})
	}
	return out
}

// splitDiagnostics removes the suite's diagnostic markers from an expected
// result, returning what remains and how many were removed. It recurses, because
// a diagnostic can sit inside a block or a function.
func splitDiagnostics(v any) (any, int) {
	arr, ok := v.([]any)
	if !ok {
		return v, 0
	}
	// ["error", kind] is a marker; anything else is a node whose contents may
	// still hold markers.
	if len(arr) == 2 {
		if tag, ok := arr[0].(string); ok && tag == "error" {
			if kind, ok := arr[1].(string); ok && diagnostics[kind] {
				return nil, 1
			}
		}
	}
	out := make([]any, 0, len(arr))
	total := 0
	for _, e := range arr {
		kept, n := splitDiagnostics(e)
		total += n
		if n > 0 && kept == nil {
			continue
		}
		out = append(out, kept)
	}
	return out, total
}

// numberType is the suite's spelling of a numeric token's type flag.
func numberType(t Token) string {
	if t.IsInteger {
		return "integer"
	}
	return "number"
}

// oracleValue renders one component value in the suite's notation.
func oracleValue(c ComponentValue) any {
	t := c.Token
	switch t.Kind {
	case Ident:
		return []any{"ident", t.Value}
	case AtKeyword:
		return []any{"at-keyword", t.Value}
	case Hash:
		kind := "unrestricted"
		if t.IsID {
			kind = "id"
		}
		return []any{"hash", t.Value, kind}
	case String:
		return []any{"string", t.Value}
	case BadString:
		return []any{"error", "bad-string"}
	case URL:
		return []any{"url", t.Value}
	case BadURL:
		return []any{"error", "bad-url"}
	case Delim:
		return t.Value
	case Number:
		return []any{"number", t.Repr, t.Number, numberType(t)}
	case Percentage:
		return []any{"percentage", t.Repr, t.Number, numberType(t)}
	case Dimension:
		return []any{"dimension", t.Repr, t.Number, numberType(t), t.Unit}
	case Whitespace:
		return " "
	case CDO:
		return "<!--"
	case CDC:
		return "-->"
	case Colon:
		return ":"
	case Semicolon:
		return ";"
	case Comma:
		return ","
	case Function:
		return append([]any{"function", t.Value}, oracleValues(c.Values)...)
	case LeftParen:
		return append([]any{"()"}, oracleValues(c.Values)...)
	case LeftSquare:
		return append([]any{"[]"}, oracleValues(c.Values)...)
	case LeftBrace:
		return append([]any{"{}"}, oracleValues(c.Values)...)

	// An unmatched close delimiter is a preserved token here and an error node
	// there, which the README states outright.
	case RightParen:
		return []any{"error", ")"}
	case RightSquare:
		return []any{"error", "]"}
	case RightBrace:
		return []any{"error", "}"}
	}
	return fmt.Sprintf("<unrepresentable token kind %d>", t.Kind)
}

// oracleValues renders a list, always as a non-nil slice: the suite writes an
// empty list as [], which unmarshals to an empty slice and not to null.
func oracleValues(vals []ComponentValue) []any {
	out := make([]any, 0, len(vals))
	for _, v := range vals {
		out = append(out, oracleValue(v))
	}
	return out
}

func oracleRule(r Rule) any {
	if r.At {
		// The block slot is null for a statement at-rule such as @import, and a
		// list — possibly empty — for a block at-rule such as @media.
		var block any
		if r.HasBlock {
			block = oracleValues(r.Block)
		}
		return []any{"at-rule", r.Name, oracleValues(r.Prelude), block}
	}
	return []any{"qualified rule", oracleValues(r.Prelude), oracleValues(r.Block)}
}

func oracleDeclaration(d Declaration) any {
	return []any{"declaration", d.Name, oracleValues(d.Value), d.Important}
}

// run is one suite file: how to parse its inputs and how to render the result.
type run struct {
	file  string
	parse func(input string) (nodes []any, errs []Error)
}

func oracleRuns() []run {
	return []run{
		{"component_value_list.json", func(in string) ([]any, []Error) {
			vals, errs := ParseComponentValues(in)
			return oracleValues(vals), errs
		}},
		{"stylesheet.json", func(in string) ([]any, []Error) {
			rules, errs := ParseStylesheet(in)
			return renderRules(rules), errs
		}},
		{"rule_list.json", func(in string) ([]any, []Error) {
			rules, errs := ParseRules(in)
			return renderRules(rules), errs
		}},
		{"declaration_list.json", func(in string) ([]any, []Error) {
			decls, rules, errs := ParseDeclarations(in)
			return renderDeclarationList(decls, rules), errs
		}},
	}
}

// TestCSSOracleAnB checks the An+B microsyntax against the suite's file for it.
//
// It is separate from the runs above because its file has a different shape:
// the expected result is not an AST but a pair of integers, or null for input
// that is not an An+B at all. Roughly half its cases are the null ones, which is
// the half worth having — "3 n", "+ 2n" and "3.1n" all look like An+B values and
// are not, and each is a place where a reader that is merely permissive selects
// elements the author did not ask for.
func TestCSSOracleAnB(t *testing.T) {
	dir := oracleDir(t)

	pairs := loadPairs(t, dir, "An+B.json")
	if len(pairs) == 0 {
		t.Fatal("An+B.json holds no cases")
	}
	var valid, invalid int

	for _, pair := range pairs {
		input, ok := pair[0].(string)
		if !ok {
			t.Fatalf("an input that is not a string: %v", pair[0])
		}
		vals, _ := ParseComponentValues(input)
		got, gotOK := ParseAnB(vals)

		want, wantOK := pair[1].([]any)
		if !wantOK {
			// null: the input is not an An+B.
			invalid++
			if gotOK {
				t.Errorf("%q read as %dn%+d, and is not an An+B at all", input, got.A, got.B)
			}
			continue
		}
		valid++
		if len(want) != 2 {
			t.Fatalf("%q: expected result is not a pair: %v", input, want)
		}
		wantA, okA := want[0].(float64)
		wantB, okB := want[1].(float64)
		if !okA || !okB {
			t.Fatalf("%q: expected result is not two numbers: %v", input, want)
		}
		if !gotOK {
			t.Errorf("%q was rejected, and is the An+B %vn%+v", input, wantA, wantB)
			continue
		}
		if float64(got.A) != wantA || float64(got.B) != wantB {
			t.Errorf("%q read as %dn%+d, want %vn%+v", input, got.A, got.B, wantA, wantB)
		}
	}
	t.Logf("An+B.json: %d valid and %d invalid cases checked", valid, invalid)

	// A suite that had drifted to all-valid or all-invalid would still pass
	// every assertion above while checking almost nothing.
	if valid == 0 || invalid == 0 {
		t.Errorf("the suite gave %d valid and %d invalid cases; both are needed", valid, invalid)
	}
}

func renderRules(rules []Rule) []any {
	out := make([]any, 0, len(rules))
	for _, r := range rules {
		out = append(out, oracleRule(r))
	}
	return out
}

// renderDeclarationList puts declarations and at-rules back into source order.
//
// ParseDeclarations returns them separately, because an at-rule inside a
// declaration block is CSS Nesting and out of the implemented subset, so no
// caller in this engine wants them interleaved. The suite does compare order,
// and both carry the offset they began at, so the order is recoverable exactly
// rather than approximated.
func renderDeclarationList(decls []Declaration, rules []Rule) []any {
	type item struct {
		offset int
		value  any
	}
	items := make([]item, 0, len(decls)+len(rules))
	for _, d := range decls {
		items = append(items, item{d.Offset, oracleDeclaration(d)})
	}
	for _, r := range rules {
		items = append(items, item{r.Offset, oracleRule(r)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].offset < items[j].offset })

	out := make([]any, 0, len(items))
	for _, it := range items {
		out = append(out, it.value)
	}
	return out
}

func TestCSSOracle(t *testing.T) {
	dir := oracleDir(t)

	// How many cases each deviation rule accounted for, so that a rule which
	// excuses nothing can be caught and deleted.
	excusedBy := map[string]int{}

	for _, r := range oracleRuns() {
		t.Run(r.file, func(t *testing.T) {
			pairs := loadPairs(t, dir, r.file)
			if len(pairs) == 0 {
				t.Fatalf("%s holds no cases", r.file)
			}
			var checked, excused int

			for _, pair := range pairs {
				input, ok := pair[0].(string)
				if !ok {
					t.Fatalf("%s: an input that is not a string: %v", r.file, pair[0])
				}
				if name, _, ok := excuse(pair[1]); ok {
					excused++
					excusedBy[name]++
					continue
				}
				checked++

				want, wantErrs := splitDiagnostics(pair[1])
				got, errs := r.parse(input)

				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s\ninput %q\n got %s\nwant %s",
						r.file, input, mustJSON(got), mustJSON(want))
					continue
				}
				// The suite says the input was malformed, so pdf0 must have
				// noticed something.
				//
				// Only this direction is asserted. The converse — that pdf0 is
				// silent wherever the suite is — would be false, and not because
				// pdf0 is noisy: the suite's markers are the errors tinycss2
				// chose to put in the tree, while these are every parse error
				// the specification defines. An unterminated comment, a
				// backslash at end of input and an unclosed block are all parse
				// errors in §4 and §5, and the suite marks none of the three.
				// Requiring agreement would mean copying another parser's
				// reporting policy and calling it conformance. That pdf0 stays
				// quiet on correct input is asserted next door, over stylesheets
				// written to be correct, where it is a claim about pdf0 rather
				// than about tinycss2.
				if wantErrs > 0 && len(errs) == 0 {
					t.Errorf("%s\ninput %q\nparsed identically but reported nothing, "+
						"while the suite marks %d problem(s)", r.file, input, wantErrs)
				}
			}
			t.Logf("%s: %d cases checked, %d deliberately excused", r.file, checked, excused)
		})
	}

	// A rule that excuses nothing has outlived its reason — the suite was
	// regenerated, or the rule never matched what its author thought. Either
	// way it must go, or the list becomes a place where exemptions accumulate
	// unread.
	for _, r := range deviationRules {
		if excusedBy[r.name] == 0 {
			t.Errorf("the deviation rule %q excused no case; the suite no longer "+
				"tests %s, so the rule should be removed", r.name, r.why)
		}
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// TestCSSOracleHasTeeth is the check on the check, on the model of
// TestArlingtonOracleHasTeeth. An oracle whose comparison silently accepts
// everything is worse than no oracle, because it reads as coverage.
//
// It plants three faults of the kinds that matter — a wrong value, a wrong
// structure, and a dropped node — and requires the comparison to reject each.
func TestCSSOracleHasTeeth(t *testing.T) {
	ident := func(name string) any { return []any{"ident", name} }

	cases := []struct {
		name  string
		got   any
		want  any
		equal bool
	}{
		{"identical", []any{ident("a")}, []any{ident("a")}, true},
		{"a different value", []any{ident("a")}, []any{ident("b")}, false},
		{"a different kind", []any{ident("a")}, []any{[]any{"string", "a"}}, false},
		{"a dropped node", []any{ident("a")}, []any{ident("a"), " "}, false},
		{"a flattened block", []any{[]any{"{}", ident("a")}}, []any{[]any{"{}"}}, false},
		{
			"an integer where a number was written",
			[]any{[]any{"number", "1.0", 1.0, "number"}},
			[]any{[]any{"number", "1.0", 1.0, "integer"}},
			false,
		},
		{
			"a number equal in value but not as written",
			[]any{[]any{"number", "1.0", 1.0, "number"}},
			[]any{[]any{"number", "1", 1.0, "number"}},
			false,
		},
	}
	for _, tc := range cases {
		if got := reflect.DeepEqual(tc.got, tc.want); got != tc.equal {
			t.Errorf("%s: comparison said equal=%v, want %v", tc.name, got, tc.equal)
		}
	}

	// And the diagnostic split must actually remove markers, or every
	// malformed-input case would compare against a stream pdf0 never produces.
	in := []any{ident("a"), []any{"error", "eof-in-string"}, []any{"error", "bad-url"}}
	kept, n := splitDiagnostics(in)
	if n != 1 {
		t.Errorf("splitDiagnostics removed %d markers, want 1", n)
	}
	want := []any{ident("a"), []any{"error", "bad-url"}}
	if !reflect.DeepEqual(kept, want) {
		t.Errorf("splitDiagnostics kept %s, want %s", mustJSON(kept), mustJSON(want))
	}
}

// TestCSSOracleDeviationsAreNarrow is the check on the exemptions. A rule that
// excused more than the construct it names would hide real failures behind a
// reason that sounds good, which is the failure mode a list of exemptions
// always has.
//
// Each rule is given a result that does *not* exercise its construct and must
// decline it, and one that does and must take it.
func TestCSSOracleDeviationsAreNarrow(t *testing.T) {
	ordinary := []any{
		[]any{"ident", "a"}, " ", ":", []any{"string", "b"},
		[]any{"function", "rgb", []any{"number", "1", 1.0, "integer"}},
		[]any{"{}", []any{"ident", "c"}}, "~", "=", "|",
	}
	for _, r := range deviationRules {
		if r.applies(ordinary) {
			t.Errorf("the rule %q excuses an ordinary result, so it would hide real failures", r.name)
		}
	}

	// And each rule takes the construct it is for, so that none is dead.
	exercises := []struct {
		rule     string
		expected any
	}{
		{"unicode-range is no longer a token", []any{[]any{"unicode-range", 16.0, 31.0}}},
		{"the attribute-match tokens were removed", []any{[]any{"[]", []any{"ident", "h"}, "^=", []any{"ident", "x"}}}},
		{"the C1 controls are not ident code points", []any{[]any{"ident", "a\u0080b"}}},
	}
	for _, tc := range exercises {
		name, _, ok := excuse(tc.expected)
		if !ok {
			t.Errorf("no rule excused %s, which tests a superseded construct", mustJSON(tc.expected))
			continue
		}
		if name != tc.rule {
			t.Errorf("%s was excused by %q, want %q", mustJSON(tc.expected), name, tc.rule)
		}
	}
}
