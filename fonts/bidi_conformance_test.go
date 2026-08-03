package fonts

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Unicode's own conformance suite for the bidirectional algorithm.
//
// UAX #9 is a large algorithm whose failures are quiet: text comes out in an
// order that looks plausible to someone who does not read the script, and only a
// reader notices. Unicode publishes two exhaustive test files for exactly that
// problem, and they are the oracle here — the same relationship the PDF/A
// validator has with the veraPDF corpus. Nothing this package could invent as a
// test would be worth as much as half a million cases written by the people who
// wrote the specification.
//
// BidiTest.txt enumerates every combination of bidirectional character *classes*
// up to length four, with the resolved levels and the visual order for each
// paragraph direction. It carries no characters, so rule N0 — the paired-bracket
// rule, the one place the algorithm looks at characters — is out of its scope.
//
// BidiCharacterTest.txt is the complement: real character sequences, brackets
// included, again with levels and order.
//
// Both are fetched by `make bidi-tests` and are not committed.

// bidiTestDir finds the Unicode test files, and distinguishes "nobody fetched
// them" from "somebody pointed at the wrong place".
//
// The difference matters for the same reason it does in arlington_test.go, which
// this follows: a skip is indistinguishable from a pass in the output, so a
// mistyped path turns an oracle of six hundred thousand cases off silently and
// the run still looks green. Unset and absent is a skip, because a developer
// without the files should still be able to run the suite. Set and wrong is a
// failure, because somebody meant to run this and did not.
func bidiTestDir(t *testing.T) string {
	t.Helper()
	const marker = "BidiTest.txt"

	env := os.Getenv("UNICODE_BIDI_TESTS")
	dirs := []string{env}
	if env == "" {
		// The tests run with fonts/ as the working directory, and the data is
		// fetched into the repository's testdata alongside every other corpus.
		dirs = []string{"../testdata/unicode-bidi", "testdata/unicode-bidi"}
	}
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return dir
		}
	}
	if env == "" {
		t.Skip("Unicode bidi conformance data not present; run `make bidi-tests`")
	}
	t.Fatalf("UNICODE_BIDI_TESTS is set to %q, and there is no %s there.\n"+
		"It must name the directory holding BidiTest.txt and BidiCharacterTest.txt.\n"+
		"Failing rather than skipping: this is the oracle for UAX #9, and a skip\n"+
		"would report success having checked nothing.", env, marker)
	return ""
}

// bidiClassByName maps the short aliases the test files use onto the constants.
var bidiClassByName = map[string]bidiClass{
	"L": bidiL, "R": bidiR, "AL": bidiAL, "EN": bidiEN, "ES": bidiES,
	"ET": bidiET, "AN": bidiAN, "CS": bidiCS, "NSM": bidiNSM, "BN": bidiBN,
	"B": bidiB, "S": bidiS, "WS": bidiWS, "ON": bidiON,
	"LRE": bidiLRE, "RLE": bidiRLE, "LRO": bidiLRO, "RLO": bidiRLO, "PDF": bidiPDF,
	"LRI": bidiLRI, "RLI": bidiRLI, "FSI": bidiFSI, "PDI": bidiPDI,
}

