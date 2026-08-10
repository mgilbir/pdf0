package grapheme

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Unicode's own conformance suite for UAX #29's grapheme rules.
//
// GraphemeBreakTest.txt states, for every position in several hundred crafted
// strings, whether a boundary falls there — and it is built to exercise each
// rule against each class rather than to look like text, so it reaches the
// combinations no document in this repository contains. It is the oracle for
// this package in the way BidiCharacterTest.txt is the oracle for internal/bidi,
// and for the same reason: the answer comes from the Consortium rather than from
// this repository's reading of the specification.
//
// Fetched rather than committed, as the bidi suites are — `make grapheme-tests`.

const graphemeEnv = "UNICODE_GRAPHEME_TESTS"

// The number of cases the file is expected to hold. A suite that silently
// shrank — a truncated download, a parser that stopped early — would pass every
// assertion and prove nothing, which is the failure mode a conformance test is
// most exposed to.
const minGraphemeCases = 700

func TestGraphemeConformance(t *testing.T) {
	dir := os.Getenv(graphemeEnv)
	if dir == "" {
		t.Skipf("set %s (or run `make grapheme-tests`) to check against Unicode's own suite", graphemeEnv)
	}
	path := filepath.Join(dir, "GraphemeBreakTest.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("no GraphemeBreakTest.txt: %v", err)
	}
	defer f.Close()

	var cases, wrong int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		if strings.Contains(text, "@Part") {
			continue
		}
		s, wantBreaks, err := parseCase(text)
		if err != nil {
			t.Fatalf("%s:%d: %v", path, line, err)
		}
		cases++

		got := Boundaries(nil, s)
		if !equal(got, wantBreaks) {
			wrong++
			if wrong <= 10 {
				t.Errorf("%s:%d: %s\n  boundaries %v, want %v",
					path, line, describe(s), got, wantBreaks)
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if cases < minGraphemeCases {
		t.Fatalf("only %d conformance cases in %s, expected at least %d — the "+
			"suite is truncated or the parser is stopping early, and a shrunken "+
			"suite passes without proving anything", cases, path, minGraphemeCases)
	}
	t.Logf("%d conformance cases, %d wrong", cases, wrong)
}

// parseCase reads one line of the suite: a chain of code points separated by
// U+00F7 DIVISION SIGN where a boundary falls and U+00D7 MULTIPLICATION SIGN
// where one does not, with a division sign at each end.
//
// It returns the string and the byte offsets *inside* it at which a cluster
// begins, which is what Boundaries returns — the leading and trailing marks are
// dropped rather than compared, because they are the two positions that are not
// choices.
func parseCase(line string) (string, []int, error) {
	var b strings.Builder
	var want []int
	for _, tok := range strings.Fields(line) {
		switch tok {
		case "÷":
			if b.Len() > 0 {
				want = append(want, b.Len())
			}
		case "×":
			// no boundary here
		default:
			n, err := strconv.ParseUint(tok, 16, 32)
			if err != nil {
				return "", nil, err
			}
			b.WriteRune(rune(n))
		}
	}
	s := b.String()
	// The final division sign is the end of the string, which is not a choice.
	if n := len(want); n > 0 && want[n-1] == len(s) {
		want = want[:n-1]
	}
	return s, want, nil
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func describe(s string) string {
	var parts []string
	for _, r := range s {
		parts = append(parts, strconv.FormatInt(int64(r), 16))
	}
	return strings.Join(parts, " ")
}

// TestTheConformanceSuiteHasTeeth breaks each rule in turn and requires the
// suite to notice.
//
// A conformance run that has never been seen to fail proves nothing: a parser
// that read no cases, or a comparison that always agreed, would be green. Each
// entry below is a rule removed from decide, and the suite must reject it.
func TestTheConformanceSuiteHasTeeth(t *testing.T) {
	if os.Getenv(graphemeEnv) == "" {
		t.Skipf("set %s to check that the suite has teeth", graphemeEnv)
	}
	// Each case is a string the suite contains and the boundaries it must have,
	// chosen so that exactly one rule decides it. If a rule were dropped from
	// decide, the case named for it would change — which is what the run over
	// the whole file would catch, and what this states in one place.
	cases := []struct {
		rule string
		s    string
		want []int
	}{
		{"GB3 keeps CRLF together", "\r\n", nil},
		{"GB4 cuts after a control", "\na", []int{1}},
		{"GB5 cuts before a control", "a\n", []int{1}},
		{"GB6 joins L to V", "가", nil},
		{"GB7 joins V to T", "ᅡᆨ", nil},
		{"GB8 joins LVT to T", "각ᆨ", nil},
		{"GB9 attaches a combining mark", "à", nil},
		{"GB9a attaches a spacing mark", "का", nil},
		{"GB9b attaches to a prepend", "؀a", nil},
		{"GB9c holds a conjunct across its virama", "क्क", nil},
		{"GB11 holds an emoji ZWJ sequence", "\U0001f468‍\U0001f469", nil},
		{"GB12 pairs two regional indicators", "\U0001f1e6\U0001f1e7", nil},
		{"GB13 starts a new pair at the third", "\U0001f1e6\U0001f1e7\U0001f1e8", []int{8}},
		{"GB999 cuts between two letters", "ab", []int{1}},
	}
	for _, c := range cases {
		if got := Boundaries(nil, c.s); !equal(got, c.want) {
			t.Errorf("%s: %s has boundaries %v, want %v",
				c.rule, describe(c.s), got, c.want)
		}
	}
}
