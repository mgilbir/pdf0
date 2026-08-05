package html

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// The named character reference table.
//
// entities.go is generated from the standard's own entities.json and committed,
// while the input is not — the arrangement the font tables had. That buys a
// checkout with no network and costs one thing: nothing would notice the
// committed table drifting from the standard it claims to be.
//
// So the drift check is here, and it runs whenever the input is on disk (`make
// html-entities`). It is a real external check: the expectations are the
// standard's, not this repository's.

const entitiesJSON = "../testdata/html/entities.json"

// TestEntityTableMatchesTheStandard compares the generated table with the file
// it was generated from, entry for entry in both directions.
func TestEntityTableMatchesTheStandard(t *testing.T) {
	raw, err := os.ReadFile(entitiesJSON)
	if err != nil {
		t.Skipf("run `make html-entities` to check the table against the standard (%v)", err)
	}
	var table map[string]struct {
		Characters string `json:"characters"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("parsing %s: %v", entitiesJSON, err)
	}
	if len(table) == 0 {
		t.Fatal("the standard's file holds no entities")
	}

	for name, want := range table {
		key := strings.TrimPrefix(name, "&")
		got, ok := namedEntities[key]
		if !ok {
			t.Errorf("the table is missing %q, which the standard defines", name)
			continue
		}
		if got != want.Characters {
			t.Errorf("%q is %q in the table and %q in the standard", name, got, want.Characters)
		}
	}
	// And nothing invented: an entity here that the standard does not have would
	// resolve text a browser leaves alone.
	for key := range namedEntities {
		if _, ok := table["&"+key]; !ok {
			t.Errorf("the table holds %q, which the standard does not define", key)
		}
	}
	if len(namedEntities) != len(table) {
		t.Errorf("the table holds %d entities and the standard %d",
			len(namedEntities), len(table))
	}
}

// TestEntityTableIsUsable pins the invariants the tokenizer relies on, which
// hold whether or not the standard's file is present.
func TestEntityTableIsUsable(t *testing.T) {
	if len(namedEntities) < 2000 {
		t.Fatalf("the table holds %d entities, far fewer than the standard's set",
			len(namedEntities))
	}

	longest, withSemi, without := 0, 0, 0
	for name, text := range namedEntities {
		if len(name) > longest {
			longest = len(name)
		}
		if strings.HasSuffix(name, ";") {
			withSemi++
		} else {
			without++
		}
		if text == "" {
			t.Errorf("%q stands for nothing", name)
		}
		if !utf8.ValidString(text) {
			t.Errorf("%q stands for text that is not valid UTF-8", name)
		}
	}

	// maxEntityNameLen bounds the tokenizer's lookahead. If it were short, the
	// longest names would silently stop resolving.
	if longest != maxEntityNameLen {
		t.Errorf("the longest name is %d bytes and maxEntityNameLen is %d; "+
			"the tokenizer would stop looking before reaching it", longest, maxEntityNameLen)
	}
	// Both spellings have to be present: the semicolon-less ones are what
	// longestLegacyName consults to tell a literal ampersand from the one place
	// this engine and a browser would differ.
	if withSemi == 0 || without == 0 {
		t.Errorf("the table holds %d names with a semicolon and %d without; "+
			"both are needed", withSemi, without)
	}

	// A spot check of the shapes that matter, so a table that was regenerated
	// into a different key convention fails here rather than in every document.
	for name, want := range map[string]string{
		"amp;":  "&",
		"lt;":   "<",
		"gt;":   ">",
		"quot;": `"`,
		"amp":   "&", // the legacy spelling, kept apart from "amp;"
		"lt":    "<",
	} {
		if got := namedEntities[name]; got != want {
			t.Errorf("namedEntities[%q] is %q, want %q", name, got, want)
		}
	}
}