// TestBidiConformanceClasses runs BidiTest.txt: every combination of
// bidirectional classes up to length four, against all three paragraph
// directions the file asks for.
func TestBidiConformanceClasses(t *testing.T) {
	path := filepath.Join(bidiTestDir(t), "BidiTest.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	var (
		wantLevels  []int // -1 where the file writes "x"
		wantOrder   []int
		cases, fail int
	)
	// The file states levels and order once and then lists every input they
	// apply to, so both are carried forward until the next directive.
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for line := 0; sc.Scan(); line++ {
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
			wantLevels = wantLevels[:0]
			for _, tok := range strings.Fields(text[len("@Levels:"):]) {
				if tok == "x" {
					wantLevels = append(wantLevels, -1)
					continue
				}
				n, err := strconv.Atoi(tok)
				if err != nil {
					t.Fatalf("%s:%d: bad level %q", path, line+1, tok)
				}
				wantLevels = append(wantLevels, n)
			}
			continue
		case strings.HasPrefix(text, "@Reorder:"):
			wantOrder = wantOrder[:0]
			for _, tok := range strings.Fields(text[len("@Reorder:"):]) {
				n, err := strconv.Atoi(tok)
				if err != nil {
					t.Fatalf("%s:%d: bad order %q", path, line+1, tok)
				}
				wantOrder = append(wantOrder, n)
			}
			continue
		case strings.HasPrefix(text, "@"):
			// The format reserves other directives for forward compatibility.
			continue
		}

		semi := strings.IndexByte(text, ';')
		if semi < 0 {
			t.Fatalf("%s:%d: data line with no bitset: %q", path, line+1, text)
		}
		names := strings.Fields(text[:semi])
		classes := make([]bidiClass, len(names))
		for i, name := range names {
			c, ok := bidiClassByName[name]
			if !ok {
				t.Fatalf("%s:%d: unknown bidi class %q", path, line+1, name)
			}
			classes[i] = c
		}
		bits, err := strconv.ParseUint(strings.TrimSpace(text[semi+1:]), 16, 8)
		if err != nil {
			t.Fatalf("%s:%d: bad bitset: %v", path, line+1, err)
		}

		// 1 = derive the direction from the text, 2 = force left-to-right,
		// 4 = force right-to-left.
		for _, d := range [...]struct {
			bit   uint64
			level int
		}{{1, -1}, {2, 0}, {4, 1}} {
			if bits&d.bit == 0 {
				continue
			}
			cases++
			p := bidiResolve(classes, nil, d.level)
			if why, ok := bidiCaseMatches(&p, wantLevels, wantOrder); !ok {
				fail++
				if fail <= 20 {
					t.Errorf("%s:%d [para %d] %s: %s", path, line+1, d.level, text[:semi], why)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if cases == 0 {
		t.Fatal("no cases were read: the file parsed to nothing")
	}
	t.Logf("BidiTest.txt: %d cases, %d failures (%.4f%% pass)",
		cases, fail, 100*float64(cases-fail)/float64(cases))
	if fail > 0 {
		t.Errorf("%d of %d cases failed", fail, cases)
	}
}

// TestBidiConformanceCharacters runs BidiCharacterTest.txt: real character
// sequences, which is what brings rule N0 and the paired brackets into scope.
func TestBidiConformanceCharacters(t *testing.T) {
	path := filepath.Join(bidiTestDir(t), "BidiCharacterTest.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	var cases, fail int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for line := 0; sc.Scan(); line++ {
		text := sc.Text()
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		// codepoints ; direction ; paragraph level ; levels ; visual order
		fields := strings.Split(text, ";")
		if len(fields) != 5 {
			t.Fatalf("%s:%d: %d fields, want 5", path, line+1, len(fields))
		}
		var runes []rune
		for _, tok := range strings.Fields(fields[0]) {
			cp, err := strconv.ParseUint(tok, 16, 32)
			if err != nil {
				t.Fatalf("%s:%d: bad code point %q", path, line+1, tok)
			}
			runes = append(runes, rune(cp))
		}
		// 0 = left-to-right, 1 = right-to-left, 2 = derive it.
		want := -1
		switch strings.TrimSpace(fields[1]) {
		case "0":
			want = 0
		case "1":
			want = 1
		case "2":
			want = -1
		default:
			t.Fatalf("%s:%d: unknown direction %q", path, line+1, fields[1])
		}
		wantPara, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil {
			t.Fatalf("%s:%d: bad paragraph level: %v", path, line+1, err)
		}
		var wantLevels []int
		for _, tok := range strings.Fields(fields[3]) {
			if tok == "x" {
				wantLevels = append(wantLevels, -1)
				continue
			}
			n, err := strconv.Atoi(tok)
			if err != nil {
				t.Fatalf("%s:%d: bad level %q", path, line+1, tok)
			}
			wantLevels = append(wantLevels, n)
		}
		var wantOrder []int
		for _, tok := range strings.Fields(fields[4]) {
			n, err := strconv.Atoi(tok)
			if err != nil {
				t.Fatalf("%s:%d: bad order %q", path, line+1, tok)
			}
			wantOrder = append(wantOrder, n)
		}

		classes := make([]bidiClass, len(runes))
		for i, r := range runes {
			classes[i] = bidiClassOf(r)
		}
		cases++
		p := bidiResolve(classes, runes, want)
		why, ok := bidiCaseMatches(&p, wantLevels, wantOrder)
		if ok && p.para != wantPara {
			why, ok = "paragraph level "+strconv.Itoa(p.para)+", want "+strconv.Itoa(wantPara), false
		}
		if !ok {
			fail++
			if fail <= 20 {
				t.Errorf("%s:%d [%s]: %s", path, line+1, fields[0], why)
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if cases == 0 {
		t.Fatal("no cases were read: the file parsed to nothing")
	}
	t.Logf("BidiCharacterTest.txt: %d cases, %d failures (%.4f%% pass)",
		cases, fail, 100*float64(cases-fail)/float64(cases))
	if fail > 0 {
		t.Errorf("%d of %d cases failed", fail, cases)
	}
}

// bidiCaseMatches compares a resolved paragraph against one expected result,
// returning a description of the first difference.
func bidiCaseMatches(p *bidiParagraph, wantLevels, wantOrder []int) (string, bool) {
	if len(p.levels) != len(wantLevels) {
		return "got " + itoas(p.levels) + " levels, want " + itoas(wantLevels), false
	}
	for i := range wantLevels {
		// A level of -1 is the file's "x": the character was removed by X9 and
		// has no level. Anything else must match exactly.
		if p.levels[i] != wantLevels[i] {
			return "levels " + itoas(p.levels) + ", want " + itoas(wantLevels), false
		}
	}
	got := p.bidiReorder()
	if len(got) != len(wantOrder) {
		return "order " + itoas(got) + ", want " + itoas(wantOrder), false
	}
	for i := range wantOrder {
		if got[i] != wantOrder[i] {
			return "order " + itoas(got) + ", want " + itoas(wantOrder), false
		}
	}
	return "", true
}

func itoas(a []int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range a {
		if i > 0 {
			b.WriteByte(' ')
		}
		if v < 0 {
			b.WriteByte('x')
			continue
		}
		b.WriteString(strconv.Itoa(v))
	}
	b.WriteByte(']')
	return b.String()
}
