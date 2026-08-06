package bidi

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The bidirectional algorithm against its own conformance data.
//
// UAX #9 ships two test files, and between them they are the reason this package
// can claim to implement the algorithm rather than to approximate it. They are an
// external oracle in the sense ADR 0003 means: the cases and the expected levels
// were written by the Unicode Consortium from the specification, so a
// disagreement is evidence about this code and not a restatement of its own
// reading.
//
//   - BidiTest.txt states inputs as *classes* rather than as characters —
//     "R; ON; EN" — with the expected levels and visual order for each of the
//     three base directions it applies to. That is 770,000 cases, and it is
//     exhaustive over short sequences in a way no hand-written test is: every
//     combination of four classes is in there, including the ones nobody would
//     think to write.
//   - BidiCharacterTest.txt states inputs as real code points, which is what
//     exercises the two rules that depend on the characters themselves rather
//     than on their classes: N0's bracket pairing, and the canonical equivalence
//     of the two angle brackets.
//
// Both are fetched rather than committed — 15 MB, versioned by Unicode — so the
// tests skip when they are absent, exactly as the veraPDF corpus and the
// Arlington model do. `make test-bidi` fetches and runs them.

const bidiTestEnv = "UNICODE_BIDI_TESTS"

// The pass counts are ratchets. They are at the totals because the algorithm
// passes both suites entirely; a drop means a regression and the numbers are not
// to be lowered to make a red test green.
const (
	bidiTestCaseBaseline      = 770241
	bidiCharacterCaseBaseline = 91707
)

func bidiTestDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(bidiTestEnv)
	if dir == "" {
		t.Skipf("set %s (or run `make test-bidi`) to check the algorithm against "+
			"Unicode's own conformance data", bidiTestEnv)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("%s=%s: %v", bidiTestEnv, dir, err)
	}
	return dir
}

// representative is one character per Bidi_Class, for the suite that states its
// inputs as classes.
//
// The choice matters in one place: the other-neutral must not be a bracket, or
// every ON in 770,000 cases would go through rule N0 and the suite would be
// testing something other than what it says. An exclamation mark is not a
// bracket; a parenthesis is.
var representative = map[Class]rune{
	L: 'A', R: 0x05D0, AL: 0x0627,
	EN: '0', ES: '+', ET: '#', AN: 0x0660, CS: ',', NSM: 0x0300, BN: 0x00AD,
	B: 0x2029, S: '\t', WS: ' ', ON: '!',
	LRE: 0x202A, RLE: 0x202B, LRO: 0x202D, RLO: 0x202E, PDF: 0x202C,
	LRI: 0x2066, RLI: 0x2067, FSI: 0x2068, PDI: 0x2069,
}

var classByName = map[string]Class{
	"L": L, "R": R, "AL": AL, "EN": EN, "ES": ES, "ET": ET, "AN": AN, "CS": CS,
	"NSM": NSM, "BN": BN, "B": B, "S": S, "WS": WS, "ON": ON,
	"LRE": LRE, "RLE": RLE, "LRO": LRO, "RLO": RLO, "PDF": PDF,
	"LRI": LRI, "RLI": RLI, "FSI": FSI, "PDI": PDI,
}

// TestRepresentativesHaveTheirClass checks the table above against the generated
// one. A representative whose class is not the one it stands for would make
// every case using it test the wrong thing while still passing or failing
// plausibly, which is the worst available failure for an oracle.
func TestRepresentativesHaveTheirClass(t *testing.T) {
	if len(representative) != len(classByName) {
		t.Fatalf("%d representatives for %d classes", len(representative), len(classByName))
	}
	for name, class := range classByName {
		r, ok := representative[class]
		if !ok {
			t.Errorf("no representative character for %s", name)
			continue
		}
		if got := ClassOf(r); got != class {
			t.Errorf("U+%04X stands for %s but its Bidi_Class is %d", r, name, got)
		}
	}
	if _, _, isBracket := bracketOf(representative[ON]); isBracket {
		t.Error("the other-neutral representative is a bracket, so rule N0 would run " +
			"on every neutral in the suite")
	}
}

// runLevels resolves a whole input as a single line and returns the levels after
// L1 together with the visual order, which is what both suites state.
func runLevels(text []rune, dir Direction) (levels []uint8, order []int) {
	p := Resolve(text, dir)
	levels = p.LineLevels(0, len(text))
	return levels, Reorder(levels)
}

