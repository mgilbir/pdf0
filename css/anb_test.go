package css

import (
	"strconv"
	"strings"
	"testing"
)

// The An+B microsyntax, covering what the external suite in oracle_test.go does
// not. As with the parser tests, the division is measured rather than guessed:
// each case below was kept because planting the corresponding fault left every
// one of the suite's 128 cases passing.
//
// The suite is thorough about the *shapes* of an An+B — it has "3n + 1",
// "3n+ 1", "3N +1" and "3 n" — and has no case at all where a signless integer
// follows the "n" with nothing to join them. So the rule that "3n 1" is not an
// An+B is untested by it, and the difference matters: a reader that accepts it
// selects on a value the author did not write.
//
// It also stops at the syntax. What an An+B *selects* is not in it, and that is
// the half a layout engine actually runs.

func parseAnB(t *testing.T, input string) (AnB, bool) {
	t.Helper()
	vals, _ := ParseComponentValues(input)
	return ParseAnB(vals)
}

// TestAnBNeedsASignBeforeB pins the gap the suite leaves. After the "n", B must
// arrive either as a signed integer or as a sign and a signless one; a bare
// integer is two values rather than one An+B.
func TestAnBNeedsASignBeforeB(t *testing.T) {
	invalid := []string{
		"3n 1", "3n1", "n 1", "-n 1", "2n 3n", "3n 1 2",
		// The sign has to have something to sign.
		"3n +", "3n -", "3n + ", "n +", "3n + +1",
		// And a signed integer after a sign is a second sign.
		"3n + +1", "3n - -1",
	}
	for _, in := range invalid {
		if got, ok := parseAnB(t, in); ok {
			t.Errorf("%q read as %dn%+d, and is not an An+B", in, got.A, got.B)
		}
	}

	// The shapes that are valid, so the rule above is not simply refusing
	// everything.
	valid := map[string]AnB{
		"3n+1":   {3, 1},
		"3n +1":  {3, 1},
		"3n + 1": {3, 1},
		"3n-1":   {3, -1},
		"3n- 1":  {3, -1},
		"3n - 1": {3, -1},
	}
	for in, want := range valid {
		got, ok := parseAnB(t, in)
		if !ok {
			t.Errorf("%q was rejected, and is an An+B", in)
			continue
		}
		if got != want {
			t.Errorf("%q read as %dn%+d, want %dn%+d", in, got.A, got.B, want.A, want.B)
		}
	}
}

// TestAnBRejectsUnrepresentableCounts pins that a value too large to be an index
// is refused rather than wrapped. An index that has silently changed sign
// selects a different set of elements, which is the worst way to be wrong.
func TestAnBRejectsUnrepresentableCounts(t *testing.T) {
	for _, in := range []string{
		"99999999999999999999n",
		"n+99999999999999999999",
		"3n-99999999999999999999",
		"99999999999999999999",
		"1e40n",
	} {
		if got, ok := parseAnB(t, in); ok {
			t.Errorf("%q read as %dn%+d, and is past what an index can be", in, got.A, got.B)
		}
	}
}

// TestAnBMatches pins what an An+B selects, which the suite does not cover at
// all — it checks only that a value parses.
//
// The definition is "index = A×n + B for some integer n ≥ 0", and the two easy
// mistakes are both here: forgetting that n may not be negative, and reading a
// negative A as selecting nothing.
func TestAnBMatches(t *testing.T) {
	cases := []struct {
		anb     AnB
		matches []int // within 1..10
	}{
		{AnB{2, 1}, []int{1, 3, 5, 7, 9}},  // odd
		{AnB{2, 0}, []int{2, 4, 6, 8, 10}}, // even
		{AnB{0, 3}, []int{3}},              // a single index
		{AnB{1, 0}, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{AnB{3, 0}, []int{3, 6, 9}},
		{AnB{3, 1}, []int{1, 4, 7, 10}},
		// n may not be negative, so 2n+3 does not select 1.
		{AnB{2, 3}, []int{3, 5, 7, 9}},
		// A negative A counts down from B and then stops.
		{AnB{-1, 3}, []int{1, 2, 3}},
		{AnB{-2, 5}, []int{1, 3, 5}},
		// B past the end of any list selects nothing here.
		{AnB{0, 0}, nil},
		{AnB{0, -1}, nil},
		{AnB{-1, 0}, nil},
	}
	for _, tc := range cases {
		want := map[int]bool{}
		for _, i := range tc.matches {
			want[i] = true
		}
		var got []string
		for i := 1; i <= 10; i++ {
			if tc.anb.Matches(i) != want[i] {
				got = append(got, "index "+strconv.Itoa(i))
			}
		}
		if got != nil {
			t.Errorf("%dn%+d disagrees at %s (it selects %v)",
				tc.anb.A, tc.anb.B, strings.Join(got, ", "), selected(tc.anb, 10))
		}
	}

	// An index before the first is never selected, whatever the value.
	for _, a := range []AnB{{2, 1}, {1, 0}, {0, 1}, {-1, 3}} {
		for _, i := range []int{0, -1, -100} {
			if a.Matches(i) {
				t.Errorf("%dn%+d selects index %d, and there is no such element", a.A, a.B, i)
			}
		}
	}
}

// TestAnBMatchesIsNotALoop pins that matching an index is arithmetic rather than
// a walk. A hostile stylesheet may write :nth-child(1000000000n+1), and a layout
// engine asking "is this the third child" must not count to a billion to answer.
func TestAnBMatchesIsNotALoop(t *testing.T) {
	huge := AnB{1000000000, 1}
	if !huge.Matches(1) {
		t.Error("1000000000n+1 does not select the first child")
	}
	if huge.Matches(2) {
		t.Error("1000000000n+1 selects the second child")
	}
	if !huge.Matches(1000000001) {
		t.Error("1000000000n+1 does not select index 1000000001")
	}
}

func selected(a AnB, upTo int) []int {
	var out []int
	for i := 1; i <= upTo; i++ {
		if a.Matches(i) {
			out = append(out, i)
		}
	}
	return out
}
