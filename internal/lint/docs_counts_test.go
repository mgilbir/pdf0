package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestDocsQuoteNoDriftingCounts fails on a document that states how many
// validators, standards, options, checks, methods, fuzz targets or data sets
// there are. Every such count in the docs went stale: "59 checks" in four
// places when there were 61, validator counts of nine, ten and eleven, "around
// forty" skips when a fresh clone had 51, "eleven" options when there were
// twelve, "28 methods" when there were 40, and "four fuzz targets" of which two
// had moved to another module (audit 2026-09-22 C160, C165, C167). A count that
// matters is generated (TestGeneratedDocSections) or asserted by a test; one
// written into prose is only true on the day it was written.
//
// Measurements are not counts of the code — "2,907 corpus files", "about
// eight checks" collected the page tree before the run cache existed — and the
// nouns below do not match them.
func TestDocsQuoteNoDriftingCounts(t *testing.T) {
	root := testfiles.Root(t)
	files := []string{"doc.go"}
	for _, rel := range markdownFiles(t) {
		if !isHistorical(rel) {
			files = append(files, rel)
		}
	}
	checked := 0
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			checked++
			for _, m := range driftingCounts(line) {
				t.Errorf("%s:%d: %q states a count that nothing keeps true; generate it, test it, or drop the number", rel, i+1, m)
			}
		}
	}
	if checked < 1000 {
		t.Fatalf("scanned %d lines; the scan is looking in the wrong place", checked)
	}
}

var countPattern = func() *regexp.Regexp {
	number := `(?:\d[\d,]*|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|sixty|seventy|eighty|ninety|hundred|dozen)`
	noun := `(?:validators|conformance standards|standards|options|` + "`With\\*` options" + `|check functions|dispatched checks|checks dispatched|test functions|methods|fuzz targets|fuzzers|external data ?sets|data ?sets|datasets)`
	return regexp.MustCompile(`(?i)\b(?:around |roughly |about |~)?` + number + `(?:\s+[A-Za-z` + "`" + `*/-]+){0,2}?\s+` + noun + `\b`)
}()

// driftingCounts returns the counts of the code a line states.
func driftingCounts(line string) []string {
	var out []string
	for _, loc := range countPattern.FindAllStringIndex(line, -1) {
		m := line[loc[0]:loc[1]]
		// "PDF/UA-1 validators" and "/V 4 and 5 the methods" are a name and a
		// sentence, not counts.
		if loc[0] > 0 && strings.ContainsRune("-/.", rune(line[loc[0]-1])) {
			continue
		}
		if words := strings.Fields(m); len(words) > 2 && hasStopWord(words[1:len(words)-1]) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func hasStopWord(words []string) bool {
	for _, w := range words {
		switch strings.ToLower(w) {
		case "the", "a", "an", "of", "and", "or", "to", "in", "on", "for":
			return true
		}
	}
	return false
}