func TestBidiTestConformance(t *testing.T) {
	dir := bidiTestDir(t)
	path := filepath.Join(dir, "BidiTest.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%v (run `make bidi-tests`)", err)
	}
	defer f.Close()

	var (
		wantLevels []string // "x" for a character rule X9 removed
		wantOrder  []int
		cases, bad int
	)
	report := func(format string, args ...any) {
		bad++
		if bad <= 10 {
			t.Errorf(format, args...)
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		switch {
		case strings.HasPrefix(text, "@Levels:"):
			wantLevels = strings.Fields(strings.TrimPrefix(text, "@Levels:"))
			continue
		case strings.HasPrefix(text, "@Reorder:"):
			wantOrder = nil
			for _, f := range strings.Fields(strings.TrimPrefix(text, "@Reorder:")) {
				n, err := strconv.Atoi(f)
				if err != nil {
					t.Fatalf("%s:%d: %q is not an index", path, line, f)
				}
				wantOrder = append(wantOrder, n)
			}
			continue
		case strings.HasPrefix(text, "@"):
			t.Fatalf("%s:%d: unknown directive %q", path, line, text)
		}

		// "L RLE ON; 7" — the classes separated by spaces, then a semicolon and a
		// bit set of the base directions the expectations above apply to.
		semi := strings.LastIndexByte(text, ';')
		if semi < 0 {
			t.Fatalf("%s:%d: %q has no direction bit set", path, line, text)
		}
		bits, err := strconv.Atoi(strings.TrimSpace(text[semi+1:]))
		if err != nil {
			t.Fatalf("%s:%d: %q is not a bit set", path, line, text[semi+1:])
		}
		var input []rune
		for _, name := range strings.Fields(text[:semi]) {
			class, ok := classByName[strings.TrimSpace(name)]
			if !ok {
				t.Fatalf("%s:%d: unknown Bidi_Class %q", path, line, strings.TrimSpace(name))
			}
			input = append(input, representative[class])
		}
		if len(input) != len(wantLevels) {
			t.Fatalf("%s:%d: %d characters against %d expected levels",
				path, line, len(input), len(wantLevels))
		}

		for _, d := range []struct {
			bit int
			dir Direction
		}{{1, Auto}, {2, LeftToRight}, {4, RightToLeft}} {
			if bits&d.bit == 0 {
				continue
			}
			cases++
			gotLevels, gotOrder := runLevels(input, d.dir)
			if !levelsMatch(gotLevels, wantLevels) {
				report("%s:%d (%s, direction %d): levels %v, want %v",
					path, line, text[:semi], d.dir, gotLevels, wantLevels)
				continue
			}
			if got := visualOrder(gotOrder, wantLevels); !sameInts(got, wantOrder) {
				report("%s:%d (%s, direction %d): order %v, want %v",
					path, line, text[:semi], d.dir, got, wantOrder)
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	t.Logf("BidiTest.txt: %d cases, %d wrong", cases, bad)
	if bad > 0 {
		t.Errorf("%d cases disagree with Unicode's expectations", bad)
	}
	if cases < bidiTestCaseBaseline {
		t.Errorf("only %d cases ran, below the baseline of %d — the suite was not "+
			"read in full", cases, bidiTestCaseBaseline)
	}
}

func TestBidiCharacterTestConformance(t *testing.T) {
	dir := bidiTestDir(t)
	path := filepath.Join(dir, "BidiCharacterTest.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%v (run `make bidi-tests`)", err)
	}
	defer f.Close()

	cases, bad := 0, 0
	report := func(format string, args ...any) {
		bad++
		if bad <= 10 {
			t.Errorf(format, args...)
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		// codepoints; direction; paragraph level; levels; visual order
		fields := strings.Split(text, ";")
		if len(fields) != 5 {
			t.Fatalf("%s:%d: %d fields, want 5", path, line, len(fields))
		}
		var input []rune
		for _, cp := range strings.Fields(fields[0]) {
			v, err := strconv.ParseUint(cp, 16, 32)
			if err != nil {
				t.Fatalf("%s:%d: %q is not a code point", path, line, cp)
			}
			input = append(input, rune(v))
		}
		wantDir, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil {
			t.Fatalf("%s:%d: %q is not a direction", path, line, fields[1])
		}
		wantPara, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil {
			t.Fatalf("%s:%d: %q is not a paragraph level", path, line, fields[2])
		}
		wantLevels := strings.Fields(fields[3])
		var wantOrder []int
		for _, f := range strings.Fields(fields[4]) {
			n, err := strconv.Atoi(f)
			if err != nil {
				t.Fatalf("%s:%d: %q is not an index", path, line, f)
			}
			wantOrder = append(wantOrder, n)
		}

		dir := Auto
		switch wantDir {
		case 0:
			dir = LeftToRight
		case 1:
			dir = RightToLeft
		}
		cases++

		p := Resolve(input, dir)
		if int(p.Level) != wantPara {
			report("%s:%d: paragraph level %d, want %d", path, line, p.Level, wantPara)
			continue
		}
		levels := p.LineLevels(0, len(input))
		if !levelsMatch(levels, wantLevels) {
			report("%s:%d: levels %v, want %v", path, line, levels, wantLevels)
			continue
		}
		if got := visualOrder(Reorder(levels), wantLevels); !sameInts(got, wantOrder) {
			report("%s:%d: order %v, want %v", path, line, got, wantOrder)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	t.Logf("BidiCharacterTest.txt: %d cases, %d wrong", cases, bad)
	if bad > 0 {
		t.Errorf("%d cases disagree with Unicode's expectations", bad)
	}
	if cases < bidiCharacterCaseBaseline {
		t.Errorf("only %d cases ran, below the baseline of %d — the suite was not "+
			"read in full", cases, bidiCharacterCaseBaseline)
	}
}

// levelsMatch compares against the expectations, where "x" marks a character
// rule X9 removed and whose level is therefore not stated.
func levelsMatch(got []uint8, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i, w := range want {
		if w == "x" {
			continue
		}
		n, err := strconv.Atoi(w)
		if err != nil || int(got[i]) != n {
			return false
		}
	}
	return true
}

// visualOrder drops the characters the expectations do not order: the ones rule
// X9 removed, which both files mark with an "x" level.
func visualOrder(order []int, levels []string) []int {
	out := make([]int, 0, len(order))
	for _, i := range order {
		if i < len(levels) && levels[i] == "x" {
			continue
		}
		out = append(out, i)
	}
	return out
}

func sameInts(a, b []int) bool {
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
